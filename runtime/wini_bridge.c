/*
 * wini_bridge.c — implementación del puente C <-> Wini.
 *
 * Ver wini_bridge.h para la documentación de cada función.
 */

#include "wini_bridge.h"

#include <errno.h>
#include <setjmp.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* -------------------------------------------------------------------
 * Conversiones primitivo <-> WiniString
 * ------------------------------------------------------------------- */

int64_t wini_bridge_to_entero(WINI_CADENA s) {
    if (!s || !s->data || s->len == 0) return 0;
    errno = 0;
    char *end = NULL;
    long long v = strtoll(s->data, &end, 10);
    if (errno != 0) return 0;
    return (int64_t)v;
}

double wini_bridge_to_decimal(WINI_CADENA s) {
    if (!s || !s->data || s->len == 0) return 0.0;
    errno = 0;
    char *end = NULL;
    double v = strtod(s->data, &end);
    if (errno != 0) return 0.0;
    return v;
}

_Bool wini_bridge_to_booleano(WINI_CADENA s) {
    if (!s || !s->data || s->len == 0) return 0;
    const char *d = s->data;
    if (strcmp(d, "verdadero") == 0) return 1;
    if (strcmp(d, "falso") == 0)     return 0;
    if (strcmp(d, "true") == 0)      return 1;
    if (strcmp(d, "false") == 0)     return 0;
    if (strcmp(d, "1") == 0)         return 1;
    if (strcmp(d, "0") == 0)         return 0;
    return 0;
}

/* -------------------------------------------------------------------
 * Listas — creación vacía
 * ------------------------------------------------------------------- */

WINI_LISTA wini_bridge_list_vacia_entero  (void) { return wini_list_new_i64(NULL, 0); }
WINI_LISTA wini_bridge_list_vacia_decimal (void) { return wini_list_new_f64(NULL, 0); }
WINI_LISTA wini_bridge_list_vacia_booleano(void) { return wini_list_new_i1 (NULL, 0); }
WINI_LISTA wini_bridge_list_vacia_cadena  (void) { return wini_list_new_str(NULL, 0); }

/* -------------------------------------------------------------------
 * Listas — append
 *
 * Nota: usamos una copia local del struct WiniList para realloc del
 * arreglo de datos, sin tocar el original hasta que todo salga bien.
 * Si el realloc falla, no perdemos la lista original.
 * ------------------------------------------------------------------- */

static void *bridge_xrealloc(void *p, size_t sz) {
    void *q = realloc(p, sz);
    if (!q) { fprintf(stderr, "sin memoria en wini_bridge\n"); exit(1); }
    return q;
}

/* Macro común para los 3 tipos por valor. */
#define BRIDGE_APPEND_POR_VALOR(SUFIJO, TIPO_C)                             \
    WINI_LISTA wini_bridge_list_append_##SUFIJO(WINI_LISTA l, TIPO_C v) {   \
        if (!l) {                                                           \
            l = wini_bridge_list_vacia_##SUFIJO();                          \
        }                                                                   \
        int64_t n = l->len;                                                 \
        TIPO_C *datos = (TIPO_C *)bridge_xrealloc(l->data,                  \
            (size_t)(n + 1) * sizeof(TIPO_C));                              \
        datos[n] = v;                                                       \
        l->data  = datos;                                                   \
        l->len   = n + 1;                                                   \
        return l;                                                           \
    }

BRIDGE_APPEND_POR_VALOR(entero,   int64_t)
BRIDGE_APPEND_POR_VALOR(decimal,  double)
BRIDGE_APPEND_POR_VALOR(booleano, int8_t)

#undef BRIDGE_APPEND_POR_VALOR

WINI_LISTA wini_bridge_list_append_cadena(WINI_LISTA l, WiniString *v) {
    if (!l) l = wini_bridge_list_vacia_cadena();
    int64_t n = l->len;
    WiniString **datos = (WiniString **)bridge_xrealloc(l->data,
        (size_t)(n + 1) * sizeof(WiniString *));
    /* Retenemos: la lista cuenta como un dueño más. Si el caller
     * libera su copia, la lista sigue válida. */
    wini_string_retener(v);
    datos[n] = v;
    l->data  = datos;
    l->len   = n + 1;
    return l;
}

