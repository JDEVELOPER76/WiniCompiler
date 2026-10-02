package wini

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/llir/llvm/ir"
	"github.com/llir/llvm/ir/constant"
	"github.com/llir/llvm/ir/enum"
	"github.com/llir/llvm/ir/types"
	"github.com/llir/llvm/ir/value"
)

type cgValue struct {
	ref value.Value
	tip Tipo
	// compartido indica que 'ref' es un puntero a un valor en el heap
	// (cadena/lista/diccionario) que YA tenía otro dueño antes de esta
	// expresión: viene de leer una variable, o de indexar una lista o
	// diccionario existente. No es un valor "recién construido" (literal,
	// concatenación, conversión, resultado de función, etc.), así que
	// guardarlo en una nueva variable o dentro de otra lista/diccionario
	// requiere retenerlo primero (ver retenerSiCompartido) o dos
	// ubicaciones terminan compartiendo el mismo puntero sin que el
	// conteo de referencias lo sepa.
	compartido bool
}

type cgVar struct {
	ptr value.Value
	tip Tipo
}

type loopLabels struct {
	cond *ir.Block
	end  *ir.Block
	// nombreVar es el nombre de la variable de un 'para ... en' (vacío
	// para 'mientras'). Se usa para no liberar esa variable en un
	// 'retornar' que la devuelve literalmente tal cual (ver el caso
	// RETORNO): si no, se liberaría la única referencia justo antes de
	// devolverla.
	nombreVar string
	// tryFramesAlEntrar es len(Codegen.tryFrames) justo al entrar a este
	// bucle: 'romper'/'continuar' lo usan como nivel objetivo para
	// desapilarTryFrames, de modo que solo corran el 'finalmente' y
	// desapilen los frames de 'intentar' empujados DENTRO de esta
	// iteración (los de fuera del bucle no son suyos).
	tryFramesAlEntrar int
	// limpiezaIter libera la variable de la iteración actual de un
	// 'para ... en' (nil para 'mientras', o si el tipo de elemento no es
	// heap). 'romper' y 'retornar' deben invocarla para todo bucle activo
	// que estén abandonando, ya que ellos no pasan por el bloque de
	// incremento donde normalmente se libera.
	limpiezaIter func()
	// limpiezaListaTemporal libera la lista de claves que 'para ... en
	// diccionario' pide prestada solo para iterar (nil si no aplica).
	// Solo debe invocarse una vez por cada ejecución real del bucle;
	// tanto el camino natural/'romper' (que convergen en el bloque final)
	// como 'retornar' (que no pasa por ese bloque) deben cubrirla, pero
	// nunca ambos a la vez para una misma pasada.
	limpiezaListaTemporal func()
	// limpiezaIterable libera el propio iterable de un 'para ... en'
	// (lista, diccionario o cadena) cuando era un valor temporal (recién
	// construido, no una variable): nil si el tipo no es heap o si el
	// iterable vino de una variable (compartido == true), en cuyo caso le
	// pertenece a esa variable y no se toca acá. Al igual que
	// limpiezaListaTemporal, solo debe invocarse una vez por cada
	// ejecución real del bucle: el camino natural/'romper' (que llegan al
	// bloque final) y 'retornar' (que no pasa por ese bloque) la cubren
	// cada uno por su lado, nunca los dos a la vez.
	limpiezaIterable func()
}

// paramHeap identifica un parámetro de tipo heap (cadena/lista/diccionario)
// de la función que se está generando en este momento, para poder
// liberarlo automáticamente en cada 'retornar' (ver parametrosHeapActuales
// en Codegen).
type paramHeap struct {
	nombre string
	ptr    value.Value
	tip    Tipo
}

// tryFrame representa un frame de 'intentar' activo, mientras dura su
// propio cuerpo. Además de contar para saber cuántos wini_try_pop hacen
// falta, guarda cómo reproducir SU 'finalmente' en el punto exacto donde
// se lo abandona: así, una salida temprana ('retornar'/'romper'/
// 'continuar') que atraviesa este 'intentar' corre su 'finalmente' igual
// que la salida normal, en vez de saltárselo (ver desapilarTryFrames).
type tryFrame struct {
	emitirFinally func() error
	// runtimeActivo indica si este frame SIGUE empujado en el stack del
	// runtime (wini_try_push): true desde que se empuja hasta que se
	// resuelve con wini_try_pop (salida normal o desapilarTryFrames) o
	// wini_try_pop_saltado (se llegó acá vía longjmp, ver catchDispatch
	// en emitirIntentar). El frame de un 'intentar' se mantiene en
	// Codegen.tryFrames durante TODO su cuerpo del try Y todos sus
	// 'capturar' — no solo mientras dura la primera parte — para que un
	// 'retornar'/'romper'/'continuar' dentro de un 'capturar' también
	// encuentre este frame y corra su 'finalmente'; runtimeActivo evita
	// que, en ese caso, se intente un wini_try_pop de más (el runtime ya
	// lo resolvió con wini_try_pop_saltado al entrar al despacho).
	runtimeActivo bool
}

type Codegen struct {
	module *ir.Module

	// parametrosHeapActuales son los parámetros de tipo heap de la
	// función que se está generando ahora mismo. El llamador ya retuvo
	// (ver retenerSiCompartido en el sitio de llamada) una referencia por
	// cada uno al pasarlo; esta función es su única dueña mientras se
	// ejecuta, así que cada 'retornar' libera esa referencia — salvo la
	// del parámetro que se devuelve literalmente tal cual, cuya
	// referencia pasa a ser la del valor de retorno. Las variables
	// locales normales (no parámetros) siguen sin liberación automática:
	// eso sigue siendo responsabilidad de 'liberar' explícito, igual que
	// antes.
	parametrosHeapActuales []paramHeap

	// tryFrames son los frames de manejo de excepciones ('intentar'
	// activos, ver wini_try_push en runtime.c) que empujó la función que
	// se está generando ahora mismo y siguen sin desapilarse en este
	// punto del código, del más externo (índice 0) al más interno. Cada
	// uno sabe reproducir su propio 'finalmente' (ver tryFrame). Tres
	// caminos desapilan frames, todos vía desapilarTryFrames:
	//   - 'retornar' desapila TODOS (nivel objetivo 0): 'ret' está a
	//     punto de destruir este stack frame, así que ningún frame puede
	//     quedar apuntando a memoria inválida para un 'lanzar' futuro en
	//     cualquier otra parte del programa, y cada 'finalmente' que se
	//     esté abandonando debe correr antes de salir.
	//   - 'romper'/'continuar' desapilan solo los empujados DESPUÉS de
	//     entrar al bucle que están abandonando (nivel objetivo
	//     loopLabels.tryFramesAlEntrar), no los de bucles o funciones
	//     exteriores.
	//   - el propio final del cuerpo de un 'intentar' (ver
	//     emitirIntentar) desapila como mucho SU PROPIO frame, y solo si
	//     ninguno de los caminos anteriores ya lo hizo.
	tryFrames []*tryFrame
	// finallyEnCursoDe evita volver a emitir el propio 'finalmente' cuando
	// contiene un 'lanzar'.
	finallyEnCursoDe *tryFrame

	// Runtime: print & string
	rtPrintI64    *ir.Func
	rtPrintF64    *ir.Func
	rtPrintBool   *ir.Func
	rtPrintString *ir.Func
	rtPrintList   *ir.Func
	rtPrintDict   *ir.Func
	rtStringNew   *ir.Func
	rtReadString  *ir.Func
	// Longitud y acceso por índice de cadena, usados por 'para ... en'
	// cuando el iterable es una cadena (recorre sus bytes uno a uno).
	rtStringLen     *ir.Func
	rtStringGetChar *ir.Func

	// Runtime: gestión de memoria (liberar)
	rtLiberarCadena      *ir.Func
	rtLiberarLista       *ir.Func
	rtLiberarDiccionario *ir.Func

	// Retención (conteo de referencias): se llaman cada vez que un valor
	// ya existente (no recién construido) se copia a una ubicación nueva
	// que puede sobrevivir de forma independiente (otra variable, un
	// elemento de lista/diccionario). Ver nota en runtime.h junto a
	// wini_string_retener.
	rtRetenerCadena      *ir.Func
	rtRetenerLista       *ir.Func
	rtRetenerDiccionario *ir.Func

	// Excepciones (intentar/capturar/finalmente/lanzar). Ver runtime.h.
	rtTryPush               *ir.Func
	rtTryPop                *ir.Func
	rtTryPopSaltado         *ir.Func
	rtLanzar                *ir.Func
	rtRelanzar              *ir.Func
	rtExcepcionEsTipo       *ir.Func
	rtExcepcionTomarMensaje *ir.Func
	rtExcepcionDescartar    *ir.Func
	rtSetjmp                *ir.Func

	// Runtime: string concatenation and conversions
	rtStringConcat *ir.Func
	rtI64ToString  *ir.Func
	rtF64ToString  *ir.Func
	rtBoolToString *ir.Func

	// Runtime: list concatenation
	rtListConcat *ir.Func

	// Runtime: string comparisons
	rtStringEq *ir.Func
	rtStringNe *ir.Func
	rtStringLt *ir.Func
	rtStringLe *ir.Func
	rtStringGt *ir.Func
	rtStringGe *ir.Func

	// Runtime: list
	rtListNewI64 *ir.Func
	rtListNewF64 *ir.Func
	rtListNewI1  *ir.Func
	rtListNewStr *ir.Func
	rtListLen    *ir.Func
	rtListGetI64 *ir.Func
	rtListGetF64 *ir.Func
	rtListGetI1  *ir.Func
	rtListGetStr *ir.Func
	rtListSetI64 *ir.Func
	rtListSetF64 *ir.Func
	rtListSetI1  *ir.Func
	rtListSetStr *ir.Func

	// Runtime: range
	rtRangeI64 *ir.Func

	// Runtime: argv
	rtArgsToList *ir.Func

	// Runtime: dict
	rtDictNew      *ir.Func
	rtDictLen      *ir.Func
	rtDictContains map[string]*ir.Func
	rtDictSet      map[string]*ir.Func
	rtDictGet      map[string]*ir.Func
	rtDictKeys     map[string]*ir.Func
	rtDictValues   map[string]*ir.Func

	funciones   map[string]*TipoFuncion
	llvmFuncs   map[string]*ir.Func
	estructuras map[string]*TipoEstructura

	scopes []map[string]cgVar
	loops  []loopLabels

	stringConstants map[string]*ir.Global

	funcionActual     *TipoFuncion
	funcionActualNodo *Nodo
	currentFunc       *ir.Func
	entryBlock        *ir.Block
	currentBlock      *ir.Block
	blockTerminated   bool

	labelCounter int
}

// ParsearYVerificar parsea y verifica 'source' sin contexto de archivo: si
// contiene alguna sentencia 'importar', esta se resuelve contra el
// directorio de trabajo actual. Para compilar un archivo real (con sus
// importaciones relativas a su propia carpeta) usar ParsearYVerificarArchivo.
func ParsearYVerificar(source string) (_ *Nodo, _ []string, err error) {
	return ParsearYVerificarArchivo(source, ".", "")
}

// ParsearYVerificarArchivo funciona igual que ParsearYVerificar, pero además
// resuelve las sentencias 'importar' del archivo buscando los módulos .wn
// referenciados relativos a 'dirBase' (normalmente el directorio del
// archivo de entrada). 'rutaEntrada', si se conoce, es la ruta del propio
// archivo que se compila, usada solo para evitar que se auto-importe.
//
// El segundo valor de retorno son las rutas absolutas de las bibliotecas
// .a/.o detectadas automáticamente (ver resolverImportaciones/detectarLibsEnDir):
// cualquier archivo o módulo con al menos una función 'externa' arrastra
// las .a/.o que estén en su misma carpeta, para enlazarse directamente.
func ParsearYVerificarArchivo(source string, dirBase string, rutaEntrada string) (_ *Nodo, _ []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch v := r.(type) {
			case error:
				err = v
			default:
				err = fmt.Errorf("%v", v)
			}
		}
	}()

	lexer := NewLexer()
	tokens := lexer.Tokenize(source)
	parser := NewParser(tokens)
	raiz := parser.Parse()
	libs, err := resolverImportaciones(raiz, dirBase, rutaEntrada)
	if err != nil {
		return nil, nil, err
	}
	VerificarPrograma(raiz)
	return raiz, libs, nil
}

func CompileToLLVM(source string) (string, error) {
	return NuevoCompilador("wini_module").Compilar(source)
}

// CompileFileToLLVM compila 'source' (el contenido de 'rutaEntrada') a IR de
// LLVM, resolviendo sus sentencias 'importar' relativas al directorio de
// 'rutaEntrada'. Es la variante que debe usar cualquier herramienta de
// línea de comandos que compile un archivo .wini/.wn real desde disco.
//
// El segundo valor de retorno son las rutas .a/.o detectadas automáticamente
// a partir de los módulos importados (y del propio archivo de entrada) que
// declaran funciones 'externa'; quien enlaza el binario final (p.ej. winic)
// debe agregarlas directamente al enlazador (se embeben en el binario, no
// requieren rpath). Las .so ya no se autodetectan acá: siguen soportadas,
// pero solo cuando el usuario las pasa a mano con '-so'.
func CompileFileToLLVM(source string, rutaEntrada string) (string, []string, error) {
	return NuevoCompilador("wini_module").CompilarArchivo(source, rutaEntrada)
}

