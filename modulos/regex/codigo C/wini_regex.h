#ifndef WINI_REGEX_H
#define WINI_REGEX_H

#include "wini_bridge.h"

/* ---- Compilación ----
 *
 * Contratos comunes: los argumentos cadena se CONSUMEN siempre (ver
 * wini_bridge.h); lo devuelto es NUEVO (rc=1, dueño = quien llama).
 * Los patrones y textos con NUL embebido se cortan ahí (la API C de
 * regex trabaja con NUL-terminado).
 *
 * Los patrones compilados viven en una tabla interna y se nombran por
 * id (entero >= 0): nunca viaja un puntero a Wini. Compilar dos veces
 * el mismo patrón da dos ids distintos (cada uno se libera aparte).
 */

/* Compila 'patron' (sintaxis EXTENDED de POSIX). Devuelve el id (>= 0)
 * o -1 si no compiló (ver regex_ultimo_error()). */
WINI_ENTERO    regex_compilarx(WINI_CADENA patron);
/* Libera el id (doble liberar: falso). */
WINI_BOOLEANO  regex_liberarx(WINI_ENTERO id);
/* Texto del último error de compilación (cadena nueva). "" si no hubo. */
WINI_CADENA    regex_ultimo_errorx(void);

/* ---- Búsqueda ----
 * 'id' inválido o liberado: falso / "" / lista vacía (no lanzan).
 * 'texto' se consume siempre.
 */

/* Verdadero si el patrón aparece en alguna parte del texto. */
WINI_BOOLEANO  regex_coincidex(WINI_ENTERO id, WINI_CADENA texto);
/* Verdadero si el patrón coincide con TODO el texto (match completo,
 * equivalente a rodear el patrón con ^...$). 'texto' se consume. */
WINI_BOOLEANO  regex_match_completox(WINI_ENTERO id, WINI_CADENA texto);
/* El trozo que matcheó (grupo 0) o "" si no hay match. */
WINI_CADENA    regex_extraerx(WINI_ENTERO id, WINI_CADENA texto);
/* Grupos 0..9 como lista<cadena> NUEVA (rc=1): los que no participaron
 * vienen como "". Lista vacía si no hay match. */
WINI_LISTA     regex_capturasx(WINI_ENTERO id, WINI_CADENA texto);
/* Reemplaza el PRIMER match por 'reemplazo', donde \1..\9 es el grupo N
 * (fuera de rango o no participante: "") y \\ es barra literal. Sin
 * match devuelve una copia del texto. 'reemplazo' se consume. */
WINI_CADENA    regex_reemplazarx(WINI_ENTERO id, WINI_CADENA texto, WINI_CADENA reemplazo);
/* Como reemplazarx pero sobre TODOS los matches no vacíos (los vacíos
 * se saltean avanzando 1 byte: nunca cuelga). */
WINI_CADENA    regex_reemplazar_todox(WINI_ENTERO id, WINI_CADENA texto, WINI_CADENA reemplazo);
/* Parte por cada match no vacío; los trozos vacíos se omiten (igual que
 * cadena2lista() de libstd). Lista NUEVA (rc=1). */
WINI_LISTA     regex_dividirx(WINI_ENTERO id, WINI_CADENA texto);
/* Cuenta los matches no vacíos. */
WINI_ENTERO    regex_contarx(WINI_ENTERO id, WINI_CADENA texto);
/* Escapa un literal para incrustarlo en un patrón. */
WINI_CADENA    regex_escaparx(WINI_CADENA texto);
/* Reemplaza TODOS los matches no vacíos (misma sintaxis de reemplazo).
 * Los matches vacíos se saltean (avanza 1 byte): nunca cuelga ni mete
 * inserciones fantasma estilo sed. Sin match devuelve copia del texto. */
WINI_CADENA    regex_reemplazar_todox(WINI_ENTERO id, WINI_CADENA texto, WINI_CADENA reemplazo);
/* Parte el texto por cada match no vacío (los vacíos se saltean, igual
 * que cadena2lista() omite huecos: nunca hay trozos vacíos por matches
 * de largo cero, solo los reales de los bordes). Lista NUEVA (rc=1). */
WINI_LISTA     regex_dividirx(WINI_ENTERO id, WINI_CADENA texto);
/* Cuenta los matches no vacíos. */
WINI_ENTERO    regex_contarx(WINI_ENTERO id, WINI_CADENA texto);
/* Escapa un literal para usarlo dentro de un patrón: antepone \ a
 * .^$*+?()[]{}|\ . "a.c" matchea literal "a.c", no "aXc". */
WINI_CADENA    regex_escaparx(WINI_CADENA texto);

/* ---- Información de versión ---- */
WINI_CADENA    include__version_regex(void);

#endif /* WINI_REGEX_H */
