#include "runtime.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <setjmp.h>

// ---- strings ----
WiniString *wini_string_new(const char *src, int64_t len) {
    WiniString *s = (WiniString *)malloc(sizeof(WiniString));
    if (!s) { fprintf(stderr, "sin memoria para WiniString\n"); exit(1); }
    s->data = (char *)malloc((size_t)len + 1);
    if (!s->data) { fprintf(stderr, "sin memoria para contenido de cadena\n"); exit(1); }
    memcpy(s->data, src, (size_t)len);
    s->data[len] = '\0';
    s->len = len;
    s->rc = 1;
    return s;
}

WiniString *wini_string_retener(WiniString *s) {
    if (!s) return NULL;
    s->rc++;
    return s;
}

void wini_print_i64(int64_t value) { printf("%lld\n", (long long)value); }
void wini_print_f64(double value) { printf("%g\n", value); }
void wini_print_bool(_Bool value) { puts(value ? "verdadero" : "falso"); }
void wini_print_string(WiniString *value) {
    if (!value || !value->data) { puts("<nulo>"); return; }
    printf("%s\n", value->data);
}

void wini_print_list(void* list, int tipo) {
    WiniList* l = (WiniList*)list;
    if (!l) { printf("[]\n"); return; }
    printf("[");
    for (int64_t i = 0; i < l->len; i++) {
        if (i > 0) printf(", ");
        switch (tipo) {
            case 0: { // i64
                int64_t val = ((int64_t*)l->data)[i];
                printf("%lld", (long long)val);
                break;
            }
            case 1: { // f64
                double val = ((double*)l->data)[i];
                printf("%g", val);
                break;
            }
            case 2: { // i1
                int8_t val = ((int8_t*)l->data)[i];
                printf("%s", val ? "verdadero" : "falso");
                break;
            }
            case 3: { // str
                WiniString* s = ((WiniString**)l->data)[i];
                if (s && s->data) printf("%s", s->data);
                else printf("<nulo>");
                break;
            }
            default:
                printf("<?>");
        }
    }
    printf("]\n");
}
void wini_print_dict(void* dict, int tipoClave, int tipoValor) {
    WiniDict* d = (WiniDict*)dict;
    if (!d) { printf("{}\n"); return; }
    printf("{");
    for (int64_t i = 0; i < d->len; i++) {
        if (i > 0) printf(", ");
        // Imprimir clave según tipoClave
        switch (tipoClave) {
            case 0: { // i64
                int64_t key = ((int64_t*)d->claves)[i];
                printf("%lld", (long long)key);
                break;
            }
            case 1: { // f64
                double key = ((double*)d->claves)[i];
                printf("%g", key);
                break;
            }
            case 2: { // i1
                int8_t key = ((int8_t*)d->claves)[i];
                printf("%s", key ? "verdadero" : "falso");
                break;
            }
            case 3: { // str
                WiniString* s = ((WiniString**)d->claves)[i];
                if (s && s->data) printf("%s", s->data);
                else printf("<nulo>");
                break;
            }
            default:
                printf("<?>");
        }
        printf(": ");
        // Imprimir valor según tipoValor
        switch (tipoValor) {
            case 0: { // i64
                int64_t val = ((int64_t*)d->valores)[i];
                printf("%lld", (long long)val);
                break;
            }
            case 1: { // f64
                double val = ((double*)d->valores)[i];
                printf("%g", val);
                break;
            }
            case 2: { // i1
                int8_t val = ((int8_t*)d->valores)[i];
                printf("%s", val ? "verdadero" : "falso");
                break;
            }
            case 3: { // str
                WiniString* s = ((WiniString**)d->valores)[i];
                if (s && s->data) printf("%s", s->data);
                else printf("<nulo>");
                break;
            }
            default:
                printf("<?>");
        }
    }
    printf("}\n");
}
WiniString* wini_string_concat(WiniString* a, WiniString* b) {
    if (!a && !b) return wini_string_new("", 0);
    if (!a) return b;
    if (!b) return a;
    int64_t newLen = a->len + b->len;
    char* data = (char*)malloc((size_t)newLen + 1);
    if (!data) { fprintf(stderr, "sin memoria en wini_string_concat\n"); exit(1); }
    memcpy(data, a->data, (size_t)a->len);
    memcpy(data + a->len, b->data, (size_t)b->len);
    data[newLen] = '\0';
    WiniString* s = (WiniString*)malloc(sizeof(WiniString));
    if (!s) { free(data); fprintf(stderr, "sin memoria en wini_string_concat\n"); exit(1); }
    s->data = data;
    s->len = newLen;
    s->rc = 1;              // <-- faltaba: el nuevo buffer nace con un dueño
    return s;
}

WiniString* wini_i64_to_string(int64_t v) {
    char buf[64];
    int n = snprintf(buf, sizeof(buf), "%lld", (long long)v);
    return wini_string_new(buf, n);
}

WiniString* wini_f64_to_string(double v) {
    char buf[128];
    int n = snprintf(buf, sizeof(buf), "%g", v);
    return wini_string_new(buf, n);
}

WiniString* wini_bool_to_string(int8_t v) {
    return wini_string_new(v ? "verdadero" : "falso", v ? 9 : 5);
}
// Añadir al final del archivo
// Helper: arma el mensaje y llama a wini_lanzar cuando 'leer' se topa con
// EOF (stdin cerrado o redirigido desde /dev/null). No retorna. El largo
// del mensaje se calcula con strlen, no con un número mágico, para que
// no quede desincronizado si el texto del mensaje cambia.
static void wini_read_eof_error(void) {
    static const char *msg = "no se pudo leer: se alcanzó EOF en stdin";
    wini_lanzar("EOFError", wini_string_new(msg, (int64_t)strlen(msg)), 0);
    // wini_lanzar nunca retorna: o hace longjmp a un frame 'capturar', o
    // termina el programa. Por eso wini_read_string no necesita (ni
    // debe) devolver nada después de llamar a esto.
}

