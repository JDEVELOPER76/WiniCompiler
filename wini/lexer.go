package wini

import (
	"strconv"
	"strings"
)

// Lexer implementa el analizador léxico
type Lexer struct {
	tokensCompilados []DefinicionToken
}

// NewLexer crea un nuevo lexer
func NewLexer() *Lexer {
	return &Lexer{
		tokensCompilados: compilarTokens(),
	}
}

// Tokenize convierte un código fuente en una lista de tokens
func (l *Lexer) Tokenize(code string) []Token {
	// Normalizar saltos de línea
	code = strings.ReplaceAll(code, "\r\n", "\n")
	code = strings.ReplaceAll(code, "\r", "\n")

	// Extraer cadenas triples
	code, cadenasTriples := l.extraerCadenasTriples(code)

	lines := strings.Split(code, "\n")
	var tokens []Token

	// Recorrer línea por línea
	for lineNum, line := range lines {
		originalLine := line
		_ = originalLine // Usado para preservar la línea original si es necesario

		// Expandir tabs a 4 espacios
		line = strings.ReplaceAll(line, "\t", "    ")

		// Calcular indentación
		stripped := strings.TrimLeft(line, " ")
		indent := len(line) - len(stripped)

		// Ignorar líneas vacías o que solo tengan comentario
		if strings.TrimSpace(stripped) == "" || strings.HasPrefix(strings.TrimLeft(stripped, " "), "#") {
			continue
		}

		// Token NUEVA_LINEA con valor = indentación
		tokens = append(tokens, Token{
			Tipo:    NUEVA_LINEA,
			Valor:   string(rune(indent)),
			Linea:   lineNum + 1,
			Columna: 0,
		})

		// Tokenizar el contenido (sin indentación)
		content := stripped
		col := indent + 1
		pos := 0
		lenContent := len(content)

		for pos < lenContent {
			matched := false
			for _, tokenDef := range l.tokensCompilados {
				m := tokenDef.Patron.FindStringIndex(content[pos:])
				if m != nil && m[0] == 0 {
					text := content[pos : pos+m[1]]

					// Ignorar espacios y comentarios
					if tokenDef.Tipo != ESPACIO && tokenDef.Tipo != COMENTARIO {
						tokens = append(tokens, Token{
							Tipo:    tokenDef.Tipo,
							Valor:   text,
							Linea:   lineNum + 1,
							Columna: col,
						})
					}

					pos += m[1]
					col += m[1]
					matched = true
					break
				}
			}
			if !matched {
				// Error de sintaxis
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: "Carácter inesperado en línea " + strconv.Itoa(lineNum+1) + ", columna " + strconv.Itoa(col),
						Linea:   &[]int{lineNum + 1}[0],
						Columna: &[]int{col}[0],
					},
				})
			}
		}
	}

	// Restituir cadenas triples
	if len(cadenasTriples) > 0 {
		for i, token := range tokens {
			if token.Tipo == CADENA_TEXTO || token.Tipo == CADENA_INTERPOLADA {
				tokens[i].Valor = l.restituirTriple(token.Valor, cadenasTriples)
			}
		}
	}

	return tokens
}

// extraerCadenasTriples busca cadenas delimitadas por comillas triples
func (l *Lexer) extraerCadenasTriples(code string) (string, map[string]string) {
	contenidos := make(map[string]string)
	var resultado strings.Builder
	i := 0
	n := len(code)
	contador := 0

	for i < n {
		esC := i < n-3 && code[i] == 'c' && (code[i+1:i+4] == `"""` || code[i+1:i+4] == "'''")
		esSimple := i < n-2 && (code[i:i+3] == `"""` || code[i:i+3] == "'''")

		if esC || esSimple {
			prefijo := ""
			if esC {
				prefijo = "c"
			}
			comillas := ""
			if esC {
				comillas = code[i+1 : i+4]
			} else {
				comillas = code[i : i+3]
			}
			inicioContenido := i
			if esC {
				inicioContenido = i + 4
			} else {
				inicioContenido = i + 3
			}

			cierre := strings.Index(code[inicioContenido:], comillas)
			if cierre == -1 {
				linea := strings.Count(code[:i], "\n") + 1
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: "Cadena triple sin cerrar iniciada en línea " + strconv.Itoa(linea),
						Linea:   &linea,
					},
				})
			}

			contenido := code[inicioContenido : inicioContenido+cierre]
			marcador := "\x00T" + string(rune(contador)) + "\x00"
			contenidos[marcador] = contenido
			contador++

			resultado.WriteString(prefijo)
			resultado.WriteString(`"`)
			resultado.WriteString(marcador)
			resultado.WriteString(`"`)
			resultado.WriteString(strings.Repeat("\n", strings.Count(contenido, "\n")))

			i = inicioContenido + cierre + 3
		} else {
			resultado.WriteByte(code[i])
			i++
		}
	}

	return resultado.String(), contenidos
}

// restituirTriple reemplaza el marcador de cadena triple por su contenido real
func (l *Lexer) restituirTriple(valor string, mapa map[string]string) string {
	if strings.Contains(valor, "\x00T") {
		for marcador, contenido := range mapa {
			if strings.Contains(valor, marcador) {
				return strings.ReplaceAll(valor, marcador, contenido)
			}
		}
	}
	return valor
}
