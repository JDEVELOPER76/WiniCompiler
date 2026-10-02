package wini

import (
	"regexp"
)

// TipoToken define los tipos de token del lenguaje
type TipoToken string

const (
	COMENTARIO          TipoToken = "COMENTARIO"
	CADENA_INTERPOLADA  TipoToken = "CADENA_INTERPOLADA"
	CADENA_TEXTO        TipoToken = "CADENA_TEXTO"
	NINGUNO             TipoToken = "NINGUNO"
	BOOLEANO            TipoToken = "BOOLEANO"
	DECIMAL             TipoToken = "DECIMAL"
	ENTERO              TipoToken = "ENTERO"
	PALABRA_CLAVE       TipoToken = "PALABRA_CLAVE"
	OPERADOR_LOGICO     TipoToken = "OPERADOR_LOGICO"
	TIPO_DATO           TipoToken = "TIPO_DATO"
	COMPARADOR          TipoToken = "COMPARADOR"
	OPERADOR            TipoToken = "OPERADOR"
	IDENTIFICADOR       TipoToken = "IDENTIFICADOR"
	PARENTESIS          TipoToken = "PARENTESIS"
	CORCHETE            TipoToken = "CORCHETE"
	LLAVE               TipoToken = "LLAVE"
	COMA                TipoToken = "COMA"
	PUNTOS              TipoToken = "PUNTOS"
	PUNTO               TipoToken = "PUNTO"
	ESPACIO             TipoToken = "ESPACIO"
	NUEVA_LINEA         TipoToken = "NUEVA_LINEA"
	OPERADOR_ASIGNACION TipoToken = "OPERADOR_ASIGNACION"
	FLECHA              TipoToken = "FLECHA"
)

// DefinicionToken une un TipoToken con su patrón regex
type DefinicionToken struct {
	Tipo   TipoToken
	Patron *regexp.Regexp
}

// Token representa un token con su información de ubicación
type Token struct {
	Tipo    TipoToken
	Valor   string
	Linea   int
	Columna int
}

// Palabras clave del lenguaje
var palabrasClave = map[string]bool{
	"escribir":   true,
	"sino":       true,
	"demas":      true,
	"si":         true,
	"leer":       true,
	"funcion":    true,
	"retornar":   true,
	"tipo":       true,
	"importar":   true,
	"mientras":   true,
	"para":       true,
	"romper":     true,
	"continuar":  true,
	"intentar":   true,
	"capturar":   true,
	"finalmente": true,
	"lanzar":     true,
	"relanzar":   true,
	"como":       true,
	"liberar":    true,
	"externa":    true,
	"estructura": true,
}

// Nueva función para compilar los tokens
func compilarTokens() []DefinicionToken {
	return []DefinicionToken{
		{COMENTARIO, regexp.MustCompile(`#[^\n]*`)},
		{CADENA_INTERPOLADA, regexp.MustCompile(`c"[^"\n]*"|c'[^'\n]*'`)},
		{CADENA_TEXTO, regexp.MustCompile(`"[^"]*"|'[^']*'`)},
		{NINGUNO, regexp.MustCompile(`\b(nulo)\b`)},
		{BOOLEANO, regexp.MustCompile(`\b(verdadero|falso)\b`)},
		{DECIMAL, regexp.MustCompile(`\d+\.\d+`)},
		{ENTERO, regexp.MustCompile(`\d+`)},
		{PALABRA_CLAVE, regexp.MustCompile(`\b(escribir|sino|demas|si|leer|funcion|retornar|tipo|importar|mientras|para|romper|continuar|intentar|capturar|finalmente|lanzar|relanzar|como|liberar|externa|estructura)\b`)},
		{OPERADOR_LOGICO, regexp.MustCompile(`\b(y|o|no|en)\b`)},
		{TIPO_DATO, regexp.MustCompile(`\b(entero|decimal|cadena|booleano|vacio|diccionario|lista|int|float|bool|dict|list|str)\b`)},
		{COMPARADOR, regexp.MustCompile(`==|!=|<>|<=|>=|<|>`)},
		{OPERADOR_ASIGNACION, regexp.MustCompile(`\+=|-=|=`)},
		{FLECHA, regexp.MustCompile(`->`)},
		{OPERADOR, regexp.MustCompile(`[+\-*/%=]`)},
		{IDENTIFICADOR, regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)},
		{PARENTESIS, regexp.MustCompile(`[()]`)},
		{CORCHETE, regexp.MustCompile(`[\[\]]`)},
		{LLAVE, regexp.MustCompile(`[{}]`)},
		{COMA, regexp.MustCompile(`,`)},
		{PUNTOS, regexp.MustCompile(`:`)},
		{PUNTO, regexp.MustCompile(`\.`)},
		{ESPACIO, regexp.MustCompile(`\s+`)},
		{NUEVA_LINEA, regexp.MustCompile(`\n`)},
	}
}

// Nueva función para verificar si un token es palabra clave
func esPalabraClave(valor string) bool {
	_, ok := palabrasClave[valor]
	return ok
}
