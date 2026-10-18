#ifndef AZAR_H
#define AZAR_H

#include "wini_bridge.h"

/* =====================================================================
 * azar.h — generador pseudoaleatorio determinista (LCG) para Wini
 * ---------------------------------------------------------------------
 * Implementado en C para ser rápido en generación masiva de números
 * aleatorios. Mismo algoritmo en todas las plataformas (LCG de 31 bits,
 * constante de MINSTD): determinista y reproducible. NO sirve para
 * criptografía.
 *
 * El "estado" viaja en una lista<entero> de un elemento que las funciones
 * MUTAN en el lugar (igual que la versión Wini pura):
 *
 *   lista<entero> estado = azar.semilla(42)
 *   entero a = azar.rango(estado, 1, 6)
 *
 * Contratos comunes (igual que el resto de módulos C de Wini):
 * - Los argumentos WINI_CADENA de entrada se CONSUMEN siempre.
 * - WINI_LISTA de entrada se consume (se libera una referencia); si se
 *   muta (estado), se modifica en el lugar y se devuelve el mismo puntero.
 * - Lo devuelto es NUEVO (rc=1, dueño = quien llama).
 * ===================================================================== */

/* Crea el estado a partir de una semilla (entero). Devuelve lista<entero>
 * de un elemento (el estado). 'inicial' se normaliza al rango del LCG. */
WINI_LISTA  azar_semilla(WINI_ENTERO inicial);

/* Próximo valor crudo del generador (1 .. 2147483646). Muta 'estado' en
 * el lugar y devuelve el mismo puntero de lista. 'estado' se consume. */
WINI_LISTA  azar_siguiente(WINI_LISTA estado);

/* Entero en [minimo, maximo] (ambos incluidos). Muta 'estado'. Lanza
 * ValueError si maximo < minimo (vía wini_lanzar). 'estado' se consume. */
WINI_ENTERO azar_rango(WINI_LISTA estado, WINI_ENTERO minimo, WINI_ENTERO maximo);

/* Decimal en [0.0, 1.0). Muta 'estado'. */
WINI_DECIMAL azar_decimal(WINI_LISTA estado);

/* Booleano al azar. Muta 'estado'. */
WINI_BOOLEANO azar_booleano(WINI_LISTA estado);

/* Elemento al azar de 'l' (lista<cadena>). Muta 'estado'. Lista vacía
 * lanza ValueError. Devuelve cadena NUEVA (retención propia). 'l' se
 * consume (se libera una referencia). */
WINI_CADENA azar_elegir(WINI_LISTA estado, WINI_LISTA l);

/* Fisher-Yates: devuelve una lista<cadena> NUEVA barajada. La original
 * no se toca. Muta 'estado'. 'l' se consume. */
WINI_LISTA  azar_barajar(WINI_LISTA estado, WINI_LISTA l);

/* Cadena aleatoria de 'n' caracteres del 'alfabeto' dado. Muta 'estado'.
 * 'alfabeto' se consume. */
WINI_CADENA azar_cadena(WINI_LISTA estado, WINI_ENTERO n, WINI_CADENA alfabeto);

/* Versión del módulo. */
WINI_CADENA azar_version(void);

#endif /* AZAR_H */
