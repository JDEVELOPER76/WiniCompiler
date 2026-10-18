package wini

import (
	"regexp"
	"strconv"
	"strings"
)

// Lexer implementa el analizador léxico.
//
// Antes de cada posición del texto se probaban los 23 patrones uno por uno, y
// cada FindStringIndex recorría el resto de la línea; un número, por ejemplo,
// tenía que fallar contra CADENA_INTERPOLADA, CADENA_TEXTO, NINGUNO,
// BOOLEANO y DECIMAL antes de llegar a ENTERO. Con pprof, el 90% del tiempo
// de compilación estaba ahí.
//
// Ahora hay UN solo patrón que es la alternancia de todos los de la tabla de
// tokens, con cada alternativa nombrada con su TipoToken. RE2 (el motor de
// Go) es lineal, y entre las alternativas que matchean en la posición más a
// la izquierda elige la primera por orden de aparición: exactamente la
// semántica del bucle anterior ('gana el primer patrón de la tabla que
// matchea en la posición 0'). El lenguaje reconocido no cambia.
type Lexer struct {
	tokensCompilados []DefinicionToken
	// conjunto: los 23 patrones alternados y nombrados.
	conjunto *regexp.Regexp
	// grupoDeTipo da, para cada TipoToken, el número de grupo que le
	// asignó el motor (vía SubexpIndex).
	//
	// No se asume que ese número coincida con la posición del patrón en la
	// tabla: el motor numera por orden de aparición de los paréntesis de
	// apertura, y entre un patrón y el siguiente puede haber grupos
	// internos. Consultar por nombre es lo que garantiza que cada token
	// reciba SU tipo.
	grupoDeTipo map[TipoToken]int
}

// NewLexer crea un nuevo lexer
func NewLexer() *Lexer {
	defs := compilarTokens()

	var patron strings.Builder
	grupoDeTipo := make(map[TipoToken]int, len(defs))
	for _, d := range defs {
		patron.WriteString("(?P<")
		patron.WriteString(string(d.Tipo))
		patron.WriteString(">")
		// Los grupos internos se vuelven no capturantes: no cambia qué
		// matchea, pero evita subgrupos que complican el mapeo.
		patron.WriteString(sinCapturas(d.Patron.String()))
		patron.WriteString(")|")
	}
	// Se quita la barra vertical final.
	s := patron.String()
	s = strings.TrimSuffix(s, "|")

	conjunto, err := regexp.Compile(s)
	if err != nil {
		// No debería ocurrir: los nombres de grupo son los TipoToken, que
		// son identificadores válidos en una expresión regular. Si
		// fallara, tokenizarDesde cae al recorrido lineal.
		conjunto = nil
	} else {
		for _, d := range defs {
			grupoDeTipo[d.Tipo] = conjunto.SubexpIndex(string(d.Tipo))
		}
	}

	return &Lexer{
		tokensCompilados: defs,
		conjunto:         conjunto,
		grupoDeTipo:      grupoDeTipo,
	}
}

// sinCapturas reescribe un patrón para que sus grupos sean no capturantes
// ('(?:' en lugar de '('), dejando intactos los que ya lo son y los grupos
// nombrados.
//
// Motivo: en el patrón combinado del lexer cada alternativa debe aportar un
// solo grupo (el nombrado con el TipoToken). Con grupos internos capturantes
// —como los de `\b(nulo)\b` o `\b(escribir|sino|...)`— el motor asigna
// números de grupo también a esos subgrupos.
//
// La conversión rompería una referencia al grupo (tipo `\1`); ninguno de
// los patrones de la tabla de tokens las usa.
func sinCapturas(p string) string {
	var b strings.Builder
	b.Grow(len(p) + 8)
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			b.WriteByte(p[i])
			if i+1 < len(p) {
				i++
				b.WriteByte(p[i])
			}
		case '[':
			// Clase de caracteres: se copia entera, respetando escapes.
			b.WriteByte('[')
			i++
			for i < len(p) && p[i] != ']' {
				if p[i] == '\\' && i+1 < len(p) {
					b.WriteByte(p[i])
					i++
				}
				if i < len(p) {
					b.WriteByte(p[i])
				}
				i++
			}
			if i < len(p) {
				b.WriteByte(']')
			}
		case '(':
			if i+1 < len(p) && p[i+1] == '?' {
				b.WriteByte('(') // ya es un grupo especial: se copia tal cual
			} else {
				b.WriteString("(?:")
			}
		default:
			b.WriteByte(p[i])
		}
	}
	return b.String()
}

