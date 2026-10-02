package wini

import (
	"fmt"
	"strconv"
	"strings"
)

// Nodo representa un nodo en el AST
type Nodo struct {
	Tipo         string
	Valor        interface{}
	Hijos        []*Nodo
	Linea        int
	TipoInferido Tipo
}

// NewNodo crea un nuevo nodo
func NewNodo(tipo string, valor interface{}, linea int) *Nodo {
	return &Nodo{
		Tipo:  tipo,
		Valor: valor,
		Linea: linea,
		Hijos: []*Nodo{},
	}
}

// AgregarHijo añade uno o varios hijos al nodo y permite encadenar llamadas
func (n *Nodo) AgregarHijo(hijos ...*Nodo) *Nodo {
	n.Hijos = append(n.Hijos, hijos...)
	return n
}

// aliasTipoDato mapea los alias en inglés de un tipo a su nombre canónico
// en español. "cadena" no tiene alias porque el usuario no lo pidió.
var aliasTipoDato = map[string]string{
	"int":   "entero",
	"float": "decimal",
	"bool":  "booleano",
	"dict":  "diccionario",
	"list":  "lista",
	"str":   "cadena",
}

// tipoDatoCanonico devuelve el nombre canónico en español de un TIPO_DATO,
// resolviendo el alias en inglés si corresponde. "entero", "int" -> "entero".
func tipoDatoCanonico(valor string) string {
	if canon, ok := aliasTipoDato[valor]; ok {
		return canon
	}
	return valor
}

// Parser implementa el analizador sintáctico
type Parser struct {
	tokens   []Token
	posicion int
}

// NewParser crea un nuevo parser
func NewParser(tokens []Token) *Parser {
	return &Parser{
		tokens:   tokens,
		posicion: 0,
	}
}

// tokenActual retorna el token actual
func (p *Parser) tokenActual() *Token {
	if p.posicion < len(p.tokens) {
		return &p.tokens[p.posicion]
	}
	return nil
}

// tokenSiguiente avanza al siguiente token
func (p *Parser) tokenSiguiente() {
	p.posicion++
}

// tokenLinea retorna la línea del token actual
func (p *Parser) tokenLinea() int {
	t := p.tokenActual()
	if t != nil {
		return t.Linea
	}
	return 0
}

// saltarNuevasLineas salta los tokens NUEVA_LINEA
func (p *Parser) saltarNuevasLineas() {
	for p.tokenActual() != nil && p.tokenActual().Tipo == NUEVA_LINEA {
		p.tokenSiguiente()
	}
}

// primerTokenNoNuevaLinea devuelve el índice del primer token que no sea
// NUEVA_LINEA a partir de 'desde', SIN mover el cursor del parser. Se usa
// para "espiar" si después de un bloque viene una palabra clave de
// continuación (sino/capturar/finalmente) sin consumir de forma permanente
// el salto de línea que marca el fin del bloque: si la palabra clave no
// aparece, ese salto de línea debe quedar intacto para que el bloque que
// contiene a este (funcion/para/mientras/si/intentar) pueda detectar
// correctamente el des-sangrado.
func (p *Parser) primerTokenNoNuevaLinea(desde int) int {
	i := desde
	for i < len(p.tokens) && p.tokens[i].Tipo == NUEVA_LINEA {
		i++
	}
	return i
}

// esperado verifica que el token actual sea del tipo esperado
func (p *Parser) esperado(tipo TipoToken) string {
	token := p.tokenActual()
	if token != nil && token.Tipo == tipo {
		valor := token.Valor
		p.tokenSiguiente()
		return valor
	}
	linea := 0
	if token != nil {
		linea = token.Linea
	}
	panic(&SintaxisError{
		ErrorConLinea: ErrorConLinea{
			Mensaje: fmt.Sprintf("Se esperaba %s, pero se encontró %v", tipo, token),
			Linea:   &linea,
		},
	})
}

// parsearArgumento parsea un argumento de una llamada
func (p *Parser) parsearArgumento() *Nodo {
	token := p.tokenActual()
	if token == nil {
		return p.parsearExpresion()
	}

	siguiente := p.tokenSiguientePeek()
	if token.Tipo == IDENTIFICADOR && siguiente != nil && siguiente.Tipo == OPERADOR_ASIGNACION && siguiente.Valor == "=" {
		linea := token.Linea
		nombreArg := p.esperado(IDENTIFICADOR)
		p.esperado(OPERADOR_ASIGNACION) // consume '='
		valor := p.parsearExpresion()
		return NewNodo("ARG_NOMBRADO", nombreArg, linea).AgregarHijo(valor)
	}
	return p.parsearExpresion()
}

// tokenSiguientePeek mira el siguiente token sin consumirlo
func (p *Parser) tokenSiguientePeek() *Token {
	if p.posicion+1 < len(p.tokens) {
		return &p.tokens[p.posicion+1]
	}
	return nil
}

// parsearArgumentosLlamada parsea los argumentos entre paréntesis de una
// llamada, con el '(' ya consumido por el llamador, hasta (e incluyendo)
// el ')' de cierre. Se comparte entre 'escribir/leer/liberar' y las
// llamadas por PALABRA_CLAVE/TIPO_DATO/IDENTIFICADOR en parsearAtomo.
func (p *Parser) parsearArgumentosLlamada() []*Nodo {
	argumentos := []*Nodo{}
	if p.tokenActual() != nil && !(p.tokenActual().Tipo == PARENTESIS && p.tokenActual().Valor == ")") {
		argumentos = append(argumentos, p.parsearArgumento())
		for p.tokenActual() != nil && p.tokenActual().Tipo == COMA {
			p.esperado(COMA)
			argumentos = append(argumentos, p.parsearArgumento())
		}
	}
	p.esperado(PARENTESIS)
	return argumentos
}

// parsearSeparadorTipoRetorno consume el separador entre la lista de
// parámetros y el tipo de retorno de una 'funcion'/'externa' — acepta las
// dos formas equivalentes ':' y '->' — y devuelve la anotación de tipo ya
// parseada. 'nombreFn' y 'ejemplo' son solo para el mensaje de error.
func (p *Parser) parsearSeparadorTipoRetorno(nombreFn string, linea int, ejemplo string) *Nodo {
	switch {
	case p.tokenActual() != nil && p.tokenActual().Tipo == FLECHA:
		p.tokenSiguiente() // consumir '->'
	case p.tokenActual() != nil && p.tokenActual().Tipo == PUNTOS:
		p.tokenSiguiente() // consumir ':' de separación
	default:
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: fmt.Sprintf("'%s' debe declarar un tipo de retorno, ej. '%s'", nombreFn, ejemplo),
				Linea:   &linea,
			},
		})
	}
	return p.parsearAnotacionTipo()
}

// encontrarCorcheteCierre encuentra el corchete de cierre
func (p *Parser) encontrarCorcheteCierre(inicio int) int {
	balance := 0
	for i := inicio; i < len(p.tokens); i++ {
		if p.tokens[i].Tipo == CORCHETE {
			switch p.tokens[i].Valor {
			case "[":
				balance++
			case "]":
				balance--
				if balance == 0 {
					return i
				}
			}
		}
	}
	panic(&SintaxisError{
		ErrorConLinea: ErrorConLinea{
			Mensaje: "Corchete ']' no encontrado",
			Linea:   &[]int{p.tokenLinea()}[0],
		},
	})
}

// Parse inicia el análisis sintáctico
func (p *Parser) Parse() *Nodo {
	sentencias := []*Nodo{}
	for p.tokenActual() != nil {
		if p.tokenActual().Tipo == NUEVA_LINEA {
			p.tokenSiguiente()
			continue
		}
		sent := p.parsearSentencia(nil)
		if sent == nil {
			tok := p.tokenActual()
			linea := 0
			if tok != nil {
				linea = tok.Linea
			}
			panic(&SintaxisError{
				ErrorConLinea: ErrorConLinea{
					Mensaje: fmt.Sprintf("Sentencia inesperada o palabra reservada fuera de contexto: %v", tok),
					Linea:   &linea,
				},
			})
		}
		sentencias = append(sentencias, sent)
	}
	return NewNodo("PROGRAMA", nil, 0).AgregarHijo(sentencias...)
}

func indentacionDeToken(token *Token) int {
	if token == nil || token.Tipo != NUEVA_LINEA {
		return -1
	}
	if token.Valor == "" {
		return 0
	}
	if runas := []rune(token.Valor); len(runas) == 1 {
		return int(runas[0])
	}
	if i, err := strconv.Atoi(token.Valor); err == nil {
		return i
	}
	return -1
}