/* -------------------------------------------------------------------
 * Diccionarios — get/set/contains SIN consumir la clave
 *
 * Estrategia: retener antes de llamar al runtime (que sí consume),
 * liberar después. Así la clave del caller queda intacta.
 * ------------------------------------------------------------------- */

int64_t wini_bridge_dict_get_entero_cadena(WINI_DICCIONARIO d, WINI_CADENA k) {
    WiniString *kc = wini_string_retener(k);
    return wini_dict_get_str_i64(d, kc);
}

double wini_bridge_dict_get_decimal_cadena(WINI_DICCIONARIO d, WINI_CADENA k) {
    WiniString *kc = wini_string_retener(k);
    return wini_dict_get_str_f64(d, kc);
}

_Bool wini_bridge_dict_get_booleano_cadena(WINI_DICCIONARIO d, WINI_CADENA k) {
    WiniString *kc = wini_string_retener(k);
    return wini_dict_get_str_i1(d, kc) != 0;
}

WINI_CADENA wini_bridge_dict_get_cadena_cadena(WINI_DICCIONARIO d, WINI_CADENA k) {
    WiniString *kc = wini_string_retener(k);
    return wini_dict_get_str_str(d, kc);
}

void wini_bridge_dict_set_entero_cadena(WINI_DICCIONARIO d, WINI_CADENA k, int64_t v) {
    WiniString *kc = wini_string_retener(k);
    wini_dict_set_str_i64(d, kc, v);
}

void wini_bridge_dict_set_decimal_cadena(WINI_DICCIONARIO d, WINI_CADENA k, double v) {
    WiniString *kc = wini_string_retener(k);
    wini_dict_set_str_f64(d, kc, v);
}

void wini_bridge_dict_set_booleano_cadena(WINI_DICCIONARIO d, WINI_CADENA k, _Bool v) {
    WiniString *kc = wini_string_retener(k);
    wini_dict_set_str_i1(d, kc, (int8_t)(v ? 1 : 0));
}

void wini_bridge_dict_set_cadena_cadena(WINI_DICCIONARIO d, WINI_CADENA k, WINI_CADENA v) {
    WiniString *kc = wini_string_retener(k);
    /* El runtime NO retiene 'v'; el dueño anterior (el caller) sigue
     * siéndolo. Si el caller quiere conservar su copia, que retenga
     * aparte. Pero el contrato "NO consume 'v'" ya es así en el
     * runtime: wini_dict_set_str_str guarda el puntero tal cual y, si
     * pisa un valor anterior, lo libera. Así que el valor entrante
     * pasa a ser propiedad del dict. Para que el caller pueda liberar
     * su copia sin romper el dict, retenemos 'v' antes. */
    WiniString *vc = wini_string_retener(v);
    wini_dict_set_str_str(d, kc, vc);
}

_Bool wini_bridge_dict_contains_cadena(WINI_DICCIONARIO d, WINI_CADENA k) {
    WiniString *kc = wini_string_retener(k);
    _Bool r = wini_dict_contains_str(d, kc) != 0;
    wini_liberar_cadena(kc);
    return r;
}

/* -------------------------------------------------------------------
 * Captura de excepciones
 *
 * wini_try_push devuelve un jmp_buf donde hay que hacer setjmp. Ese
 * setjmp DEBE ejecutarse en el stack frame de la función que llama a
 * wini_try_push, no en una sub-función — por eso wini_bridge_call
 * hace el setjmp directamente acá, no dentro de un helper aparte.
 * ------------------------------------------------------------------- */

