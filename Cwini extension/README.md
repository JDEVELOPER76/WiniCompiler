# Cwin Language Support (Wini)

Extensión de VS Code para el lenguaje **Cwin/Wini**: resaltado **100 % a color** (nada en blanco), **documentación al hacer hover** de las funciones integradas y de los comentarios encima de cada función, **sistema de módulos mejorado** con detección silenciosa del compilador (`where cwini` / `which cwini`) e **iconos propios** para los archivos `.cwn` y `.wini`.

## Instalación

1. Abre VS Code → paleta de comandos (`Ctrl+Shift+P`) → **Extensions: Install from VSIX…**
2. Elige `cwin-language-1.0.0.vsix`.
3. (Recomendado) Acepta el aviso de activar el tema **Cwin Dark**, o ejecuta **Cwin: Activar tema Cwin Dark**.

No requiere Node.js ni dependencias: es JavaScript puro, sin paso de compilación.

## Qué incluye

### Resaltado sincronizado con el compilador (tokens.go)

- Palabras clave reales del lenguaje: `si sino demas mientras para romper continuar retornar intentar capturar finalmente lanzar relanzar como` y declaraciones `funcion externa tipo importar`.
- **Palabras nuevas añadidas:** `estructura` y `vacio`.
- **Eliminadas** las que el compilador no usa (el borrador viejo tenía `fin`, `clase`…).
- Tipos: `entero decimal cadena booleano lista diccionario` + alias en inglés `int float str bool list dict`.
- Constantes `verdadero falso nulo`, operadores lógicos `y o no en`, comparadores `== != <> <= >= < >`, asignación `= += -=`, flecha `->`, aritmética `+ - * / %`.
- Comentarios `#` y docstrings `#@`, cadenas `"..."`, `'...'`, triples `"""..."""`, e interpoladas `c"...{expr}..."` con la expresión coloreada dentro.
- **Todo token tiene color**: identificadores, llamadas, paréntesis, llaves, corchetes, comas, dos puntos y puntos. Para que incluso la puntuación se coloree con tu tema actual, usa el tema incluido **Cwin Dark** (con cualquier otro tema el resto de tokens también se colorea).

### Documentación al hacer hover

- **Funciones integradas** (extraídas del compilador): `escribir`, `leer`, `tipo`, `claves`, `valores`, `rango`, `liberar` y las conversiones `entero/decimal/cadena/booleano` (+ alias `int/float/str/bool/dict/list`), con firma, parámetros y ejemplos.
- **Funciones del usuario**: al hacer hover se muestra la firma, los parámetros y, como documentación, **los comentarios `#` escritos encima de la declaración** y el docstring `#@` del cuerpo (el compilador ya los consume como documentación). Busca en el archivo actual, en los módulos importados y en el resto del proyecto.
- **Palabras clave y módulos**: cada palabra clave tiene su explicación; los nombres en `importar` muestran la ruta del módulo y las funciones que exporta.
- `estructura`: muestra el nombre, los campos y su documentación.

### Sistema de módulos mejorado

1. Al activarse, la extensión localiza el compilador **en silencio** ejecutando `where cwini` (Windows) o `which cwini` (Linux/macOS) — sin abrir terminal y sin mostrar salida; si no está, prueba `winic`.
2. Busca módulos `.cwn` / `.wn` / `.wini` en: la carpeta del archivo actual, el proyecto y sus carpetas `modulos/`, la carpeta del compilador detectado (+ `modulos/`) y las carpetas extra configuradas.
3. Con eso ofrece: autocompletado de nombres en `importar`, hover y salto a definición de cada módulo, autocompletado de las funciones exportadas, y un **aviso amarillo** cuando un `importar` no se puede resolver.
4. Resolución igual que el compilador: `importar utilidades.texto` → `utilidades/texto.*`; con comillas, ruta explícita.

### Iconos y extras

- Icono propio para **`.cwn`** (morado, "C") y para **`.wini`** (verde azulado, "w") en el explorador.
- Configuración de indentación, plegado y auto-cierre acorde a los bloques con `:` (sin `fin`).
- Barra de estado con el compilador detectado y el número de módulos (clic = lista de módulos).
- Proveedor de símbolos (outline) y "Ir a definición" de funciones, estructuras y módulos.

## Comandos

| Comando | Descripción |
|---|---|
| `Cwin: Buscar compilador (where/which cwini)` | Ejecuta de nuevo la detección silenciosa y muestra la ruta |
| `Cwin: Listar módulos detectados` | Lista los módulos con su ruta y funciones |
| `Cwin: Reescanear módulos` | Limpia cachés y vuelve a escanear |
| `Cwin: Activar tema Cwin Dark` | Activa el tema incluido con todo coloreado |

## Ajustes

| Ajuste | Por defecto | Descripción |
|---|---|---|
| `cwin.compilador.ruta` | `""` | Ruta del ejecutable; vacío = detectar con `where/which cwini` |
| `cwin.modulos.carpetasExtra` | `[]` | Carpetas adicionales donde buscar módulos |
| `cwin.modulos.buscarJuntoAlCompilador` | `true` | Buscar módulos junto al compilador detectado |
| `cwin.diagnosticos.importaciones` | `true` | Avisar `importar` que no se resuelvan |

## Probar rápido

Abre la carpeta `ejemplos/` de la extensión: `demo.cwn` importa `saludos` y `math_extra` (carpeta `modulos/`), con funciones documentadas por comentarios, `estructura`, `vacio`, cadenas interpoladas y `externa`.

## Estructura del proyecto

```
cwin-vscode/
├── package.json                  # manifiesto y contribuciones
├── language-configuration.json   # indentación/comentarios reales del lenguaje
├── syntaxes/cwin.tmLanguage.json # gramática .cwn/.wn
├── syntaxes/wini.tmLanguage.json # gramática .wini
├── themes/cwin-dark.json         # tema con TODO coloreado
├── src/extension.js              # hover, completado, definición, símbolos, diagnósticos
├── src/builtins.js               # documentación de las integradas (del compilador)
├── src/analisis.js               # analizador ligero de documentos
├── src/modulos.js                # where/which cwini silencioso + búsqueda de módulos
└── icons/                        # iconos de .cwn, .wini y de la extensión
```
