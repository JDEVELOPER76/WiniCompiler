package wini

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------
// Resolución de 'importar': soporte de módulos .wn
// ---------------------------------------------------------------------
//
// Un módulo .wn es un archivo Wini que solo declara estructuras y
// funciones en su nivel superior (una "biblioteca"): no tiene sentido
// ejecutable propio ni requiere 'principal'. 'importar nombre' (o
// 'importar "ruta"') localiza ese archivo, lo parsea, resuelve
// recursivamente sus propias importaciones, y trae sus declaraciones al
// espacio de nombres global del programa que importa. Las llamadas
// calificadas 'modulo.funcion()' se normalizan a esa misma función
// global después de expandir los módulos.

// resolverImportaciones expande, en el AST ya parseado de 'raiz', los
// nodos IMPORTAR de nivel superior. rutaEntrada es la ruta (si se conoce)
// del propio archivo que se está compilando, usada solo para detectar
// auto-importación; dirBase es el directorio desde el que se resuelven
// las rutas relativas de 'importar' en ese archivo.
//
// Además, de paso, detecta automáticamente qué bibliotecas nativas
// hacen falta para enlazar el programa: cualquier archivo/módulo que
// declare al menos una función 'externa' "reclama" todas las .a/.o que
// estén en su mismo directorio (ver detectarLibsEnDir). Así, 'importar
// sistema' ya no requiere que quien compila pase nada a mano: la
// biblioteca estática de ese módulo se agrega sola y se enlaza
// directamente (sin rpath, queda embebida en el binario). Las .so siguen
// soportadas, pero ya no se autodetectan: quien las necesite debe seguir
// pasándolas a mano con '-so' (ver main.go). Devuelve la lista de rutas
// absolutas .a/.o detectadas (sin duplicados, orden estable).
func resolverImportaciones(raiz *Nodo, dirBase string, rutaEntrada string) ([]string, error) {
	cargador := &cargadorModulos{
		cargados:   map[string]bool{},
		enProgreso: map[string]bool{},
		funciones:  map[string]bool{},
		libs:       map[string]bool{},
		aliasModulos: map[string]bool{},
	}
	if rutaEntrada != "" {
		if abs, err := filepath.Abs(rutaEntrada); err == nil {
			cargador.cargados[abs] = true
		}
	}
	// Pre-registrar las funciones ya declaradas en el archivo principal,
	// para detectar colisiones de nombre con las que se importen.
	for _, s := range raiz.Hijos {
		if s.Tipo == "FUNCION" || s.Tipo == "EXTERNA" {
			nombre, _ := s.Valor.(string)
			cargador.funciones[nombre] = true
		}
	}
	// El propio archivo de entrada puede declarar 'externa' sin pasar por
	// ningún módulo importado (p.ej. un .wini suelto que usa printf()):
	// también se le busca .a/.o en su directorio.
	cargador.detectarLibsEnDir(dirBase, raiz.Hijos)

	nuevosHijos, err := cargador.expandir(raiz.Hijos, dirBase)
	if err != nil {
		return nil, err
	}
	raiz.Hijos = nuevosHijos
	normalizarLlamadasDeModulo(raiz, cargador.aliasModulos)
	return cargador.listaLibs(), nil
}

type cargadorModulos struct {
	cargados   map[string]bool // rutas absolutas ya cargadas (evita duplicar en importaciones "diamante")
	enProgreso map[string]bool // rutas absolutas en proceso de carga (detecta importaciones circulares)
	funciones  map[string]bool // nombres de función ya vistos en todo el programa
	libs       map[string]bool // rutas absolutas .a/.o detectadas automáticamente
	aliasModulos map[string]bool // alias y nombres cortos usados para llamadas calificadas
}

