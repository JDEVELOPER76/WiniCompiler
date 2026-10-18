#ifndef ORDEN_H
#define ORDEN_H

#include "wini_bridge.h"

/* =====================================================================
 * orden.h — ordenamiento y búsqueda binaria para Wini (en C)
 * ---------------------------------------------------------------------
 * Quicksort rápido sobre listas. Devuelve listas NUEVAS; las de entrada
 * no se tocan.
 *
 *   importar "orden/orden" como ord
 *   lista<entero> l = ord.enteros([3, 1, 2])        # [1, 2, 3]
 *   lista<cadena> c = ord.cadenas(["b", "a"])       # ["a", "b"]
 *   entero i = ord.busqueda_binaria_enteros([1,2,3], 2)   # índice o -1
 *
 * Contratos (igual que el resto de módulos C):
 * - WINI_LISTA de entrada se consume; lo devuelto es NUEVO.
 * ===================================================================== */

/* Ordena lista<entero> ascendente. Devuelve lista nueva. */
WINI_LISTA  ord_enteros(WINI_LISTA l);
/* Ordena lista<entero> descendente. Devuelve lista nueva. */
WINI_LISTA  ord_enteros_desc(WINI_LISTA l);
/* Ordena lista<decimal> ascendente. Devuelve lista nueva. */
WINI_LISTA  ord_decimales(WINI_LISTA l);
/* Ordena lista<decimal> descendente. Devuelve lista nueva. */
WINI_LISTA  ord_decimales_desc(WINI_LISTA l);
/* Ordena lista<cadena> ascendente (lexicográfico). Devuelve lista nueva. */
WINI_LISTA  ord_cadenas(WINI_LISTA l);
/* Ordena lista<cadena> descendente (lexicográfico). Devuelve lista nueva. */
WINI_LISTA  ord_cadenas_desc(WINI_LISTA l);

/* Búsqueda binaria sobre lista<entero> ordenada ascendente. Devuelve el
 * índice o -1 si no está. 'l' se consume. */
WINI_ENTERO ord_busqueda_binaria_enteros(WINI_LISTA l, WINI_ENTERO x);
/* Búsqueda binaria sobre lista<cadena> ordenada ascendente. Devuelve el
 * índice o -1 si no está. 'l' y 'x' se consumen. */
WINI_ENTERO ord_busqueda_binaria_cadenas(WINI_LISTA l, WINI_CADENA x);

/* Versión del módulo. */
WINI_CADENA ord_version(void);

#endif /* ORDEN_H */
