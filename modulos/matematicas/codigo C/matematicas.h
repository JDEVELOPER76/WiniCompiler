#ifndef MATEMATICAS_H
#define MATEMATICAS_H

#include "wini_bridge.h"

/* =====================================================================
 * matematicas.h — funciones matemáticas para Wini
 * ---------------------------------------------------------------------
 * Implementado en C sobre <math.h> para ser rápido y preciso.
 * Compilar:
 *
 *   cc -O2 -fPIC -Wall -Wextra -Iruntime -I"modulos/matematicas/codigo C" \
 *       -c "modulos/matematicas/codigo C/matematicas.c" -o /tmp/matematicas.o
 *   ar rcs modulos/matematicas/libmatematicas.a /tmp/matematicas.o
 *
 * En Linux/Unix el enlazador necesita -lm (winic lo agrega automáticamente
 * al enlazar; ver main.go).
 *
 * Contratos: WINI_DECIMAL de entrada se lee (no consume: es por valor);
 * lo devuelto es NUEVO cuando es cadena. No hay cadenas de entrada en
 * este módulo, solo decimales y enteros.
 * ===================================================================== */

/* Trigonométrica (radianes). */
WINI_DECIMAL mat_seno(WINI_DECIMAL x);
WINI_DECIMAL mat_coseno(WINI_DECIMAL x);
WINI_DECIMAL mat_tangente(WINI_DECIMAL x);
WINI_DECIMAL mat_arco_seno(WINI_DECIMAL x);   /* devuelve NaN si |x| > 1 */
WINI_DECIMAL mat_arco_coseno(WINI_DECIMAL x); /* devuelve NaN si |x| > 1 */
WINI_DECIMAL mat_arco_tangente(WINI_DECIMAL x);

/* Potencia, raíz, exponencial, logaritmo. */
WINI_DECIMAL mat_potencia(WINI_DECIMAL base, WINI_DECIMAL exp);
WINI_DECIMAL mat_raiz_cuadrada(WINI_DECIMAL x);   /* NaN si x < 0 */
WINI_DECIMAL mat_exponencial(WINI_DECIMAL x);    /* e^x */
WINI_DECIMAL mat_logaritmo(WINI_DECIMAL x);      /* ln(x), NaN si x <= 0 */
WINI_DECIMAL mat_logaritmo10(WINI_DECIMAL x);    /* log10(x), NaN si x <= 0 */

/* Redondeo. */
WINI_DECIMAL mat_redondear(WINI_DECIMAL x);   /* al entero más cercano */
WINI_DECIMAL mat_techo(WINI_DECIMAL x);        /* ceil */
WINI_DECIMAL mat_piso(WINI_DECIMAL x);         /* floor */
WINI_DECIMAL mat_abs_decimal(WINI_DECIMAL x);  /* |x| */

/* Enteros. */
WINI_ENTERO  mat_abs_entero(WINI_ENTERO x);
WINI_ENTERO  mat_maximo_entero(WINI_ENTERO a, WINI_ENTERO b);
WINI_ENTERO  mat_minimo_entero(WINI_ENTERO a, WINI_ENTERO b);

/* Versión del módulo. */
WINI_CADENA  mat_version(void);

#endif /* MATEMATICAS_H */
