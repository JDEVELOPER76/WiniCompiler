/* =====================================================================
 * matematicas.c — funciones matemáticas para Wini
 * ---------------------------------------------------------------------
 * Implementado en C sobre <math.h>. Ver matematicas.h para el comando
 * de compilación y la nota sobre -lm en Linux/Unix.
 * ===================================================================== */

#define _POSIX_C_SOURCE 200809L
#define _ISOC99_SOURCE

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "wini_bridge.h"
#include "matematicas.h"

/* ---- Trigonométrica (radianes) ---- */

WINI_DECIMAL mat_seno(WINI_DECIMAL x) {
    return sin(x);
}

WINI_DECIMAL mat_coseno(WINI_DECIMAL x) {
    return cos(x);
}

WINI_DECIMAL mat_tangente(WINI_DECIMAL x) {
    return tan(x);
}

WINI_DECIMAL mat_arco_seno(WINI_DECIMAL x) {
    if (x < -1.0 || x > 1.0) return NAN;
    return asin(x);
}

WINI_DECIMAL mat_arco_coseno(WINI_DECIMAL x) {
    if (x < -1.0 || x > 1.0) return NAN;
    return acos(x);
}

WINI_DECIMAL mat_arco_tangente(WINI_DECIMAL x) {
    return atan(x);
}

/* ---- Potencia, raíz, exponencial, logaritmo ---- */

WINI_DECIMAL mat_potencia(WINI_DECIMAL base, WINI_DECIMAL exp) {
    return pow(base, exp);
}

WINI_DECIMAL mat_raiz_cuadrada(WINI_DECIMAL x) {
    if (x < 0.0) return NAN;
    return sqrt(x);
}

WINI_DECIMAL mat_exponencial(WINI_DECIMAL x) {
    return exp(x);
}

WINI_DECIMAL mat_logaritmo(WINI_DECIMAL x) {
    if (x <= 0.0) return NAN;
    return log(x);
}

WINI_DECIMAL mat_logaritmo10(WINI_DECIMAL x) {
    if (x <= 0.0) return NAN;
    return log10(x);
}

/* ---- Redondeo ---- */

WINI_DECIMAL mat_redondear(WINI_DECIMAL x) {
    return round(x);
}

WINI_DECIMAL mat_techo(WINI_DECIMAL x) {
    return ceil(x);
}

WINI_DECIMAL mat_piso(WINI_DECIMAL x) {
    return floor(x);
}

WINI_DECIMAL mat_abs_decimal(WINI_DECIMAL x) {
    return fabs(x);
}

/* ---- Enteros ---- */

WINI_ENTERO mat_abs_entero(WINI_ENTERO x) {
    return x < 0 ? -x : x;
}

WINI_ENTERO mat_maximo_entero(WINI_ENTERO a, WINI_ENTERO b) {
    return a > b ? a : b;
}

WINI_ENTERO mat_minimo_entero(WINI_ENTERO a, WINI_ENTERO b) {
    return a < b ? a : b;
}

/* ---- Versión ---- */

WINI_CADENA mat_version(void) {
    return wini_bridge_str("matematicas 1.0 (C + math.h)");
}