WiniString* wini_read_string(WiniString* prompt) {
    if (prompt && prompt->data) {
        printf("%s", prompt->data);   // sin salto de línea
        fflush(stdout);
    }
    char buffer[4096];
    if (fgets(buffer, sizeof(buffer), stdin) == NULL) {
        // EOF (o error de lectura): antes esto devolvía silenciosamente
        // "", indistinguible de una línea vacía tecleada por el usuario.
        // Eso hace que 'mientras verdadero: cadena x = leer(...); si x ==
        // "salir": romper' nunca termine con stdin redirigido desde
        // /dev/null, porque fgets no bloquea en EOF: vuelve a llamarse
        // millones de veces por segundo (CPU 100%). Lanzar EOFError deja
        // que el programa Wini decida (con 'intentar/capturar') o, si no
        // lo hace, termine limpio en vez de spinear.
        wini_read_eof_error();
    }
    size_t len = strlen(buffer);
    if (len > 0 && buffer[len-1] == '\n') {
        buffer[len-1] = '\0';
        len--;
    }
    return wini_string_new(buffer, (int64_t)len);
}

// ---- listas ----
static void *check_malloc(size_t sz) {
    void *p = malloc(sz);
    if (!p) { fprintf(stderr, "sin memoria en runtime\n"); exit(1); }
    return p;
}

WiniList *wini_list_new_i64(int64_t *data, int64_t len) {
    WiniList *list = (WiniList *)check_malloc(sizeof(WiniList));
    list->len = len;
    list->rc = 1;
    if (len == 0) {
        list->data = NULL;
        return list;
    }
    int64_t *copy = (int64_t *)check_malloc((size_t)len * sizeof(int64_t));
    memcpy(copy, data, (size_t)len * sizeof(int64_t));
    list->data = copy;
    return list;
}

WiniList *wini_list_new_f64(double *data, int64_t len) {
    WiniList *list = (WiniList *)check_malloc(sizeof(WiniList));
    list->len = len;
    list->rc = 1;
    if (len == 0) { list->data = NULL; return list; }
    double *copy = (double *)check_malloc((size_t)len * sizeof(double));
    memcpy(copy, data, (size_t)len * sizeof(double));
    list->data = copy;
    return list;
}

WiniList *wini_list_new_i1(int8_t *data, int64_t len) {
    WiniList *list = (WiniList *)check_malloc(sizeof(WiniList));
    list->len = len;
    list->rc = 1;
    if (len == 0) { list->data = NULL; return list; }
    int8_t *copy = (int8_t *)check_malloc((size_t)len * sizeof(int8_t));
    memcpy(copy, data, (size_t)len * sizeof(int8_t));
    list->data = copy;
    return list;
}

WiniList *wini_list_new_str(WiniString **data, int64_t len) {
    WiniList *list = (WiniList *)check_malloc(sizeof(WiniList));
    list->len = len;
    list->rc = 1;
    if (len == 0) { list->data = NULL; return list; }
    WiniString **copy = (WiniString **)check_malloc((size_t)len * sizeof(WiniString *));
    memcpy(copy, data, (size_t)len * sizeof(WiniString *));
    list->data = copy;
    return list;
}

WiniList *wini_list_retener(WiniList *lista) {
    if (!lista) return NULL;
    lista->rc++;
    return lista;
}

// Retiene (incrementa rc) cada WiniString* contenido en una lista de
// cadenas. Se usa cuando el arreglo interno de la lista se acaba de
// llenar con punteros que YA existían en otro lado (por ejemplo, al
// concatenar dos listas, o al extraer las claves/valores de un
// diccionario), para que la nueva lista cuente como un dueño más en vez
// de compartir el puntero "gratis".
static void wini_retener_elementos_str(WiniList *lista) {
    if (!lista || !lista->data) return;
    WiniString **elems = (WiniString **)lista->data;
    for (int64_t i = 0; i < lista->len; i++) {
        wini_string_retener(elems[i]);
    }
}


int64_t wini_list_len(WiniList *list) {
    if (!list) return 0;
    return list->len;
}

// Helper: arma el mensaje y llama a wini_lanzar. No retorna.
static void wini_list_index_error(int64_t idx, int64_t len, int64_t linea) {
    char buf[128];
    int n = snprintf(buf, sizeof(buf), "índice %lld fuera de rango (len=%lld)",
                     (long long)idx, (long long)len);
    if (n < 0) n = 0;
    if ((size_t)n >= sizeof(buf)) n = (int)sizeof(buf) - 1;
    wini_lanzar("IndexError", wini_string_new(buf, (int64_t)n), linea);
    // wini_lanzar nunca retorna: o hace longjmp a un frame, o exit(1).
}

int64_t wini_list_get_i64(WiniList *list, int64_t idx, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    return ((int64_t *)list->data)[idx];
}

double wini_list_get_f64(WiniList *list, int64_t idx, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    return ((double *)list->data)[idx];
}

int8_t wini_list_get_i1(WiniList *list, int64_t idx, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    return ((int8_t *)list->data)[idx];
}

WiniString *wini_list_get_str(WiniList *list, int64_t idx, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    return ((WiniString **)list->data)[idx];
}