// parsearSentencia parsea una sentencia
func (p *Parser) parsearSentencia(palabrasParada map[string]bool) *Nodo {
	p.saltarNuevasLineas()

	token := p.tokenActual()
	if token == nil {
		return nil
	}

	// Palabra de parada
	if token.Tipo == PALABRA_CLAVE {
		if palabrasParada != nil && palabrasParada[token.Valor] {
			return nil
		}
		if token.Valor == "capturar" || token.Valor == "finalmente" {
			return nil
		}
	}

	// Si encontramos una NUEVA_LINEA con indentación (cierre de bloque), retornar nil
	if token.Tipo == NUEVA_LINEA {
		return nil
	}

	// === VERIFICACIÓN POR VALOR ===
	if token.Tipo == PALABRA_CLAVE {
		switch token.Valor {
		case "intentar":
			return p.parsearIntentar(palabrasParada)
		case "lanzar":
			return p.parsearLanzar()
		case "relanzar":
			linea := p.tokenLinea()
			p.esperado(PALABRA_CLAVE)
			return NewNodo("RELANZAR", nil, linea)
		case "mientras":
			return p.parsearMientras()
		case "para":
			return p.parsearPara()
		case "romper":
			linea := p.tokenLinea()
			p.esperado(PALABRA_CLAVE)
			return NewNodo("ROMPER", nil, linea)
		case "continuar":
			linea := p.tokenLinea()
			p.esperado(PALABRA_CLAVE)
			return NewNodo("CONTINUAR", nil, linea)
		case "retornar":
			linea := p.tokenLinea()
			p.esperado(PALABRA_CLAVE)
			if p.tokenActual() == nil || p.tokenActual().Tipo == NUEVA_LINEA {
				return NewNodo("RETORNO", nil, linea)
			}
			expr := p.parsearExpresion()
			return NewNodo("RETORNO", nil, linea).AgregarHijo(expr)
		case "importar":
			return p.parsearImportar()
		case "funcion":
			return p.parsearFuncion()
		case "externa":
			return p.parsearExterna()
		case "estructura":
			return p.parsearEstructura()
		case "si":
			return p.parsearSi(palabrasParada)
		case "escribir", "leer", "liberar":
			linea := p.tokenLinea()
			nombre := p.esperado(PALABRA_CLAVE)
			p.esperado(PARENTESIS)
			argumentos := p.parsearArgumentosLlamada()
			return NewNodo("LLAMADA", nombre, linea).AgregarHijo(argumentos...)
		}

	}

	// Declaración tipada: 'entero x = 1', 'lista<entero> x = [...]', etc.
	// Distinguimos una declaración de una llamada de conversión explícita
	// (entero(x), lista(x)) mirando el token siguiente:
	//   - TIPO_DATO seguido de IDENTIFICADOR       -> declaración simple
	//   - TIPO_DATO "lista"/"diccionario" seguido
	//     de '<' (genérico)                        -> declaración genérica
	//   - cualquier otro caso (p.ej. seguido de '(')-> conversión, se deja
	//     para parsearExpresion/parsearPrimario.
	if token.Tipo == TIPO_DATO {
		siguiente := p.tokenSiguientePeek()
		nombreCanon := tipoDatoCanonico(token.Valor)
		esGenericoAbierto := siguiente != nil && siguiente.Tipo == COMPARADOR && siguiente.Valor == "<" &&
			(nombreCanon == "lista" || nombreCanon == "diccionario")
		if (siguiente != nil && siguiente.Tipo == IDENTIFICADOR) || esGenericoAbierto {
			return p.parsearDeclaracionTipada()
		}
		// Caso especial: 'entero y = 5'. 'y'/'o'/'no'/'en' son palabras
		// reservadas (operadores lógicos, ver tokens.go) y por lo tanto
		// NUNCA se tokenizan como IDENTIFICADOR, así que la rama de
		// arriba no dispara. Sin este chequeo, el parser caía a
		// "expresión suelta" (más abajo en esta función), que no sabe
		// qué hacer con 'entero' seguido de un operador lógico y termina
		// produciendo un error de sintaxis confuso que apunta a la línea
		// SIGUIENTE (donde sea que la expresión mal formada termine de
		// desmoronarse) en vez de acá, donde está el problema real.
		// Detectamos el patrón "TIPO_DATO OPERADOR_LOGICO =" (una
		// declaración que intenta usar una palabra reservada como
		// nombre) y avisamos de inmediato, en la línea correcta.
		if siguiente != nil && siguiente.Tipo == OPERADOR_LOGICO {
			var despues *Token
			if p.posicion+2 < len(p.tokens) {
				despues = &p.tokens[p.posicion+2]
			}
			if despues != nil && (despues.Valor == "=" || despues.Tipo == OPERADOR_ASIGNACION) {
				linea := siguiente.Linea
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: fmt.Sprintf("'%s' es una palabra reservada (operador lógico) y no se puede usar como nombre de variable", siguiente.Valor),
						Linea:   &linea,
					},
				})
			}
		}
	}

	// Asignaciones
	if token.Tipo == IDENTIFICADOR {
		siguiente := p.tokenSiguientePeek()
		if siguiente != nil && siguiente.Tipo == IDENTIFICADOR && p.posicion+2 < len(p.tokens) && p.tokens[p.posicion+2].Valor == "=" {
			linea := token.Linea
			tipo := p.esperado(IDENTIFICADOR)
			nombre := p.esperado(IDENTIFICADOR)
			p.esperado(OPERADOR_ASIGNACION)
			expr := p.parsearExpresion()
			return NewNodo("ASIGNACION_TIPADA", nombre, linea).
				AgregarHijo(NewNodo("TIPO_DECLARADO", nil, linea).AgregarHijo(NewNodo("ANOTACION_TIPO", tipo, linea))).AgregarHijo(expr)
		}
	}
	if token.Tipo == IDENTIFICADOR {
		siguiente := p.tokenSiguientePeek()
		if siguiente != nil && siguiente.Tipo == CORCHETE && siguiente.Valor == "[" {
			idxCierre := p.encontrarCorcheteCierre(p.posicion + 1)
			if idxCierre+1 < len(p.tokens) {
				tokenTrasIndice := p.tokens[idxCierre+1]
				if tokenTrasIndice.Valor == "=" {
					return p.parsearAsignacion()
				}
				if tokenTrasIndice.Tipo == OPERADOR_ASIGNACION {
					return p.parsearAsignacionCompuesta()
				}
			}
		}
		if siguiente != nil && siguiente.Valor == "=" {
			return p.parsearAsignacion()
		}
		if siguiente != nil && siguiente.Tipo == OPERADOR_ASIGNACION {
			return p.parsearAsignacionCompuesta()
		}
	}

	// Expresión suelta
	return p.parsearExpresion()
}

func (p *Parser) parsearEstructura() *Nodo {
	linea := p.tokenLinea()
	p.esperado(PALABRA_CLAVE)
	nombre := p.esperado(IDENTIFICADOR)
	p.esperado(PUNTOS)
	nl := p.tokenActual()
	if nl == nil || nl.Tipo != NUEVA_LINEA {
		panic(&SintaxisError{ErrorConLinea: ErrorConLinea{Mensaje: "Se esperaba un salto de línea después de la estructura", Linea: &linea}})
	}
	ind := indentacionDeToken(nl)
	p.esperado(NUEVA_LINEA)
	campos := []*Nodo{}
	for p.tokenActual() != nil {
		if p.tokenActual().Tipo == NUEVA_LINEA {
			if indentacionDeToken(p.tokenActual()) < ind { break }
			p.tokenSiguiente()
			continue
		}
		campoLinea := p.tokenLinea()
		campo := p.esperado(IDENTIFICADOR)
		p.esperado(PUNTOS)
		tipo := p.parsearAnotacionTipo()
		campos = append(campos, NewNodo("CAMPO", campo, campoLinea).AgregarHijo(tipo))
		// El salto de línea que sigue al último campo puede tener la
		// indentación del bloque exterior; debe quedar para que el parser
		// superior cierre la estructura sin consumir la siguiente sentencia.
		if p.tokenActual() != nil && p.tokenActual().Tipo == NUEVA_LINEA &&
			indentacionDeToken(p.tokenActual()) >= ind {
			p.tokenSiguiente()
		}
	}
	return NewNodo("ESTRUCTURA", nombre, linea).AgregarHijo(NewNodo("CAMPOS", nil, linea).AgregarHijo(campos...))
}

// parsearAsignacion parsea una asignación
func (p *Parser) parsearAsignacion() *Nodo {
	lineaID := p.tokenLinea()
	idNombre := p.esperado(IDENTIFICADOR)

	// Asignación con índice
	if p.tokenActual() != nil && p.tokenActual().Tipo == CORCHETE && p.tokenActual().Valor == "[" {
		p.esperado(CORCHETE)
		indice := p.parsearExpresion()
		p.esperado(CORCHETE)
		token := p.tokenActual()
		if token == nil || token.Valor != "=" {
			panic(&SintaxisError{
				ErrorConLinea: ErrorConLinea{
					Mensaje: fmt.Sprintf("Se esperaba '=' pero se encontró %v", token),
					Linea:   &lineaID,
				},
			})
		}
		p.tokenSiguiente()
		expr := p.parsearExpresion()
		return NewNodo("ASIGNACION_INDEX", idNombre, lineaID).AgregarHijo(indice).AgregarHijo(expr)
	}

	// Asignación simple
	token := p.tokenActual()
	if token == nil || token.Valor != "=" {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: fmt.Sprintf("Se esperaba '=' pero se encontró %v", token),
				Linea:   &lineaID,
			},
		})
	}
	p.tokenSiguiente()
	expr := p.parsearExpresion()
	return NewNodo("ASIGNACION", idNombre, lineaID).AgregarHijo(expr)
}

// parsearDeclaracionTipada parsea 'anotacionTipo nombre = expr', donde la
// anotación es obligatoria: un primitivo (entero/decimal/booleano/cadena o
// sus alias int/float/bool/str) o un genérico (lista<T>, diccionario<K,V>).
// A diferencia de antes, esto ya no es azúcar sintáctica validada en
// runtime: el verificador de tipos exige que 'expr' tenga exactamente el
// tipo declarado (sin coerción implícita) antes de generar código.
func (p *Parser) parsearDeclaracionTipada() *Nodo {
	lineaTipo := p.tokenLinea()
	tipoNodo := p.parsearAnotacionTipo()

	idNombre := p.esperado(IDENTIFICADOR)

	token := p.tokenActual()
	if token == nil || token.Valor != "=" {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: fmt.Sprintf("Se esperaba '=' pero se encontró %v", token),
				Linea:   &lineaTipo,
			},
		})
	}
	p.tokenSiguiente()
	expr := p.parsearExpresion()

	return NewNodo("ASIGNACION_TIPADA", idNombre, lineaTipo).
		AgregarHijo(NewNodo("TIPO_DECLARADO", nil, lineaTipo).AgregarHijo(tipoNodo)).
		AgregarHijo(expr)
}