func NewCodegen(moduleName string) *Codegen {
	m := ir.NewModule()
	m.SourceFilename = moduleName

	c := &Codegen{
		module:          m,
		funciones:       map[string]*TipoFuncion{},
		llvmFuncs:       map[string]*ir.Func{},
		estructuras:     map[string]*TipoEstructura{},
		stringConstants: map[string]*ir.Global{},
	}

	stringPtr := types.NewPointer(types.I8)
	listPtr := types.NewPointer(types.I8)

	// Print & string
	c.rtPrintI64 = m.NewFunc("wini_print_i64", types.Void, ir.NewParam("value", types.I64))
	c.rtPrintF64 = m.NewFunc("wini_print_f64", types.Void, ir.NewParam("value", types.Double))
	c.rtPrintBool = m.NewFunc("wini_print_bool", types.Void, ir.NewParam("value", types.I1))
	c.rtPrintString = m.NewFunc("wini_print_string", types.Void, ir.NewParam("value", stringPtr))
	c.rtPrintList = m.NewFunc("wini_print_list", types.Void,
		ir.NewParam("list", types.NewPointer(types.I8)),
		ir.NewParam("tipo", types.I32))
	c.rtPrintDict = m.NewFunc("wini_print_dict", types.Void,
		ir.NewParam("dict", types.NewPointer(types.I8)),
		ir.NewParam("tipoClave", types.I32),
		ir.NewParam("tipoValor", types.I32))
	c.rtStringNew = m.NewFunc("wini_string_new", stringPtr,
		ir.NewParam("src", types.NewPointer(types.I8)),
		ir.NewParam("len", types.I64))
	c.rtReadString = m.NewFunc("wini_read_string", stringPtr,
		ir.NewParam("prompt", stringPtr))
	c.rtStringLen = m.NewFunc("wini_string_len", types.I64,
		ir.NewParam("s", stringPtr))
	c.rtStringGetChar = m.NewFunc("wini_string_get_char", stringPtr,
		ir.NewParam("s", stringPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("linea", types.I64))

	// Gestión de memoria (liberar)
	c.rtLiberarCadena = m.NewFunc("wini_liberar_cadena", types.Void,
		ir.NewParam("s", stringPtr))
	c.rtLiberarLista = m.NewFunc("wini_liberar_lista", types.Void,
		ir.NewParam("lista", listPtr),
		ir.NewParam("tipo", types.I32))
	c.rtLiberarDiccionario = m.NewFunc("wini_liberar_diccionario", types.Void,
		ir.NewParam("dict", types.NewPointer(types.I8)),
		ir.NewParam("tipoClave", types.I32),
		ir.NewParam("tipoValor", types.I32))

	c.rtRetenerCadena = m.NewFunc("wini_string_retener", stringPtr,
		ir.NewParam("s", stringPtr))
	c.rtRetenerLista = m.NewFunc("wini_list_retener", listPtr,
		ir.NewParam("lista", listPtr))
	c.rtRetenerDiccionario = m.NewFunc("wini_dict_retener", types.NewPointer(types.I8),
		ir.NewParam("dict", types.NewPointer(types.I8)))

	// Excepciones
	c.rtTryPush = m.NewFunc("wini_try_push", types.NewPointer(types.I8))
	c.rtTryPop = m.NewFunc("wini_try_pop", types.Void)
	c.rtTryPopSaltado = m.NewFunc("wini_try_pop_saltado", types.Void)
	c.rtLanzar = m.NewFunc("wini_lanzar", types.Void,
		ir.NewParam("tipo", types.NewPointer(types.I8)),
		ir.NewParam("mensaje", stringPtr),
		ir.NewParam("linea", types.I64))
	c.rtRelanzar = m.NewFunc("wini_relanzar", types.Void)
	c.rtExcepcionEsTipo = m.NewFunc("wini_excepcion_es_tipo", types.I8,
		ir.NewParam("tipo", types.NewPointer(types.I8)))
	c.rtExcepcionTomarMensaje = m.NewFunc("wini_excepcion_tomar_mensaje", stringPtr)
	c.rtExcepcionDescartar = m.NewFunc("wini_excepcion_descartar", types.Void)
	// 'setjmp' de la libc: se llama DIRECTAMENTE desde el código
	// generado (nunca envuelto en una función de este runtime), porque
	// solo es válido en el stack frame que lo invoca. Requiere el
	// atributo 'returns_twice': el optimizador no puede asumir que una
	// función que lo tiene se ejecuta una sola vez.
	c.rtSetjmp = m.NewFunc("setjmp", types.I32, ir.NewParam("env", types.NewPointer(types.I8)))
	c.rtSetjmp.FuncAttrs = append(c.rtSetjmp.FuncAttrs, enum.FuncAttrReturnsTwice)

	// String concatenation and conversions
	c.rtStringConcat = m.NewFunc("wini_string_concat", stringPtr,
		ir.NewParam("a", stringPtr),
		ir.NewParam("b", stringPtr))
	c.rtI64ToString = m.NewFunc("wini_i64_to_string", stringPtr,
		ir.NewParam("v", types.I64))
	c.rtF64ToString = m.NewFunc("wini_f64_to_string", stringPtr,
		ir.NewParam("v", types.Double))
	c.rtBoolToString = m.NewFunc("wini_bool_to_string", stringPtr,
		ir.NewParam("v", types.I1))

	// List concatenation
	c.rtListConcat = m.NewFunc("wini_list_concat", listPtr,
		ir.NewParam("a", listPtr),
		ir.NewParam("b", listPtr),
		ir.NewParam("tipo", types.I32))

	// String comparisons
	c.rtStringEq = m.NewFunc("wini_string_eq", types.I1,
		ir.NewParam("a", stringPtr),
		ir.NewParam("b", stringPtr))
	c.rtStringNe = m.NewFunc("wini_string_ne", types.I1,
		ir.NewParam("a", stringPtr),
		ir.NewParam("b", stringPtr))
	c.rtStringLt = m.NewFunc("wini_string_lt", types.I1,
		ir.NewParam("a", stringPtr),
		ir.NewParam("b", stringPtr))
	c.rtStringLe = m.NewFunc("wini_string_le", types.I1,
		ir.NewParam("a", stringPtr),
		ir.NewParam("b", stringPtr))
	c.rtStringGt = m.NewFunc("wini_string_gt", types.I1,
		ir.NewParam("a", stringPtr),
		ir.NewParam("b", stringPtr))
	c.rtStringGe = m.NewFunc("wini_string_ge", types.I1,
		ir.NewParam("a", stringPtr),
		ir.NewParam("b", stringPtr))

	// List creation
	c.rtListNewI64 = m.NewFunc("wini_list_new_i64", listPtr,
		ir.NewParam("data", types.NewPointer(types.I8)),
		ir.NewParam("len", types.I64))
	c.rtListNewF64 = m.NewFunc("wini_list_new_f64", listPtr,
		ir.NewParam("data", types.NewPointer(types.I8)),
		ir.NewParam("len", types.I64))
	c.rtListNewI1 = m.NewFunc("wini_list_new_i1", listPtr,
		ir.NewParam("data", types.NewPointer(types.I8)),
		ir.NewParam("len", types.I64))
	c.rtListNewStr = m.NewFunc("wini_list_new_str", listPtr,
		ir.NewParam("data", types.NewPointer(types.I8)),
		ir.NewParam("len", types.I64))

	// List length & access
	c.rtListLen = m.NewFunc("wini_list_len", types.I64,
		ir.NewParam("list", listPtr))

	// Nótese que los get/set llevan un parámetro extra 'linea': el runtime
	// lo usa para reportar en qué línea del fuente se indexó fuera de
	// rango (IndexError capturable). Ver runtime.c.
	c.rtListGetI64 = m.NewFunc("wini_list_get_i64", types.I64,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("linea", types.I64))
	c.rtListGetF64 = m.NewFunc("wini_list_get_f64", types.Double,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("linea", types.I64))
	c.rtListGetI1 = m.NewFunc("wini_list_get_i1", types.I1,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("linea", types.I64))
	c.rtListGetStr = m.NewFunc("wini_list_get_str", stringPtr,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("linea", types.I64))

	c.rtListSetI64 = m.NewFunc("wini_list_set_i64", types.Void,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("value", types.I64),
		ir.NewParam("linea", types.I64))
	c.rtListSetF64 = m.NewFunc("wini_list_set_f64", types.Void,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("value", types.Double),
		ir.NewParam("linea", types.I64))
	c.rtListSetI1 = m.NewFunc("wini_list_set_i1", types.Void,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("value", types.I1),
		ir.NewParam("linea", types.I64))
	c.rtListSetStr = m.NewFunc("wini_list_set_str", types.Void,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("value", stringPtr),
		ir.NewParam("linea", types.I64))

	// Range function
	c.rtRangeI64 = m.NewFunc("wini_range_i64", listPtr,
		ir.NewParam("start", types.I64),
		ir.NewParam("end", types.I64),
		ir.NewParam("step", types.I64))

	// argc/argv -> lista<cadena>
	c.rtArgsToList = m.NewFunc("wini_args_to_list", listPtr,
		ir.NewParam("argc", types.I64),
		ir.NewParam("argv", types.NewPointer(types.NewPointer(types.I8))))

	// Dict functions
	dictPtr := types.NewPointer(types.I8)
	c.rtDictNew = m.NewFunc("wini_dict_new", dictPtr)
	c.rtDictLen = m.NewFunc("wini_dict_len", types.I64, ir.NewParam("dict", dictPtr))

	c.rtDictContains = map[string]*ir.Func{}
	c.rtDictSet = map[string]*ir.Func{}
	c.rtDictGet = map[string]*ir.Func{}
	c.rtDictKeys = map[string]*ir.Func{}
	c.rtDictValues = map[string]*ir.Func{}

	primTipos := []struct {
		suf string
		lt  types.Type
	}{
		{"i64", types.I64},
		{"f64", types.Double},
		{"i1", types.I1},
		{"str", stringPtr},
	}
	for _, k := range primTipos {
		c.rtDictContains[k.suf] = m.NewFunc("wini_dict_contains_"+k.suf, types.I1,
			ir.NewParam("dict", dictPtr), ir.NewParam("clave", k.lt))
		c.rtDictKeys[k.suf] = m.NewFunc("wini_dict_keys_"+k.suf, listPtr,
			ir.NewParam("dict", dictPtr))
		c.rtDictValues[k.suf] = m.NewFunc("wini_dict_values_"+k.suf, listPtr,
			ir.NewParam("dict", dictPtr))
		for _, v := range primTipos {
			combo := k.suf + "_" + v.suf
			c.rtDictSet[combo] = m.NewFunc("wini_dict_set_"+combo, types.Void,
				ir.NewParam("dict", dictPtr), ir.NewParam("clave", k.lt), ir.NewParam("valor", v.lt))
			c.rtDictGet[combo] = m.NewFunc("wini_dict_get_"+combo, v.lt,
				ir.NewParam("dict", dictPtr), ir.NewParam("clave", k.lt))
		}
	}

	return c
}

// primSuffix devuelve el sufijo usado en los símbolos runtime de listas/
// diccionarios ("i64"/"f64"/"i1"/"str") y el tipo LLVM correspondiente,
// para un tipo primitivo de Wini. El segundo valor de retorno es false si
// el tipo no es uno de los cuatro primitivos soportados por el runtime.
func primSuffix(t Tipo) (string, types.Type, bool) {
	switch {
	case t.Igual(TipoEntero):
		return "i64", types.I64, true
	case t.Igual(TipoDecimal):
		return "f64", types.Double, true
	case t.Igual(TipoBooleano):
		return "i1", types.I1, true
	case t.Igual(TipoCadena):
		return "str", types.NewPointer(types.I8), true
	default:
		return "", nil, false
	}
}

// tipoCode devuelve un código numérico (0=i64, 1=f64, 2=i1, 3=str) para
// los tipos primitivos soportados por el runtime; -1 si no está soportado.
func tipoCode(t Tipo) int {
	switch {
	case t.Igual(TipoEntero):
		return 0
	case t.Igual(TipoDecimal):
		return 1
	case t.Igual(TipoBooleano):
		return 2
	case t.Igual(TipoCadena):
		return 3
	default:
		return -1
	}
}

// esTipoHeap indica si un valor de este tipo vive en el heap y se
// comparte por puntero (cadena, lista o diccionario), a diferencia de los
// primitivos (entero/decimal/booleano) que se pasan por valor.
func esTipoHeap(t Tipo) bool {
	if t == nil {
		return false
	}
	if t.Igual(TipoCadena) {
		return true
	}
	switch t.(type) {
	case *TipoLista, *TipoDiccionario:
		return true
	}
	return false
}

// emitirRetenerValor retiene incondicionalmente un valor de tipo heap ya
// cargado en 'ref' (a diferencia de retenerSiCompartido, no consulta
// ningún cgValue: se usa en sitios donde ya sabemos, por construcción,
// que 'ref' apunta a algo con otro dueño, como el elemento de una lista
// que se acaba de leer para la variable de un 'para ... en').
func (c *Codegen) emitirRetenerValor(ref value.Value, t Tipo) value.Value {
	if !esTipoHeap(t) {
		return ref
	}
	switch t.(type) {
	case *TipoLista:
		return c.currentBlock.NewCall(c.rtRetenerLista, ref)
	case *TipoDiccionario:
		return c.currentBlock.NewCall(c.rtRetenerDiccionario, ref)
	default:
		return c.currentBlock.NewCall(c.rtRetenerCadena, ref)
	}
}

// retenerSiCompartido, si v es un valor de tipo heap que ya tenía otro
// dueño (v.compartido), emite la llamada de retención correspondiente y
// devuelve el nuevo cgValue (ya no marcado como compartido: la llamada
// que recibe este valor de vuelta pasa a ser su propio dueño de esa
// referencia). Si v es un valor recién construido (compartido == false),
// no hace nada: ya viene con su propia referencia "de fábrica".
func (c *Codegen) retenerSiCompartido(v cgValue) cgValue {
	if !v.compartido || !esTipoHeap(v.tip) {
		return v
	}
	v.ref = c.emitirRetenerValor(v.ref, v.tip)
	v.compartido = false
	return v
}

// emitirLiberarValor emite la liberación (una referencia) de un valor de
// tipo heap ya cargado en 'ref'. No hace nada para tipos no soportados
// por el runtime (mismo criterio que el builtin 'liberar').
func (c *Codegen) emitirLiberarValor(ref value.Value, t Tipo) {
	if ref == nil || !esTipoHeap(t) {
		return
	}
	switch tt := t.(type) {
	case *TipoLista:
		code := tipoCode(tt.Elemento)
		if code == -1 {
			return
		}
		c.currentBlock.NewCall(c.rtLiberarLista, ref, constant.NewInt(types.I32, int64(code)))
	case *TipoDiccionario:
		claveCode := tipoCode(tt.Clave)
		valorCode := tipoCode(tt.Valor)
		if claveCode == -1 || valorCode == -1 {
			return
		}
		c.currentBlock.NewCall(c.rtLiberarDiccionario, ref,
			constant.NewInt(types.I32, int64(claveCode)), constant.NewInt(types.I32, int64(valorCode)))
	default:
		c.currentBlock.NewCall(c.rtLiberarCadena, ref)
	}
}

// parseInterpolatedString descompone un texto de cadena interpolada (sin comillas)
// en literales y expresiones entre llaves.
func parseInterpolatedString(s string) (literales []string, expresiones []string) {
	var buf strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '{' {
			// Guardar literal acumulado
			literales = append(literales, buf.String())
			buf.Reset()
			// Buscar '}' de cierre
			j := i + 1
			for j < len(s) && s[j] != '}' {
				j++
			}
			if j == len(s) {
				// Error: falta '}'; tratamos como literal
				buf.WriteString(s[i:])
				break
			}
			expr := s[i+1 : j]
			expresiones = append(expresiones, expr)
			i = j + 1
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	if buf.Len() > 0 {
		literales = append(literales, buf.String())
	}
	return
}

func (c *Codegen) Generate(raiz *Nodo) (string, error) {
	if raiz == nil || raiz.Tipo != "PROGRAMA" {
		return "", fmt.Errorf("se esperaba un nodo PROGRAMA")
	}

	// Pase 1: registrar firmas de funciones (propias y externas)
	for _, n := range raiz.Hijos {
		if n.Tipo == "ESTRUCTURA" {
			nombre, _ := n.Valor.(string)
			c.estructuras[nombre] = &TipoEstructura{Nombre: nombre}
		}
	}
	for _, n := range raiz.Hijos {
		if n.Tipo == "ESTRUCTURA" {
			nombre, _ := n.Valor.(string)
			t := c.estructuras[nombre]
			for _, f := range n.Hijos[0].Hijos {
				t.Campos = append(t.Campos, CampoEstructura{f.Valor.(string), c.resolverAnotacion(f.Hijos[0])})
			}
		}
	}
	for _, n := range raiz.Hijos {
		if n.Tipo == "FUNCION" || n.Tipo == "EXTERNA" {
			nombre, _ := n.Valor.(string)
			c.funciones[nombre] = construirFirma(n)
		}

	}

	// Pase 2: pre-declarar funciones LLVM
	for _, n := range raiz.Hijos {
		if n.Tipo == "ESTRUCTURA" {
			continue
		}
		if n.Tipo != "FUNCION" && n.Tipo != "EXTERNA" {
			return "", fmt.Errorf("la base mínima solo compila funciones (o declaraciones 'externa') de nivel superior; se encontró '%s' en línea %d", n.Tipo, n.Linea)
		}
		nombre, _ := n.Valor.(string)
		firma := c.funciones[nombre]
		paramsNodo := n.Hijos[0]

		params := make([]*ir.Param, 0, len(paramsNodo.Hijos))
		flagParams := make([]*ir.Param, 0)
		for i, p := range paramsNodo.Hijos {
			nombreParam, _ := p.Valor.(string)
			// PARAM_VARIADICO ('*nombre:tipo') ya llega con su tipo
			// colapsado a lista<tipo> desde construirFirma, así que se
			// declara como cualquier otro parámetro de tipo lista: quien
			// llama arma la lista real (vacía o con los elementos sueltos
			// que junte) antes de la llamada.
			params = append(params, ir.NewParam(c.safeName("arg."+nombreParam), c.llvmType(firma.Parametros[i])))
			if len(p.Hijos) > 1 { // tiene valor por defecto
				flagParams = append(flagParams, ir.NewParam(c.safeName("arg."+nombreParam+".dado"), types.I1))
			}
		}
		// Los flags "dado" (uno por parámetro con valor por defecto, en el
		// mismo orden relativo) van al final de la firma. Así fn.Params[i]
		// sigue siendo el valor del parámetro i, sin importar si tiene
		// default o no; el flag de fn.Params[i] (cuando corresponde) vive
		// en fn.Params[len(firma.Parametros) + (i - firma.Requeridos)].
		params = append(params, flagParams...)

		if n.Tipo == "EXTERNA" {
			// Una función 'externa' no tiene valores por defecto (el
			// parser ya lo rechaza), así que no debe haber flagParams: si
			// los hubiera, se le agregaría a la firma un parámetro "dado"
			// (i1) extra que no existe en la función C real, rompiendo su
			// ABI. Se declara bajo su símbolo C real (sin pasar por
			// safeName, que es solo para identificadores Wini) para que
			// el linker la resuelva contra la biblioteca nativa
			// correspondiente. Al no agregarle bloques, se emite como
			// 'declare' en el .ll, igual que las funciones de runtime.
			if len(flagParams) > 0 {
				return "", fmt.Errorf("línea %d: la función externa '%s' no puede tener parámetros con valor por defecto", n.Linea, nombre)
			}
			simboloC, _ := n.Hijos[2].Valor.(string)
			fn := c.module.NewFunc(simboloC, c.llvmType(firma.Retorno), params...)
			if firma.VariadicoC {
				// Marca la firma LLVM como variádica de verdad ('...' al
				// final): así se imprime como 'declare ... (tipos, ...)' y
				// admite recibir en la llamada argumentos extra sin
				// parámetro formal correspondiente, cada uno impreso con
				// su propio tipo (igual que cualquier llamada real a
				// printf en C).
				fn.Sig.Variadic = true
			}
			c.llvmFuncs[nombre] = fn
			continue
		}

		fn := c.module.NewFunc(c.safeName(nombre), c.llvmType(firma.Retorno), params...)
		c.llvmFuncs[nombre] = fn
	}

	// Pase 3: emitir el cuerpo de cada función (las 'externa' no tienen
	// cuerpo: ya quedaron declaradas en el Pase 2)
	for _, n := range raiz.Hijos {
		if n.Tipo == "EXTERNA" || n.Tipo == "ESTRUCTURA" {
			continue
		}
		if err := c.emitirFuncion(n); err != nil {
			return "", err
		}
	}

	// Wrapper main -> principal
	if firmaPrincipal, ok := c.funciones["principal"]; ok {
		if !firmaPrincipal.Retorno.Igual(TipoEntero) {
			return "", fmt.Errorf("'principal' debe retornar entero para generar un ejecutable")
		}
		params := firmaPrincipal.Parametros
		listaCadena := NewTipoLista(TipoCadena)
		conArgs := len(params) == 2 && params[0].Igual(TipoEntero) && params[1].Igual(listaCadena)
		sinArgs := len(params) == 0
		if !sinArgs && !conArgs {
			return "", fmt.Errorf("'principal' debe tener firma 'funcion principal():entero:' o 'funcion principal(argc:entero, argv:lista<cadena>):entero:' para generar un ejecutable")
		}

		var mainFn *ir.Func
		var b *ir.Block
		var callArgs []value.Value
		if conArgs {
			argvType := types.NewPointer(types.NewPointer(types.I8))
			mainFn = c.module.NewFunc("main", types.I32,
				ir.NewParam("argc", types.I32),
				ir.NewParam("argv", argvType))
			b = mainFn.NewBlock("entry")
			argc64 := b.NewSExt(mainFn.Params[0], types.I64)
			argvList := b.NewCall(c.rtArgsToList, argc64, mainFn.Params[1])
			callArgs = []value.Value{argc64, argvList}
		} else {
			mainFn = c.module.NewFunc("main", types.I32)
			b = mainFn.NewBlock("entry")
		}
		call := b.NewCall(c.llvmFuncs["principal"], c.conFlagsDado(callArgs, firmaPrincipal)...)
		// FIX 1.4: NO liberar argvList acá. 'principal' recibe el argvList
		// como parámetro heap y, por contrato del codegen, libera SIEMPRE
		// sus parámetros heap en cada 'retornar' (salvo que devuelva el
		// parámetro tal cual, cosa que no puede pasar con 'argv' porque
		// 'principal' retorna entero). Si además el wrapper intentara
		// liberarlo, la segunda llamada sería sobre memoria ya liberada:
		// el guard 'rc > 0' del runtime no protege contra un puntero
		// colgado (leer lista->rc de memoria liberada ya es UB).
		trunc := b.NewTrunc(call, types.I32)
		b.NewRet(trunc)
	}

	return c.module.String(), nil
}

func (c *Codegen) resolverAnotacion(n *Nodo) Tipo {
	if nombre, ok := n.Valor.(string); ok {
		if t, found := c.estructuras[nombre]; found {
			return t
		}
	}
	return resolverAnotacion(n)
}

func (c *Codegen) emitirFuncion(n *Nodo) error {
	nombre, _ := n.Valor.(string)
	firma := c.funciones[nombre]
	if firma == nil {
		return fmt.Errorf("firma no encontrada para la función '%s'", nombre)
	}
	fn := c.llvmFuncs[nombre]

	paramsNodo := n.Hijos[0]
	c.funcionActual = firma
	c.funcionActualNodo = n
	c.currentFunc = fn
	c.scopes = nil
	c.loops = nil
	c.pushScope()

	entry := fn.NewBlock("entry")
	c.entryBlock = entry
	c.currentBlock = entry
	c.blockTerminated = false
	c.parametrosHeapActuales = nil
	c.tryFrames = nil
	c.finallyEnCursoDe = nil

	for i, p := range paramsNodo.Hijos {
		nombreParam, _ := p.Valor.(string)
		tipoParam := firma.Parametros[i]
		ptr := c.entryBlock.NewAlloca(c.llvmType(tipoParam))
		c.entryBlock.NewStore(fn.Params[i], ptr)
		c.declareVar(nombreParam, cgVar{ptr: ptr, tip: tipoParam})
		if esTipoHeap(tipoParam) {
			c.parametrosHeapActuales = append(c.parametrosHeapActuales, paramHeap{nombre: nombreParam, ptr: ptr, tip: tipoParam})
		}

		if len(p.Hijos) > 1 { // tiene valor por defecto
			// El flag "dado" dice si el llamador pasó un valor real para
			// este parámetro. Si no, calculamos acá adentro el valor por
			// defecto (en el propio cuerpo de la función, para que pueda
			// referenciar parámetros anteriores ya resueltos, incluidos
			// otros con default) y lo sobreescribimos en su alloca.
			flagIdx := len(firma.Parametros) + (i - firma.Requeridos)
			dado := fn.Params[flagIdx]

			defaultBlock := c.currentFunc.NewBlock(c.newLabel("param.default." + nombreParam))
			afterBlock := c.currentFunc.NewBlock(c.newLabel("param.after." + nombreParam))
			c.currentBlock.NewCondBr(dado, afterBlock, defaultBlock)

			c.currentBlock = defaultBlock
			defVal, err := c.emitirExpr(p.Hijos[1], tipoParam)
			if err != nil {
				return err
			}
			if !defVal.tip.Igual(tipoParam) {
				return fmt.Errorf("línea %d: el valor por defecto de '%s' no coincide con su tipo declarado", p.Linea, nombreParam)
			}
			// FIX 1.1: si el valor por defecto es heap y viene de otra
			// variable/parámetro ya existente (compartido == true), hay que
			// retenerlo antes de guardarlo en el alloca del parámetro —
			// igual que ya se hace en ASIGNACION_TIPADA/ASIGNACION. Si no,
			// el parámetro y la fuente original quedan compartiendo el
			// mismo rc=1 y el primer 'liberar' de cualquiera de los dos
			// deja al otro con un puntero colgante (use-after-free).
			defVal = c.retenerSiCompartido(defVal)
			c.currentBlock.NewStore(defVal.ref, ptr)
			c.currentBlock.NewBr(afterBlock)

			c.currentBlock = afterBlock
		}
	}

	for _, s := range n.Hijos[2].Hijos {
		if err := c.emitirSentencia(s); err != nil {
			return err
		}
		if c.blockTerminated {
			break
		}
	}

	if !c.blockTerminated {
		if firma.Retorno.Igual(TipoVacio) {
			// FIX(bug): antes, este camino de retorno IMPLÍCITO (la
			// función 'vacio' llega al final de su cuerpo sin ningún
			// 'retornar' explícito) hacía 'ret void' directamente, sin
			// pasar por la misma limpieza que sí hace el caso
			// 'RETORNO' explícito de más abajo (ver ese case, rama
			// TipoVacio): desapilar frames de 'intentar' activos y,
			// sobre todo, liberar los parámetros heap de la función.
			// Resultado: una función como
			//   'funcion f(s: cadena): vacio: escribir(s)'
			// (sin 'retornar' final) fugaba su parámetro 's' en TODAS
			// las llamadas — confirmado con LeakSanitizer. Alcanzaba
			// con agregar un 'retornar' explícito al final para que
			// dejara de fugar, lo cual delataba que el problema estaba
			// específicamente en este camino implícito, no en el
			// mecanismo de limpieza en sí. Replicamos acá exactamente
			// la misma limpieza que hace el 'RETORNO' explícito sin
			// valor.
			if err := c.desapilarTryFrames(0); err != nil {
				return err
			}
			if c.blockTerminated {
				return nil
			}
			for _, p := range c.parametrosHeapActuales {
				actual := c.currentBlock.NewLoad(c.llvmType(p.tip), p.ptr)
				c.emitirLiberarValor(actual, p.tip)
			}
			c.currentBlock.NewRet(nil)
			c.blockTerminated = true
			return nil
		}
		return fmt.Errorf("la función '%s' puede terminar sin 'retornar'", nombre)
	}
	return nil
}

func (c *Codegen) emitirSentencia(n *Nodo) error {
	switch n.Tipo {
	case "ASIGNACION_TIPADA":
		nombre, _ := n.Valor.(string)
		tipoDecl := c.resolverAnotacion(n.Hijos[0].Hijos[0])
		valor, err := c.emitirExpr(n.Hijos[1], nil)
		if err != nil {
			return err
		}
		if !valor.tip.Igual(tipoDecl) {
			return fmt.Errorf("línea %d: tipo incompatible en declaración de '%s'", n.Linea, nombre)
		}
		// Si 'valor' viene de otra variable o de leer una lista/diccionario
		// (compartido == true), esta nueva variable pasa a ser otro dueño
		// del mismo valor en el heap: hay que retenerlo, o liberar una de
		// las dos terminaría dejando a la otra con un puntero colgante.
		valor = c.retenerSiCompartido(valor)

		if existente, ok := c.lookupVarEnScopeActual(nombre); ok {
			// 'nombre' ya fue declarada en ESTE mismo scope: es el caso de
			// un 'mientras'/'para' cuyo cuerpo se emite una sola vez a
			// nivel LLVM (el alloca vive en entryBlock) pero corre muchas
			// veces en tiempo de ejecución. Antes de este fix, cada
			// iteración pisaba el store anterior sin liberar: si tipoDecl
			// es heap, cada vuelta del loop fugaba una referencia (el bug
			// reportado). Ahora se exige que el tipo coincida y, si es
			// heap, se libera el valor anterior antes de pisarlo — el
			// mismo tratamiento que ya recibe "ASIGNACION" para una
			// reasignación explícita.
			if !existente.tip.Igual(tipoDecl) {
				return fmt.Errorf("línea %d: '%s' ya fue declarada con otro tipo en este alcance", n.Linea, nombre)
			}
			if esTipoHeap(existente.tip) {
				anterior := c.currentBlock.NewLoad(c.llvmType(existente.tip), existente.ptr)
				if anterior != valor.ref {
					c.emitirLiberarValor(anterior, existente.tip)
				}
			}
			c.currentBlock.NewStore(valor.ref, existente.ptr)
			return nil
		}

		// Primera vez que 'nombre' se declara en este scope: alloca
		// nuevo. Para tipos heap se inicializa a null antes del store
		// real, así el alloca nunca queda con basura sin inicializar si
		// en el futuro algo lo lee o libera antes de este punto.
		ptr := c.entryBlock.NewAlloca(c.llvmType(tipoDecl))
		if esTipoHeap(tipoDecl) {
			c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), ptr)
		}
		c.currentBlock.NewStore(valor.ref, ptr)
		c.declareVar(nombre, cgVar{ptr: ptr, tip: tipoDecl})
		return nil

	case "ASIGNACION":
		nombre, _ := n.Valor.(string)
		variable, ok := c.lookupVar(nombre)
		if !ok {
			return fmt.Errorf("línea %d: variable '%s' no declarada", n.Linea, nombre)
		}
		valor, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return err
		}
		if !valor.tip.Igual(variable.tip) {
			return fmt.Errorf("línea %d: asignación incompatible a '%s'", n.Linea, nombre)
		}
		valor = c.retenerSiCompartido(valor)
		if esTipoHeap(variable.tip) {
			// 'nombre' está a punto de dejar de apuntar a lo que tenía
			// antes: liberamos esa referencia para no perderla (fuga de
			// memoria) ahora que reasignar ya no la vuelve inalcanzable
			// sin más. Comparamos primero para no romper 'a = a'.
			anterior := c.currentBlock.NewLoad(c.llvmType(variable.tip), variable.ptr)
			if anterior != valor.ref {
				c.emitirLiberarValor(anterior, variable.tip)
			}
		}
		c.currentBlock.NewStore(valor.ref, variable.ptr)
		return nil

	case "ASIGNACION_INDEX":
		nombre, _ := n.Valor.(string)
		variable, ok := c.lookupVar(nombre)
		if !ok {
			return fmt.Errorf("línea %d: variable '%s' no declarada", n.Linea, nombre)
		}
		contenedorVal := c.currentBlock.NewLoad(types.NewPointer(types.I8), variable.ptr)

		switch cont := variable.tip.(type) {
		case *TipoLista:
			idx, err := c.emitirExpr(n.Hijos[0], nil)
			if err != nil {
				return err
			}
			if !idx.tip.Igual(TipoEntero) {
				return fmt.Errorf("línea %d: el índice de una lista debe ser entero", n.Linea)
			}
			val, err := c.emitirExpr(n.Hijos[1], nil)
			if err != nil {
				return err
			}
			if !val.tip.Igual(cont.Elemento) {
				return fmt.Errorf("línea %d: el valor asignado no coincide con el tipo de la lista", n.Linea)
			}
			// El setter en runtime libera el elemento que se pisa, pero
			// espera que el valor que llega ya sea "suyo": si 'val' viene
			// de otra variable/lectura (compartido), lo retenemos antes
			// de guardarlo, para que la lista cuente como un dueño más.
			val = c.retenerSiCompartido(val)
			listPtr := c.currentBlock.NewBitCast(contenedorVal, types.NewPointer(types.I8))
			linea := constant.NewInt(types.I64, int64(n.Linea))
			switch {
			case cont.Elemento.Igual(TipoEntero):
				c.currentBlock.NewCall(c.rtListSetI64, listPtr, idx.ref, val.ref, linea)
			case cont.Elemento.Igual(TipoDecimal):
				c.currentBlock.NewCall(c.rtListSetF64, listPtr, idx.ref, val.ref, linea)
			case cont.Elemento.Igual(TipoBooleano):
				c.currentBlock.NewCall(c.rtListSetI1, listPtr, idx.ref, val.ref, linea)
			case cont.Elemento.Igual(TipoCadena):
				c.currentBlock.NewCall(c.rtListSetStr, listPtr, idx.ref, val.ref, linea)
			default:
				return fmt.Errorf("asignación por índice a lista de %s no soportada", cont.Elemento)
			}
			return nil

		case *TipoDiccionario:
			idx, err := c.emitirExpr(n.Hijos[0], nil)
			if err != nil {
				return err
			}
			if !idx.tip.Igual(cont.Clave) {
				return fmt.Errorf("línea %d: la clave usada para asignar no coincide con el tipo de clave del diccionario", n.Linea)
			}
			val, err := c.emitirExpr(n.Hijos[1], nil)
			if err != nil {
				return err
			}
			if !val.tip.Igual(cont.Valor) {
				return fmt.Errorf("línea %d: el valor asignado no coincide con el tipo de valor del diccionario", n.Linea)
			}
			kSuf, _, ok1 := primSuffix(cont.Clave)
			vSuf, _, ok2 := primSuffix(cont.Valor)
			if !ok1 || !ok2 {
				return fmt.Errorf("línea %d: tipo de clave/valor de diccionario no soportado en codegen", n.Linea)
			}
			setFn, ok := c.rtDictSet[kSuf+"_"+vSuf]
			if !ok {
				return fmt.Errorf("línea %d: combinación de tipos de diccionario (%s, %s) no soportada", n.Linea, kSuf, vSuf)
			}
			dictPtr := c.currentBlock.NewBitCast(contenedorVal, types.NewPointer(types.I8))
			// Igual que con las listas: si la clave o el valor vienen de
			// otra ubicación ya existente, hay que retenerlos antes de que
			// el diccionario se quede con el puntero (el setter libera el
			// valor anterior al pisar una clave existente, pero no retiene
			// lo que se le pasa).
			idx = c.retenerSiCompartido(idx)
			val = c.retenerSiCompartido(val)
			c.currentBlock.NewCall(setFn, dictPtr, idx.ref, val.ref)
			return nil

		default:
			return fmt.Errorf("línea %d: '%s' no es una lista ni un diccionario, no se puede asignar por índice", n.Linea, nombre)
		}

	case "SI":
		return c.emitirSi(n)

	case "MIENTRAS":
		return c.emitirMientras(n)

	case "PARA":
		return c.emitirPara(n)

	case "RETORNO":
		if c.funcionActual.Retorno.Igual(TipoVacio) {
			if len(n.Hijos) != 0 {
				return fmt.Errorf("línea %d: una función vacía no puede retornar un valor", n.Linea)
			}
			if err := c.desapilarTryFrames(0); err != nil {
				return err
			}
			if c.blockTerminated {
				return nil
			}
			for _, p := range c.parametrosHeapActuales {
				actual := c.currentBlock.NewLoad(c.llvmType(p.tip), p.ptr)
				c.emitirLiberarValor(actual, p.tip)
			}
			c.currentBlock.NewRet(nil)
			c.blockTerminated = true
			return nil
		}
		valor, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return err
		}
		nombreDevueltoDirecto := ""
		if n.Hijos[0].Tipo == "IDENTIFICADOR" {
			nombreDevueltoDirecto, _ = n.Hijos[0].Valor.(string)
		}
		// 'retornar' puede estar saliendo desde dentro de uno o más
		// 'para' anidados, sin pasar por sus bloques de incremento ni por
		// sus bloques finales: hay que liberar aquí la variable de la
		// iteración actual y la lista temporal de claves de CADA bucle
		// activo (de más interno a más externo da igual, no dependen
		// entre sí) — salvo que la variable de ESE bucle sea justo lo que
		// se está devolviendo tal cual, en cuyo caso liberarla la dejaría
		// colgante justo antes de devolverla.
		for i := len(c.loops) - 1; i >= 0; i-- {
			lp := c.loops[i]
			if lp.limpiezaIter != nil && lp.nombreVar != nombreDevueltoDirecto {
				lp.limpiezaIter()
			}
			if lp.limpiezaListaTemporal != nil {
				lp.limpiezaListaTemporal()
			}
			// FIX 2.3c: igual que limpiezaListaTemporal, el iterable
			// temporal de cada 'para' activo tampoco pasa por endBlock
			// cuando se sale por 'retornar'.
			if lp.limpiezaIterable != nil {
				lp.limpiezaIterable()
			}
		}
		// Lo mismo con los frames de 'intentar' activos: 'ret' está a
		// punto de destruir este stack frame, así que cualquier frame que
		// siga apilado tiene que desapilarse antes (o quedaría apuntando
		// a memoria inválida para un 'lanzar' futuro en cualquier otra
		// parte del programa), y el 'finalmente' de cada uno debe correr
		// igual que en la salida normal.
		if err := c.desapilarTryFrames(0); err != nil {
			return err
		}
		if c.blockTerminated {
			// El 'finalmente' de algún frame activo ya terminó este
			// camino por su cuenta (p.ej. con su propio 'retornar'): este
			// 'retornar' original ya no corresponde.
			return nil
		}
		// Si se devuelve un parámetro heap literalmente ('retornar x'),
		// su referencia pasa a ser la del valor de retorno: no se libera.
		// Cualquier OTRO parámetro heap de esta función sí se libera acá,
		// porque la función está por terminar y ya no lo necesita.
		for _, p := range c.parametrosHeapActuales {
			if p.nombre == nombreDevueltoDirecto {
				continue
			}
			actual := c.currentBlock.NewLoad(c.llvmType(p.tip), p.ptr)
			c.emitirLiberarValor(actual, p.tip)
		}
		c.currentBlock.NewRet(valor.ref)
		c.blockTerminated = true
		return nil

	case "LLAMADA":
		v, err := c.emitirExpr(n, nil)
		if err != nil {
			return err
		}
		// El resultado de una llamada usada como sentencia suelta (su
		// valor no se guarda en ninguna variable ni se le hace nada más)
		// es un valor recién construido que nadie más va a liberar: si es
		// de tipo heap, lo liberamos aquí mismo para no dejarlo fugado.
		if esTipoHeap(v.tip) {
			c.emitirLiberarValor(v.ref, v.tip)
		}
		return nil

	case "ROMPER":
		if len(c.loops) == 0 {
			return fmt.Errorf("línea %d: 'romper' fuera de un bucle", n.Linea)
		}
		lp := c.loops[len(c.loops)-1]
		// Desapilar los frames de 'intentar' empujados dentro de esta
		// iteración (no los de fuera del bucle), corriendo su
		// 'finalmente': si no, quedarían apuntando a un stack frame que
		// ya no se va a volver a visitar del mismo modo, un 'lanzar'
		// posterior podría saltar mal, y el 'finalmente' se saltearía.
		if err := c.desapilarTryFrames(lp.tryFramesAlEntrar); err != nil {
			return err
		}
		if c.blockTerminated {
			// El 'finalmente' de algún frame activo ya terminó este
			// camino por su cuenta: este 'romper' original ya no
			// corresponde.
			return nil
		}
		// 'romper' salta directo al final del bucle sin pasar por el
		// bloque de incremento: hay que liberar ahí mismo la variable de
		// esta iteración (si aplica). La lista temporal de claves (si el
		// bucle itera un diccionario) NO se libera aquí: eso ya lo hace
		// el bloque final, al que este salto converge.
		if lim := lp.limpiezaIter; lim != nil {
			lim()
		}
		c.currentBlock.NewBr(lp.end)
		c.blockTerminated = true
		return nil

	case "CONTINUAR":
		if len(c.loops) == 0 {
			return fmt.Errorf("línea %d: 'continuar' fuera de un bucle", n.Linea)
		}
		lp := c.loops[len(c.loops)-1]
		if err := c.desapilarTryFrames(lp.tryFramesAlEntrar); err != nil {
			return err
		}
		if c.blockTerminated {
			return nil
		}
		c.currentBlock.NewBr(lp.cond)
		c.blockTerminated = true
		return nil

	case "INTENTAR":
		return c.emitirIntentar(n)

	case "LANZAR":
		return c.emitirLanzar(n)

	case "RELANZAR":
		if err := c.finallyDeFramesResueltos(); err != nil {
			return err
		}
		if !c.blockTerminated {
			c.currentBlock.NewCall(c.rtRelanzar)
			c.currentBlock.NewUnreachable()
			c.blockTerminated = true
		}
		return nil

	default:
		return fmt.Errorf("línea %d: la base mínima no soporta la sentencia '%s' en codegen LLVM todavía", n.Linea, n.Tipo)
	}
}

