package wini

import (
	"fmt"
	"strings"
)

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
	// tiposLanzados guarda todos los nombres de excepción usados en algún
	// 'lanzar Tipo(...)' del programa (incluidos los módulos importados,
	// que resolverImportaciones ya fusionó en el mismo AST). Se usa para
	// distinguir un tipo de excepción definido por el usuario (perfectamente
	// válido) de un nombre desconocido, producto de un error de tipeo.
	tiposLanzados map[string]bool
	// profundidadCapturar cuenta cuántos 'capturar' en curso hay al
	// verificar una sentencia. Lo usa 'relanzar' para saber si hay una
	// excepción en curso que propagar (si no la hay, el runtime termina el
	// proceso con un error que no señala la causa).
	profundidadCapturar int
	// avisadas acumula los avisos no fatales (código inalcanzable). No
	// detienen la compilación, pero main.go los imprime después de generar
	// el código para que el programador los vea.
	avisadas []Avisa
}

// Avisa es una advertencia no fatal con su línea.
type Avisa struct {
	Linea   int
	Mensaje string
	Columna int
}

func (a Avisa) String() string {
	return fmt.Sprintf("línea %d: %s", a.Linea, a.Mensaje)
}

// AvisasDelVerificador devuelve las advertencias acumuladas.
func AvisasDelVerificador() []Avisa { return ultimasAvisas }

// ultimasAvisas guarda las advertencias de la última verificación, para que
// quien compiló (main.go) pueda mostrarlas sin tener que tener acceso al
// Verificador, que es interno al pipeline.
var ultimasAvisas []Avisa

// tiposExcepcionNativos son los nombres de excepción que el propio runtime
// (o el codegen) puede lanzar, y que por lo tanto siempre son válidos en un
// 'capturar' aunque en el programa no haya ningún 'lanzar' con ese nombre.
// Ver wini_lanzar(...) en runtime/runtime.c y los tipos documentados en
// exepciones.go.
var tiposExcepcionNativos = map[string]bool{
	"ErrorMatematico": true,
	"EOFError":        true,
	"IndexError":      true,
	"KeyError":        true,
	"ValueError":      true,
	"TypeError":       true,
	"AttributeError":  true,
	"ImportError":     true,
	"RuntimeError":    true,
}

// pareceNombreDeExcepcionValido indica si un nombre de tipo de excepción
// tiene "forma" de nombre propio del usuario, o si más bien parece un
// error de tipeo de uno de los tipos nativos.
//
// Existe porque 'tiposLanzados' sirve para que 'capturar MiError' sea
// válido aunque el 'lanzar MiError(...)' aparezca después, en otra función
// o en un módulo importado. Pero si se aceptara CUALQUIER nombre que
// aparece en algún 'lanzar', el chequeo en el lado del lanzamiento nunca
// detectaría un error de tipeo: 'lanzar ErroRMatematico("x")' se
// auto-validaría a sí mismo y dejaría al programa con una excepción que
// ningún 'capturar' puede coincidir (el match del runtime es un strcmp
// exacto).
//
// El criterio es: nombres a distancia de edición ≤2 (ignorando mayúsculas)
// de algún tipo nativo, lo que cubre 'IndexErro', 'ErroRMatematico' o
// 'ValueErr'. Un nombre claramente propio ('MiErrorDeLaApp',
// 'ErrorDeNegocio') queda a bastante distancia de los nativos y se acepta
// sin observaciones.
func pareceTypoDeTipoNativo(nombre string) bool {
	n := strings.ToLower(nombre)
	for nativo := range tiposExcepcionNativos {
		if distanciaEdicion(n, strings.ToLower(nativo)) <= 2 {
			return true
		}
	}
	return false
}

// recolectarTiposLanzados recorre todo el AST acumulando los nombres de
// 'lanzar Tipo(...)' en 'set'. No le importa dónde aparezcan: un 'capturar'
// puede estar antes o después del 'lanzar', en otra función o en un módulo
// importado.
func recolectarTiposLanzados(n *Nodo, set map[string]bool) {
	if n == nil {
		return
	}
	if n.Tipo == "LANZAR" && len(n.Hijos) > 0 && n.Hijos[0].Tipo == "TIPO_ERROR" {
		if nombre, _ := n.Hijos[0].Valor.(string); nombre != "" {
			set[nombre] = true
		}
	}
	for _, h := range n.Hijos {
		recolectarTiposLanzados(h, set)
	}
}

func VerificarPrograma(raiz *Nodo) {
	if raiz.Tipo != "PROGRAMA" {
		panic(tipoErr(raiz, "se esperaba un nodo PROGRAMA en la raíz del AST, se encontró '%s'", raiz.Tipo))
	}
	v := &Verificador{
		funciones:     make(map[string]*TipoFuncion),
		estructuras:   make(map[string]*TipoEstructura),
		ambito:        nuevoAmbito(nil),
		tiposLanzados: make(map[string]bool),
		avisadas:      nil,
	}
	ultimasAvisas = nil
	recolectarTiposLanzados(raiz, v.tiposLanzados)
	v.registrarEstructuras(raiz.Hijos) // ahora primero
	v.registrarFunciones(raiz.Hijos)   // ahora segundo
	v.verificarBloque(raiz.Hijos)
	ultimasAvisas = v.avisadas
}