// detectarLibsEnDir busca, dentro de 'dir', archivos '.a' u '.o' para
// agregarlos a la lista de bibliotecas a enlazar estáticamente — pero
// solo si 'sentencias' (el nivel superior de un archivo/módulo) declara
// al menos una función 'externa': si no hay ninguna 'externa', ese
// archivo no necesita ninguna biblioteca y no tiene sentido arrastrar lo
// que pueda estar suelto en el mismo directorio por otro motivo. Cada
// carpeta de módulo (como 'modulos/system/') es, por convención, dedicada
// a ese módulo, así que se toman todas las .a/.o que haya ahí: no hace
// falta que el nombre coincida con el del archivo .wn. Las .so
// deliberadamente NO se autodetectan acá: al ser dinámicas (necesitan
// rpath o vivir junto al ejecutable) quedan reservadas para cuando quien
// compila las pasa a mano con '-so'.
func (c *cargadorModulos) detectarLibsEnDir(dir string, sentencias []*Nodo) {
	tieneExterna := false
	for _, s := range sentencias {
		if s.Tipo == "EXTERNA" {
			tieneExterna = true
			break
		}
	}
	if !tieneExterna {
		return
	}

	entradas, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entradas {
		if e.IsDir() {
			continue
		}
		nombre := e.Name()
		if !strings.HasSuffix(nombre, ".a") && !strings.HasSuffix(nombre, ".o") {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(dir, nombre))
		if err != nil {
			continue
		}
		c.libs[abs] = true
	}
}

// listaLibs devuelve las rutas .a/.o detectadas, ordenadas para que la
// salida (y el orden en que se pasan al enlazador) sea determinista.
func (c *cargadorModulos) listaLibs() []string {
	lista := make([]string, 0, len(c.libs))
	for s := range c.libs {
		lista = append(lista, s)
	}
	sort.Strings(lista)
	return lista
}

// expandir procesa las sentencias de nivel superior de un archivo ya
// parseado (el principal o un módulo importado), sustituye cada IMPORTAR
// por las funciones del módulo referenciado (cargadas recursivamente), y
// devuelve la lista final de sentencias, sin nodos IMPORTAR.
func (c *cargadorModulos) expandir(sentencias []*Nodo, dirActual string) ([]*Nodo, error) {
	resultado := make([]*Nodo, 0, len(sentencias))
	for _, s := range sentencias {
		if s.Tipo != "IMPORTAR" {
			resultado = append(resultado, s)
			continue
		}

		nombreModulo, _ := s.Valor.(string)
		if len(s.Hijos) > 0 {
			if alias, ok := s.Hijos[0].Valor.(string); ok && alias != "" {
				c.aliasModulos[alias] = true
			}
		}
		if partes := strings.FieldsFunc(nombreModulo, func(r rune) bool { return r == '/' || r == '\\' || r == '.' }); len(partes) > 0 {
			nombre := partes[len(partes)-1]
			nombre = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(nombre, ".wn"), ".wini"), ".cwn")
			c.aliasModulos[nombre] = true
		}
		ruta, err := resolverRutaModulo(nombreModulo, dirActual)
		if err != nil {
			return nil, fmt.Errorf("línea %d: %v", s.Linea, err)
		}
		rutaAbs, err := filepath.Abs(ruta)
		if err != nil {
			return nil, fmt.Errorf("línea %d: no se pudo resolver la ruta de '%s': %v", s.Linea, nombreModulo, err)
		}

		if c.enProgreso[rutaAbs] {
			return nil, fmt.Errorf("línea %d: importación circular detectada al importar '%s' (%s)", s.Linea, nombreModulo, ruta)
		}
		if c.cargados[rutaAbs] {
			// Ya se cargó antes (p.ej. importado desde dos módulos
			// distintos): no duplicar sus funciones.
			continue
		}

		datos, err := os.ReadFile(rutaAbs)
		if err != nil {
			return nil, fmt.Errorf("línea %d: no se pudo leer el módulo '%s' (%s): %v", s.Linea, nombreModulo, ruta, err)
		}

		modRaiz, err := parsearModulo(string(datos), nombreModulo, ruta)
		if err != nil {
			return nil, fmt.Errorf("línea %d: %v", s.Linea, err)
		}

		// Un módulo .wn declara tipos y funciones (y sus propias
		// importaciones) en el nivel superior: es una biblioteca, no un
		// programa con lógica de nivel superior.
		for _, hijoMod := range modRaiz.Hijos {
			if hijoMod.Tipo != "FUNCION" && hijoMod.Tipo != "IMPORTAR" && hijoMod.Tipo != "EXTERNA" && hijoMod.Tipo != "ESTRUCTURA" {
				return nil, fmt.Errorf("el módulo '%s' (%s) solo puede declarar estructuras y funciones (o 'externa') en su nivel superior; se encontró '%s' en la línea %d", nombreModulo, ruta, hijoMod.Tipo, hijoMod.Linea)
			}
		}

		// El módulo puede declarar sus propias 'externa': su .a/.o vive,
		// por convención, en la misma carpeta que el .wn del módulo.
		dirModulo := filepath.Dir(rutaAbs)
		c.detectarLibsEnDir(dirModulo, modRaiz.Hijos)

		c.enProgreso[rutaAbs] = true
		expandidoMod, err := c.expandir(modRaiz.Hijos, dirModulo)
		delete(c.enProgreso, rutaAbs)
		if err != nil {
			return nil, err
		}
		c.cargados[rutaAbs] = true

		// Colisiones: solo se revisan las funciones definidas DIRECTAMENTE
		// en este módulo. Las transitivas (de sus propios imports) ya se
		// registraron al expandir esos archivos; revisar la lista ya
		// expandida las contaría dos veces y daría falsos positivos.
		// Los duplicados de estructuras los detecta el verificador.
		for _, hijoMod := range modRaiz.Hijos {
			if hijoMod.Tipo != "FUNCION" && hijoMod.Tipo != "EXTERNA" {
				continue
			}
			nombreFn, _ := hijoMod.Valor.(string)
			if c.funciones[nombreFn] {
				return nil, fmt.Errorf("línea %d: la función '%s' importada de '%s' colisiona con una función ya declarada", s.Linea, nombreFn, ruta)
			}
			c.funciones[nombreFn] = true
		}

		resultado = append(resultado, expandidoMod...)
	}
	return resultado, nil
}