func (c *Codegen) emitirSi(n *Nodo) error {
	cond, err := c.emitirExpr(n.Hijos[0], nil)
	if err != nil {
		return err
	}

	thenBlock := c.currentFunc.NewBlock(c.newLabel("if.then"))
	endBlock := c.currentFunc.NewBlock(c.newLabel("if.end"))
	haySino := len(n.Hijos[2].Hijos) > 0
	elseBlock := endBlock
	if haySino {
		elseBlock = c.currentFunc.NewBlock(c.newLabel("if.else"))
	}
	c.currentBlock.NewCondBr(cond.ref, thenBlock, elseBlock)

	c.pushScope()
	c.currentBlock = thenBlock
	c.blockTerminated = false
	for _, s := range n.Hijos[1].Hijos {
		if err := c.emitirSentencia(s); err != nil {
			return err
		}
		if c.blockTerminated {
			break
		}
	}
	thenTerminado := c.blockTerminated
	if !thenTerminado {
		c.currentBlock.NewBr(endBlock)
	}
	c.popScope()

	elseTerminado := false
	if haySino {
		c.pushScope()
		c.currentBlock = elseBlock
		c.blockTerminated = false
		for _, s := range n.Hijos[2].Hijos {
			if err := c.emitirSentencia(s); err != nil {
				return err
			}
			if c.blockTerminated {
				break
			}
		}
		elseTerminado = c.blockTerminated
		if !elseTerminado {
			c.currentBlock.NewBr(endBlock)
		}
		c.popScope()
	}

	if haySino && thenTerminado && elseTerminado {
		endBlock.NewUnreachable()
		c.currentBlock = endBlock
		c.blockTerminated = true
		return nil
	}

	c.currentBlock = endBlock
	c.blockTerminated = false
	return nil
}