// parsearAnotacionTipo parsea una anotación de tipo, obligatoria ahora en
// declaraciones de variable, parámetros de función y tipo de retorno:
//
//	entero | decimal | booleano | cadena | vacio | NombreEstructura
//	lista<AnotacionTipo>
//	diccionario<AnotacionTipo, AnotacionTipo>
//
// Devuelve un nodo "ANOTACION_TIPO" cuyo Valor es el nombre canónico
// ("entero", "lista", "diccionario", ...) y cuyos Hijos son las anotaciones
// de los parámetros de tipo genérico (0 hijos para primitivos, 1 para
// lista, 2 para diccionario: clave y valor). El verificador de tipos es
// quien traduce este árbol sintáctico a un tipos.Tipo real (ver tipos.go).
func (p *Parser) parsearAnotacionTipo() *Nodo {
	linea := p.tokenLinea()
	tok := p.tokenActual()
	if tok == nil || (tok.Tipo != TIPO_DATO && tok.Tipo != IDENTIFICADOR) {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba una anotación de tipo (primitivo, estructura, lista<...> o diccionario<...,...>)",
				Linea:   &linea,
			},
		})
	}
	nombre := tipoDatoCanonico(tok.Valor)
	p.tokenSiguiente()

	nodo := NewNodo("ANOTACION_TIPO", nombre, linea)

	switch nombre {
	case "lista":
		p.esperarSimbolo(COMPARADOR, "<")
		elemento := p.parsearAnotacionTipo()
		p.esperarSimbolo(COMPARADOR, ">")
		nodo.AgregarHijo(elemento)
	case "diccionario":
		p.esperarSimbolo(COMPARADOR, "<")
		clave := p.parsearAnotacionTipo()
		p.esperado(COMA)
		valor := p.parsearAnotacionTipo()
		p.esperarSimbolo(COMPARADOR, ">")
		nodo.AgregarHijo(clave, valor)
	default:
		if tok.Tipo == TIPO_DATO && !EsPrimitivo(nombre) {
			panic(&SintaxisError{
				ErrorConLinea: ErrorConLinea{
					Mensaje: fmt.Sprintf("Tipo desconocido '%s'", nombre),
					Linea:   &linea,
				},
			})
		}
	}
	return nodo
}

// esperarSimbolo consume el token actual si coincide con 'tipoTok' y
// 'valor' exactamente (se usa para '<' y '>' en genéricos, que el lexer ya
// tokeniza como COMPARADOR junto con ==, !=, <=, >=). Da un error de
// sintaxis claro si no coincide, igual que 'esperado'.
func (p *Parser) esperarSimbolo(tipoTok TipoToken, valor string) {
	linea := p.tokenLinea()
	tok := p.tokenActual()
	if tok == nil || tok.Tipo != tipoTok || tok.Valor != valor {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: fmt.Sprintf("Se esperaba '%s' pero se encontró %v", valor, tok),
				Linea:   &linea,
			},
		})
	}
	p.tokenSiguiente()
}

// parsearAsignacionCompuesta parsea 'x += expr', 'x -= expr', etc, y también
// 'x[i] += expr'. Se desazucara (desugar) a una asignación normal cuyo valor
// es una operación binaria: 'x = x + (expr)' / 'x[i] = x[i] + (expr)'.
func (p *Parser) parsearAsignacionCompuesta() *Nodo {
	lineaID := p.tokenLinea()
	idNombre := p.esperado(IDENTIFICADOR)

	// Forma indexada: x[i] += expr
	if p.tokenActual() != nil && p.tokenActual().Tipo == CORCHETE && p.tokenActual().Valor == "[" {
		p.esperado(CORCHETE)
		indice := p.parsearExpresion()
		p.esperado(CORCHETE)

		token := p.tokenActual()
		if token == nil || token.Tipo != OPERADOR_ASIGNACION {
			panic(&SintaxisError{
				ErrorConLinea: ErrorConLinea{
					Mensaje: fmt.Sprintf("Se esperaba un operador de asignación compuesta pero se encontró %v", token),
					Linea:   &lineaID,
				},
			})
		}
		opCompuesto := p.esperado(OPERADOR_ASIGNACION)
		opBase := string(opCompuesto[0])

		expr := p.parsearExpresion()

		valorActual := NewNodo("INDEXACION", nil, lineaID).
			AgregarHijo(NewNodo("IDENTIFICADOR", idNombre, lineaID)).
			AgregarHijo(indice)
		combinado := NewNodo("BINARIA", opBase, lineaID).AgregarHijo(valorActual).AgregarHijo(expr)

		return NewNodo("ASIGNACION_INDEX", idNombre, lineaID).AgregarHijo(indice).AgregarHijo(combinado)
	}

	// Forma simple: x += expr
	token := p.tokenActual()
	if token == nil || token.Tipo != OPERADOR_ASIGNACION {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: fmt.Sprintf("Se esperaba un operador de asignación compuesta pero se encontró %v", token),
				Linea:   &lineaID,
			},
		})
	}
	opCompuesto := p.esperado(OPERADOR_ASIGNACION)
	opBase := string(opCompuesto[0])

	expr := p.parsearExpresion()

	valorActual := NewNodo("IDENTIFICADOR", idNombre, lineaID)
	combinado := NewNodo("BINARIA", opBase, lineaID).AgregarHijo(valorActual).AgregarHijo(expr)

	return NewNodo("ASIGNACION", idNombre, lineaID).AgregarHijo(combinado)
}

// parsearUnParametro parsea un parámetro: nombre:tipo[=expresion]. El
// tipo es OBLIGATORIO (entero/decimal/cadena/booleano, o genérico
// lista<...>/diccionario<...,...>), ya no es azúcar opcional: sin tipo
// de parámetro no hay forma de generar la firma de la función en LLVM
// ni de verificar las llamadas.
//
// El nodo PARAM siempre tiene un hijo "TIPO_PARAM" (con la anotación
// de tipo del parámetro) y opcionalmente un segundo hijo de expresión
// si tiene valor por defecto.
// parsearUnParametro también admite un parámetro variádico opcional con
// prefijo '*' (p.ej. "*datos:entero"), que recolecta en una lista
// (lista<entero>) todos los argumentos posicionales sobrantes. Un
// parámetro variádico no puede tener valor por defecto, y debe ser
// siempre el último de la lista.
func (p *Parser) parsearUnParametro() (*Nodo, bool) {
	lineaParam := p.tokenLinea()

	esVariadico := false
	if p.tokenActual() != nil && p.tokenActual().Tipo == OPERADOR && p.tokenActual().Valor == "*" {
		p.tokenSiguiente() // consumir '*'
		esVariadico = true
	}

	nombreParam := p.esperado(IDENTIFICADOR)
	tipoNodo := "PARAM"
	if esVariadico {
		tipoNodo = "PARAM_VARIADICO"
	}
	paramNodo := NewNodo(tipoNodo, nombreParam, lineaParam)

	if p.tokenActual() == nil || p.tokenActual().Tipo != PUNTOS {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: fmt.Sprintf("El parámetro '%s' debe declarar un tipo, ej. '%s:entero'", nombreParam, nombreParam),
				Linea:   &lineaParam,
			},
		})
	}
	p.tokenSiguiente() // consumir ':'
	anotacion := p.parsearAnotacionTipo()
	paramNodo.AgregarHijo(NewNodo("TIPO_PARAM", nil, lineaParam).AgregarHijo(anotacion))

	if p.tokenActual() != nil && p.tokenActual().Tipo == OPERADOR_ASIGNACION && p.tokenActual().Valor == "=" {
		if esVariadico {
			panic(&SintaxisError{
				ErrorConLinea: ErrorConLinea{
					Mensaje: fmt.Sprintf("El parámetro variádico '*%s' no puede tener un valor por defecto", nombreParam),
					Linea:   &lineaParam,
				},
			})
		}
		p.tokenSiguiente() // consumir '='
		valorPredeterminado := p.parsearExpresion()
		paramNodo.AgregarHijo(valorPredeterminado)
	}
	return paramNodo, esVariadico
}

