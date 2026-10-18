# Guía del módulo `regex` para Wini

Expresiones regulares POSIX EXTENDED (como `grep -E`) en Wini.
Se importa así (la ruta es relativa a tu `.wn`, igual que `system/`):

```wini
importar "regex/regex" como re
```

Los ejemplos de abajo corren tal cual en una función principal; debajo de cada bloque va su salida real.

## 1. Sintaxis soportada (resumen ERE)

| Patrón | Significado |
|---|---|
| `a+` `a*` `a?` `a{2,4}` | repetición |
| `(ab)` | grupo (cuentan desde 1; el 0 es todo el match) |
| `a\|b` | alternativa |
| `[abc]` `[^abc]` `[0-9]` | clase / negada / rango |
| `.` | cualquiera salvo salto de línea |
| `^` `$` | ancla inicio / fin de texto |
| `\1`–`\9` | grupo N (solo en reemplazo), `\\` barra literal |

OJO: `[..]` y `.` no cruzan NUL embebido (la API C corta ahí).

## 2. Dos niveles: one-shot vs manual

One-shot (compila, usa y libera en una llamada). Para usos aislados:

```wini
re.coincide("[0-9]+", "abc123")        # verdadero
re.extraer("[0-9]+", "abc123def456")   # "123"
```

Manual (compila UNA vez y reusa el id). En un loop rinde más porque compilar el patrón es lo caro:

```wini
entero id = re.compilar("[0-9]+")
para linea en lineas:
    si re.coincide_en(id, linea):
        escribir(re.extraer_en(id, linea))
re.liberar_patron(id)   # siempre liberar al terminar
```

Si el patrón no compila: `compilar()` da -1 y `error()` trae el mensaje (`Unmatched ( or \(` y similares). Los one-shot devuelven el valor neutro en ese caso (falso, `""`, `[]`, copia).

## 3. Referencia con ejemplos

**`coincide(patron, texto) -> booleano`**
Verdadero si aparece en CUALQUIER parte (usar `^...$` para todo).
```wini
re.coincide("[0-9]+", "abc123")      # verdadero
re.coincide("^[0-9]+$", "abc123")    # falso
```

**`extraer(patron, texto) -> cadena`**
El trozo del grupo 0, o `""` sin match.
```wini
re.extraer("[0-9]+", "abc123def456") # "123"
```

**`capturas(patron, texto) -> lista<cadena>`**
Grupos 0..9 (siempre 10 elementos); el que no participó viene `""`. Lista vacía si no hay match. NO olvidar liberar la lista.
```wini
re.capturas("([0-9]+)-([a-z]+)", "abc123-def")
# ["123-def", "123", "def", "", "", "", "", "", "", ""]
```

**`reemplazar(patron, texto, reemplazo) -> cadena`**
Solo el PRIMER match. `\N` es el grupo N (vacío si no participó o no existe), `\\` es barra literal. Sin match: copia del texto.
```wini
re.reemplazar("[0-9]+", "abc123def", "<N>")  # "abc<N>def"
re.reemplazar("(a)(b)?", "xa", "[\1|\2]")   # "x[a|]"
```

**`reemplazar_todo(patron, texto, reemplazo) -> cadena`**
Como `reemplazar` pero sobre TODOS los matches no vacíos.
```wini
re.reemplazar_todo("[0-9]+", "a1b22c333", "#")  # "a#b#c#"
```

**`dividir(patron, texto) -> lista<cadena>`**
Parte por cada match no vacío; los trozos vacíos se OMITEN (igual que `cadena2lista()` de libstd: nunca hay `""` por huecos). NO olvidar liberar.
```wini
re.dividir(",", "a,b,,c")      # ["a", "b", "c"]
re.dividir(" +", "hola   x")   # ["hola", "x"]
```

**`contar(patron, texto) -> entero`**
Cantidad de matches no vacíos.
```wini
re.contar("[0-9]+", "a1b22c333")  # 3
```

**`escapar(texto) -> cadena`**
Antepone `\` a `.^$*+?()[]{}|\` para incrustar un literal en un patrón. Imprescindible con entrada del usuario:
```wini
re.escapar("a.c (x)")                  # "a\.c \(x\)"
re.coincide(re.escapar("a.c"), "a.c")  # verdadero
re.coincide(re.escapar("a.c"), "aXc")  # falso
```

## 4. Propiedad de memoria (refcount manual)

- Todo lo que devuelven estas funciones es NUEVO y tuyo: liberalo (cadenas y listas) cuando termines, como cualquier valor Wini.
- Los id de `compilar()` se liberan con `liberar_patron(id)`. Doble liberar devuelve falso y es inocuo; id inválido en uso devuelve el valor neutro sin romper nada.
- Los argumentos se consumen al llamar (igual que el resto del runtime): si pasás una variable y la seguís usando, el mecanismo de llamada ya la retuvo por vos; no hagas nada especial.

## 5. Trampas conocidas

- Matches VACÍOS (p. ej. `a*` sobre `"b"`): `dividir`/`contar`/`reemplazar_todo` los SALTEAN (avanzan 1 byte). Nunca cuelgan ni inventan inserciones estilo sed; documentado y testeado así a propósito.
- Máximo 9 grupos (0..9 en `capturas`; `\1`..`\9` en reemplazo).
- Textos o patrones con NUL embebido se cortan ahí.
- En MSVC no-MinGW no hay `<regex.h>`: todo devuelve el valor neutro (falso/`""`/`[]`/-1) en vez de romper la compilación del módulo.

## 6. Mini-ejemplo: lexer de juguete (relevante para self-hosting)

```wini
funcion principal(): entero:
    entero id_num = re.compilar("^[0-9]+")
    entero id_id = re.compilar("^[a-zA-Z_][a-zA-Z0-9_]*")
    lista<cadena> toks = ["123", "hola", "+", "45x"]
    para t en toks:
        si re.coincide_en(id_num, t):
            escribir(c"numero: {t}")
        sino si re.coincide_en(id_id, t):
            escribir(c"ident: {t}")
        sino:
            escribir(c"otro: {t}")
    re.liberar_patron(id_num)
    re.liberar_patron(id_id)
    liberar(toks)
    retornar 0
```

Salida:

```
numero: 123
ident: hola
otro: +
numero: 45x
```

OJO la última línea: `^[0-9]+` ancla al inicio pero no al fin, así que `"45x"` matchea por el prefijo. Con `^[0-9]+$` daría `otro: 45x`.