func (c *Codegen) emitirMientras(n *Nodo) error {
	condBlock := c.currentFunc.NewBlock(c.newLabel("while.cond"))
	bodyBlock := c.currentFunc.NewBlock(c.newLabel("while.body"))
	endBlock := c.currentFunc.NewBlock(c.newLabel("while.end"))

	c.currentBlock.NewBr(condBlock)

	c.currentBlock = condBlock
	cond, err := c.emitirExpr(n.Hijos[0], nil)
	if err != nil {
		return err
	}
	c.currentBlock.NewCondBr(cond.ref, bodyBlock, endBlock)

	c.loops = append(c.loops, loopLabels{cond: condBlock, end: endBlock, tryFramesAlEntrar: len(c.tryFrames)})
	c.pushScope()
	c.currentBlock = bodyBlock
	c.blockTerminated = false
	for _, s := range n.Hijos[1].Hijos {
		if err := c.emitirSentencia(s); err != nil {
			return err
		}
		if c.blockTerminated {
			break
		}
	}
	if !c.blockTerminated {
		c.currentBlock.NewBr(condBlock)
	}
	c.popScope()
	c.loops = c.loops[:len(c.loops)-1]

	c.currentBlock = endBlock
	c.blockTerminated = false
	return nil
}

// emitirLanzar genera 'lanzar Tipo(mensaje)' / 'lanzar Tipo': arma el
// mensaje (o una cadena vacía si no se dio ninguno) y llama a
// 'wini_lanzar', que nunca retorna (hace longjmp al 'intentar' más
// cercano, o termina el programa si no hay ninguno activo). Antes de
// propagarlo, corre los 'finalmente' de los frames ya resueltos.
func (c *Codegen) emitirLanzar(n *Nodo) error {
	tipoNodo := n.Hijos[0]
	mensajeNodo := n.Hijos[1]
	tipoError, _ := tipoNodo.Valor.(string)
	if tipoError == "" {
		tipoError = "RuntimeError"
	}

	var mensajeVal cgValue
	if len(mensajeNodo.Hijos) > 0 {
		v, err := c.emitirExpr(mensajeNodo.Hijos[0], TipoCadena)
		if err != nil {
			return err
		}
		if !v.tip.Igual(TipoCadena) {
			return fmt.Errorf("línea %d: el mensaje de 'lanzar' debe ser de tipo cadena, se encontró %s", n.Linea, v.tip)
		}
		mensajeVal = v
	} else {
		vacio := c.internString("")
		arrType := vacio.ContentType.(*types.ArrayType)
		gep := c.currentBlock.NewGetElementPtr(arrType, vacio,
			constant.NewInt(types.I64, 0), constant.NewInt(types.I64, 0))
		call := c.currentBlock.NewCall(c.rtStringNew, gep, constant.NewInt(types.I64, 0))
		mensajeVal = cgValue{ref: call, tip: TipoCadena}
	}

	// 'wini_lanzar' toma posesión de la referencia del mensaje: si viene
	// de una variable existente hay que retenerla antes, igual que con
	// cualquier otra función del runtime que absorbe una referencia
	// (dict/list set, etc.).
	mensajeVal = c.retenerSiCompartido(mensajeVal)

	tipoGlobal := c.internString(tipoError)
	tipoArrType := tipoGlobal.ContentType.(*types.ArrayType)
	tipoGep := c.currentBlock.NewGetElementPtr(tipoArrType, tipoGlobal,
		constant.NewInt(types.I64, 0), constant.NewInt(types.I64, 0))

	if err := c.finallyDeFramesResueltos(); err != nil {
		return err
	}
	if c.blockTerminated {
		return nil
	}

	c.currentBlock.NewCall(c.rtLanzar, tipoGep, mensajeVal.ref, constant.NewInt(types.I64, int64(n.Linea)))
	// 'wini_lanzar' nunca retorna (longjmp o exit()); LLVM exige que todo
	// bloque termine en una instrucción terminadora.
	c.currentBlock.NewUnreachable()
	c.blockTerminated = true
	return nil
}

// finallyDeFramesResueltos corre, de más interno a más externo, el
// 'finalmente' de cada frame ya resuelto hasta encontrar el primer frame
// cuyo runtime sigue activo. Ese frame sigue siendo un destino legítimo del
// longjmp nativo y no se desenrolla aquí.
func (c *Codegen) finallyDeFramesResueltos() error {
	for i := len(c.tryFrames) - 1; i >= 0; i-- {
		frame := c.tryFrames[i]
		if frame.runtimeActivo {
			break
		}
		if frame == c.finallyEnCursoDe {
			continue
		}
		if err := frame.emitirFinally(); err != nil {
			return err
		}
		if c.blockTerminated {
			return nil
		}
	}
	return nil
}

// desapilarTryFrames desapila, de más interno a más externo, cada frame
// de 'intentar' activo por encima de 'nivelObjetivo' (sin incluirlo):
// para cada uno, primero lo saca del stack del runtime (wini_try_pop) y
// RECIÉN DESPUÉS corre su 'finalmente' propio. Ese orden importa: si el
// 'finalmente' en sí lanza una excepción, tiene que propagarse al frame
// siguiente hacia afuera, no volver a caer sobre el jmp_buf de un frame
// que ya estamos abandonando.
//
// Si un 'finalmente' termina el bloque actual por su cuenta (por
// ejemplo, con su propio 'retornar' o 'romper'), el desapilado se corta
// ahí mismo: ese 'finalmente' ya decidió el destino de este camino, así
// que los frames más externos (y sus propios 'finalmente') no
// corresponden a esta salida — mismo criterio que ya usa el resto del
// codegen para 'finally' que interrumpe su propio camino normal.
//
// Se usa desde 'retornar' (nivelObjetivo 0, se abandona toda la
// función), 'romper' y 'continuar' (nivelObjetivo
// loopLabels.tryFramesAlEntrar), y desde el propio final del cuerpo de
// un 'intentar' para su caso general (ver emitirIntentar).
func (c *Codegen) desapilarTryFrames(nivelObjetivo int) error {
	for len(c.tryFrames) > nivelObjetivo {
		frame := c.tryFrames[len(c.tryFrames)-1]
		c.tryFrames = c.tryFrames[:len(c.tryFrames)-1]
		if frame.runtimeActivo {
			c.currentBlock.NewCall(c.rtTryPop)
		}
		// FIX 1.3: si 'frame' es el mismo frame cuyo propio 'finalmente' se
		// está ejecutando en este momento (c.finallyEnCursoDe), NO volver a
		// correr su bloque 'finalmente': ya se está ejecutando en este
		// mismo camino (p. ej. un 'retornar' dentro del propio
		// 'finalmente' llamó a desapilarTryFrames). El frame igual se
		// desapila de c.tryFrames arriba; solo se salta la re-ejecución.
		if frame == c.finallyEnCursoDe {
			if c.blockTerminated {
				return nil
			}
			continue
		}
		if err := frame.emitirFinally(); err != nil {
			return err
		}
		if c.blockTerminated {
			return nil
		}
	}
	return nil
}

// emitirIntentar genera 'intentar / capturar / finalmente' usando
// setjmp/longjmp: ver la nota junto a wini_try_push en runtime.h para el
// contrato completo. Estructura del AST (ver parsearIntentar):
//
//	INTENTAR
//	├── BLOQUE_TRY        -> [sentencias]
//	├── BLOQUES_CAPTURAR  -> [CAPTURAR(tipoError) -> [VARIABLE_ERROR(nombre), BLOQUE -> [sentencias]], ...]
//	└── BLOQUE_FINALLY    -> [sentencias]  (puede no tener hijos)
//
// 'finalmente' corre en todos los caminos de salida "conocidos en
// tiempo de compilación": el try termina sin lanzar, el try lanza y un
// 'capturar' de esta misma 'intentar' hace match y completa normal, el
// try lanza y ningún 'capturar' hace match (se re-lanza después del
// finally), y un 'retornar'/'romper'/'continuar' dentro del try o de
// cualquier 'capturar' de esta misma 'intentar' (ver desapilarTryFrames
// y el frame que se mantiene vivo en c.tryFrames durante toda la
// función).
//
// Un 'lanzar' emitido dentro de un 'capturar' o de un 'finalmente' de esta
// 'intentar' no puede volver a ser atrapado por los otros 'capturar' de la
// misma 'intentar'. Por eso emitirLanzar corre inline los 'finalmente' de
// los frames ya resueltos antes de propagarlo, sin alterar el longjmp nativo
// que sigue resolviendo los destinos todavía activos.
func (c *Codegen) emitirIntentar(n *Nodo) error {
	bloqueTry := n.Hijos[0]
	bloquesCapturar := n.Hijos[1]
	bloqueFinally := n.Hijos[2]

	fn := c.currentBlock.Parent
	nivelAlEntrar := len(c.tryFrames)

	// Sin capturas no hay ningún destino local para una excepción. Evitamos
	// introducir setjmp; el finally sigue cubriendo las salidas normales y
	// las salidas estructurales mediante tryFrames.
	if len(bloquesCapturar.Hijos) == 0 {
		endBlock := fn.NewBlock(c.newLabel("intentar.fin"))
		var frame *tryFrame
		if len(bloqueFinally.Hijos) > 0 {
			frame = &tryFrame{runtimeActivo: false}
			frame.emitirFinally = func() error {
				anterior := c.finallyEnCursoDe
				c.finallyEnCursoDe = frame
				defer func() { c.finallyEnCursoDe = anterior }()
				c.pushScope()
				defer c.popScope()
				for _, s := range bloqueFinally.Hijos {
					if err := c.emitirSentencia(s); err != nil {
						return err
					}
					if c.blockTerminated {
						break
					}
				}
				return nil
			}
			c.tryFrames = append(c.tryFrames, frame)
		}
		c.pushScope()
		for _, s := range bloqueTry.Hijos {
			if err := c.emitirSentencia(s); err != nil {
				return err
			}
			if c.blockTerminated {
				break
			}
		}
		c.popScope()

		// El finally sólo se corre inline si el try terminó normalmente:
		// si terminó por 'retornar'/'romper'/'continuar'/'lanzar', ese
		// camino ya lo corrió por su cuenta vía desapilarTryFrames /
		// finallyDeFramesResueltos (ambos pasan por emitirFinally
		// también).
		if !c.blockTerminated && frame != nil {
			if err := frame.emitirFinally(); err != nil {
				return err
			}
		}

		// Cerrar el camino hacia endBlock sólo si todavía estamos en un
		// bloque vivo. Si el try o el finally terminaron por su cuenta,
		// endBlock queda sin predecesores y hay que propagar
		// blockTerminated para que quien llamó no intente seguir
		// emitiendo código inalcanzable.
		llegoAEnd := false
		if !c.blockTerminated {
			c.currentBlock.NewBr(endBlock)
			llegoAEnd = true
		} else {
			// FIX(bug): si nadie más branchea hacia 'endBlock' (nada de
			// código, en ningún otro lado, tiene una referencia a este
			// bloque más que esta misma función), queda agregado a la
			// función vía fn.NewBlock() de arriba pero SIN ningún
			// terminador — LLVM exige que TODO basic block de una
			// función termine en una instrucción terminadora (br, ret,
			// unreachable, etc.), sin importar si el bloque es o no
			// alcanzable en tiempo de ejecución. Antes, este caso
			// (típicamente: un 'intentar' anidado sin su propio
			// 'capturar', cuyo cuerpo termina siempre lanzando, anidado
			// a su vez dentro de un 'intentar' externo con 'capturar')
			// dejaba 'endBlock' vacío y sin terminador, y
			// c.currentBlock apuntando ahí con blockTerminated=true, así
			// que ningún código posterior lo completaba — el panic
			// ("missing terminator in basic block") recién aparecía
			// mucho después, al serializar el módulo completo a texto
			// LLVM IR. Como en este punto ya sabemos que endBlock es
			// código muerto (nada llega hasta acá desde este 'intentar'),
			// 'unreachable' es el terminador correcto: documenta la
			// garantía en el propio IR en vez de dejar un bloque
			// inválido.
			endBlock.NewUnreachable()
		}

		if len(c.tryFrames) > nivelAlEntrar {
			c.tryFrames = c.tryFrames[:nivelAlEntrar]
		}
		c.currentBlock = endBlock
		c.blockTerminated = !llegoAEnd
		return nil
	}

	tryBodyBlock := fn.NewBlock(c.newLabel("intentar.cuerpo"))
	catchDispatch := fn.NewBlock(c.newLabel("intentar.despacho"))
	finallyBlock := fn.NewBlock(c.newLabel("intentar.finalmente"))
	endBlock := fn.NewBlock(c.newLabel("intentar.fin"))
	var relanzarBlock *ir.Block
	if !hayCatchAll(bloquesCapturar) {
		relanzarBlock = fn.NewBlock(c.newLabel("intentar.relanzar"))
	}

	frame := &tryFrame{runtimeActivo: true}
	frame.emitirFinally = func() error {
		anterior := c.finallyEnCursoDe
		c.finallyEnCursoDe = frame
		defer func() { c.finallyEnCursoDe = anterior }()
		c.pushScope()
		defer c.popScope()
		for _, s := range bloqueFinally.Hijos {
			if err := c.emitirSentencia(s); err != nil {
				return err
			}
			if c.blockTerminated {
				break
			}
		}
		return nil
	}

	// --- Empujar el frame y hacer 'setjmp' directamente aquí: solo es
	// válido en el stack frame que lo invoca, así que no puede
	// envolverse en una función del runtime. ---
	bufPtr := c.currentBlock.NewCall(c.rtTryPush)
	setjmpVal := c.currentBlock.NewCall(c.rtSetjmp, bufPtr)
	// El frame se empuja UNA sola vez y se mantiene en c.tryFrames durante
	// TODO el resto de esta función (cuerpo del try y cada 'capturar'):
	// solo se saca al final (ver el desapilado unificado más abajo), o
	// antes si algún 'retornar'/'romper'/'continuar' — en el try o en
	// cualquier 'capturar' — lo encuentra primero vía desapilarTryFrames.
	// Así, un 'retornar' dentro de un 'capturar' también corre el
	// 'finalmente' de esta 'intentar', no solo uno dentro del try.
	c.tryFrames = append(c.tryFrames, frame)
	cond := c.currentBlock.NewICmp(enum.IPredNE, setjmpVal, constant.NewInt(types.I32, 0))
	c.currentBlock.NewCondBr(cond, catchDispatch, tryBodyBlock)

	// --- Cuerpo del try ---
	c.currentBlock = tryBodyBlock
	c.blockTerminated = false
	c.pushScope()
	for _, s := range bloqueTry.Hijos {
		if err := c.emitirSentencia(s); err != nil {
			return err
		}
		if c.blockTerminated {
			break
		}
	}
	c.popScope()

	// incomingFinally acumula un incoming por cada camino que
	// efectivamente ramifica a finallyBlock. Nunca más de uno por camino,
	// y NUNCA un incoming desde un bloque que no sea realmente predecesor
	// de finallyBlock (LLVM rechaza PHI nodes con predecesores que no lo
	// son).
	incomingFinally := make([]*ir.Incoming, 0, len(bloquesCapturar.Hijos)+1)

	// Si el try terminó normalmente (no por 'retornar'/'romper'/
	// 'continuar'/'lanzar'), cerrar el frame del runtime y ramificar a
	// finallyBlock. El predecesor real es c.currentBlock en este
	// instante, NO tryBodyBlock: si el try body contenía un 'si',
	// 'mientras' o 'para', el bloque desde el que salimos puede ser
	// cualquier bloque interno.
	if !c.blockTerminated {
		c.currentBlock.NewCall(c.rtTryPop)
		frame.runtimeActivo = false
		predTryBody := c.currentBlock
		c.currentBlock.NewBr(finallyBlock)
		incomingFinally = append(incomingFinally,
			ir.NewIncoming(constant.NewInt(types.I1, 0), predTryBody))
	}

	// --- Despacho de 'capturar' (llegamos aquí solo vía longjmp) ---
	c.currentBlock = catchDispatch
	c.blockTerminated = false
	c.currentBlock.NewCall(c.rtTryPopSaltado)
	// El runtime ya resolvió este frame al saltar acá: si un 'retornar'
	// dentro de algún 'capturar' de más abajo termina encontrando este
	// frame vía desapilarTryFrames, no debe volver a pedirle un
	// wini_try_pop al runtime.
	frame.runtimeActivo = false

	huboCatchAll := false
	for _, capturar := range bloquesCapturar.Hijos {
		tipoError, _ := capturar.Valor.(string)
		variableNodo := capturar.Hijos[0]
		cuerpoNodo := capturar.Hijos[1]
		nombreVar, _ := variableNodo.Valor.(string)

		catchBody := fn.NewBlock(c.newLabel("intentar.capturar"))
		var proximoTest *ir.Block

		if tipoError == "" {
			// 'capturar' sin tipo (catch-all): siempre hace match. No
			// queda ningún camino de "no hizo match" después de esto, así
			// que 'catchDispatch' termina aquí mismo (sin pasar por el
			// bloque de "ningún 'capturar' hizo match" de más abajo, que
			// asume que se llegó a él sin saltar a ningún 'catchBody').
			c.currentBlock.NewBr(catchBody)
		} else {
			proximoTest = fn.NewBlock(c.newLabel("intentar.prueba"))
			tipoGlobal := c.internString(tipoError)
			arrType := tipoGlobal.ContentType.(*types.ArrayType)
			gep := c.currentBlock.NewGetElementPtr(arrType, tipoGlobal,
				constant.NewInt(types.I64, 0), constant.NewInt(types.I64, 0))
			esTipo := c.currentBlock.NewCall(c.rtExcepcionEsTipo, gep)
			haceMatch := c.currentBlock.NewICmp(enum.IPredNE, esTipo, constant.NewInt(types.I8, 0))
			c.currentBlock.NewCondBr(haceMatch, catchBody, proximoTest)
		}

		c.currentBlock = catchBody
		c.pushScope()
		if nombreVar != "" {
			msg := c.currentBlock.NewCall(c.rtExcepcionTomarMensaje)
			ptr := c.entryBlock.NewAlloca(c.llvmType(TipoCadena))
			c.currentBlock.NewStore(msg, ptr)
			c.declareVar(nombreVar, cgVar{ptr: ptr, tip: TipoCadena})
		} else {
			c.currentBlock.NewCall(c.rtExcepcionDescartar)
		}
		for _, s := range cuerpoNodo.Hijos {
			if err := c.emitirSentencia(s); err != nil {
				return err
			}
			if c.blockTerminated {
				break
			}
		}
		c.popScope()
		if !c.blockTerminated {
			// El catch completó normal: descartar la reserva del mensaje
			// (si 'como var' la generó) y saltar al finally con
			// "no relanzar".
			c.currentBlock.NewCall(c.rtExcepcionDescartar)
			c.currentBlock.NewBr(finallyBlock)
			incomingFinally = append(incomingFinally,
				ir.NewIncoming(constant.NewInt(types.I1, 0), c.currentBlock))
		}
		c.blockTerminated = false

		if tipoError == "" {
			huboCatchAll = true
			break
		}
		// El próximo 'capturar' de este mismo 'intentar' (si lo hay)
		// continúa probando desde el bloque que dejamos preparado arriba
		// para el caso de "no hizo match".
		c.currentBlock = proximoTest
	}

	if !huboCatchAll {
		// Llegamos al último bloque de prueba sin haber entrado a ningún
		// catch: hay que relanzar la excepción después del finally. Este
		// bloque es el predecesor real de finallyBlock por este camino.
		c.currentBlock.NewBr(finallyBlock)
		incomingFinally = append(incomingFinally,
			ir.NewIncoming(constant.NewInt(types.I1, 1), c.currentBlock))
	}

	// Si absolutamente ningún camino llegó a finallyBlock (todos
	// terminaron por su cuenta dentro del try o de algún catch), endBlock
	// queda sin predecesores: hay que propagar blockTerminated = true
	// para que quien llamó no intente seguir emitiendo código
	// inalcanzable (y para que emitirFuncion no se queje de "puede
	// terminar sin retornar" cuando en realidad sí retorna).
	if len(incomingFinally) == 0 {
		if len(c.tryFrames) > nivelAlEntrar {
			c.tryFrames = c.tryFrames[:nivelAlEntrar]
		}
		// FIX(bug): si ningún camino llegó a finallyBlock, ni
		// 'finallyBlock' ni 'endBlock' tienen predecesores: todos los
		// caminos del try y de los catch terminaron por su cuenta
		// (retornar/lanzar/romper/continuar). LLVM exige terminador en
		// TODO basic block, incluso en los inalcanzables, así que hay
		// que ponerles 'unreachable' a los dos. Antes esto solo se
		// hacía (a medias) en la rama sin capturas, y en esta rama se
		// dejaban ambos bloques vacíos y sin terminador, haciendo que
		// llir/llvm paniqueara con "missing terminator in basic block"
		// al serializar el módulo (ver el panic en Generate).
		finallyBlock.NewUnreachable()
		endBlock.NewUnreachable()
		c.currentBlock = endBlock
		c.blockTerminated = true
		return nil
	}

	// --- finally unificado ---
	c.currentBlock = finallyBlock
	c.blockTerminated = false
	debeRelanzar := finallyBlock.NewPhi(incomingFinally...)
	if err := frame.emitirFinally(); err != nil {
		return err
	}
	llegoAEnd := false
	if !c.blockTerminated {
		if relanzarBlock != nil {
			c.currentBlock.NewCondBr(debeRelanzar, relanzarBlock, endBlock)
		} else {
			c.currentBlock.NewBr(endBlock)
		}
		llegoAEnd = true
	}
	if relanzarBlock != nil {
		c.currentBlock = relanzarBlock
		c.blockTerminated = false
		c.currentBlock.NewCall(c.rtRelanzar)
		c.currentBlock.NewUnreachable()
	}

	// El frame ya no puede ser destino al continuar por el camino normal.
	if len(c.tryFrames) > nivelAlEntrar {
		c.tryFrames = c.tryFrames[:nivelAlEntrar]
	}

	c.currentBlock = endBlock
	c.blockTerminated = !llegoAEnd
	return nil
}