// normalizarLlamadasDeModulo convierte `alias.funcion(args)` en una llamada
// normal a `funcion(args)`. Los módulos se expanden en un espacio de nombres
// plano, por lo que el alias solo sirve para seleccionar el módulo y no se
// pasa como argumento a la función.
func normalizarLlamadasDeModulo(n *Nodo, aliases map[string]bool) {
	if n == nil {
		return
	}
	for i := range n.Hijos {
		normalizarLlamadasDeModulo(n.Hijos[i], aliases)
	}
	if n.Tipo != "LLAMADA_METODO" || len(n.Hijos) == 0 {
		return
	}
	base := n.Hijos[0]
	nombreBase, ok := base.Valor.(string)
	if base.Tipo != "IDENTIFICADOR" || !ok || !aliases[nombreBase] {
		return
	}
	n.Tipo = "LLAMADA"
	n.Hijos = n.Hijos[1:]
}

// parsearModulo lexea y parsea el código fuente de un módulo importado,
// convirtiendo cualquier panic de lexer/parser en un error normal con
// contexto de qué módulo lo produjo.
func parsearModulo(source string, nombreModulo string, ruta string) (raiz *Nodo, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch v := r.(type) {
			case error:
				err = fmt.Errorf("en el módulo '%s' (%s): %v", nombreModulo, ruta, v)
			default:
				err = fmt.Errorf("en el módulo '%s' (%s): %v", nombreModulo, ruta, r)
			}
		}
	}()
	lexer := NewLexer()
	tokens := lexer.Tokenize(source)
	parser := NewParser(tokens)
	raiz = parser.Parse()
	return raiz, nil
}