// parsearListaParametros parsea la lista de parámetros entre paréntesis,
// ya con el '(' consumido por el llamador, hasta (e incluyendo) el ')'
// de cierre. Se comparte entre 'funcion' y 'externa'. Solo se admite un
// parámetro variádico por función. Los parámetros que aparezcan después
// de él quedan como "solo por nombre": no reciben argumentos
// posicionales (esos se los queda el variádico), solo pueden pasarse
// como argumento con nombre.
func (p *Parser) parsearListaParametros(lineaContexto int) []*Nodo {
	parametros := []*Nodo{}
	if p.tokenActual() != nil && !(p.tokenActual().Tipo == PARENTESIS && p.tokenActual().Valor == ")") {
		nodoParam, esVariadico := p.parsearUnParametro()
		parametros = append(parametros, nodoParam)
		yaHuboVariadico := esVariadico
		for p.tokenActual() != nil && p.tokenActual().Tipo == COMA {
			p.esperado(COMA)
			nodoParam, esVariadico = p.parsearUnParametro()
			if esVariadico && yaHuboVariadico {
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: "Una función solo puede tener un parámetro variádico (*nombre)",
						Linea:   &lineaContexto,
					},
				})
			}
			if esVariadico {
				yaHuboVariadico = true
			}
			parametros = append(parametros, nodoParam)
		}
	}
	p.esperado(PARENTESIS)

	// Un parámetro variádico (*nombre) debe ser siempre el ÚLTIMO de la
	// lista. El comentario original de esta función describía una
	// intención de soportar parámetros "solo por nombre" después del
	// variádico (al estilo 'def f(*args, x)' de Python), pero esa
	// semántica nunca se implementó de verdad: ni el verificador
	// (tipoDeLlamada) ni el codegen distinguen ese caso, así que hoy
	// CUALQUIER parámetro después de uno variádico simplemente no
	// funciona (ver TestParser_VariadicoDebeSerUltimo). Como nada
	// depende hoy de aceptar esta forma (no puede estar funcionando en
	// ningún programa existente), la rechazamos temprano con un error
	// claro en vez de dejar que produzca un conteo de argumentos
	// incorrecto y confuso más adelante.
	for i, param := range parametros {
		if param.Tipo == "PARAM_VARIADICO" && i != len(parametros)-1 {
			panic(&SintaxisError{
				ErrorConLinea: ErrorConLinea{
					Mensaje: fmt.Sprintf("el parámetro variádico '*%s' debe ser el último de la lista de parámetros", param.Valor),
					Linea:   &lineaContexto,
				},
			})
		}
	}

	return parametros
}

// esTokenElipsis reporta si a partir de la posición actual del parser hay
// tres tokens PUNTO consecutivos ('...'), la marca de variádico C real en
// una declaración 'externa' (p.ej. 'externa printf(fmt: cadena, ...): entero').
// No consume ningún token.
func (p *Parser) esTokenElipsis() bool {
	if p.tokenActual() == nil || p.tokenActual().Tipo != PUNTO {
		return false
	}
	if p.posicion+2 >= len(p.tokens) {
		return false
	}
	return p.tokens[p.posicion+1].Tipo == PUNTO && p.tokens[p.posicion+2].Tipo == PUNTO
}

// parsearListaParametrosExterna es como parsearListaParametros, pero
// además admite terminar la lista con '...' (variádico C real): en ese
// caso no se agrega ningún nodo de parámetro por él, solo se reporta con
// el segundo valor de retorno. El '...', si aparece, debe ser el último
// elemento. No se admiten parámetros '*nombre' (variádico "a la Wini")
// dentro de una 'externa': no tiene sentido recolectar en una lista Wini
// argumentos que va a leer código C ajeno.
func (p *Parser) parsearListaParametrosExterna(nombreWini string, lineaContexto int) ([]*Nodo, bool) {
	parametros := []*Nodo{}
	variadicoC := false
	if p.tokenActual() != nil && !(p.tokenActual().Tipo == PARENTESIS && p.tokenActual().Valor == ")") {
		for {
			if p.esTokenElipsis() {
				p.tokenSiguiente()
				p.tokenSiguiente()
				p.tokenSiguiente() // consumir los 3 tokens PUNTO de '...'
				variadicoC = true
				break
			}
			nodoParam, esVariadicoWini := p.parsearUnParametro()
			if esVariadicoWini {
				nombreParam, _ := nodoParam.Valor.(string)
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: fmt.Sprintf("la función externa '%s' no admite un parámetro '*%s'; usá '...' al final de la firma para variádico C real", nombreWini, nombreParam),
						Linea:   &lineaContexto,
					},
				})
			}
			parametros = append(parametros, nodoParam)
			if p.tokenActual() != nil && p.tokenActual().Tipo == COMA {
				p.esperado(COMA)
				continue
			}
			break
		}
	}
	p.esperado(PARENTESIS)
	return parametros, variadicoC
}

// parsearExterna parsea la declaración de una función externa, definida
// en una biblioteca nativa (.so/.dll/.a) que se linkeará junto al
// programa. No lleva cuerpo: solo firma. Sintaxis:
//
//	externa nombre(params): tipoRetorno
//	externa "simbolo_c" como nombreWini(params): tipoRetorno
//
// 'params' admite terminar en '...' para exponer una función C
// verdaderamente variádica (p.ej. printf): esos argumentos extra no
// tienen tipo Wini y se pasan tal cual a la función nativa.
//
// La forma con comillas + 'como' permite exponer bajo un nombre Wini
// distinto un símbolo C cuyo nombre no sea un identificador válido en
// Wini, o simplemente para desambiguar/traducir el nombre.
func (p *Parser) parsearExterna() *Nodo {
	linea := p.tokenLinea()
	p.esperado(PALABRA_CLAVE) // externa

	simboloC := ""
	tok := p.tokenActual()
	if tok != nil && (tok.Tipo == CADENA_TEXTO || tok.Tipo == CADENA_INTERPOLADA) {
		simboloC = p.esperado(tok.Tipo)
		simboloC = strings.Trim(simboloC, "\"'")
		if p.tokenActual() == nil || p.tokenActual().Tipo != PALABRA_CLAVE || p.tokenActual().Valor != "como" {
			panic(&SintaxisError{
				ErrorConLinea: ErrorConLinea{
					Mensaje: "Se esperaba 'como nombre' después del símbolo entre comillas en 'externa'",
					Linea:   &linea,
				},
			})
		}
		p.esperado(PALABRA_CLAVE) // como
	}

	nombreWini := p.esperado(IDENTIFICADOR)
	if simboloC == "" {
		simboloC = nombreWini
	}

	p.esperado(PARENTESIS)
	parametros, variadicoC := p.parsearListaParametrosExterna(nombreWini, linea)

	// A diferencia de 'funcion', una 'externa' no admite valores por
	// defecto: no hay cuerpo Wini que los calcule, y codegen le agrega a
	// cada parámetro con default un parámetro "dado" (i1) extra al final
	// de la firma LLVM, lo que rompería el ABI real de la función C.
	for _, param := range parametros {
		if param.Tipo == "PARAM" && len(param.Hijos) > 1 {
			nombreParam, _ := param.Valor.(string)
			panic(&SintaxisError{
				ErrorConLinea: ErrorConLinea{
					Mensaje: fmt.Sprintf("el parámetro '%s' de la función externa '%s' no puede tener un valor por defecto", nombreParam, nombreWini),
					Linea:   &linea,
				},
			})
		}
	}

	// Mismos dos separadores equivalentes que 'funcion' (':' o '->'); acá
	// no hay ':' de cuerpo que abrir después, la declaración termina ahí.
	ejemploRetorno := fmt.Sprintf("%s(...):entero' o '%s(...)-> entero", nombreWini, nombreWini)
	tipoRetorno := p.parsearSeparadorTipoRetorno(nombreWini, linea, ejemploRetorno)

	nodo := NewNodo("EXTERNA", nombreWini, linea)
	nodo.AgregarHijo(NewNodo("PARAMETROS", nil, linea).AgregarHijo(parametros...))
	nodo.AgregarHijo(NewNodo("TIPO_RETORNO", nil, linea).AgregarHijo(tipoRetorno))
	nodo.AgregarHijo(NewNodo("SIMBOLO_C", simboloC, linea))
	nodo.AgregarHijo(NewNodo("VARIADICO_C", variadicoC, linea))
	return nodo
}

// parsearFuncion parsea una función
func (p *Parser) parsearFuncion() *Nodo {
	linea := p.tokenLinea()
	p.esperado(PALABRA_CLAVE) // funcion
	nombreFuncion := p.esperado(IDENTIFICADOR)
	p.esperado(PARENTESIS)

	parametros := p.parsearListaParametros(linea)

	// Tipo de retorno OBLIGATORIO, en cualquiera de dos formas equivalentes:
	//   'funcion nombre(params):tipoRetorno:'   (':' separa params y tipo)
	//   'funcion nombre(params)-> tipoRetorno:' ('->' separa params y tipo)
	// En ambos casos el ':' final sigue siendo el que abre el cuerpo.
	ejemploRetorno := fmt.Sprintf("%s(...):entero:' o '%s(...)-> entero:", nombreFuncion, nombreFuncion)
	tipoRetorno := p.parsearSeparadorTipoRetorno(nombreFuncion, linea, ejemploRetorno)
	p.esperado(PUNTOS) // ':' que abre el cuerpo

	if p.tokenActual() == nil || p.tokenActual().Tipo != NUEVA_LINEA {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea después de ':' en la función",
				Linea:   &linea,
			},
		})
	}

	indentacionBloque := indentacionDeToken(p.tokenActual())
	if indentacionBloque < 0 {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea con indentación en la función",
				Linea:   &linea,
			},
		})
	}
	p.esperado(NUEVA_LINEA)

	// Docstring opcional
	p.saltarNuevasLineas()
	token := p.tokenActual()
	if token != nil && token.Tipo == CADENA_TEXTO {
		p.tokenSiguiente()
		if p.tokenActual() != nil && p.tokenActual().Tipo == NUEVA_LINEA {
			p.tokenSiguiente()
		}
	} else if token != nil && token.Tipo == COMENTARIO && strings.HasPrefix(token.Valor, "#@") {
		p.tokenSiguiente()
		if p.tokenActual() != nil && p.tokenActual().Tipo == NUEVA_LINEA {
			p.tokenSiguiente()
		}
	}

	// Cuerpo
	cuerpo := []*Nodo{}
	for p.tokenActual() != nil {
		tok := p.tokenActual()
		if tok.Tipo == NUEVA_LINEA {
			if indentacionDeToken(tok) < indentacionBloque {
				break
			}
			p.tokenSiguiente()
			continue
		}
		sent := p.parsearSentencia(nil)
		if sent == nil {
			break
		}
		cuerpo = append(cuerpo, sent)
	}

	nodo := NewNodo("FUNCION", nombreFuncion, linea)
	nodo.AgregarHijo(NewNodo("PARAMETROS", nil, linea).AgregarHijo(parametros...))
	nodo.AgregarHijo(NewNodo("TIPO_RETORNO", nil, linea).AgregarHijo(tipoRetorno))
	nodo.AgregarHijo(NewNodo("CUERPO", nil, linea).AgregarHijo(cuerpo...))
	// Nota: el docstring/comentario '#@' inicial ya fue consumido y
	// descartado más arriba; no se adjunta al nodo ni llega al codegen.
	return nodo
}