func hayCatchAll(bloques *Nodo) bool {
	for _, capturar := range bloques.Hijos {
		tipo, _ := capturar.Valor.(string)
		if tipo == "" {
			return true
		}
	}
	return false
}

func (c *Codegen) emitirPara(n *Nodo) error {
	varNodo := n.Hijos[0]
	iterableNodo := n.Hijos[1]
	bloqueNodo := n.Hijos[2]

	nombreVar, _ := varNodo.Valor.(string)

	iterable, err := c.emitirExpr(iterableNodo, nil)
	if err != nil {
		return err
	}

	var elemTipo Tipo
	var listRef value.Value
	iterandoDiccionario := false
	iterandoCadena := false

	switch it := iterable.tip.(type) {
	case *TipoLista:
		elemTipo = it.Elemento
		listRef = iterable.ref
	case *TipoDiccionario:
		kSuf, _, ok := primSuffix(it.Clave)
		if !ok {
			return fmt.Errorf("línea %d: tipo de clave de diccionario no soportado para iteración", n.Linea)
		}
		keysFn, ok := c.rtDictKeys[kSuf]
		if !ok {
			return fmt.Errorf("línea %d: no se pudieron obtener las claves del diccionario", n.Linea)
		}
		listRef = c.currentBlock.NewCall(keysFn, iterable.ref)
		elemTipo = it.Clave
		iterandoDiccionario = true
	default:
		if iterable.tip != nil && iterable.tip.Igual(TipoCadena) {
			// Recorre la cadena byte a byte; cada elemento es en sí
			// mismo una cadena de 1 byte (Wini no tiene tipo 'caracter').
			// A diferencia de una lista/diccionario, 'listRef' acá NO es
			// un contenedor temporal: es la cadena original tal cual la
			// pasó quien llamó a 'para ... en', así que no se libera al
			// terminar el bucle (ver limpiezaListaFn más abajo).
			elemTipo = TipoCadena
			listRef = iterable.ref
			iterandoCadena = true
		} else {
			return fmt.Errorf("línea %d: 'para ... en' requiere una lista, un diccionario o una cadena, se encontró %s", n.Linea, iterable.tip)
		}
	}

	condBlock := c.currentFunc.NewBlock(c.newLabel("for.cond"))
	bodyBlock := c.currentFunc.NewBlock(c.newLabel("for.body"))
	incBlock := c.currentFunc.NewBlock(c.newLabel("for.inc"))
	endBlock := c.currentFunc.NewBlock(c.newLabel("for.end"))

	listPtrVar := c.entryBlock.NewAlloca(types.NewPointer(types.I8))
	listOpq := c.currentBlock.NewBitCast(listRef, types.NewPointer(types.I8))
	c.currentBlock.NewStore(listOpq, listPtrVar)

	idxPtr := c.entryBlock.NewAlloca(types.I64)
	c.currentBlock.NewStore(constant.NewInt(types.I64, 0), idxPtr)

	c.currentBlock.NewBr(condBlock)

	c.currentBlock = condBlock
	listVal := c.currentBlock.NewLoad(types.NewPointer(types.I8), listPtrVar)
	var lenCall value.Value
	if iterandoCadena {
		lenCall = c.currentBlock.NewCall(c.rtStringLen, listVal)
	} else {
		lenCall = c.currentBlock.NewCall(c.rtListLen, listVal)
	}
	idxVal := c.currentBlock.NewLoad(types.I64, idxPtr)
	cmp := c.currentBlock.NewICmp(enum.IPredSLT, idxVal, lenCall)
	c.currentBlock.NewCondBr(cmp, bodyBlock, endBlock)

	c.currentBlock = bodyBlock
	var getCall value.Value
	// Aunque en este contexto el índice siempre cae dentro de rango (la
	// condición del for lo garantiza), pasamos la línea por consistencia
	// con la firma de las funciones de runtime.
	linea := constant.NewInt(types.I64, int64(n.Linea))
	if iterandoCadena {
		getCall = c.currentBlock.NewCall(c.rtStringGetChar, listVal, idxVal, linea)
	} else {
		switch {
		case elemTipo.Igual(TipoEntero):
			getCall = c.currentBlock.NewCall(c.rtListGetI64, listVal, idxVal, linea)
		case elemTipo.Igual(TipoDecimal):
			getCall = c.currentBlock.NewCall(c.rtListGetF64, listVal, idxVal, linea)
		case elemTipo.Igual(TipoBooleano):
			getCall = c.currentBlock.NewCall(c.rtListGetI1, listVal, idxVal, linea)
		case elemTipo.Igual(TipoCadena):
			getCall = c.currentBlock.NewCall(c.rtListGetStr, listVal, idxVal, linea)
		default:
			return fmt.Errorf("iteración sobre lista de %s no soportada", elemTipo)
		}
	}
	varPtr := c.entryBlock.NewAlloca(c.llvmType(elemTipo))
	// El elemento leído aquí sigue siendo propiedad de la lista/diccionario
	// de origen (get_str no transfiere dueño): la variable del 'para'
	// necesita su propia referencia retenida, para poder liberarla dentro
	// del cuerpo del bucle (p. ej. con 'liberar(x)') sin afectar a la
	// lista/diccionario original. 'wini_string_get_char', en cambio, ya
	// devuelve una cadena NUEVA (rc=1) sin más dueño que esta variable:
	// retenerla de nuevo la dejaría en rc=2 y con eso, fugada (nunca
	// llegaría a 0 con un solo 'liberar' por iteración).
	if !iterandoCadena {
		getCall = c.emitirRetenerValor(getCall, elemTipo)
	}
	c.currentBlock.NewStore(getCall, varPtr)
	c.pushScope()
	c.declareVar(nombreVar, cgVar{ptr: varPtr, tip: elemTipo})

	limpiezaIterFn := func() {
		if esTipoHeap(elemTipo) {
			v := c.currentBlock.NewLoad(c.llvmType(elemTipo), varPtr)
			c.emitirLiberarValor(v, elemTipo)
		}
	}
	limpiezaListaFn := func() {
		if iterandoDiccionario {
			c.emitirLiberarValor(listRef, NewTipoLista(elemTipo))
		}
	}
	// FIX 2.3c: libera el iterable original (la lista/diccionario/cadena
	// que se pasó a 'para ... en') si era un valor temporal. Para
	// diccionario, esto es DISTINTO de limpiezaListaFn: esa libera la
	// lista de claves que se pidió prestada para iterar; esta libera el
	// diccionario en sí. Para lista/cadena, 'iterable.ref' es el mismo
	// puntero que 'listRef' (se itera directo sobre él), así que alcanza
	// con esta única liberación.
	limpiezaIterableFn := func() {
		if esTipoHeap(iterable.tip) && !iterable.compartido {
			c.emitirLiberarValor(iterable.ref, iterable.tip)
		}
	}

	c.loops = append(c.loops, loopLabels{
		cond:                  incBlock,
		end:                   endBlock,
		nombreVar:             nombreVar,
		limpiezaIter:          limpiezaIterFn,
		limpiezaListaTemporal: limpiezaListaFn,
		limpiezaIterable:      limpiezaIterableFn,
		tryFramesAlEntrar:     len(c.tryFrames),
	})
	for _, s := range bloqueNodo.Hijos {
		if err := c.emitirSentencia(s); err != nil {
			return err
		}
		if c.blockTerminated {
			break
		}
	}
	c.loops = c.loops[:len(c.loops)-1]

	if !c.blockTerminated {
		c.currentBlock.NewBr(incBlock)
	}
	c.popScope()

	c.currentBlock = incBlock
	// La referencia que retuvimos para la variable del 'para' ya cumplió
	// su ciclo de vida en esta iteración (si el cuerpo la guardó en otro
	// lado, ese destino ya la retuvo por su cuenta). La liberamos aquí;
	// si el cuerpo ya la había liberado explícitamente con 'liberar(x)',
	// esto vuelve a llamar sobre un puntero nulo, lo cual es seguro.
	limpiezaIterFn()
	idxVal = c.currentBlock.NewLoad(types.I64, idxPtr)
	one := constant.NewInt(types.I64, 1)
	newIdx := c.currentBlock.NewAdd(idxVal, one)
	c.currentBlock.NewStore(newIdx, idxPtr)
	c.currentBlock.NewBr(condBlock)

	c.currentBlock = endBlock
	// 'listRef' es la lista de claves que pedimos prestada al diccionario
	// solo para iterar: es nuestra (rc propio), nadie más la referencia,
	// así que la liberamos al terminar el bucle. Este bloque se alcanza
	// tanto al terminar el bucle normalmente como al salir con 'romper'
	// (que salta directo aquí); 'retornar' se libera por separado (ver
	// el caso RETORNO), ya que ese camino nunca llega a este bloque.
	limpiezaListaFn()
	limpiezaIterableFn()
	c.blockTerminated = false
	return nil
}