// resolverRutaModulo traduce el nombre de un módulo importado en una ruta
// de archivo .wn, buscando en este orden:
//
//  1. Relativo a 'dirActual' (el directorio de quien importa): conserva
//     el comportamiento histórico. Vale para 'importar utilidades',
//     'importar utilidades.texto', 'importar "otra/ruta"' y rutas con
//     '../' o './'.
//  2. Búsqueda global (solo si no se encontró en 1 y la ruta no es
//     explícitamente relativa con '.' ni absoluta): cada raíz de
//     raicesBusquedaModulos() se prueba como base. Esto permite compilar
//     desde cualquier carpeta con winic instalado en el PATH, sin tener
//     que copiar 'modulos/' junto al .wn:
//
//       importar "tiempo/tiempo"  -> <raiz>/tiempo/tiempo.cwn
//       importar "system/sistem"  -> <raíz>/system/sistem.cwn
//       importar tiempo           -> <raíz>/tiempo.cwn o <raíz>/tiempo/tiempo.cwn
//
//     Por compatibilidad, si el import empieza con 'modulos/' o
//     'modules/' (p.ej. el histórico 'importar "modulos/tiempo/tiempo"'),
//     también se prueba sin ese prefijo, porque las raíces ya apuntan
//     dentro de 'modulos/'. Y si el nombre es corto ('tiempo'), se prueba
//     además '<raíz>/<nombre>/<nombre>.*' (p.ej. 'tiempo' -> 'tiempo/tiempo.cwn').
//
// Raíces globales: $WINI_PATH (separado por ':' en Linux/macOS, ';' en
// Windows), junto al ejecutable winic, y ubicaciones estándar
// (~/.local/share/wini, /usr/local/share/wini, /usr/share/wini, ~/.wini).
func resolverRutaModulo(nombreModulo string, dirActual string) (string, error) {
	if strings.TrimSpace(nombreModulo) == "" {
		return "", fmt.Errorf("nombre de módulo vacío en 'importar'")
	}

	var relativo string

	// Si contiene / o \, se considera una ruta explícita.
	if strings.ContainsAny(nombreModulo, "/\\") {
		relativo = nombreModulo
	} else {
		// importar utilidades.texto
		// -> utilidades/texto
		relativo = strings.ReplaceAll(nombreModulo, ".", string(filepath.Separator))
	}

	// 1) Intento local: relativo a dirActual (comportamiento histórico).
	if ruta, ok := probarRutaModuloEn(dirActual, relativo); ok {
		return ruta, nil
	}
	// Con extensión explícita también se prueba solo en local primero.
	ext := strings.ToLower(filepath.Ext(relativo))
	if ext == ".cwn" || ext == ".wini" || ext == ".wn" {
		if !esBusquedaGlobalAplicable(relativo) {
			return "", fmt.Errorf(
				"no se encontró el módulo '%s' (se buscó en '%s'; definí WINI_PATH o instalá winic con 'make install' para búsqueda global)",
				nombreModulo,
				filepath.Join(dirActual, relativo),
			)
		}
		// Si es aplicable a global (p.ej. "modulos/tiempo/tiempo.cwn"),
		// se sigue abajo a la búsqueda global.
	} else {
		if !esBusquedaGlobalAplicable(relativo) {
			return "", fmt.Errorf(
				"no se encontró el módulo '%s' (se buscaron '%s.cwn', '%s.wini' y '%s.wn' junto al archivo)",
				nombreModulo,
				filepath.Join(dirActual, relativo),
				filepath.Join(dirActual, relativo),
				filepath.Join(dirActual, relativo),
			)
		}
	}

	// 2) Búsqueda global: WINI_PATH, junto a winic y ubicaciones estándar.
	// Permite 'winic /tmp/hola.wn' con 'importar "tiempo/tiempo"' sin
	// copiar 'modulos/' junto al fuente.
	variantes := variantesRelativo(relativo)
	var probadas []string
	for _, raiz := range raicesBusquedaModulos(dirActual) {
		for _, v := range variantes {
			if ruta, ok := probarRutaModuloEn(raiz, v); ok {
				return ruta, nil
			}
			probadas = append(probadas, filepath.Join(raiz, v))
		}
	}
	return "", fmt.Errorf(
		"no se encontró el módulo '%s' (se buscó junto al archivo y en %d ubicacion(es) global(es): WINI_PATH=%q; probé p.ej. '%s')",
		nombreModulo,
		len(probadas),
		os.Getenv("WINI_PATH"),
		primeroDe(probadas),
	)
}