// ---- longitud y acceso por índice de una cadena ----
// Reutiliza wini_list_index_error (mismo formato de mensaje/IndexError)
// aunque el contenedor no sea una WiniList: el mensaje solo habla de
// "índice ... fuera de rango (len=...)", válido para ambos casos.
int64_t wini_string_len(WiniString *s) {
    if (!s) return 0;
    return s->len;
}

WiniString *wini_string_get_char(WiniString *s, int64_t idx, int64_t linea) {
    if (!s || idx < 0 || idx >= s->len) {
        wini_list_index_error(idx, s ? s->len : 0, linea);
    }
    return wini_string_new(s->data + idx, 1);
}

void wini_list_set_i64(WiniList *list, int64_t idx, int64_t value, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    ((int64_t *)list->data)[idx] = value;
}

void wini_list_set_f64(WiniList *list, int64_t idx, double value, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    ((double *)list->data)[idx] = value;
}

void wini_list_set_i1(WiniList *list, int64_t idx, int8_t value, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    ((int8_t *)list->data)[idx] = value;
}

void wini_list_set_str(WiniList *list, int64_t idx, WiniString *value, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        // El codegen ya nos transfirió la referencia de 'value'
        // (hizo retenerSiCompartido antes de la llamada): si no la
        // vamos a guardar, hay que liberarla o queda fugada para
        // siempre.
        wini_liberar_cadena(value);
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    // El elemento que se pisa deja de estar referenciado desde aquí.
    WiniString *anterior = ((WiniString **)list->data)[idx];
    if (anterior != value) {
        wini_liberar_cadena(anterior);
    }
    ((WiniString **)list->data)[idx] = value;
}

// Tabla auxiliar: listas "vacías" reales (len=0, data=NULL) de cada tipo,
// usadas por wini_list_concat cuando uno de los operandos es NULL. Antes
// esta función devolvía directamente el otro operando (¡el mismo
// puntero!) en ese caso, así que el resultado quedaba compartiendo objeto
// con una variable ya existente: liberar cualquiera de las dos dejaba a
// la otra colgando. Ahora siempre se construye una lista nueva e
// independiente.
WiniList* wini_list_concat(WiniList* a, WiniList* b, int tipo) {
    WiniList vacia = {0, NULL, 1};
    if (!a) a = &vacia;
    if (!b) b = &vacia;
    int64_t newLen = a->len + b->len;
    // Creamos una nueva lista según el tipo
    WiniList* result;
    size_t elemSize;
    void* (*listNewFn)(void*, int64_t); // puntero a función de creación
    switch (tipo) {
        case 0: // i64
            elemSize = sizeof(int64_t);
            listNewFn = (void* (*)(void*, int64_t))wini_list_new_i64;
            break;
        case 1: // f64
            elemSize = sizeof(double);
            listNewFn = (void* (*)(void*, int64_t))wini_list_new_f64;
            break;
        case 2: // i1
            elemSize = sizeof(int8_t);
            listNewFn = (void* (*)(void*, int64_t))wini_list_new_i1;
            break;
        case 3: // str (punteros)
            elemSize = sizeof(WiniString*);
            listNewFn = (void* (*)(void*, int64_t))wini_list_new_str;
            break;
        default:
            fprintf(stderr, "tipo de lista no soportado para concatenación\n");
            exit(1);
    }
    // Asignar memoria para los datos combinados
    void* data = malloc((size_t)newLen * elemSize);
    if (!data) { fprintf(stderr, "sin memoria en concatenación de listas\n"); exit(1); }
    // Copiar primera lista
    if (a->data && a->len > 0) {
        memcpy(data, a->data, (size_t)a->len * elemSize);
    }
    // Copiar segunda lista
    if (b->data && b->len > 0) {
        memcpy((char*)data + (size_t)a->len * elemSize, b->data, (size_t)b->len * elemSize);
    }
    // Crear la lista con el constructor adecuado (que copiará los datos nuevamente o los tomará)
    // Para evitar doble copia, podríamos crear una función que acepte un puntero ya asignado,
    // pero por simplicidad usamos los constructores existentes que copian.
    // Para evitar doble copia, mejor hacemos una función interna, pero dejamos así.
    // Esto es ineficiente pero funcional.
    // Liberamos el buffer temporal y devolvemos la nueva lista.
    result = listNewFn(data, newLen);
    free(data);
    if (tipo == 3) {
        // 'result' ahora tiene su propio arreglo con copias de los
        // mismos punteros WiniString* que 'a' y 'b'. Sin retenerlos, esas
        // cadenas quedarían con tres "dueños" (a, b y result) pero un
        // solo rc de verdad: el primero de los tres en liberar su copia
        // dejaría a los otros dos con punteros colgantes.
        wini_retener_elementos_str(result);
    }
    return result;
}


// ---- diccionarios ----

WiniDict *wini_dict_new(void) {
    WiniDict *d = (WiniDict *)check_malloc(sizeof(WiniDict));
    d->len = 0;
    d->cap = 0;
    d->claves = NULL;
    d->valores = NULL;
    d->rc = 1;
    return d;
}

WiniDict *wini_dict_retener(WiniDict *dict) {
    if (!dict) return NULL;
    dict->rc++;
    return dict;
}

int64_t wini_dict_len(WiniDict *dict) {
    if (!dict) return 0;
    return dict->len;
}

// Búsqueda de índice por clave (una implementación por tipo de clave).
static int64_t wini_dict_find_i64(WiniDict *d, int64_t clave) {
    int64_t *claves = (int64_t *)d->claves;
    for (int64_t i = 0; i < d->len; i++) {
        if (claves[i] == clave) return i;
    }
    return -1;
}

