package wini

import "fmt"

// ---------------------------------------------------------------------
// Ámbitos (tabla de símbolos con scope léxico)
// ---------------------------------------------------------------------

type Ambito struct {
	padre     *Ambito
	variables map[string]Tipo
}

func nuevoAmbito(padre *Ambito) *Ambito {
	return &Ambito{padre: padre, variables: make(map[string]Tipo)}
}

func (a *Ambito) declarar(nombre string, t Tipo) error {
	if _, existe := a.variables[nombre]; existe {
		return fmt.Errorf("la variable '%s' ya fue declarada en este ámbito", nombre)
	}
	a.variables[nombre] = t
	return nil
}

func (a *Ambito) buscar(nombre string) (Tipo, bool) {
	for amb := a; amb != nil; amb = amb.padre {
		if t, ok := amb.variables[nombre]; ok {
			return t, true
		}
	}
	return nil, false
}

// ---------------------------------------------------------------------
// Verificador
// ---------------------------------------------------------------------

type Verificador struct {
	funciones       map[string]*TipoFuncion
	estructuras     map[string]*TipoEstructura
	ambito          *Ambito
	retornoEsperado Tipo
}

func VerificarPrograma(raiz *Nodo) {
	if raiz.Tipo != "PROGRAMA" {
		panic(tipoErr(raiz, "se esperaba un nodo PROGRAMA en la raíz del AST, se encontró '%s'", raiz.Tipo))
	}
	v := &Verificador{
		funciones: make(map[string]*TipoFuncion),
		estructuras: make(map[string]*TipoEstructura),
		ambito:    nuevoAmbito(nil),
	}
	v.registrarFunciones(raiz.Hijos)
	v.registrarEstructuras(raiz.Hijos)
	v.verificarBloque(raiz.Hijos)
}

func (v *Verificador) registrarEstructuras(sentencias []*Nodo) {
	for _, s := range sentencias {
		if s.Tipo != "ESTRUCTURA" { continue }
		nombre, _ := s.Valor.(string)
		if _, ok := v.estructuras[nombre]; ok { panic(tipoErr(s, "la estructura '%s' ya fue declarada", nombre)) }
		v.estructuras[nombre] = &TipoEstructura{Nombre: nombre}
	}
	for _, s := range sentencias {
		if s.Tipo != "ESTRUCTURA" { continue }
		nombre, _ := s.Valor.(string)
		t := v.estructuras[nombre]
		for _, c := range s.Hijos[0].Hijos {
			cn, _ := c.Valor.(string)
			ct := v.resolverAnotacion(c.Hijos[0])
			t.Campos = append(t.Campos, CampoEstructura{cn, ct})
		}
	}
}

func (v *Verificador) registrarFunciones(sentencias []*Nodo) {
	for _, s := range sentencias {
		if s.Tipo != "FUNCION" && s.Tipo != "EXTERNA" {
			continue
		}
		nombre, _ := s.Valor.(string)
		if _, existe := v.funciones[nombre]; existe {
			panic(tipoErr(s, "la función '%s' ya fue declarada", nombre))
		}
		v.funciones[nombre] = construirFirma(s)
	}
}

func (v *Verificador) enNuevoAmbito(f func()) {
	anterior := v.ambito
	v.ambito = nuevoAmbito(anterior)
	f()
	v.ambito = anterior
}

// ---------------------------------------------------------------------
// Resolución de anotaciones de tipo
// ---------------------------------------------------------------------

// resolverAnotacion resuelve un nodo de anotación de tipo en cualquier
// posición donde se necesita un tipo de VALOR real: parámetros de
// función, declaraciones de variable ('entero x = ...'), y el elemento
// de 'lista<...>' o la clave/valor de 'diccionario<...,...>' (vía la
// recursión de acá abajo). 'vacio' (que representa "sin valor", como
// 'void' en C) NUNCA es un tipo de valor válido en ninguna de estas
// posiciones — no existe forma de tener una variable, un parámetro, un
// elemento de lista o una clave/valor de diccionario que "no tenga
// valor". La ÚNICA posición donde 'vacio' es válido es como tipo de
// RETORNO de una función, que se resuelve aparte, con
// resolverAnotacionRetorno (ver más abajo) — esa es la única llamada en
// todo el código que no pasa por acá.
func resolverAnotacion(nodo *Nodo) Tipo {
	nombre, _ := nodo.Valor.(string)
	switch nombre {
	case "lista":
		return NewTipoLista(resolverAnotacion(nodo.Hijos[0]))
	case "diccionario":
		return NewTipoDiccionario(resolverAnotacion(nodo.Hijos[0]), resolverAnotacion(nodo.Hijos[1]))
	case "vacio":
		panic(tipoErr(nodo, "'vacio' solo puede usarse como tipo de retorno de una función ('funcion f(): vacio:'); no es un tipo válido para variables, parámetros, ni elementos de lista o diccionario"))
	default:
		t, ok := TipoPrimitivoDesdeNombre(nombre)
		if !ok {
			panic(tipoErr(nodo, "tipo desconocido '%s'", nombre))
		}
		return t
	}
}