// esBusquedaGlobalAplicable dice si 'relativo' puede buscarse globalmente.
// Las rutas explícitamente relativas (./, ../) y las absolutas son
// locales por definición: solo tienen sentido junto al archivo que importa.
func esBusquedaGlobalAplicable(relativo string) bool {
	if filepath.IsAbs(relativo) {
		return false
	}
	limpio := filepath.Clean(relativo)
	if limpio == "." || limpio == ".." ||
		strings.HasPrefix(limpio, "."+string(filepath.Separator)) ||
		strings.HasPrefix(limpio, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// variantesRelativo genera las formas a probar dentro de cada raíz global.
// - La forma tal cual ('tiempo/tiempo', 'system/sistem').
// - Sin prefijo 'modulos/' o 'modules/' ('modulos/tiempo/tiempo' ->
//   'tiempo/tiempo'), por compatibilidad con imports históricos.
// - Si es un nombre corto sin separador ('tiempo'), además
//   '<nombre>/<nombre>' ('tiempo/tiempo'), que es la convención de la
//   stdlib instalada (modulos/tiempo/tiempo.cwn, etc).
func variantesRelativo(relativo string) []string {
	vistas := map[string]bool{}
	var out []string
	agregar := func(v string) {
		v = filepath.Clean(v)
		if !vistas[v] {
			vistas[v] = true
			out = append(out, v)
		}
	}
	agregar(relativo)
	sinPref := sinPrefijoModulos(relativo)
	if sinPref != relativo {
		agregar(sinPref)
	}
	if !strings.ContainsAny(relativo, "/\\") {
		base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(filepath.Base(relativo), ".cwn"), ".wini"), ".wn")
		if base != "" && base != relativo {
			// Por si vino con extensión y sin separador.
			agregar(filepath.Join(base, filepath.Base(relativo)))
		} else if ext := strings.ToLower(filepath.Ext(relativo)); ext == "" {
			agregar(filepath.Join(relativo, relativo))
			if sinPref != relativo {
				agregar(filepath.Join(sinPref, sinPref))
			}
		}
	} else if sinPref != relativo && !strings.ContainsAny(sinPref, "/\\") {
		agregar(filepath.Join(sinPref, sinPref))
	}
	return out
}

// sinPrefijoModulos quita un primer segmento 'modulos' o 'modules'.
func sinPrefijoModulos(relativo string) string {
	partes := strings.FieldsFunc(relativo, func(r rune) bool { return r == '/' || r == '\\' })
	if len(partes) > 1 && (partes[0] == "modulos" || partes[0] == "modules") {
		return filepath.Join(partes[1:]...)
	}
	return relativo
}

// probarRutaModuloEn busca 'relativo' dentro de 'base', probando las
// extensiones .cwn, .wini y .wn (en ese orden) si no trae una válida.
// Devuelve la ruta existente, o ("", false) si no está ahí.
func probarRutaModuloEn(base, relativo string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(relativo))
	if ext == ".cwn" || ext == ".wini" || ext == ".wn" {
		ruta := filepath.Join(base, relativo)
		if info, err := os.Stat(ruta); err == nil && !info.IsDir() {
			return ruta, true
		}
		return "", false
	}
	for _, extension := range []string{".cwn", ".wini", ".wn"} {
		ruta := filepath.Join(base, relativo+extension)
		if info, err := os.Stat(ruta); err == nil {
			if info.IsDir() {
				continue
			}
			return ruta, true
		}
	}
	return "", false
}

