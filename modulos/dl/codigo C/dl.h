#ifndef DL_H
#define DL_H

#include "wini_bridge.h"

/* =====================================================================
 * dl.h — carga dinámica de librerías (.so en Linux/Unix, .dll en Windows)
 * ---------------------------------------------------------------------
 * Permite abrir una librería compartida, buscar símbolos y llamar a
 * funciones expuestas por ella. Implementado en C sobre dlopen/dlsym
 * (POSIX) o LoadLibrary/GetProcAddress (Windows).
 *
 * Contratos comunes (igual que el resto de módulos C de Wini):
 * - Los WINI_CADENA de entrada se CONSUMEN siempre; lo devuelto es NUEVO.
 * - El handle devuelto por dl_open es un entero (>= 1). 0 = inválido /
 *   error. Se pueden tener varias abiertas a la vez (hasta DL_MAX_LIBS).
 * ===================================================================== */

/* Máximo de librerías abiertas simultáneamente. */
#define DL_MAX_LIBS 32

/* Abre 'ruta' (.so o .dll). Devuelve un handle (>= 1) o 0 si falló.
 * Tras un fallo, dl_error() devuelve el motivo. */
WINI_ENTERO dl_open(WINI_CADENA ruta);

/* Cierra la librería 'handle'. Devuelve verdadero si se cerró. */
WINI_BOOLEANO dl_close(WINI_ENTERO handle);

/* Busca el símbolo 'nombre' en 'handle' y devuelve su dirección como
 * entero (para usar con dl_call_*), o 0 si no se encontró. Tras fallo,
 * dl_error() devuelve el motivo. */
WINI_ENTERO dl_sym(WINI_ENTERO handle, WINI_CADENA nombre);

/* Devuelve el texto del último error de dl_open / dl_sym / dl_close.
 * "" si no hubo error desde la última llamada exitosa. */
WINI_CADENA dl_error(void);

/* Llama a una función int f(int, int) expuesta por la librería.
 * 'handle' es el dl_open; 'nombre' es el símbolo. Devuelve el resultado
 * o -1 si no se pudo abrir/encuentre/llamar. Tras fallo, dl_error(). */
WINI_ENTERO dl_call_entero(WINI_ENTERO handle, WINI_CADENA nombre,
                           WINI_ENTERO a, WINI_ENTERO b);

/* Llama a una función char * f(char *) expuesta por la librería.
 * 's' se consume. Devuelve una cadena NUEVA o "" si falló. */
WINI_CADENA dl_call_cadena(WINI_ENTERO handle, WINI_CADENA nombre,
                           WINI_CADENA s);

/* Versión del módulo. */
WINI_CADENA dl_version(void);

#endif /* DL_H */