func (c *Codegen) emitirExpr(n *Nodo, expected Tipo) (cgValue, error) {
	switch n.Tipo {
	case "ENTERO":
		texto, _ := n.Valor.(string)
		x, err := strconv.ParseInt(texto, 10, 64)
		if err != nil {
			return cgValue{}, fmt.Errorf("línea %d: entero inválido '%s'", n.Linea, texto)
		}
		return cgValue{ref: constant.NewInt(types.I64, x), tip: TipoEntero}, nil

	case "DECIMAL":
		texto, _ := n.Valor.(string)
		x, err := strconv.ParseFloat(texto, 64)
		if err != nil {
			return cgValue{}, fmt.Errorf("línea %d: decimal inválido '%s'", n.Linea, texto)
		}
		return cgValue{ref: constant.NewFloat(types.Double, x), tip: TipoDecimal}, nil

	case "BOOLEANO":
		return cgValue{ref: constant.NewBool(n.Valor.(bool)), tip: TipoBooleano}, nil

	case "NINGUNO":
		t := n.TipoInferido
		if t == nil {
			t = expected
		}
		if t == nil {
			return cgValue{}, fmt.Errorf("línea %d: no se puede determinar el tipo de 'nulo' en este contexto", n.Linea)
		}
		return cgValue{ref: c.valorPlaceholder(t), tip: t}, nil

	case "CADENA_TEXTO":
		texto, _ := n.Valor.(string)
		g := c.internString(texto)
		arrType := g.ContentType.(*types.ArrayType)
		gep := c.currentBlock.NewGetElementPtr(arrType, g,
			constant.NewInt(types.I64, 0), constant.NewInt(types.I64, 0))
		inst := c.currentBlock.NewCall(c.rtStringNew, gep, constant.NewInt(types.I64, int64(len([]byte(texto)))))
		return cgValue{ref: inst, tip: TipoCadena}, nil

	case "CADENA_INTERPOLADA":
		texto, _ := n.Valor.(string)
		literales, expresiones := parseInterpolatedString(texto)

		makeLiteral := func(lit string) value.Value {
			g := c.internString(lit)
			arrType := g.ContentType.(*types.ArrayType)
			gep := c.currentBlock.NewGetElementPtr(arrType, g,
				constant.NewInt(types.I64, 0), constant.NewInt(types.I64, 0))
			return c.currentBlock.NewCall(c.rtStringNew, gep, constant.NewInt(types.I64, int64(len(lit))))
		}

		// FIX 2.2: cada 'wini_string_concat' devuelve una cadena NUEVA
		// (rc=1); la 'result' anterior que reemplaza queda sin dueño si no
		// se libera. 'liberarIntermedio' libera incondicionalmente porque
		// solo se llama sobre cadenas que sabemos frescas: 'result' previo
		// (siempre viene de otro concat o de un literal, nunca de una
		// variable) y 'litStr' (siempre un literal recién construido).
		liberarIntermedio := func(v value.Value) {
			if v != nil {
				c.emitirLiberarValor(v, TipoCadena)
			}
		}

		var result value.Value
		if len(literales) > 0 {
			result = makeLiteral(literales[0])
		} else {
			result = makeLiteral("")
		}

		litIdx := 1
		for _, expr := range expresiones {
			tempNodo := &Nodo{Tipo: "IDENTIFICADOR", Valor: expr, Linea: n.Linea}
			val, err := c.emitirExpr(tempNodo, nil)
			if err != nil {
				return cgValue{}, err
			}

			var strVal value.Value
			// strValFresco indica si 'strVal' es una cadena recién
			// construida (por una conversión, o porque 'val' ya era una
			// cadena fresca) que hay que liberar después de concatenarla.
			// Si 'val' es una cadena compartida (viene de una variable,
			// '{variable}'), strVal == val.ref y NO se libera: le
			// pertenece a esa variable.
			strValFresco := false
			if val.tip.Igual(TipoCadena) {
				strVal = val.ref
				strValFresco = !val.compartido
			} else {
				switch {
				case val.tip.Igual(TipoEntero):
					strVal = c.currentBlock.NewCall(c.rtI64ToString, val.ref)
				case val.tip.Igual(TipoDecimal):
					strVal = c.currentBlock.NewCall(c.rtF64ToString, val.ref)
				case val.tip.Igual(TipoBooleano):
					strVal = c.currentBlock.NewCall(c.rtBoolToString, val.ref)
				default:
					return cgValue{}, fmt.Errorf("línea %d: interpolación de %s no soportada", n.Linea, val.tip)
				}
				strValFresco = true
			}

			nuevoResult := c.currentBlock.NewCall(c.rtStringConcat, result, strVal)
			liberarIntermedio(result)
			if strValFresco {
				liberarIntermedio(strVal)
			}
			result = nuevoResult

			if litIdx < len(literales) {
				litStr := makeLiteral(literales[litIdx])
				nuevoResult := c.currentBlock.NewCall(c.rtStringConcat, result, litStr)
				liberarIntermedio(result)
				liberarIntermedio(litStr)
				result = nuevoResult
				litIdx++
			}
		}

		return cgValue{ref: result, tip: TipoCadena}, nil

	case "IDENTIFICADOR":
		nombre, _ := n.Valor.(string)
		variable, ok := c.lookupVar(nombre)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: variable '%s' no declarada", n.Linea, nombre)
		}
		load := c.currentBlock.NewLoad(c.llvmType(variable.tip), variable.ptr)
		return cgValue{ref: load, tip: variable.tip, compartido: esTipoHeap(variable.tip)}, nil

	case "UNARIA":
		valor, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		op, _ := n.Valor.(string)
		if op != "-" {
			return cgValue{}, fmt.Errorf("línea %d: operador unario '%s' no soportado", n.Linea, op)
		}
		if valor.tip.Igual(TipoEntero) {
			neg := c.currentBlock.NewSub(constant.NewInt(types.I64, 0), valor.ref)
			return cgValue{ref: neg, tip: TipoEntero}, nil
		}
		if valor.tip.Igual(TipoDecimal) {
			neg := c.currentBlock.NewFNeg(valor.ref)
			return cgValue{ref: neg, tip: TipoDecimal}, nil
		}
		return cgValue{}, fmt.Errorf("línea %d: '-' unario solo soporta enteros y decimales", n.Linea)

	case "BINARIA":
		return c.emitirBinaria(n)

	case "LOGICO":
		return c.emitirLogico(n)

	case "IN":
		elemento, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		contenedor, err := c.emitirExpr(n.Hijos[1], nil)
		if err != nil {
			return cgValue{}, err
		}
		dictTipo, ok := contenedor.tip.(*TipoDiccionario)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: 'en' todavía solo está soportado en LLVM sobre diccionarios", n.Linea)
		}
		kSuf, _, ok1 := primSuffix(dictTipo.Clave)
		if !ok1 {
			return cgValue{}, fmt.Errorf("línea %d: tipo de clave de diccionario no soportado en codegen", n.Linea)
		}
		containsFn, ok := c.rtDictContains[kSuf]
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: tipo de clave de diccionario (%s) no soportado para 'en'", n.Linea, kSuf)
		}
		dictPtr := c.currentBlock.NewBitCast(contenedor.ref, types.NewPointer(types.I8))
		call := c.currentBlock.NewCall(containsFn, dictPtr, elemento.ref)
		// Igual que en la lectura por índice: 'contains' solo compara,
		// no se queda con la clave. Si es un valor recién construido que
		// no vino de una variable, se libera aquí.
		if esTipoHeap(elemento.tip) && !elemento.compartido {
			c.emitirLiberarValor(elemento.ref, elemento.tip)
		}
		// FIX 2.3b: 'contenedor' (el diccionario) solo se lee acá para la
		// búsqueda; si era un diccionario temporal, se fuga si no se
		// libera.
		c.liberarOperandoFrescoSiHeap(contenedor)
		return cgValue{ref: call, tip: TipoBooleano}, nil

	case "LLAMADA":
		return c.emitirLlamada(n)

	case "ACCESO_ATRIBUTO":
		base, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		s, ok := base.tip.(*TipoEstructura)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: no es una estructura", n.Linea)
		}
		_, idx, ok := s.Campo(n.Valor.(string))
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: campo inexistente", n.Linea)
		}
		ptr := c.currentBlock.NewAlloca(c.llvmType(s))
		c.currentBlock.NewStore(base.ref, ptr)
		fieldPtr := c.currentBlock.NewGetElementPtr(c.llvmType(s), ptr, constant.NewInt(types.I32, 0), constant.NewInt(types.I32, int64(idx)))
		return cgValue{ref: c.currentBlock.NewLoad(c.llvmType(s.Campos[idx].Tipo), fieldPtr), tip: s.Campos[idx].Tipo}, nil

	case "LISTA":
		if n.TipoInferido == nil {
			return cgValue{}, fmt.Errorf("línea %d: no se pudo inferir el tipo de la lista", n.Linea)
		}
		listTipo, ok := n.TipoInferido.(*TipoLista)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: el tipo inferido no es una lista", n.Linea)
		}
		elemTipo := listTipo.Elemento

		elementos := make([]cgValue, len(n.Hijos))
		for i, hijo := range n.Hijos {
			val, err := c.emitirExpr(hijo, nil)
			if err != nil {
				return cgValue{}, err
			}
			if !val.tip.Igual(elemTipo) {
				return cgValue{}, fmt.Errorf("línea %d: tipo de elemento no coincide con el tipo de lista", hijo.Linea)
			}
			elementos[i] = val
		}

		call, err := c.construirListaRuntime(elemTipo, elementos)
		if err != nil {
			return cgValue{}, fmt.Errorf("línea %d: %v", n.Linea, err)
		}

		return cgValue{ref: call, tip: listTipo}, nil

	case "INDEXACION":
		base, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}

		switch b := base.tip.(type) {
		case *TipoLista:
			idx, err := c.emitirExpr(n.Hijos[1], nil)
			if err != nil {
				return cgValue{}, err
			}
			if !idx.tip.Igual(TipoEntero) {
				return cgValue{}, fmt.Errorf("línea %d: el índice de una lista debe ser entero", n.Linea)
			}
			elemTipo := b.Elemento
			listPtr := c.currentBlock.NewBitCast(base.ref, types.NewPointer(types.I8))
			linea := constant.NewInt(types.I64, int64(n.Linea))
			var getCall value.Value
			switch {
			case elemTipo.Igual(TipoEntero):
				getCall = c.currentBlock.NewCall(c.rtListGetI64, listPtr, idx.ref, linea)
			case elemTipo.Igual(TipoDecimal):
				getCall = c.currentBlock.NewCall(c.rtListGetF64, listPtr, idx.ref, linea)
			case elemTipo.Igual(TipoBooleano):
				getCall = c.currentBlock.NewCall(c.rtListGetI1, listPtr, idx.ref, linea)
			case elemTipo.Igual(TipoCadena):
				getCall = c.currentBlock.NewCall(c.rtListGetStr, listPtr, idx.ref, linea)
			default:
				return cgValue{}, fmt.Errorf("acceso por índice a lista de %s no soportado", elemTipo)
			}
			// FIX 2.3a: si 'base' es fresco, el contenedor pudo haber
			// sido el único dueño del elemento. Antes de liberar 'base'
			// hay que retener el elemento (si es heap), o la lista
			// libera su única referencia y 'getCall' queda colgando.
			// El resultado pasa a ser dueño propio (compartido=false)
			// cuando retuvimos; si 'base' era compartido, seguimos siendo
			// un alias y el llamador debe retener.
			if esTipoHeap(elemTipo) && !base.compartido {
				getCall = c.emitirRetenerValor(getCall, elemTipo)
			}
			c.liberarOperandoFrescoSiHeap(base)
			compartidoRes := base.compartido && esTipoHeap(elemTipo)
			return cgValue{ref: getCall, tip: elemTipo, compartido: compartidoRes}, nil

		case *TipoDiccionario:
			idx, err := c.emitirExpr(n.Hijos[1], nil)
			if err != nil {
				return cgValue{}, err
			}
			if !idx.tip.Igual(b.Clave) {
				return cgValue{}, fmt.Errorf("línea %d: la clave usada para indexar no coincide con el tipo de clave del diccionario", n.Linea)
			}
			kSuf, _, ok1 := primSuffix(b.Clave)
			vSuf, _, ok2 := primSuffix(b.Valor)
			if !ok1 || !ok2 {
				return cgValue{}, fmt.Errorf("línea %d: tipo de clave/valor de diccionario no soportado en codegen", n.Linea)
			}
			getFn, ok := c.rtDictGet[kSuf+"_"+vSuf]
			if !ok {
				return cgValue{}, fmt.Errorf("línea %d: combinación de tipos de diccionario (%s, %s) no soportada", n.Linea, kSuf, vSuf)
			}
			dictPtr := c.currentBlock.NewBitCast(base.ref, types.NewPointer(types.I8))
			// FIX(bug): wini_dict_get_*_str ahora SIEMPRE consume
			// exactamente una referencia de la clave (tanto si la
			// encuentra como si lanza KeyError; ver runtime.c,
			// WINI_DICT_GET y wini_dict_key_error_str, y la nota de
			// ownership en runtime.h junto a estas funciones). Antes,
			// el runtime no tocaba la clave y ESTE codegen la liberaba
			// después de la llamada solo cuando era fresca — pero eso
			// dependía de que la llamada retornara normalmente, cosa
			// que no pasa cuando lanza KeyError (longjmp), así que una
			// clave recién construida (p. ej. un literal en d["k1"])
			// se perdía para siempre si la búsqueda fallaba.
			//
			// Con el nuevo contrato: si 'idx' es una clave recién
			// construida (no compartida con una variable), ya no
			// liberamos nada después de la llamada — el runtime se
			// queda con esa única referencia y la libera él mismo. Si
			// en cambio 'idx' viene de una variable existente
			// (compartido == true) que el programa Wini puede seguir
			// usando después de esta expresión, le damos al runtime su
			// PROPIA referencia retenida ANTES de la llamada, para que
			// consumir una referencia no le quite a la variable la
			// suya.
			if esTipoHeap(idx.tip) && idx.compartido {
				idx.ref = c.emitirRetenerValor(idx.ref, idx.tip)
			}
			// Declarado como value.Value (no *ir.InstCall) para poder
			// reasignarlo con el resultado de emitirRetenerValor, que
			// devuelve value.Value.
			var getCall value.Value = c.currentBlock.NewCall(getFn, dictPtr, idx.ref)
			// FIX 2.3a (mismo caso que lista, aplicado a diccionario):
			// si 'base' era fresco y el valor es heap, el diccionario
			// pudo ser el único dueño del valor. Retenemos el valor
			// ANTES de liberar 'base', o el valor quedaría colgando.
			if esTipoHeap(b.Valor) && !base.compartido {
				getCall = c.emitirRetenerValor(getCall, b.Valor)
			}
			c.liberarOperandoFrescoSiHeap(base)
			compartidoRes := base.compartido && esTipoHeap(b.Valor)
			return cgValue{ref: getCall, tip: b.Valor, compartido: compartidoRes}, nil

		default:
			if base.tip != nil && base.tip.Igual(TipoCadena) {
				idx, err := c.emitirExpr(n.Hijos[1], nil)
				if err != nil {
					return cgValue{}, err
				}
				if !idx.tip.Igual(TipoEntero) {
					return cgValue{}, fmt.Errorf("línea %d: el índice de una cadena debe ser entero", n.Linea)
				}
				linea := constant.NewInt(types.I64, int64(n.Linea))
				getCall := c.currentBlock.NewCall(c.rtStringGetChar, base.ref, idx.ref, linea)
				// A diferencia de una lista/diccionario, esta llamada
				// devuelve una cadena NUEVA (rc=1) sin más dueño que esta
				// expresión: no está "compartida" con nadie más, así que
				// no hace falta retenerla antes de guardarla o usarla (ver
				// retenerSiCompartido).
				// FIX 2.3a (mismo caso, aplicado a cadena): 'base' solo se
				// lee para extraer el carácter; si era una cadena temporal
				// (p. ej. '("abc")[0]'), se fuga si no se libera. El
				// resultado es fresco (rc=1 recién creado por
				// wini_string_get_char), no un alias: no hay que retener
				// nada antes de liberar 'base'.
				c.liberarOperandoFrescoSiHeap(base)
				return cgValue{ref: getCall, tip: TipoCadena, compartido: false}, nil
			}
			return cgValue{}, fmt.Errorf("no se puede indexar tipo %s", base.tip)
		}

	case "DICCIONARIO":
		if n.TipoInferido == nil {
			return cgValue{}, fmt.Errorf("línea %d: no se pudo inferir el tipo del diccionario", n.Linea)
		}
		dictTipo, ok := n.TipoInferido.(*TipoDiccionario)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: el tipo inferido no es un diccionario", n.Linea)
		}
		kSuf, _, ok1 := primSuffix(dictTipo.Clave)
		vSuf, _, ok2 := primSuffix(dictTipo.Valor)
		if !ok1 || !ok2 {
			return cgValue{}, fmt.Errorf("línea %d: tipo de clave/valor de diccionario no soportado en codegen", n.Linea)
		}
		setFn, ok := c.rtDictSet[kSuf+"_"+vSuf]
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: combinación de tipos de diccionario (%s, %s) no soportada", n.Linea, kSuf, vSuf)
		}

		dictCall := c.currentBlock.NewCall(c.rtDictNew)
		for _, par := range n.Hijos {
			claveVal, err := c.emitirExpr(par.Hijos[0], nil)
			if err != nil {
				return cgValue{}, err
			}
			if !claveVal.tip.Igual(dictTipo.Clave) {
				return cgValue{}, fmt.Errorf("línea %d: tipo de clave no coincide con el tipo del diccionario", par.Hijos[0].Linea)
			}
			valorVal, err := c.emitirExpr(par.Hijos[1], nil)
			if err != nil {
				return cgValue{}, err
			}
			if !valorVal.tip.Igual(dictTipo.Valor) {
				return cgValue{}, fmt.Errorf("línea %d: tipo de valor no coincide con el tipo del diccionario", par.Hijos[1].Linea)
			}
			// Igual que en las listas: retenemos clave/valor si vienen de
			// una ubicación ya existente, antes de que el diccionario se
			// quede con el puntero.
			claveVal = c.retenerSiCompartido(claveVal)
			valorVal = c.retenerSiCompartido(valorVal)
			c.currentBlock.NewCall(setFn, dictCall, claveVal.ref, valorVal.ref)
		}
		return cgValue{ref: dictCall, tip: dictTipo}, nil

	default:
		return cgValue{}, fmt.Errorf("línea %d: la base mínima no soporta la expresión '%s' en LLVM todavía", n.Linea, n.Tipo)
	}
}

// liberarOperandoFrescoSiHeap libera un operando de tipo heap que ya
// cumplió su función dentro de una operación que solo LEE su contenido
// sin quedarse con el puntero (concatenar, comparar): 'wini_string_concat'
// copia los bytes a un buffer nuevo, no reutiliza los operandos, así que
// si 'v' es un valor recién construido (un literal, otra concatenación,
// etc.) que no vino de ninguna variable, nadie más lo va a liberar. Si en
// cambio viene de una variable existente (compartido == true), NO se
// libera aquí: esa referencia le pertenece a la variable.
func (c *Codegen) liberarOperandoFrescoSiHeap(v cgValue) {
	if esTipoHeap(v.tip) && !v.compartido {
		c.emitirLiberarValor(v.ref, v.tip)
	}
}

func (c *Codegen) emitirBinaria(n *Nodo) (cgValue, error) {
	izq, err := c.emitirExpr(n.Hijos[0], nil)
	if err != nil {
		return cgValue{}, err
	}
	der, err := c.emitirExpr(n.Hijos[1], nil)
	if err != nil {
		return cgValue{}, err
	}
	op, _ := n.Valor.(string)

	if !izq.tip.Igual(der.tip) {
		return cgValue{}, fmt.Errorf("línea %d: operandos incompatibles en '%s'", n.Linea, op)
	}

	if esComparador(op) {
		return c.emitirComparacion(op, izq, der, n.Linea)
	}

	// --- Concatenación de cadenas con '+' ---
	if op == "+" && izq.tip.Igual(TipoCadena) && der.tip.Igual(TipoCadena) {
		call := c.currentBlock.NewCall(c.rtStringConcat, izq.ref, der.ref)
		c.liberarOperandoFrescoSiHeap(izq)
		c.liberarOperandoFrescoSiHeap(der)
		return cgValue{ref: call, tip: TipoCadena}, nil
	}

	// --- Concatenación de listas con '+' ---
	if op == "+" {
		if listA, ok := izq.tip.(*TipoLista); ok {
			if listB, ok := der.tip.(*TipoLista); ok && listA.Igual(listB) {
				code := tipoCode(listA.Elemento)
				if code == -1 {
					return cgValue{}, fmt.Errorf("línea %d: concatenación de listas de %s no soportada", n.Linea, listA.Elemento)
				}
				call := c.currentBlock.NewCall(c.rtListConcat, izq.ref, der.ref, constant.NewInt(types.I32, int64(code)))
				c.liberarOperandoFrescoSiHeap(izq)
				c.liberarOperandoFrescoSiHeap(der)
				return cgValue{ref: call, tip: izq.tip}, nil
			}
		}
	}

	// --- Operaciones aritméticas ---
	if izq.tip.Igual(TipoEntero) {
		var res value.Value
		switch op {
		case "+":
			res = c.currentBlock.NewAdd(izq.ref, der.ref)
		case "-":
			res = c.currentBlock.NewSub(izq.ref, der.ref)
		case "*":
			res = c.currentBlock.NewMul(izq.ref, der.ref)
		case "/":
			res = c.currentBlock.NewSDiv(izq.ref, der.ref)
		case "%":
			res = c.currentBlock.NewSRem(izq.ref, der.ref)
		default:
			return cgValue{}, fmt.Errorf("línea %d: operador '%s' no soportado para enteros", n.Linea, op)
		}
		return cgValue{ref: res, tip: TipoEntero}, nil
	}
	if izq.tip.Igual(TipoDecimal) {
		var res value.Value
		switch op {
		case "+":
			res = c.currentBlock.NewFAdd(izq.ref, der.ref)
		case "-":
			res = c.currentBlock.NewFSub(izq.ref, der.ref)
		case "*":
			res = c.currentBlock.NewFMul(izq.ref, der.ref)
		case "/":
			res = c.currentBlock.NewFDiv(izq.ref, der.ref)
		case "%":
			res = c.currentBlock.NewFRem(izq.ref, der.ref)
		default:
			return cgValue{}, fmt.Errorf("línea %d: operador '%s' no soportado para decimales", n.Linea, op)
		}
		return cgValue{ref: res, tip: TipoDecimal}, nil
	}
	return cgValue{}, fmt.Errorf("línea %d: operador '%s' no soportado para el tipo %s", n.Linea, op, izq.tip)
}