static int64_t wini_dict_find_f64(WiniDict *d, double clave) {
    double *claves = (double *)d->claves;
    for (int64_t i = 0; i < d->len; i++) {
        if (claves[i] == clave) return i;
    }
    return -1;
}

static int64_t wini_dict_find_i1(WiniDict *d, int8_t clave) {
    int8_t *claves = (int8_t *)d->claves;
    for (int64_t i = 0; i < d->len; i++) {
        if (claves[i] == clave) return i;
    }
    return -1;
}

static int64_t wini_dict_find_str(WiniDict *d, WiniString *clave) {
    if (!d || !d->claves) return -1;
    WiniString **claves = (WiniString **)d->claves;
    for (int64_t i = 0; i < d->len; i++) {
        WiniString *actual = claves[i];
        if (!actual || !clave) {
            if (actual == clave) return i;
            continue;
        }
        if (actual->len == clave->len &&
            memcmp(actual->data, clave->data, (size_t)clave->len) == 0) {
            return i;
        }
    }
    return -1;
}

// Crece los arreglos de claves/valores si hace falta espacio para una
// entrada nueva. keySize/valSize son sizeof(TIPO_CLAVE)/sizeof(TIPO_VALOR).
static void wini_dict_grow(WiniDict *d, size_t keySize, size_t valSize) {
    if (d->len < d->cap) return;
    int64_t newCap = d->cap == 0 ? 4 : d->cap * 2;
    void *nuevasClaves = realloc(d->claves, (size_t)newCap * keySize);
    void *nuevosValores = realloc(d->valores, (size_t)newCap * valSize);
    if (!nuevasClaves || !nuevosValores) {
        fprintf(stderr, "sin memoria en runtime\n");
        exit(1);
    }
    d->claves = nuevasClaves;
    d->valores = nuevosValores;
    d->cap = newCap;
}

static void wini_dict_key_error_i64(int64_t clave) {
    char buf[128];
    int n = snprintf(buf, sizeof(buf), "KeyError: clave no encontrada en el diccionario: %lld", (long long)clave);
    if (n < 0) n = 0;
    if ((size_t)n >= sizeof(buf)) n = (int)sizeof(buf) - 1;
    wini_lanzar("KeyError", wini_string_new(buf, (int64_t)n), 0);
}

static void wini_dict_key_error_f64(double clave) {
    char buf[128];
    int n = snprintf(buf, sizeof(buf), "KeyError: clave no encontrada en el diccionario: %g", clave);
    if (n < 0) n = 0;
    if ((size_t)n >= sizeof(buf)) n = (int)sizeof(buf) - 1;
    wini_lanzar("KeyError", wini_string_new(buf, (int64_t)n), 0);
}

static void wini_dict_key_error_i1(int8_t clave) {
    char buf[128];
    int n = snprintf(buf, sizeof(buf), "KeyError: clave no encontrada en el diccionario: %s", clave ? "verdadero" : "falso");
    if (n < 0) n = 0;
    if ((size_t)n >= sizeof(buf)) n = (int)sizeof(buf) - 1;
    wini_lanzar("KeyError", wini_string_new(buf, (int64_t)n), 0);
}

static void wini_dict_key_error_str(WiniString *clave) {
    const char *text = clave && clave->data ? clave->data : "<nulo>";
    int64_t len = clave ? clave->len : 7;
    char buf[256];
    int n = snprintf(buf, sizeof(buf), "KeyError: clave no encontrada en el diccionario: %.*s", (int)len, text);
    if (n < 0) n = 0;
    if ((size_t)n >= sizeof(buf)) n = (int)sizeof(buf) - 1;
    // FIX(bug): antes 'clave' quedaba sin liberar en este camino. El
    // codegen (ver codegen.go, indexación de diccionario) libera una
    // clave recién construida (no compartida con una variable) DESPUÉS
    // de que la llamada a wini_dict_get_*_str retorna — pero
    // wini_lanzar() más abajo hace longjmp y esa llamada NUNCA retorna,
    // así que esa liberación posterior nunca se ejecutaba: la clave
    // (p. ej. un literal como en d["falta"]) se perdía para siempre
    // (fuga confirmada con LeakSanitizer). Liberamos 'clave' ACÁ, justo
    // después de copiar su contenido al mensaje de error y ANTES de
    // saltar, que es el único lugar por el que pasan tanto el camino de
    // clave-no-encontrada como builds futuros. Esto es la contraparte,
    // en el camino de excepción, del release incondicional que ahora
    // hace WINI_DICT_GET en el camino de éxito (ver esa macro más
    // abajo) — juntos hacen que wini_dict_get_*_str SIEMPRE consuma
    // exactamente una referencia de 'clave', haya o no KeyError. El
    // codegen ya no debe liberar la clave por su cuenta después de
    // llamar a wini_dict_get_*_str (y debe retenerla antes de la
    // llamada si viene de una variable que el programa Wini sigue
    // usando después) — ver el comentario junto a WINI_DICT_GET.
    wini_liberar_cadena(clave);
    wini_lanzar("KeyError", wini_string_new(buf, (int64_t)n), 0);
}

// Genera wini_dict_contains_<KSUF>
#define WINI_DICT_CONTAINS(KSUF, KTYPE)                                     \
    int8_t wini_dict_contains_##KSUF(WiniDict *d, KTYPE clave) {            \
        return wini_dict_find_##KSUF(d, clave) >= 0 ? 1 : 0;                \
    }

