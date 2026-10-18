/*
 * wini_bridge.c — implementación del puente C <-> Wini (versión mejorada)
 *
 * Ver wini_bridge.h para la documentación de cada función.
 *
 * Todo lo de la versión original se mantiene; se agregan:
 *   - WiniBridgeValue y sus funciones de creación/lectura/destrucción
 *   - Iteradores de lista y diccionario
 *   - wini_bridge_invoke() (llamada genérica por nombre)
 *   - wini_bridge_register_native() (externa inversa: C -> Wini)
 *   - wini_bridge_init() / wini_bridge_cleanup()
 */

#include "wini_bridge.h"

#include <errno.h>
#include <setjmp.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Prototipos de helpers estáticos definidos más abajo. */
static char *bridge_copiar_mensaje(WiniString *m);

/* -------------------------------------------------------------------
 * Tabla de funciones nativas registradas (externa inversa)
 * ------------------------------------------------------------------- */
#define WINI_BRIDGE_MAX_NATIVOS 64

typedef struct {
    char                  nombre[256];
    wini_bridge_fn_nativo fn;
    void                 *usuario;
    int                   usado;
} WiniBridgeNativo;

static WiniBridgeNativo nativos_tabla[WINI_BRIDGE_MAX_NATIVOS];
static int inicializado = 0;

static void asegurar_inicializado(void) {
    if (inicializado) return;
    memset(nativos_tabla, 0, sizeof(nativos_tabla));
    inicializado = 1;
}

static int encontrar_nativo(const char *nombre) {
    for (int i = 0; i < WINI_BRIDGE_MAX_NATIVOS; i++) {
        if (nativos_tabla[i].usado &&
            strcmp(nativos_tabla[i].nombre, nombre) == 0) {
            return i;
        }
    }
    return -1;
}

static int registrar_nativo_interno(const char *nombre,
                                    wini_bridge_fn_nativo fn,
                                    void *usuario) {
    asegurar_inicializado();
    if (encontrar_nativo(nombre) >= 0) return -1; /* ya existe */
    for (int i = 0; i < WINI_BRIDGE_MAX_NATIVOS; i++) {
        if (!nativos_tabla[i].usado) {
            strncpy(nativos_tabla[i].nombre, nombre, sizeof(nativos_tabla[i].nombre) - 1);
            nativos_tabla[i].nombre[sizeof(nativos_tabla[i].nombre) - 1] = '\0';
            nativos_tabla[i].fn = fn;
            nativos_tabla[i].usuario = usuario;
            nativos_tabla[i].usado = 1;
            return 0;
        }
    }
    return -1; /* tabla llena */
}

/* Helper local para copiar un mensaje de excepción. */
/* -------------------------------------------------------------------
 * Valor variante
 * ------------------------------------------------------------------- */

WiniBridgeValue wini_bridge_value_nulo(void) {
    WiniBridgeValue v;
    v.tipo = WINI_BRIDGE_NULL;
    v.valor.entero = 0;
    return v;
}

WiniBridgeValue wini_bridge_value_entero(int64_t v) {
    WiniBridgeValue r;
    r.tipo = WINI_BRIDGE_ENTERO;
    r.valor.entero = v;
    return r;
}

WiniBridgeValue wini_bridge_value_decimal(double v) {
    WiniBridgeValue r;
    r.tipo = WINI_BRIDGE_DECIMAL;
    r.valor.decimal = v;
    return r;
}

WiniBridgeValue wini_bridge_value_booleano(_Bool v) {
    WiniBridgeValue r;
    r.tipo = WINI_BRIDGE_BOOLEANO;
    r.valor.booleano = v;
    return r;
}

WiniBridgeValue wini_bridge_value_cadena(WINI_CADENA s) {
    WiniBridgeValue r;
    r.tipo = WINI_BRIDGE_CADENA;
    r.valor.cadena = s ? wini_string_retener(s) : NULL;
    return r;
}

WiniBridgeValue wini_bridge_value_lista(WINI_LISTA l) {
    WiniBridgeValue r;
    r.tipo = WINI_BRIDGE_LISTA;
    r.valor.lista = l ? wini_list_retener(l) : NULL;
    return r;
}

