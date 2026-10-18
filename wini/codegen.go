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

// temporalEnCurso es una referencia heap creada a mitad del cálculo de una
// expresión, que se libera si la expresión termina lanzando una excepción.
type temporalEnCurso struct {
	ref value.Value
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
	// varPtr es el alloca de esa misma variable de iteración. Con
	// shadowing ('para x en a: para x en b:') dos loops distintos pueden
	// llamarse 'x', así que el nombre no alcanza para saber si son la
	// misma variable: lo que las distingue es la ranura (ver
	// esLaMismaVariable).
	varPtr value.Value
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
	rtStringToI64  *ir.Func
	rtStringToF64  *ir.Func
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

	// Raíces de excepción (limpieza automática al desenrollar): cada
	// variable local heap se registra una vez al entrar a la función y la
	// función poda las suyas al retornar; 'lanzar'/'relanzar' liberan las
	// que queden vivas antes de saltar. Ver runtime.h (wini_raiz_agregar).
	rtRaizAgregar        *ir.Func
	rtRaicesProfundidad  *ir.Func
	rtRaicesPodar        *ir.Func
	rtInvocacionNueva    *ir.Func

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

	// Runtime: verificación aritmética
	// Verifica que el divisor de una división/módulo ENTERO no sea cero y
	// lanza un ErrorMatematico capturable si lo es (ver runtime.c). Sin
	// esto, un '/ 0' entero moriría con SIGFPE, que no se puede capturar
	// con 'intentar/capturar'.
	rtCheckDivisor *ir.Func
	// rtCheckDivisorDecimal verifica el divisor de una división decimal
	// por cero: IEEE-754 daría infinito en silencio, pero el lenguaje lo
	// trata como error (igual que el entero) para que la operación se
	// comporte igual según el tipo de los operandos.
	rtCheckDivisorDecimal *ir.Func

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
	rtListInsertI64 *ir.Func
	rtListInsertF64 *ir.Func
	rtListInsertI1  *ir.Func
	rtListInsertStr *ir.Func
	rtListRemove    *ir.Func

	// funcionesPorNombre indexa c.module.Funcs por nombre para que
	// buscarFuncion no tenga que recorrerlo entero en cada consulta
	// (ver buscarFuncion). Se arma laxo en el primer uso.
	funcionesPorNombre map[string]*ir.Func

	// temporalesEnCurso son valores heap que se construyeron a mitad de una
	// expresión y todavía no tienen dueño estable (el 'result' parcial de
	// una cadena interpolada, por ejemplo). Si más adelante esa expresión
	// LANZA en runtime, el 'lanzar' recorre esta lista y devuelve las
	// referencias antes de hacer longjmp: si no, se fugan
	// (confirmado con LeakSanitizer).
	temporalesEnCurso []temporalEnCurso

	// Runtime: listas genéricas (elementos lista o estructura). Ver
	// "listas genéricas" en runtime.h y descriptorElemento más abajo.
	rtListNewGen    *ir.Func
	rtListSlot      *ir.Func
	rtListSetGen    *ir.Func
	rtListConcatGen *ir.Func
	rtListInsertGen *ir.Func
	rtListRemoveGen *ir.Func

	// descriptores guarda, por tipo de elemento (Tipo.String()), la
	// constante global WiniElemDesc ya emitida; thunks hace lo mismo con
	// las funciones retener/liberar emitidas por estructura. Así cada
	// descriptor/thunk se emite una sola vez por módulo.
	descriptores map[string]constant.Constant
	thunks       map[string]*ir.Func

	// Runtime: range
	rtRangeI64 *ir.Func

	// Runtime: argv
	rtArgsToList *ir.Func

	// Runtime: dict
	rtDictNew      *ir.Func
	rtDictLen      *ir.Func
	rtDictContains map[string]*ir.Func
	// rtListSlotStr y rtDictSlotStr devuelven la DIRECCIÓN de una ranura
	// (i8**) para poder escribirle NULL después de 'liberar' un elemento.
	// Sin esto, un 'liberar(l)' posterior liberaba dos veces la misma
	// memoria (doble free confirmado con AddressSanitizer).
	rtListSlotStr *ir.Func
	// rtDictSlotStr devuelve la dirección de la ranura de un valor de tipo
	// cadena en un diccionario de claves cadena. La usa 'liberar(d[clave])'
	// para dejarle NULL después de soltar la referencia y evitar el doble
	// free de un 'liberar(d)' posterior.
	rtDictSlotStr *ir.Func
	rtDictSet     map[string]*ir.Func
	rtDictGet     map[string]*ir.Func
	rtDictKeys    map[string]*ir.Func
	rtDictValues  map[string]*ir.Func
	// Variantes genéricas del valor de diccionario (lista o estructura):
	// una por tipo de CLAVE primitiva (set/slot), más valores() genérico.
	rtDictSetGen    map[string]*ir.Func
	rtDictSlotGen   map[string]*ir.Func
	rtDictValuesGen *ir.Func

	funciones   map[string]*TipoFuncion
	llvmFuncs   map[string]*ir.Func
	estructuras map[string]*TipoEstructura

	scopes []map[string]cgVar
	loops  []loopLabels

	stringConstants map[string]*ir.Global

	funcionActual     *TipoFuncion
	funcionActualNodo *Nodo
	currentFunc       *ir.Func
	initBlock         *ir.Block
	entryBlock        *ir.Block
	currentBlock      *ir.Block
	blockTerminated   bool

	// raizBasePtr es el alloca (en el bloque de entrada) que guarda la
	// profundidad de la pila de raíces al entrar a la función actual.
	// Cada 'retornar' (y el retorno implícito) poda hasta esa base antes
	// de salir, descartando las raíces de la función sin leerlas. Nil en
	// funciones generadas que no registran raíces (thunks, wrapper main).
	raizBasePtr value.Value
	// raizFidPtr es el alloca (en el bloque de entrada) que guarda el fid
	// de esta invocación (ver wini_invocacion_nueva): identifica las
	// raíces propias al registrarlas y al empujar frames de 'intentar'.
	// Nil donde raizBasePtr lo es.
	raizFidPtr value.Value
	// regionesTry es la pila de regiones abandonables activas: cada
	// 'intentar' en compilación apila una, y cada ranura registrada se
	// anota en todas. Al aterrizar en un despacho se liberan las de ese
	// 'try'; 'lanzar'/'relanzar' liberan las de todas. Idempotente.
	regionesTry [][]ranuraRegion

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

// UltimasAvisas devuelve las advertencias no fatales de la última
// compilación hecha con CompileFileToLLVM (por ejemplo, código
// inalcanzable). No son errores: la compilación igual genera el binario.
// Devuelve nil si aún no se compiló nada, o si no hubo advertencias.
func UltimasAvisas() []Avisa {
	return AvisasDelVerificador()
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
		descriptores:    map[string]constant.Constant{},
		thunks:          map[string]*ir.Func{},
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
	c.rtStringToI64 = m.NewFunc("wini_string_to_i64", types.I64,
		ir.NewParam("s", stringPtr),
		ir.NewParam("linea", types.I64))
	c.rtStringToF64 = m.NewFunc("wini_string_to_f64", types.Double,
		ir.NewParam("s", stringPtr),
		ir.NewParam("linea", types.I64))
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

	// Raíces de excepción (ver runtime.h): registrar la ranura de cada
	// local heap para que 'lanzar' la libere al desenrollar.
	c.rtRaizAgregar = m.NewFunc("wini_raiz_agregar", types.Void,
		ir.NewParam("ranura", stringPtr),
		ir.NewParam("kind", types.I32),
		ir.NewParam("aux1", types.I32),
		ir.NewParam("aux2", types.I32),
		ir.NewParam("fid", types.I64))
	c.rtRaicesProfundidad = m.NewFunc("wini_raices_profundidad", types.I64)
	c.rtRaicesPodar = m.NewFunc("wini_raices_podar", types.Void,
		ir.NewParam("prof", types.I64))
	// Identidad de invocación para las raíces (ver wini_invocacion_nueva
	// en runtime.h): monótona con el anidado de llamados, distingue lo
	// abandonado (fid mayor) de lo que sobrevive.
	c.rtInvocacionNueva = m.NewFunc("wini_invocacion_nueva", types.I64)

	// Excepciones
	c.rtTryPush = m.NewFunc("wini_try_push", types.NewPointer(types.I8),
		ir.NewParam("fid", types.I64))
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

	// Verificación aritmética (división entera por cero)
	c.rtCheckDivisor = m.NewFunc("wini_check_divisor", types.Void,
		ir.NewParam("dividendo", types.I64),
		ir.NewParam("divisor", types.I64),
		ir.NewParam("linea", types.I64))

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

	// Insertar un elemento en una posición (builtins 'insertar'):
	// devuelven una lista NUEVA (la original no se modifica).
	c.rtListInsertI64 = m.NewFunc("wini_list_insert_i64", listPtr,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("value", types.I64),
		ir.NewParam("linea", types.I64))
	c.rtListInsertF64 = m.NewFunc("wini_list_insert_f64", listPtr,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("value", types.Double),
		ir.NewParam("linea", types.I64))
	c.rtListInsertI1 = m.NewFunc("wini_list_insert_i1", listPtr,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("value", types.I1),
		ir.NewParam("linea", types.I64))
	c.rtListInsertStr = m.NewFunc("wini_list_insert_str", listPtr,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("value", stringPtr),
		ir.NewParam("linea", types.I64))
	// Quitar el elemento de una posición (builtin 'quitar'): devuelve una
	// lista NUEVA sin ese elemento. 'tipo' usa los códigos de concat.
	c.rtListRemove = m.NewFunc("wini_list_remove", listPtr,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("tipo", types.I32),
		ir.NewParam("linea", types.I64))

	// Listas genéricas (elementos lista o estructura). El descriptor viaja
	// como i8* (la constante global se castea al emitirla).
	c.rtListNewGen = m.NewFunc("wini_list_new_gen", listPtr,
		ir.NewParam("desc", types.NewPointer(types.I8)),
		ir.NewParam("data", types.NewPointer(types.I8)),
		ir.NewParam("len", types.I64))
	c.rtListSlot = m.NewFunc("wini_list_slot", types.NewPointer(types.I8),
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("linea", types.I64))
	// Verificación aritmética de división decimal por cero (ver el runtime).
	c.rtCheckDivisorDecimal = m.NewFunc("wini_check_divisor_decimal", types.Void,
		ir.NewParam("divisor", types.Double),
		ir.NewParam("linea", types.I64))
	// wini_list_slot_str(list, idx, linea) -> dirección de la ranura de un
	// elemento de tipo cadena (lista<cadena>). La usa 'liberar(l[i])' para
	// dejarle NULL después de soltar la referencia.
	c.rtListSlotStr = m.NewFunc("wini_list_slot_str",
		types.NewPointer(types.NewPointer(types.I8)),
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("linea", types.I64))
	// wini_dict_slot_str(dict, clave) -> dirección de la ranura del valor
	// (o NULL si la clave no está).
	c.rtDictSlotStr = m.NewFunc("wini_dict_slot_str",
		types.NewPointer(types.NewPointer(types.I8)),
		ir.NewParam("dict", types.NewPointer(types.I8)),
		ir.NewParam("clave", stringPtr))
	c.rtListSetGen = m.NewFunc("wini_list_set_gen", types.Void,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("valor", types.NewPointer(types.I8)),
		ir.NewParam("desc", types.NewPointer(types.I8)),
		ir.NewParam("linea", types.I64))
	c.rtListConcatGen = m.NewFunc("wini_list_concat_gen", listPtr,
		ir.NewParam("a", listPtr),
		ir.NewParam("b", listPtr),
		ir.NewParam("desc", types.NewPointer(types.I8)))
	c.rtListInsertGen = m.NewFunc("wini_list_insert_gen", listPtr,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("valor", types.NewPointer(types.I8)),
		ir.NewParam("desc", types.NewPointer(types.I8)),
		ir.NewParam("linea", types.I64))
	c.rtListRemoveGen = m.NewFunc("wini_list_remove_gen", listPtr,
		ir.NewParam("list", listPtr),
		ir.NewParam("idx", types.I64),
		ir.NewParam("desc", types.NewPointer(types.I8)),
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
	c.rtDictSetGen = map[string]*ir.Func{}
	c.rtDictSlotGen = map[string]*ir.Func{}

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
		// Variantes genéricas del valor (lista o estructura): el valor viaja
		// por puntero opaco más su descriptor (ver WiniElemDesc). Las claves
		// siguen siendo primitivas (una por tipo, igual que arriba).
		c.rtDictSetGen[k.suf] = m.NewFunc("wini_dict_set_"+k.suf+"_gen", types.Void,
			ir.NewParam("dict", dictPtr), ir.NewParam("clave", k.lt),
			ir.NewParam("valor", stringPtr), ir.NewParam("desc", stringPtr))
		c.rtDictSlotGen[k.suf] = m.NewFunc("wini_dict_slot_"+k.suf+"_gen", stringPtr,
			ir.NewParam("dict", dictPtr), ir.NewParam("clave", k.lt),
			ir.NewParam("desc", stringPtr), ir.NewParam("linea", types.I64))
	}
	// Valores del diccionario como lista genérica (builtin 'valores' con
	// valor lista/estructura).
	c.rtDictValuesGen = m.NewFunc("wini_dict_values_gen", listPtr,
		ir.NewParam("dict", dictPtr), ir.NewParam("desc", stringPtr))

	return c
}

// Tablas de predicados para las comparaciones, a nivel de paquete.
//
// Antes se escribía un literal de mapa dentro de emitirComparacion, lo que
// obligaba al compilador de Go a construir un map, hashear la clave y
// buscar, en CADA comparación que emitía. Son tablas constantes de 7
// entradas: inicializarlas una sola vez al cargar el paquete las convierte
// en una simple lectura.
var predicadosEntero = map[string]enum.IPred{
	"==": enum.IPredEQ, "!=": enum.IPredNE, "<>": enum.IPredNE,
	"<": enum.IPredSLT, "<=": enum.IPredSLE, ">": enum.IPredSGT, ">=": enum.IPredSGE,
}

var predicadosDecimal = map[string]enum.FPred{
	"==": enum.FPredOEQ, "!=": enum.FPredONE, "<>": enum.FPredONE,
	"<": enum.FPredOLT, "<=": enum.FPredOLE, ">": enum.FPredOGT, ">=": enum.FPredOGE,
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
		code := codigoElemento(tt.Elemento)
		if code == -1 {
			return
		}
		c.currentBlock.NewCall(c.rtLiberarLista, ref, constant.NewInt(types.I32, int64(code)))
	case *TipoDiccionario:
		claveCode := tipoCode(tt.Clave)
		valorCode := codigoElemento(tt.Valor)
		if claveCode == -1 || valorCode == -1 {
			return
		}
		c.currentBlock.NewCall(c.rtLiberarDiccionario, ref,
			constant.NewInt(types.I32, int64(claveCode)), constant.NewInt(types.I32, int64(valorCode)))
	default:
		c.currentBlock.NewCall(c.rtLiberarCadena, ref)
	}
}