// parsearSi parsea un condicional si
func (p *Parser) parsearSi(palabrasParada map[string]bool) *Nodo {
	if palabrasParada == nil {
		palabrasParada = make(map[string]bool)
	}
	linea := p.tokenLinea()
	// Columna del propio 'si': se usa para verificar que cualquier
	// 'sino'/'demas' encadenado más adelante pertenezca a ESTE si y no a
	// uno exterior (o interior) que simplemente resulta ser el próximo
	// sino/demas del archivo — mismo criterio que ya usa 'intentar' con
	// capturar/finalmente.
	indentSi := p.tokenActual().Columna - 1
	p.esperado(PALABRA_CLAVE) // si
	condicion := p.parsearExpresion()

	p.esperado(PUNTOS)

	if p.tokenActual() == nil || p.tokenActual().Tipo != NUEVA_LINEA {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea después de ':'",
				Linea:   &linea,
			},
		})
	}

	indentacionBloque := indentacionDeToken(p.tokenActual())
	if indentacionBloque < 0 {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea con indentación después de ':'",
				Linea:   &linea,
			},
		})
	}
	p.esperado(NUEVA_LINEA)

	// Bloque SI
	bloqueSi := []*Nodo{}
	for p.tokenActual() != nil {
		token := p.tokenActual()
		if token.Tipo == NUEVA_LINEA {
			if indentacionDeToken(token) < indentacionBloque {
				break
			}
			p.tokenSiguiente()
			continue
		}
		if token.Tipo == PALABRA_CLAVE && (token.Valor == "sino" || token.Valor == "demas") {
			break
		}
		sent := p.parsearSentencia(nil)
		if sent == nil {
			break
		}
		bloqueSi = append(bloqueSi, sent)
	}

	// Bloque SINO (opcional), incluyendo toda la cadena de 'sino si ... / sino'.
	bloqueSino := p.parsearClausulaSino(linea, indentSi)

	return NewNodo("SI", nil, linea).
		AgregarHijo(condicion).
		AgregarHijo(NewNodo("BLOQUE_SI", nil, linea).AgregarHijo(bloqueSi...)).
		AgregarHijo(NewNodo("BLOQUE_SINO", nil, linea).AgregarHijo(bloqueSino...))
}

// parsearClausulaSino parsea la cadena de cláusulas 'sino'/'sino si'
// (siempre else-if: exigen condición) y, al cerrar la cadena, una cláusula
// terminal 'demas' opcional (else puro, sin condición). 'demas' nunca puede
// seguir siendo encadenado con más 'sino'/'demas': es siempre el final.
// Se "espía" hacia adelante sin consumir de forma permanente los saltos de
// línea: si lo que sigue no es 'sino' ni 'demas', el salto de línea que marca
// el des-sangrado debe quedar disponible para quien llamó a parsearSi
// (funcion/para/mientras/demas si/intentar).
func (p *Parser) parsearClausulaSino(lineaSi int, indentSi int) []*Nodo {
	idxSiguiente := p.primerTokenNoNuevaLinea(p.posicion)
	if idxSiguiente >= len(p.tokens) || p.tokens[idxSiguiente].Tipo != PALABRA_CLAVE {
		return []*Nodo{}
	}
	if p.tokens[idxSiguiente].Valor != "demas" && p.tokens[idxSiguiente].Valor != "sino" {
		return []*Nodo{}
	}
	// El 'sino'/'demas' espiado solo pertenece a ESTE 'si' si está escrito
	// en la misma columna que el 'si' (igual criterio que intentar con
	// capturar/finalmente): si está más adentro, es el cierre de un 'si'
	// anidado que ya debería haberlo consumido él mismo, y si está más
	// afuera, pertenece a un 'si' exterior. En ambos casos hay que dejar
	// el salto de línea intacto para que ese otro bloque lo detecte.
	if idxSiguiente > p.posicion {
		indentEncontrada := indentacionDeToken(&p.tokens[idxSiguiente-1])
		if indentEncontrada != indentSi {
			return []*Nodo{}
		}
	}

	switch p.tokens[idxSiguiente].Valor {
	case "demas":
		return p.parsearClausulademas(idxSiguiente, lineaSi)
	case "sino":
		return p.parsearClausulaSinoSi(idxSiguiente, lineaSi, indentSi)
	default:
		return []*Nodo{}
	}
}

// parsearClausulademas parsea la cláusula terminal 'demas:' (else puro, sin
// condición). Al no llevar condición, no hay nada que encadenar después: si
// aparece un 'sino' u demas 'demas' tras este bloque, se queda sin consumir
// para que el llamador decida qué hacer con él.
func (p *Parser) parsearClausulademas(idxdemas int, lineaSi int) []*Nodo {
	p.posicion = idxdemas
	p.esperado(PALABRA_CLAVE) // demas
	p.esperado(PUNTOS)

	if p.tokenActual() == nil || p.tokenActual().Tipo != NUEVA_LINEA {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea después de ':'",
				Linea:   &lineaSi,
			},
		})
	}

	indentaciondemas := indentacionDeToken(p.tokenActual())
	if indentaciondemas < 0 {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea con indentación después de ':'",
				Linea:   &lineaSi,
			},
		})
	}
	p.esperado(NUEVA_LINEA)

	bloque := []*Nodo{}
	for p.tokenActual() != nil {
		token := p.tokenActual()
		if token.Tipo == NUEVA_LINEA {
			if indentacionDeToken(token) < indentaciondemas {
				break
			}
			p.tokenSiguiente()
			continue
		}
		sent := p.parsearSentencia(nil)
		if sent == nil {
			break
		}
		bloque = append(bloque, sent)
	}
	return bloque
}

// parsearClausulaSinoSi parsea 'sino condicion:' / 'sino si condicion:'
// (equivalentes: 'si' es azúcar opcional) y sigue buscando, tras su bloque,
// más 'sino'/'sino si' encadenados o un 'demas' final.
func (p *Parser) parsearClausulaSinoSi(idxSino int, lineaSi int, indentSi int) []*Nodo {
	p.posicion = idxSino
	p.esperado(PALABRA_CLAVE) // sino

	if p.tokenActual() != nil && p.tokenActual().Tipo == PALABRA_CLAVE && p.tokenActual().Valor == "si" {
		p.esperado(PALABRA_CLAVE)
	}

	condicionSino := p.parsearExpresion()
	p.esperado(PUNTOS)

	if p.tokenActual() == nil || p.tokenActual().Tipo != NUEVA_LINEA {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea después de ':'",
				Linea:   &lineaSi,
			},
		})
	}

	indentacionSinoCond := indentacionDeToken(p.tokenActual())
	if indentacionSinoCond < 0 {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea con indentación después de ':'",
				Linea:   &lineaSi,
			},
		})
	}
	p.esperado(NUEVA_LINEA)

	bloqueSinoCond := []*Nodo{}
	for p.tokenActual() != nil {
		token := p.tokenActual()
		if token.Tipo == NUEVA_LINEA {
			if indentacionDeToken(token) < indentacionSinoCond {
				break
			}
			p.tokenSiguiente()
			continue
		}
		if token.Tipo == PALABRA_CLAVE && (token.Valor == "sino" || token.Valor == "demas") {
			break
		}
		sent := p.parsearSentencia(nil)
		if sent == nil {
			break
		}
		bloqueSinoCond = append(bloqueSinoCond, sent)
	}

	// Clave del fix original: seguir buscando más 'sino'/'sino si' o un
	// 'demas' final en vez de dejar la rama sino de este nodo vacía y perder
	// el resto de la cadena.
	bloqueSinoAnidado := p.parsearClausulaSino(lineaSi, indentSi)

	nodoSinoSi := NewNodo("SI", nil, lineaSi).
		AgregarHijo(condicionSino).
		AgregarHijo(NewNodo("BLOQUE_SI", nil, lineaSi).AgregarHijo(bloqueSinoCond...)).
		AgregarHijo(NewNodo("BLOQUE_SINO", nil, lineaSi).AgregarHijo(bloqueSinoAnidado...))

	return []*Nodo{nodoSinoSi}
}

