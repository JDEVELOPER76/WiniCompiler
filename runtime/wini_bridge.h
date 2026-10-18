/*
 * wini_bridge.h — puente C <-> Wini (versión mejorada y portable)
 * =================================================================
 *
 * Capa que facilita escribir código C que habla con Wini en cualquiera de
 * las dos direcciones:
 *
 *   1) C -> Wini : un programa host en C llama funciones compiladas desde
 *      un archivo .wini (vía `winic archivo.wini -shared -o lib.so`).
 *   2) Wini -> C : la implementación en C de una función declarada
 *      `externa` en Wini.
 *
 * Esta versión agrega sobre la original:
 *   - Un tipo variante WiniBridgeValue que puede contener cualquier tipo
 *     Wini (entero, decimal, booleano, cadena, lista, diccionario, nulo).
 *   - Invocación genérica por nombre: wini_bridge_invoke("mi_funcion",
 *     argc, argv, &resultado, &err).
 *   - Iteradores para recorrer listas y diccionarios desde C.
 *   - Registro de funciones C nativas (wini_bridge_register_native) para
 *     que Wini pueda llamarlas como si fueran externas.
 *   - Inicialización y limpieza del runtime desde C host
 *       (wini_bridge_init / wini_bridge_cleanup).
 *
 * Todo es aditivo: la API antigua (wini_bridge_call, call0..call6, etc.)
 * sigue funcionando tal cual.
 */

#ifndef WINI_BRIDGE_H
#define WINI_BRIDGE_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "runtime.h"