// VRELEASE se llama sobre el valor que se pisa al reasignar una clave ya
// existente: para tipos primitivos es un no-op; para VTYPE == WiniString*
// libera esa referencia (evita la fuga de memoria que había antes al
// sobreescribir un valor cadena sin liberar el anterior).
#define WINI_NOOP_RELEASE(x) ((void)(x))

// Genera wini_dict_set_<KSUF>_<VSUF>
//
// KRELEASE se llama sobre la CLAVE que llega cuando ya existía una igual
// en el diccionario (idx >= 0): en ese camino la clave nueva no se
// guarda en ningún lado (se sigue usando la que ya estaba), así que si
// KTYPE == WiniString* esa referencia quedaría fugada para siempre si no
// se libera aquí. Esto es correcto sin importar de dónde vino esa
// referencia: si el codegen la retuvo porque venía de una variable
// existente, este release compensa exactamente esa retención; si era un
// literal recién construido, esto es lo único que la libera.
#define WINI_DICT_SET(KSUF, KTYPE, VSUF, VTYPE, KRELEASE, VRELEASE)         \
    void wini_dict_set_##KSUF##_##VSUF(WiniDict *d, KTYPE clave,            \
                                        VTYPE valor) {                      \
        int64_t idx = wini_dict_find_##KSUF(d, clave);                      \
        if (idx >= 0) {                                                     \
            VTYPE anterior = ((VTYPE *)d->valores)[idx];                    \
            if (anterior != valor) { VRELEASE(anterior); }                  \
            ((VTYPE *)d->valores)[idx] = valor;                             \
            KRELEASE(clave);                                                \
            return;                                                        \
        }                                                                   \
        wini_dict_grow(d, sizeof(KTYPE), sizeof(VTYPE));                    \
        ((KTYPE *)d->claves)[d->len] = clave;                               \
        ((VTYPE *)d->valores)[d->len] = valor;                              \
        d->len++;                                                          \
    }

// Genera wini_dict_get_<KSUF>_<VSUF>. KRELEASE se llama sobre 'clave'
// justo antes de retornar el valor encontrado — es un no-op para claves
// de tipo primitivo (WINI_NOOP_RELEASE) y wini_liberar_cadena para
// claves de tipo cadena, mismo patrón que ya usa WINI_DICT_SET más
// abajo. En el camino de excepción (clave no encontrada), la liberación
// equivalente de una clave tipo cadena la hace wini_dict_key_error_str
// (ver comentario ahí) — entre las dos, wini_dict_get_*_str consume
// SIEMPRE exactamente una referencia de una clave tipo cadena, sin
// importar si la búsqueda tuvo éxito o lanzó KeyError. El codegen ya no
// debe liberar la clave después de llamar a estas funciones (y debe
// retenerla antes de la llamada si la clave viene de una variable que
// el programa Wini sigue usando después de esta expresión).
#define WINI_DICT_GET(KSUF, KTYPE, VSUF, VTYPE, KRELEASE)                   \
    VTYPE wini_dict_get_##KSUF##_##VSUF(WiniDict *d, KTYPE clave) {         \
        int64_t idx = wini_dict_find_##KSUF(d, clave);                      \
        if (idx < 0) wini_dict_key_error_##KSUF(clave); /* no retorna */    \
        VTYPE resultado = ((VTYPE *)d->valores)[idx];                       \
        KRELEASE(clave);                                                    \
        return resultado;                                                  \
    }

// KRETAIN_EACH se llama sobre la WiniList* resultante: para tipos
// primitivos es un no-op; para KTYPE/VTYPE == WiniString* retiene cada
// elemento copiado, porque ahora tanto el diccionario como la lista
// devuelta apuntan a las mismas cadenas.
#define WINI_RETAIN_EACH_NOOP(lista) ((void)(lista))

// Genera wini_dict_keys_<KSUF> reusando el constructor de WiniList del tipo
#define WINI_DICT_KEYS(KSUF, KTYPE, LISTNEWFN, KRETAIN_EACH)                \
    WiniList *wini_dict_keys_##KSUF(WiniDict *d) {                         \
        WiniList *lista = LISTNEWFN((KTYPE *)d->claves, d->len);           \
        KRETAIN_EACH(lista);                                               \
        return lista;                                                      \
    }

// Genera wini_dict_values_<VSUF> reusando el constructor de WiniList
#define WINI_DICT_VALUES(VSUF, VTYPE, LISTNEWFN, VRETAIN_EACH)             \
    WiniList *wini_dict_values_##VSUF(WiniDict *d) {                       \
        WiniList *lista = LISTNEWFN((VTYPE *)d->valores, d->len);          \
        VRETAIN_EACH(lista);                                               \
        return lista;                                                      \
    }

WINI_DICT_CONTAINS(i64, int64_t)
WINI_DICT_CONTAINS(f64, double)
WINI_DICT_CONTAINS(i1, int8_t)
WINI_DICT_CONTAINS(str, WiniString *)