func (c *Codegen) emitirComparacion(op string, izq, der cgValue, linea int) (cgValue, error) {
	// Comparación de cadenas
	if izq.tip.Igual(TipoCadena) && der.tip.Igual(TipoCadena) {
		var fn *ir.Func
		switch op {
		case "==":
			fn = c.rtStringEq
		case "!=", "<>":
			fn = c.rtStringNe
		case "<":
			fn = c.rtStringLt
		case "<=":
			fn = c.rtStringLe
		case ">":
			fn = c.rtStringGt
		case ">=":
			fn = c.rtStringGe
		default:
			return cgValue{}, fmt.Errorf("línea %d: operador '%s' no soportado para cadenas", linea, op)
		}
		call := c.currentBlock.NewCall(fn, izq.ref, der.ref)
		c.liberarOperandoFrescoSiHeap(izq)
		c.liberarOperandoFrescoSiHeap(der)
		return cgValue{ref: call, tip: TipoBooleano}, nil
	}

	if izq.tip.Igual(TipoEntero) || izq.tip.Igual(TipoBooleano) {
		pred, ok := map[string]enum.IPred{
			"==": enum.IPredEQ, "!=": enum.IPredNE, "<>": enum.IPredNE,
			"<": enum.IPredSLT, "<=": enum.IPredSLE, ">": enum.IPredSGT, ">=": enum.IPredSGE,
		}[op]
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: comparador '%s' no soportado", linea, op)
		}
		res := c.currentBlock.NewICmp(pred, izq.ref, der.ref)
		return cgValue{ref: res, tip: TipoBooleano}, nil
	}
	if izq.tip.Igual(TipoDecimal) {
		pred, ok := map[string]enum.FPred{
			"==": enum.FPredOEQ, "!=": enum.FPredONE, "<>": enum.FPredONE,
			"<": enum.FPredOLT, "<=": enum.FPredOLE, ">": enum.FPredOGT, ">=": enum.FPredOGE,
		}[op]
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: comparador '%s' no soportado", linea, op)
		}
		res := c.currentBlock.NewFCmp(pred, izq.ref, der.ref)
		return cgValue{ref: res, tip: TipoBooleano}, nil
	}

	// Comparación de identidad (==, !=) para listas y diccionarios: se
	// representan como puntero opaco i8*, así que alcanza con comparar
	// el puntero. Cubre en particular 'x == nulo' / 'x != nulo'.
	switch izq.tip.(type) {
	case *TipoLista, *TipoDiccionario:
		pred, ok := map[string]enum.IPred{
			"==": enum.IPredEQ, "!=": enum.IPredNE, "<>": enum.IPredNE,
		}[op]
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: comparador '%s' no soportado para %s", linea, op, izq.tip)
		}
		res := c.currentBlock.NewICmp(pred, izq.ref, der.ref)
		return cgValue{ref: res, tip: TipoBooleano}, nil
	}

	return cgValue{}, fmt.Errorf("línea %d: comparaciones no soportadas para %s en la base mínima", linea, izq.tip)
}

func (c *Codegen) emitirLogico(n *Nodo) (cgValue, error) {
	op, _ := n.Valor.(string)

	if len(n.Hijos) == 1 {
		valor, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		if !valor.tip.Igual(TipoBooleano) {
			return cgValue{}, fmt.Errorf("línea %d: 'no' requiere booleano", n.Linea)
		}
		res := c.currentBlock.NewXor(valor.ref, constant.NewBool(true))
		return cgValue{ref: res, tip: TipoBooleano}, nil
	}

	izq, err := c.emitirExpr(n.Hijos[0], nil)
	if err != nil {
		return cgValue{}, err
	}
	if !izq.tip.Igual(TipoBooleano) {
		return cgValue{}, fmt.Errorf("línea %d: operador lógico requiere booleanos", n.Linea)
	}

	rhsBlock := c.currentFunc.NewBlock(c.newLabel("logic.rhs"))
	mergeBlock := c.currentFunc.NewBlock(c.newLabel("logic.merge"))
	prevBlock := c.currentBlock

	switch op {
	case "y", "and":
		c.currentBlock.NewCondBr(izq.ref, rhsBlock, mergeBlock)
		c.currentBlock = rhsBlock
		der, err := c.emitirExpr(n.Hijos[1], nil)
		if err != nil {
			return cgValue{}, err
		}
		if !der.tip.Igual(TipoBooleano) {
			return cgValue{}, fmt.Errorf("línea %d: operador lógico requiere booleanos", n.Linea)
		}
		rhsEnd := c.currentBlock
		rhsEnd.NewBr(mergeBlock)
		c.currentBlock = mergeBlock
		phi := mergeBlock.NewPhi(
			ir.NewIncoming(constant.NewBool(false), prevBlock),
			ir.NewIncoming(der.ref, rhsEnd),
		)
		return cgValue{ref: phi, tip: TipoBooleano}, nil

	case "o", "or":
		c.currentBlock.NewCondBr(izq.ref, mergeBlock, rhsBlock)
		c.currentBlock = rhsBlock
		der, err := c.emitirExpr(n.Hijos[1], nil)
		if err != nil {
			return cgValue{}, err
		}
		if !der.tip.Igual(TipoBooleano) {
			return cgValue{}, fmt.Errorf("línea %d: operador lógico requiere booleanos", n.Linea)
		}
		rhsEnd := c.currentBlock
		rhsEnd.NewBr(mergeBlock)
		c.currentBlock = mergeBlock
		phi := mergeBlock.NewPhi(
			ir.NewIncoming(constant.NewBool(true), prevBlock),
			ir.NewIncoming(der.ref, rhsEnd),
		)
		return cgValue{ref: phi, tip: TipoBooleano}, nil
	}

	return cgValue{}, fmt.Errorf("línea %d: operador lógico '%s' no soportado", n.Linea, op)
}

// construirListaRuntime arma en tiempo de ejecución una WiniList a partir
// de 'elementos' ya evaluados (todos del mismo tipo 'elemTipo'): reserva un
// arreglo en el stack, copia cada valor, y llama a la variante tipada de
// wini_list_new_*. Se comparte entre el literal '[...]' y la recolección de
// argumentos de un parámetro variádico estilo Wini ('*nombre:tipo').
func (c *Codegen) construirListaRuntime(elemTipo Tipo, elementos []cgValue) (value.Value, error) {
	var llvmElemType types.Type
	switch {
	case elemTipo.Igual(TipoEntero):
		llvmElemType = types.I64
	case elemTipo.Igual(TipoDecimal):
		llvmElemType = types.Double
	case elemTipo.Igual(TipoBooleano):
		llvmElemType = types.I1
	case elemTipo.Igual(TipoCadena):
		llvmElemType = types.NewPointer(types.I8)
	default:
		return nil, fmt.Errorf("tipo de elemento no soportado para lista: %s", elemTipo)
	}

	var rtListNew *ir.Func
	switch {
	case elemTipo.Igual(TipoEntero):
		rtListNew = c.rtListNewI64
	case elemTipo.Igual(TipoDecimal):
		rtListNew = c.rtListNewF64
	case elemTipo.Igual(TipoBooleano):
		rtListNew = c.rtListNewI1
	case elemTipo.Igual(TipoCadena):
		rtListNew = c.rtListNewStr
	default:
		return nil, fmt.Errorf("tipo de elemento no soportado para lista: %s", elemTipo)
	}

	if len(elementos) == 0 {
		zero := constant.NewInt(types.I64, 0)
		null := constant.NewNull(types.NewPointer(types.I8))
		return c.currentBlock.NewCall(rtListNew, null, zero), nil
	}

	numElem := uint64(len(elementos))
	arrType := types.NewArray(numElem, llvmElemType)
	arrPtr := c.entryBlock.NewAlloca(arrType)

	for i, val := range elementos {
		// Si el elemento viene de una variable existente (o de leer otra
		// lista/diccionario), el constructor de abajo copiará el puntero
		// tal cual: hay que retenerlo antes, o la lista resultante y la
		// fuente original terminan compartiendo el mismo valor sin que el
		// conteo de referencias se entere.
		val = c.retenerSiCompartido(val)
		idx := constant.NewInt(types.I64, int64(i))
		gep := c.currentBlock.NewGetElementPtr(arrType, arrPtr,
			constant.NewInt(types.I64, 0),
			idx)
		c.currentBlock.NewStore(val.ref, gep)
	}

	arrDataPtr := c.currentBlock.NewGetElementPtr(arrType, arrPtr,
		constant.NewInt(types.I64, 0),
		constant.NewInt(types.I64, 0))
	dataPtr := c.currentBlock.NewBitCast(arrDataPtr, types.NewPointer(types.I8))

	size := constant.NewInt(types.I64, int64(numElem))
	return c.currentBlock.NewCall(rtListNew, dataPtr, size), nil
}