// parsearIntentar parsea una estructura intentar-capturar-finalmente
func (p *Parser) parsearIntentar(palabrasParada map[string]bool) *Nodo {
	if palabrasParada == nil {
		palabrasParada = make(map[string]bool)
	}

	linea := p.tokenLinea()
	// Columna del propio 'intentar': se usa para verificar que cualquier
	// 'capturar'/'finalmente' encadenado más adelante pertenezca a ESTE
	// bloque y no a uno exterior que simplemente resulta ser el próximo
	// capturar/finalmente en el archivo.
	indentIntentar := p.tokenActual().Columna - 1
	p.esperado(PALABRA_CLAVE) // intentar
	p.esperado(PUNTOS)

	if p.tokenActual() == nil || p.tokenActual().Tipo != NUEVA_LINEA {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea después de ':' en intentar",
				Linea:   &[]int{p.tokenLinea()}[0],
			},
		})
	}

	indentacionBloque := indentacionDeToken(p.tokenActual())
	if indentacionBloque < 0 {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea con indentación en intentar",
				Linea:   &[]int{p.tokenLinea()}[0],
			},
		})
	}
	p.esperado(NUEVA_LINEA)

	paradaTry := make(map[string]bool)
	for k, v := range palabrasParada {
		paradaTry[k] = v
	}
	paradaTry["capturar"] = true
	paradaTry["finalmente"] = true

	bloqueTry := []*Nodo{}
	for p.tokenActual() != nil {
		token := p.tokenActual()
		if token.Tipo == NUEVA_LINEA {
			if indentacionDeToken(token) < indentacionBloque {
				break
			}
			p.tokenSiguiente()
			continue
		}
		if token.Tipo == PALABRA_CLAVE && (token.Valor == "capturar" || token.Valor == "finalmente") {
			break
		}
		sent := p.parsearSentencia(paradaTry)
		if sent == nil {
			break
		}
		bloqueTry = append(bloqueTry, sent)
	}

	// Parsear bloques capturar y finally. Se "espía" hacia adelante (sin
	// consumir de forma permanente) para saber si lo que sigue es
	// 'capturar'/'finalmente'; si no lo es, dejamos el salto de línea
	// intacto para que el bloque contenedor detecte el des-sangrado.
	bloquesCapturar := []*Nodo{}
	var bloqueFinally []*Nodo

	for {
		idxSiguiente := p.primerTokenNoNuevaLinea(p.posicion)
		if idxSiguiente >= len(p.tokens) {
			break
		}
		token := p.tokens[idxSiguiente]
		if token.Tipo != PALABRA_CLAVE || (token.Valor != "capturar" && token.Valor != "finalmente") {
			break
		}
		// El capturar/finalmente espiado solo pertenece a ESTE intentar si
		// está escrito en la misma columna que el 'intentar'. Si está más
		// a la izquierda, pertenece a un bloque exterior (y hay que dejarlo
		// intacto para que ese bloque lo detecte); si está más adentro, es
		// un error de indentación en otro lado y tampoco es nuestro.
		if idxSiguiente > p.posicion {
			indentEncontrada := indentacionDeToken(&p.tokens[idxSiguiente-1])
			if indentEncontrada != indentIntentar {
				break
			}
		}
		p.posicion = idxSiguiente

		switch token.Valor {
		case "capturar":
			lineaCapturar := p.tokenLinea()
			p.esperado(PALABRA_CLAVE) // capturar

			var tipoError string
			var variableError string

			// Forma con paréntesis: capturar(Tipo) como var:
			if p.tokenActual() != nil && p.tokenActual().Tipo == PARENTESIS && p.tokenActual().Valor == "(" {
				p.esperado(PARENTESIS)
				if p.tokenActual() != nil && p.tokenActual().Tipo == IDENTIFICADOR {
					tipoError = p.esperado(IDENTIFICADOR)
				} else if p.tokenActual() != nil && p.tokenActual().Tipo == PALABRA_CLAVE {
					tipoError = p.esperado(PALABRA_CLAVE)
				}
				p.esperado(PARENTESIS)
			} else if p.tokenActual() != nil && p.tokenActual().Tipo == IDENTIFICADOR {
				tipoError = p.esperado(IDENTIFICADOR)
				if p.tokenActual() != nil && p.tokenActual().Tipo == PUNTO {
					p.esperado(PUNTO)
					tipoError = tipoError + "." + p.esperado(IDENTIFICADOR)
				}
			}

			if p.tokenActual() != nil && p.tokenActual().Tipo == PALABRA_CLAVE && p.tokenActual().Valor == "como" {
				p.esperado(PALABRA_CLAVE) // como
				variableError = p.esperado(IDENTIFICADOR)
			}

			p.esperado(PUNTOS)

			if p.tokenActual() == nil || p.tokenActual().Tipo != NUEVA_LINEA {
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: "Se esperaba un salto de línea después de ':' en capturar",
						Linea:   &[]int{p.tokenLinea()}[0],
					},
				})
			}

			indentCapturar := indentacionDeToken(p.tokenActual())
			if indentCapturar < 0 {
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: "Se esperaba un salto de línea con indentación en capturar",
						Linea:   &[]int{p.tokenLinea()}[0],
					},
				})
			}
			p.esperado(NUEVA_LINEA)

			paradaCapturar := make(map[string]bool)
			for k, v := range palabrasParada {
				paradaCapturar[k] = v
			}
			paradaCapturar["capturar"] = true
			paradaCapturar["finalmente"] = true

			bloqueCapturar := []*Nodo{}
			for p.tokenActual() != nil {
				if p.tokenActual().Tipo == NUEVA_LINEA {
					if indentacionDeToken(p.tokenActual()) < indentCapturar {
						break
					}
					p.tokenSiguiente()
					continue
				}
				if p.tokenActual().Tipo == PALABRA_CLAVE && (p.tokenActual().Valor == "capturar" || p.tokenActual().Valor == "finalmente") {
					break
				}
				sent := p.parsearSentencia(paradaCapturar)
				if sent == nil {
					break
				}
				bloqueCapturar = append(bloqueCapturar, sent)
			}

			nodoCapturar := NewNodo("CAPTURAR", tipoError, lineaCapturar)
			nodoCapturar.AgregarHijo(NewNodo("VARIABLE_ERROR", variableError, lineaCapturar))
			nodoCapturar.AgregarHijo(NewNodo("BLOQUE", nil, lineaCapturar).AgregarHijo(bloqueCapturar...))
			bloquesCapturar = append(bloquesCapturar, nodoCapturar)

		case "finalmente":
			p.esperado(PALABRA_CLAVE) // finalmente
			p.esperado(PUNTOS)

			if p.tokenActual() == nil || p.tokenActual().Tipo != NUEVA_LINEA {
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: "Se esperaba un salto de línea después de ':' en finalmente",
						Linea:   &[]int{p.tokenLinea()}[0],
					},
				})
			}

			indentFinally := indentacionDeToken(p.tokenActual())
			if indentFinally < 0 {
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: "Se esperaba un salto de línea con indentación en finalmente",
						Linea:   &[]int{p.tokenLinea()}[0],
					},
				})
			}
			p.esperado(NUEVA_LINEA)

			paradaFinally := make(map[string]bool)
			for k, v := range palabrasParada {
				paradaFinally[k] = v
			}

			bloqueFinally = []*Nodo{}
			for p.tokenActual() != nil {
				if p.tokenActual().Tipo == NUEVA_LINEA {
					if indentacionDeToken(p.tokenActual()) < indentFinally {
						break
					}
					p.tokenSiguiente()
					continue
				}
				sent := p.parsearSentencia(paradaFinally)
				if sent == nil {
					break
				}
				bloqueFinally = append(bloqueFinally, sent)
			}
		}

		if token.Valor == "finalmente" {
			break
		}
	}

	nodo := NewNodo("INTENTAR", nil, linea)
	nodo.AgregarHijo(NewNodo("BLOQUE_TRY", nil, linea).AgregarHijo(bloqueTry...))
	nodo.AgregarHijo(NewNodo("BLOQUES_CAPTURAR", nil, linea).AgregarHijo(bloquesCapturar...))
	if len(bloqueFinally) > 0 {
		nodo.AgregarHijo(NewNodo("BLOQUE_FINALLY", nil, linea).AgregarHijo(bloqueFinally...))
	} else {
		nodo.AgregarHijo(NewNodo("BLOQUE_FINALLY", nil, linea))
	}

	return nodo
}

// parsearImportar parsea la instrucción 'importar'
// En parser.go, modificar parsearImportar
func (p *Parser) parsearImportar() *Nodo {
	linea := p.tokenLinea()
	p.esperado(PALABRA_CLAVE) // importar

	var nombreModulo string

	// Soporte para importar con nombre en comillas: importar "os.dll" como os
	token := p.tokenActual()
	if token != nil && (token.Tipo == CADENA_TEXTO || token.Tipo == CADENA_INTERPOLADA) {
		nombreModulo = p.esperado(token.Tipo)
		nombreModulo = strings.Trim(nombreModulo, "\"'") // Quitar comillas
	} else {
		nombreModulo = p.esperado(IDENTIFICADOR)
		for p.tokenActual() != nil && p.tokenActual().Tipo == PUNTO {
			p.esperado(PUNTO)
			nombreModulo += "." + p.esperado(IDENTIFICADOR)
		}
	}

	alias := ""
	if p.tokenActual() != nil && p.tokenActual().Tipo == PALABRA_CLAVE && p.tokenActual().Valor == "como" {
		p.esperado(PALABRA_CLAVE) // como
		alias = p.esperado(IDENTIFICADOR)
	}

	nodo := NewNodo("IMPORTAR", nombreModulo, linea)
	nodo.AgregarHijo(NewNodo("ALIAS", alias, linea))
	return nodo
}