func (v *Verificador) registrarEstructuras(sentencias []*Nodo) {
	for _, s := range sentencias {
		if s.Tipo != "ESTRUCTURA" {
			continue
		}
		nombre, _ := s.Valor.(string)
		if _, ok := v.estructuras[nombre]; ok {
			panic(tipoErr(s, "la estructura '%s' ya fue declarada", nombre))
		}
		v.estructuras[nombre] = &TipoEstructura{Nombre: nombre}
	}
	for _, s := range sentencias {
		if s.Tipo != "ESTRUCTURA" {
			continue
		}
		nombre, _ := s.Valor.(string)
		t := v.estructuras[nombre]
		for _, c := range s.Hijos[0].Hijos {
			cn, _ := c.Valor.(string)
			ct := v.resolverAnotacion(c.Hijos[0])
			t.Campos = append(t.Campos, CampoEstructura{cn, ct})
		}
	}
	// Una estructura que se contiene a sí misma POR VALOR (directa o
	// indirectamente) tendría tamaño infinito. Se rechaza acá, con un
	// mensaje claro, en vez de dejar que el codegen recurra sin fin. La
	// recursión a través de una lista ('hijos: lista<Nodo>') sí es válida:
	// la lista guarda punteros, no el valor.
	for _, s := range sentencias {
		if s.Tipo != "ESTRUCTURA" {
			continue
		}
		nombre, _ := s.Valor.(string)
		if ruta := rutaContencionPorValor(v.estructuras[nombre], v.estructuras[nombre], map[string]bool{}); ruta != "" {
			panic(tipoErr(s, "la estructura '%s' se contiene a sí misma por valor (%s); un campo así tendría tamaño infinito: usá 'lista<%s>' para una relación recursiva", nombre, ruta, nombre))
		}
	}
}

// rutaContencionPorValor devuelve, si 'actual' contiene a 'objetivo' por
// valor (campo de tipo estructura, no a través de lista/diccionario), la
// cadena de campos que lo demuestra ('A.b -> B.a -> A'); "" si no.
func rutaContencionPorValor(objetivo, actual *TipoEstructura, visitadas map[string]bool) string {
	if visitadas[actual.Nombre] {
		return ""
	}
	visitadas[actual.Nombre] = true
	for _, campo := range actual.Campos {
		hijo, ok := campo.Tipo.(*TipoEstructura)
		if !ok {
			continue
		}
		paso := actual.Nombre + "." + campo.Nombre
		if hijo.Nombre == objetivo.Nombre {
			return paso + " -> " + objetivo.Nombre
		}
		if resto := rutaContencionPorValor(objetivo, hijo, visitadas); resto != "" {
			return paso + " -> " + resto
		}
	}
	return ""
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
		v.funciones[nombre] = v.construirFirma(s)
	}
}

func (v *Verificador) enNuevoAmbito(f func()) {
	anterior := v.ambito
	v.ambito = nuevoAmbito(anterior)
	// 'defer' y no una asignación al final: todos los errores del
	// verificador se señalan con panic, y sin esto un panic dentro del
	// bloque abandonaba 'v.ambito' apuntando al ámbito interno, dejando
	// el stack de ámbitos permanentemente desincronizado para todo lo que
	// siga. Hoy
	// el compile entero aborta (el recover de codegen.go corta la
	// compilación), así que no se nota; pero cualquier consumidor que
	// recupere errores por sentencia —un LSP, que es justo lo que hace
	// extension-wini-vscode— se encontraría con el verificador roto.
	defer func() { v.ambito = anterior }()
	f()
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
	return resolverAnotacionCon(nodo, nil)
}

// resolverAnotacionCon es resolverAnotacion con una función para buscar
// nombres de estructura en CUALQUIER nivel de la anotación, no solo en el
// nivel superior: así 'lista<Token>', 'lista<lista<Token>>' o una
// estructura con un campo 'lista<Nodo>' resuelven 'Token'/'Nodo' en vez de
// fallar con "tipo desconocido". 'estructura' puede ser nil (sin
// estructuras en juego, como en los usos que solo manejan primitivos).
func resolverAnotacionCon(nodo *Nodo, estructura func(string) (Tipo, bool)) Tipo {
	nombre, _ := nodo.Valor.(string)
	switch nombre {
	case "lista":
		elemento := resolverAnotacionCon(nodo.Hijos[0], estructura)
		exigirElementoColeccion(nodo.Hijos[0], elemento, posElementoLista)
		return NewTipoLista(elemento)
	case "diccionario":
		clave := resolverAnotacionCon(nodo.Hijos[0], estructura)
		valor := resolverAnotacionCon(nodo.Hijos[1], estructura)
		exigirElementoColeccion(nodo.Hijos[0], clave, posClaveDiccionario)
		exigirElementoColeccion(nodo.Hijos[1], valor, posValorDiccionario)
		return NewTipoDiccionario(clave, valor)
	case "vacio":
		panic(tipoErr(nodo, "'vacio' solo puede usarse como tipo de retorno de una función ('funcion f(): vacio:'); no es un tipo válido para variables, parámetros, ni elementos de lista o diccionario"))
	default:
		if estructura != nil {
			if t, ok := estructura(nombre); ok {
				return t
			}
		}
		t, ok := TipoPrimitivoDesdeNombre(nombre)
		if !ok {
			panic(tipoErr(nodo, "tipo desconocido '%s'", nombre))
		}
		return t
	}
}

// posicionColeccion indica en qué "hueco" de una colección va un tipo, ya
// que no todos los huecos aceptan los mismos tipos.
type posicionColeccion int

const (
	posElementoLista posicionColeccion = iota
	posClaveDiccionario
	posValorDiccionario
)