int wini_bridge_call(wini_bridge_fn fn, void *arg, void *result, wini_bridge_error *err) {
    if (!fn) {
        if (err) {
            snprintf(err->tipo, sizeof(err->tipo), "%s", "BridgeError");
            err->mensaje = strdup("wini_bridge_call: fn == NULL");
            err->linea = 0;
        }
        return -1;
    }

    void *buf = wini_try_push();
    /* setjmp guarda el estado del stack acá. Si 'lanzar' salta, volvemos
     * a este punto con valor != 0. */
    if (setjmp(*(jmp_buf *)buf) == 0) {
        /* Camino normal */
        fn(arg, result);
        wini_try_pop();
        return 0;
    }

    /* Camino excepción: el runtime ya desapiló el frame al saltar,
     * pero hay que liberar la memoria del frame en sí. */
    wini_try_pop_saltado();

    if (err) {
        /* Copiamos el tipo: wini_exc_tipo es un buffer interno que el
         * runtime puede pisar en el próximo 'lanzar'. */
        extern char wini_exc_tipo[256];
        extern WiniString *wini_exc_mensaje;
        extern int64_t wini_exc_linea;

        snprintf(err->tipo, sizeof(err->tipo), "%s",
                 wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");
        if (wini_exc_mensaje && wini_exc_mensaje->data) {
            err->mensaje = strdup(wini_exc_mensaje->data);
        } else {
            err->mensaje = strdup("");
        }
        err->linea = wini_exc_linea;

        /* Consumimos el mensaje para que su rc se libere correctamente. */
        if (wini_exc_mensaje) {
            wini_liberar_cadena(wini_exc_mensaje);
            wini_exc_mensaje = NULL;
        }
    } else {
        /* Sin struct 'err', igual hay que descartar el mensaje para
         * no dejar el rc colgado. */
        wini_excepcion_descartar();
    }

    return -1;
}

/* -------------------------------------------------------------------
 * Wrappers callN: más cómodos que castear a wini_bridge_fn.
 * ------------------------------------------------------------------- */

#define BRIDGE_CALLN(N, FNTYPE, ARGS_DECL, ARGS_PASS)                      \
    void wini_bridge_call##N(FNTYPE fn, ARGS_DECL void *result,            \
                              wini_bridge_error *err) {                    \
        if (!fn) {                                                         \
            if (err) {                                                     \
                snprintf(err->tipo, sizeof(err->tipo), "%s", "BridgeError");\
                err->mensaje = strdup("fn == NULL");                       \
                err->linea = 0;                                            \
            }                                                              \
            return;                                                        \
        }                                                                  \
        void *buf = wini_try_push();                                       \
        if (setjmp(*(jmp_buf *)buf) == 0) {                                \
            /* Llamada real: fn devuelve algo (posiblemente void*).     */ \
            /* Guardamos el retorno en 'result' si el caller lo pidió.  */ \
            void *r = (void *)(uintptr_t)(fn ARGS_PASS);                   \
            if (result) *(void **)result = r;                              \
            wini_try_pop();                                                \
            return;                                                        \
        }                                                                  \
        wini_try_pop_saltado();                                            \
        if (err) {                                                         \
            extern char wini_exc_tipo[256];                                \
            extern WiniString *wini_exc_mensaje;                           \
            extern int64_t wini_exc_linea;                                 \
            snprintf(err->tipo, sizeof(err->tipo), "%s",                   \
                     wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");    \
            err->mensaje = (wini_exc_mensaje && wini_exc_mensaje->data)    \
                ? strdup(wini_exc_mensaje->data) : strdup("");             \
            err->linea = wini_exc_linea;                                   \
            if (wini_exc_mensaje) {                                        \
                wini_liberar_cadena(wini_exc_mensaje);                     \
                wini_exc_mensaje = NULL;                                   \
            }                                                              \
        } else {                                                           \
            wini_excepcion_descartar();                                    \
        }                                                                  \
    }

void wini_bridge_call0(void (*fn)(void), void *result, wini_bridge_error *err) {
    if (!fn) { if (err) { snprintf(err->tipo, sizeof(err->tipo), "BridgeError"); err->mensaje = strdup("fn == NULL"); err->linea = 0; } return; }
    void *buf = wini_try_push();
    if (setjmp(*(jmp_buf *)buf) == 0) {
        fn();
        wini_try_pop();
        return;
    }
    wini_try_pop_saltado();
    if (err) {
        extern char wini_exc_tipo[256]; extern WiniString *wini_exc_mensaje; extern int64_t wini_exc_linea;
        snprintf(err->tipo, sizeof(err->tipo), "%s", wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");
        err->mensaje = (wini_exc_mensaje && wini_exc_mensaje->data) ? strdup(wini_exc_mensaje->data) : strdup("");
        err->linea = wini_exc_linea;
        if (wini_exc_mensaje) { wini_liberar_cadena(wini_exc_mensaje); wini_exc_mensaje = NULL; }
    } else wini_excepcion_descartar();
}

void wini_bridge_call1(void (*fn)(void *), void *a1, void *result, wini_bridge_error *err) {
    if (!fn) { if (err) { snprintf(err->tipo, sizeof(err->tipo), "BridgeError"); err->mensaje = strdup("fn == NULL"); err->linea = 0; } return; }
    void *buf = wini_try_push();
    if (setjmp(*(jmp_buf *)buf) == 0) {
        void *r = (void *)(uintptr_t)fn(a1);
        if (result) *(void **)result = r;
        wini_try_pop();
        return;
    }
    wini_try_pop_saltado();
    if (err) {
        extern char wini_exc_tipo[256]; extern WiniString *wini_exc_mensaje; extern int64_t wini_exc_linea;
        snprintf(err->tipo, sizeof(err->tipo), "%s", wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");
        err->mensaje = (wini_exc_mensaje && wini_exc_mensaje->data) ? strdup(wini_exc_mensaje->data) : strdup("");
        err->linea = wini_exc_linea;
        if (wini_exc_mensaje) { wini_liberar_cadena(wini_exc_mensaje); wini_exc_mensaje = NULL; }
    } else wini_excepcion_descartar();
}

/* Análogos call2..call6. Los genero con una macro para no repetir. */

#define BRIDGE_CALLN_BODY(N, FNPTR, CALL)                                   \
    void wini_bridge_call##N(FNPTR fn,                                      \
        void *a1, void *a2, void *a3, void *a4, void *a5, void *a6,         \
        void *result, wini_bridge_error *err)                               \
    {                                                                       \
        if (!fn) {                                                          \
            if (err) {                                                      \
                snprintf(err->tipo, sizeof(err->tipo), "BridgeError");      \
                err->mensaje = strdup("fn == NULL");                        \
                err->linea = 0;                                             \
            }                                                               \
            return;                                                         \
        }                                                                   \
        void *buf = wini_try_push();                                        \
        if (setjmp(*(jmp_buf *)buf) == 0) {                                 \
            void *r = (void *)(uintptr_t)(CALL);                            \
            if (result) *(void **)result = r;                               \
            wini_try_pop();                                                 \
            return;                                                         \
        }                                                                   \
        wini_try_pop_saltado();                                             \
        if (err) {                                                          \
            extern char wini_exc_tipo[256];                                 \
            extern WiniString *wini_exc_mensaje;                            \
            extern int64_t wini_exc_linea;                                  \
            snprintf(err->tipo, sizeof(err->tipo), "%s",                    \
                     wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");    \
            err->mensaje = (wini_exc_mensaje && wini_exc_mensaje->data)     \
                ? strdup(wini_exc_mensaje->data) : strdup("");              \
            err->linea = wini_exc_linea;                                    \
            if (wini_exc_mensaje) {                                         \
                wini_liberar_cadena(wini_exc_mensaje);                      \
                wini_exc_mensaje = NULL;                                    \
            }                                                               \
        } else {                                                            \
            wini_excepcion_descartar();                                     \
        }                                                                   \
    }

BRIDGE_CALLN_BODY(2, void (*)(void *, void *),
                  fn(a1, a2))
BRIDGE_CALLN_BODY(3, void (*)(void *, void *, void *),
                  fn(a1, a2, a3))
BRIDGE_CALLN_BODY(4, void (*)(void *, void *, void *, void *),
                  fn(a1, a2, a3, a4))
BRIDGE_CALLN_BODY(5, void (*)(void *, void *, void *, void *, void *),
                  fn(a1, a2, a3, a4, a5))
BRIDGE_CALLN_BODY(6, void (*)(void *, void *, void *, void *, void *, void *),
                  fn(a1, a2, a3, a4, a5, a6))

#undef BRIDGE_CALLN_BODY