// resolverAnotacionRetorno resuelve específicamente la anotación de tipo
// de retorno de una función, donde 'vacio' SÍ es válido (significa "esta
// función no retorna ningún valor"). Para cualquier otro nombre de tipo,
// se comporta exactamente igual que resolverAnotacion.
func resolverAnotacionRetorno(nodo *Nodo) Tipo {
	nombre, _ := nodo.Valor.(string)
	if nombre == "vacio" {
		return TipoVacio
	}
	return resolverAnotacion(nodo)
}

func construirFirma(nodoFuncion *Nodo) *TipoFuncion {
	paramsNodo := nodoFuncion.Hijos[0]
	tipoRetNodo := nodoFuncion.Hijos[1]
	parametros := make([]Tipo, 0, len(paramsNodo.Hijos))
	esVariadico := false
	requeridos := -1 // índice del primer parámetro con valor por defecto
	for i, p := range paramsNodo.Hijos {
		anotacion := p.Hijos[0].Hijos[0]
		t := resolverAnotacion(anotacion)
		if p.Tipo == "PARAM_VARIADICO" {
			t = NewTipoLista(t)
			esVariadico = true
		}
		tieneDefault := p.Tipo == "PARAM" && len(p.Hijos) > 1
		if tieneDefault {
			if requeridos == -1 {
				requeridos = i
			}
		} else if requeridos != -1 {
			nombreParam, _ := p.Valor.(string)
			panic(tipoErr(p, "el parámetro '%s' no tiene valor por defecto, pero aparece después de un parámetro que sí lo tiene; los parámetros con valor por defecto deben ir al final", nombreParam))
		}
		parametros = append(parametros, t)
	}
	if requeridos == -1 {
		requeridos = len(parametros)
	}
	retorno := resolverAnotacionRetorno(tipoRetNodo.Hijos[0])
	firma := NewTipoFuncion(parametros, retorno, requeridos)
	firma.EsVariadico = esVariadico
	// Solo un nodo EXTERNA trae un cuarto hijo VARIADICO_C (marca el '...'
	// de variádico C real); una FUNCION nunca lo tiene.
	if len(nodoFuncion.Hijos) > 3 {
		if vc, ok := nodoFuncion.Hijos[3].Valor.(bool); ok {
			firma.VariadicoC = vc
		}
	}
	return firma
}

// ---------------------------------------------------------------------
// Sentencias
// ---------------------------------------------------------------------

func (v *Verificador) verificarBloque(sentencias []*Nodo) {
	for _, s := range sentencias {
		v.verificarSentencia(s)
	}
}

