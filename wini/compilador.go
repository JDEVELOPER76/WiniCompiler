package wini

import "path/filepath"

// GeneradorCodigo representa el backend que convierte un AST verificado en
// código de salida.
type GeneradorCodigo interface {
	Generate(raiz *Nodo) (string, error)
}

// FabricaGeneradorCodigo permite seleccionar el backend para una compilación.
// El nombre del módulo se usa como nombre de origen en el formato generado.
type FabricaGeneradorCodigo func(nombreModulo string) GeneradorCodigo

// Compilador contiene la configuración compartida del pipeline Wini.
//
// Si Fabrica es nil, se utiliza el backend LLVM integrado (Codegen).
type Compilador struct {
	NombreModulo string
	Fabrica      FabricaGeneradorCodigo
}

// NuevoCompilador crea un compilador que genera LLVM mediante el codegen
// integrado.
func NuevoCompilador(nombreModulo string) *Compilador {
	if nombreModulo == "" {
		nombreModulo = "wini_module"
	}
	return &Compilador{
		NombreModulo: nombreModulo,
		Fabrica:      func(nombre string) GeneradorCodigo {
			return NewCodegen(nombre)
		},
	}
}

func (c *Compilador) generador() GeneradorCodigo {
	if c.Fabrica == nil {
		return NewCodegen(c.NombreModulo)
	}
	return c.Fabrica(c.NombreModulo)
}

// Compilar analiza, verifica y genera código para una fuente sin contexto de
// archivo. Las importaciones se resuelven relativas al directorio actual.
func (c *Compilador) Compilar(source string) (string, error) {
	raiz, _, err := ParsearYVerificar(source)
	if err != nil {
		return "", err
	}
	return c.generador().Generate(raiz)
}

// CompilarArchivo analiza, verifica y genera código para un archivo fuente.
// También devuelve las bibliotecas estáticas detectadas en sus módulos.
func (c *Compilador) CompilarArchivo(source string, rutaEntrada string) (string, []string, error) {
	raiz, libs, err := ParsearYVerificarArchivo(
		source,
		filepath.Dir(rutaEntrada),
		rutaEntrada,
	)
	if err != nil {
		return "", nil, err
	}
	llvm, err := c.generador().Generate(raiz)
	if err != nil {
		return "", nil, err
	}
	return llvm, libs, nil
}