WiniBridgeValue wini_bridge_value_diccionario(WINI_DICCIONARIO d) {
    WiniBridgeValue r;
    r.tipo = WINI_BRIDGE_DICCIONARIO;
    r.valor.diccionario = d ? wini_dict_retener(d) : NULL;
    return r;
}

void wini_bridge_value_destruir(WiniBridgeValue *v) {
    if (!v) return;
    switch (v->tipo) {
        case WINI_BRIDGE_CADENA:
            wini_liberar_cadena(v->valor.cadena);
            break;
        case WINI_BRIDGE_LISTA:
            wini_liberar_lista(v->valor.lista, 0); /* tipo se ignora si desc != NULL */
            break;
        case WINI_BRIDGE_DICCIONARIO:
            wini_liberar_diccionario(v->valor.diccionario, 0, 0);
            break;
        default:
            break;
    }
    v->tipo = WINI_BRIDGE_NULL;
    v->valor.entero = 0;
}

/* Lectura prestada. */
int64_t wini_bridge_value_as_entero(WiniBridgeValue *v) {
    if (!v) return 0;
    if (v->tipo == WINI_BRIDGE_ENTERO) return v->valor.entero;
    return 0;
}

double wini_bridge_value_as_decimal(WiniBridgeValue *v) {
    if (!v) return 0.0;
    if (v->tipo == WINI_BRIDGE_DECIMAL) return v->valor.decimal;
    return 0.0;
}

_Bool wini_bridge_value_as_booleano(WiniBridgeValue *v) {
    if (!v) return 0;
    if (v->tipo == WINI_BRIDGE_BOOLEANO) return v->valor.booleano;
    return 0;
}

WINI_CADENA wini_bridge_value_as_cadena(WiniBridgeValue *v) {
    if (!v) return NULL;
    if (v->tipo == WINI_BRIDGE_CADENA) return v->valor.cadena;
    return NULL;
}

WINI_LISTA wini_bridge_value_as_lista(WiniBridgeValue *v) {
    if (!v) return NULL;
    if (v->tipo == WINI_BRIDGE_LISTA) return v->valor.lista;
    return NULL;
}

WINI_DICCIONARIO wini_bridge_value_as_diccionario(WiniBridgeValue *v) {
    if (!v) return NULL;
    if (v->tipo == WINI_BRIDGE_DICCIONARIO) return v->valor.diccionario;
    return NULL;
}

/* -------------------------------------------------------------------
 * Listas — iterador
 * ------------------------------------------------------------------- */

/* Detectamos si la lista es genérica o primitiva por desc. Si es genérica,
 * no sabemos el tipo de los elementos sin el descriptor; los tratamos como
 * opacos y no visitamos sus elementos (devolvemos nulo). */
void wini_bridge_list_visitar(WINI_LISTA lista, WiniBridgeVisitElem visitar, void *usuario) {
    if (!lista || !visitar) return;
    int64_t n = wini_list_len(lista);
    for (int64_t i = 0; i < n; i++) {
        if (lista->desc) {
            /* Lista genérica: no podemos interpretar el elemento sin más info. */
            WiniBridgeValue v = wini_bridge_value_nulo();
            if (!visitar(&v, usuario)) break;
            wini_bridge_value_destruir(&v);
        } else {
            /* Primitiva: necesitamos saber el tipo. Como el puente no lo
             * sabe de antemano, usamos el tipo de cadena como fallback
             * (el más común) o entero. Para no adivinar mal, visitamos
             * como cadena si los elementos parecen cadenas... pero no hay
             * forma fiable sin el descriptor. Usamos entero por defecto;
             * el caller que conozca el tipo puede usar wini_list_get_* directamente. */
            WiniBridgeValue v = wini_bridge_value_entero(wini_list_get_i64(lista, i, 0));
            if (!visitar(&v, usuario)) break;
            wini_bridge_value_destruir(&v);
        }
    }
}

/* -------------------------------------------------------------------
 * Diccionarios — iterador
 * ------------------------------------------------------------------- */