#ifdef __cplusplus
extern "C" {
#endif

/* =====================================================================
 * Alias de tipos Wini a su tipo C real
 * ===================================================================== */
typedef int64_t     WINI_ENTERO;
typedef double      WINI_DECIMAL;
typedef _Bool       WINI_BOOLEANO;
typedef WiniString *WINI_CADENA;
typedef WiniList   *WINI_LISTA;
typedef WiniDict   *WINI_DICCIONARIO;

/* =====================================================================
 * Tipo variante: un valor Wini de cualquier tipo
 *
 * El caller es dueño del valor devuelto por las funciones de creación y
 * debe llamar a wini_bridge_value_destruir() cuando termine. Las funciones
 * "as_" devuelven préstamos (no transfieren ownership).
 * ===================================================================== */
typedef enum {
    WINI_BRIDGE_NULL              = 0,
    WINI_BRIDGE_ENTERO            = 1,
    WINI_BRIDGE_DECIMAL           = 2,
    WINI_BRIDGE_BOOLEANO          = 3,
    WINI_BRIDGE_CADENA            = 4,
    WINI_BRIDGE_LISTA             = 5,
    WINI_BRIDGE_DICCIONARIO       = 6,
} wini_bridge_tipo;

typedef struct {
    wini_bridge_tipo tipo;
    union {
        int64_t              entero;
        double               decimal;
        _Bool                booleano;
        WINI_CADENA          cadena;
        WINI_LISTA           lista;
        WINI_DICCIONARIO     diccionario;
    } valor;
} WiniBridgeValue;

/* Creación de valores variante (el caller es dueño). */
WiniBridgeValue wini_bridge_value_nulo(void);
WiniBridgeValue wini_bridge_value_entero(int64_t v);
WiniBridgeValue wini_bridge_value_decimal(double v);
WiniBridgeValue wini_bridge_value_booleano(_Bool v);
/* Para heap: retiene una referencia, así que el caller puede liberar su
 * copia original si lo desea. */
WiniBridgeValue wini_bridge_value_cadena(WINI_CADENA s);
WiniBridgeValue wini_bridge_value_lista(WINI_LISTA l);
WiniBridgeValue wini_bridge_value_diccionario(WINI_DICCIONARIO d);

/* Libera los recursos que posee un valor variante. Tolera puntero nulo. */
void wini_bridge_value_destruir(WiniBridgeValue *v);

/* Lectura prestada (no destruye el valor; devuelve 0/0.0/falso/"<nulo>"
 * si el tipo no coincide). */
int64_t   wini_bridge_value_as_entero   (WiniBridgeValue *v);
double    wini_bridge_value_as_decimal  (WiniBridgeValue *v);
_Bool     wini_bridge_value_as_booleano (WiniBridgeValue *v);
WINI_CADENA      wini_bridge_value_as_cadena   (WiniBridgeValue *v);
WINI_LISTA       wini_bridge_value_as_lista    (WiniBridgeValue *v);
WINI_DICCIONARIO wini_bridge_value_as_diccionario(WiniBridgeValue *v);

/* =====================================================================
 * Cadenas
 * ===================================================================== */
static inline WINI_CADENA wini_bridge_str(const char *cstr) {
    if (!cstr) cstr = "";
    return wini_string_new(cstr, (int64_t)strlen(cstr));
}
static inline WINI_CADENA wini_bridge_str_n(const char *data, int64_t len) {
    return wini_string_new(data, len);
}
static inline const char *wini_bridge_cstr(WINI_CADENA s) {
    return s ? s->data : "";
}
static inline int64_t wini_bridge_str_len(WINI_CADENA s) {
    return wini_string_len(s);
}
static inline WINI_CADENA wini_bridge_retener_str(WINI_CADENA s) {
    return wini_string_retener(s);
}
static inline void wini_bridge_liberar_str(WINI_CADENA s) {
    wini_liberar_cadena(s);
}

/* Conversiones cadena -> primitivo (no liberan 's'). */
int64_t wini_bridge_to_entero   (WINI_CADENA s);
double  wini_bridge_to_decimal  (WINI_CADENA s);
_Bool   wini_bridge_to_booleano (WINI_CADENA s);

/* =====================================================================
 * Listas
 * ===================================================================== */
WINI_LISTA wini_bridge_list_vacia_entero  (void);
WINI_LISTA wini_bridge_list_vacia_decimal (void);
WINI_LISTA wini_bridge_list_vacia_booleano(void);
WINI_LISTA wini_bridge_list_vacia_cadena  (void);

/* Append: para cadenas retiene el valor pasado. */
WINI_LISTA wini_bridge_list_append_entero  (WINI_LISTA l, int64_t v);
WINI_LISTA wini_bridge_list_append_decimal (WINI_LISTA l, double v);
WINI_LISTA wini_bridge_list_append_booleano(WINI_LISTA l, _Bool v);
WINI_LISTA wini_bridge_list_append_cadena  (WINI_LISTA l, WINI_CADENA v);

static inline int64_t wini_bridge_list_len(WINI_LISTA l) { return wini_list_len(l); }
static inline int64_t wini_bridge_list_get_entero  (WINI_LISTA l, int64_t i){ return wini_list_get_i64(l, i, 0); }
static inline double  wini_bridge_list_get_decimal (WINI_LISTA l, int64_t i){ return wini_list_get_f64(l, i, 0); }
static inline _Bool   wini_bridge_list_get_booleano(WINI_LISTA l, int64_t i){ return wini_list_get_i1(l, i, 0) != 0; }
static inline WINI_CADENA wini_bridge_list_get_cadena(WINI_LISTA l, int64_t i){ return wini_list_get_str(l, i, 0); }

/* Iterador de lista: visita cada elemento como WiniBridgeValue.
 * Retornar 0 detiene la iteración. */
typedef int (*WiniBridgeVisitElem)(WiniBridgeValue *elem, void *usuario);
void wini_bridge_list_visitar(WINI_LISTA lista, WiniBridgeVisitElem visitar, void *usuario);

/* =====================================================================
 * Diccionarios
 * ===================================================================== */
WINI_DICCIONARIO wini_bridge_dict_nuevo(void);
static inline int64_t wini_bridge_dict_len(WINI_DICCIONARIO d){ return wini_dict_len(d); }

/* Clave cadena: no consumen la clave (retienen internamente). */
int64_t      wini_bridge_dict_get_entero_cadena  (WINI_DICCIONARIO d, WINI_CADENA k);
double       wini_bridge_dict_get_decimal_cadena (WINI_DICCIONARIO d, WINI_CADENA k);
_Bool        wini_bridge_dict_get_booleano_cadena(WINI_DICCIONARIO d, WINI_CADENA k);
WINI_CADENA  wini_bridge_dict_get_cadena_cadena  (WINI_DICCIONARIO d, WINI_CADENA k);
void wini_bridge_dict_set_entero_cadena  (WINI_DICCIONARIO d, WINI_CADENA k, int64_t v);
void wini_bridge_dict_set_decimal_cadena (WINI_DICCIONARIO d, WINI_CADENA k, double v);
void wini_bridge_dict_set_booleano_cadena(WINI_DICCIONARIO d, WINI_CADENA k, _Bool v);
void wini_bridge_dict_set_cadena_cadena  (WINI_DICCIONARIO d, WINI_CADENA k, WINI_CADENA v);
_Bool wini_bridge_dict_contains_cadena(WINI_DICCIONARIO d, WINI_CADENA k);

/* Iterador de diccionario: clave y valor como WiniBridgeValue.
 * Retornar 0 detiene la iteración. */
typedef int (*WiniBridgeVisitDicc)(WiniBridgeValue *clave, WiniBridgeValue *valor, void *usuario);
void wini_bridge_dict_visitar(WINI_DICCIONARIO d, WiniBridgeVisitDicc visitar, void *usuario);

/* =====================================================================
 * Captura de excepciones y llamada genérica por nombre
 * ===================================================================== */
typedef struct {
    char    tipo[256];
    char   *mensaje;   /* asignado con malloc; liberar con free() */
    int64_t linea;
    int     codigo;    /* código de error adicional */
} wini_bridge_error;

/* Prototipo de una función invocable genéricamente. */
typedef void (*wini_bridge_fn_invoke)(int argc, WiniBridgeValue *argv, WiniBridgeValue *resultado);

/* Llama a una función Wini por nombre (debe estar exportada en el .so o
 * registrada con wini_bridge_register_native). argc puede ser 0.
 * Devuelve 0 si OK, -1 si hubo excepción (llena *err). */
int wini_bridge_invoke(const char *nombre_funcion,
                       int argc, WiniBridgeValue *argv,
                       WiniBridgeValue *resultado,
                       wini_bridge_error *err);

/* =====================================================================
 * Registro de funciones C para ser llamadas desde Wini (externa inversa)
 * ===================================================================== */
/* Firma de una función C expuesta a Wini. */
typedef void (*wini_bridge_fn_nativo)(int argc, WiniBridgeValue *argv, WiniBridgeValue *resultado);

/* Registra 'fn' bajo 'nombre_wini'. Desde Wini se declara luego:
 *   externa "nombre_wini" como mi_funcion(a: entero, b: cadena): entero
 * y el codegen la resuelve contra este registro al cargar el .so.
 * Devuelve 0 si OK, -1 si ya existía el nombre. */
int wini_bridge_register_native(const char *nombre_wini,
                                wini_bridge_fn_nativo fn,
                                void *usuario);

/* =====================================================================
 * Inicialización / limpieza del runtime desde C host
 * ===================================================================== */
/* Prepara el runtime para ser usado desde un programa host en C.
 * Devuelve 0 si OK. Llama una vez al inicio. */
int  wini_bridge_init(void);

/* Limpia recursos del runtime. Llama al salir del programa host. */
void wini_bridge_cleanup(void);

/* =====================================================================
 * API antigua (mantenida por compatibilidad)
 * ===================================================================== */
typedef void (*wini_bridge_fn)(void *arg, void *result);
typedef void (*wini_bridge_fn0)(void);
typedef void (*wini_bridge_fn1)(void *);
typedef void (*wini_bridge_fn2)(void *, void *);
typedef void (*wini_bridge_fn3)(void *, void *, void *);
typedef void (*wini_bridge_fn4)(void *, void *, void *, void *);
typedef void (*wini_bridge_fn5)(void *, void *, void *, void *, void *);
typedef void (*wini_bridge_fn6)(void *, void *, void *, void *, void *, void *);

int wini_bridge_call(wini_bridge_fn fn, void *arg, void *result, wini_bridge_error *err);
void wini_bridge_call0(wini_bridge_fn0 fn, void *result, wini_bridge_error *err);
void wini_bridge_call1(wini_bridge_fn1 fn, void *a1, void *result, wini_bridge_error *err);
void wini_bridge_call2(wini_bridge_fn2 fn, void *a1, void *a2, void *a3, void *a4, void *a5, void *a6, void *result, wini_bridge_error *err);
void wini_bridge_call3(wini_bridge_fn3 fn, void *a1, void *a2, void *a3, void *a4, void *a5, void *a6, void *result, wini_bridge_error *err);
void wini_bridge_call4(wini_bridge_fn4 fn, void *a1, void *a2, void *a3, void *a4, void *a5, void *a6, void *result, wini_bridge_error *err);
void wini_bridge_call5(wini_bridge_fn5 fn, void *a1, void *a2, void *a3, void *a4, void *a5, void *a6, void *result, wini_bridge_error *err);
void wini_bridge_call6(wini_bridge_fn6 fn, void *a1, void *a2, void *a3, void *a4, void *a5, void *a6, void *result, wini_bridge_error *err);

/* Diccionarios _cstr de azúcar. */
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

#ifdef __cplusplus
}
#endif

#endif /* WINI_BRIDGE_H */
