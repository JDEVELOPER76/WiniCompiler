package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"wini/wini"
)

// flagLista acumula valores repetidos (ej. -so a.so -so b.so, o -agregar
// datos/ -agregar licencia.txt).
type flagLista []string

func (f *flagLista) String() string     { return strings.Join(*f, ",") }
func (f *flagLista) Set(v string) error { *f = append(*f, v); return nil }

// Flags que consumen un valor. Se usan para reordenar os.Args antes de
// flag.Parse y aceptar `winic archivo.wini -o salida` (orden indiferente).
var flagsConArg = map[string]bool{
	"-o":           true,
	"-so":          true,
	"-agregar":     true,
	"-linker":      true,
	"-runtime-obj": true,
	"-cc":          true,
	"-llvm":        true,
	"-asm":         true,
}

func main() {
	reordenarArgs()

	out := flag.String("o", "",
		"archivo de salida (ejecutable por defecto; .so con -shared; .ll con -llvm; .s con -asm). Si se omite, se usa la carpeta 'build/'")
	llvmOut := flag.String("llvm", "",
		"generar SOLO LLVM IR (.ll) en la ruta indicada, sin enlazar (mutuamente excluyente con -shared y -asm)")
	asmOut := flag.String("asm", "",
		"generar SOLO ensamblador (.s) en la ruta indicada, sin enlazar (mutuamente excluyente con -shared y -llvm)")
	version := flag.Bool("version", false, "mostrar la versión de winic")
	var sos flagLista
	flag.Var(&sos, "so", "biblioteca .so adicional a enlazar dinámicamente (repetible). Ya no se autodetectan: los módulos usan .a/.o")
	var agregados flagLista
	flag.Var(&agregados, "agregar", "archivo o carpeta extra a copiar junto al resultado, como dependencia (repetible)")
	linker := flag.String("linker", "", "enlazador (por defecto: clang, cc o gcc)")
	rtObj := flag.String("runtime-obj", "", "ruta a runtime.o (por defecto: se busca automáticamente)")
	ccBin := flag.String("cc", "", "compilador de C para el runtime (por defecto: clang, cc o gcc)")
	noRT := flag.Bool("no-runtime", false, "no incluir el runtime (útil si tu .so ya lo trae)")
	shared := flag.Bool("shared", false, "generar una biblioteca .so en vez de un ejecutable")
	flag.Parse()

	if *version {
		fmt.Println("Winic LInuxClang 0.0.1 [alpha]")
		return
	}

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "uso: winic archivo.wini [-o salida] [-shared] [-llvm salida.ll | -asm salida.s] [-so lib.so ...] [-agregar ruta ...]")
		os.Exit(2)
	}
	if *llvmOut != "" && *shared {
		fmt.Fprintln(os.Stderr, "-llvm y -shared son mutuamente excluyentes")
		os.Exit(2)
	}
	if *asmOut != "" && *shared {
		fmt.Fprintln(os.Stderr, "-asm y -shared son mutuamente excluyentes")
		os.Exit(2)
	}
	if *asmOut != "" && *llvmOut != "" {
		fmt.Fprintln(os.Stderr, "-asm y -llvm son mutuamente excluyentes")
		os.Exit(2)
	}

	input := flag.Arg(0)
	data, err := os.ReadFile(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error leyendo %s: %v\n", input, err)
		os.Exit(1)
	}

	// ---- 1) Wini -> LLVM IR (siempre) ----
	// De paso, wini detecta qué bibliotecas estáticas (.a/.o) hacen
	// falta: cualquier módulo importado (o el propio archivo de entrada)
	// que declare 'externa' arrastra las .a/.o que estén en su misma
	// carpeta, sin necesidad de pasarlas a mano. Se enlazan directo
	// (quedan embebidas en el binario, sin rpath). Las .so, en cambio,
	// solo se usan si el usuario las pasa a mano con -so.
	llvm, libsAuto, err := wini.CompileFileToLLVM(string(data), input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error compilando: %v\n", err)
		os.Exit(1)
	}

	base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	dirFuente := filepath.Dir(input)

	// ---- Carpeta de salida ----
	// Si el usuario no da -o, todo se dirige a 'build/' (creada si hace
	// falta) junto al archivo fuente, en vez de ensuciar esa carpeta.
	buildDir := filepath.Join(dirFuente, "build")

	// ---- Modo "solo .ll": explícito, con -llvm <ruta> ----
	if *llvmOut != "" {
		output := *llvmOut
		if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error creando %s: %v\n", filepath.Dir(output), err)
			os.Exit(1)
		}
		if err := os.WriteFile(output, []byte(llvm), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error escribiendo %s: %v\n", output, err)
			os.Exit(1)
		}
		if err := copiarAgregados(agregados, filepath.Dir(output)); err != nil {
			fmt.Fprintf(os.Stderr, "error copiando -agregar: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(output)
		return
	}

	// ---- Modo "solo ensamblador": con -asm <ruta> ----
	// El IR ya lo tenemos; solo hay que bajarlo a asm con el mismo
	// compilador que usaríamos para enlazar (clang/cc/gcc entienden -S
	// y generan .s a partir del .ll). Se usa el binario elegido con
	// -linker (o el primero de clang/cc/gcc disponible) porque cualquiera
	// de ellos sirve tanto para -S como para enlazar.
	if *asmOut != "" {
		output := *asmOut
		outDirAsm := filepath.Dir(output)
		if err := os.MkdirAll(outDirAsm, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error creando %s: %v\n", outDirAsm, err)
			os.Exit(1)
		}

		// .ll intermedio (se borra al terminar). Va junto al .s de salida.
		llPath := output + ".ll"
		if err := os.WriteFile(llPath, []byte(llvm), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error escribiendo %s: %v\n", llPath, err)
			os.Exit(1)
		}
		defer os.Remove(llPath)

		cc := elegirBinario(*linker, "compilador")
		if err := correr(cc, "-Wno-override-module", "-S", llPath, "-o", output); err != nil {
			fmt.Fprintf(os.Stderr, "error generando ensamblador: %v\n", err)
			os.Exit(1)
		}

		if err := copiarAgregados(agregados, outDirAsm); err != nil {
			fmt.Fprintf(os.Stderr, "error copiando -agregar: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(output)
		return
	}

	// ---- Modo "enlazar" (default): ejecutable, o .so con -shared ----
	// Ya no hace falta ninguna .so para entrar acá: `winic main.wn -o app`
	// produce directamente un ejecutable. Las .a/.o detectadas por
	// 'externa' (libsAuto) se agregan solas; las .so pasadas con -so se
	// copian a core/ y se enlazan con rpath relativo.
	//
	// hayDinamicas indica si hace falta cargar algo en tiempo de
	// ejecución (.so pasadas con -so): si las hay, esas .so se guardan
	// en '<carpeta del resultado>/core/' y el ejecutable (o la .so que
	// genera este mismo comando con -shared) queda a la misma altura,
	// con rpath relativo ($ORIGIN/core) para que esa carpeta sea
	// portable (se puede mover o copiar entera, con -o o sin él).
	hayDinamicas := len(sos) > 0
	usaCore := hayDinamicas

	var outDir string
	var outPath string
	if *out != "" {
		outPath = *out
		outDir = filepath.Dir(outPath)
	} else {
		outDir = buildDir
		if *shared {
			outPath = filepath.Join(outDir, base+".so")
		} else {
			outPath = filepath.Join(outDir, base)
		}
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creando %s: %v\n", outDir, err)
		os.Exit(1)
	}
	coreDir := filepath.Join(outDir, "core")
	if usaCore {
		if err := os.MkdirAll(coreDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error creando %s: %v\n", coreDir, err)
			os.Exit(1)
		}
	}

	// .ll intermedio (se borra al terminar).
	llPath := outPath + ".ll"
	if err := os.WriteFile(llPath, []byte(llvm), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error escribiendo %s: %v\n", llPath, err)
		os.Exit(1)
	}
	defer os.Remove(llPath)

	// ---- 2) runtime.o (con -fPIC siempre, sirve para ejecutable y para .so) ----
	var rtPath string
	if !*noRT {
		rtPath, err = obtenerRuntimeObj(dirFuente, *rtObj, *ccBin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error con el runtime: %v\n", err)
			os.Exit(1)
		}
	}

	link := elegirBinario(*linker, "enlazador")

	// ---- 3) .ll -> .o con -fPIC (necesario para poder armar un .so) ----
	objTmp := llPath + ".o"
	defer os.Remove(objTmp)
	if err := correr(link, "-Wno-override-module", "-c", "-fPIC", llPath, "-o", objTmp); err != nil {
		fmt.Fprintf(os.Stderr, "error compilando %s a .o: %v\n", llPath, err)
		os.Exit(1)
	}

	// ---- 4) Enlazar: .o + runtime.o + .a/.o automáticas + .so extra -> .so o ejecutable ----
	args := []string{}
	if *shared {
		args = append(args, "-shared", "-fPIC")
		// soname: cómo se referirá a esta lib quien la consuma.
		args = append(args, "-Wl,-soname,"+filepath.Base(outPath))
	}
	args = append(args, objTmp)
	if rtPath != "" {
		args = append(args, rtPath)
	}

	// Bibliotecas estáticas (.a/.o) detectadas automáticamente: se pasan
	// tal cual al enlazador, sin rpath, porque quedan embebidas en el
	// resultado (no generan una dependencia en tiempo de ejecución).
	for _, lib := range libsAuto {
		args = append(args, lib)
	}

	// Bibliotecas dinámicas (.so) pasadas a mano con -so: se copian a
	// 'core/' (junto al resultado) y se enlazan con -L/-l: (en vez de la
	// ruta completa) para que en el binario quede grabado solo el
	// nombre del archivo como dependencia (DT_NEEDED), no una ruta; si
	// el .so no trae 'soname' propio, pasarle la ruta completa al
	// enlazador hace que esa ruta completa quede grabada tal cual, y
	// entonces el resultado solo funciona ejecutado desde ese mismo
	// directorio. Con rpath relativo ($ORIGIN/core), el resultado y sus
	// .so viajan juntos aunque muevas la carpeta o lo ejecutes desde
	// otro lado.
	coreDirAbs, err := filepath.Abs(coreDir)
	if err != nil {
		coreDirAbs = coreDir
	}
	for _, so := range sos {
		abs, err := filepath.Abs(so)
		if err != nil {
			abs = so
		}
		if _, err := os.Stat(abs); err != nil {
			fmt.Fprintf(os.Stderr, "no encontré el .so '%s': %v\n", so, err)
			os.Exit(1)
		}
		nombre := filepath.Base(abs)
		destino := filepath.Join(coreDir, nombre)
		if err := copiarArchivo(abs, destino); err != nil {
			fmt.Fprintf(os.Stderr, "error copiando '%s' a '%s': %v\n", abs, destino, err)
			os.Exit(1)
		}
		args = append(args, "-L"+coreDirAbs, "-l:"+nombre)
	}
	if usaCore {
		args = append(args, "-Wl,-rpath,$ORIGIN/core")
	}
	args = append(args, "-o", outPath)

	if err := correr(link, args...); err != nil {
		fmt.Fprintf(os.Stderr, "error enlazando: %v\n", err)
		os.Exit(1)
	}

	if err := copiarAgregados(agregados, outDir); err != nil {
		fmt.Fprintf(os.Stderr, "error copiando -agregar: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(outPath)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// reordenarArgs mueve los flags al principio, respetando los que toman valor.
func reordenarArgs() {
	raw := os.Args[1:]
	var flags, positional []string
	for i := 0; i < len(raw); i++ {
		arg := raw[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if flagsConArg[arg] && i+1 < len(raw) {
				i++
				flags = append(flags, raw[i])
			}
		} else {
			positional = append(positional, arg)
		}
	}
	os.Args = append([]string{os.Args[0]}, append(flags, positional...)...)
}

// elegirBinario devuelve user, o el primero de clang/cc/gcc disponible.
func elegirBinario(user, rol string) string {
	if user != "" {
		return user
	}
	for _, c := range []string{"clang", "cc", "gcc"} {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	fmt.Fprintf(os.Stderr, "no encontré %s (probé clang, cc, gcc); usá -linker <ruta>\n", rol)
	os.Exit(1)
	return ""
}

// obtenerRuntimeObj devuelve la ruta a runtime.o:
//   - respeta -runtime-obj si se pasó (y existe);
//   - busca en sitios habituales;
//   - si no lo encuentra, compila runtime/runtime.c a un temporal con -fPIC.
func obtenerRuntimeObj(dirFuente, rtObjUser, ccUser string) (string, error) {
	if rtObjUser != "" {
		if _, err := os.Stat(rtObjUser); err != nil {
			return "", fmt.Errorf("no encontré el runtime.o indicado '%s': %w", rtObjUser, err)
		}
		return rtObjUser, nil
	}

	candidatos := []string{
		filepath.Join(dirFuente, "runtime.o"),
		filepath.Join(filepath.Dir(os.Args[0]), "runtime.o"),
		filepath.Join(dirFuente, "runtime", "runtime.o"),
		filepath.Join(filepath.Dir(os.Args[0]), "runtime", "runtime.o"),
		filepath.Join(dirFuente, "..", "runtime.o"),
		"runtime.o",
	}
	for _, c := range candidatos {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs, nil
		}
	}

	runtimeC := buscarRuntimeC(dirFuente)
	if runtimeC == "" {
		return "", fmt.Errorf("no encontré runtime.o ni runtime/runtime.c; compilalo a mano o pasá -runtime-obj <ruta>")
	}

	cc := elegirBinario(ccUser, "compilador de C")
	tmp, err := os.CreateTemp("", "winic-runtime-*.o")
	if err != nil {
		return "", err
	}
	tmp.Close()
	// -fPIC siempre: sirve para ejecutables y para .so.
	if err := correr(cc, "-O2", "-Wall", "-Wextra", "-fPIC", "-c", runtimeC, "-o", tmp.Name()); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("compilando %s: %w", runtimeC, err)
	}
	return tmp.Name(), nil
}

// buscarRuntimeC busca runtime/runtime.c en los sitios habituales.
func buscarRuntimeC(dirFuente string) string {
	candidatos := []string{
		filepath.Join(dirFuente, "runtime", "runtime.c"),
		filepath.Join(filepath.Dir(os.Args[0]), "runtime", "runtime.c"),
		filepath.Join(dirFuente, "..", "runtime", "runtime.c"),
		"runtime/runtime.c",
	}
	for _, c := range candidatos {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

// copiarAgregados copia cada ruta de 'rutas' (archivo o carpeta) dentro
// de 'destinoDir', preservando su nombre base. Una carpeta se copia
// recursivamente. Se usa para -agregar: cualquier dependencia extra que
// el usuario quiera que viaje junto al resultado (datos, otra .so no
// enlazada, assets, etc).
func copiarAgregados(rutas []string, destinoDir string) error {
	if len(rutas) == 0 {
		return nil
	}
	if err := os.MkdirAll(destinoDir, 0755); err != nil {
		return err
	}
	for _, ruta := range rutas {
		info, err := os.Stat(ruta)
		if err != nil {
			return fmt.Errorf("no encontré '%s': %w", ruta, err)
		}
		destino := filepath.Join(destinoDir, filepath.Base(ruta))
		if info.IsDir() {
			if err := copiarCarpeta(ruta, destino); err != nil {
				return err
			}
		} else {
			if err := copiarArchivo(ruta, destino); err != nil {
				return err
			}
		}
	}
	return nil
}

// copiarArchivo copia un único archivo, preservando el modo del original.
func copiarArchivo(origen, destino string) error {
	info, err := os.Stat(origen)
	if err != nil {
		return err
	}
	src, err := os.Open(origen)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(destino, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// copiarCarpeta copia recursivamente 'origen' a 'destino'.
func copiarCarpeta(origen, destino string) error {
	return filepath.Walk(origen, func(ruta string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(origen, ruta)
		if err != nil {
			return err
		}
		destinoRuta := filepath.Join(destino, rel)
		if info.IsDir() {
			return os.MkdirAll(destinoRuta, 0755)
		}
		return copiarArchivo(ruta, destinoRuta)
	})
}

func correr(nombre string, args ...string) error {
	cmd := exec.Command(nombre, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