// ranuraRegion es una ranura con dueño anotada en una región de 'try':
// al abandonar la región (despacho del 'capturar', 'lanzar' o 'relanzar')
// se libera su contenido y se la deja en NULL. Idempotente.
type ranuraRegion struct {
	ptr value.Value
	tip Tipo
}

// ---------------------------------------------------------------------
// Raíces de excepción: registro de locales heap para limpieza automática
// al desenrollar (ver wini_raiz_agregar en runtime.h).
// ---------------------------------------------------------------------

// Códigos de kind para wini_raiz_agregar (ver runtime.h).
const (
	raizKindCadena = 0
	raizKindLista  = 1
	raizKindDicc   = 2
)

// raizCodigos traduce un tipo heap a los (kind, aux1, aux2) de
// wini_raiz_agregar, con el mismo criterio que emitirLiberarValor: los
// tipos que el runtime no sabe liberar no se registran (ok == false).
func raizCodigos(t Tipo) (kind, aux1, aux2 int64, ok bool) {
	switch tt := t.(type) {
	case *TipoLista:
		code := codigoElemento(tt.Elemento)
		if code == -1 {
			return 0, 0, 0, false
		}
		return raizKindLista, int64(code), 0, true
	case *TipoDiccionario:
		claveCode := tipoCode(tt.Clave)
		valorCode := codigoElemento(tt.Valor)
		if claveCode == -1 || valorCode == -1 {
			return 0, 0, 0, false
		}
		return raizKindDicc, int64(claveCode), int64(valorCode), true
	default:
		if t.Igual(TipoCadena) {
			return raizKindCadena, 0, 0, true
		}
		return 0, 0, 0, false
	}
}

// registrarRaiz registra la ranura 'slot' (alloca o campo) de tipo 't'
// como raíz de excepción, emitiendo en el bloque de entrada (corre una
// sola vez por invocación). Las estructuras se descomponen por campo: un
// campo heap se registra igual que una variable, y los campos sin
// referencias se ignoran. Llamar solo con slots ya inicializados a NULL
// (o cero, para estructuras): el desenrollado libera lo que encuentre no
// nulo, así que basura sin inicializar sería un free inválido.
//
// Además anota la ranura en todas las regiones de 'try' activas (ver
// regionesTry): si una excepción abandona esas regiones, el despacho del
// 'capturar' (o el propio 'lanzar'/'relanzar') libera y anula esas
// ranuras. Es idempotente ('liberar' + NULL): una ranura ya liberada se
// saltea sin hacer nada.
func (c *Codegen) registrarRaiz(slot value.Value, t Tipo) {
	if st, ok := t.(*TipoEstructura); ok {
		tipoLLVM := c.llvmType(st)
		for i, f := range st.Campos {
			if !poseeReferencias(f.Tipo) {
				continue
			}
			campoPtr := c.initBlock.NewGetElementPtr(tipoLLVM, slot,
				constant.NewInt(types.I32, 0), constant.NewInt(types.I32, int64(i)))
			c.registrarRaiz(campoPtr, f.Tipo)
		}
		return
	}
	kind, aux1, aux2, ok := raizCodigos(t)
	if !ok {
		return
	}
	i8p := types.NewPointer(types.I8)
	slot8 := c.initBlock.NewBitCast(slot, i8p)
	// El fid vive en un alloca del bloque de entrada: se lee ahí mismo,
	// una sola vez por invocación, junto al registro.
	var fid value.Value = constant.NewInt(types.I64, 0)
	if c.raizFidPtr != nil {
		fid = c.initBlock.NewLoad(types.I64, c.raizFidPtr)
	}
	c.initBlock.NewCall(c.rtRaizAgregar, slot8,
		constant.NewInt(types.I32, kind),
		constant.NewInt(types.I32, aux1),
		constant.NewInt(types.I32, aux2),
		fid)
	// La ranura queda anotada en cada región de 'try' activa: si una
	// excepción abandona esas regiones, sus ganchos la liberan y anulan.
	for i := range c.regionesTry {
		c.regionesTry[i] = append(c.regionesTry[i], ranuraRegion{ptr: slot, tip: t})
	}
}

// podarRaicesAlRetornar descarta las raíces registradas por la función
// actual, sin liberarlas (camino normal: lo que seguía vivo acá ya lo
// liberó el propio 'retornar', o pasa al llamador como valor de retorno).
// Emítase justo antes de cada 'ret' de una función de usuario.
func (c *Codegen) podarRaicesAlRetornar() {
	if c.raizBasePtr == nil {
		return
	}
	base := c.currentBlock.NewLoad(types.I64, c.raizBasePtr)
	c.currentBlock.NewCall(c.rtRaicesPodar, base)
}

// emitirLiberarYAnular libera el contenido heap de la ranura 'slot' de
// tipo 't' y la deja en NULL/cero. Es la contrapartida en flujo de una
// raíz registrada: idempotente (una ranura ya liberada se saltea sin
// hacer nada, porque 'liberar' anula y los liberar_* toleran NULL).
func (c *Codegen) emitirLiberarYAnular(slot value.Value, t Tipo) {
	if st, ok := t.(*TipoEstructura); ok {
		if poseeReferencias(st) {
			val := c.currentBlock.NewLoad(c.llvmType(st), slot)
			c.emitirLiberarEnValor(val, st)
		}
		c.currentBlock.NewStore(constant.NewZeroInitializer(c.llvmType(st)), slot)
		return
	}
	if !esTipoHeap(t) {
		return
	}
	var actual value.Value
	switch t.(type) {
	case *TipoLista, *TipoDiccionario:
		actual = c.currentBlock.NewLoad(types.NewPointer(types.I8), slot)
	default:
		actual = c.currentBlock.NewLoad(c.llvmType(t), slot)
	}
	c.emitirLiberarValor(actual, t)
	c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), slot)
}

// liberarRegion libera y anula cada ranura de la región (ver
// regionesTry). Nulo si ya estaban liberadas.
func (c *Codegen) liberarRegion(slots []ranuraRegion) {
	for _, s := range slots {
		c.emitirLiberarYAnular(s.ptr, s.tip)
	}
}

// ---------------------------------------------------------------------
// Listas de listas y de estructuras: propiedad de referencias y descriptores
// ---------------------------------------------------------------------

// tipoCodeGenerico es el 'tipo' que se le pasa al runtime para una lista
// cuyos elementos son listas o estructuras (ver WINI_TIPO_GENERICO en
// runtime.h): el runtime lo ignora, porque esas listas llevan su propio
// descriptor de elemento.
const tipoCodeGenerico = 4

// esElementoGenerico indica si 't', como elemento de lista, necesita el
// camino genérico del runtime (descriptor + slots) en vez de las
// funciones por-primitivo wini_list_*_{i64,f64,i1,str}.
func esElementoGenerico(t Tipo) bool {
	switch t.(type) {
	case *TipoLista, *TipoEstructura:
		return true
	}
	return false
}

// codigoElemento devuelve el 'tipo' a pasar a wini_liberar_lista /
// wini_print_list / wini_list_concat para una lista con elementos 't':
// 0..3 para los primitivos, tipoCodeGenerico para listas/estructuras, y -1
// si el elemento no es soportado. (tipoCode, en cambio, sigue siendo solo
// para primitivos: lo usan los diccionarios.)
func codigoElemento(t Tipo) int {
	if code := tipoCode(t); code != -1 {
		return code
	}
	if esElementoGenerico(t) {
		return tipoCodeGenerico
	}
	return -1
}

// puedeImprimirLista indica si 'escribir' sabe imprimir esta lista: sus
// elementos son primitivos, o listas que a su vez se pueden imprimir. Las
// estructuras (en cualquier nivel) no tienen impresión definida.
func puedeImprimirLista(l *TipoLista) bool {
	switch e := l.Elemento.(type) {
	case *TipoPrimitivo:
		return true
	case *TipoLista:
		return puedeImprimirLista(e)
	}
	return false
}

// poseeReferencias indica si un valor de tipo 't' contiene referencias
// heap que hay que retener/liberar al copiarlo o descartarlo: es un valor
// heap (cadena/lista/diccionario) o una estructura con algún campo así.
// Distinto de esTipoHeap, que además decide cosas como "se libera solo al
// retornar": una estructura NO es heap (se pasa por valor), pero sus
// campos heap sí cuentan referencias.
func poseeReferencias(t Tipo) bool {
	if st, ok := t.(*TipoEstructura); ok {
		for _, f := range st.Campos {
			if poseeReferencias(f.Tipo) {
				return true
			}
		}
		return false
	}
	return esTipoHeap(t)
}

// emitirRetenerEnValor retiene, una vez, CADA referencia heap contenida en
// el valor 'ref' de tipo 't' (si 't' es una estructura, recorre sus campos
// recursivamente; si es heap, lo retiene; si no, no hace nada).
func (c *Codegen) emitirRetenerEnValor(ref value.Value, t Tipo) {
	if st, ok := t.(*TipoEstructura); ok {
		for i, f := range st.Campos {
			if !poseeReferencias(f.Tipo) {
				continue
			}
			campo := c.currentBlock.NewExtractValue(ref, uint64(i))
			c.emitirRetenerEnValor(campo, f.Tipo)
		}
		return
	}
	if esTipoHeap(t) {
		c.emitirRetenerValor(ref, t)
	}
}

// emitirLiberarEnValor es la contraparte de emitirRetenerEnValor: libera
// UNA referencia de cada valor heap contenido en 'ref'.
func (c *Codegen) emitirLiberarEnValor(ref value.Value, t Tipo) {
	if st, ok := t.(*TipoEstructura); ok {
		for i, f := range st.Campos {
			if !poseeReferencias(f.Tipo) {
				continue
			}
			campo := c.currentBlock.NewExtractValue(ref, uint64(i))
			c.emitirLiberarEnValor(campo, f.Tipo)
		}
		return
	}
	c.emitirLiberarValor(ref, t)
}

// retenerParaAlmacenar prepara 'v' para guardarlo en una ubicación que
// pasa a ser su dueña (variable, campo, elemento de lista, campo de una
// construcción). Para valores heap es retenerSiCompartido. Para una
// estructura con referencias que viene de otra ubicación (variable, campo,
// elemento de lista: compartido == true) retiene TODAS las referencias que
// contiene, así la nueva ubicación tiene las suyas; una estructura recién
// construida (constructor) ya trae las propias y se adopta tal cual.
func (c *Codegen) retenerParaAlmacenar(v cgValue) cgValue {
	if st, ok := v.tip.(*TipoEstructura); ok {
		if v.compartido && poseeReferencias(st) {
			c.emitirRetenerEnValor(v.ref, st)
			v.compartido = false
		}
		return v
	}
	return c.retenerSiCompartido(v)
}

// esLocalPropio indica si 'nombre' es una variable local de la función
// actual que ES DUEÑA de lo que contiene: ni un parámetro (el llamador
// solo se lo presta) ni la variable de un 'para' (alias de un elemento de
// la lista). Solo de esas se puede "transferir" el contenido al retornar.
func (c *Codegen) esLocalPropio(nombre string) bool {
	if c.funcionActualNodo != nil && len(c.funcionActualNodo.Hijos) > 0 {
		for _, p := range c.funcionActualNodo.Hijos[0].Hijos {
			if pn, _ := p.Valor.(string); pn == nombre {
				return false
			}
		}
	}
	for _, lp := range c.loops {
		if lp.nombreVar == nombre {
			return false
		}
	}
	_, ok := c.lookupVar(nombre)
	return ok
}

// esLaMismaVariable indica si el nombre resuelve al MISMO alloca que una
// variable de iteración de un 'para' activo.
//
// No alcanza con comparar los nombres: con shadowing
// ('para x en a: para x en b: ...') varios 'para' pueden usar el mismo
// nombre y refers a variables DISTINTAS. Lo que las distingue es la
// ranura de memoria (el alloca). Se usa para decidir a qué limpieza de
// iteración hay que saltarse: solo la del loop cuya variable es
// exactamente el valor devuelto por el 'retornar'.
func (c *Codegen) esLaMismaVariable(nombre, nombreLoop string) bool {
	if nombre == "" || nombreLoop == "" {
		return false
	}
	if nombre != nombreLoop {
		return false
	}
	// lookupVar recorre los ámbitos de adentro hacia afuera, así que
	// devuelve el shadowing más interno: el 'para' activo más interno que
	// declara ese nombre, que es justo el que buscamos (es el loop del que
	// estamos saliendo).
	variable, ok := c.lookupVar(nombre)
	if !ok {
		return false
	}
	for _, lp := range c.loops {
		if lp.nombreVar != nombreLoop {
			continue
		}
		// Coinciden si comparten la misma ranura. lp.varPtr es nil para
		// bucles sin limpieza asociada, y en ese caso no hay nada que
		// saltarse, así que tampoco se puede estar devolviendo su
		// variable.
		if lp.varPtr != nil && lp.varPtr == variable.ptr {
			return true
		}
	}
	return false
}

// tamanoDeTipo devuelve el sizeof de un tipo LLVM como constante i64, usando
// el truco clásico ptrtoint(gep(null, 1)): así el tamaño lo calcula el
// propio LLVM con el layout real de la plataforma (padding incluido) y
// coincide con el stride del arreglo que arma el codegen.
func tamanoDeTipo(t types.Type) constant.Constant {
	gep := constant.NewGetElementPtr(t, constant.NewNull(types.NewPointer(t)), constant.NewInt(types.I32, 1))
	return constant.NewPtrToInt(gep, types.I64)
}

// thunkEstructura emite (una vez por estructura y modo) la función
// 'void(i8* slot)' que retiene o libera todas las referencias heap de un
// valor de estructura almacenado en 'slot'. El runtime la llama desde el
// descriptor de una lista de esa estructura.
func (c *Codegen) thunkEstructura(st *TipoEstructura, retener bool) *ir.Func {
	modo := "liberar"
	if retener {
		modo = "retener"
	}
	clave := st.Nombre + "/" + modo
	if f, ok := c.thunks[clave]; ok {
		return f
	}
	i8p := types.NewPointer(types.I8)
	f := c.module.NewFunc("wini.thunk."+st.Nombre+"."+modo, types.Void, ir.NewParam("slot", i8p))
	c.thunks[clave] = f

	// Los helpers de retener/liberar emiten sobre c.currentBlock: se
	// apunta al bloque del thunk y se restaura al terminar, porque esto
	// puede dispararse en medio de la emisión de otra función.
	previo := c.currentBlock
	bloque := f.NewBlock("entry")
	c.currentBlock = bloque
	tipado := bloque.NewBitCast(f.Params[0], types.NewPointer(c.llvmType(st)))
	valor := bloque.NewLoad(c.llvmType(st), tipado)
	if retener {
		c.emitirRetenerEnValor(valor, st)
	} else {
		c.emitirLiberarEnValor(valor, st)
	}
	c.currentBlock.NewRet(nil)
	c.currentBlock = previo
	return f
}