WINI_DICT_SET(i64, int64_t, i64, int64_t, WINI_NOOP_RELEASE, WINI_NOOP_RELEASE)
WINI_DICT_SET(i64, int64_t, f64, double, WINI_NOOP_RELEASE, WINI_NOOP_RELEASE)
WINI_DICT_SET(i64, int64_t, i1, int8_t, WINI_NOOP_RELEASE, WINI_NOOP_RELEASE)
WINI_DICT_SET(i64, int64_t, str, WiniString *, WINI_NOOP_RELEASE, wini_liberar_cadena)
WINI_DICT_SET(f64, double, i64, int64_t, WINI_NOOP_RELEASE, WINI_NOOP_RELEASE)
WINI_DICT_SET(f64, double, f64, double, WINI_NOOP_RELEASE, WINI_NOOP_RELEASE)
WINI_DICT_SET(f64, double, i1, int8_t, WINI_NOOP_RELEASE, WINI_NOOP_RELEASE)
WINI_DICT_SET(f64, double, str, WiniString *, WINI_NOOP_RELEASE, wini_liberar_cadena)
WINI_DICT_SET(i1, int8_t, i64, int64_t, WINI_NOOP_RELEASE, WINI_NOOP_RELEASE)
WINI_DICT_SET(i1, int8_t, f64, double, WINI_NOOP_RELEASE, WINI_NOOP_RELEASE)
WINI_DICT_SET(i1, int8_t, i1, int8_t, WINI_NOOP_RELEASE, WINI_NOOP_RELEASE)
WINI_DICT_SET(i1, int8_t, str, WiniString *, WINI_NOOP_RELEASE, wini_liberar_cadena)
WINI_DICT_SET(str, WiniString *, i64, int64_t, wini_liberar_cadena, WINI_NOOP_RELEASE)
WINI_DICT_SET(str, WiniString *, f64, double, wini_liberar_cadena, WINI_NOOP_RELEASE)
WINI_DICT_SET(str, WiniString *, i1, int8_t, wini_liberar_cadena, WINI_NOOP_RELEASE)
WINI_DICT_SET(str, WiniString *, str, WiniString *, wini_liberar_cadena, wini_liberar_cadena)

WINI_DICT_GET(i64, int64_t, i64, int64_t, WINI_NOOP_RELEASE)
WINI_DICT_GET(i64, int64_t, f64, double, WINI_NOOP_RELEASE)
WINI_DICT_GET(i64, int64_t, i1, int8_t, WINI_NOOP_RELEASE)
WINI_DICT_GET(i64, int64_t, str, WiniString *, WINI_NOOP_RELEASE)
WINI_DICT_GET(f64, double, i64, int64_t, WINI_NOOP_RELEASE)
WINI_DICT_GET(f64, double, f64, double, WINI_NOOP_RELEASE)
WINI_DICT_GET(f64, double, i1, int8_t, WINI_NOOP_RELEASE)
WINI_DICT_GET(f64, double, str, WiniString *, WINI_NOOP_RELEASE)
WINI_DICT_GET(i1, int8_t, i64, int64_t, WINI_NOOP_RELEASE)
WINI_DICT_GET(i1, int8_t, f64, double, WINI_NOOP_RELEASE)
WINI_DICT_GET(i1, int8_t, i1, int8_t, WINI_NOOP_RELEASE)
WINI_DICT_GET(i1, int8_t, str, WiniString *, WINI_NOOP_RELEASE)
WINI_DICT_GET(str, WiniString *, i64, int64_t, wini_liberar_cadena)
WINI_DICT_GET(str, WiniString *, f64, double, wini_liberar_cadena)
WINI_DICT_GET(str, WiniString *, i1, int8_t, wini_liberar_cadena)
WINI_DICT_GET(str, WiniString *, str, WiniString *, wini_liberar_cadena)

WINI_DICT_KEYS(i64, int64_t, wini_list_new_i64, WINI_RETAIN_EACH_NOOP)
WINI_DICT_KEYS(f64, double, wini_list_new_f64, WINI_RETAIN_EACH_NOOP)
WINI_DICT_KEYS(i1, int8_t, wini_list_new_i1, WINI_RETAIN_EACH_NOOP)
WINI_DICT_KEYS(str, WiniString *, wini_list_new_str, wini_retener_elementos_str)

WINI_DICT_VALUES(i64, int64_t, wini_list_new_i64, WINI_RETAIN_EACH_NOOP)
WINI_DICT_VALUES(f64, double, wini_list_new_f64, WINI_RETAIN_EACH_NOOP)
WINI_DICT_VALUES(i1, int8_t, wini_list_new_i1, WINI_RETAIN_EACH_NOOP)
WINI_DICT_VALUES(str, WiniString *, wini_list_new_str, wini_retener_elementos_str)

static void wini_range_step_error(void) {
    // FIX(bug): antes esto pasaba un tamaño hardcodeado (23) para un
    // literal de 20 bytes ("step no puede ser 0"), un
    // global-buffer-overflow confirmado con AddressSanitizer: wini_string_new
    // copia 'len' bytes desde 'src' (ver más abajo), así que leía 3 bytes
    // más allá del final del literal en .rodata. Se usa strlen() para no
    // depender de mantener sincronizado un número mágico con el texto del
    // mensaje si alguien lo edita en el futuro.
    wini_lanzar("ValueError", wini_string_new("step no puede ser 0", (int64_t)strlen("step no puede ser 0")), 0);
}

WiniList* wini_range_i64(int64_t start, int64_t end, int64_t step) {
    if (step == 0) {
        wini_range_step_error();
    }
    // Calcular la longitud
    int64_t len = 0;
    if (step > 0) {
        if (start < end) len = (end - start + step - 1) / step;
    } else {
        if (start > end) len = (start - end - step - 1) / (-step);
    }
    // Crear la lista
    WiniList *list = (WiniList*)check_malloc(sizeof(WiniList));
    list->len = len;
    list->rc = 1;           // <-- faltaba en el caso len == 0 (antes del return)
    if (len == 0) {
        list->data = NULL;
        return list;
    }
    int64_t *data = (int64_t*)check_malloc((size_t)len * sizeof(int64_t));
    for (int64_t i = 0; i < len; i++) {
        data[i] = start + i * step;
    }
    list->data = data;
    // (list->rc ya quedó en 1 arriba)
    return list;
}

