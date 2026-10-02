/*
 * wini_bridge.h — puente C <-> Wini
 * =================================
 *
 * Capa opcional que facilita escribir código C que habla con Wini en
 * cualquiera de las dos direcciones:
 *
 *   1) C -> Wini : un programa host en C que llama funciones compiladas
 *      desde un archivo .wini (vía `winic archivo.wini -shared -o lib.so`).
 *   2) Wini -> C : la implementación en C de una función declarada
 *      `externa` en Wini.
 *
 * No cambia nada del ABI que ya genera codegen.go: solo envuelve
 * runtime.h con nombres más cómodos y agrega utilidades (append, lectura
 * tipada, captura de excepciones) que el runtime crudo no ofrece.
 *
 * Requiere: runtime.h y enlazar contra runtime.o (o la .so que ya lo
 * incluya) + wini_bridge.o (este archivo compilado).
 */

#ifndef WINI_BRIDGE_H
#define WINI_BRIDGE_H

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "runtime.h"

#ifdef __cplusplus
extern "C" {
#endif

/* -------------------------------------------------------------------
 * Alias de tipos Wini a su tipo C real
 * ------------------------------------------------------------------- */
typedef int64_t     WINI_ENTERO;
typedef double      WINI_DECIMAL;
typedef _Bool       WINI_BOOLEANO;
typedef WiniString *WINI_CADENA;
typedef WiniList   *WINI_LISTA;
typedef WiniDict   *WINI_DICCIONARIO;

typedef enum {
    WINI_TIPO_ENTERO   = 0,
    WINI_TIPO_DECIMAL  = 1,
    WINI_TIPO_BOOLEANO = 2,
    WINI_TIPO_CADENA   = 3,
} wini_tipo_elem;

/* -------------------------------------------------------------------
 * Cadenas
 * ------------------------------------------------------------------- */

/* Crea una WiniString* NUEVA (rc=1, dueño = quien llama) copiando 'cstr'
 * (terminado en NUL). Si 'cstr' es NULL, se usa "". */
static inline WINI_CADENA wini_bridge_str(const char *cstr) {
    if (!cstr) cstr = "";
    return wini_string_new(cstr, (int64_t)strlen(cstr));
}

/* Igual, pero con longitud explícita (para contenido con NUL embebido). */
static inline WINI_CADENA wini_bridge_str_n(const char *data, int64_t len) {
    return wini_string_new(data, len);
}

/* Puntero de solo-lectura al contenido (terminado en NUL). NO transfiere
 * dueño. Válido mientras 's' siga vivo. */
static inline const char *wini_bridge_cstr(WINI_CADENA s) {
    return s ? s->data : "";
}

static inline int64_t wini_bridge_str_len(WINI_CADENA s) {
    return wini_string_len(s);
}

/* Azúcar: usar antes de pasar 's' a una función y seguir usándola. */
static inline WINI_CADENA wini_bridge_retener_str(WINI_CADENA s) {
    return wini_string_retener(s);
}

/* Azúcar: liberar una referencia propia. */
static inline void wini_bridge_liberar_str(WINI_CADENA s) {
    wini_liberar_cadena(s);
}

/* -------------------------------------------------------------------
 * Conversiones primitivo <-> WiniString
 * ------------------------------------------------------------------- */

/* Parseo de una WiniString* a primitivos. No liberan 's'.
 * Si 's' es NULL o no parseable, devuelven 0. */
int64_t wini_bridge_to_entero   (WINI_CADENA s);
double  wini_bridge_to_decimal  (WINI_CADENA s);
_Bool   wini_bridge_to_booleano (WINI_CADENA s);

/* Construcción de WiniString* a partir de primitivos (rc=1, dueño=caller). */
static inline WINI_CADENA wini_bridge_from_entero  (int64_t v) { return wini_i64_to_string(v); }
static inline WINI_CADENA wini_bridge_from_decimal (double v)  { return wini_f64_to_string(v); }
static inline WINI_CADENA wini_bridge_from_booleano(_Bool v)   { return wini_bool_to_string(v ? 1 : 0); }

/* -------------------------------------------------------------------
 * Listas — construcción
 * ------------------------------------------------------------------- */

static inline WINI_LISTA wini_bridge_list_new_entero  (const int64_t *d, int64_t n) { return wini_list_new_i64((int64_t *)d, n); }
static inline WINI_LISTA wini_bridge_list_new_decimal (const double  *d, int64_t n) { return wini_list_new_f64((double  *)d, n); }
static inline WINI_LISTA wini_bridge_list_new_booleano(const int8_t  *d, int64_t n) { return wini_list_new_i1 ((int8_t  *)d, n); }
/* OJO: wini_list_new_str NO retiene las cadenas. Si vas a liberar tus
 * copias por separado, retené antes con wini_bridge_retener_str(). */
static inline WINI_LISTA wini_bridge_list_new_cadena  (WiniString **d, int64_t n){ return wini_list_new_str(d, n); }

/* Lista vacía tipada: crea la lista y la devuelve con rc=1. */
WINI_LISTA wini_bridge_list_vacia_entero  (void);
WINI_LISTA wini_bridge_list_vacia_decimal (void);
WINI_LISTA wini_bridge_list_vacia_booleano(void);
WINI_LISTA wini_bridge_list_vacia_cadena  (void);

/* -------------------------------------------------------------------
 * Listas — lectura
 * ------------------------------------------------------------------- */

static inline int64_t     wini_bridge_list_get_entero  (WINI_LISTA l, int64_t i){ return wini_list_get_i64(l, i, 0); }
static inline double      wini_bridge_list_get_decimal (WINI_LISTA l, int64_t i){ return wini_list_get_f64(l, i, 0); }
static inline _Bool       wini_bridge_list_get_booleano(WINI_LISTA l, int64_t i){ return wini_list_get_i1 (l, i, 0) != 0; }
static inline WiniString *wini_bridge_list_get_cadena  (WINI_LISTA l, int64_t i){ return wini_list_get_str(l, i, 0); }

static inline int64_t wini_bridge_list_len(WINI_LISTA l) { return wini_list_len(l); }

/* -------------------------------------------------------------------
 * Listas — escritura (in-place, NO cambia el tamaño)
 * ------------------------------------------------------------------- */

static inline void wini_bridge_list_set_entero  (WINI_LISTA l, int64_t i, int64_t v)     { wini_list_set_i64(l, i, v, 0); }
static inline void wini_bridge_list_set_decimal (WINI_LISTA l, int64_t i, double v)      { wini_list_set_f64(l, i, v, 0); }
static inline void wini_bridge_list_set_booleano(WINI_LISTA l, int64_t i, _Bool v)       { wini_list_set_i1 (l, i, (int8_t)(v ? 1 : 0), 0); }
/* wini_list_set_str libera el elemento anterior: igual que el runtime. */
static inline void wini_bridge_list_set_cadena  (WINI_LISTA l, int64_t i, WiniString *v){ wini_list_set_str(l, i, v, 0); }

/* -------------------------------------------------------------------
 * Listas — append (crece con realloc). Devuelve la lista (posiblemente
 * reubicada). El caller DEBE reasignar:  l = wini_bridge_list_append_xx(l, v);
 * ------------------------------------------------------------------- */
WINI_LISTA wini_bridge_list_append_entero  (WINI_LISTA l, int64_t v);
WINI_LISTA wini_bridge_list_append_decimal (WINI_LISTA l, double v);
WINI_LISTA wini_bridge_list_append_booleano(WINI_LISTA l, _Bool v);
/* Retiene 'v' antes de guardarlo: podés liberar tu copia después. */
WINI_LISTA wini_bridge_list_append_cadena  (WINI_LISTA l, WiniString *v);

static inline WINI_LISTA wini_bridge_retener_lista(WINI_LISTA l) { return wini_list_retener(l); }
static inline void      wini_bridge_liberar_lista(WINI_LISTA l, wini_tipo_elem t) {
    wini_liberar_lista(l, (int)t);
}

/* -------------------------------------------------------------------
 * Diccionarios — construcción / tamaño
 * ------------------------------------------------------------------- */

static inline WINI_DICCIONARIO wini_bridge_dict_nuevo(void) { return wini_dict_new(); }
static inline int64_t          wini_bridge_dict_len  (WINI_DICCIONARIO d){ return wini_dict_len(d); }

/* -------------------------------------------------------------------
 * Diccionarios — get/set/contains SIN consumir la clave
 *
 * El runtime expone wini_dict_get_str_* / wini_dict_set_str_* que
 * CONSUMEN la clave (liberan una referencia). Ese contrato es cómodo
 * desde el codegen (que ya retiene cuando viene de una variable), pero
 * incómodo a mano. Estas envolturas retienen por dentro y liberan al
 * salir, así que la clave del caller queda intacta.
 * ------------------------------------------------------------------- */

/* Clave cadena (NO consume 'k') */
int64_t      wini_bridge_dict_get_entero_cadena  (WINI_DICCIONARIO d, WINI_CADENA k);
double       wini_bridge_dict_get_decimal_cadena (WINI_DICCIONARIO d, WINI_CADENA k);
_Bool        wini_bridge_dict_get_booleano_cadena(WINI_DICCIONARIO d, WINI_CADENA k);
/* Devuelve el valor SIN retener: es prestado del dict. Si querés
 * conservarlo más allá de un eventual set/delete, retenelo vos. */
WINI_CADENA  wini_bridge_dict_get_cadena_cadena  (WINI_DICCIONARIO d, WINI_CADENA k);

void wini_bridge_dict_set_entero_cadena  (WINI_DICCIONARIO d, WINI_CADENA k, int64_t v);
void wini_bridge_dict_set_decimal_cadena (WINI_DICCIONARIO d, WINI_CADENA k, double v);
void wini_bridge_dict_set_booleano_cadena(WINI_DICCIONARIO d, WINI_CADENA k, _Bool v);
/* Retiene 'v' antes de guardarlo: podés liberar tu copia después. */
void wini_bridge_dict_set_cadena_cadena  (WINI_DICCIONARIO d, WINI_CADENA k, WINI_CADENA v);

_Bool wini_bridge_dict_contains_cadena(WINI_DICCIONARIO d, WINI_CADENA k);

/* Variantes _cstr de azúcar: crean la WiniString internamente. */
static inline int64_t wini_bridge_dict_get_entero_cstr(WINI_DICCIONARIO d, const char *k) {
    WINI_CADENA s = wini_bridge_str(k);
    int64_t r = wini_bridge_dict_get_entero_cadena(d, s);
    wini_bridge_liberar_str(s);
    return r;
}
static inline void wini_bridge_dict_set_entero_cstr(WINI_DICCIONARIO d, const char *k, int64_t v) {
    WINI_CADENA s = wini_bridge_str(k);
    wini_bridge_dict_set_entero_cadena(d, s, v);
    wini_bridge_liberar_str(s);
}
static inline _Bool wini_bridge_dict_contains_cstr(WINI_DICCIONARIO d, const char *k) {
    WINI_CADENA s = wini_bridge_str(k);
    _Bool r = wini_bridge_dict_contains_cadena(d, s);
    wini_bridge_liberar_str(s);
    return r;
}

static inline WINI_DICCIONARIO wini_bridge_retener_dict(WINI_DICCIONARIO d) { return wini_dict_retener(d); }
static inline void wini_bridge_liberar_dict(WINI_DICCIONARIO d,
                                             wini_tipo_elem tk,
                                             wini_tipo_elem tv) {
    wini_liberar_diccionario(d, (int)tk, (int)tv);
}

/* -------------------------------------------------------------------
 * Captura de excepciones Wini desde C
 *
 * Permite llamar a una función Wini (o a cualquier código que pueda
 * hacer 'lanzar') sin que un 'lanzar' no capturado mate el proceso.
 *
 * Uso típico:
 *
 *     wini_bridge_error err;
 *     if (wini_bridge_call((wini_bridge_fn)mi_funcion_wini, mi_arg,
 *                          &resultado, &err) < 0) {
 *         fprintf(stderr, "%s: %s\n", err.tipo, err.mensaje);
 *         free(err.mensaje);
 *     }
 *
 * 'fn' recibe 'arg' y escribe en 'result' (si no es NULL). No puede
 * devolver nada por sí misma; para eso, hacé una pequeña función C
 * envoltorio que llame a la de Wini y guarde el retorno en *result.
 * ------------------------------------------------------------------- */

typedef struct {
    char    tipo[256];
    char   *mensaje;   /* asignado con malloc; liberar con free() */
    int64_t linea;
} wini_bridge_error;

typedef void (*wini_bridge_fn)(void *arg, void *result);

/* Llama 'fn(arg, result)' capturando excepciones.
 * Devuelve 0 si todo OK, -1 si hubo excepción (y llena *err). */
int wini_bridge_call(wini_bridge_fn fn, void *arg, void *result, wini_bridge_error *err);

/* Variante con prototipo típico "una función Wini que recibe N args".
 * No es variádica de verdad (C no lo permite con tipos heterogéneos);
 * pero cubre el 90% de los casos: hasta 6 argumentos 'void*'. */
void wini_bridge_call0(void (*fn)(void), void *result, wini_bridge_error *err);
void wini_bridge_call1(void (*fn)(void *), void *a1, void *result, wini_bridge_error *err);
void wini_bridge_call2(void (*fn)(void *, void *), void *a1, void *a2, void *result, wini_bridge_error *err);
void wini_bridge_call3(void (*fn)(void *, void *, void *), void *a1, void *a2, void *a3, void *result, wini_bridge_error *err);
void wini_bridge_call4(void (*fn)(void *, void *, void *, void *), void *a1, void *a2, void *a3, void *a4, void *result, wini_bridge_error *err);
void wini_bridge_call5(void (*fn)(void *, void *, void *, void *, void *), void *a1, void *a2, void *a3, void *a4, void *a5, void *result, wini_bridge_error *err);
void wini_bridge_call6(void (*fn)(void *, void *, void *, void *, void *, void *), void *a1, void *a2, void *a3, void *a4, void *a5, void *a6, void *result, wini_bridge_error *err);

#ifdef __cplusplus
}
#endif

#endif /* WINI_BRIDGE_H */
