package wini

import "strings"

type Tipo interface {
	String() string
	Igual(otro Tipo) bool
}

type TipoPrimitivo struct {
	nombre string
}

type CampoEstructura struct {
	Nombre string
	Tipo   Tipo
}

type TipoEstructura struct {
	Nombre string
	Campos []CampoEstructura
}

func (t *TipoEstructura) String() string { return t.Nombre }
func (t *TipoEstructura) Igual(otro Tipo) bool {
	o, ok := otro.(*TipoEstructura)
	return ok && o.Nombre == t.Nombre
}

func (t *TipoEstructura) Campo(nombre string) (Tipo, int, bool) {
	for i, c := range t.Campos {
		if c.Nombre == nombre {
			return c.Tipo, i, true
		}
	}
	return nil, -1, false
}

func (t *TipoPrimitivo) String() string { return t.nombre }
func (t *TipoPrimitivo) Igual(otro Tipo) bool {
	o, ok := otro.(*TipoPrimitivo)
	return ok && o.nombre == t.nombre
}

var (
	TipoEntero   = &TipoPrimitivo{"entero"}
	TipoDecimal  = &TipoPrimitivo{"decimal"}
	TipoBooleano = &TipoPrimitivo{"booleano"}
	TipoCadena   = &TipoPrimitivo{"cadena"}
	TipoVacio    = &TipoPrimitivo{"vacio"}
)

var tiposPrimitivos = map[string]*TipoPrimitivo{
	"entero":   TipoEntero,
	"decimal":  TipoDecimal,
	"booleano": TipoBooleano,
	"cadena":   TipoCadena,
	"vacio":    TipoVacio,
}

// TipoSinValor representa la ausencia de valor de una expresión: es lo que
// devuelven las llamadas que solo efectos secundarios ('escribir(...)' y
// 'liberar(...)'). Antes estas devolvían 'nil' (la interfaz Tipo vacía), y
// cualquier consumidor que hiciera real.Igual(...) sobre ese nil —como
// 'exigir' al validar la condición de un 'si' o de un 'mientras', o la
// comparación contra el tipo declarado de una asignación— entraba en
// nil-pointer dereference y el compilador moría con un panic interno de Go
// en vez de reportar un error. Con este sentinel, el nil-deref desaparece
// y los usos indebidos dan un mensaje de tipo claro.
var TipoSinValor = &TipoPrimitivo{"sin_valor"}

// EsTipoSinValor reporta si 't' es el sentinel de "esta expresión no
// produce valor" (llamadas como 'escribir'/'liberar').
func EsTipoSinValor(t Tipo) bool {
	p, ok := t.(*TipoPrimitivo)
	return ok && p.nombre == "sin_valor"
}

func EsPrimitivo(nombre string) bool {
	_, ok := tiposPrimitivos[nombre]
	return ok
}

func TipoPrimitivoDesdeNombre(nombre string) (Tipo, bool) {
	t, ok := tiposPrimitivos[nombre]
	return t, ok
}

type TipoLista struct {
	Elemento Tipo
}

func NewTipoLista(elemento Tipo) *TipoLista {
	return &TipoLista{Elemento: elemento}
}

func (t *TipoLista) String() string {
	return "lista<" + t.Elemento.String() + ">"
}

func (t *TipoLista) Igual(otro Tipo) bool {
	o, ok := otro.(*TipoLista)
	return ok && t.Elemento.Igual(o.Elemento)
}

type TipoDiccionario struct {
	Clave Tipo
	Valor Tipo
}

func NewTipoDiccionario(clave, valor Tipo) *TipoDiccionario {
	return &TipoDiccionario{Clave: clave, Valor: valor}
}

func (t *TipoDiccionario) String() string {
	return "diccionario<" + t.Clave.String() + ", " + t.Valor.String() + ">"
}

func (t *TipoDiccionario) Igual(otro Tipo) bool {
	o, ok := otro.(*TipoDiccionario)
	return ok && t.Clave.Igual(o.Clave) && t.Valor.Igual(o.Valor)
}

func ClaveDiccionarioValida(t Tipo) bool {
	_, ok := t.(*TipoPrimitivo)
	return ok
}

type TipoFuncion struct {
	Parametros []Tipo
	Retorno    Tipo
	// Nombres trae, para cada parámetro de Parametros, el nombre con el
	// que fue declarado ('funcion f(a: entero, b: entero = 0)' ->
	// ["a", "b"]). Lo usan el verificador y el codegen para resolver
	// argumentos nombrados en las llamadas ('f(b = 1, a = 2)'). Puede
	// estar vacío si la firma se construyó sin nodos de parámetro; no
	// participa de Igual(): dos firmas son del mismo tipo por cantidad y
	// tipos de parámetros, no por cómo se llamen.
	Nombres []string
	// Requeridos es la cantidad de parámetros obligatorios (sin valor por
	// defecto). Los parámetros desde el índice Requeridos en adelante
	// tienen valor por defecto y son opcionales en llamadas posicionales
	// (siempre forman un sufijo: no puede haber un parámetro obligatorio
	// después de uno con valor por defecto).
	Requeridos int
	// EsVariadico indica que el último elemento de Parametros corresponde
	// a un parámetro '*nombre:tipo' (variádico "a la Wini"): en la firma
	// ya aparece como lista<tipo>, pero al llamar la función se pueden
	// pasar sueltos 0 o más argumentos de 'tipo' en esa posición, que se
	// recolectan en una lista real antes de la llamada.
	EsVariadico bool
	// VariadicoC indica que la función (siempre 'externa') admite además
	// argumentos variádicos "a la C" (`...` al final de la firma): no
	// tienen tipo declarado ni se recolectan en una lista, se pasan tal
	// cual a la función nativa como en cualquier llamado a printf().
	VariadicoC bool
}

func NewTipoFuncion(parametros []Tipo, retorno Tipo, requeridos int) *TipoFuncion {
	return &TipoFuncion{Parametros: parametros, Retorno: retorno, Requeridos: requeridos}
}

// nombreAIndice devuelve el índice del parámetro con ese nombre, o -1 si
// la firma no tiene ningún parámetro con ese nombre (o no guarda nombres).
func (t *TipoFuncion) nombreAIndice(nombre string) int {
	for i, n := range t.Nombres {
		if n == nombre && i < len(t.Parametros) {
			return i
		}
	}
	return -1
}

func (t *TipoFuncion) String() string {
	partes := make([]string, len(t.Parametros))
	for i, p := range t.Parametros {
		nombre := ""
		if i < len(t.Nombres) {
			nombre = t.Nombres[i] + ":"
		}
		partes[i] = nombre + p.String()
	}
	if t.VariadicoC {
		partes = append(partes, "...")
	}
	retorno := "vacio"
	if t.Retorno != nil {
		retorno = t.Retorno.String()
	}
	return "funcion(" + strings.Join(partes, ", ") + ") -> " + retorno
}

func (t *TipoFuncion) Igual(otro Tipo) bool {
	o, ok := otro.(*TipoFuncion)
	if !ok || len(t.Parametros) != len(o.Parametros) || t.VariadicoC != o.VariadicoC {
		return false
	}
	for i := range t.Parametros {
		if !t.Parametros[i].Igual(o.Parametros[i]) {
			return false
		}
	}
	if (t.Retorno == nil) != (o.Retorno == nil) {
		return false
	}
	if t.Retorno == nil {
		return true
	}
	return t.Retorno.Igual(o.Retorno)
}