// parsearLanzar parsea la instrucción 'lanzar'
func (p *Parser) parsearLanzar() *Nodo {
	linea := p.tokenLinea()
	p.esperado(PALABRA_CLAVE) // lanzar

	var tipoError string
	var mensajeExpr *Nodo

	token := p.tokenActual()

	// Forma nueva: lanzar Tipo(...) o lanzar modulo.Tipo(...)
	if token != nil && token.Tipo == IDENTIFICADOR {
		tipoError = p.esperado(IDENTIFICADOR)

		if p.tokenActual() != nil && p.tokenActual().Tipo == PUNTO {
			p.esperado(PUNTO)
			tipoError = tipoError + "." + p.esperado(IDENTIFICADOR)
		}

		if p.tokenActual() != nil && p.tokenActual().Tipo == PARENTESIS && p.tokenActual().Valor == "(" {
			p.esperado(PARENTESIS)
			if p.tokenActual() != nil && !(p.tokenActual().Tipo == PARENTESIS && p.tokenActual().Valor == ")") {
				mensajeExpr = p.parsearExpresion()
			}
			p.esperado(PARENTESIS)
		}

		nodo := NewNodo("LANZAR", nil, linea)
		nodo.AgregarHijo(NewNodo("TIPO_ERROR", tipoError, linea))
		if mensajeExpr != nil {
			nodo.AgregarHijo(NewNodo("MENSAJE", nil, linea).AgregarHijo(mensajeExpr))
		} else {
			nodo.AgregarHijo(NewNodo("MENSAJE", nil, linea))
		}
		return nodo
	}

	// Forma antigua: lanzar("msg") o lanzar(Tipo, "msg")
	if token != nil && token.Tipo == PARENTESIS && token.Valor == "(" {
		p.esperado(PARENTESIS)

		token2 := p.tokenActual()
		if token2 != nil && token2.Tipo == IDENTIFICADOR {
			siguiente := p.tokenSiguientePeek()
			if siguiente != nil && siguiente.Tipo == COMA {
				tipoError = p.esperado(IDENTIFICADOR)
				p.esperado(COMA)
				mensajeExpr = p.parsearExpresion()
			} else {
				mensajeExpr = p.parsearExpresion()
			}
		} else {
			mensajeExpr = p.parsearExpresion()
		}

		p.esperado(PARENTESIS)

		nodo := NewNodo("LANZAR", nil, linea)
		nodo.AgregarHijo(NewNodo("TIPO_ERROR", tipoError, linea))
		if mensajeExpr != nil {
			nodo.AgregarHijo(NewNodo("MENSAJE", nil, linea).AgregarHijo(mensajeExpr))
		} else {
			nodo.AgregarHijo(NewNodo("MENSAJE", nil, linea))
		}
		return nodo
	}

	panic(&SintaxisError{
		ErrorConLinea: ErrorConLinea{
			Mensaje: "Sintaxis inválida para 'lanzar'",
			Linea:   &linea,
		},
	})
}

// parsearMientras parsea un bucle mientras
func (p *Parser) parsearMientras() *Nodo {
	linea := p.tokenLinea()
	p.esperado(PALABRA_CLAVE) // mientras

	condicion := p.parsearExpresion()

	p.esperado(PUNTOS)

	if p.tokenActual() == nil || p.tokenActual().Tipo != NUEVA_LINEA {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea después de ':'",
				Linea:   &linea,
			},
		})
	}

	indentacionBloque := indentacionDeToken(p.tokenActual())
	if indentacionBloque < 0 {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea con indentación después de ':'",
				Linea:   &linea,
			},
		})
	}
	p.esperado(NUEVA_LINEA)

	bloque := []*Nodo{}
	for p.tokenActual() != nil {
		token := p.tokenActual()
		if token.Tipo == NUEVA_LINEA {
			if indentacionDeToken(token) < indentacionBloque {
				break
			}
			p.tokenSiguiente()
			continue
		}
		sent := p.parsearSentencia(nil)
		if sent == nil {
			break
		}
		bloque = append(bloque, sent)
	}

	return NewNodo("MIENTRAS", nil, linea).
		AgregarHijo(condicion).
		AgregarHijo(NewNodo("BLOQUE", nil, linea).AgregarHijo(bloque...))
}

// parsearPara parsea un bucle para
func (p *Parser) parsearPara() *Nodo {
	linea := p.tokenLinea()

	p.esperado(PALABRA_CLAVE) // para

	variableStr := p.esperado(IDENTIFICADOR)
	variableNodo := NewNodo("IDENTIFICADOR", variableStr, linea)

	// Consumir 'en'
	tokenEn := p.tokenActual()
	if tokenEn != nil && tokenEn.Tipo == OPERADOR_LOGICO && tokenEn.Valor == "en" {
		p.tokenSiguiente()
	} else {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: fmt.Sprintf("Se esperaba 'en', pero se encontró %v", tokenEn),
				Linea:   &linea,
			},
		})
	}

	expresionIterable := p.parsearExpresion()

	p.esperado(PUNTOS)

	if p.tokenActual() == nil || p.tokenActual().Tipo != NUEVA_LINEA {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea después de ':'",
				Linea:   &linea,
			},
		})
	}

	indentacionBloque := indentacionDeToken(p.tokenActual())
	if indentacionBloque < 0 {
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: "Se esperaba un salto de línea con indentación después de ':'",
				Linea:   &linea,
			},
		})
	}
	p.esperado(NUEVA_LINEA)

	sentenciasBloque := []*Nodo{}
	for p.tokenActual() != nil {
		token := p.tokenActual()
		if token.Tipo == NUEVA_LINEA {
			if indentacionDeToken(token) < indentacionBloque {
				break
			}
			p.tokenSiguiente()
			continue
		}
		sentencia := p.parsearSentencia(nil)
		if sentencia == nil {
			break
		}
		sentenciasBloque = append(sentenciasBloque, sentencia)
	}

	bloqueNodo := NewNodo("BLOQUE", nil, linea).AgregarHijo(sentenciasBloque...)
	return NewNodo("PARA", nil, linea).
		AgregarHijo(variableNodo).
		AgregarHijo(expresionIterable).
		AgregarHijo(bloqueNodo)
}

// ========== EXPRESIONES ==========

// parsearExpresion parsea una expresión
func (p *Parser) parsearExpresion() *Nodo {
	return p.parsearLogicoOr()
}

// parsearLogicoOr parsea OR lógico
func (p *Parser) parsearLogicoOr() *Nodo {
	left := p.parsearLogicoAnd()
	for p.tokenActual() != nil && p.tokenActual().Tipo == OPERADOR_LOGICO {
		if p.tokenActual().Valor != "o" && p.tokenActual().Valor != "or" {
			break
		}
		opLinea := p.tokenLinea()
		op := p.esperado(OPERADOR_LOGICO)
		right := p.parsearLogicoAnd()
		left = NewNodo("LOGICO", op, opLinea).AgregarHijo(left).AgregarHijo(right)
	}
	return left
}

// parsearLogicoAnd parsea AND lógico
func (p *Parser) parsearLogicoAnd() *Nodo {
	left := p.parsearNot()
	for p.tokenActual() != nil && p.tokenActual().Tipo == OPERADOR_LOGICO {
		if p.tokenActual().Valor != "y" && p.tokenActual().Valor != "and" {
			break
		}
		opLinea := p.tokenLinea()
		op := p.esperado(OPERADOR_LOGICO)
		right := p.parsearNot()
		left = NewNodo("LOGICO", op, opLinea).AgregarHijo(left).AgregarHijo(right)
	}
	return left
}

// parsearNot parsea NOT lógico
func (p *Parser) parsearNot() *Nodo {
	if p.tokenActual() != nil && p.tokenActual().Tipo == OPERADOR_LOGICO {
		if p.tokenActual().Valor == "no" || p.tokenActual().Valor == "not" {
			opLinea := p.tokenLinea()
			op := p.esperado(OPERADOR_LOGICO)
			expr := p.parsearNot()
			return NewNodo("LOGICO", op, opLinea).AgregarHijo(expr)
		}
	}
	return p.parsearIn()
}

// parsearIn parsea el operador IN
func (p *Parser) parsearIn() *Nodo {
	left := p.parsearComparacion()
	if p.tokenActual() != nil && p.tokenActual().Tipo == OPERADOR_LOGICO {
		if p.tokenActual().Valor == "en" || p.tokenActual().Valor == "in" {
			opLinea := p.tokenLinea()
			op := p.esperado(OPERADOR_LOGICO)
			right := p.parsearComparacion()
			return NewNodo("IN", op, opLinea).AgregarHijo(left).AgregarHijo(right)
		}
	}
	return left
}

// parsearComparacion parsea comparaciones
func (p *Parser) parsearComparacion() *Nodo {
	izq := p.parsearSuma()
	for p.tokenActual() != nil && p.tokenActual().Tipo == COMPARADOR {
		opLinea := p.tokenLinea()
		op := p.esperado(COMPARADOR)
		der := p.parsearSuma()
		izq = NewNodo("BINARIA", op, opLinea).AgregarHijo(izq).AgregarHijo(der)
	}
	return izq
}

// parsearSuma parsea suma y resta
func (p *Parser) parsearSuma() *Nodo {
	izq := p.parsearTermino()
	for p.tokenActual() != nil && p.tokenActual().Tipo == OPERADOR {
		if p.tokenActual().Valor != "+" && p.tokenActual().Valor != "-" {
			break
		}
		opLinea := p.tokenLinea()
		op := p.esperado(OPERADOR)
		der := p.parsearTermino()
		izq = NewNodo("BINARIA", op, opLinea).AgregarHijo(izq).AgregarHijo(der)
	}
	return izq
}