func (v *Verificador) verificarSentencia(nodo *Nodo) {
	switch nodo.Tipo {
	case "ESTRUCTURA":
		return

	case "ASIGNACION_TIPADA":
		nombre, _ := nodo.Valor.(string)
		tipoDecl := v.resolverAnotacion(nodo.Hijos[0].Hijos[0])
		tipoExpr := v.tipoDeExpresionCtx(nodo.Hijos[1], tipoDecl)
		if !tipoExpr.Igual(tipoDecl) {
			panic(tipoErr(nodo, "no se puede inicializar '%s' (declarada como %s) con un valor de tipo %s", nombre, tipoDecl, tipoExpr))
		}

		if err := v.ambito.declarar(nombre, tipoDecl); err != nil {
			panic(tipoErr(nodo, "%s", err.Error()))
		}

	case "ASIGNACION":
		nombre, _ := nodo.Valor.(string)
		tipoActual, ok := v.ambito.buscar(nombre)
		if !ok {
			panic(tipoErr(nodo, "variable '%s' no declarada; usa 'tipo %s = valor' para declararla antes de asignarle", nombre, nombre))
		}
		tipoExpr := v.tipoDeExpresionCtx(nodo.Hijos[0], tipoActual)
		if !tipoExpr.Igual(tipoActual) {
			panic(tipoErr(nodo, "no se puede asignar un valor de tipo %s a '%s' (declarada como %s)", tipoExpr, nombre, tipoActual))
		}

	case "ASIGNACION_INDEX":
		nombre, _ := nodo.Valor.(string)
		base, ok := v.ambito.buscar(nombre)
		if !ok {
			panic(tipoErr(nodo, "variable '%s' no declarada", nombre))
		}
		valNodo := nodo.Hijos[1]
		// FIX: el índice/clave se tipa DESPUÉS de conocer el tipo del
		// contenedor, para que 'd[nulo] = ...' pueda inferir que 'nulo'
		// debe ser del tipo de la clave (p. ej. cadena).
		switch b := base.(type) {
		case *TipoLista:
			idxTipo := v.tipoDeExpresionCtx(nodo.Hijos[0], TipoEntero)
			v.exigir(nodo.Hijos[0], idxTipo, TipoEntero, "el índice de una lista")
			valTipo := v.tipoDeExpresionCtx(valNodo, b.Elemento)
			v.exigir(valNodo, valTipo, b.Elemento, fmt.Sprintf("el valor asignado a '%s[...]'", nombre))
		case *TipoDiccionario:
			idxTipo := v.tipoDeExpresionCtx(nodo.Hijos[0], b.Clave)
			v.exigir(nodo.Hijos[0], idxTipo, b.Clave, "la clave de un diccionario")
			valTipo := v.tipoDeExpresionCtx(valNodo, b.Valor)
			v.exigir(valNodo, valTipo, b.Valor, fmt.Sprintf("el valor asignado a '%s[...]'", nombre))
		default:
			panic(tipoErr(nodo, "no se puede indexar '%s', que es de tipo %s", nombre, base))
		}

	case "SI":
		cond := v.tipoDeExpresion(nodo.Hijos[0])
		v.exigir(nodo.Hijos[0], cond, TipoBooleano, "la condición de 'si'")
		v.enNuevoAmbito(func() { v.verificarBloque(nodo.Hijos[1].Hijos) })
		v.enNuevoAmbito(func() { v.verificarBloque(nodo.Hijos[2].Hijos) })

	case "MIENTRAS":
		cond := v.tipoDeExpresion(nodo.Hijos[0])
		v.exigir(nodo.Hijos[0], cond, TipoBooleano, "la condición de 'mientras'")
		v.enNuevoAmbito(func() { v.verificarBloque(nodo.Hijos[1].Hijos) })

	case "PARA":
		variableNodo, iterableNodo, bloqueNodo := nodo.Hijos[0], nodo.Hijos[1], nodo.Hijos[2]
		contenedor := v.tipoDeExpresion(iterableNodo)
		var tipoVar Tipo
		switch c := contenedor.(type) {
		case *TipoLista:
			tipoVar = c.Elemento
		case *TipoDiccionario:
			tipoVar = c.Clave
		default:
			if contenedor != nil && contenedor.Igual(TipoCadena) {
				// Iterar una cadena recorre sus caracteres, cada uno
				// como una cadena de 1 byte (no hay tipo 'caracter').
				tipoVar = TipoCadena
			} else {
				panic(tipoErr(iterableNodo, "'para ... en' requiere una lista, un diccionario o una cadena, se encontró %s", contenedor))
			}
		}
		v.enNuevoAmbito(func() {
			nombreVar, _ := variableNodo.Valor.(string)
			_ = v.ambito.declarar(nombreVar, tipoVar)
			v.verificarBloque(bloqueNodo.Hijos)
		})

	case "FUNCION":
		v.verificarFuncion(nodo)

	case "EXTERNA":
		// Ya fue registrada en registrarFunciones (su firma). No tiene
		// cuerpo que verificar; solo se permite en el nivel superior del
		// archivo, restricción que impone el propio codegen.

	case "RETORNO":
		if v.retornoEsperado == nil {
			panic(tipoErr(nodo, "'retornar' solo puede usarse dentro de una función"))
		}
		if v.retornoEsperado.Igual(TipoVacio) {
			if len(nodo.Hijos) != 0 {
				panic(tipoErr(nodo, "una función vacía no puede retornar un valor"))
			}
			return
		}
		if len(nodo.Hijos) == 0 {
			panic(tipoErr(nodo, "la función debe retornar un valor de tipo %s", v.retornoEsperado))
		}
		tipoExpr := v.tipoDeExpresionCtx(nodo.Hijos[0], v.retornoEsperado)
		v.exigir(nodo.Hijos[0], tipoExpr, v.retornoEsperado, "el valor de retorno")

	case "ROMPER", "CONTINUAR":
		// sin verificación de tipos

	case "LLAMADA":
		v.tipoDeExpresion(nodo)

	case "IMPORTAR":
		panic(tipoErr(nodo, "'importar' solo se permite en el nivel superior de un archivo, no dentro de una función u otro bloque"))

	case "INTENTAR":
		bloqueTry := nodo.Hijos[0]
		bloquesCapturar := nodo.Hijos[1]
		bloqueFinally := nodo.Hijos[2]

		v.enNuevoAmbito(func() { v.verificarBloque(bloqueTry.Hijos) })

		tiposCapturados := make(map[string]int)
		catchAllEncontrado := false
		for _, capturar := range bloquesCapturar.Hijos {
			tipoError, _ := capturar.Valor.(string)
			if catchAllEncontrado {
				panic(tipoErr(capturar, "un 'capturar' posterior a un catch-all es inalcanzable"))
			}
			if tipoError == "" {
				catchAllEncontrado = true
			} else if lineaAnterior, existe := tiposCapturados[tipoError]; existe {
				panic(tipoErr(capturar, "el tipo '%s' ya fue capturado en la línea %d", tipoError, lineaAnterior))
			} else {
				tiposCapturados[tipoError] = capturar.Linea
			}
			variableNodo := capturar.Hijos[0]
			cuerpoNodo := capturar.Hijos[1]
			v.enNuevoAmbito(func() {
				nombreVar, _ := variableNodo.Valor.(string)
				if nombreVar != "" {
					// La variable de 'capturar ... como var' siempre es de
					// tipo cadena: el modelo de excepciones de Wini no
					// tiene (todavía) un tipo de dato "excepción" propio,
					// solo un nombre de tipo (para el 'capturar Tipo:') y
					// un mensaje de texto.
					_ = v.ambito.declarar(nombreVar, TipoCadena)
				}
				v.verificarBloque(cuerpoNodo.Hijos)
			})
		}

		if len(bloqueFinally.Hijos) > 0 {
			v.enNuevoAmbito(func() { v.verificarBloque(bloqueFinally.Hijos) })
		}

	case "LANZAR":
		mensajeNodo := nodo.Hijos[1]
		if len(mensajeNodo.Hijos) > 0 {
			tipoMsg := v.tipoDeExpresionCtx(mensajeNodo.Hijos[0], TipoCadena)
			v.exigir(mensajeNodo.Hijos[0], tipoMsg, TipoCadena, "el mensaje de 'lanzar'")
		}

	case "RELANZAR":
		// No consume ni altera la excepción: el codegen la propaga intacta.

	default:
		panic(tipoErr(nodo, "la sentencia '%s' todavía no está soportada por el verificador de tipos", nodo.Tipo))
	}
}

