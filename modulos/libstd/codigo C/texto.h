#ifndef TEXTO_H
#define TEXTO_H

#include "wini_bridge.h"

/* =====================================================================
 * texto.h — utilidades de cadena (stdlib unificada: std + texto) para Wini
 * ---------------------------------------------------------------------
 * Implementado en C para ser rápido en cadenas largas y listas grandes.
 * Ver "texto.c" para el detalle de cada contrato.
 *
 * Contratos comunes (igual que el resto de módulos C de Wini):
 * - Los argumentos WINI_CADENA se CONSUMEN siempre: el llamador ya retuvo
 *   una referencia por si la sigue usando; la función C libera la suya.
 * - Lo devuelto es NUEVO (rc=1, dueño = quien llama).
 * - WINI_LISTA de entrada se consume también (se libera una referencia);
 *   los elementos que devuelve en una lista nueva ya están retenidos.
 * ===================================================================== */

/* Pasa a minúsculas solo las letras A-Z; lo demás queda igual. */
WINI_CADENA texto_minusculas(WINI_CADENA s);

/* Pasa a mayúsculas solo las letras a-z; lo demás queda igual. */
WINI_CADENA texto_mayusculas(WINI_CADENA s);

/* Une 'lista<cadena>' con 'sep' entre elementos. Consume 'lista' y 'sep'. */
WINI_CADENA texto_unir(WINI_LISTA lista, WINI_CADENA sep);

/* Parte 's' por cada ocurrencia de 'sep'. Sep vacío -> lista con 's' entera. */
WINI_LISTA  texto_partir(WINI_CADENA s, WINI_CADENA sep);

/* Reemplaza TODAS las ocurrencias de 'viejo' por 'nuevo'. */
WINI_CADENA texto_reemplazar(WINI_CADENA s, WINI_CADENA viejo, WINI_CADENA nuevo);

/* Verdadero si 'sub' aparece en 's'. */
WINI_BOOLEANO texto_contiene(WINI_CADENA s, WINI_CADENA sub);

/* Cuenta cuántas veces aparece 'sub' (no solapadas). Sub vacío -> 0. */
WINI_ENTERO   texto_contar(WINI_CADENA s, WINI_CADENA sub);

/* Quita espacios de ambos extremos. */
WINI_CADENA texto_recortar(WINI_CADENA s);

/* Invierte el orden de los caracteres. */
WINI_CADENA texto_invertir(WINI_CADENA s);

/* Primera letra en mayúscula, el resto en minúscula. "" -> "". */
WINI_CADENA texto_capitalizar(WINI_CADENA s);

/* Parsea un entero (signo y espacios opcionales). No-numérico -> 0. */
WINI_ENTERO   texto_a_entero(WINI_CADENA s);

/* Parsea un decimal (punto o coma). No-numérico -> 0.0. */
WINI_DECIMAL  texto_a_decimal(WINI_CADENA s);

/* Versión del módulo. */
WINI_CADENA   texto_version(void);

#endif /* TEXTO_H */