// parsearTermino parsea multiplicación, división y módulo
func (p *Parser) parsearTermino() *Nodo {
	izq := p.parsearFactor()
	for p.tokenActual() != nil && p.tokenActual().Tipo == OPERADOR {
		if p.tokenActual().Valor != "*" && p.tokenActual().Valor != "/" && p.tokenActual().Valor != "%" {
			break
		}
		opLinea := p.tokenLinea()
		op := p.esperado(OPERADOR)
		der := p.parsearFactor()
		izq = NewNodo("BINARIA", op, opLinea).AgregarHijo(izq).AgregarHijo(der)
	}
	return izq
}

// parsearFactor parsea factores (operadores unarios y átomos)
func (p *Parser) parsearFactor() *Nodo {
	token := p.tokenActual()
	if token != nil && token.Tipo == OPERADOR && (token.Valor == "-" || token.Valor == "+") {
		linea := p.tokenLinea()
		op := p.esperado(OPERADOR)
		operando := p.parsearFactor()
		if op == "+" {
			return operando
		}
		return NewNodo("UNARIA", op, linea).AgregarHijo(operando)
	}

	nodo := p.parsearAtomo()
	return p.parsearPostfijos(nodo)
}

// parsearPostfijos parsea postfijos (atributos, índices, llamadas)
func (p *Parser) parsearPostfijos(nodo *Nodo) *Nodo {
	for {
		token := p.tokenActual()
		if token == nil {
			break
		}

		if token.Tipo == PUNTO {
			linea := nodo.Linea
			p.esperado(PUNTO)
			tokenAtributo := p.tokenActual()
			var atributo string
			if tokenAtributo != nil && tokenAtributo.Tipo == IDENTIFICADOR {
				atributo = p.esperado(IDENTIFICADOR)
			} else if tokenAtributo != nil && tokenAtributo.Tipo == PALABRA_CLAVE {
				atributo = p.esperado(PALABRA_CLAVE)
			} else {
				panic(&SintaxisError{
					ErrorConLinea: ErrorConLinea{
						Mensaje: fmt.Sprintf("Se esperaba identificador después de '.', pero encontró %v", tokenAtributo),
						Linea:   &linea,
					},
				})
			}

			if p.tokenActual() != nil && p.tokenActual().Tipo == PARENTESIS && p.tokenActual().Valor == "(" {
				p.esperado(PARENTESIS)
				argumentos := []*Nodo{nodo}
				if p.tokenActual() != nil && !(p.tokenActual().Tipo == PARENTESIS && p.tokenActual().Valor == ")") {
					argumentos = append(argumentos, p.parsearArgumento())
					for p.tokenActual() != nil && p.tokenActual().Tipo == COMA {
						p.esperado(COMA)
						argumentos = append(argumentos, p.parsearArgumento())
					}
				}
				p.esperado(PARENTESIS)
				nodo = NewNodo("LLAMADA_METODO", atributo, linea)
				for _, arg := range argumentos {
					nodo.AgregarHijo(arg)
				}
			} else {
				nodo = NewNodo("ACCESO_ATRIBUTO", atributo, linea).AgregarHijo(nodo)
			}

		} else if token.Tipo == CORCHETE && token.Valor == "[" {
			linea := nodo.Linea
			p.esperado(CORCHETE)
			indice := p.parsearExpresion()
			p.esperado(CORCHETE)
			nodo = NewNodo("INDEXACION", nil, linea).AgregarHijo(nodo).AgregarHijo(indice)

		} else {
			break
		}
	}

	return nodo
}

// parsearAtomo parsea un átomo (literal, variable, etc.)
func (p *Parser) parsearAtomo() *Nodo {
	token := p.tokenActual()
	if token == nil {
		return nil
	}

	linea := token.Linea

	switch token.Tipo {
	case ENTERO:
		valor := p.esperado(ENTERO)
		return NewNodo("ENTERO", valor, linea)

	case DECIMAL:
		valor := p.esperado(DECIMAL)
		return NewNodo("DECIMAL", valor, linea)

	case CADENA_INTERPOLADA:
		valor := p.esperado(CADENA_INTERPOLADA)
		return NewNodo("CADENA_INTERPOLADA", valor[2:len(valor)-1], linea)

	case CADENA_TEXTO:
		valor := p.esperado(CADENA_TEXTO)
		return NewNodo("CADENA_TEXTO", valor[1:len(valor)-1], linea)

	case NINGUNO:
		p.esperado(NINGUNO)
		return NewNodo("NINGUNO", nil, linea)

	case BOOLEANO:
		valor := p.esperado(BOOLEANO)
		boolVal := valor == "true" || valor == "verdadero"
		return NewNodo("BOOLEANO", boolVal, linea)
	case PALABRA_CLAVE:
		valor := token.Valor
		// Llamada a función
		if p.tokenSiguientePeek() != nil && p.tokenSiguientePeek().Tipo == PARENTESIS && p.tokenSiguientePeek().Valor == "(" {
			p.esperado(PALABRA_CLAVE)
			p.esperado(PARENTESIS)
			argumentos := p.parsearArgumentosLlamada()
			return NewNodo("LLAMADA", valor, linea).AgregarHijo(argumentos...)
		}
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: fmt.Sprintf("Se esperaba una expresión, pero se encontró la palabra reservada '%s'", valor),
				Linea:   &linea,
			},
		})

	case TIPO_DATO:
		valor := tipoDatoCanonico(token.Valor)
		// Llamada a función de conversión: cadena(x), decimal(x), entero(x),
		// o su alias en inglés float(x)/int(x)/etc — se resuelve al nombre
		// canónico para reusar las mismas funciones nativas de conversión.
		if p.tokenSiguientePeek() != nil && p.tokenSiguientePeek().Tipo == PARENTESIS && p.tokenSiguientePeek().Valor == "(" {
			p.esperado(TIPO_DATO)
			p.esperado(PARENTESIS)
			argumentos := p.parsearArgumentosLlamada()
			return NewNodo("LLAMADA", valor, linea).AgregarHijo(argumentos...)
		}
		panic(&SintaxisError{
			ErrorConLinea: ErrorConLinea{
				Mensaje: fmt.Sprintf("Se esperaba una expresión, pero se encontró la palabra reservada '%s'", token.Valor),
				Linea:   &linea,
			},
		})

	case IDENTIFICADOR:
		valor := token.Valor
		// Llamada a función
		if p.tokenSiguientePeek() != nil && p.tokenSiguientePeek().Tipo == PARENTESIS && p.tokenSiguientePeek().Valor == "(" {
			p.esperado(IDENTIFICADOR)
			p.esperado(PARENTESIS)
			argumentos := p.parsearArgumentosLlamada()
			return NewNodo("LLAMADA", valor, linea).AgregarHijo(argumentos...)
		}
		p.esperado(IDENTIFICADOR)
		return NewNodo("IDENTIFICADOR", valor, linea)

	case CORCHETE:
		if token.Valor == "[" {
			p.esperado(CORCHETE)
			elementos := []*Nodo{}
			p.saltarNuevasLineas()
			if p.tokenActual() != nil && !(p.tokenActual().Tipo == CORCHETE && p.tokenActual().Valor == "]") {
				for {
					elementos = append(elementos, p.parsearExpresion())
					p.saltarNuevasLineas()

					if p.tokenActual() == nil || p.tokenActual().Tipo != COMA {
						break
					}
					p.esperado(COMA)
					p.saltarNuevasLineas()

					// Soporta coma final antes de ']'.
					if p.tokenActual() != nil && p.tokenActual().Tipo == CORCHETE && p.tokenActual().Valor == "]" {
						break
					}
				}
			}
			p.esperado(CORCHETE)
			return NewNodo("LISTA", nil, linea).AgregarHijo(elementos...)
		}

	case LLAVE:
		if token.Valor == "{" {
			p.esperado(LLAVE)
			pares := []*Nodo{}
			p.saltarNuevasLineas()
			if p.tokenActual() != nil && !(p.tokenActual().Tipo == LLAVE && p.tokenActual().Valor == "}") {
				for {
					clave := p.parsearExpresion()
					p.saltarNuevasLineas()
					p.esperado(PUNTOS)
					p.saltarNuevasLineas()

					valor := p.parsearExpresion()
					par := NewNodo("PAR", nil, linea).AgregarHijo(clave).AgregarHijo(valor)
					pares = append(pares, par)
					p.saltarNuevasLineas()

					if p.tokenActual() == nil || p.tokenActual().Tipo != COMA {
						break
					}
					p.esperado(COMA)
					p.saltarNuevasLineas()

					// Soporta coma final antes de '}'.
					if p.tokenActual() != nil && p.tokenActual().Tipo == LLAVE && p.tokenActual().Valor == "}" {
						break
					}
				}
			}
			p.esperado(LLAVE)
			return NewNodo("DICCIONARIO", nil, linea).AgregarHijo(pares...)
		}

	case PARENTESIS:
		if token.Valor == "(" {
			p.esperado(PARENTESIS)
			expr := p.parsearExpresion()
			p.esperado(PARENTESIS)
			return expr
		}
	}

	panic(&SintaxisError{
		ErrorConLinea: ErrorConLinea{
			Mensaje: fmt.Sprintf("Factor inesperado: %v", token),
			Linea:   &linea,
		},
	})
}