func (v *Verificador) verificarFuncion(nodo *Nodo) {
	nombre, _ := nodo.Valor.(string)
	firma := v.funciones[nombre]
	paramsNodo, cuerpoNodo := nodo.Hijos[0], nodo.Hijos[2]

	ambitoAnterior, retornoAnterior := v.ambito, v.retornoEsperado
	v.ambito = nuevoAmbito(ambitoAnterior)
	v.retornoEsperado = firma.Retorno

	for i, p := range paramsNodo.Hijos {
		nombreParam, _ := p.Valor.(string)
		tipoParam := firma.Parametros[i]
		// El valor por defecto de un parámetro se verifica ANTES de
		// declarar ese mismo parámetro en el ámbito: un default solo
		// puede referirse a parámetros ANTERIORES (ya declarados en
		// iteraciones previas de este mismo for), nunca a sí mismo ni a
		// los que vienen después. Antes, el orden era al revés (se
		// declaraba el parámetro y RECIÉN DESPUÉS se verificaba su
		// propio default), lo que permitía escribir
		// 'funcion f(a: entero, b: entero = b): ...' sin error, porque
		// para cuando se verificaba 'b' como expresión, 'b' ya estaba
		// en el ámbito (apuntándose a sí misma).
		if len(p.Hijos) > 1 {
			tDef := v.tipoDeExpresionCtx(p.Hijos[1], tipoParam)
			v.exigir(p.Hijos[1], tDef, tipoParam, fmt.Sprintf("el valor por defecto de '%s'", nombreParam))
		}
		if err := v.ambito.declarar(nombreParam, tipoParam); err != nil {
			panic(tipoErr(p, "%s", err.Error()))
		}
	}

	v.verificarBloque(cuerpoNodo.Hijos)

	v.ambito, v.retornoEsperado = ambitoAnterior, retornoAnterior
}

func (v *Verificador) resolverAnotacion(n *Nodo) Tipo {
	if nombre, ok := n.Valor.(string); ok {
		if t, found := v.estructuras[nombre]; found {
			return t
		}
	}
	return resolverAnotacion(n)
}

// ---------------------------------------------------------------------
// Expresiones
// ---------------------------------------------------------------------

func (v *Verificador) tipoDeExpresion(nodo *Nodo) Tipo {
	return v.tipoDeExpresionCtx(nodo, nil)
}