static void visitar_dicc_genérico(WiniDict *d, WiniBridgeVisitDicc visitar, void *usuario) {
    if (!d || !visitar) return;
    /* Iteramos sobre las claves de tipo cadena (el caso más común). Si el
     * diccionario tiene claves de otro tipo, el caller puede usar
     * wini_dict_get_* directamente. */
    for (int64_t i = 0; i < d->len; i++) {
        WiniString *clave_s = ((WiniString **)d->claves)[i];
        WiniBridgeValue k = wini_bridge_value_cadena(wini_string_retener(clave_s));

        /* Leer el valor según descVal o tipo implícito. Como no conocemos
         * el tipo de valor sin más info, intentamos cadena primero. */
        WiniString *val_s = ((WiniString **)d->valores)[i];
        WiniBridgeValue v = wini_bridge_value_cadena(wini_string_retener(val_s));

        if (!visitar(&k, &v, usuario)) {
            wini_bridge_value_destruir(&k);
            wini_bridge_value_destruir(&v);
            break;
        }
        wini_bridge_value_destruir(&k);
        wini_bridge_value_destruir(&v);
    }
}

void wini_bridge_dict_visitar(WINI_DICCIONARIO d, WiniBridgeVisitDicc visitar, void *usuario) {
    visitar_dicc_genérico(d, visitar, usuario);
}

/* -------------------------------------------------------------------
 * Llamada genérica por nombre
 * ------------------------------------------------------------------- */