// exigirElementoColeccion rechaza temprano (en el verificador, no en el
// codegen) los tipos que el runtime no sabe guardar en ese hueco de una
// colección, con un mensaje claro en vez de uno confuso del generador.
//
//   - Elemento de lista: entero, decimal, booleano, cadena, una
//     estructura, u otra lista (a cualquier profundidad: el elemento
//     interno ya fue validado al construirse). Un diccionario o una
//     función como elemento sigue sin estar soportado.
//   - Clave de diccionario: solo entero, decimal, booleano o cadena (el
//     runtime busca por valor en arreglos tipados; una lista no es
//     comparable ni hashable).
//   - Valor de diccionario: primitivos, o una lista o estructura (valores
//     genéricos con descriptor, igual que los elementos de lista).
func exigirElementoColeccion(nodo *Nodo, t Tipo, pos posicionColeccion) {
	if _, ok := t.(*TipoPrimitivo); ok {
		return
	}
	if pos == posElementoLista || pos == posValorDiccionario {
		switch t.(type) {
		case *TipoEstructura, *TipoLista:
			return
		}
	}
	if pos == posElementoLista {
		panic(tipoErr(nodo, "las listas con elementos de tipo %s no están soportadas todavía: los elementos de una lista pueden ser entero, decimal, booleano, cadena, una estructura u otra lista", t))
	}
	panic(tipoErr(nodo, "las colecciones con elementos de tipo %s no están soportadas todavía: la clave de un diccionario solo puede ser entero, decimal, booleano o cadena, y el valor esos mismos o una lista o estructura", t))
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

func (v *Verificador) construirFirma(nodoFuncion *Nodo) *TipoFuncion {
	return construirFirmaConResolutor(nodoFuncion, v.resolverAnotacion)
}

func construirFirmaConResolutor(nodoFuncion *Nodo, resolver func(*Nodo) Tipo) *TipoFuncion {
	paramsNodo := nodoFuncion.Hijos[0]
	tipoRetNodo := nodoFuncion.Hijos[1]
	parametros := make([]Tipo, 0, len(paramsNodo.Hijos))
	esVariadico := false
	requeridos := -1 // índice del primer parámetro con valor por defecto
	for i, p := range paramsNodo.Hijos {
		anotacion := p.Hijos[0].Hijos[0]
		t := resolver(anotacion)
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
	// Un parámetro variádico ('*nombre') no puede tener valor por defecto
	// (el parser lo rechaza explícitamente), así que el bucle de arriba
	// siempre lo contaba como obligatorio y 'f()' fallaba con "espera al
	// menos 1 argumento(s)". El variádico justamente representa 0 o más
	// argumentos, así que no debe contar para el mínimo obligatorio.
	if esVariadico {
		requeridos--
	}
	retornoNodo := tipoRetNodo.Hijos[0]
	retorno := Tipo(nil)
	if nombre, ok := retornoNodo.Valor.(string); ok && nombre == "vacio" {
		retorno = TipoVacio
	} else {
		retorno = resolver(retornoNodo)
	}
	firma := NewTipoFuncion(parametros, retorno, requeridos)
	firma.EsVariadico = esVariadico
	// Guardar los nombres de los parámetros para resolver argumentos
	// nombrados en las llamadas ('f(b = 1, a = 2)').
	for _, p := range paramsNodo.Hijos {
		nombreParam, _ := p.Valor.(string)
		firma.Nombres = append(firma.Nombres, nombreParam)
	}
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

// verificarBloque verifica las sentencias de un bloque. A partir de la
// primera que corta el flujo de control ('retornar', 'romper', 'continuar',
// 'lanzar'), las siguientes son inalcanzables: no se verifican (su error de
// tipos es irrelevante si nunca se ejecutan) y se reporta una advertencia
// para que no quede código muerto en silencio.
func (v *Verificador) verificarBloque(sentencias []*Nodo) {
	for i, s := range sentencias {
		// La sentencia terminadora SE verifica igual: es la que más
		// validaciones necesita ('retornar X' contra el tipo de retorno,
		// 'lanzar TipoDesconocido', 'relanzar' sin un 'capturar' que lo
		// respalde). Solo lo que viene DESPUÉS es inalcanzable y se avisa
		// en vez de verificarse.
		v.verificarSentencia(s)
		if terminaElFlujo(s) {
			for _, muerto := range sentencias[i+1:] {
				v.avisar(muerto,
					"esta sentencia es inalcanzable: la anterior (%s) termina el flujo de control",
					tipoTerminador(s))
			}
			return
		}
	}
}

// terminaElFlujo indica si la sentencia nunca deja el control volver a la
// siguiente del bloque.
func terminaElFlujo(s *Nodo) bool {
	switch s.Tipo {
	case "RETORNO", "ROMPER", "CONTINUAR", "LANZAR", "RELANZAR":
		return true
	}
	return false
}

// tipoTerminador da un nombre legible de por qué se cortó el flujo, para el
// mensaje de código inalcanzable.
func tipoTerminador(s *Nodo) string {
	switch s.Tipo {
	case "RETORNO":
		return "'retornar'"
	case "ROMPER":
		return "'romper'"
	case "CONTINUAR":
		return "'continuar'"
	case "LANZAR":
		return "'lanzar'"
	case "RELANZAR":
		return "'relanzar'"
	}
	return "'" + s.Tipo + "'"
}

// avisar registra una advertencia. No interrumpe la compilación: el código
// es válido, solo que hay algo que probablemente no se quiso escribir.
//
// OJO con la guarda: un slice nil admite append (crea el backing array), así
// que comprobarla abortaría el PRIMER aviso siempre —que es justo el que
// importa, porque en ese momento el slice todavía es nil. Solo se descarta
// el caso del verificador inexistente.
func (v *Verificador) avisar(nodo *Nodo, formato string, args ...interface{}) {
	if v == nil {
		return
	}
	mensaje := fmt.Sprintf(formato, args...)
	v.avisadas = append(v.avisadas, Avisa{
		Linea:   nodo.Linea,
		Mensaje: mensaje,
	})
}

func (v *Verificador) verificarSentencia(nodo *Nodo) {
	switch nodo.Tipo {
	case "ESTRUCTURA":
		return

	case "ASIGNACION_CAMPO":
		baseNodo, campoNodo, valorNodo := nodo.Hijos[0], nodo.Hijos[1], nodo.Hijos[2]
		nombreBase, _ := baseNodo.Valor.(string)
		nombreCampo, _ := campoNodo.Valor.(string)
		baseTipo, ok := v.ambito.buscar(nombreBase)
		if !ok {
			panic(tipoErr(nodo, "variable '%s' no declarada", nombreBase))
		}
		s, ok := baseTipo.(*TipoEstructura)
		if !ok {
			panic(tipoErr(nodo, "no se puede asignar a '%s.%s': '%s' no es una estructura, es de tipo %s", nombreBase, nombreCampo, nombreBase, baseTipo))
		}
		tipoCampo, _, ok := s.Campo(nombreCampo)
		if !ok {
			panic(tipoErr(nodo, "la estructura '%s' no tiene el campo '%s'", s.Nombre, nombreCampo))
		}
		tipoValor := v.tipoDeExpresionCtx(valorNodo, tipoCampo)
		if !tipoValor.Igual(tipoCampo) {
			panic(tipoErr(valorNodo, "no se puede asignar un valor de tipo %s a '%s.%s' (campo declarado como %s)", tipoValor, nombreBase, nombreCampo, tipoCampo))
		}

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
				panic(tipoErr(iterableNodo, "'para ... en' requiere una lista, un diccionario o una cadena, se encontró %s", nombreDeTipo(contenedor)))
			}
		}
		v.enNuevoAmbito(func() {
			nombreVar, _ := variableNodo.Valor.(string)
			// El error no se descarta con '_ =': el ámbito es nuevo y está
			// vacío, así que hoy no puede fallar, pero si alguna vez lo
			// hiciera (una declaración previa en el mismo ámbito, un
			// cambio en nuevoAmbito) el fallo sería silencioso y la
			// variable quedaría sin tipo. Panica con contexto, como el
			// resto de los errores del verificador.
			if err := v.ambito.declarar(nombreVar, tipoVar); err != nil {
				panic(tipoErr(variableNodo, "%s", err.Error()))
			}
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
				// Mismo criterio que en el 'lanzar': un nombre a poca
				// distancia de edición de un tipo nativo es un error de
				// tipeo, no un tipo propio. Si no se rechazara acá, el
				// 'capturar IndexErro' aceptaría al typo porque el
				// programa también lo lanza (tiposLanzados), y el
				// programa compilaría con una captura que nunca puede
				// coincidir (el match del runtime es un strcmp exacto).
				if !tiposExcepcionNativos[tipoError] && pareceTypoDeTipoNativo(tipoError) {
					sugerencia := ""
					if cercano, ok := tipoExcepcionParecido(tipoError); ok {
						sugerencia = fmt.Sprintf("; ¿querías decir '%s'?", cercano)
					}
					panic(tipoErr(capturar, "el tipo de excepción '%s' no es conocido%s. Los tipos personalizados deben tener un nombre propio, no parecer un error de tipeo de uno nativo", tipoError, sugerencia))
				}
				v.exigirTipoExcepcionConocido(capturar, tipoError)
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
					if err := v.ambito.declarar(nombreVar, TipoCadena); err != nil {
						panic(tipoErr(variableNodo, "%s", err.Error()))
					}
				}
				// Dentro del cuerpo del 'capturar' sí hay una excepción en
				// curso, así que 'relanzar' es válido (lo que hace falta
				// para envolver y re-lanzar con otro tipo).
				v.profundidadCapturar++
				defer func() { v.profundidadCapturar-- }()
				v.verificarBloque(cuerpoNodo.Hijos)
			})
		}

		if len(bloqueFinally.Hijos) > 0 {
			v.enNuevoAmbito(func() { v.verificarBloque(bloqueFinally.Hijos) })
		}

	case "LANZAR":
		// El tipo de la excepción se valida en el mismo lugar donde se
		// lanza, y no solo en el 'capturar'. Antes solo se chequeaba del
		// lado de la captura, así que un nombre mal escrito
		// ('lanzar ErroRMatematico(...)') compilaba sin quejas, generaba
		// código correcto y en runtime quedaba una excepción que NINGÚN
		// 'capturar' podía coincidir (el match es un strcmp exacto): el
		// programa moría con un error que no parecía tener causa.
		if nodo.Hijos[0] != nil {
			if nombreTipo, ok := nodo.Hijos[0].Valor.(string); ok && nombreTipo != "" {
				// Si contiene un '.', es un tipo de módulo: se toma la
				// parte final, que es la que llega al runtime.
				simple := nombreTipo
				for i := len(nombreTipo) - 1; i >= 0; i-- {
					if nombreTipo[i] == '.' {
						simple = nombreTipo[i+1:]
						break
					}
				}
				// Un nombre que está a poca distancia de edición de un tipo nativo
				// casi seguro es un error de tipeo, aunque el programa lo
				// lance él mismo (si no, se auto-validaría y el chequeo no
				// serviría de nada). Los nombres claramente propios
				// ('MiErrorDeLaApp') quedan lejos de los nativos y se
				// aceptan sin observaciones.
				if !tiposExcepcionNativos[simple] && pareceTypoDeTipoNativo(simple) {
					sugerencia := ""
					if cercano, ok := tipoExcepcionParecido(simple); ok {
						sugerencia = fmt.Sprintf("; ¿querías decir '%s'?", cercano)
					}
					panic(tipoErr(nodo.Hijos[0], "el tipo de excepción '%s' no es conocido%s. Los tipos personalizados deben tener un nombre propio, no parecer un error de tipeo de uno nativo", simple, sugerencia))
				}
				v.exigirTipoExcepcionConocido(nodo.Hijos[0], simple)
			}
		}
		mensajeNodo := nodo.Hijos[1]
		if len(mensajeNodo.Hijos) > 0 {
			tipoMsg := v.tipoDeExpresionCtx(mensajeNodo.Hijos[0], TipoCadena)
			v.exigir(mensajeNodo.Hijos[0], tipoMsg, TipoCadena, "el mensaje de 'lanzar'")
		}

	case "RELANZAR":
		// 'relanzar' propaga la excepción en curso. Si no hay ninguna
		// (o sea, estamos fuera de todo 'capturar'), el runtime no tiene
		// a dónde saltar y termina el proceso con un error genérico,
		// sin relación aparente con la línea que lo causó. Es mejor
		// rechazarlo al compilar.
		if v.profundidadCapturar == 0 {
			panic(tipoErr(nodo, "'relanzar' solo puede usarse dentro de un bloque 'capturar'; afuera no hay ninguna excepción en curso que propagar"))
		}

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
	// Igual que en enNuevoAmbito: si el cuerpo falla con panic, el ámbito y
	// el tipo de retorno esperado tienen que restaurarse igual.
	defer func() {
		v.ambito = ambitoAnterior
		v.retornoEsperado = retornoAnterior
	}()

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
	// La restauración de ambito/retornoEsperado la hace el defer de arriba.
}