func (v *Verificador) tipoDeExpresionCtx(nodo *Nodo, esperado Tipo) Tipo {
	switch nodo.Tipo {

	case "ENTERO":
		nodo.TipoInferido = TipoEntero
		return TipoEntero

	case "DECIMAL":
		nodo.TipoInferido = TipoDecimal
		return TipoDecimal

	case "BOOLEANO":
		nodo.TipoInferido = TipoBooleano
		return TipoBooleano

	case "NINGUNO":
		// 'nulo' no tiene un tipo propio: es válido allí donde se espera
		// un tipo que en codegen se representa como puntero (cadena,
		// lista<...> o diccionario<...,...>), y toma ese tipo del
		// contexto (declaración, retorno, argumento, comparación, etc).
		switch esperado.(type) {
		case *TipoLista, *TipoDiccionario:
			nodo.TipoInferido = esperado
			return esperado
		case nil:
			panic(tipoErr(nodo, "no se puede inferir el tipo de 'nulo' en este contexto; usalo en una declaración, retorno, argumento o comparación con un tipo conocido"))
		default:
			// 'cadena' también se representa como puntero en codegen
			// (i8*), así que 'nulo' vale como puntero nulo para ella.
			if esperado.Igual(TipoCadena) {
				nodo.TipoInferido = esperado
				return esperado
			}
			panic(tipoErr(nodo, "'nulo' no es válido para el tipo %s", esperado))
		}

	case "CADENA_TEXTO", "CADENA_INTERPOLADA":
		nodo.TipoInferido = TipoCadena
		return TipoCadena

	case "IDENTIFICADOR":
		nombre, _ := nodo.Valor.(string)
		t, ok := v.ambito.buscar(nombre)
		if !ok {
			panic(tipoErr(nodo, "variable '%s' no declarada", nombre))
		}
		nodo.TipoInferido = t
		return t

	case "UNARIA":
		op, _ := nodo.Valor.(string)
		operando := v.tipoDeExpresion(nodo.Hijos[0])
		if op == "-" {
			if !operando.Igual(TipoEntero) && !operando.Igual(TipoDecimal) {
				panic(tipoErr(nodo, "el operador unario '-' requiere entero o decimal, se encontró %s", operando))
			}
			nodo.TipoInferido = operando
			return operando
		}
		panic(tipoErr(nodo, "operador unario '%s' no soportado", op))

	case "LOGICO":
		op, _ := nodo.Valor.(string)
		if len(nodo.Hijos) == 1 {
			operando := v.tipoDeExpresion(nodo.Hijos[0])
			v.exigir(nodo.Hijos[0], operando, TipoBooleano, "el operador 'no'")
			nodo.TipoInferido = TipoBooleano
			return TipoBooleano
		}
		izq := v.tipoDeExpresion(nodo.Hijos[0])
		der := v.tipoDeExpresion(nodo.Hijos[1])
		v.exigir(nodo.Hijos[0], izq, TipoBooleano, fmt.Sprintf("el operando izquierdo de '%s'", op))
		v.exigir(nodo.Hijos[1], der, TipoBooleano, fmt.Sprintf("el operando derecho de '%s'", op))
		nodo.TipoInferido = TipoBooleano
		return TipoBooleano

	case "IN":
		// FIX: tipamos primero el contenedor para saber el tipo esperado
		// del operando izquierdo (clave de diccionario / elemento de
		// lista). Así 'nulo en d' puede inferir que 'nulo' debe ser del
		// tipo de la clave (p. ej. cadena).
		contenedor := v.tipoDeExpresion(nodo.Hijos[1])
		var esperado Tipo
		switch c := contenedor.(type) {
		case *TipoLista:
			esperado = c.Elemento
		case *TipoDiccionario:
			esperado = c.Clave
		default:
			panic(tipoErr(nodo, "'en' requiere una lista o un diccionario a la derecha, se encontró %s", contenedor))
		}
		elemento := v.tipoDeExpresionCtx(nodo.Hijos[0], esperado)
		if !elemento.Igual(esperado) {
			panic(tipoErr(nodo.Hijos[0], "el operando izquierdo de 'en' debe ser %s, se encontró %s", esperado, elemento))
		}
		nodo.TipoInferido = TipoBooleano
		return TipoBooleano

	case "BINARIA":
		op, _ := nodo.Valor.(string)
		var izq, der Tipo
		switch {
		case nodo.Hijos[0].Tipo == "NINGUNO" && nodo.Hijos[1].Tipo == "NINGUNO":
			panic(tipoErr(nodo, "no se puede comparar 'nulo' con 'nulo': no hay tipo que inferir"))
		case nodo.Hijos[0].Tipo == "NINGUNO":
			der = v.tipoDeExpresion(nodo.Hijos[1])
			izq = v.tipoDeExpresionCtx(nodo.Hijos[0], der)
		case nodo.Hijos[1].Tipo == "NINGUNO":
			izq = v.tipoDeExpresion(nodo.Hijos[0])
			der = v.tipoDeExpresionCtx(nodo.Hijos[1], izq)
		default:
			izq = v.tipoDeExpresion(nodo.Hijos[0])
			der = v.tipoDeExpresion(nodo.Hijos[1])
		}
		if !izq.Igual(der) {
			panic(tipoErr(nodo, "operandos de tipos distintos en '%s': %s y %s (no hay conversión implícita, usa un casteo explícito)", op, izq, der))
		}
		var res Tipo
		switch op {
		case "+":
			if izq.Igual(TipoCadena) || izq.Igual(TipoEntero) || izq.Igual(TipoDecimal) {
				res = izq
			} else if _, esLista := izq.(*TipoLista); esLista {
				// Concatenación de listas del mismo tipo: el codegen ya la
				// soporta vía wini_list_concat. Solo hacen falta los 4
				// elementos primitivos soportados por el runtime.
				if _, ok := izq.(*TipoLista); ok {
					elem := izq.(*TipoLista).Elemento
					switch elem.(type) {
					case *TipoPrimitivo:
						// ok: entero/decimal/booleano/cadena
						res = izq
					default:
						panic(tipoErr(nodo, "'+' entre listas solo soporta elementos primitivos (entero, decimal, booleano, cadena), se encontró %s", izq))
					}
				}
			} else {
				panic(tipoErr(nodo, "'+' requiere entero, decimal, cadena o lista, se encontró %s", izq))
			}
		case "-", "*", "/", "%":
			if izq.Igual(TipoEntero) || izq.Igual(TipoDecimal) {
				res = izq
			} else {
				panic(tipoErr(nodo, "'%s' requiere entero o decimal, se encontró %s", op, izq))
			}
		case "==", "!=", "<>":
			res = TipoBooleano
		case "<", ">", "<=", ">=":
			if !esOrdenable(izq) {
				panic(tipoErr(nodo, "'%s' requiere entero, decimal o cadena, se encontró %s", op, izq))
			}
			res = TipoBooleano
		default:
			panic(tipoErr(nodo, "operador '%s' no soportado", op))
		}
		nodo.TipoInferido = res
		return res

	case "INDEXACION":
		base := v.tipoDeExpresion(nodo.Hijos[0])
		var res Tipo
		// FIX: el índice/clave se tipa DESPUÉS de conocer el tipo del
		// contenedor, para que 'd[nulo]' pueda inferir que 'nulo' debe
		// ser del tipo de la clave (p. ej. cadena).
		switch b := base.(type) {
		case *TipoLista:
			idx := v.tipoDeExpresionCtx(nodo.Hijos[1], TipoEntero)
			v.exigir(nodo.Hijos[1], idx, TipoEntero, "el índice de una lista")
			res = b.Elemento
		case *TipoDiccionario:
			idx := v.tipoDeExpresionCtx(nodo.Hijos[1], b.Clave)
			v.exigir(nodo.Hijos[1], idx, b.Clave, "la clave de un diccionario")
			res = b.Valor
		default:
			if base != nil && base.Igual(TipoCadena) {
				idx := v.tipoDeExpresionCtx(nodo.Hijos[1], TipoEntero)
				v.exigir(nodo.Hijos[1], idx, TipoEntero, "el índice de una cadena")
				res = TipoCadena
			} else {
				panic(tipoErr(nodo, "no se puede indexar un valor de tipo %s", base))
			}
		}
		nodo.TipoInferido = res
		return res

	case "LISTA":
		var elemEsperado Tipo
		if le, ok := esperado.(*TipoLista); ok {
			elemEsperado = le.Elemento
		}
		if len(nodo.Hijos) == 0 {
			if elemEsperado != nil {
				res := NewTipoLista(elemEsperado)
				nodo.TipoInferido = res
				return res
			}
			panic(tipoErr(nodo, "no se puede inferir el tipo de una lista vacía; anotala explícitamente, ej. 'lista<entero> x = []'"))
		}
		primero := v.tipoDeExpresionCtx(nodo.Hijos[0], elemEsperado)
		for _, h := range nodo.Hijos[1:] {
			t := v.tipoDeExpresionCtx(h, primero)
			if !t.Igual(primero) {
				panic(tipoErr(h, "todos los elementos de una lista deben tener el mismo tipo: se esperaba %s, se encontró %s", primero, t))
			}
		}
		res := NewTipoLista(primero)
		nodo.TipoInferido = res
		return res

	case "DICCIONARIO":
		var claveEsp, valorEsp Tipo
		if de, ok := esperado.(*TipoDiccionario); ok {
			claveEsp, valorEsp = de.Clave, de.Valor
		}
		if len(nodo.Hijos) == 0 {
			if claveEsp != nil && valorEsp != nil {
				res := NewTipoDiccionario(claveEsp, valorEsp)
				nodo.TipoInferido = res
				return res
			}
			panic(tipoErr(nodo, "no se puede inferir el tipo de un diccionario vacío; anotalo explícitamente, ej. 'diccionario<cadena, entero> x = {}'"))
		}
		primerPar := nodo.Hijos[0]
		tClave := v.tipoDeExpresionCtx(primerPar.Hijos[0], claveEsp)
		if !ClaveDiccionarioValida(tClave) {
			panic(tipoErr(primerPar.Hijos[0], "%s no puede usarse como tipo de clave de un diccionario", tClave))
		}
		tValor := v.tipoDeExpresionCtx(primerPar.Hijos[1], valorEsp)
		for _, par := range nodo.Hijos[1:] {
			tc := v.tipoDeExpresionCtx(par.Hijos[0], tClave)
			if !tc.Igual(tClave) {
				panic(tipoErr(par.Hijos[0], "todas las claves de un diccionario deben tener el mismo tipo: se esperaba %s, se encontró %s", tClave, tc))
			}
			tv := v.tipoDeExpresionCtx(par.Hijos[1], tValor)
			if !tv.Igual(tValor) {
				panic(tipoErr(par.Hijos[1], "todos los valores de un diccionario deben tener el mismo tipo: se esperaba %s, se encontró %s", tValor, tv))
			}
		}
		res := NewTipoDiccionario(tClave, tValor)
		nodo.TipoInferido = res
		return res

	case "LLAMADA":
		res := v.tipoDeLlamada(nodo)
		nodo.TipoInferido = res
		return res

	case "ARG_NOMBRADO":
		res := v.tipoDeExpresionCtx(nodo.Hijos[0], esperado)
		nodo.TipoInferido = res
		return res

	case "ACCESO_ATRIBUTO", "LLAMADA_METODO":
		if nodo.Tipo == "LLAMADA_METODO" { panic(tipoErr(nodo, "los métodos de estructuras no están soportados")) }
		base := v.tipoDeExpresion(nodo.Hijos[0])
		s, ok := base.(*TipoEstructura); if !ok { panic(tipoErr(nodo, "el acceso '%s' requiere una estructura", nodo.Valor)) }
		t, _, ok := s.Campo(nodo.Valor.(string)); if !ok { panic(tipoErr(nodo, "la estructura '%s' no tiene el campo '%s'", s.Nombre, nodo.Valor)) }
		nodo.TipoInferido = t; return t

	default:
		panic(tipoErr(nodo, "expresión '%s' todavía no soportada por el verificador de tipos", nodo.Tipo))
	}
}