// ---- argumentos de línea de comandos ----
WiniList* wini_args_to_list(int64_t argc, char **argv) {
    if (argc <= 0) {
        return wini_list_new_str(NULL, 0);
    }
    WiniString **cadenas = (WiniString **)check_malloc((size_t)argc * sizeof(WiniString *));
    for (int64_t i = 0; i < argc; i++) {
        cadenas[i] = wini_string_new(argv[i], (int64_t)strlen(argv[i]));
    }
    WiniList *list = wini_list_new_str(cadenas, argc);
    free(cadenas);
    return list;
}
// ---- comparación de cadenas ----
static int wini_string_cmp(WiniString *a, WiniString *b) {
    if (!a && !b) return 0;
    if (!a) return -1;
    if (!b) return 1;
    int64_t min = a->len < b->len ? a->len : b->len;
    int cmp = memcmp(a->data, b->data, (size_t)min);
    if (cmp != 0) return cmp;
    if (a->len == b->len) return 0;
    return (a->len < b->len) ? -1 : 1;
}

int8_t wini_string_eq(WiniString *a, WiniString *b) {
    return wini_string_cmp(a, b) == 0 ? 1 : 0;
}
int8_t wini_string_ne(WiniString *a, WiniString *b) {
    return wini_string_cmp(a, b) != 0 ? 1 : 0;
}
int8_t wini_string_lt(WiniString *a, WiniString *b) {
    return wini_string_cmp(a, b) < 0 ? 1 : 0;
}
int8_t wini_string_le(WiniString *a, WiniString *b) {
    return wini_string_cmp(a, b) <= 0 ? 1 : 0;
}
int8_t wini_string_gt(WiniString *a, WiniString *b) {
    return wini_string_cmp(a, b) > 0 ? 1 : 0;
}
int8_t wini_string_ge(WiniString *a, WiniString *b) {
    return wini_string_cmp(a, b) >= 0 ? 1 : 0;
}

// ---- gestión de memoria (liberar) ----

void wini_liberar_cadena(WiniString *s) {
    if (!s) return;
    // Decrementa el contador de referencias; solo libera memoria de
    // verdad cuando ya no queda ningún dueño. Si 'rc' ya estaba en 0 (uso
    // incorrecto: liberar más veces de las que se retuvo) no volvemos a
    // hacer free() sobre memoria ya liberada.
    if (s->rc > 0) s->rc--;
    if (s->rc > 0) return;
    if (s->data) free(s->data);
    free(s);
}

void wini_liberar_lista(WiniList *lista, int tipo) {
    if (!lista) return;
    if (lista->rc > 0) lista->rc--;
    if (lista->rc > 0) return;
    if (tipo == 3 && lista->data) { // str: liberar una referencia de cada WiniString
        WiniString **elems = (WiniString **)lista->data;
        for (int64_t i = 0; i < lista->len; i++) {
            wini_liberar_cadena(elems[i]);
        }
    }
    if (lista->data) free(lista->data);
    free(lista);
}

void wini_liberar_diccionario(WiniDict *dict, int tipoClave, int tipoValor) {
    if (!dict) return;
    if (dict->rc > 0) dict->rc--;
    if (dict->rc > 0) return;
    if (tipoClave == 3 && dict->claves) { // claves tipo str
        WiniString **claves = (WiniString **)dict->claves;
        for (int64_t i = 0; i < dict->len; i++) {
            wini_liberar_cadena(claves[i]);
        }
    }
    if (tipoValor == 3 && dict->valores) { // valores tipo str
        WiniString **valores = (WiniString **)dict->valores;
        for (int64_t i = 0; i < dict->len; i++) {
            wini_liberar_cadena(valores[i]);
        }
    }
    if (dict->claves) free(dict->claves);
    if (dict->valores) free(dict->valores);
    free(dict);
}

// ---- excepciones (intentar / capturar / finalmente / lanzar) ----

struct WiniTryFrame {
    jmp_buf buf;
    struct WiniTryFrame *anterior;
};

// Pila de manejadores activos (el más reciente primero). No es
// thread-safe: como el resto de este runtime, asume un programa Wini de
// un solo hilo.
static struct WiniTryFrame *wini_try_pila = NULL;

// Datos de la excepción actualmente "en vuelo": válidos desde que
// 'wini_lanzar'/'wini_relanzar' hacen el salto hasta que el 'capturar'
// que hizo match los consume (o hasta que el programa termina por no
// haber ningún manejador).
//
// NOTA: estas variables eran 'static' antes. Ahora son de enlace externo
// (aunque NO se declaran en runtime.h) para que wini_bridge.c —que es
// código separado, fuera de este archivo— pueda leerlas. Para código
// cliente que no quiera depender del layout interno, se expone
// 'wini_excepcion_actual' como API pública (ver runtime.h).
char wini_exc_tipo[256];
WiniString *wini_exc_mensaje = NULL;
// Se conserva una referencia mientras el mensaje está prestado a un catch,
// para que 'relanzar' pueda reconstruir la excepción original.
static WiniString *wini_exc_mensaje_reservado = NULL;
int64_t wini_exc_linea = 0;

void *wini_try_push(void) {
    struct WiniTryFrame *f = (struct WiniTryFrame *)check_malloc(sizeof(struct WiniTryFrame));
    f->anterior = wini_try_pila;
    wini_try_pila = f;
    return f->buf; // un jmp_buf es un array: decae a puntero al primer elemento
}

void wini_try_pop(void) {
    if (!wini_try_pila) return;
    struct WiniTryFrame *f = wini_try_pila;
    wini_try_pila = f->anterior;
    free(f);
}