int wini_bridge_invoke(const char *nombre_funcion,
                       int argc, WiniBridgeValue *argv,
                       WiniBridgeValue *resultado,
                       wini_bridge_error *err) {
    if (err) {
        err->tipo[0] = '\0';
        err->mensaje = NULL;
        err->linea = 0;
        err->codigo = 0;
    }
    if (!nombre_funcion) {
        if (err) {
            snprintf(err->tipo, sizeof(err->tipo), "BridgeError");
            err->mensaje = strdup("nombre_funcion == NULL");
            err->linea = 0;
            err->codigo = -1;
        }
        return -1;
    }

    asegurar_inicializado();

    /* Buscar en la tabla de nativos registrados. */
    int idx = encontrar_nativo(nombre_funcion);
    if (idx < 0) {
        if (err) {
            snprintf(err->tipo, sizeof(err->tipo), "BridgeError");
            size_t n = strlen(nombre_funcion) + 32;
            err->mensaje = (char *)malloc(n);
            if (err->mensaje)
                snprintf(err->mensaje, n, "función '%s' no registrada", nombre_funcion);
            err->linea = 0;
            err->codigo = -2;
        }
        return -1;
    }

    /* Construir argumentos para la función nativa. */
    wini_bridge_fn_nativo fn = nativos_tabla[idx].fn;
    if (!fn) {
        if (err) {
            snprintf(err->tipo, sizeof(err->tipo), "BridgeError");
            err->mensaje = strdup("función nativa nula");
            err->linea = 0;
            err->codigo = -3;
        }
        return -1;
    }

    /* Usar wini_bridge_call para capturar excepciones Wini dentro del nativo. */
    void *buf = wini_try_push(wini_invocacion_nueva());
    int ok = 0;
    if (setjmp(*(jmp_buf *)buf) == 0) {
        fn(argc, argv, resultado);
        wini_try_pop();
        ok = 1;
    } else {
        wini_try_pop_saltado();
        if (err) {
            extern char wini_exc_tipo[256];
            extern WiniString *wini_exc_mensaje;
            extern int64_t wini_exc_linea;
            snprintf(err->tipo, sizeof(err->tipo), "%s",
                     wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");
            err->mensaje = (wini_exc_mensaje)
                ? bridge_copiar_mensaje(wini_exc_mensaje)
                : strdup("");
            err->linea = wini_exc_linea;
            err->codigo = -4;
            if (wini_exc_mensaje) {
                wini_liberar_cadena(wini_exc_mensaje);
                wini_exc_mensaje = NULL;
            }
        } else {
            wini_excepcion_descartar();
        }
    }
    return ok ? 0 : -1;
}

/* Helper local para copiar un mensaje de excepción. */
/* -------------------------------------------------------------------
 * Registro de nativos (API pública)
 * ------------------------------------------------------------------- */

int wini_bridge_register_native(const char *nombre_wini,
                                wini_bridge_fn_nativo fn,
                                void *usuario) {
    if (!nombre_wini || !fn) return -1;
    return registrar_nativo_interno(nombre_wini, fn, usuario);
}

/* -------------------------------------------------------------------
 * Inicialización / limpieza
 * ------------------------------------------------------------------- */

int wini_bridge_init(void) {
    asegurar_inicializado();
    /* El runtime de Wini no requiere inicialización global explícita,
     * pero marcamos el bridge como listo. */
    return 0;
}

void wini_bridge_cleanup(void) {
    if (!inicializado) return;
    for (int i = 0; i < WINI_BRIDGE_MAX_NATIVOS; i++) {
        nativos_tabla[i].usado = 0;
        nativos_tabla[i].fn = NULL;
        nativos_tabla[i].usuario = NULL;
        nativos_tabla[i].nombre[0] = '\0';
    }
    inicializado = 0;
}

/* -------------------------------------------------------------------
 * Código original de la versión anterior (mantenido por compatibilidad)
 * ------------------------------------------------------------------- */

/* Conversiones primitivo <-> WiniString. */
int64_t wini_bridge_to_entero(WINI_CADENA s) {
    if (!s || !s->data || s->len == 0) return 0;
    errno = 0;
    char *end = NULL;
    long long v = strtoll(s->data, &end, 10);
    if (errno != 0 || end == s->data) return 0;
    return (int64_t)v;
}

double wini_bridge_to_decimal(WINI_CADENA s) {
    if (!s || !s->data || s->len == 0) return 0.0;
    errno = 0;
    char *end = NULL;
    double v = strtod(s->data, &end);
    if (errno != 0 || end == s->data) return 0.0;
    return v;
}

_Bool wini_bridge_to_booleano(WINI_CADENA s) {
    if (!s || !s->data || s->len == 0) return 0;
    const char *d = s->data;
    if (strcmp(d, "verdadero") == 0) return 1;
    if (strcmp(d, "falso") == 0) return 0;
    if (strcmp(d, "true") == 0) return 1;
    if (strcmp(d, "false") == 0) return 0;
    if (strcmp(d, "1") == 0) return 1;
    if (strcmp(d, "0") == 0) return 0;
    return 0;
}

/* Listas — creación vacía */
WINI_LISTA wini_bridge_list_vacia_entero(void) { return wini_list_new_i64(NULL, 0); }
WINI_LISTA wini_bridge_list_vacia_decimal(void) { return wini_list_new_f64(NULL, 0); }
WINI_LISTA wini_bridge_list_vacia_booleano(void) { return wini_list_new_i1(NULL, 0); }
WINI_LISTA wini_bridge_list_vacia_cadena(void) { return wini_list_new_str(NULL, 0); }

static void *bridge_xrealloc(void *p, size_t sz) {
    void *q = realloc(p, sz);
    if (!q) { fprintf(stderr, "sin memoria en wini_bridge\n"); exit(1); }
    return q;
}

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
BRIDGE_APPEND_POR_VALOR(booleano, _Bool)

#undef BRIDGE_APPEND_POR_VALOR

WINI_LISTA wini_bridge_list_append_cadena(WINI_LISTA l, WINI_CADENA v) {
    if (!l) l = wini_bridge_list_vacia_cadena();
    int64_t n = l->len;
    WiniString **datos = (WiniString **)bridge_xrealloc(l->data,
        (size_t)(n + 1) * sizeof(WiniString *));
    wini_string_retener(v);
    datos[n] = v;
    l->data  = datos;
    l->len   = n + 1;
    return l;
}

/* Diccionarios — get/set/contains sin consumir la clave */
int64_t wini_bridge_dict_get_entero_cadena(WINI_DICCIONARIO d, WINI_CADENA k) {
    WiniString *kc = wini_string_retener(k);
    int64_t r = wini_dict_get_str_i64(d, kc);
    wini_liberar_cadena(kc);
    return r;
}

double wini_bridge_dict_get_decimal_cadena(WINI_DICCIONARIO d, WINI_CADENA k) {
    WiniString *kc = wini_string_retener(k);
    double r = wini_dict_get_str_f64(d, kc);
    wini_liberar_cadena(kc);
    return r;
}

_Bool wini_bridge_dict_get_booleano_cadena(WINI_DICCIONARIO d, WINI_CADENA k) {
    WiniString *kc = wini_string_retener(k);
    _Bool r = wini_dict_get_str_i1(d, kc) != 0;
    wini_liberar_cadena(kc);
    return r;
}

WINI_CADENA wini_bridge_dict_get_cadena_cadena(WINI_DICCIONARIO d, WINI_CADENA k) {
    WiniString *kc = wini_string_retener(k);
    WiniString *r = wini_dict_get_str_str(d, kc);
    wini_liberar_cadena(kc);
    return r; /* prestado del dict; el caller retiene si quiere conservarlo */
}

void wini_bridge_dict_set_entero_cadena(WINI_DICCIONARIO d, WINI_CADENA k, int64_t v) {
    WiniString *kc = wini_string_retener(k);
    wini_dict_set_str_i64(d, kc, v);
    wini_liberar_cadena(kc);
}

void wini_bridge_dict_set_decimal_cadena(WINI_DICCIONARIO d, WINI_CADENA k, double v) {
    WiniString *kc = wini_string_retener(k);
    wini_dict_set_str_f64(d, kc, v);
    wini_liberar_cadena(kc);
}

void wini_bridge_dict_set_booleano_cadena(WINI_DICCIONARIO d, WINI_CADENA k, _Bool v) {
    WiniString *kc = wini_string_retener(k);
    wini_dict_set_str_i1(d, kc, (int8_t)(v ? 1 : 0));
    wini_liberar_cadena(kc);
}

void wini_bridge_dict_set_cadena_cadena(WINI_DICCIONARIO d, WINI_CADENA k, WINI_CADENA v) {
    WiniString *kc = wini_string_retener(k);
    WiniString *vc = wini_string_retener(v);
    wini_dict_set_str_str(d, kc, vc);
    wini_liberar_cadena(kc);
    /* vc queda como referencia del dict; soltamos nuestra copia extra */
    wini_liberar_cadena(vc);
}

_Bool wini_bridge_dict_contains_cadena(WINI_DICCIONARIO d, WINI_CADENA k) {
    WiniString *kc = wini_string_retener(k);
    _Bool r = wini_dict_contains_str(d, kc) != 0;
    wini_liberar_cadena(kc);
    return r;
}

/* =====================================================================
 * Captura de excepciones (código original)
 * ===================================================================== */

static char *bridge_copiar_mensaje(WiniString *m) {
    if (!m || !m->data || m->len <= 0) {
        char *vacio = strdup("");
        return vacio ? vacio : NULL;
    }
    char *copia = (char *)malloc((size_t)m->len + 1);
    if (!copia) return NULL;
    memcpy(copia, m->data, (size_t)m->len);
    copia[m->len] = '\0';
    return copia;
}

int wini_bridge_call(wini_bridge_fn fn, void *arg, void *result, wini_bridge_error *err) {
    if (!fn) {
        if (err) {
            snprintf(err->tipo, sizeof(err->tipo), "%s", "BridgeError");
            err->mensaje = strdup("wini_bridge_call: fn == NULL");
            err->linea = 0;
            err->codigo = -1;
        }
        return -1;
    }

    void *buf = wini_try_push(wini_invocacion_nueva());
    if (setjmp(*(jmp_buf *)buf) == 0) {
        fn(arg, result);
        wini_try_pop();
        return 0;
    }

    wini_try_pop_saltado();

    if (err) {
        extern char wini_exc_tipo[256];
        extern WiniString *wini_exc_mensaje;
        extern int64_t wini_exc_linea;
        snprintf(err->tipo, sizeof(err->tipo), "%s",
                 wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");
        err->mensaje = bridge_copiar_mensaje(wini_exc_mensaje);
        err->linea = wini_exc_linea;
        err->codigo = -2;
        if (wini_exc_mensaje) {
            wini_liberar_cadena(wini_exc_mensaje);
            wini_exc_mensaje = NULL;
        }
    } else {
        wini_excepcion_descartar();
    }

    return -1;
}

/* -------------------------------------------------------------------
 * Wrappers callN (código original)
 * ------------------------------------------------------------------- */

void wini_bridge_call0(wini_bridge_fn0 fn, void *result, wini_bridge_error *err) {
    if (!fn) { if (err) { snprintf(err->tipo, sizeof(err->tipo), "BridgeError"); err->mensaje = strdup("fn == NULL"); err->linea = 0; err->codigo = -1; } if (result) *(void **)result = NULL; return; }
    void *buf = wini_try_push(wini_invocacion_nueva());
    if (setjmp(*(jmp_buf *)buf) == 0) {
        fn();
        if (result) *(void **)result = NULL;
        wini_try_pop();
        return;
    }
    wini_try_pop_saltado();
    if (err) {
        extern char wini_exc_tipo[256]; extern WiniString *wini_exc_mensaje; extern int64_t wini_exc_linea;
        snprintf(err->tipo, sizeof(err->tipo), "%s", wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");
        err->mensaje = bridge_copiar_mensaje(wini_exc_mensaje);
        err->linea = wini_exc_linea;
        err->codigo = -2;
        if (wini_exc_mensaje) { wini_liberar_cadena(wini_exc_mensaje); wini_exc_mensaje = NULL; }
    } else wini_excepcion_descartar();
}

void wini_bridge_call1(wini_bridge_fn1 fn, void *a1, void *result, wini_bridge_error *err) {
    if (!fn) { if (err) { snprintf(err->tipo, sizeof(err->tipo), "BridgeError"); err->mensaje = strdup("fn == NULL"); err->linea = 0; err->codigo = -1; } if (result) *(void **)result = NULL; return; }
    void *buf = wini_try_push(wini_invocacion_nueva());
    if (setjmp(*(jmp_buf *)buf) == 0) {
        fn(a1);
        if (result) *(void **)result = NULL;
        wini_try_pop();
        return;
    }
    wini_try_pop_saltado();
    if (err) {
        extern char wini_exc_tipo[256]; extern WiniString *wini_exc_mensaje; extern int64_t wini_exc_linea;
        snprintf(err->tipo, sizeof(err->tipo), "%s", wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");
        err->mensaje = bridge_copiar_mensaje(wini_exc_mensaje);
        err->linea = wini_exc_linea;
        err->codigo = -2;
        if (wini_exc_mensaje) { wini_liberar_cadena(wini_exc_mensaje); wini_exc_mensaje = NULL; }
    } else wini_excepcion_descartar();
}

#define BRIDGE_CAT_(a, b) a##b
#define BRIDGE_CAT(a, b) BRIDGE_CAT_(a, b)

#define BRIDGE_WRAP(NAME, FNTYPE, CALL)                                     \
    void NAME(FNTYPE fn,                                                    \
        void *a1, void *a2, void *a3, void *a4, void *a5, void *a6,         \
        void *result, wini_bridge_error *err)                               \
    {                                                                       \
        (void)a1; (void)a2; (void)a3; (void)a4; (void)a5; (void)a6;         \
        if (!fn) {                                                          \
            if (err) {                                                      \
                snprintf(err->tipo, sizeof(err->tipo), "BridgeError");      \
                err->mensaje = strdup("fn == NULL");                        \
                err->linea = 0;                                             \
                err->codigo = -1;                                           \
            }                                                               \
            if (result) *(void **)result = NULL;                            \
            return;                                                         \
        }                                                                   \
        void *buf = wini_try_push(wini_invocacion_nueva());                                        \
        if (setjmp(*(jmp_buf *)buf) == 0) {                                 \
            CALL;                                                           \
            if (result) *(void **)result = NULL;                            \
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
            err->mensaje = bridge_copiar_mensaje(wini_exc_mensaje);         \
            err->linea = wini_exc_linea;                                    \
            err->codigo = -2;                                               \
            if (wini_exc_mensaje) {                                         \
                wini_liberar_cadena(wini_exc_mensaje);                      \
                wini_exc_mensaje = NULL;                                    \
            }                                                               \
        } else {                                                            \
            wini_excepcion_descartar();                                     \
        }                                                                   \
    }

BRIDGE_WRAP(wini_bridge_call2, wini_bridge_fn2, fn(a1, a2))
BRIDGE_WRAP(wini_bridge_call3, wini_bridge_fn3, fn(a1, a2, a3))
BRIDGE_WRAP(wini_bridge_call4, wini_bridge_fn4, fn(a1, a2, a3, a4))
BRIDGE_WRAP(wini_bridge_call5, wini_bridge_fn5, fn(a1, a2, a3, a4, a5))
BRIDGE_WRAP(wini_bridge_call6, wini_bridge_fn6, fn(a1, a2, a3, a4, a5, a6))

#undef BRIDGE_WRAP
#undef BRIDGE_CAT
#undef BRIDGE_CAT_

#undef BRIDGE_CALLN_BODY