func (v *Verificador) resolverAnotacion(n *Nodo) Tipo {
	return resolverAnotacionCon(n, func(nombre string) (Tipo, bool) {
		t, ok := v.estructuras[nombre]
		if !ok {
			return nil, false
		}
		return t, true
	})
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

	case "CADENA_TEXTO":
		nodo.TipoInferido = TipoCadena
		return TipoCadena

	case "CADENA_INTERPOLADA":
		// Los hijos del nodo son las {expresiones} interpoladas, ya
		// parseadas como expresiones completas: se verifican una por una.
		// Solo se puede interpolar un valor convertible a cadena: los
		// primitivos (que el codegen convierte con wini_*_to_string) y
		// cadenas. Colecciones y estructuras no soportadas todavía.
		for _, h := range nodo.Hijos {
			t := v.tipoDeExpresion(h)
			switch {
			case t.Igual(TipoEntero), t.Igual(TipoDecimal), t.Igual(TipoBooleano), t.Igual(TipoCadena):
				// ok
			default:
				panic(tipoErr(h, "no se puede interpolar un valor de tipo %s en una cadena; solo entero, decimal, booleano o cadena", t))
			}
		}
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
		// El parser solo produce nodos LOGICO de 1 hijo ('no x') o de 2
		// ('a y b'). Se valida la forma para que un nodo mal formado dé un
		// error con línea en lugar de un 'index out of range' interno, que
		// este archivo existe para evitar.
		if len(nodo.Hijos) != 2 {
			panic(tipoErr(nodo, "el operador lógico '%s' espera 1 o 2 operandos, se recibieron %d", op, len(nodo.Hijos)))
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
			panic(tipoErr(nodo, "'en' requiere una lista o un diccionario a la derecha, se encontró %s", nombreDeTipo(contenedor)))
		}
		elemento := v.tipoDeExpresionCtx(nodo.Hijos[0], esperado)
		if !elemento.Igual(esperado) {
			panic(tipoErr(nodo.Hijos[0], "el operando izquierdo de 'en' debe ser %s, se encontró %s", esperado, elemento))
		}
		nodo.TipoInferido = TipoBooleano
		return TipoBooleano

	case "BINARIA":
		op, _ := nodo.Valor.(string)
		// El parser siempre arma BINARIA con exactamente dos operandos.
		if len(nodo.Hijos) != 2 {
			panic(tipoErr(nodo, "la operación '%s' espera 2 operandos, se recibieron %d", op, len(nodo.Hijos)))
		}
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
			panic(tipoErr(nodo, "operandos de tipos distintos en '%s': %s y %s (no hay conversión implícita; castea explícitamente, ej. decimal(a) / decimal(b))", op, izq, der))
		}
		var res Tipo
		switch op {
		case "+":
			if izq.Igual(TipoCadena) || izq.Igual(TipoEntero) || izq.Igual(TipoDecimal) {
				res = izq
			} else if _, esLista := izq.(*TipoLista); esLista {
				// Concatenación de listas del mismo tipo: el codegen la
				// resuelve con wini_list_concat (elementos primitivos) o
				// wini_list_concat_gen (listas de listas / de
				// estructuras). Cualquier lista ya validada sirve.
				res = izq
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
			// El codegen solo sabe comparar primitivos y colecciones
			// simples; una estructura como operando no tiene comparación
			// implementada. Antes el verificador la aceptaba y el programa
			// moría más adelante en el codegen con un error que suena a
			// limitación interna ("comparaciones no soportadas para %s en
			// la base mínima"). Se rechaza acá, que es donde corresponde.
			if !esComparable(izq) {
				panic(tipoErr(nodo, "'%s' no se puede comparar con %s", op, izq))
			}
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
		// Mismo criterio que resolverAnotacion (rechazo temprano de lo que
		// el runtime no puede guardar como elemento de lista).
		exigirElementoColeccion(nodo.Hijos[0], primero, posElementoLista)
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
		// Igual que en LISTA, pero solo para el valor: la clave ya la
		// garantiza ClaveDiccionarioValida (solo primitivos).
		exigirElementoColeccion(primerPar.Hijos[1], tValor, posValorDiccionario)
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
		res := v.tipoDeLlamada(nodo, esperado)
		nodo.TipoInferido = res
		return res

	case "ARG_NOMBRADO":
		res := v.tipoDeExpresionCtx(nodo.Hijos[0], esperado)
		nodo.TipoInferido = res
		return res

	case "ACCESO_ATRIBUTO", "LLAMADA_METODO":
		if nodo.Tipo == "LLAMADA_METODO" {
			panic(tipoErr(nodo, "los métodos de estructuras no están soportados"))
		}
		base := v.tipoDeExpresion(nodo.Hijos[0])
		s, ok := base.(*TipoEstructura)
		if !ok {
			panic(tipoErr(nodo, "el acceso '%s' requiere una estructura", nodo.Valor))
		}
		// Con la forma ', ok': una aserción desnuda sobre un Nodo mal
		// formado revienta con "interface conversion: interface {} is X,
		// not string", un panic interno sin línea ni contexto, cuando este
		// archivo entero existe para dar errores claros y localizados.
		nombreCampo, esTexto := nodo.Valor.(string)
		if !esTexto {
			panic(tipoErr(nodo, "acceso a un campo con un nombre inválido"))
		}
		t, _, ok := s.Campo(nombreCampo)
		if !ok {
			panic(tipoErr(nodo, "la estructura '%s' no tiene el campo '%s'", s.Nombre, nombreCampo))
		}
		nodo.TipoInferido = t
		return t

	default:
		panic(tipoErr(nodo, "expresión '%s' todavía no soportada por el verificador de tipos", nodo.Tipo))
	}
}