// descriptorElemento devuelve (emitiéndola la primera vez) la constante
// global WiniElemDesc para listas cuyo elemento es 'elem', ya casteada a
// i8* para pasársela al runtime. El layout TIENE que coincidir con el
// typedef WiniElemDesc de runtime.h: { i64 size, i32 kind, i32 sub,
// void(i8*)* retener, void(i8*)* liberar }.
func (c *Codegen) descriptorElemento(elem Tipo) (constant.Constant, error) {
	clave := elem.String()
	if d, ok := c.descriptores[clave]; ok {
		return d, nil
	}
	i8p := types.NewPointer(types.I8)
	thunkTy := types.NewPointer(types.NewFunc(types.Void, i8p))
	descTy := types.NewStruct(types.I64, types.I32, types.I32, thunkTy, thunkTy)

	var kind, sub int64
	var retener, liberar constant.Constant = constant.NewNull(thunkTy), constant.NewNull(thunkTy)
	switch tt := elem.(type) {
	case *TipoLista:
		kind = 1 // WINI_ELEM_LISTA: el runtime retiene/libera el WiniList* por sí solo
		code := codigoElemento(tt.Elemento)
		if code == -1 {
			return nil, fmt.Errorf("tipo de elemento no soportado para lista: %s", tt.Elemento)
		}
		sub = int64(code)
	case *TipoEstructura:
		kind = 2 // WINI_ELEM_OPACO: retener/liberar vía thunks (solo si hay referencias)
		if poseeReferencias(tt) {
			retener = c.thunkEstructura(tt, true)
			liberar = c.thunkEstructura(tt, false)
		}
	default:
		return nil, fmt.Errorf("tipo de elemento no soportado para lista: %s", elem)
	}

	g := c.module.NewGlobalDef(fmt.Sprintf("wini.desc.%d", len(c.descriptores)),
		constant.NewStruct(descTy,
			tamanoDeTipo(c.llvmType(elem)),
			constant.NewInt(types.I32, kind),
			constant.NewInt(types.I32, sub),
			retener, liberar))
	g.Immutable = true
	d := constant.NewBitCast(g, i8p)
	c.descriptores[clave] = d
	return d, nil
}

// cargarSlot emite la lectura de un elemento de una lista genérica:
// pide el puntero al slot (con chequeo de rango, IndexError capturable) y
// carga el valor tipado desde ahí. No retiene nada: el elemento sigue
// siendo de la lista.
func (c *Codegen) cargarSlot(listPtr, idx value.Value, elem Tipo, linea value.Value) value.Value {
	slot := c.currentBlock.NewCall(c.rtListSlot, listPtr, idx, linea)
	tipado := c.currentBlock.NewBitCast(slot, types.NewPointer(c.llvmType(elem)))
	return c.currentBlock.NewLoad(c.llvmType(elem), tipado)
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
				// Comprobado, no asumido: Generate no tiene recover(), así
				// que una aserción desnuda que falle mata el compilador en
				// vez de devolver un error.
				nombreCampo, ok := f.Valor.(string)
				if !ok {
					return "", fmt.Errorf("línea %d: el campo de la estructura '%s' no tiene nombre", f.Linea, nombre)
				}
				t.Campos = append(t.Campos, CampoEstructura{nombreCampo, c.resolverAnotacion(f.Hijos[0])})
			}
		}
	}
	for _, n := range raiz.Hijos {
		if n.Tipo == "FUNCION" || n.Tipo == "EXTERNA" {
			nombre, _ := n.Valor.(string)
			c.funciones[nombre] = c.construirFirma(n)
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
			// BUG-FIX(redefinición): si el símbolo C coincide con una función
			// que ya está declarada en el módulo (típicamente una del
			// runtime, como 'wini_string_len'), antes se creaba una SEGUNDA
			// declaración del mismo nombre: el .ll quedaba con dos 'declare'
			// del mismo símbolo y clang lo rechazaba con "invalid redefinition
			// of function". Si la firma es idéntica, reutilizamos la
			// declaración existente (así 'externa "wini_string_len" como
			// largo(s: cadena): entero' funciona y 'largo' llama al runtime);
			// si difiere, damos un error claro en vez de IR inválido.
			sigDeseada := firmaLLVM(c.llvmType(firma.Retorno), params, firma.VariadicoC)
			if existente := c.buscarFuncion(simboloC); existente != nil {
				if existente.Sig.String() != sigDeseada.String() {
					return "", fmt.Errorf("línea %d: el símbolo C '%s' de la función externa '%s' ya está declarado con una firma distinta (no se puede redeclarar; revisá los tipos de los parámetros o elegí otro símbolo)", n.Linea, simboloC, nombre)
				}
				c.llvmFuncs[nombre] = existente
				continue
			}
			fn := c.module.NewFunc(simboloC, c.llvmType(firma.Retorno), params...)
			fn.Sig.Variadic = sigDeseada.Variadic
			c.llvmFuncs[nombre] = fn
			continue
		}

		simboloWini := c.safeName(nombre)
		if c.buscarFuncion(simboloWini) != nil {
			return "", fmt.Errorf("línea %d: la función '%s' usa un nombre reservado por el runtime de Wini; elegí otro nombre", n.Linea, nombre)
		}
		fn := c.module.NewFunc(simboloWini, c.llvmType(firma.Retorno), params...)
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
	return resolverAnotacionCon(n, func(nombre string) (Tipo, bool) {
		t, ok := c.estructuras[nombre]
		if !ok {
			return nil, false
		}
		return t, true
	})
}

func (c *Codegen) construirFirma(nodoFuncion *Nodo) *TipoFuncion {
	return construirFirmaConResolutor(nodoFuncion, c.resolverAnotacion)
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

	// Bloque de inicialización: corre una sola vez al entrar, antes que
	// todo lo demás, y nunca contiene un 'setjmp'. Todo lo que debe
	// ejecutarse una sola vez por invocación (allocas, stores iniciales,
	// registros de raíces, fid/base) se emite acá y NO en entryBlock:
	// entryBlock sí puede alojar un 'setjmp' (si la primera sentencia es
	// un 'intentar'), y todo lo anexado después de ese 'setjmp' se
	// re-ejecutaría en cada aterrizaje de longjmp (anularía locales vivas
	// y duplicaría raíces). Se termina con el salto a 'entry' al final
	// de emitirFuncion, cuando ya se anexó todo. Va primero en layout
	// porque la ejecución arranca en el primer bloque.
	init := fn.NewBlock("init")
	c.initBlock = init
	entry := fn.NewBlock("entry")
	c.entryBlock = entry
	c.currentBlock = entry
	c.blockTerminated = false
	c.parametrosHeapActuales = nil
	c.tryFrames = nil
	c.finallyEnCursoDe = nil
	// Base de raíces de esta invocación: todo lo que se registre de acá
	// en más pertenece a esta función y se poda al retornar (ver
	// podarRaicesAlRetornar en los tres caminos de 'ret').
	baseRaices := c.initBlock.NewCall(c.rtRaicesProfundidad)
	basePtr := c.initBlock.NewAlloca(types.I64)
	c.initBlock.NewStore(baseRaices, basePtr)
	c.raizBasePtr = basePtr
	// Fid de esta invocación (ver wini_invocacion_nueva): identifica las
	// raíces propias y los frames que empuje.
	fidRaices := c.initBlock.NewCall(c.rtInvocacionNueva)
	fidPtr := c.initBlock.NewAlloca(types.I64)
	c.initBlock.NewStore(fidRaices, fidPtr)
	c.raizFidPtr = fidPtr
	// Sin regiones de 'try' activas al entrar (cada 'intentar' apila la
	// suya al compilarse y la desapila al terminar).
	c.regionesTry = nil

	for i, p := range paramsNodo.Hijos {
		nombreParam, _ := p.Valor.(string)
		tipoParam := firma.Parametros[i]
		ptr := c.initBlock.NewAlloca(c.llvmType(tipoParam))
		c.initBlock.NewStore(fn.Params[i], ptr)
		c.declareVar(nombreParam, cgVar{ptr: ptr, tip: tipoParam})
		// El llamador retuvo una referencia por cada parámetro heap al
		// pasarlo: si esta función lanza antes de retornarlo/liberarlo,
		// el desenrollado la devuelve. (Los params con valor por defecto
		// omitido reciben placeholder NULL: seguro para el desenrollado.)
		c.registrarRaiz(ptr, tipoParam)
		// Una estructura no es un valor heap en sí misma (viaja por valor), pero
		// sus CAMPOS heap sí lo son y quedan en manos de la función al
		// recibirla. Por eso también entra en la lista: sin esto, un
		// parámetro de tipo estructura nunca liberaba sus campos al
		// retornar (fuga) y, peor, el llamador seguía usando los mismos
		// punteros que el llamado liberaría (use-after-free).
		if esTipoHeap(tipoParam) || poseeReferencias(tipoParam) {
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
			defVal = c.retenerParaAlmacenar(defVal)
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
				// Cerrar init igual que en el camino normal (ver final
				// de emitirFuncion): el módulo debe quedar válido.
				c.initBlock.NewBr(entry)
				return nil
			}
			for _, p := range c.parametrosHeapActuales {
				actual := c.currentBlock.NewLoad(c.llvmType(p.tip), p.ptr)
				c.emitirLiberarEnValor(actual, p.tip)
			}
			c.podarRaicesAlRetornar()
			c.currentBlock.NewRet(nil)
			c.blockTerminated = true
			c.initBlock.NewBr(entry)
			return nil
		}
		return fmt.Errorf("la función '%s' puede terminar sin 'retornar'", nombre)
	}
	// Cerrar el bloque de inicialización: todo lo anexado durante la
	// compilación ya está adentro, en orden, y corre una sola vez.
	c.initBlock.NewBr(entry)
	return nil
}