// tokenizarDesde intenta reconocer un token en la posición 0 de 'resto'.
// Devuelve el tipo, el texto y el largo consumido. Si nada matchea en esa
// posición, devuelve ok=false.
func (l *Lexer) tokenizarDesde(resto string) (TipoToken, string, int, bool) {
	if l.conjunto != nil {
		loc := l.conjunto.FindStringSubmatchIndex(resto)
		if loc != nil && loc[0] == 0 {
			// El grupo que matcheó es el primero con subíndice no negativo;
			// se recorre el mapa para traducir su número a TipoToken.
			for g := 1; g*2+1 < len(loc); g++ {
				if loc[g*2] < 0 {
					continue
				}
				for tipo, num := range l.grupoDeTipo {
					if num == g {
						return tipo, resto[loc[g*2]:loc[g*2+1]], loc[g*2+1] - loc[g*2], true
					}
				}
			}
			// Ningún grupo capturado (no debería pasar: todas las
			// alternativas nombran su grupo). Se usa el largo total.
			if loc[1] > 0 {
				return "", resto[:loc[1]], loc[1], true
			}
			return "", "", 0, false
		}
		return "", "", 0, false
	}

	// Respaldo: recorrido lineal, igual que la implementación original.
	for _, d := range l.tokensCompilados {
		m := d.Patron.FindStringIndex(resto)
		if m != nil && m[0] == 0 {
			return d.Tipo, resto[:m[1]], m[1], true
		}
	}
	return "", "", 0, false
}

// interpretarEscapes convierte las secuencias de escape de un literal de
// cadena (YA con sus comillas delimitadoras, que se dejan intactas) a su
// valor real: \\n salto de línea, \\t tabulación, \\r retorno de carro,
// \\\\ barra, \\" comilla doble, \\' comilla simple. Cualquier otra
// secuencia (\x) se deja TAL CUAL (barra + letra): así el código viejo
// que usaba la barra como carácter normal (rutas "C:\\temp" sin t/n/r,
// regex) sigue funcionando, salvo las seis de arriba que ahora sí
// cambian de significado (documentado como evolución del lenguaje).
// Las cadenas triples NO pasan por acá: quedan siempre crudas.
func interpretarEscapes(s string) string {
	idx := strings.IndexByte(s, '\\')
	if idx < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:idx])
	i := idx
	for i < len(s) {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			i++
			continue
		}
		switch s[i+1] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		case '\'':
			b.WriteByte('\'')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i+1])
		}
		i += 2
	}
	return b.String()
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
			tipo, text, largo, ok := l.tokenizarDesde(content[pos:])
			if !ok || largo <= 0 {
				// Error de sintaxis. Un 'largo' cero con match vacío sería un
				// bucle infinito, así que también se trata como error.
				lineaErr := lineNum + 1
				colErr := col
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: "Carácter inesperado en línea " + strconv.Itoa(lineaErr) + ", columna " + strconv.Itoa(colErr),
						Linea:   &lineaErr,
						Columna: &colErr,
					},
				})
			}

			// Ignorar espacios y comentarios
			if tipo != ESPACIO && tipo != COMENTARIO {
				// Las cadenas simples se "cocinan" acá (\\n, \\t, \\", ...):
				// las interpoladas NO, porque sus {expresiones} se
				// re-lexean crudas más tarde y cocinarlas dos veces las
				// rompería; sus partes literales se cocinan en el codegen
				// (ver makeLiteral en CADENA_INTERPOLADA). Las triples ni
				// pasan por acá con contenido real (viajan como marcador y
				// se restituyen crudas después): quedan siempre literales.
				if tipo == CADENA_TEXTO {
					text = interpretarEscapes(text)
				}
				tokens = append(tokens, Token{
					Tipo:    tipo,
					Valor:   text,
					Linea:   lineNum + 1,
					Columna: col,
				})
			}

			pos += largo
			col += largo
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