func (v *Verificador) tipoDeLlamada(nodo *Nodo, esperado Tipo) Tipo {
	nombre, _ := nodo.Valor.(string)
	if s, ok := v.estructuras[nombre]; ok {
		if len(nodo.Hijos) != len(s.Campos) {
			panic(tipoErr(nodo, "la construcción '%s' espera %d campos", nombre, len(s.Campos)))
		}
		for i, a := range nodo.Hijos {
			v.exigir(a, v.tipoDeExpresionCtx(a, s.Campos[i].Tipo), s.Campos[i].Tipo, "el campo de la estructura")
		}
		nodo.TipoInferido = s
		return s
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
		return TipoSinValor
	}

	if nombre == "leer" {
		if len(nodo.Hijos) != 1 {
			panic(tipoErr(nodo, "'leer' espera exactamente 1 argumento (el prompt)"))
		}
		for _, arg := range nodo.Hijos {
			v.exigir(arg, v.tipoDeExpresion(arg), TipoCadena, "el mensaje de 'leer'")
		}
		if esperado != nil && (esperado.Igual(TipoEntero) || esperado.Igual(TipoDecimal)) {
			return esperado
		}
		return TipoCadena
	}

	if nombre == "liberar" {
		if len(nodo.Hijos) != 1 {
			panic(tipoErr(nodo, "'liberar' espera exactamente 1 argumento"))
		}
		argTipo := v.tipoDeExpresion(nodo.Hijos[0])
		switch argTipo.(type) {
		case *TipoLista, *TipoDiccionario, *TipoEstructura:
			// ok: se liberan con wini_liberar_lista / wini_liberar_diccionario,
			// o (estructura) liberando cada referencia heap que contiene.
		default:
			if !argTipo.Igual(TipoCadena) {
				panic(tipoErr(nodo, "'liberar' espera una cadena, lista, diccionario o estructura, se encontró %s", argTipo))
			}
		}
		return TipoSinValor
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
	if nombre == "longitud" {
		if len(nodo.Hijos) != 1 {
			panic(tipoErr(nodo, "'longitud' espera exactamente 1 argumento"))
		}
		argTipo := v.tipoDeExpresion(nodo.Hijos[0])
		switch argTipo.(type) {
		case *TipoLista, *TipoDiccionario:
			// ok: cantidad de elementos / pares clave-valor.
		default:
			if !argTipo.Igual(TipoCadena) {
				panic(tipoErr(nodo, "'longitud' espera una cadena, lista o diccionario, se encontró %s", argTipo))
			}
		}
		return TipoEntero
	}
	if nombre == "agregar" {
		if len(nodo.Hijos) != 2 {
			panic(tipoErr(nodo, "'agregar' espera exactamente 2 argumentos (lista y elemento)"))
		}
		argTipo := v.tipoDeExpresion(nodo.Hijos[0])
		listaTipo, ok := argTipo.(*TipoLista)
		if !ok {
			panic(tipoErr(nodo, "'agregar' espera una lista como primer argumento, se encontró %s", argTipo))
		}
		v.exigir(nodo.Hijos[1], v.tipoDeExpresion(nodo.Hijos[1]), listaTipo.Elemento, "el elemento de 'agregar'")
		res := NewTipoLista(listaTipo.Elemento)
		nodo.TipoInferido = res
		return res
	}
	if nombre == "vacia" {
		if len(nodo.Hijos) != 1 {
			panic(tipoErr(nodo, "'vacia' espera exactamente 1 argumento"))
		}
		argTipo := v.tipoDeExpresion(nodo.Hijos[0])
		switch argTipo.(type) {
		case *TipoLista, *TipoDiccionario:
			// ok: sin elementos / pares.
		default:
			if !argTipo.Igual(TipoCadena) {
				panic(tipoErr(nodo, "'vacia' espera una cadena, lista o diccionario, se encontró %s", argTipo))
			}
		}
		return TipoBooleano
	}
	if nombre == "primero" || nombre == "ultimo" {
		if len(nodo.Hijos) != 1 {
			panic(tipoErr(nodo, "'%s' espera exactamente 1 argumento (la lista)", nombre))
		}
		argTipo := v.tipoDeExpresion(nodo.Hijos[0])
		listaTipo, ok := argTipo.(*TipoLista)
		if !ok {
			panic(tipoErr(nodo, "'%s' espera una lista, se encontró %s", nombre, argTipo))
		}
		// En lista vacía el runtime lanza IndexError capturable.
		return listaTipo.Elemento
	}
	if nombre == "insertar" {
		if len(nodo.Hijos) != 3 {
			panic(tipoErr(nodo, "'insertar' espera exactamente 3 argumentos (lista, índice y elemento)"))
		}
		argTipo := v.tipoDeExpresion(nodo.Hijos[0])
		listaTipo, ok := argTipo.(*TipoLista)
		if !ok {
			panic(tipoErr(nodo, "'insertar' espera una lista como primer argumento, se encontró %s", argTipo))
		}
		v.exigir(nodo.Hijos[1], v.tipoDeExpresion(nodo.Hijos[1]), TipoEntero, "el índice de 'insertar'")
		v.exigir(nodo.Hijos[2], v.tipoDeExpresion(nodo.Hijos[2]), listaTipo.Elemento, "el elemento de 'insertar'")
		res := NewTipoLista(listaTipo.Elemento)
		nodo.TipoInferido = res
		return res
	}
	if nombre == "quitar" {
		if len(nodo.Hijos) != 2 {
			panic(tipoErr(nodo, "'quitar' espera exactamente 2 argumentos (lista e índice)"))
		}
		argTipo := v.tipoDeExpresion(nodo.Hijos[0])
		listaTipo, ok := argTipo.(*TipoLista)
		if !ok {
			panic(tipoErr(nodo, "'quitar' espera una lista como primer argumento, se encontró %s", argTipo))
		}
		v.exigir(nodo.Hijos[1], v.tipoDeExpresion(nodo.Hijos[1]), TipoEntero, "el índice de 'quitar'")
		res := NewTipoLista(listaTipo.Elemento)
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

	// El último parámetro variádico "a la Wini" ('*nombre:tipo') agrupa en
	// realidad 0 o más argumentos posicionales sueltos en una lista. Por eso
	// no cuenta como parámetro fijo: numFijos es la cantidad de parámetros
	// que reciben exactamente un argumento posicional cada uno. Sin esto,
	// una llamada con exactamente numFijos argumentos (incluido el
	// variádico) se tomato el camino de "una lista ya construida" y los
	// argumentos sueltos se tipaban contra lista<tipo> en vez de tipo.
	numFijos := len(firma.Parametros)
	if firma.EsVariadico {
		numFijos--
	}
	// ultimoEsLista se calcula sobre el ÚLTIMO parámetro real (incluido el
	// variádico si lo hay), no sobre numFijos: para una firma variádica pura
	// el último parámetro es justamente el que recibe los argumentos
	// sueltos. Solo se usa para decidir si los excedentes se recolectan en
	// una lista.
	ultimoEsLista := false
	if len(firma.Parametros) > 0 {
		_, ultimoEsLista = firma.Parametros[len(firma.Parametros)-1].(*TipoLista)
	}

	// --- Argumentos nombrados ('f(a = 1, b = 2)') ---
	// Reglas: el nombre debe existir en la firma; no se puede pasar el
	// mismo parámetro dos veces (ni dos nombrados con el mismo nombre, ni
	// un posicional que ya lo cubre más un nombrado para él); no puede
	// haber posicionales después de un nombrado; y una función variádica
	// (*x o ...) no acepta nombrados (los excedentes son elementos de la
	// lista variádica o argumentos C sueltos, no tienen nombre asignable).
	hayNombrado := false
	cantidadPosicionales := 0
	for i, a := range nodo.Hijos {
		if a.Tipo != "ARG_NOMBRADO" {
			if hayNombrado {
				panic(tipoErr(a, "argumento posicional después de un argumento con nombre en la llamada a '%s'; los posicionales deben ir primero", nombre))
			}
			cantidadPosicionales++
			continue
		}
		if ultimoEsLista {
			nombreArg, _ := a.Valor.(string)
			panic(tipoErr(a, "'%s' es variádica (*%s) y no acepta argumentos con nombre", nombre, nombreArg))
		}
		if firma.VariadicoC {
			panic(tipoErr(a, "'%s' es una función externa variádica (...) y no acepta argumentos con nombre", nombre))
		}
		hayNombrado = true
		nombreArg, _ := a.Valor.(string)
		idx := firma.nombreAIndice(nombreArg)
		if idx == -1 {
			panic(tipoErr(a, "'%s' no es un parámetro de '%s' (su firma es: %s)", nombreArg, nombre, firma.String()))
		}
		if idx < cantidadPosicionales {
			panic(tipoErr(a, "el parámetro '%s' recibe más de un argumento en la llamada a '%s'", nombreArg, nombre))
		}
		for j := 0; j < i; j++ {
			if nodo.Hijos[j].Tipo == "ARG_NOMBRADO" {
				nombrePrevio, _ := nodo.Hijos[j].Valor.(string)
				if nombrePrevio == nombreArg {
					panic(tipoErr(a, "el parámetro '%s' recibe más de un argumento en la llamada a '%s'", nombreArg, nombre))
				}
			}
		}
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
	// Índice real del último parámetro de la firma (el que recibe la lista
	// recolectada si recolectaEnLista). Coincide con numFijos salvo que
	// haya un variádico al final, en cuyo caso es numFijos (el variádico
	// ocupa la última posición).
	idxUltimoParam := len(firma.Parametros) - 1
	for i, argNodo := range nodo.Hijos {
		if argNodo.Tipo == "ARG_NOMBRADO" {
			v.tipoDeExpresion(argNodo)
			continue
		}
		var esperadoArg Tipo
		if recolectaEnLista && i >= idxUltimoParam {
			if listaVar, ok := firma.Parametros[idxUltimoParam].(*TipoLista); ok {
				esperadoArg = listaVar.Elemento
			}
		} else if i < len(firma.Parametros) {
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

// nombreDeTipo formatea un Tipo para los mensajes de error. Un Tipo nil
// (la interfaz vacía) se imprimía con %s como "%!s(<nil>)", que es ruido
// técnico en un mensaje que le/user se muestra al programador.
func nombreDeTipo(t Tipo) string {
	if t == nil {
		return "(sin tipo)"
	}
	return t.String()
}

func tipoErr(nodo *Nodo, formato string, args ...interface{}) *ErrorTipo {
	return &ErrorTipo{ErrorConLinea: ErrorConLinea{
		Mensaje: fmt.Sprintf(formato, args...),
		Linea:   &nodo.Linea,
	}}
}

func (v *Verificador) exigir(nodo *Nodo, real Tipo, esperado Tipo, contexto string) {
	if EsTipoSinValor(real) {
		panic(tipoErr(nodo, "%s no puede ser una llamada como '%s', que no devuelve valor", contexto, nodo.Valor))
	}
	if !real.Igual(esperado) {
		panic(tipoErr(nodo, "%s debe ser %s, se encontró %s", contexto, esperado, real))
	}
}

// exigirTipoExcepcionConocido valida el nombre de tipo de un 'capturar'.
// Un nombre es válido si es un tipo nativo del runtime o si el programa lo
// lanza en algún 'lanzar ...' (los errores personalizados son una función
// del lenguaje, ver ErrorPersonalizado en exepciones.go). Cualquier otro
// nombre —el caso típico es un error de tipeo, como 'ErrorIndice' por
// 'IndexError'— antes pasaba en silencio y producía un 'capturar' que no
// atrapaba nada; ahora es un error de compilación, con sugerencia del tipo
// nativo parecido más cercano cuando lo hay.
func (v *Verificador) exigirTipoExcepcionConocido(nodo *Nodo, nombre string) {
	if tiposExcepcionNativos[nombre] || v.tiposLanzados[nombre] {
		return
	}
	sugerencia := ""
	if cercano, ok := tipoExcepcionParecido(nombre); ok {
		sugerencia = fmt.Sprintf("; ¿querías decir '%s'?", cercano)
	}
	panic(tipoErr(nodo, "el tipo de excepción '%s' no es conocido: no es un tipo nativo ni se lanza en ningún 'lanzar %s(...)' del programa%s", nombre, nombre, sugerencia))
}

// tipoExcepcionParecido busca, entre los tipos nativos, el más parecido a
// 'nombre' (distancia de edición, ignorando mayúsculas y la palabra
// 'error', que suele ir como prefijo o sufijo). Devuelve el candidato solo
// si la distancia es chica, para no sugerir cualquier cosa.
func tipoExcepcionParecido(nombre string) (string, bool) {
	normalizado := normalizarNombreExcepcion(nombre)
	mejor := ""
	mejorDist := -1
	for nativo := range tiposExcepcionNativos {
		d := distanciaEdicion(normalizado, normalizarNombreExcepcion(nativo))
		// Desempate explícito: a igual distancia gana el nombre menor. Sin
		// esto gana el último del map, y el orden de iteración de un map de
		// Go es aleatorio, así que dos compilaciones del mismo programa
		// podían sugerir nombres distintos.
		if mejorDist == -1 || d < mejorDist || (d == mejorDist && nativo < mejor) {
			mejor, mejorDist = nativo, d
		}
	}
	if mejorDist >= 0 && mejorDist <= 3 {
		return mejor, true
	}
	return "", false
}

func normalizarNombreExcepcion(s string) string {
	var b []rune
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			r = r - 'A' + 'a'
		}
		b = append(b, r)
	}
	todo := string(b)
	// Se recorta por PREFIJO y SUFIJO, no por índices numéricos: con un
	// nombre multibyte ('Érror', 'ÑandúError'), un corte por bytes parte un
	// rune por la mitad y deja un texto UTF-8 inválido, que después hace
	// que la distancia de edición compare basura.
	if len(todo) >= 5 && strings.HasPrefix(todo, "error") {
		todo = todo[5:]
	}
	if len(todo) >= 5 && strings.HasSuffix(todo, "error") {
		todo = todo[:len(todo)-5]
	}
	return todo
}

// distanciaEdicion es la distancia de Levenshtein clásica (con memoria O(n)).
func distanciaEdicion(a, b string) int {
	aa, bb := []rune(a), []rune(b)
	if len(aa) == 0 {
		return len(bb)
	}
	if len(bb) == 0 {
		return len(aa)
	}
	anterior := make([]int, len(bb)+1)
	actual := make([]int, len(bb)+1)
	for j := range anterior {
		anterior[j] = j
	}
	for i := 1; i <= len(aa); i++ {
		actual[0] = i
		for j := 1; j <= len(bb); j++ {
			costo := 1
			if aa[i-1] == bb[j-1] {
				costo = 0
			}
			min := anterior[j] + 1
			if actual[j-1]+1 < min {
				min = actual[j-1] + 1
			}
			if anterior[j-1]+costo < min {
				min = anterior[j-1] + costo
			}
			actual[j] = min
		}
		anterior, actual = actual, anterior
	}
	return anterior[len(bb)]
}

func esOrdenable(t Tipo) bool {
	return t.Igual(TipoEntero) || t.Igual(TipoDecimal) || t.Igual(TipoCadena)
}

// esComparable indica si el codegen sabe comparar dos valores de este tipo
// con '=='/'!='. Cubre los primitivos (comparación directa en LLVM) y las
// listas/diccionarios (el runtime tiene 'contains' y compara por
// identidad/contenido). Las estructuras quedan afuera: no hay comparación
// por valor implementada, y aceptarlas aquí solo difería el error hasta el
// codegen.
func esComparable(t Tipo) bool {
	if t == nil {
		return false
	}
	if t.Igual(TipoEntero) || t.Igual(TipoDecimal) ||
		t.Igual(TipoCadena) || t.Igual(TipoBooleano) {
		return true
	}
	switch t.(type) {
	case *TipoLista, *TipoDiccionario:
		return true
	}
	return false
}