static void wini_terminar_no_capturada(void) {
    fprintf(stderr, "%s", wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");
    if (wini_exc_mensaje && wini_exc_mensaje->len > 0) {
        fprintf(stderr, ": %.*s", (int)wini_exc_mensaje->len, wini_exc_mensaje->data);
    }
    if (wini_exc_linea > 0) {
        fprintf(stderr, " (línea %lld)", (long long)wini_exc_linea);
    }
    fprintf(stderr, "\n");
    exit(1);
}

// Frame que acaba de resolver un salto (longjmp) hacia este punto:
// 'wini_relanzar' lo deja acá justo antes de saltar, porque ya no hay
// forma segura de liberarlo ni antes del salto (longjmp todavía necesita
// leer su jmp_buf) ni después (esa ejecución nunca vuelve: se fue por
// longjmp). Quien atrapa el salto lo libera con wini_try_pop_saltado.
static struct WiniTryFrame *wini_frame_recien_saltado = NULL;

void wini_relanzar(void) {
    if (!wini_exc_mensaje && wini_exc_mensaje_reservado) {
        wini_exc_mensaje = wini_exc_mensaje_reservado;
        wini_exc_mensaje_reservado = NULL;
    }
    if (!wini_try_pila) {
        wini_terminar_no_capturada();
    }
    struct WiniTryFrame *f = wini_try_pila;
    wini_try_pila = f->anterior;
    wini_frame_recien_saltado = f;
    longjmp(f->buf, 1);
}

// Libera el frame que 'wini_relanzar' acaba de usar para saltar hasta
// acá. Debe llamarse exactamente una vez, justo al entrar al despacho de
// 'capturar' de un 'intentar' (inmediatamente después de que su propio
// 'setjmp' devolvió no-cero) — nunca en el camino normal sin excepción,
// que usa wini_try_pop en su lugar.
void wini_try_pop_saltado(void) {
    if (!wini_frame_recien_saltado) return;
    free(wini_frame_recien_saltado);
    wini_frame_recien_saltado = NULL;
}

void wini_lanzar(const char *tipo, WiniString *mensaje, int64_t linea) {
    const char *tipoReal = (tipo && *tipo) ? tipo : "RuntimeError";
    int n = snprintf(wini_exc_tipo, sizeof(wini_exc_tipo), "%s", tipoReal);
    if (n < 0) {
        wini_exc_tipo[0] = '\0';
        snprintf(wini_exc_tipo, sizeof(wini_exc_tipo), "%s", "RuntimeError");
    } else if ((size_t)n >= sizeof(wini_exc_tipo)) {
        fprintf(stderr, "warning: nombre de tipo de excepción truncado a %zu chars\n", sizeof(wini_exc_tipo) - 1);
        wini_exc_tipo[sizeof(wini_exc_tipo) - 1] = '\0';
    }
    if (wini_exc_mensaje) {
        wini_liberar_cadena(wini_exc_mensaje);
    }
    if (wini_exc_mensaje_reservado) {
        wini_liberar_cadena(wini_exc_mensaje_reservado);
        wini_exc_mensaje_reservado = NULL;
    }
    wini_exc_mensaje = mensaje ? mensaje : wini_string_new("", 0);
    wini_exc_linea = linea;
    wini_relanzar();
}

int8_t wini_excepcion_es_tipo(const char *tipo) {
    return strcmp(wini_exc_tipo, tipo) == 0;
}

WiniString *wini_excepcion_tomar_mensaje(void) {
    WiniString *m = wini_exc_mensaje ? wini_exc_mensaje : wini_string_new("", 0);
    if (wini_exc_mensaje) {
        wini_exc_mensaje_reservado = wini_string_retener(wini_exc_mensaje);
    }
    wini_exc_mensaje = NULL;
    // NO limpiar wini_exc_tipo acá: un 'relanzar' desde este catch
    // necesita el tipo original para que los captures externos hagan
    // match. El próximo 'wini_lanzar' lo va a pisar de todos modos.
    return m;
}

void wini_excepcion_descartar(void) {
    if (wini_exc_mensaje) {
        wini_liberar_cadena(wini_exc_mensaje);
        wini_exc_mensaje = NULL;
    }
    if (wini_exc_mensaje_reservado) {
        wini_liberar_cadena(wini_exc_mensaje_reservado);
        wini_exc_mensaje_reservado = NULL;
    }
    // Ídem: no limpiar wini_exc_tipo. Solo el próximo 'lanzar' debe
    // resetear el estado; mientras tanto, 'relanzar' lo sigue usando.
}

// ---- API de inspección para puentes externos ----

void wini_excepcion_actual(char *tipo_out, size_t tipo_cap,
                           char **mensaje_out, int64_t *linea_out) {
    if (tipo_out && tipo_cap > 0) {
        // snprintf siempre NUL-termina (a menos que tipo_cap == 0).
        snprintf(tipo_out, tipo_cap, "%s",
                 wini_exc_tipo[0] ? wini_exc_tipo : "RuntimeError");
    }
    if (mensaje_out) {
        if (wini_exc_mensaje && wini_exc_mensaje->data) {
            size_t n = (size_t)wini_exc_mensaje->len;
            char *copia = (char *)malloc(n + 1);
            if (copia) {
                memcpy(copia, wini_exc_mensaje->data, n);
                copia[n] = '\0';
            }
            *mensaje_out = copia;
        } else {
            char *vacio = (char *)malloc(1);
            if (vacio) vacio[0] = '\0';
            *mensaje_out = vacio;
        }
    }
    if (linea_out) {
        *linea_out = wini_exc_linea;
    }
}
