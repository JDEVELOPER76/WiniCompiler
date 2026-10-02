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
// Un módulo .wn es un archivo Wini que solo declara funciones en su nivel
// superior (una "biblioteca"): no tiene sentido ejecutable propio ni
// requiere 'principal'. 'importar nombre' (o 'importar "ruta"') localiza
// ese archivo, lo parsea, resuelve recursivamente sus propias
// importaciones, y trae sus funciones al espacio de nombres global del
// programa que importa (no hay todavía llamadas calificadas del estilo
// 'modulo.funcion()': todo queda en un único espacio de nombres plano, tal
// como ya lo maneja el resto de la base mínima). El alias de
// 'importar x como y' se acepta sintácticamente pero, por ahora, no
// cambia la resolución de nombres.

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
	return cargador.listaLibs(), nil
}

type cargadorModulos struct {
	cargados   map[string]bool // rutas absolutas ya cargadas (evita duplicar en importaciones "diamante")
	enProgreso map[string]bool // rutas absolutas en proceso de carga (detecta importaciones circulares)
	funciones  map[string]bool // nombres de función ya vistos en todo el programa
	libs       map[string]bool // rutas absolutas .a/.o detectadas automáticamente
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

		// Un módulo .wn solo puede declarar funciones (y sus propias
		// importaciones) en el nivel superior: es una biblioteca, no un
		// programa con lógica de nivel superior.
		for _, hijoMod := range modRaiz.Hijos {
			if hijoMod.Tipo != "FUNCION" && hijoMod.Tipo != "IMPORTAR" && hijoMod.Tipo != "EXTERNA" {
				return nil, fmt.Errorf("el módulo '%s' (%s) solo puede declarar funciones (o 'externa') en su nivel superior; se encontró '%s' en la línea %d", nombreModulo, ruta, hijoMod.Tipo, hijoMod.Linea)
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

		for _, f := range expandidoMod {
			nombreFn, _ := f.Valor.(string)
			if c.funciones[nombreFn] {
				return nil, fmt.Errorf("línea %d: la función '%s' importada de '%s' colisiona con una función ya declarada", s.Linea, nombreFn, ruta)
			}
			c.funciones[nombreFn] = true
		}

		resultado = append(resultado, expandidoMod...)
	}
	return resultado, nil
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
// de archivo .wn, relativa a 'dirActual' (el directorio de quien importa):
//
//   - 'importar utilidades'          -> utilidades.wn
//   - 'importar utilidades.texto'    -> utilidades/texto.wn (cada punto es
//     un separador de subcarpeta, igual que en Python)
//   - 'importar "otra/ruta.wn"'      -> ruta explícita, tal cual
//   - 'importar "otra/ruta"'         -> ruta explícita, se le agrega .wn
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

	// Si el usuario ya especificó una extensión válida,
	// se conserva tal cual.
	ext := strings.ToLower(filepath.Ext(relativo))

	if ext != ".cwn" && ext != ".wini" {
		// Si no tiene extensión, probar primero .cwn
		// y luego .wini.
		rutaCWN := filepath.Join(dirActual, relativo+".cwn")
		if info, err := os.Stat(rutaCWN); err == nil {
			if info.IsDir() {
				return "", fmt.Errorf("'%s' (%s) es un directorio, no un módulo", nombreModulo, rutaCWN)
			}
			return rutaCWN, nil
		}

		rutaWINI := filepath.Join(dirActual, relativo+".wini")
		if info, err := os.Stat(rutaWINI); err == nil {
			if info.IsDir() {
				return "", fmt.Errorf("'%s' (%s) es un directorio, no un módulo", nombreModulo, rutaWINI)
			}
			return rutaWINI, nil
		}

		return "", fmt.Errorf(
			"no se encontró el módulo '%s' (se buscó '%s.cwn' y '%s.wini')",
			nombreModulo,
			filepath.Join(dirActual, relativo),
			filepath.Join(dirActual, relativo),
		)
	}

	// Si termina en .cwn o .wini, usar la ruta directamente.
	ruta := filepath.Join(dirActual, relativo)

	info, err := os.Stat(ruta)
	if err != nil {
		return "", fmt.Errorf(
			"no se encontró el módulo '%s' (se buscó en '%s')",
			nombreModulo,
			ruta,
		)
	}

	if info.IsDir() {
		return "", fmt.Errorf(
			"'%s' (%s) es un directorio, no un módulo",
			nombreModulo,
			ruta,
		)
	}

	return ruta, nil
}

// Un módulo .cwn o .wini es un archivo Wini que solo declara funciones
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
// Si no se especifica extensión, se intenta primero .cwn y después .wini.