// raicesBusquedaModulos devuelve las carpetas base para la búsqueda global
// de módulos, en orden de prioridad (sin duplicados, solo existentes o
// potencialmente válidas; no se filtran por existencia para no ocultar
// errores de configuración en el mensaje final, pero se omiten vacías).
func raicesBusquedaModulos(dirActual string) []string {
	var raices []string
	agregar := func(r string) {
		if r == "" {
			return
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			abs = r
		}
		for _, e := range raices {
			if e == abs {
				return
			}
		}
		raices = append(raices, abs)
	}

	// 1) $WINI_PATH: una o varias carpetas separadas por ':' (';' en Windows).
	// Cada entrada se usa tal cual; si apunta a la raíz de instalación
	// (contiene 'modulos/'), también se agrega su subcarpeta 'modulos'.
	if wp := strings.TrimSpace(os.Getenv("WINI_PATH")); wp != "" {
		for _, d := range filepath.SplitList(wp) {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			agregar(d)
			agregar(filepath.Join(d, "modulos"))
			agregar(filepath.Join(d, "modules"))
		}
	}

	// 2) Junto al ejecutable winic (os.Executable resuelve symlinks de
	// ~/.local/bin). Cubre instalación 'make install' (bin/ + share/wini)
	// y el uso directo desde el repo (el binario junto a modulos/).
	if exe, err := os.Executable(); err == nil {
		if dirExe, err := filepath.EvalSymlinks(filepath.Dir(exe)); err == nil {
			agregar(filepath.Join(dirExe, "modulos"))
			agregar(filepath.Join(dirExe, "modules"))
			agregar(dirExe)
			agregar(filepath.Join(dirExe, "..", "modulos"))
			agregar(filepath.Join(dirExe, "..", "share", "wini", "modulos"))
			agregar(filepath.Join(dirExe, "..", "share", "wini"))
		} else {
			dirExe := filepath.Dir(exe)
			agregar(filepath.Join(dirExe, "modulos"))
			agregar(dirExe)
			agregar(filepath.Join(dirExe, "..", "share", "wini", "modulos"))
		}
	}

	// 3) Ubicaciones estándar de instalación.
	if home, err := os.UserHomeDir(); err == nil {
		agregar(filepath.Join(home, ".local", "share", "wini", "modulos"))
		agregar(filepath.Join(home, ".local", "share", "wini"))
		agregar(filepath.Join(home, ".wini", "modulos"))
		agregar(filepath.Join(home, ".wini"))
	}
	agregar(filepath.Join(string(filepath.Separator), "usr", "local", "share", "wini", "modulos"))
	agregar(filepath.Join(string(filepath.Separator), "usr", "local", "share", "wini"))
	agregar(filepath.Join(string(filepath.Separator), "usr", "share", "wini", "modulos"))

	// 4) Carpeta actual y 'modulos' del proyecto en curso (por si winic se
	// invoca desde la raíz del proyecto con otro cwd que dirActual).
	if cwd, err := os.Getwd(); err == nil {
		agregar(filepath.Join(cwd, "modulos"))
		agregar(cwd)
	}
	// dirActual ya se probó como local, pero su 'modulos' vecino también
	// sirve (p.ej. fuente en build/ y módulos en ../modulos/).
	if dirActual != "" && dirActual != "." {
		agregar(filepath.Join(dirActual, "modulos"))
		agregar(filepath.Join(dirActual, "..", "modulos"))
	}

	return raices
}

func primeroDe(lista []string) string {
	if len(lista) == 0 {
		return "(ninguna)"
	}
	return lista[0]
}

// Un módulo .wn, .cwn o .wini es un archivo Wini que solo declara funciones
// en su nivel superior (una "biblioteca").
//
// 'importar nombre' localiza automáticamente:
//
//   importar utilidades
//       -> utilidades.cwn
//       -> utilidades.wini
//
// 'importar utilidades.texto'
//
//       -> utilidades/texto.cwn
//       -> utilidades/texto.wini
//
// También se permiten rutas explícitas:
//
//   importar "otra/ruta.cwn"
//   importar "otra/ruta.wini"
//
// Si no se especifica extensión, se intenta primero .cwn, después .wini y
// finalmente .wn.