func (c *Codegen) emitirSentencia(n *Nodo) error {
	switch n.Tipo {
	case "ASIGNACION_TIPADA":
		nombre, _ := n.Valor.(string)
		tipoDecl := c.resolverAnotacion(n.Hijos[0].Hijos[0])
		valor, err := c.emitirExpr(n.Hijos[1], tipoDecl)
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
		valor = c.retenerParaAlmacenar(valor)

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
				// Liberación incondicional: arriba hubo un retain (+1) por
				// el valor compartido, así que esta baja deja rc = 1 para
				// el único dueño. Ver la nota equivalente en ASIGNACION.
				anterior := c.currentBlock.NewLoad(c.llvmType(existente.tip), existente.ptr)
				c.emitirLiberarValor(anterior, existente.tip)
			}
			c.currentBlock.NewStore(valor.ref, existente.ptr)
			return nil
		}

		// Primera vez que 'nombre' se declara en este scope: alloca
		// nuevo. Para tipos heap se inicializa a null antes del store
		// real, así el alloca nunca queda con basura sin inicializar si
		// en el futuro algo lo lee o libera antes de este punto.
		ptr := c.initBlock.NewAlloca(c.llvmType(tipoDecl))
		if esTipoHeap(tipoDecl) {
			c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), ptr)
		}
		// Raíz de excepción (ver registrarRaiz): el bloque de entrada
		// corre una sola vez, así que el registro también, aunque esta
		// declaración viva dentro de un bucle. Se inicializa acá mismo a
		// NULL/cero para que la ranura sea segura desde la entrada de la
		// función (el store de arriba puede correr mucho después, tras
		// código que lanza).
		if esTipoHeap(tipoDecl) {
			c.initBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), ptr)
			c.registrarRaiz(ptr, tipoDecl)
		} else if st, ok := tipoDecl.(*TipoEstructura); ok && poseeReferencias(st) {
			c.initBlock.NewStore(constant.NewZeroInitializer(c.llvmType(st)), ptr)
			c.registrarRaiz(ptr, tipoDecl)
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
		valor, err := c.emitirExpr(n.Hijos[0], c.funcionActual.Retorno)
		if err != nil {
			return err
		}
		if !valor.tip.Igual(variable.tip) {
			return fmt.Errorf("línea %d: asignación incompatible a '%s'", n.Linea, nombre)
		}
		valor = c.retenerParaAlmacenar(valor)
		if esTipoHeap(variable.tip) {
			// 'nombre' está a punto de dejar de apuntar a lo que tenía
			// antes: liberamos esa referencia para no perderla (fuga de
			// memoria) ahora que reasignar ya no la vuelve inalcanzable
			// sin más.
			//
			// La liberación es INCONDICIONAL a propósito, incluso en el
			// auto-asignamiento 'a = a'. No parece un error: arriba,
			// retenerParaAlmacenar hizo un retain (+1) porque el valor venía
			// compartido (era la variable misma). Con rc = 2 tras el
			// retain, esta liberación baja a 1 y la variable conserva
			// exactamente una referencia. Si se salteara la liberación
			// (por ejemplo comparando punteros), quedaría rc = 2 para un
			// solo dueño y la cadena se fugaría.
			anterior := c.currentBlock.NewLoad(c.llvmType(variable.tip), variable.ptr)
			c.emitirLiberarValor(anterior, variable.tip)
		}
		c.currentBlock.NewStore(valor.ref, variable.ptr)
		return nil

	case "ASIGNACION_CAMPO":
		// p.campo = expr  /  p.campo += expr (desazucarado). La variable
		// es un alloca del struct; se calcula el puntero al campo con GEP
		// y se le asigna, con el mismo manejo de refcount que una
		// asignación de variable: retener el valor si viene compartido y
		// liberar la referencia anterior del campo si es heap.
		//
		// BUG-FIX: la comparación `anterior != valor.ref` en Go compara
		// value.Value interfaces (cada NewLoad crea una instrucción LLVM
		// nueva), NO punteros en runtime, así que nunca era igual. Aquí
		// eso NO es un problema: la liberación de abajo es incondicional a
		// propósito. El retain que hizo retenerParaAlmacenar (+1) ya
		//aternion la referencia del valor nuevo, así que liberar la
		// anterior (una vez, incluso si es el mismo objeto) deja rc = 1
		// para el único dueño. Condicionar esa liberación produce una
		// fuga: 'a = a' dejaría rc = 2 para un solo dueño.
		baseNodo, campoNodo, valorNodo := n.Hijos[0], n.Hijos[1], n.Hijos[2]
		nombreBase, _ := baseNodo.Valor.(string)
		nombreCampo, _ := campoNodo.Valor.(string)
		variable, ok := c.lookupVar(nombreBase)
		if !ok {
			return fmt.Errorf("línea %d: variable '%s' no declarada", n.Linea, nombreBase)
		}
		s, ok := variable.tip.(*TipoEstructura)
		if !ok {
			return fmt.Errorf("línea %d: '%s' no es una estructura", n.Linea, nombreBase)
		}
		_, idx, ok := s.Campo(nombreCampo)
		if !ok {
			return fmt.Errorf("línea %d: la estructura '%s' no tiene el campo '%s'", n.Linea, s.Nombre, nombreCampo)
		}
		tipoCampo := s.Campos[idx].Tipo
		valor, err := c.emitirExpr(valorNodo, tipoCampo)
		if err != nil {
			return err
		}
		if !valor.tip.Igual(tipoCampo) {
			return fmt.Errorf("línea %d: asignación incompatible al campo '%s' de '%s'", n.Linea, nombreCampo, nombreBase)
		}
		valor = c.retenerParaAlmacenar(valor)
		structPtr := variable.ptr
		fieldPtr := c.currentBlock.NewGetElementPtr(c.llvmType(s), structPtr,
			constant.NewInt(types.I32, 0), constant.NewInt(types.I32, int64(idx)))
		if esTipoHeap(tipoCampo) {
			// Cargar el puntero anterior del campo PARA COMPARAR en runtime
			// con el nuevo valor (no para liberar inmediatamente).
			anteriorLoad := c.currentBlock.NewLoad(c.llvmType(tipoCampo), fieldPtr)
			// Comparar en runtime si anterior == valor.ref.
			// Los hilos que construyeron `anteriorLoad` y `valor.ref`
			// pueden ser diferentes instrucciones LLVM, pero si apuntan
			// al mismo objeto en runtime, no debemos liberar.
			esIgual := c.currentBlock.NewICmp(enum.IPredEQ, anteriorLoad, valor.ref)
			// Crear bloques condicionales para liberar solo `anterior`
			// si es diferente de `valor.ref`.
			noLiberar := c.currentFunc.NewBlock(c.newLabel("acampo.no_liberar"))
			liberar := c.currentFunc.NewBlock(c.newLabel("acampo.liberar"))
			despues := c.currentFunc.NewBlock(c.newLabel("acampo.despues"))
			c.currentBlock.NewCondBr(esIgual, noLiberar, liberar)
			// --- bloque: no liberar ---
			c.currentBlock = noLiberar
			c.currentBlock.NewBr(despues)
			// --- bloque: liberar ---
			c.currentBlock = liberar
			c.emitirLiberarValor(anteriorLoad, tipoCampo)
			c.currentBlock.NewBr(despues)
			// --- bloque: después ---
			c.currentBlock = despues
		}
		c.currentBlock.NewStore(valor.ref, fieldPtr)
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
			val = c.retenerParaAlmacenar(val)
			listPtr := c.currentBlock.NewBitCast(contenedorVal, types.NewPointer(types.I8))
			linea := constant.NewInt(types.I64, int64(n.Linea))
			if esElementoGenerico(cont.Elemento) {
				// Lista de listas / de estructuras: el valor se pasa por
				// puntero a un temporal (el runtime copia desc->size bytes
				// y adopta las referencias que contiene).
				desc, err := c.descriptorElemento(cont.Elemento)
				if err != nil {
					return fmt.Errorf("línea %d: %v", n.Linea, err)
				}
				tmp := c.initBlock.NewAlloca(c.llvmType(cont.Elemento))
				c.currentBlock.NewStore(val.ref, tmp)
				valPtr := c.currentBlock.NewBitCast(tmp, types.NewPointer(types.I8))
				c.currentBlock.NewCall(c.rtListSetGen, listPtr, idx.ref, valPtr, desc, linea)
				return nil
			}
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
			if !ok1 {
				return fmt.Errorf("línea %d: tipo de clave de diccionario no soportado en codegen", n.Linea)
			}
			// Valor genérico (lista o estructura): descriptor + set_gen.
			// El runtime adopta el valor (y libera el pisado): retener
			// antes si venía de otra ubicación; las estructuras por valor
			// necesitan dirección.
			if esElementoGenerico(cont.Valor) {
				desc, err := c.descriptorElemento(cont.Valor)
				if err != nil {
					return fmt.Errorf("línea %d: %v", n.Linea, err)
				}
				setFn, ok := c.rtDictSetGen[kSuf]
				if !ok {
					return fmt.Errorf("línea %d: combinación de tipos de diccionario (%s, genérico) no soportada", n.Linea, kSuf)
				}
				val = c.retenerParaAlmacenar(val)
				tmpVal := c.initBlock.NewAlloca(c.llvmType(cont.Valor))
				c.currentBlock.NewStore(val.ref, tmpVal)
				valPtr := c.currentBlock.NewBitCast(tmpVal, types.NewPointer(types.I8))
				dictPtr := c.currentBlock.NewBitCast(contenedorVal, types.NewPointer(types.I8))
				idx = c.retenerSiCompartido(idx)
				c.currentBlock.NewCall(setFn, dictPtr, idx.ref, valPtr, desc)
				return nil
			}
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
				c.emitirLiberarEnValor(actual, p.tip)
			}
			c.podarRaicesAlRetornar()
			c.currentBlock.NewRet(nil)
			c.blockTerminated = true
			return nil
		}
		valor, err := c.emitirExpr(n.Hijos[0], c.funcionActual.Retorno)
		if err != nil {
			return err
		}
		nombreDevueltoDirecto := ""
		if n.Hijos[0].Tipo == "IDENTIFICADOR" {
			nombreDevueltoDirecto, _ = n.Hijos[0].Valor.(string)
		}
		// El valor devuelto debe llegar al llamador con una referencia
		// PROPIA. Una variable simple la transfiere (ver más abajo); pero
		// un valor compartido que NO es una variable simple ('retornar
		// l[0]', 'retornar p.s', 'retornar toks[i]') sigue siendo del
		// contenedor: sin retenerlo, el llamador lo liberaría y el
		// contenedor lo volvería a liberar (doble free).
		if valor.compartido {
			if st, esEstructura := valor.tip.(*TipoEstructura); esEstructura {
				// Estructura: solo se transfiere desde un local propio (no
				// parámetro ni variable de 'para'); el resto se retiene.
				if poseeReferencias(st) && !(nombreDevueltoDirecto != "" && c.esLocalPropio(nombreDevueltoDirecto)) {
					c.emitirRetenerEnValor(valor.ref, st)
				}
			} else if nombreDevueltoDirecto == "" {
				valor = c.retenerSiCompartido(valor)
			}
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
			// Antes se comparaba lp.nombreVar con el nombre devuelto, lo
			// que falla con shadowing: en 'para x en a: para x en b:
			// retornar x', los DOS loops tienen variable 'x', así que los
			// dos se saltaban la limpieza y el 'x' del loop externo
			// fugaba en cada vuelta. Ahora se compara la IDENTIDAD de la
			// variable (el puntero a su alloca): solo se saltaba la
			// limpieza del loop cuya variable es exactamente el valor que
			// se está devolviendo.
			if lp.limpiezaIter != nil && !c.esLaMismaVariable(lp.nombreVar, nombreDevueltoDirecto) {
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
			c.emitirLiberarEnValor(actual, p.tip)
		}
		c.podarRaicesAlRetornar()
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
		// es un valor recién construido que nadie más va a liberar: hay
		// que devolver sus referencias aquí mismo para no dejarlo fugado.
		//
		// Se usa 'poseeReferencias' y no 'esTipoHeap' porque una estructura
		// no es un valor heap por sí misma (viaja por valor) pero SÍ tiene
		// referencias propias en sus campos: 'crear().s = ...' descartado
		// como sentencia fugaba la cadena de la estructura recién creada
		// (confirmado con LeakSanitizer). emitirLiberarEnValor recorre los
		// campos; para tipos primitivos no hace nada.
		//
		// EXCEPCIÓN (no liberar): si el resultado es un alias de algo que
		// ya tiene dueño ('compartido'), liberarlo sería quitarle una
		// referencia que no nos pertenece. Es el caso de los builtins que
		// mutan o aliasan: 'agregar'/'insertar'/'quitar' devuelven la
		// misma lista que recibieron, y 'primero'/'ultimo' el elemento de
		// la lista. Antes todos los builtins devolvían valores frescos y
		// liberar incondicional era correcto.
		if !v.compartido {
			c.emitirLiberarEnValor(v.ref, v.tip)
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

	// BUG-FIX(fuga): los 'finalmente' de los frames ya resueltos se corren
	// ACÁ, ANTES de construir el mensaje. Antes se construía primero el
	// mensaje (un wini_string_new si no se dio uno, o la evaluación de la
	// expresión) y recién después se corrían esos 'finalmente': si uno de
	// ellos terminaba el camino por su cuenta ('retornar', 'romper',
	// 'lanzar', ...), este 'lanzar' quedaba abandonado y el mensaje ya
	// construido nunca llegaba a 'wini_lanzar' (que es quien toma posesión
	// de esa referencia) — un wini_string_new fugado por cada 'lanzar'
	// dentro de un 'intentar' cuyo 'finalmente' sale temprano. Corriendo los
	// 'finalmente' primero, si terminan ya no construimos nada.
	if err := c.finallyDeFramesResueltos(); err != nil {
		return err
	}
	if c.blockTerminated {
		return nil
	}

	var mensajeVal cgValue
	if len(mensajeNodo.Hijos) > 0 {
		v, err := c.emitirExpr(mensajeNodo.Hijos[0], TipoCadena)
		if err != nil {
			return err
		}
		// Si el mensaje se construyó pero el 'lanzar' no llega a ejecutar
		// (porque la expresión del mensaje terminó el flujo por su cuenta,
		// o porque el código de abajo ya no está en un bloque vivo), esa
		// referencia se pierde. wini_lanzar es el que normalmente toma
		// posesión de ella; si no se le entrega, hay que devolverla acá.
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

	// Los parámetros heap de esta función quedan huérfanos: wini_lanzar no
	// retorna (hace longjmp al 'capturar' correspondiente o termina el
	// proceso), así que el 'retornar' normal que los liberaría nunca se
	// ejecuta. Una función variádica recibe su lista como parámetro heap,
	// así que 'promedio()' que lanzaba por estar vacío fugaba esa lista
	// entera (confirmado con LeakSanitizer). Se devuelven antes de saltar,
	// que es el único punto donde todavía somos dueños.
	for _, p := range c.parametrosHeapActuales {
		actual := c.currentBlock.NewLoad(c.llvmType(p.tip), p.ptr)
		c.emitirLiberarEnValor(actual, p.tip)
		// La ranura sigue registrada como raíz de excepción y ESTE MISMO
		// 'lanzar' va a desenrollar: sin anularla, el desenrollado la
		// liberaría otra vez (doble free). 'retornar' no necesita esto
		// porque poda las raíces antes de salir.
		if st, ok := p.tip.(*TipoEstructura); ok {
			c.currentBlock.NewStore(constant.NewZeroInitializer(c.llvmType(st)), p.ptr)
		} else if esTipoHeap(p.tip) {
			c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), p.ptr)
		}
	}
	// Temporales de expresiones a medio calcular (el 'result' de una
	// cadena interpolada que todavía no terminó, etc).
	c.liberarTemporalesEnCurso()

	tipoGlobal := c.internString(tipoError)
	tipoArrType := tipoGlobal.ContentType.(*types.ArrayType)
	tipoGep := c.currentBlock.NewGetElementPtr(tipoArrType, tipoGlobal,
		constant.NewInt(types.I64, 0), constant.NewInt(types.I64, 0))

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
	popped := 0
	for len(c.tryFrames) > nivelObjetivo {
		frame := c.tryFrames[len(c.tryFrames)-1]
		c.tryFrames = c.tryFrames[:len(c.tryFrames)-1]
		popped++
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
	// BUG-FIX(fuga): al salir de un 'capturar' de forma temprana
	// ('retornar'/'romper'/'continuar') el codegen antes NO liberaba la
	// referencia auxiliar que el runtime guarda al hacer
	// wini_excepcion_tomar_mensaje (ver runtime.h): solo el final normal
	// del cuerpo del catch llamaba a wini_excepcion_descartar, así que esa
	// referencia quedaba viva para siempre (una fuga por cada catch
	// abandonado). Si desapilamos al menos un frame de 'intentar' estamos
	// abandonando todo lo que ese frame cubría —incluida cualquier
	// excepción capturada— y hay que descartarla. Si no se desapiló
	// ninguno (p. ej. un 'romper' de un bucle que vive DENTRO del propio
	// catch), el catch sigue en curso y su 'descartar' normal se encarga.
	// La llamada es idempotente y segura aunque no haya nada reservado.
	if popped > 0 && !c.blockTerminated {
		c.currentBlock.NewCall(c.rtExcepcionDescartar)
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
	var fidTry value.Value = constant.NewInt(types.I64, 0)
	if c.raizFidPtr != nil {
		fidTry = c.currentBlock.NewLoad(types.I64, c.raizFidPtr)
	}
	bufPtr := c.currentBlock.NewCall(c.rtTryPush, fidTry)
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

	// Región abandonable de este 'try': cada ranura registrada mientras
	// se compila su cuerpo, sus 'capturar' o su 'finalmente' se anota acá
	// (además de en las regiones exteriores). Al aterrizar en el despacho
	// (solo se llega vía longjmp), el cuerpo quedó abandonado y sus
	// ranuras se liberan y anulan (ver gancho más abajo). Se desapila al
	// terminar de compilar este 'intentar'.
	c.regionesTry = append(c.regionesTry, nil)
	defer func() {
		c.regionesTry = c.regionesTry[:len(c.regionesTry)-1]
	}()

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
	// Gancho de región: el cuerpo quedó abandonado por el salto (sus
	// variables no van a liberarse por el camino normal). Se liberan y
	// anulan sus ranuras; es idempotente (lo ya liberado a mano está en
	// NULL) y no toca las regiones exteriores (siguen vivas: el 'capturar'
	// puede usar esas variables). Se captura la lista actual porque los
	// 'capturar' que se compilan después agregan las suyas.
	if nreg := len(c.regionesTry); nreg > 0 {
		propia := append([]ranuraRegion(nil), c.regionesTry[nreg-1]...)
		c.liberarRegion(propia)
	}
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
		var varError cgVar
		if nombreVar != "" {
			msg := c.currentBlock.NewCall(c.rtExcepcionTomarMensaje)
			ptr := c.initBlock.NewAlloca(c.llvmType(TipoCadena))
			// Raíz de excepción: si el cuerpo del 'capturar' lanza antes
			// de liberar 'e', el desenrollado la devuelve. Se inicializa
			// en el bloque de entrada (corre una vez) porque el store de
			// abajo puede no haberse ejecutado todavía.
			c.initBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), ptr)
			c.registrarRaiz(ptr, TipoCadena)
			c.currentBlock.NewStore(msg, ptr)
			c.declareVar(nombreVar, cgVar{ptr: ptr, tip: TipoCadena})
			varError = cgVar{ptr: ptr, tip: TipoCadena}
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
			// La variable de error ('capturar T como e') guarda el mensaje
			// capturado con una referencia propia, y la reserva del runtime
			// ya se descartó. Como es una variable de BLOQUE (solo existe
			// dentro del 'capturar'), su referencia se devuelve al salir de
			// acá: si el usuario no la liberó explícitamente, el mensaje se
			// fugaba una vez por cada excepción capturada (confirmado con
			// LeakSanitizer). Solo se toca si sigue siendo un valor heap
			// vivo: si el usuario la liberó, el puntero quedó en NULL y
			// liberar(NULL) es inocuo.
			if varError.ptr != nil {
				actual := c.currentBlock.NewLoad(c.llvmType(varError.tip), varError.ptr)
				c.emitirLiberarValor(actual, varError.tip)
				c.currentBlock.NewStore(
					constant.NewNull(types.NewPointer(types.I8)),
					varError.ptr,
				)
			}
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

	// Si el 'finalmente' terminó por su cuenta ('retornar'/'romper'/
	// 'continuar'/'lanzar'), nadie brancheó hacia 'endBlock' y este queda
	// sin terminador: LLVM exige que TODO basic block termine en una
	// instrucción terminadora, sin importar si es alcanzable. Igual que
	// en la rama sin capturas (más arriba), se documenta como código
	// muerto con 'unreachable' en vez de dejar un bloque inválido que
	// hace paniquear a llir al serializar el módulo.
	if !llegoAEnd {
		endBlock.NewUnreachable()
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
	// dictLiberadoAnticipado indica que el diccionario iterado (un
	// temporal) ya se liberó justo tras extraer sus claves, así que la
	// limpieza de fin de bucle debe saltearlo (ver arriba).
	dictLiberadoAnticipado := false

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
		// El diccionario ya cumplió su misión (extraer las claves, que
		// son independientes: primitivos copiados o cadenas retenidas).
		// Si era un temporal sin otro dueño, se libera acá mismo en vez
		// de esperar al final del bucle: el cuerpo nunca lo toca (solo
		// ve las claves) y así una excepción dentro del cuerpo no lo
		// fuga. Se marca para que la limpieza final no lo libere otra
		// vez (ver dictLiberadoAnticipado).
		if esTipoHeap(iterable.tip) && !iterable.compartido {
			c.emitirLiberarValor(iterable.ref, iterable.tip)
			dictLiberadoAnticipado = true
		}
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

	listPtrVar := c.initBlock.NewAlloca(types.NewPointer(types.I8))
	listOpq := c.currentBlock.NewBitCast(listRef, types.NewPointer(types.I8))
	c.currentBlock.NewStore(listOpq, listPtrVar)
	// Raíz de excepción para el contenedor sobre el que se itera, cuando
	// es un temporal propio (lista de claves recién extraída, o iterable
	// fresco): el bloque final que lo libera no se alcanza si el cuerpo
	// lanza, así que sin raíz se fugaría. Con iterable prestado
	// (compartido) NO se registra: sigue siendo del dueño original y
	// liberarlo acá sería un doble free.
	raizContenedor := false
	if iterandoDiccionario {
		c.initBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), listPtrVar)
		c.registrarRaiz(listPtrVar, NewTipoLista(elemTipo))
		raizContenedor = true
	} else if esTipoHeap(iterable.tip) && !iterable.compartido {
		c.initBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), listPtrVar)
		c.registrarRaiz(listPtrVar, iterable.tip)
		raizContenedor = true
	}

	idxPtr := c.initBlock.NewAlloca(types.I64)
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
	// El cuerpo arranca en un bloque nuevo y vivo: hay que limpiar el
	// flag. 'mientras' y 'si' ya lo hacen; 'para' depended de llegar
	// siempre con el flag en false, lo que es un invariante frágil (es lo
	// que dejó pasar bugs en la emisión de 'finally' y 'break').
	c.blockTerminated = false
	var getCall value.Value
	// Aunque en este contexto el índice siempre cae dentro de rango (la
	// condición del for lo garantiza), pasamos la línea por consistencia
	// con la firma de las funciones de runtime.
	linea := constant.NewInt(types.I64, int64(n.Linea))
	if iterandoCadena {
		getCall = c.currentBlock.NewCall(c.rtStringGetChar, listVal, idxVal, linea)
	} else if esElementoGenerico(elemTipo) {
		// Lista de listas / de estructuras. Una lista interna se retiene
		// más abajo como cualquier valor heap (la variable del 'para' es
		// dueña de su propia referencia). Una estructura queda como
		// ALIAS del elemento: no se retiene ni se libera, así que el
		// cuerpo no debe modificar la lista que está recorriendo.
		getCall = c.cargarSlot(listVal, idxVal, elemTipo, linea)
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
	varPtr := c.initBlock.NewAlloca(c.llvmType(elemTipo))
	// Raíz de excepción para la variable de iteración (corre una vez por
	// invocación, en el bloque de entrada). Solo si es DUEÑA de su
	// referencia: los elementos heap se retienen abajo, pero una
	// estructura queda como ALIAS del elemento de la lista (sin retener)
	// y registrarla liberaría memoria de la lista al desenrollar.
	if _, esEstructura := elemTipo.(*TipoEstructura); !esEstructura && esTipoHeap(elemTipo) {
		c.initBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), varPtr)
		c.registrarRaiz(varPtr, elemTipo)
	}
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
			// La ranura queda anulada: sigue registrada como raíz hasta
			// que la función retorne, y un desenrollado posterior debe
			// ver NULL, no un puntero ya liberado.
			c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), varPtr)
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
		// Si el diccionario temporal ya se liberó tras extraer sus
		// claves (ver dictLiberadoAnticipado), no queda nada que hacer:
		// liberarlo otra vez sería un doble free.
		if dictLiberadoAnticipado {
			return
		}
		if esTipoHeap(iterable.tip) && !iterable.compartido {
			c.emitirLiberarValor(iterable.ref, iterable.tip)
		}
	}

	c.loops = append(c.loops, loopLabels{
		cond:                  incBlock,
		end:                   endBlock,
		nombreVar:             nombreVar,
		varPtr:                varPtr,
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
	if raizContenedor {
		// La ranura sigue registrada como raíz hasta que la función
		// retorne: anularla para que un desenrollado posterior no
		// libere lo que se acaba de liberar acá.
		c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), listPtrVar)
	}
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
		b, ok := n.Valor.(bool)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: literal booleano inválido", n.Linea)
		}
		return cgValue{ref: constant.NewBool(b), tip: TipoBooleano}, nil

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
		// len(texto) directo: len([]byte(texto)) es idéntico para un string de Go
		// (ambos cuentan BYTES, no runes), pero la conversión asigna y copia la
		// cadena entera en el heap solo para poder medirla.
		inst := c.currentBlock.NewCall(c.rtStringNew, gep, constant.NewInt(types.I64, int64(len(texto))))
		return cgValue{ref: inst, tip: TipoCadena}, nil

	case "CADENA_INTERPOLADA":
		texto, _ := n.Valor.(string)
		literales, _ := parseInterpolatedString(texto)

		// Las {expresiones} llegaron parseadas como hijos del nodo (ver
		// Parser.parsearCadenaInterpolada): cada uno es una expresión
		// completa — '{p.x}', '{sumar(1, 2)}', '{lista[i]}' — no solo un
		// nombre de variable. El texto de la expresión ya no se usa acá.

		makeLiteral := func(lit string) value.Value {
			// Las partes literales llegan crudas del lexer (las
			// {expresiones} se re-lexean aparte): se cocinan acá los
			// escapes (\\n, \\t, ...) igual que en CADENA_TEXTO. El largo
			// se mide DESPUÉS de cocinar (bytes reales).
			lit = interpretarEscapes(lit)
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
		// Mientras la interpolación se construye, 'result' no tiene dueño
		// estable. Se registra como temporal en curso para que, si alguna
		// de las {expresiones} lanza, el 'lanzar' la devuelva antes del
		// longjmp en vez de fugarla.
		registrado := c.registrarTemporal(result, TipoCadena)

		litIdx := 1
		for _, hijo := range n.Hijos {
			val, err := c.emitirExpr(hijo, nil)
			if err != nil {
				// Si al evaluar una de las {expresiones} el código
				// LANZA (por ejemplo, una llamada que falla), la cadena
				// que ya veníamos concatenando queda huérfana: nadie la
				// va a liberar. Antes se perdía una cadena por cada
				// interpolación interrumpida por una excepción
				// (confirmado con LeakSanitizer). Se devuelve ahora,
				// mientras aún somos el dueño.
				liberarIntermedio(result)
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
			// 'result' cambió de puntero: se actualiza el temporal registrado
			// para que apunte al valor vivo (los anteriores ya se liberaron
			// arriba).
			c.actualizarTemporal(registrado, result)
		}

		// La expresión terminó bien: el temporal deja de estar en curso y
		// la referencia pasa al cgValue que se devuelve, cuyo dueño es el
		// código que la recibe.
		c.desregistrarTemporal(registrado)
		return cgValue{ref: result, tip: TipoCadena}, nil

	case "IDENTIFICADOR":
		nombre, _ := n.Valor.(string)
		variable, ok := c.lookupVar(nombre)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: variable '%s' no declarada", n.Linea, nombre)
		}
		load := c.currentBlock.NewLoad(c.llvmType(variable.tip), variable.ptr)
		return cgValue{ref: load, tip: variable.tip, compartido: poseeReferencias(variable.tip)}, nil

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
		// Listas: el verificador ya acepta 'x en lista' y tipa el operando
		// izquierdo contra el tipo de elemento, así que acá hay que
		// recorrer la lista comparando. Antes solo se soportaba el
		// diccionario y una expresión que pasaba el verificador moría en
		// el codegen con "'en' todavía solo está soportado en LLVM sobre
		// diccionarios" (verificador.go:715-717).
		if listaTipo, esLista := contenedor.tip.(*TipoLista); esLista {
			return c.emitirEnLista(elemento, contenedor, listaTipo, n.Linea)
		}
		dictTipo, ok := contenedor.tip.(*TipoDiccionario)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: 'en' todavía solo está soportado en LLVM sobre listas y diccionarios", n.Linea)
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
		return c.emitirLlamada(n, expected)

	case "ACCESO_ATRIBUTO":
		base, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		s, ok := base.tip.(*TipoEstructura)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: no es una estructura", n.Linea)
		}
		nombreCampo, _ := n.Valor.(string)
		_, idx, ok := s.Campo(nombreCampo)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: la estructura '%s' no tiene el campo '%s'", n.Linea, s.Nombre, nombreCampo)
		}
		// 'base.ref' ya es un valor SSA del tipo de la estructura, así
		// que leer un campo es un solo 'extractvalue'. Antes se armaba un
		// alloca + store + gep + load (4 instrucciones y una ranura en el
		// stack) solo para copiarla a memoria y volverla a leer. Peor: el
		// alloca caía en el bloque CORRIENTE, y dentro de un bucle eso no
		// lo puede promover mem2reg, así que el frame crecía en cada
		// iteración (una lectura de campo por vuelta podía terminar
		// desbordando el stack en un bucle largo).
		campo := c.currentBlock.NewExtractValue(base.ref, uint64(idx))
		compartido := poseeReferencias(s.Campos[idx].Tipo)
		return cgValue{ref: campo, tip: s.Campos[idx].Tipo, compartido: compartido}, nil

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
			if esElementoGenerico(elemTipo) {
				// Lista de listas / de estructuras. Mismo criterio de
				// propiedad que abajo (FIX 2.3a): si 'base' es un
				// temporal, él era el dueño del elemento, así que se
				// retiene (todas sus referencias, si es una estructura)
				// ANTES de liberar 'base', y el resultado pasa a ser
				// propio. Si 'base' es una variable, el resultado es un
				// alias y quien lo guarde debe retenerlo.
				elem := c.cargarSlot(listPtr, idx.ref, elemTipo, linea)
				if !base.compartido {
					c.emitirRetenerEnValor(elem, elemTipo)
				}
				c.liberarOperandoFrescoSiHeap(base)
				return cgValue{ref: elem, tip: elemTipo, compartido: base.compartido && poseeReferencias(elemTipo)}, nil
			}
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
			if !ok1 {
				return cgValue{}, fmt.Errorf("línea %d: tipo de clave de diccionario no soportado en codegen", n.Linea)
			}
			// Valor genérico (lista o estructura): se pide el slot con
			// chequeo de rango (KeyError capturable) y se carga el valor
			// tipado, con el mismo criterio de propiedad que la lista
			// genérica (FIX 2.3a): si 'base' es temporal, él era el dueño
			// y se retiene antes de liberarlo; si es variable, es alias.
			if esElementoGenerico(b.Valor) {
				desc, err := c.descriptorElemento(b.Valor)
				if err != nil {
					return cgValue{}, fmt.Errorf("línea %d: %v", n.Linea, err)
				}
				slotFn, ok := c.rtDictSlotGen[kSuf]
				if !ok {
					return cgValue{}, fmt.Errorf("línea %d: combinación de tipos de diccionario (%s, genérico) no soportada", n.Linea, kSuf)
				}
				// Las variantes con clave cadena consumen una referencia
				// (igual que wini_dict_get_str_*): retener antes si la
				// clave viene de una variable viva.
				if esTipoHeap(idx.tip) && idx.compartido {
					idx.ref = c.emitirRetenerValor(idx.ref, idx.tip)
				}
				dictPtr := c.currentBlock.NewBitCast(base.ref, types.NewPointer(types.I8))
				linea := constant.NewInt(types.I64, int64(n.Linea))
				slot := c.currentBlock.NewCall(slotFn, dictPtr, idx.ref, desc, linea)
				tipado := c.currentBlock.NewBitCast(slot, types.NewPointer(c.llvmType(b.Valor)))
				elem := c.currentBlock.NewLoad(c.llvmType(b.Valor), tipado)
				if !base.compartido {
					c.emitirRetenerEnValor(elem, b.Valor)
				}
				c.liberarOperandoFrescoSiHeap(base)
				compartidoRes := base.compartido && poseeReferencias(b.Valor)
				return cgValue{ref: elem, tip: b.Valor, compartido: compartidoRes}, nil
			}
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
		if !ok1 {
			return cgValue{}, fmt.Errorf("línea %d: tipo de clave de diccionario no soportado en codegen (la clave solo puede ser entero, decimal, booleano o cadena)", n.Linea)
		}
		// Valor genérico (lista o estructura): va por puntero opaco más
		// descriptor, igual que los elementos de lista genérica.
		if esElementoGenerico(dictTipo.Valor) {
			desc, err := c.descriptorElemento(dictTipo.Valor)
			if err != nil {
				return cgValue{}, fmt.Errorf("línea %d: %v", n.Linea, err)
			}
			setFn, ok := c.rtDictSetGen[kSuf]
			if !ok {
				return cgValue{}, fmt.Errorf("línea %d: combinación de tipos de diccionario (%s, genérico) no soportada", n.Linea, kSuf)
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
				// El diccionario adopta el valor: retener si venía de
				// otra ubicación. El runtime copia desc->size bytes
				// desde la dirección pasada: el valor viaja en un
				// temporal (igual que wini_list_set_gen), sea un
				// puntero (lista) o un struct por valor.
				valorVal = c.retenerParaAlmacenar(valorVal)
				tmpVal := c.initBlock.NewAlloca(c.llvmType(dictTipo.Valor))
				c.currentBlock.NewStore(valorVal.ref, tmpVal)
				valPtr := c.currentBlock.NewBitCast(tmpVal, types.NewPointer(types.I8))
				claveVal = c.retenerSiCompartido(claveVal)
				dictPtr := c.currentBlock.NewBitCast(dictCall, types.NewPointer(types.I8))
				c.currentBlock.NewCall(setFn, dictPtr, claveVal.ref, valPtr, desc)
			}
			return cgValue{ref: dictCall, tip: dictTipo}, nil
		}
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
	// El resultado SIEMPRE es una referencia fresca de la que este valor
	// pasa a ser dueño exclusivo (wini_string_concat retiene una copia
	// adicional cuando un operando llega NULL, en lugar de devolver el
	// puntero del otro tal cual: ver runtime.c). Por eso acá se liberan
	// solo los operandos frescos y el resultado se devuelve tal cual.
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
				if esElementoGenerico(listA.Elemento) {
					desc, err := c.descriptorElemento(listA.Elemento)
					if err != nil {
						return cgValue{}, fmt.Errorf("línea %d: %v", n.Linea, err)
					}
					call := c.currentBlock.NewCall(c.rtListConcatGen, izq.ref, der.ref, desc)
					c.liberarOperandoFrescoSiHeap(izq)
					c.liberarOperandoFrescoSiHeap(der)
					return cgValue{ref: call, tip: izq.tip}, nil
				}
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
			// El divisor puede ser 0: el runtime lanza un ErrorMatematico
			// capturable ("división por cero") en vez de dejar que la
			// instrucción sdiv mate el programa con SIGFPE, que no se
			// puede capturar con 'intentar/capturar'. Se pasan ambos
			// operandos porque 'INT64_MIN / -1' también rompe (el
			// resultado no entra en un int64_t) y eso solo se puede
			// detectar conociendo el dividendo.
			c.currentBlock.NewCall(c.rtCheckDivisor, izq.ref, der.ref, constant.NewInt(types.I64, int64(n.Linea)))
			res = c.currentBlock.NewSDiv(izq.ref, der.ref)
		case "%":
			// Igual que '/', un '%% 0' entero crashea con SIGFPE.
			c.currentBlock.NewCall(c.rtCheckDivisor, izq.ref, der.ref, constant.NewInt(types.I64, int64(n.Linea)))
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
			// Un divisor decimal cero también es un error, no un 'inf'
			// silencioso. IEEE-754 define 1.0/0.0 = inf, pero eso es
			// matemática de máquina, no el resultado que el programa
			// pidió: el autor casi siempre quiere un error. Además, el
			// lenguaje ya lanza ErrorMatematico para el entero, así que
			// 'a / b' que da 0 con decimal y explota con entero era
			// incoherente según el tipo declarado de los operandos.
			c.currentBlock.NewCall(c.rtCheckDivisorDecimal, der.ref,
				constant.NewInt(types.I64, int64(n.Linea)))
			res = c.currentBlock.NewFDiv(izq.ref, der.ref)
		case "%":
			c.currentBlock.NewCall(c.rtCheckDivisorDecimal, der.ref,
				constant.NewInt(types.I64, int64(n.Linea)))
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
		pred, ok := predicadosEntero[op]
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: comparador '%s' no soportado", linea, op)
		}
		res := c.currentBlock.NewICmp(pred, izq.ref, der.ref)
		return cgValue{ref: res, tip: TipoBooleano}, nil
	}
	if izq.tip.Igual(TipoDecimal) {
		pred, ok := predicadosDecimal[op]
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

// construirListaGenerica arma una lista cuyos elementos son listas o
// estructuras: copia los valores a un arreglo temporal y llama a
// wini_list_new_gen con el descriptor del tipo de elemento. La lista pasa a
// ser dueña de las referencias de cada elemento (retenerParaAlmacenar).
func (c *Codegen) construirListaGenerica(elemTipo Tipo, elementos []cgValue) (value.Value, error) {
	desc, err := c.descriptorElemento(elemTipo)
	if err != nil {
		return nil, err
	}
	i8p := types.NewPointer(types.I8)
	if len(elementos) == 0 {
		return c.currentBlock.NewCall(c.rtListNewGen, desc, constant.NewNull(i8p), constant.NewInt(types.I64, 0)), nil
	}
	arrType := types.NewArray(uint64(len(elementos)), c.llvmType(elemTipo))
	arrPtr := c.initBlock.NewAlloca(arrType)
	for i, val := range elementos {
		val = c.retenerParaAlmacenar(val)
		gep := c.currentBlock.NewGetElementPtr(arrType, arrPtr,
			constant.NewInt(types.I64, 0), constant.NewInt(types.I64, int64(i)))
		c.currentBlock.NewStore(val.ref, gep)
	}
	primero := c.currentBlock.NewGetElementPtr(arrType, arrPtr,
		constant.NewInt(types.I64, 0), constant.NewInt(types.I64, 0))
	datos := c.currentBlock.NewBitCast(primero, i8p)
	return c.currentBlock.NewCall(c.rtListNewGen, desc, datos, constant.NewInt(types.I64, int64(len(elementos)))), nil
}

// construirListaRuntime arma en tiempo de ejecución una WiniList a partir
// de 'elementos' ya evaluados (todos del mismo tipo 'elemTipo'): reserva un
// arreglo en el stack, copia cada valor, y llama a la variante tipada de
// wini_list_new_*. Se comparte entre el literal '[...]' y la recolección de
// argumentos de un parámetro variádico estilo Wini ('*nombre:tipo').
func (c *Codegen) construirListaRuntime(elemTipo Tipo, elementos []cgValue) (value.Value, error) {
	if esElementoGenerico(elemTipo) {
		return c.construirListaGenerica(elemTipo, elementos)
	}
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
	arrPtr := c.initBlock.NewAlloca(arrType)

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

// leerElementoLista genera la lectura del elemento 'idxRef' de la lista
// 'base' (ya evaluada), con el criterio de propiedad de INDEXACION
// (FIX 2.3a): si la base es fresca se retiene el elemento heap antes de
// liberarla; si es compartida el resultado es un alias. Lo usan los
// builtins 'primero' y 'ultimo'.
func (c *Codegen) leerElementoLista(base cgValue, listaTipo *TipoLista, idxRef value.Value, linea int64) (cgValue, error) {
	elemTipo := listaTipo.Elemento
	listPtr := c.currentBlock.NewBitCast(base.ref, types.NewPointer(types.I8))
	lineaVal := constant.NewInt(types.I64, linea)
	if esElementoGenerico(elemTipo) {
		elem := c.cargarSlot(listPtr, idxRef, elemTipo, lineaVal)
		if !base.compartido {
			c.emitirRetenerEnValor(elem, elemTipo)
		}
		c.liberarOperandoFrescoSiHeap(base)
		return cgValue{ref: elem, tip: elemTipo, compartido: base.compartido && poseeReferencias(elemTipo)}, nil
	}
	var getCall value.Value
	switch {
	case elemTipo.Igual(TipoEntero):
		getCall = c.currentBlock.NewCall(c.rtListGetI64, listPtr, idxRef, lineaVal)
	case elemTipo.Igual(TipoDecimal):
		getCall = c.currentBlock.NewCall(c.rtListGetF64, listPtr, idxRef, lineaVal)
	case elemTipo.Igual(TipoBooleano):
		getCall = c.currentBlock.NewCall(c.rtListGetI1, listPtr, idxRef, lineaVal)
	case elemTipo.Igual(TipoCadena):
		getCall = c.currentBlock.NewCall(c.rtListGetStr, listPtr, idxRef, lineaVal)
	default:
		return cgValue{}, fmt.Errorf("línea %d: acceso a lista de %s no soportado", linea, elemTipo)
	}
	if esTipoHeap(elemTipo) && !base.compartido {
		getCall = c.emitirRetenerValor(getCall, elemTipo)
	}
	c.liberarOperandoFrescoSiHeap(base)
	compartidoRes := base.compartido && esTipoHeap(elemTipo)
	return cgValue{ref: getCall, tip: elemTipo, compartido: compartidoRes}, nil
}

// emitirInsertar genera 'insertar' in place (y 'agregar' como caso
// particular con índice == longitud): muta la lista y devuelve el MISMO
// puntero, con 'compartido' propagado del operando. La asignación hace
// retener-antes-de-liberar (ver ASIGNACION), así 'l = insertar(l, ...)'
// es seguro; y como sentencia ('insertar(l, ...)') la mutación persiste.
// El operando lista NO se libera acá: sigue vivo como resultado.
func (c *Codegen) emitirInsertar(lista cgValue, listaTipo *TipoLista, indice cgValue, elem cgValue, linea int64) (cgValue, error) {
	listPtr := c.currentBlock.NewBitCast(lista.ref, types.NewPointer(types.I8))
	lineaVal := constant.NewInt(types.I64, linea)
	var call value.Value
	if esElementoGenerico(listaTipo.Elemento) {
		desc, err := c.descriptorElemento(listaTipo.Elemento)
		if err != nil {
			return cgValue{}, fmt.Errorf("línea %d: %v", linea, err)
		}
		// El runtime adopta el valor: se retiene si era compartido y se
		// pasa por dirección (igual que construirListaGenerica).
		val := c.retenerParaAlmacenar(elem)
		tmp := c.initBlock.NewAlloca(c.llvmType(listaTipo.Elemento))
		c.currentBlock.NewStore(val.ref, tmp)
		ptr := c.currentBlock.NewBitCast(tmp, types.NewPointer(types.I8))
		call = c.currentBlock.NewCall(c.rtListInsertGen, listPtr, indice.ref, ptr, desc, lineaVal)
	} else {
		// El runtime adopta la referencia del valor (solo relevante para
		// cadenas): retener si venía de otra ubicación.
		elem = c.retenerSiCompartido(elem)
		switch {
		case listaTipo.Elemento.Igual(TipoEntero):
			call = c.currentBlock.NewCall(c.rtListInsertI64, listPtr, indice.ref, elem.ref, lineaVal)
		case listaTipo.Elemento.Igual(TipoDecimal):
			call = c.currentBlock.NewCall(c.rtListInsertF64, listPtr, indice.ref, elem.ref, lineaVal)
		case listaTipo.Elemento.Igual(TipoBooleano):
			call = c.currentBlock.NewCall(c.rtListInsertI1, listPtr, indice.ref, elem.ref, lineaVal)
		case listaTipo.Elemento.Igual(TipoCadena):
			call = c.currentBlock.NewCall(c.rtListInsertStr, listPtr, indice.ref, elem.ref, lineaVal)
		default:
			return cgValue{}, fmt.Errorf("línea %d: 'insertar' no soportado para listas de %s", linea, listaTipo.Elemento)
		}
	}
	return cgValue{ref: call, tip: lista.tip, compartido: lista.compartido}, nil
}

func (c *Codegen) emitirLlamada(n *Nodo, expected Tipo) (cgValue, error) {
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
			// La estructura pasa a ser dueña de sus campos heap: un
			// argumento que viene de una variable/elemento se retiene.
			arg = c.retenerParaAlmacenar(arg)
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
		// Valores genéricos (lista o estructura): van por descriptor.
		if esElementoGenerico(dictTipo.Valor) {
			desc, err := c.descriptorElemento(dictTipo.Valor)
			if err != nil {
				return cgValue{}, fmt.Errorf("línea %d: %v", n.Linea, err)
			}
			call := c.currentBlock.NewCall(c.rtDictValuesGen, dictPtr, desc)
			// FIX 2.1a: mismo caso que 'claves', para 'valores'.
			c.liberarOperandoFrescoSiHeap(arg)
			return cgValue{ref: call, tip: NewTipoLista(dictTipo.Valor)}, nil
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

	// Built-in: longitud(x) -> entero. Acepta cadena, lista o diccionario
	// y despacha a la función de longitud del runtime que corresponda
	// según el tipo estático del argumento.
	if nombre == "longitud" {
		if len(n.Hijos) != 1 {
			return cgValue{}, fmt.Errorf("línea %d: 'longitud' espera exactamente 1 argumento", n.Linea)
		}
		arg, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		i8p := types.NewPointer(types.I8)
		switch arg.tip.(type) {
		case *TipoLista:
			ptr := c.currentBlock.NewBitCast(arg.ref, i8p)
			call := c.currentBlock.NewCall(c.rtListLen, ptr)
			return cgValue{ref: call, tip: TipoEntero}, nil
		case *TipoDiccionario:
			ptr := c.currentBlock.NewBitCast(arg.ref, i8p)
			call := c.currentBlock.NewCall(c.rtDictLen, ptr)
			return cgValue{ref: call, tip: TipoEntero}, nil
		default:
			if !arg.tip.Igual(TipoCadena) {
				return cgValue{}, fmt.Errorf("línea %d: 'longitud' espera una cadena, lista o diccionario, se encontró %s", n.Linea, arg.tip)
			}
			call := c.currentBlock.NewCall(c.rtStringLen, arg.ref)
			return cgValue{ref: call, tip: TipoEntero}, nil
		}
	}

	// Built-in: agregar(lista, elemento). Muta la lista EN EL LUGAR
	// (como append de Python) y devuelve la MISMA lista: valen tanto
	// 'agregar(l, x)' como 'l = agregar(l, x)'. Se implementa como
	// insertar al final (índice == longitud).
	if nombre == "agregar" {
		if len(n.Hijos) != 2 {
			return cgValue{}, fmt.Errorf("línea %d: 'agregar' espera exactamente 2 argumentos (lista y elemento)", n.Linea)
		}
		lista, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		listaTipo, ok := lista.tip.(*TipoLista)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: 'agregar' espera una lista como primer argumento, se encontró %s", n.Linea, lista.tip)
		}
		elem, err := c.emitirExpr(n.Hijos[1], nil)
		if err != nil {
			return cgValue{}, err
		}
		if !elem.tip.Igual(listaTipo.Elemento) {
			return cgValue{}, fmt.Errorf("línea %d: el elemento de 'agregar' debe ser %s, se encontró %s", n.Linea, listaTipo.Elemento, elem.tip)
		}
		listPtr := c.currentBlock.NewBitCast(lista.ref, types.NewPointer(types.I8))
		largo := c.currentBlock.NewCall(c.rtListLen, listPtr)
		return c.emitirInsertar(lista, listaTipo,
			cgValue{ref: largo, tip: TipoEntero}, elem, int64(n.Linea))
	}

	// Built-in: vacia(x) -> booleano. Verdadero si la cadena, lista o
	// diccionario no tiene elementos. O(1): compara la longitud del
	// runtime con cero.
	if nombre == "vacia" {
		if len(n.Hijos) != 1 {
			return cgValue{}, fmt.Errorf("línea %d: 'vacia' espera exactamente 1 argumento", n.Linea)
		}
		arg, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		var lenCall value.Value
		switch arg.tip.(type) {
		case *TipoLista:
			ptr := c.currentBlock.NewBitCast(arg.ref, types.NewPointer(types.I8))
			lenCall = c.currentBlock.NewCall(c.rtListLen, ptr)
		case *TipoDiccionario:
			ptr := c.currentBlock.NewBitCast(arg.ref, types.NewPointer(types.I8))
			lenCall = c.currentBlock.NewCall(c.rtDictLen, ptr)
		default:
			if !arg.tip.Igual(TipoCadena) {
				return cgValue{}, fmt.Errorf("línea %d: 'vacia' espera una cadena, lista o diccionario, se encontró %s", n.Linea, arg.tip)
			}
			lenCall = c.currentBlock.NewCall(c.rtStringLen, arg.ref)
		}
		cmp := c.currentBlock.NewICmp(enum.IPredEQ, lenCall, constant.NewInt(types.I64, 0))
		return cgValue{ref: cmp, tip: TipoBooleano}, nil
	}

	// Built-in: primero(l) / ultimo(l) -> elemento. En lista vacía el
	// runtime lanza IndexError capturable (índice 0 o -1 fuera de rango).
	if nombre == "primero" || nombre == "ultimo" {
		if len(n.Hijos) != 1 {
			return cgValue{}, fmt.Errorf("línea %d: '%s' espera exactamente 1 argumento (la lista)", n.Linea, nombre)
		}
		base, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		listaTipo, ok := base.tip.(*TipoLista)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: '%s' espera una lista, se encontró %s", n.Linea, nombre, base.tip)
		}
		var idxRef value.Value
		if nombre == "primero" {
			idxRef = constant.NewInt(types.I64, 0)
		} else {
			listPtr := c.currentBlock.NewBitCast(base.ref, types.NewPointer(types.I8))
			largo := c.currentBlock.NewCall(c.rtListLen, listPtr)
			idxRef = c.currentBlock.NewSub(largo, constant.NewInt(types.I64, 1))
		}
		return c.leerElementoLista(base, listaTipo, idxRef, int64(n.Linea))
	}

	// Built-in: insertar(lista, indice, elemento). Muta EN EL LUGAR y
	// devuelve la MISMA lista: valen 'insertar(l, i, x)' y
	// 'l = insertar(l, i, x)'. La original cambia (0..len).
	if nombre == "insertar" {
		if len(n.Hijos) != 3 {
			return cgValue{}, fmt.Errorf("línea %d: 'insertar' espera exactamente 3 argumentos (lista, índice y elemento)", n.Linea)
		}
		lista, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		listaTipo, ok := lista.tip.(*TipoLista)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: 'insertar' espera una lista como primer argumento, se encontró %s", n.Linea, lista.tip)
		}
		indice, err := c.emitirExpr(n.Hijos[1], nil)
		if err != nil {
			return cgValue{}, err
		}
		if !indice.tip.Igual(TipoEntero) {
			return cgValue{}, fmt.Errorf("línea %d: el índice de 'insertar' debe ser entero, se encontró %s", n.Linea, indice.tip)
		}
		elem, err := c.emitirExpr(n.Hijos[2], nil)
		if err != nil {
			return cgValue{}, err
		}
		if !elem.tip.Igual(listaTipo.Elemento) {
			return cgValue{}, fmt.Errorf("línea %d: el elemento de 'insertar' debe ser %s, se encontró %s", n.Linea, listaTipo.Elemento, elem.tip)
		}
		return c.emitirInsertar(lista, listaTipo, indice, elem, int64(n.Linea))
	}

	// Built-in: quitar(lista, indice). Muta EN EL LUGAR y devuelve la
	// MISMA lista: valen 'quitar(l, i)' y 'l = quitar(l, i)'.
	if nombre == "quitar" {
		if len(n.Hijos) != 2 {
			return cgValue{}, fmt.Errorf("línea %d: 'quitar' espera exactamente 2 argumentos (lista e índice)", n.Linea)
		}
		lista, err := c.emitirExpr(n.Hijos[0], nil)
		if err != nil {
			return cgValue{}, err
		}
		listaTipo, ok := lista.tip.(*TipoLista)
		if !ok {
			return cgValue{}, fmt.Errorf("línea %d: 'quitar' espera una lista como primer argumento, se encontró %s", n.Linea, lista.tip)
		}
		indice, err := c.emitirExpr(n.Hijos[1], nil)
		if err != nil {
			return cgValue{}, err
		}
		if !indice.tip.Igual(TipoEntero) {
			return cgValue{}, fmt.Errorf("línea %d: el índice de 'quitar' debe ser entero, se encontró %s", n.Linea, indice.tip)
		}
		listPtr := c.currentBlock.NewBitCast(lista.ref, types.NewPointer(types.I8))
		lineaVal := constant.NewInt(types.I64, int64(n.Linea))
		var call value.Value
		if esElementoGenerico(listaTipo.Elemento) {
			desc, err := c.descriptorElemento(listaTipo.Elemento)
			if err != nil {
				return cgValue{}, fmt.Errorf("línea %d: %v", n.Linea, err)
			}
			call = c.currentBlock.NewCall(c.rtListRemoveGen, listPtr, indice.ref, desc, lineaVal)
		} else {
			code := tipoCode(listaTipo.Elemento)
			if code == -1 {
				return cgValue{}, fmt.Errorf("línea %d: 'quitar' no soportado para listas de %s", n.Linea, listaTipo.Elemento)
			}
			call = c.currentBlock.NewCall(c.rtListRemove, listPtr, indice.ref, constant.NewInt(types.I32, int64(code)), lineaVal)
		}
		// Sin liberar el operando: es el mismo objeto que el resultado.
		return cgValue{ref: call, tip: lista.tip, compartido: lista.compartido}, nil
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
		if expected != nil && expected.Igual(TipoEntero) {
			res := c.currentBlock.NewCall(c.rtStringToI64, call,
				constant.NewInt(types.I64, int64(n.Linea)))
			c.currentBlock.NewCall(c.rtLiberarCadena, call)
			return cgValue{ref: res, tip: TipoEntero}, nil
		}
		if expected != nil && expected.Igual(TipoDecimal) {
			res := c.currentBlock.NewCall(c.rtStringToF64, call,
				constant.NewInt(types.I64, int64(n.Linea)))
			c.currentBlock.NewCall(c.rtLiberarCadena, call)
			return cgValue{ref: res, tip: TipoDecimal}, nil
		}
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
					if esElementoGenerico(elemTipo) && puedeImprimirLista(listTipo) {
						code = tipoCodeGenerico // lista de listas: se imprime sola
					}
					if code == -1 {
						return cgValue{}, fmt.Errorf("línea %d: imprimir listas de %s no soportado (las estructuras no se imprimen: recorré la lista con 'para')", n.Linea, elemTipo)
					}
					listPtr := c.currentBlock.NewBitCast(v.ref, types.NewPointer(types.I8))
					c.currentBlock.NewCall(c.rtPrintList, listPtr, constant.NewInt(types.I32, int64(code)))
				} else if dictTipo, ok := v.tip.(*TipoDiccionario); ok {
					claveCode := tipoCode(dictTipo.Clave)
					valorCode := codigoElemento(dictTipo.Valor)
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
			if st, ok := v.tip.(*TipoEstructura); ok {
				// Libera UNA referencia de cada valor heap que contiene.
				c.emitirLiberarEnValor(v.ref, st)
			} else if listTipo, ok := v.tip.(*TipoLista); ok {
				code := codigoElemento(listTipo.Elemento)
				if code == -1 {
					return cgValue{}, fmt.Errorf("línea %d: liberar listas de %s no soportado", n.Linea, listTipo.Elemento)
				}
				listPtr := c.currentBlock.NewBitCast(v.ref, types.NewPointer(types.I8))
				c.currentBlock.NewCall(c.rtLiberarLista, listPtr, constant.NewInt(types.I32, int64(code)))
			} else if dictTipo, ok := v.tip.(*TipoDiccionario); ok {
				claveCode := tipoCode(dictTipo.Clave)
				valorCode := codigoElemento(dictTipo.Valor)
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
		// Si el argumento es una variable simple, anulamos su puntero
		// justo después de liberar la memoria (use-after-free se
		// convierte en "puntero nulo" en vez de acceso a memoria ya
		// liberada). wini_print_string/wini_print_list/wini_print_dict
		// ya contemplan el puntero nulo y muestran "<nulo>"/"[]"/"{}"
		// en lugar de hacer segfault.
		//
		// No alcanza con anular una variable: 'liberar(l[0])' deja la
		// ranura del elemento apuntando a memoria ya liberada, y un
		// 'liberar(l)' posterior la vuelve a liberar (doble free,
		// confirmado con AddressSanitizer). Por eso, cuando el argumento
		// es una indexación, hay que ir a escribirle NULL en la ranura
		// del contenedor, no en una variable.
		c.anularRanuraLiberada(n)
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
		inst := c.currentBlock.NewCall(c.rtStringNew, gep, constant.NewInt(types.I64, int64(len(nombreTipo))))
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

	// El último parámetro variádico "a la Wini" ('*nombre:tipo') agrupa en
	// realidad 0 o más argumentos posicionales sueltos en una lista, así
	// que no cuenta como parámetro que recibe exactamente un argumento
	// (mismo criterio que el verificador). ultimoEsLista se calcula sobre
	// el último parámetro REAL para que una firma variádica pura active la
	// recolección de los argumentos sueltos.
	numFijos := len(firma.Parametros)
	if firma.EsVariadico {
		numFijos--
	}
	var ultimoEsLista *TipoLista
	if len(firma.Parametros) > 0 {
		ultimoEsLista, _ = firma.Parametros[len(firma.Parametros)-1].(*TipoLista)
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

	// recolectaEnLista vale si los argumentos en la posición del último
	// parámetro (que es una lista) se recolectan en UNA lista real antes de
	// llamar. Para una firma variádica 'a la Wini' eso es siempre cierto:
	// incluso sin ningún argumento suelto hay que construir y pasar una
	// lista VACÍA, porque el parámetro es una lista de todos modos y la
	// función va a iterarla (y liberarla) al entrar. Antes solo se
	// recolectaba cuando sobraban argumentos, así que 'f()' llamaba a la
	// función sin pasarle el puntero de lista y el callee terminaba
	// iterando un NULL (SIGSEGV).
	recolectaEnLista := ultimoEsLista != nil && (firma.EsVariadico || len(n.Hijos) > numFijos)
	// Índice real del último parámetro de la firma: coincide con numFijos
	// salvo que haya un variádico al final (que ocupa la última posición).
	idxUltimoParam := len(firma.Parametros) - 1
	limiteFijos := numFijos
	if recolectaEnLista {
		limiteFijos = idxUltimoParam
	}

	// --- Argumentos nombrados ('f(b = 1, a = 2)') ---
	// Reordenar los nodos de argumentos a la posición del parámetro cuyo
	// nombre trajeron, para que el resto del pipeline (conteo de
	// requeridos, flags "dado", defaults dentro de la función) siga
	// funcionando sin cambios, como si hubieran venido en orden. El
	// verificador ya garantizó: nombres válidos, sin duplicados, y
	// posicionales antes que nombrados; acá solo hay que mapear cada
	// ARG_NOMBRADO al índice de su parámetro y rellenar los posiciones
	// restantes con los posicionales, en orden. Las posiciones de
	// parámetros omitidos quedan en nil: el bucle de abajo las trata
	// como "argumento no pasado" (placeholder + flag "dado" en falso),
	// igual que con los omitidos posicionales.
	// El reordenamiento de argumentos con nombre se saltea si la función es
	// variádica de cualquier clase: en la "a la Wini" los excedentes se
	// recolectan en una lista (y un nombrado no tiene a qué posición
	// asignarse), y en la variádica a la C los '...' no tienen parámetro
	// asociado. La condición va sobre firma.EsVariadico, no sobre
	// 'ultimoEsLista': un parámetro lista que NO es variádico sí acepta
	// argumentos con nombre, y antes se le rechazaban con un error
	// engañoso.
	if !firma.VariadicoC && !firma.EsVariadico {
		tieneNombrados := false
		for _, a := range n.Hijos {
			if a != nil && a.Tipo == "ARG_NOMBRADO" {
				tieneNombrados = true
				break
			}
		}
		if tieneNombrados {
			reordenados := make([]*Nodo, numFijos)
			asignados := make([]bool, numFijos)
			for _, a := range n.Hijos {
				if a.Tipo == "ARG_NOMBRADO" {
					nombreArg, _ := a.Valor.(string)
					idx := firma.nombreAIndice(nombreArg)
					if idx == -1 {
						return cgValue{}, fmt.Errorf("línea %d: '%s' no es un parámetro de '%s'", a.Linea, nombreArg, nombre)
					}
					reordenados[idx] = a.Hijos[0]
					asignados[idx] = true
					continue
				}
				// Posicional: primera posición aún libre (el verificador
				// ya garantizó que no hay posicionales después de nombrados,
				// pero esto también funciona si llegaran desordenados).
				colocado := false
				for j := 0; j < numFijos; j++ {
					if !asignados[j] {
						reordenados[j] = a
						asignados[j] = true
						colocado = true
						break
					}
				}
				if !colocado {
					return cgValue{}, fmt.Errorf("línea %d: demasiados argumentos en la llamada a '%s'", a.Linea, nombre)
				}
			}
			n.Hijos = reordenados
		}
	}

	// args trae un valor por cada parámetro (real si el llamador lo pasó,
	// o un placeholder si se omitió); dados trae, en el mismo orden, un
	// booleano por cada parámetro con valor por defecto indicando si el
	// valor es real o hay que calcular el default dentro de la función.
	args := make([]value.Value, 0, len(n.Hijos))
	dados := make([]value.Value, 0, numFijos-firma.Requeridos)
	for i := 0; i < limiteFijos; i++ {
		tieneDefault := i >= firma.Requeridos
		if i < len(n.Hijos) && n.Hijos[i] != nil {
			arg := n.Hijos[i]
			if arg.Tipo == "ARG_NOMBRADO" {
				return cgValue{}, fmt.Errorf("línea %d: argumento con nombre inesperado en la llamada a '%s'", arg.Linea, nombre)
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
			// Se usa retenerParaAlmacenar (y no retenerSiCompartido) porque
			// una estructura no es un valor heap en sí mismo pero sí tiene
			// campos heap: hay que retenerlos a todos para que la copia que
			// se le pasa al llamado tenga sus propios dueños. Sin esto,
			// el llamado liberaba la cadena/lista que el llamador seguía
			// usando (use-after-free).
			v = c.retenerParaAlmacenar(v)
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
		elementos := make([]cgValue, 0, len(n.Hijos)-idxUltimoParam)
		for _, arg := range n.Hijos[idxUltimoParam:] {
			if arg.Tipo == "ARG_NOMBRADO" {
				return cgValue{}, fmt.Errorf("línea %d: argumento con nombre inesperado en la llamada variádica a '%s'", arg.Linea, nombre)
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
				return cgValue{}, fmt.Errorf("línea %d: argumento con nombre inesperado en la llamada externa a '%s'", arg.Linea, nombre)
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

// registrarTemporal anota una referencia heap creada a mitad del cálculo de
// una expresión, que debe devolverse si esa expresión termina lanzando.
func (c *Codegen) registrarTemporal(ref value.Value, tip Tipo) int {
	c.temporalesEnCurso = append(c.temporalesEnCurso, temporalEnCurso{ref: ref, tip: tip})
	return len(c.temporalesEnCurso) - 1
}

func (c *Codegen) actualizarTemporal(idx int, ref value.Value) {
	if idx >= 0 && idx < len(c.temporalesEnCurso) {
		c.temporalesEnCurso[idx].ref = ref
	}
}

// desregistrarTemporal marca que el temporal ya tiene dueño y no debe
// liberarse en un 'lanzar' posterior.
func (c *Codegen) desregistrarTemporal(idx int) {
	if idx >= 0 && idx < len(c.temporalesEnCurso) {
		c.temporalesEnCurso[idx].ref = nil
	}
}

// liberarTemporalesEnCurso devuelve las referencias heap que se construyeron
// a mitad de expresiones que no llegaron a terminar. Se llama justo antes de
// un longjmp: después del salto, esa ejecución nunca vuelve, así que es el
// único punto donde todavía somos dueños de ellas.
func (c *Codegen) liberarTemporalesEnCurso() {
	for _, t := range c.temporalesEnCurso {
		if t.ref != nil {
			c.emitirLiberarValor(t.ref, t.tip)
		}
	}
	c.temporalesEnCurso = nil
}

// emitirEnLista implementa 'elemento en lista': recorre la lista
// comparando cada elemento con el buscado y devuelve verdadero en cuanto
// encuentra uno. Cubre los tipos primitivos (entero, decimal, booleano y
// cadena), que son los únicos que el verificador admite como elemento de
// una lista en una expresión 'en'.
func (c *Codegen) emitirEnLista(elemento, contenedor cgValue, listaTipo *TipoLista, linea int) (cgValue, error) {
	suf, _, ok := primSuffix(listaTipo.Elemento)
	if !ok {
		return cgValue{}, fmt.Errorf("línea %d: 'en' sobre listas de %s no soportado", linea, listaTipo.Elemento)
	}
	var getFn *ir.Func
	switch suf {
	case "i64":
		getFn = c.rtListGetI64
	case "f64":
		getFn = c.rtListGetF64
	case "i1":
		getFn = c.rtListGetI1
	case "str":
		getFn = c.rtListGetStr
	}
	if getFn == nil {
		return cgValue{}, fmt.Errorf("línea %d: tipo de lista (%s) no soportado para 'en'", linea, suf)
	}

	listPtr := c.currentBlock.NewBitCast(contenedor.ref, types.NewPointer(types.I8))
	idxPtr := c.initBlock.NewAlloca(types.I64)
	c.currentBlock.NewStore(constant.NewInt(types.I64, 0), idxPtr)
	encontradoPtr := c.initBlock.NewAlloca(types.I1)
	c.currentBlock.NewStore(constant.NewBool(false), encontradoPtr)

	condBlock := c.currentFunc.NewBlock(c.newLabel("en.cond"))
	cuerpoBlock := c.currentFunc.NewBlock(c.newLabel("en.body"))
	sigueBlock := c.currentFunc.NewBlock(c.newLabel("en.sigue"))
	endBlock := c.currentFunc.NewBlock(c.newLabel("en.end"))
	c.currentBlock.NewBr(condBlock)

	c.currentBlock = condBlock
	idxVal := c.currentBlock.NewLoad(types.I64, idxPtr)
	lenVal := c.currentBlock.NewCall(c.rtListLen, listPtr)
	iMenor := c.currentBlock.NewICmp(enum.IPredSLT, idxVal, lenVal)
	yaEncontrado := c.currentBlock.NewLoad(types.I1, encontradoPtr)
	// Se sigue mirando mientras queden elementos por recorrer Y todavía no
	// se haya encontrado el buscado. Si lo inverso (seguidor = i < len), el
	// bucle nunca terminaría al encontrar el elemento.
	noEncontrado := c.currentBlock.NewXor(yaEncontrado, constant.NewInt(types.I1, 1))
	seguir := c.currentBlock.NewAnd(iMenor, noEncontrado)
	c.currentBlock.NewCondBr(seguir, cuerpoBlock, endBlock)

	c.currentBlock = cuerpoBlock
	idxVal2 := c.currentBlock.NewLoad(types.I64, idxPtr)
	lineaVal := constant.NewInt(types.I64, int64(linea))
	var actual value.Value
	switch suf {
	case "i64":
		actual = c.currentBlock.NewCall(getFn, listPtr, idxVal2, lineaVal)
	case "f64":
		actual = c.currentBlock.NewCall(getFn, listPtr, idxVal2, lineaVal)
	case "i1":
		actual = c.currentBlock.NewCall(getFn, listPtr, idxVal2, lineaVal)
	case "str":
		actual = c.currentBlock.NewCall(getFn, listPtr, idxVal2, lineaVal)
	}
	var iguales value.Value
	switch suf {
	case "f64":
		iguales = c.currentBlock.NewFCmp(enum.FPredOEQ, actual, elemento.ref)
	case "i1":
		// Los booleanos ya son i1 tanto en la lista como en el operando
		// (wini_list_get_i1 devuelve i1), así que la comparación es directa.
		iguales = c.currentBlock.NewICmp(enum.IPredEQ, actual, elemento.ref)
	case "str":
		// Las cadenas dentro de una lista son PUNTEROS (WiniString*), así
		// que compararlos con '==' solo daría true si fuera literalmente
		// el mismo objeto: "dos" en ["uno","dos"] daba falso. Hay que
		// comparar el CONTENIDO, que es lo que hace wini_string_eq (el
		// mismo que usa el operador '==' de cadenas en emitirComparacion).
		iguales = c.currentBlock.NewCall(c.rtStringEq, actual, elemento.ref)
	default:
		iguales = c.currentBlock.NewICmp(enum.IPredEQ, actual, elemento.ref)
	}
	encontradoPtr2 := c.currentBlock.NewLoad(types.I1, encontradoPtr)
	nuevoEncontrado := c.currentBlock.NewOr(encontradoPtr2, iguales)
	c.currentBlock.NewStore(nuevoEncontrado, encontradoPtr)
	idxSumado := c.currentBlock.NewAdd(c.currentBlock.NewLoad(types.I64, idxPtr), constant.NewInt(types.I64, 1))
	c.currentBlock.NewStore(idxSumado, idxPtr)
	c.currentBlock.NewBr(sigueBlock)

	c.currentBlock = sigueBlock
	c.currentBlock.NewBr(condBlock)

	c.currentBlock = endBlock
	resultado := c.currentBlock.NewLoad(types.I1, encontradoPtr)

	// 'elemento' y 'contenedor' solo se leen para comparar: si eran
	// temporales recién construidos, se liberan acá (mismo criterio que en
	// la versión de diccionario).
	if esTipoHeap(elemento.tip) && !elemento.compartido {
		c.emitirLiberarValor(elemento.ref, elemento.tip)
	}
	c.liberarOperandoFrescoSiHeap(contenedor)
	return cgValue{ref: resultado, tip: TipoBooleano}, nil
}

// anularRanuraLiberada escribe NULL en la ranura de la que 'liberar' acaba
// de soltar la referencia.
//
// El builtin 'liberar' devuelve una referencia al llamador (la que venía
// de la variable, el elemento o el campo), pero la variable, el elemento o
// el campo siguen apuntando a memoria que ya se liberó. Con una variable
// sola eso se tapaba poniendo NULL en su alloca; con 'liberar(l[0])' o
// 'liberar(d[k])' o 'liberar(p.s)' no había ninguna escritura, así que un
// 'liberar(l)' posterior liberaba la MISMA memoria otra vez (heap
// use-after-free confirmado con AddressSanitizer).
func (c *Codegen) anularRanuraLiberada(nodoLlamada *Nodo) {
	if len(nodoLlamada.Hijos) == 0 {
		return
	}
	arg := nodoLlamada.Hijos[0]
	switch arg.Tipo {
	case "IDENTIFICADOR":
		nombre, ok := arg.Valor.(string)
		if !ok {
			return
		}
		variable, ok := c.lookupVar(nombre)
		if !ok {
			return
		}
		if st, esEstructura := variable.tip.(*TipoEstructura); esEstructura {
			// Una estructura no es un puntero: se la deja en cero (campos
			// heap en NULL, que liberar tolera).
			c.currentBlock.NewStore(constant.NewZeroInitializer(c.llvmType(st)), variable.ptr)
			return
		}
		c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), variable.ptr)

	case "INDEXACION":
		// 'liberar(l[i])' / 'liberar(d[clave])': hay que dejarle NULL a la
		// ranura DENTRO del contenedor, que es lo que sigue teniendo la
		// referencia colgando.
		if len(arg.Hijos) != 2 {
			return
		}
		base, err := c.emitirExpr(arg.Hijos[0], nil)
		if err != nil {
			return
		}
		indice, err := c.emitirExpr(arg.Hijos[1], nil)
		if err != nil {
			return
		}
		switch cont := base.tip.(type) {
		case *TipoLista:
			// Para una lista de cadenas, los elementos son punteros
			// (WiniString*). Se pide la dirección de la ranura con
			// wini_list_slot y se le escribe NULL, de modo que un
			// 'liberar(l)' posterior ya no encuentre la referencia
			// colgando. Para elementos que no son puntero no hay nada que
			// anular (y el verificador ya rechaza liberarlos).
			if !cont.Elemento.Igual(TipoCadena) || !indice.tip.Igual(TipoEntero) {
				return
			}
			listPtr := c.currentBlock.NewBitCast(base.ref, types.NewPointer(types.I8))
			// wini_list_slot_str devuelve directamente la dirección de la
			// ranura (i8**), lista en la que escribir NULL.
			slot := c.currentBlock.NewCall(c.rtListSlotStr, listPtr, indice.ref,
				constant.NewInt(types.I64, int64(arg.Linea)))
			c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), slot)

		case *TipoDiccionario:
			// Un diccionario se busca por valor de clave, no por ranura, así
			// que no hay dirección a la que escribirle NULL. Se anula la
			// ranura del VALOR con la función de búsqueda genérica del
			// runtime, que devuelve la dirección (o NULL si no está la
			// clave, en cuyo caso no hay nada que liberar).
			//
			// Valores genéricos (lista o estructura): mismo esquema con
			// slot_gen (una por tipo de clave). A diferencia de la variante
			// para cadenas, slot_gen LANZA KeyError si la clave falta
			// (igual que leer d[clave]): liberar una clave inexistente de
			// un diccionario genérico es un error, no un no-op.
			if esElementoGenerico(cont.Valor) {
				kSuf, _, ok := primSuffix(cont.Clave)
				if !ok {
					return
				}
				desc, err := c.descriptorElemento(cont.Valor)
				if err != nil {
					return
				}
				slotFn, ok := c.rtDictSlotGen[kSuf]
				if !ok {
					return
				}
				// slot_gen consume una referencia de claves cadena (igual
				// que get_str): retener antes si viene de una variable.
				if esTipoHeap(indice.tip) && indice.compartido {
					indice.ref = c.emitirRetenerValor(indice.ref, indice.tip)
				}
				basePtr := c.currentBlock.NewBitCast(base.ref, types.NewPointer(types.I8))
				slot := c.currentBlock.NewCall(slotFn, basePtr, indice.ref, desc,
					constant.NewInt(types.I64, int64(arg.Linea)))
				if !c.blockTerminated {
					esDistinto := c.currentBlock.NewICmp(enum.IPredNE, slot,
						constant.NewNull(types.NewPointer(types.I8)))
					siHay := c.currentFunc.NewBlock(c.newLabel("liberar.dict.slot"))
					despues := c.currentFunc.NewBlock(c.newLabel("liberar.dict.slot.fin"))
					c.currentBlock.NewCondBr(esDistinto, siHay, despues)
					c.currentBlock = siHay
					// 'slot' es la dirección opaca (i8*) de la ranura: se
					// castea al tipo de ranura (puntero para listas, struct
					// para estructuras) antes de anularla.
					if _, esEstructura := cont.Valor.(*TipoEstructura); esEstructura {
						tipado := c.currentBlock.NewBitCast(slot, types.NewPointer(c.llvmType(cont.Valor)))
						c.currentBlock.NewStore(constant.NewZeroInitializer(c.llvmType(cont.Valor)), tipado)
					} else {
						ranura := c.currentBlock.NewBitCast(slot, types.NewPointer(types.NewPointer(types.I8)))
						c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), ranura)
					}
					c.currentBlock.NewBr(despues)
					c.currentBlock = despues
				}
				return
			}
			if !cont.Valor.Igual(TipoCadena) {
				return
			}
			slot := c.currentBlock.NewCall(c.rtDictSlotStr, base.ref, indice.ref)
			if !c.blockTerminated {
				esDistinto := c.currentBlock.NewICmp(enum.IPredNE, slot,
					constant.NewNull(types.NewPointer(types.I8)))
				siHay := c.currentFunc.NewBlock(c.newLabel("liberar.dict.slot"))
				despues := c.currentFunc.NewBlock(c.newLabel("liberar.dict.slot.fin"))
				c.currentBlock.NewCondBr(esDistinto, siHay, despues)
				c.currentBlock = siHay
				c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), slot)
				c.currentBlock.NewBr(despues)
				c.currentBlock = despues
			}
			// La expresión de índice evaluó una clave temporal ('d["a"]'
			// construye la cadena "a" para buscar). Ya se usó para
			// localizar la ranura, así que se devuelve su referencia: si no
			// quedaba fugada una cadena por cada 'liberar(d[clave])'.
			c.liberarOperandoFrescoSiHeap(indice)
		}

	case "ACCESO_ATRIBUTO":
		// 'liberar(p.s)': la referencia colgante está en el campo de la
		// estructura 'p'. Hay que escribirle NULL usando el alloca de la
		// variable (el valor del campo por sí solo no es direccionable).
		if len(arg.Hijos) != 1 {
			return
		}
		nombreBase, ok := arg.Hijos[0].Valor.(string)
		if !ok {
			return
		}
		variable, ok := c.lookupVar(nombreBase)
		if !ok {
			return
		}
		st, ok := variable.tip.(*TipoEstructura)
		if !ok {
			return
		}
		nombreCampo, ok := arg.Valor.(string)
		if !ok {
			return
		}
		_, idx, ok := st.Campo(nombreCampo)
		if !ok {
			return
		}
		campoPtr := c.currentBlock.NewGetElementPtr(c.llvmType(st), variable.ptr,
			constant.NewInt(types.I32, 0), constant.NewInt(types.I32, int64(idx)))
		c.currentBlock.NewStore(constant.NewNull(types.NewPointer(types.I8)), campoPtr)
	}
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
	case destino.Igual(TipoEntero) && v.tip.Igual(TipoCadena):
		res := c.currentBlock.NewCall(c.rtStringToI64, v.ref,
			constant.NewInt(types.I64, int64(linea)))
		c.liberarOperandoFrescoSiHeap(v)
		return cgValue{ref: res, tip: TipoEntero}, nil
	case destino.Igual(TipoDecimal) && v.tip.Igual(TipoCadena):
		res := c.currentBlock.NewCall(c.rtStringToF64, v.ref,
			constant.NewInt(types.I64, int64(linea)))
		c.liberarOperandoFrescoSiHeap(v)
		return cgValue{ref: res, tip: TipoDecimal}, nil
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

// buscarFuncion devuelve la función del módulo con ese nombre (declaración de
// runtime, 'externa' o 'funcion' de Wini), o nil si no existe. Se usa para
// detectar colisiones de símbolo antes de crear una declaración duplicada.
func (c *Codegen) buscarFuncion(nombre string) *ir.Func {
	// Índice nombre -> función. Antes era un escaneo lineal sobre
	// c.module.Funcs, que ya trae ~200 'declare' del runtime más cada
	// función del programa: llamarlo una vez por declaración convertía la
	// generación en O(F²). El índice se construye una sola vez (el módulo
	// no crece después de inicializar el codegen) y la búsqueda queda en
	// O(1).
	if c.funcionesPorNombre == nil {
		c.funcionesPorNombre = make(map[string]*ir.Func, len(c.module.Funcs))
		for _, f := range c.module.Funcs {
			c.funcionesPorNombre[f.Name()] = f
		}
	}
	return c.funcionesPorNombre[nombre]
}

// firmaLLVM arma el tipo de función LLVM (retorno + tipos de los
// parámetros + variádico C) que tendría una declaración con esos parámetros.
// Se usa solo para comparar firmas y decidir si una 'externa' puede reutilizar
// una declaración ya existente.
func firmaLLVM(retorno types.Type, params []*ir.Param, variadico bool) *types.FuncType {
	tipos := make([]types.Type, len(params))
	for i, p := range params {
		tipos[i] = p.Type()
	}
	sig := types.NewFunc(retorno, tipos...)
	sig.Variadic = variadico
	return sig
}
