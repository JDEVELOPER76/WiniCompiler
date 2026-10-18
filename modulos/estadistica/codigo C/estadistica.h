#ifndef ESTADISTICA_H
#define ESTADISTICA_H

#include "wini_bridge.h"

/* =====================================================================
 * estadistica.h — estadística descriptiva para Wini (en C)
 * ---------------------------------------------------------------------
 * Cálculos rápidos sobre listas. Lista vacía -> 0.0 (o 0 para enteros),
 * salvo donde se documente otro valor.
 *
 *   importar "estadistica/estadistica" como est
 *   decimal m = est.media([1.0, 2.0, 3.0])             # 2.0
 *   decimal d = est.desviacion([2,4,4,4,5,5,7,9])      # 2.0
 *   entero m2 = est.minimo([5, 2, 9, 1])               # 1
 *
 * Contratos: WINI_LISTA de entrada se consume; lo devuelto es NUEVO.
 * ===================================================================== */

/* Suma de lista<decimal>. Vacía -> 0.0. */
WINI_DECIMAL est_suma(WINI_LISTA l);
/* Suma de lista<entero>. Vacía -> 0. */
WINI_ENTERO  est_suma_enteros(WINI_LISTA l);

/* Media aritmética de lista<decimal>. Vacía -> 0.0. */
WINI_DECIMAL est_media(WINI_LISTA l);
/* Media aritmética de lista<entero>. Vacía -> 0.0. */
WINI_DECIMAL est_media_enteros(WINI_LISTA l);

/* Mínimo de lista<decimal>. Vacía -> 0.0. */
WINI_DECIMAL est_min(WINI_LISTA l);
/* Máximo de lista<decimal>. Vacía -> 0.0. */
WINI_DECIMAL est_max(WINI_LISTA l);
/* Mínimo de lista<entero>. Vacía -> 0. */
WINI_ENTERO  est_min_entero(WINI_LISTA l);
/* Máximo de lista<entero>. Vacía -> 0. */
WINI_ENTERO  est_max_entero(WINI_LISTA l);

/* Varianza poblacional de lista<decimal>. Vacía -> 0.0. */
WINI_DECIMAL est_varianza(WINI_LISTA l);
/* Desviación estándar poblacional de lista<decimal>. Vacía -> 0.0. */
WINI_DECIMAL est_desviacion(WINI_LISTA l);

/* Mediana de lista<decimal> (valor medio, o promedio de los dos centrales).
 * Vacía -> 0.0. */
WINI_DECIMAL est_mediana(WINI_LISTA l);

/* Versión del módulo. */
WINI_CADENA  est_version(void);

#endif /* ESTADISTICA_H */
