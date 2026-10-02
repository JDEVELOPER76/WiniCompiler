package wini

import "fmt"

// ErrorConLinea es la base para errores que tienen línea y columna
type ErrorConLinea struct {
	Mensaje string
	Linea   *int
	Columna *int
	Texto   string
}

func (e *ErrorConLinea) Error() string {
	base := e.Mensaje
	if e.Linea != nil {
		base += fmt.Sprintf(" (línea %d", *e.Linea)
		if e.Columna != nil {
			base += fmt.Sprintf(", columna %d", *e.Columna)
		}
		base += ")"
	}
	if e.Texto != "" {
		base += fmt.Sprintf("\n  %s", e.Texto)
	}
	return base
}

// SintaxisError es un error de sintaxis con ubicación
type SintaxisError struct {
	ErrorConLinea
}

// RuntimeError es un error en tiempo de ejecución con ubicación
type RuntimeError struct {
	ErrorConLinea
}

// ErrorTipo es un error de tipo (TypeError)
type ErrorTipo struct {
	ErrorConLinea
}

// ErrorValor es un error de valor (ValueError)
type ErrorValor struct {
	ErrorConLinea
}

// ErrorIndice es un error de índice (IndexError)
type ErrorIndice struct {
	ErrorConLinea
}

// ErrorAtributo es un error de atributo (AttributeError)
type ErrorAtributo struct {
	ErrorConLinea
}

// ErrorImportacion es un error de importación (ImportError)
type ErrorImportacion struct {
	ErrorConLinea
}

// ErrorMatematico es un error matemático (ZeroDivisionError, etc)
type ErrorMatematico struct {
	ErrorConLinea
}

// ErrorPersonalizado permite lanzar/capturar tipos de error definidos por
// el usuario en código Wini (cualquier nombre que no sea uno de los tipos
// nativos), por ejemplo: lanzar ErrorJSON("mensaje") / capturar ErrorJSON
// como err:
type ErrorPersonalizado struct {
	ErrorConLinea
	Tipo string
}

func (e *ErrorPersonalizado) Error() string {
	base := fmt.Sprintf("%s: %s", e.Tipo, e.Mensaje)
	if e.Linea != nil {
		base += fmt.Sprintf(" (línea %d)", *e.Linea)
	}
	return base
}

// RetornoException es una excepción para manejar retornos dentro de funciones
type RetornoException struct {
	Valor interface{}
}

func (e *RetornoException) Error() string {
	return fmt.Sprintf("return %v", e.Valor)
}

// RomperException es una excepción para romper bucles
type RomperException struct{}

func (e *RomperException) Error() string {
	return "break"
}

// ContinuarException es una excepción para continuar en bucles
type ContinuarException struct{}

func (e *ContinuarException) Error() string {
	return "continue"
}

// LanzarException es una excepción para lanzar errores manualmente
type LanzarException struct {
	Valor interface{}
	Linea *int
}

func (e *LanzarException) Error() string {
	if e.Linea != nil {
		return fmt.Sprintf("%v (línea %d)", e.Valor, *e.Linea)
	}
	return fmt.Sprintf("%v", e.Valor)
}