func (c *Codegen) emitirLlamada(n *Nodo) (cgValue, error) {
	nombre, _ := n.Valor.(string)
	if s, ok := c.estructuras[nombre]; ok {
		if len(n.Hijos) != len(s.Campos) {
			return cgValue{}, fmt.Errorf("línea %d: cantidad de campos incorrecta", n.Linea)
		}
		var result value.Value = constant.NewZeroInitializer(c.llvmType(s))
		for i, argNodo := range n.Hijos {
			arg, err := c.emitirExpr(argNodo, s.Campos[i].Tipo)
			if err != nil {
				return cgValue{}, err
			}
			result = c.currentBlock.NewInsertValue(result, arg.ref, uint64(i))
		}
		return cgValue{ref: result, tip: s}, nil
	}

	// Built-in: claves(d) / valores(d)
	if nombre == "claves" || nombre == "valores" {
		if len(n.Hijos) != 1 {
			return cgValue{}, fmt.Errorf("línea %d: '%s' espera exactamente 1 argumento", n.Linea, nombre)
		}
		arg, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		dictTipo, ok := arg.tip.(*TipoDiccionario)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: '%s' espera un diccionario, se encontró %s", n.Linea, nombre, arg.tip)
		}
		dictPtr := c.currentBlock.NewBitCast(arg.ref, types.NewPointer(types.I8))
		if nombre == "claves" {
			kSuf, _, ok := primSuffix(dictTipo.Clave)
			if !ok {
				return cgValue{}, fmt.Errorf("línea %d: tipo de clave de diccionario no soportado en codegen", n.Linea)
			}
			fn, ok := c.rtDictKeys[kSuf]
			if !ok {
				return cgValue{}, fmt.Errorf("línea %d: tipo de clave de diccionario (%s) no soportado para 'claves'", n.Linea, kSuf)
			}
			call := c.currentBlock.NewCall(fn, dictPtr)
			// FIX 2.1a: si 'arg' era un diccionario temporal (recién
			// construido, no una variable), 'claves' termina de leerlo acá
			// y nadie más lo va a usar: hay que liberarlo para no fugarlo.
			c.liberarOperandoFrescoSiHeap(arg)
			return cgValue{ref: call, tip: NewTipoLista(dictTipo.Clave)}, nil
		}
		vSuf, _, ok := primSuffix(dictTipo.Valor)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: tipo de valor de diccionario no soportado en codegen", n.Linea)
		}
		fn, ok := c.rtDictValues[vSuf]
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: tipo de valor de diccionario (%s) no soportado para 'valores'", n.Linea, vSuf)
		}
		call := c.currentBlock.NewCall(fn, dictPtr)
		// FIX 2.1a: mismo caso que 'claves', para 'valores'.
		c.liberarOperandoFrescoSiHeap(arg)
		return cgValue{ref: call, tip: NewTipoLista(dictTipo.Valor)}, nil
	}

	// Built-in: range
	if nombre == "rango" {
		if len(n.Hijos) < 1 || len(n.Hijos) > 3 {
			return cgValue{}, fmt.Errorf("línea %d: rango espera 1, 2 o 3 argumentos", n.Linea)
		}
		var start, end, step cgValue
		var err error
		switch len(n.Hijos) {
		case 1:
			start = cgValue{ref: constant.NewInt(types.I64, 0), tip: TipoEntero}
			end, err = c.emitirExpr(n.Hijos[0], nil)
			if err != nil {
				return cgValue{}, err
			}
			step = cgValue{ref: constant.NewInt(types.I64, 1), tip: TipoEntero}
		case 2:
			start, err = c.emitirExpr(n.Hijos[0], nil)
			if err != nil {
				return cgValue{}, err
			}
			end, err = c.emitirExpr(n.Hijos[1], nil)
			if err != nil {
				return cgValue{}, err
			}
			step = cgValue{ref: constant.NewInt(types.I64, 1), tip: TipoEntero}
		case 3:
			start, err = c.emitirExpr(n.Hijos[0], nil)
			if err != nil {
				return cgValue{}, err
			}
			end, err = c.emitirExpr(n.Hijos[1], nil)
			if err != nil {
				return cgValue{}, err
			}
			step, err = c.emitirExpr(n.Hijos[2], nil)
			if err != nil {
				return cgValue{}, err
			}
		}
		call := c.currentBlock.NewCall(c.rtRangeI64, start.ref, end.ref, step.ref)
		return cgValue{ref: call, tip: NewTipoLista(TipoEntero)}, nil
	}

	// Built-in: leer
	if nombre == "leer" {
		if len(n.Hijos) != 1 {
			return cgValue{}, fmt.Errorf("línea %d: 'leer' espera exactamente 1 argumento (el prompt)", n.Linea)
		}
		prompt, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		if !prompt.tip.Igual(TipoCadena) {
			return cgValue{}, fmt.Errorf("línea %d: el prompt de 'leer' debe ser una cadena", n.Linea)
		}
		call := c.currentBlock.NewCall(c.rtReadString, prompt.ref)
		// FIX 2.1b: si 'prompt' era una cadena temporal (p. ej.
		// 'leer("a" + "b")'), 'leer' ya la usó y nadie más la referencia.
		c.liberarOperandoFrescoSiHeap(prompt)
		return cgValue{ref: call, tip: TipoCadena}, nil
	}

	// Built-in: escribir
	if nombre == "escribir" {
		for _, arg := range n.Hijos {
			v, err := c.emitirExpr(arg, nil)
			if err != nil {
				return cgValue{}, err
			}
			switch {
			case v.tip.Igual(TipoEntero):
				c.currentBlock.NewCall(c.rtPrintI64, v.ref)
			case v.tip.Igual(TipoDecimal):
				c.currentBlock.NewCall(c.rtPrintF64, v.ref)
			case v.tip.Igual(TipoBooleano):
				c.currentBlock.NewCall(c.rtPrintBool, v.ref)
			case v.tip.Igual(TipoCadena):
				c.currentBlock.NewCall(c.rtPrintString, v.ref)
			default:
				if listTipo, ok := v.tip.(*TipoLista); ok {
					elemTipo := listTipo.Elemento
					code := tipoCode(elemTipo)
					if code == -1 {
						return cgValue{}, fmt.Errorf("línea %d: imprimir listas de %s no soportado", n.Linea, elemTipo)
					}
					listPtr := c.currentBlock.NewBitCast(v.ref, types.NewPointer(types.I8))
					c.currentBlock.NewCall(c.rtPrintList, listPtr, constant.NewInt(types.I32, int64(code)))
				} else if dictTipo, ok := v.tip.(*TipoDiccionario); ok {
					claveCode := tipoCode(dictTipo.Clave)
					valorCode := tipoCode(dictTipo.Valor)
					if claveCode == -1 || valorCode == -1 {
						return cgValue{}, fmt.Errorf("línea %d: imprimir diccionario con tipos no soportados", n.Linea)
					}
					dictPtr := c.currentBlock.NewBitCast(v.ref, types.NewPointer(types.I8))
					c.currentBlock.NewCall(c.rtPrintDict, dictPtr,
						constant.NewInt(types.I32, int64(claveCode)),
						constant.NewInt(types.I32, int64(valorCode)))
				} else {
					return cgValue{}, fmt.Errorf("línea %d: 'escribir' aún no soporta %s", n.Linea, v.tip)
				}
			}
			// 'escribir' solo lee el valor para imprimirlo, no se queda
			// con el puntero: si es un valor recién construido (no vino
			// de una variable), lo liberamos aquí para no perderlo.
			c.liberarOperandoFrescoSiHeap(v)
		}
		return cgValue{ref: nil, tip: nil}, nil
	}

	// Built-in: liberar
	if nombre == "liberar" {
		if len(n.Hijos) != 1 {
			return cgValue{}, fmt.Errorf("línea %d: 'liberar' espera exactamente 1 argumento", n.Linea)
		}
		v, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		switch {
		case v.tip.Igual(TipoCadena):
			c.currentBlock.NewCall(c.rtLiberarCadena, v.ref)
		default:
			if listTipo, ok := v.tip.(*TipoLista); ok {
				code := tipoCode(listTipo.Elemento)
				if code == -1 {
					return cgValue{}, fmt.Errorf("línea %d: liberar listas de %s no soportado", n.Linea, listTipo.Elemento)
				}
				listPtr := c.currentBlock.NewBitCast(v.ref, types.NewPointer(types.I8))
				c.currentBlock.NewCall(c.rtLiberarLista, listPtr, constant.NewInt(types.I32, int64(code)))
			} else if dictTipo, ok := v.tip.(*TipoDiccionario); ok {
				claveCode := tipoCode(dictTipo.Clave)
				valorCode := tipoCode(dictTipo.Valor)
				if claveCode == -1 || valorCode == -1 {
					return cgValue{}, fmt.Errorf("línea %d: liberar diccionario con tipos no soportados", n.Linea)
				}
				dictPtr := c.currentBlock.NewBitCast(v.ref, types.NewPointer(types.I8))
				c.currentBlock.NewCall(c.rtLiberarDiccionario, dictPtr,
					constant.NewInt(types.I32, int64(claveCode)),
					constant.NewInt(types.I32, int64(valorCode)))
			} else {
				return cgValue{}, fmt.Errorf("línea %d: 'liberar' aún no soporta %s", n.Linea, v.tip)
			}
		}
		// Si el argumento es una variable simple, anulamos su puntero
		// justo después de liberar la memoria (use-after-free se
		// convierte en "puntero nulo" en vez de acceso a memoria ya
		// liberada). wini_print_string/wini_print_list/wini_print_dict
		// ya contemplan el puntero nulo y muestran "<nulo>"/"[]"/"{}"
		// en lugar de hacer segfault.
		if n.Hijos[0].Tipo == "IDENTIFICADOR" {
			if nombreVar, ok := n.Hijos[0].Valor.(string); ok {
				if variable, ok := c.lookupVar(nombreVar); ok {
					nullPtr := constant.NewNull(types.NewPointer(types.I8))
					c.currentBlock.NewStore(nullPtr, variable.ptr)
				}
			}
		}
		return cgValue{ref: nil, tip: nil}, nil
	}

	// Built-in: tipo — nombre del tipo de dato como cadena. Wini es de
	// tipado estático, así que el nombre se conoce en tiempo de
	// compilación (usando Tipo.String(): "entero", "cadena",
	// "lista<entero>", "diccionario<cadena, entero>", etc). Igual
	// evaluamos el argumento por si tiene efectos secundarios.
	if nombre == "tipo" {
		if len(n.Hijos) != 1 {
			return cgValue{}, fmt.Errorf("línea %d: 'tipo' espera exactamente 1 argumento", n.Linea)
		}
		// FIX 2.1c: hay que quedarse con el cgValue para poder liberarlo
		// si es heap y fresco — el nombre del tipo sale del AST
		// (n.Hijos[0].TipoInferido), no del valor en sí, así que 'v' no se
		// usa para nada más que esta liberación.
		v, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		c.liberarOperandoFrescoSiHeap(v)
		argTipo := n.Hijos[0].TipoInferido
		if argTipo == nil {
			return cgValue{}, fmt.Errorf("línea %d: no se pudo determinar el tipo del argumento de 'tipo'", n.Linea)
		}
		nombreTipo := argTipo.String()
		g := c.internString(nombreTipo)
		arrType := g.ContentType.(*types.ArrayType)
		gep := c.currentBlock.NewGetElementPtr(arrType, g,
			constant.NewInt(types.I64, 0), constant.NewInt(types.I64, 0))
		inst := c.currentBlock.NewCall(c.rtStringNew, gep, constant.NewInt(types.I64, int64(len([]byte(nombreTipo)))))
		return cgValue{ref: inst, tip: TipoCadena}, nil
	}

	// Conversion a primitivo
	if t, ok := TipoPrimitivoDesdeNombre(nombre); ok {
		if len(n.Hijos) != 1 {
			return cgValue{}, fmt.Errorf("línea %d: la conversión '%s' espera 1 argumento", n.Linea, nombre)
		}
		return c.emitirConversion(t, n.Hijos[0], n.Linea)
	}

	// Función definida por el usuario
	firma, ok := c.funciones[nombre]
	if !ok {
		return cgValue{}, fmt.Errorf("línea %d: función '%s' no soportada o no declarada", n.Linea, nombre)
	}

	numFijos := len(firma.Parametros)
	var ultimoEsLista *TipoLista
	if numFijos > 0 {
		ultimoEsLista, _ = firma.Parametros[numFijos-1].(*TipoLista)
	}
	// aceptaExtra: puede recibir más argumentos posicionales que
	// firma.Parametros, ya sea porque el último parámetro es una lista
	// (variádico "a la Wini": los sueltos se recolectan ahí) o porque la
	// función es 'externa ...(...)' (variádico C real: se pasan tal cual).
	aceptaExtra := firma.VariadicoC || ultimoEsLista != nil

	if len(n.Hijos) < firma.Requeridos {
		return cgValue{}, fmt.Errorf("línea %d: llamada a '%s' espera al menos %d argumento(s), se encontraron %d", n.Linea, nombre, firma.Requeridos, len(n.Hijos))
	}
	if len(n.Hijos) > numFijos && !aceptaExtra {
		return cgValue{}, fmt.Errorf("línea %d: llamada a '%s' espera entre %d y %d argumentos, se encontraron %d", n.Linea, nombre, firma.Requeridos, numFijos, len(n.Hijos))
	}

	// recolectaEnLista: hay más argumentos que parámetros fijos Y el
	// último parámetro es una lista, así que esos excedentes son
	// elementos sueltos a recolectar en una lista real antes de llamar
	// (en vez del caso normal de pasar directamente una única lista ya
	// construida, que sigue andando cuando la cantidad de argumentos
	// coincide exactamente con la cantidad de parámetros).
	recolectaEnLista := ultimoEsLista != nil && len(n.Hijos) > numFijos
	limiteFijos := numFijos
	if recolectaEnLista {
		limiteFijos = numFijos - 1
	}

	// args trae un valor por cada parámetro (real si el llamador lo pasó,
	// o un placeholder si se omitió); dados trae, en el mismo orden, un
	// booleano por cada parámetro con valor por defecto indicando si el
	// valor es real o hay que calcular el default dentro de la función.
	args := make([]value.Value, 0, len(n.Hijos))
	dados := make([]value.Value, 0, numFijos-firma.Requeridos)
	for i := 0; i < limiteFijos; i++ {
		tieneDefault := i >= firma.Requeridos
		if i < len(n.Hijos) {
			arg := n.Hijos[i]
			if arg.Tipo == "ARG_NOMBRADO" {
				return cgValue{}, fmt.Errorf("línea %d: la base mínima aún no soporta argumentos nombrados en LLVM", arg.Linea)
			}
			v, err := c.emitirExpr(arg, firma.Parametros[i])
			if err != nil {
				return cgValue{}, err
			}
			if !v.tip.Igual(firma.Parametros[i]) {
				return cgValue{}, fmt.Errorf("línea %d: argumento %d incompatible en llamada a '%s'", arg.Linea, i+1, nombre)
			}
			// La función que llamamos va a liberar este parámetro por su
			// cuenta al retornar (salvo que lo devuelva tal cual): si el
			// argumento viene de una variable existente, retenemos una
			// referencia extra para ella, o la función terminaría
			// liberando la única referencia que tenía nuestra variable.
			v = c.retenerSiCompartido(v)
			args = append(args, v.ref)
			if tieneDefault {
				dados = append(dados, constant.NewBool(true))
			}
		} else {
			// Argumento omitido: el valor real se calcula dentro de la
			// función llamada, así que acá pasamos un placeholder que
			// nunca se lee.
			args = append(args, c.valorPlaceholder(firma.Parametros[i]))
			dados = append(dados, constant.NewBool(false))
		}
	}

	if recolectaEnLista {
		elemTipo := ultimoEsLista.Elemento
		elementos := make([]cgValue, 0, len(n.Hijos)-(numFijos-1))
		for _, arg := range n.Hijos[numFijos-1:] {
			if arg.Tipo == "ARG_NOMBRADO" {
				return cgValue{}, fmt.Errorf("línea %d: la base mínima aún no soporta argumentos nombrados en LLVM", arg.Linea)
			}
			v, err := c.emitirExpr(arg, elemTipo)
			if err != nil {
				return cgValue{}, err
			}
			if !v.tip.Igual(elemTipo) {
				return cgValue{}, fmt.Errorf("línea %d: los argumentos variádicos de '%s' deben ser %s, se encontró %s", arg.Linea, nombre, elemTipo, v.tip)
			}
			elementos = append(elementos, v)
		}
		listaVal, err := c.construirListaRuntime(elemTipo, elementos)
		if err != nil {
			return cgValue{}, fmt.Errorf("línea %d: %v", n.Linea, err)
		}
		args = append(args, listaVal)
		// El parámetro variádico "a la Wini" nunca tiene valor por
		// defecto (el parser ya lo prohíbe), así que no suma flag "dado".
	}

	// Argumentos extra "a la C" (solo si firma.VariadicoC, p.ej. printf):
	// se pasan tal cual se evalúan, sin tipo esperado ni conversión ni
	// recolección — igual que en cualquier llamada real a una función
	// variádica de C.
	if firma.VariadicoC {
		for _, arg := range n.Hijos[numFijos:] {
			if arg.Tipo == "ARG_NOMBRADO" {
				return cgValue{}, fmt.Errorf("línea %d: la base mínima aún no soporta argumentos nombrados en LLVM", arg.Linea)
			}
			v, err := c.emitirExpr(arg, nil)
			if err != nil {
				return cgValue{}, err
			}
			args = append(args, v.ref)
		}
	}

	args = append(args, dados...)
	callee := c.llvmFuncs[nombre]
	call := c.currentBlock.NewCall(callee, args...)
	return cgValue{ref: call, tip: firma.Retorno}, nil
}

func (c *Codegen) emitirConversion(destino Tipo, arg *Nodo, linea int) (cgValue, error) {
	v, err := c.emitirExpr(arg, nil)
	if err != nil {
		return cgValue{}, err
	}
	if v.tip.Igual(destino) {
		// FIX 1.2: conversión identidad (mismo tipo origen/destino). Si el
		// valor ya existía en otra ubicación (v.compartido == true), el
		// resultado sigue siendo esa misma referencia compartida, no una
		// nueva: hay que propagar el flag para que quien reciba este
		// cgValue (p. ej. al guardarlo en una variable) sepa que debe
		// retenerlo antes de quedarse con él. Para tipos no-heap el flag
		// da igual (no se retiene/libera nada), así que propagarlo
		// siempre es seguro.
		return cgValue{ref: v.ref, tip: destino, compartido: v.compartido}, nil
	}
	switch {
	case destino.Igual(TipoDecimal) && v.tip.Igual(TipoEntero):
		res := c.currentBlock.NewSIToFP(v.ref, types.Double)
		return cgValue{ref: res, tip: TipoDecimal}, nil
	case destino.Igual(TipoEntero) && v.tip.Igual(TipoDecimal):
		res := c.currentBlock.NewFPToSI(v.ref, types.I64)
		return cgValue{ref: res, tip: TipoEntero}, nil
	case destino.Igual(TipoBooleano) && v.tip.Igual(TipoEntero):
		res := c.currentBlock.NewICmp(enum.IPredNE, v.ref, constant.NewInt(types.I64, 0))
		return cgValue{ref: res, tip: TipoBooleano}, nil
	case destino.Igual(TipoBooleano) && v.tip.Igual(TipoDecimal):
		res := c.currentBlock.NewFCmp(enum.FPredONE, v.ref, constant.NewFloat(types.Double, 0.0))
		return cgValue{ref: res, tip: TipoBooleano}, nil
	case destino.Igual(TipoEntero) && v.tip.Igual(TipoBooleano):
		res := c.currentBlock.NewZExt(v.ref, types.I64)
		return cgValue{ref: res, tip: TipoEntero}, nil
	case destino.Igual(TipoDecimal) && v.tip.Igual(TipoBooleano):
		res := c.currentBlock.NewUIToFP(v.ref, types.Double)
		return cgValue{ref: res, tip: TipoDecimal}, nil
	case destino.Igual(TipoCadena) && v.tip.Igual(TipoEntero):
		res := c.currentBlock.NewCall(c.rtI64ToString, v.ref)
		return cgValue{ref: res, tip: TipoCadena}, nil
	case destino.Igual(TipoCadena) && v.tip.Igual(TipoDecimal):
		res := c.currentBlock.NewCall(c.rtF64ToString, v.ref)
		return cgValue{ref: res, tip: TipoCadena}, nil
	case destino.Igual(TipoCadena) && v.tip.Igual(TipoBooleano):
		res := c.currentBlock.NewCall(c.rtBoolToString, v.ref)
		return cgValue{ref: res, tip: TipoCadena}, nil
	case destino.Igual(TipoCadena):
		return cgValue{}, fmt.Errorf("línea %d: 'cadena(...)' no soporta convertir %s a cadena", linea, v.tip)
	default:
		return cgValue{}, fmt.Errorf("línea %d: conversión no soportada de %s a %s", linea, v.tip, destino)
	}
}

// conFlagsDado completa 'args' (que ya trae un valor real por cada
// parámetro) con los flags "dado" que exige la firma LLVM de una función con
// parámetros por defecto, todos en 'true' porque el wrapper 'main' siempre
// pasa un valor concreto para cada parámetro de 'principal'.
func (c *Codegen) conFlagsDado(args []value.Value, firma *TipoFuncion) []value.Value {
	numFlags := len(firma.Parametros) - firma.Requeridos
	for i := 0; i < numFlags; i++ {
		args = append(args, constant.NewBool(true))
	}
	return args
}

// valorPlaceholder da un valor LLVM válido del tipo 't' para rellenar la
// posición de un argumento omitido en una llamada (cuando se usa el valor
// por defecto). Nunca se lee dentro de la función llamada porque el flag
// "dado" correspondiente viaja en 'false'.
func (c *Codegen) valorPlaceholder(t Tipo) value.Value {
	switch {
	case t.Igual(TipoEntero):
		return constant.NewInt(types.I64, 0)
	case t.Igual(TipoDecimal):
		return constant.NewFloat(types.Double, 0)
	case t.Igual(TipoBooleano):
		return constant.NewBool(false)
	default:
		// cadena, lista<...> y diccionario<...> se representan como
		// puntero opaco i8*; null alcanza como placeholder.
		return constant.NewNull(types.NewPointer(types.I8))
	}
}

func (c *Codegen) llvmType(t Tipo) types.Type {
	if t == nil {
		return types.Void
	}
	if t.Igual(TipoVacio) {
		// FIX(bug): antes 'vacio' caía al 'default' de abajo (el mismo
		// que listas/diccionarios) y terminaba representado como i8*
		// — un puntero opaco — en vez de 'void'. Eso hacía que toda
		// función declarada 'funcion f(): vacio:' se generara con firma
		// LLVM "-> i8*" mientras que el codegen de 'retornar' (y el
		// retorno implícito al final de la función) siempre emiten
		// 'ret void' para el caso vacio (ver emitirFuncion y el caso
		// RETORNO en emitirSentencia, que YA tratan TipoVacio
		// correctamente) — un desajuste de tipos en el propio LLVM IR
		// generado ("value doesn't match function result type"),
		// confirmado con clang al intentar ensamblar el .ll. 'vacio'
		// representa "sin valor" (como 'void' en C), así que su
		// representación LLVM correcta es 'types.Void', igual que el
		// caso t == nil de arriba (que cubre 'externa' sin anotación de
		// retorno, un caso relacionado).
		return types.Void
	}
	if t.Igual(TipoEntero) {
		return types.I64
	}
	if t.Igual(TipoDecimal) {
		return types.Double
	}
	if t.Igual(TipoBooleano) {
		return types.I1
	}
	if t.Igual(TipoCadena) {
		return types.NewPointer(types.I8)
	}
	if s, ok := t.(*TipoEstructura); ok {
		fields := make([]types.Type, len(s.Campos))
		for i, f := range s.Campos {
			fields[i] = c.llvmType(f.Tipo)
		}
		return types.NewStruct(fields...)
	}
	// listas y diccionarios: puntero opaco i8*.
	return types.NewPointer(types.I8)
}

func (c *Codegen) newLabel(prefix string) string {
	c.labelCounter++
	return fmt.Sprintf("%s.%d", prefix, c.labelCounter)
}

func (c *Codegen) pushScope() {
	c.scopes = append(c.scopes, map[string]cgVar{})
}

func (c *Codegen) popScope() {
	c.scopes = c.scopes[:len(c.scopes)-1]
}

func (c *Codegen) declareVar(nombre string, v cgVar) {
	c.scopes[len(c.scopes)-1][nombre] = v
}

func (c *Codegen) lookupVar(nombre string) (cgVar, bool) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if v, ok := c.scopes[i][nombre]; ok {
			return v, true
		}
	}
	return cgVar{}, false
}

// lookupVarEnScopeActual busca 'nombre' SOLO en el scope más interno (el
// actual), a diferencia de lookupVar que recorre todos los scopes de
// adentro hacia afuera. Se usa para distinguir "esta declaración es una
// re-ejecución de una variable ya declarada en este mismo bloque" (reusar
// alloca, liberar valor anterior) de "variable genuinamente nueva en
// este scope, aunque haga sombra a una de un scope externo" (alloca
// nuevo).
func (c *Codegen) lookupVarEnScopeActual(nombre string) (cgVar, bool) {
	v, ok := c.scopes[len(c.scopes)-1][nombre]
	return v, ok
}

func (c *Codegen) internString(value string) *ir.Global {
	if g, ok := c.stringConstants[value]; ok {
		return g
	}
	name := fmt.Sprintf(".str.%d", len(c.stringConstants))
	data := append([]byte(value), 0)
	g := c.module.NewGlobalDef(name, constant.NewCharArray(data))
	g.Immutable = true
	g.Linkage = enum.LinkagePrivate
	g.UnnamedAddr = enum.UnnamedAddrUnnamedAddr
	c.stringConstants[value] = g
	return g
}

func esComparador(op string) bool {
	switch op {
	case "==", "!=", "<>", "<", "<=", ">", ">=":
		return true
	default:
		return false
	}
}

func (c *Codegen) safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteString("_")
			b.WriteString(strconv.FormatInt(int64(r), 10))
		}
	}
	return b.String()
}