func (v *Verificador) tipoDeLlamada(nodo *Nodo) Tipo {
	nombre, _ := nodo.Valor.(string)
	if s, ok := v.estructuras[nombre]; ok {
		if len(nodo.Hijos) != len(s.Campos) { panic(tipoErr(nodo, "la construcción '%s' espera %d campos", nombre, len(s.Campos))) }
		for i, a := range nodo.Hijos { v.exigir(a, v.tipoDeExpresionCtx(a, s.Campos[i].Tipo), s.Campos[i].Tipo, "el campo de la estructura") }
		nodo.TipoInferido = s; return s
	}

	if t, ok := TipoPrimitivoDesdeNombre(nombre); ok {
		if len(nodo.Hijos) != 1 {
			panic(tipoErr(nodo, "la conversión '%s(...)' espera exactamente 1 argumento", nombre))
		}
		v.tipoDeExpresion(nodo.Hijos[0])
		return t
	}

	if nombre == "lista" || nombre == "diccionario" {
		panic(tipoErr(nodo, "la conversión dinámica '%s(x)' no es compatible con tipado estático; construí la colección con su tipo explícito", nombre))
	}

	if nombre == "escribir" {
		for _, arg := range nodo.Hijos {
			v.tipoDeExpresion(arg)
		}
		return nil
	}

	if nombre == "leer" {
		for _, arg := range nodo.Hijos {
			v.exigir(arg, v.tipoDeExpresion(arg), TipoCadena, "el mensaje de 'leer'")
		}
		return TipoCadena
	}

	if nombre == "liberar" {
		if len(nodo.Hijos) != 1 {
			panic(tipoErr(nodo, "'liberar' espera exactamente 1 argumento"))
		}
		argTipo := v.tipoDeExpresion(nodo.Hijos[0])
		switch argTipo.(type) {
		case *TipoLista, *TipoDiccionario:
			// ok: se liberan con wini_liberar_lista / wini_liberar_diccionario
		default:
			if !argTipo.Igual(TipoCadena) {
				panic(tipoErr(nodo, "'liberar' espera una cadena, lista o diccionario, se encontró %s", argTipo))
			}
		}
		return nil
	}

	if nombre == "tipo" {
		if len(nodo.Hijos) != 1 {
			panic(tipoErr(nodo, "'tipo' espera exactamente 1 argumento"))
		}
		v.tipoDeExpresion(nodo.Hijos[0])
		return TipoCadena
	}
	if nombre == "claves" || nombre == "valores" {
		if len(nodo.Hijos) != 1 {
			panic(tipoErr(nodo, "'%s' espera exactamente 1 argumento", nombre))
		}
		argTipo := v.tipoDeExpresion(nodo.Hijos[0])
		dictTipo, ok := argTipo.(*TipoDiccionario)
		if !ok {
			panic(tipoErr(nodo, "'%s' espera un diccionario, se encontró %s", nombre, argTipo))
		}
		var res Tipo
		if nombre == "claves" {
			res = NewTipoLista(dictTipo.Clave)
		} else {
			res = NewTipoLista(dictTipo.Valor)
		}
		nodo.TipoInferido = res
		return res
	}
	if nombre == "rango" {
		// Validar argumentos: 1, 2 o 3 argumentos enteros
		if len(nodo.Hijos) < 1 || len(nodo.Hijos) > 3 {
			panic(tipoErr(nodo, "rango espera 1, 2 o 3 argumentos (start, end, step)"))
		}
		for _, arg := range nodo.Hijos {
			t := v.tipoDeExpresion(arg)
			if !t.Igual(TipoEntero) {
				panic(tipoErr(arg, "los argumentos de rango deben ser enteros, se encontró %s", t))
			}
		}
		// El tipo de retorno es lista<entero>
		nodo.TipoInferido = NewTipoLista(TipoEntero)
		return nodo.TipoInferido
	}

	firma, ok := v.funciones[nombre]
	if !ok {
		panic(tipoErr(nodo, "función '%s' no declarada", nombre))
	}

	numFijos := len(firma.Parametros)
	ultimoEsLista := false
	if numFijos > 0 {
		_, ultimoEsLista = firma.Parametros[numFijos-1].(*TipoLista)
	}
	if len(nodo.Hijos) < firma.Requeridos {
		panic(tipoErr(nodo, "'%s' espera al menos %d argumento(s), se encontraron %d", nombre, firma.Requeridos, len(nodo.Hijos)))
	}
	// Solo puede haber más argumentos que parámetros fijos si el último
	// parámetro es una lista (variádico "a la Wini": los excedentes se
	// tipan uno a uno contra su tipo de elemento, más abajo) o si la
	// función es 'externa' variádica a la C (sin tipo declarado para los
	// excedentes).
	if len(nodo.Hijos) > numFijos && !ultimoEsLista && !firma.VariadicoC {
		panic(tipoErr(nodo, "'%s' espera %d argumento(s), se encontraron %d", nombre, numFijos, len(nodo.Hijos)))
	}
	// recolectaEnLista: hay más argumentos que parámetros fijos y el
	// último parámetro es una lista, así que a partir de su posición
	// (inclusive) cada argumento es un elemento suelto a recolectar, en
	// vez del caso normal de pasar una única lista ya construida en esa
	// posición (que sigue andando cuando la cantidad de argumentos
	// coincide exactamente con la cantidad de parámetros).
	recolectaEnLista := ultimoEsLista && len(nodo.Hijos) > numFijos
	for i, argNodo := range nodo.Hijos {
		if argNodo.Tipo == "ARG_NOMBRADO" {
			v.tipoDeExpresion(argNodo)
			continue
		}
		var esperadoArg Tipo
		if recolectaEnLista && i >= numFijos-1 {
			if listaVar, ok := firma.Parametros[numFijos-1].(*TipoLista); ok {
				esperadoArg = listaVar.Elemento
			}
		} else if i < numFijos {
			esperadoArg = firma.Parametros[i]
		}
		// Los excedentes de una 'externa' variádica a la C (firma.VariadicoC,
		// i >= numFijos sin lista final) quedan con esperadoArg == nil: se
		// tipan igual más abajo, para detectar errores en la expresión, pero
		// sin comparar contra un tipo esperado (no lo tienen).
		tArg := v.tipoDeExpresionCtx(argNodo, esperadoArg)
		if esperadoArg != nil && !tArg.Igual(esperadoArg) {
			panic(tipoErr(argNodo, "el argumento %d de '%s' debe ser %s, se encontró %s", i+1, nombre, esperadoArg, tArg))
		}
	}

	return firma.Retorno
}

// ---------------------------------------------------------------------
// Utilidades
// ---------------------------------------------------------------------

func tipoErr(nodo *Nodo, formato string, args ...interface{}) *ErrorTipo {
	return &ErrorTipo{ErrorConLinea: ErrorConLinea{
		Mensaje: fmt.Sprintf(formato, args...),
		Linea:   &nodo.Linea,
	}}
}

func (v *Verificador) exigir(nodo *Nodo, real Tipo, esperado Tipo, contexto string) {
	if !real.Igual(esperado) {
		panic(tipoErr(nodo, "%s debe ser %s, se encontró %s", contexto, esperado, real))
	}
}

func esOrdenable(t Tipo) bool {
	return t.Igual(TipoEntero) || t.Igual(TipoDecimal) || t.Igual(TipoCadena)
}
