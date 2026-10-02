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
		if c.Nombre == nombre { return c.Tipo, i, true }
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

func (t *TipoFuncion) String() string {
	partes := make([]string, len(t.Parametros))
	for i, p := range t.Parametros {
		partes[i] = p.String()
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
