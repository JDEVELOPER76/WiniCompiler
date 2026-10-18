#include "runtime.h"
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <setjmp.h>

// ---- contadores de objetos vivos (ver wini_memoria_reporte en runtime.h) ----
// Solo cuentan ENCABEZADOS (un WiniString/List/Dict creado menos uno
// liberado de verdad). Se actualizan únicamente en los constructores y en
// los puntos donde el rc llega a 0 y el encabezado se libera: retener y
// los liberar parciales no los tocan.
static int64_t wini_vivas_cadenas = 0;
static int64_t wini_vivas_listas = 0;
static int64_t wini_vivas_diccs = 0;

// Guarda común de los tres 'retener': un rc que llega a INT64_MAX ya no
// puede subir más; envolverlo a negativo haría que el próximo 'liberar'
// lo tratara como "ya liberado" (rc <= 0) y el objeto se fugaría en
// silencio — o peor, se corrompería el conteo. Terminar con un mensaje
// claro es preferible: en la práctica es inalcanzable (2^63 retenciones
// vivas del mismo objeto) y si se alcanza es un bug, no un programa válido.
static void wini_rc_chequear_overflow(int64_t rc) {
    if (rc == INT64_MAX) {
        fprintf(stderr, "conteo de referencias desbordado en el runtime (demasiados retener sin liberar)\n");
        exit(1);
    }
}

// ---- strings ----
WiniString *wini_string_new(const char *src, int64_t len) {
    // Se valida 'len' antes de calcular el tamaño: '(size_t)len + 1' con
    // un len enorme o negativo envuelve a un valor pequeño (o a 0), y
    // malloc devolvería un bloque diminuto que el memcpy de abajo llena
    // de verdad: desbordamiento de heap. Es el allocator más llamado del
    // runtime, así que la guarda va acá y no en cada llamador.
    if (len < 0 || (uint64_t)len > SIZE_MAX - 1) {
        fprintf(stderr, "longitud de cadena inválida en el runtime\n");
        exit(1);
    }
    WiniString *s = (WiniString *)malloc(sizeof(WiniString));
    if (!s) { fprintf(stderr, "sin memoria para WiniString\n"); exit(1); }
    s->data = (char *)malloc((size_t)len + 1);
    if (!s->data) { fprintf(stderr, "sin memoria para contenido de cadena\n"); exit(1); }
    if (len > 0 && src) memcpy(s->data, src, (size_t)len);
    s->data[len] = '\0';
    s->len = len;
    s->rc = 1;
    wini_vivas_cadenas++;
    return s;
}

WiniString *wini_string_retener(WiniString *s) {
    if (!s) return NULL;
    wini_rc_chequear_overflow(s->rc);
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

// Imprime una lista SIN salto de línea final, para poder anidarla
// ('[[1, 2], [3]]'). Una lista genérica (desc != NULL) sabe imprimir sus
// elementos por sí sola cuando son listas; si son estructuras no hay
// impresión definida y se muestra <?> (el codegen rechaza 'escribir' de
// esas listas en compilación, esto es solo la red de seguridad).
static void wini_print_list_inline(WiniList *l, int tipo) {
    if (!l) { printf("[]"); return; }
    printf("[");
    // Una lista con len > 0 pero data == NULL es un estado inconsistente
    // (no lo produce el runtime, pero un consumidor C podría); sin este
    // chequeo el bucle de abajo calcularía NULL + offset y desreferenciaría.
    if (l->len > 0 && !l->data) { printf("]"); return; }
    if (l->desc) {
        const WiniElemDesc *d = l->desc;
        for (int64_t i = 0; i < l->len; i++) {
            if (i > 0) printf(", ");
            char *slot = (char *)l->data + (size_t)i * (size_t)d->size;
            if (d->kind == WINI_ELEM_LISTA) {
                wini_print_list_inline(*(WiniList **)slot, d->sub);
            } else {
                printf("<?>");
            }
        }
        printf("]");
        return;
    }
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
    printf("]");
}

void wini_print_list(void* list, int tipo) {
    wini_print_list_inline((WiniList *)list, tipo);
    printf("\n");
}
void wini_print_dict(void* dict, int tipoClave, int tipoValor) {
    WiniDict* d = (WiniDict*)dict;
    if (!d) { printf("{}\n"); return; }
    printf("{");
    // Igual que en wini_print_list_inline: no se puede confiar en que
    // len > 0 implica claves/valores no nulos (ver el comentario allá).
    if (d->len > 0 && (!d->claves || !d->valores)) { printf("}\n"); return; }
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
            case WINI_TIPO_GENERICO: { // lista o estructura (ver descVal)
                const WiniElemDesc *desc = d->descVal;
                void *slot = (char *)d->valores + (desc ? (size_t)i * (size_t)desc->size : 0);
                if (!desc) {
                    printf("<?>");
                } else if (desc->kind == WINI_ELEM_LISTA) {
                    wini_print_list_inline(*(WiniList **)slot, desc->sub);
                } else {
                    printf("<?>");
                }
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
    // FIX(bug): si un operando es NULL, antes se devolvía el MISMO puntero
    // del otro operando sin retenerlo. Todo el codegen trata el resultado
    // de 'concat' como un valor recién construido con rc propio (lo guarda
    // en variables, lo concatena de nuevo, etc. y lo libera EXACTAMENTE
    // una vez), así que ese alias sin retener producía doble free /
    // use-after-free apenas dos ubicaciones compartieran el puntero:
    // p. ej. 'cadena c = a + t' con 'a' ya liberada (NULL) devolvía el
    // puntero de 't', y un 'liberar(c)' posterior dejaba a 't' apuntando
    // a memoria liberada (reproducido con AddressSanitizer). Retenemos
    // una referencia extra: el resultado vuelve a comportarse, para el
    // caller, como un valor fresco con rc propio, sin importar de dónde
    // vino el operando no nulo.
    if (!a) return wini_string_retener(b);
    if (!b) return wini_string_retener(a);
    // Camino rápido: concatenar con una cadena vacía es un alias del otro
    // operando (mismo contrato que el camino NULL de arriba: se retiene una
    // referencia extra y el resultado se comporta como valor fresco con rc
    // propio). Evita un malloc + memcpy por cada "" intermedio, que es
    // común en interpolaciones y acumuladores ('s = s + x' con s == "").
    // Invisible para Wini: las cadenas se comparan por contenido.
    if (a->len == 0) return wini_string_retener(b);
    if (b->len == 0) return wini_string_retener(a);
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
    wini_vivas_cadenas++;
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

static void wini_parse_number_error(const char *tipo, WiniString *s, int64_t linea) {
    char msg[256];
    const char *texto = (s && s->data) ? s->data : "";
    snprintf(msg, sizeof(msg), "no se pudo convertir '%s' a %s", texto, tipo);
    wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), linea);
}

int64_t wini_string_to_i64(WiniString *s, int64_t linea) {
    const char *texto = (s && s->data) ? s->data : "";
    char *fin;
    errno = 0;
    long long valor = strtoll(texto, &fin, 10);
    while (*fin == ' ' || *fin == '\t' || *fin == '\r' || *fin == '\n') fin++;
    if (fin == texto || *fin != '\0' || errno == ERANGE) {
        wini_parse_number_error("entero", s, linea);
    }
    return (int64_t)valor;
}

double wini_string_to_f64(WiniString *s, int64_t linea) {
    const char *texto = (s && s->data) ? s->data : "";
    char *fin;
    errno = 0;
    double valor = strtod(texto, &fin);
    while (*fin == ' ' || *fin == '\t' || *fin == '\r' || *fin == '\n') fin++;
    if (fin == texto || *fin != '\0' || errno == ERANGE) {
        wini_parse_number_error("decimal", s, linea);
    }
    return valor;
}

// ---- verificaciones aritméticas ----
// Helper: arma el mensaje y llama a wini_lanzar. No retorna.
static void wini_division_por_cero_error(int64_t linea) {
    static const char *msg = "división por cero";
    wini_lanzar("ErrorMatematico", wini_string_new(msg, (int64_t)strlen(msg)), linea);
    // wini_lanzar nunca retorna: o hace longjmp a un frame 'capturar', o
    // termina el programa.
}

void wini_check_divisor_decimal(double divisor, int64_t linea) {
    // IEEE-754 define x/0.0 como infinito (y 0.0/0.0 como NaN), así que la
    // división decimal por cero NO rompe el proceso: no hay señal que
    // atrapar. Aun así, devolver 'inf' en silencio esconde un error de
    // lógica casi siempre, y además era incoherente con el entero, que sí
    // lanza ErrorMatematico por la misma operación. Se unifica el
    // comportamiento: los dos lanzan, los dos capturables.
    //
    // Se usa comparación exacta contra 0.0 (no epsilon): si el divisor es
    // un numero extremadamente pequeño pero válido, dividir por él es una
    // operación legítima cuyo resultado es grande, y no debe rechazarse.
    if (divisor == 0.0) {
        wini_division_por_cero_error(linea);
    }
}

void wini_check_divisor(int64_t dividendo, int64_t divisor, int64_t linea) {
    if (divisor == 0) {
        wini_division_por_cero_error(linea);
    }
    // El codegen llama a esta función antes de cada sdiv/srem para que un
    // divisor inválido LANZE una excepción capturable en vez de matar el
    // proceso con una señal. Faltaba el segundo caso inválido: en x86-64,
    // 'INT64_MIN / -1' (y su resto) desborda el resultado, la CPU levanta
    // #DE y el proceso muere con SIGFPE, que 'intentar/capturar' no puede
    // atrapar (no es una excepción del lenguaje, es una señal). Se
    // necesitan las DOS_operandos para no rechazar '-1' como divisor, que
    // es perfectly válido para cualquier otro dividendo.
    if (divisor == -1 && dividendo == INT64_MIN) {
        const char *msg = "division por -1 con el minimo entero";
        wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), linea);
    }
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

static void *check_realloc(void *ptr, size_t sz) {
    void *p = realloc(ptr, sz);
    if (!p && sz != 0) { fprintf(stderr, "sin memoria en runtime\n"); exit(1); }
    return p;
}

// Declarada acá porque los constructores de listas la usan para calcular
// el tamaño de la allocación sin riesgo de overflow (la definición está
// más abajo, junto a las listas genéricas).
static size_t wini_bytes_gen(int64_t n, int64_t size);

WiniList *wini_list_new_i64(int64_t *data, int64_t len) {
    WiniList *list = (WiniList *)check_malloc(sizeof(WiniList));
    list->desc = NULL;
    list->len = len;
    list->rc = 1;
    wini_vivas_listas++;
    if (len == 0) {
        list->data = NULL;
        return list;
    }
    // El tamaño se calcula con wini_bytes_gen: 'len * sizeof(int64_t)' sin
    // validar puede envolver size_t y pedirle a malloc un bloque diminuto
    // que después se llena de 'len' elementos (desbordamiento de heap). Es
    // alcanzable desde Wini con 'rango(0, 2^61)'.
    size_t bytes = wini_bytes_gen(len, (int64_t)sizeof(int64_t));
    int64_t *copy = (int64_t *)check_malloc(bytes);
    memcpy(copy, data, bytes);
    list->data = copy;
    return list;
}

WiniList *wini_list_new_f64(double *data, int64_t len) {
    WiniList *list = (WiniList *)check_malloc(sizeof(WiniList));
    list->desc = NULL;
    list->len = len;
    list->rc = 1;
    wini_vivas_listas++;
    if (len == 0) { list->data = NULL; return list; }
    size_t bytes = wini_bytes_gen(len, (int64_t)sizeof(double));
    double *copy = (double *)check_malloc(bytes);
    memcpy(copy, data, bytes);
    list->data = copy;
    return list;
}

WiniList *wini_list_new_i1(int8_t *data, int64_t len) {
    WiniList *list = (WiniList *)check_malloc(sizeof(WiniList));
    list->desc = NULL;
    list->len = len;
    list->rc = 1;
    wini_vivas_listas++;
    if (len == 0) { list->data = NULL; return list; }
    size_t bytes = wini_bytes_gen(len, (int64_t)sizeof(int8_t));
    int8_t *copy = (int8_t *)check_malloc(bytes);
    memcpy(copy, data, bytes);
    list->data = copy;
    return list;
}

WiniList *wini_list_new_str(WiniString **data, int64_t len) {
    WiniList *list = (WiniList *)check_malloc(sizeof(WiniList));
    list->desc = NULL;
    list->len = len;
    list->rc = 1;
    wini_vivas_listas++;
    if (len == 0) { list->data = NULL; return list; }
    size_t bytes = wini_bytes_gen(len, (int64_t)sizeof(WiniString *));
    WiniString **copy = (WiniString **)check_malloc(bytes);
    memcpy(copy, data, bytes);
    list->data = copy;
    return list;
}

WiniList *wini_list_retener(WiniList *lista) {
    if (!lista) return NULL;
    wini_rc_chequear_overflow(lista->rc);
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

// Concatenar dos listas primitivas. SIEMPRE devuelve una lista NUEVA e
// independiente (nunca un alias de un operando): antes se devolvía el
// mismo puntero cuando el otro operando era NULL, y liberar cualquiera de
// las dos ubicaciones dejaba a la otra colgando.
WiniList* wini_list_concat(WiniList* a, WiniList* b, int tipo) {
    // Red de seguridad: el codegen usa wini_list_concat_gen para listas
    // genéricas, pero si alguien (p. ej. código C externo) llega acá con
    // una, se delega en vez de interpretar mal sus elementos.
    if ((a && a->desc) || (b && b->desc)) {
        return wini_list_concat_gen(a, b, (a && a->desc) ? a->desc : b->desc);
    }
    int64_t la = a ? a->len : 0;
    int64_t lb = b ? b->len : 0;
    // 'la + lb' puede desbordar int64_t (comportamiento indefinido en C)
    // y, peor, el producto con elemSize puede envolver size_t: la malloc
    // pediría un bloque diminuto que los dos memcpy de abajo llenan con
    // len(a)+len(b) elementos reales (desbordamiento de heap). Se valida
    // la suma antes de seguir, y el tamaño pasa por wini_bytes_gen.
    if (la < 0 || lb < 0 || la > INT64_MAX - lb) {
        fprintf(stderr, "tamaño de lista inválido en concatenación\n");
        exit(1);
    }
    int64_t newLen = la + lb;
    size_t elemSize;
    switch (tipo) {
        case 0: // i64
            elemSize = sizeof(int64_t);
            break;
        case 1: // f64
            elemSize = sizeof(double);
            break;
        case 2: // i1
            elemSize = sizeof(int8_t);
            break;
        case 3: // str (punteros)
            elemSize = sizeof(WiniString*);
            break;
        default:
            fprintf(stderr, "tipo de lista no soportado para concatenación\n");
            exit(1);
    }
    // Una sola allocación y una sola copia de cada mitad, directo en el
    // arreglo definitivo del resultado (antes se armaba un buffer temporal
    // que el constructor volvía a copiar: el doble de tránsito de memoria
    // y una allocación extra del tamaño completo).
    WiniList *result = (WiniList *)check_malloc(sizeof(WiniList));
    result->desc = NULL;
    result->len = newLen;
    result->rc = 1;
    wini_vivas_listas++;
    if (newLen == 0) {
        result->data = NULL;
        return result;
    }
    size_t bytesTotal = wini_bytes_gen(newLen, (int64_t)elemSize);
    size_t bytesA = wini_bytes_gen(la, (int64_t)elemSize);
    size_t bytesB = wini_bytes_gen(lb, (int64_t)elemSize);
    result->data = check_malloc(bytesTotal);
    if (bytesA && a && a->data) memcpy(result->data, a->data, bytesA);
    if (bytesB && b && b->data) memcpy((char *)result->data + bytesA, b->data, bytesB);
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


// ---- listas genéricas (elementos lista o estructura) ----

// Retiene/libera UN elemento (slot) de una lista genérica según su
// descriptor. Retener devuelve nada: el slot ya contiene el puntero.
static void wini_desc_retener_slot(const WiniElemDesc *d, void *slot) {
    if (d->kind == WINI_ELEM_LISTA) {
        wini_list_retener(*(WiniList **)slot);
    } else if (d->retener) {
        d->retener(slot);
    }
}

static void wini_desc_liberar_slot(const WiniElemDesc *d, void *slot) {
    if (d->kind == WINI_ELEM_LISTA) {
        wini_liberar_lista(*(WiniList **)slot, d->sub);
    } else if (d->liberar) {
        d->liberar(slot);
    }
}

// Tamaño en bytes de 'n' elementos de 'size' bytes, abortando si se
// desborda size_t (un len corrupto no debe traducirse en un malloc
// chico seguido de un memcpy enorme).
static size_t wini_bytes_gen(int64_t n, int64_t size) {
    if (n < 0 || size <= 0 || (uint64_t)n > SIZE_MAX / (uint64_t)size) {
        fprintf(stderr, "tamaño de lista inválido en el runtime\n");
        exit(1);
    }
    return (size_t)n * (size_t)size;
}

WiniList *wini_list_new_gen(const WiniElemDesc *desc, const void *data, int64_t len) {
    WiniList *list = (WiniList *)check_malloc(sizeof(WiniList));
    list->len = len;
    list->rc = 1;
    wini_vivas_listas++;
    list->desc = desc;
    if (len == 0 || !data) {
        list->len = 0;
        list->data = NULL;
        return list;
    }
    size_t bytes = wini_bytes_gen(len, desc->size);
    list->data = check_malloc(bytes);
    memcpy(list->data, data, bytes);
    return list;
}

void *wini_list_slot(WiniList *list, int64_t idx, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    // Una lista primitiva tiene desc == NULL (es el caso común: el campo
    // solo existe para las listas de listas/estructuras). Antes se
    // desreferenciaba sin verificar, así que llamar a wini_list_slot con una
    // lista primitiva —que es lo natural, dado que el nombre no dice
    // 'gen'— era un NULL-deref inmediato. Se avisa con el mismo tipo de
    // error de índice que ya usa la función, en vez de colgarse.
    if (!list->desc) {
        fprintf(stderr, "wini_list_slot: la lista no es genérica (idx=%lld)\n", (long long)idx);
        exit(1);
    }
    return (char *)list->data + (size_t)idx * (size_t)list->desc->size;
}

// wini_list_slot_str devuelve la DIRECCIÓN de la ranura del elemento 'idx'
// en una lista de cadenas (lista<cadena>), para poder escribirle un valor
// (típicamente NULL) sin liberar antes lo que ahí estaba.
//
// Es la variante que necesita 'liberar(l[0])': el builtin suelta una
// referencia de la cadena del elemento, pero la ranura seguía apuntando a la
// memoria liberada, y un 'liberar(l)' posterior la liberaba otra vez (doble
// free confirmado con AddressSanitizer). Es distinta de wini_list_slot porque
// una lista de cadenas es PRIMITIVA (desc == NULL): sus elementos son
// punteros de tamaño fijo, no elementos descritos por un WiniElemDesc.
WiniString **wini_list_slot_str(WiniList *list, int64_t idx, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    if (list->desc) {
        fprintf(stderr, "wini_list_slot_str: la lista no es de cadenas (idx=%lld)\n", (long long)idx);
        exit(1);
    }
    WiniString **elems = (WiniString **)list->data;
    return &elems[idx];
}

void wini_list_set_gen(WiniList *list, int64_t idx, const void *valor, const WiniElemDesc *desc, int64_t linea) {
    if (!list || idx < 0 || idx >= list->len) {
        // No hay dónde guardar el valor nuevo: las referencias que el
        // codegen ya nos transfirió se liberan para no fugarlas (mismo
        // criterio que wini_list_set_str). Por eso el descriptor viene
        // aparte: la lista puede ser NULL.
        wini_desc_liberar_slot(desc, (void *)valor);
        wini_list_index_error(idx, list ? list->len : 0, linea);
    }
    // Una lista no puede contenerse a sí misma: un conteo de referencias
    // nunca libera un ciclo (la lista se retendría a sí misma para
    // siempre), y además wini_liberar_lista recursaría hasta el tope de
    // profundidad y fugaría igual. Se rechaza con ValueError capturable,
    // liberando la referencia entrante igual que en el camino OOB de
    // arriba. (Los ciclos INDIRECTOS a[0]=b; b[0]=a, o vía estructuras,
    // siguen siendo responsabilidad del programa: romperlos a mano antes
    // de liberar. Ver la nota de ciclos en runtime.h.)
    if (desc->kind == WINI_ELEM_LISTA && *(WiniList *const *)valor == list) {
        wini_desc_liberar_slot(desc, (void *)valor);
        const char *msg = "una lista no puede contenerse a si misma (ciclo de referencias)";
        wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), linea);
    }
    void *slot = (char *)list->data + (size_t)idx * (size_t)desc->size;
    // Primero se libera lo que se pisa y después se copia lo nuevo. Esto
    // es correcto aunque el valor nuevo comparta referencias con el viejo
    // (xs[i] = xs[j], o xs[i] = xs[i]): el codegen retuvo una referencia
    // propia para el valor nuevo ANTES de llamar, así que liberar el
    // viejo nunca llega a cero lo que el nuevo todavía necesita.
    wini_desc_liberar_slot(desc, slot);
    memcpy(slot, valor, (size_t)desc->size);
}

WiniList *wini_list_concat_gen(WiniList *a, WiniList *b, const WiniElemDesc *desc) {
    int64_t la = a ? a->len : 0;
    int64_t lb = b ? b->len : 0;
    // Igual que en wini_list_concat: la suma sin validar puede desbordar
    // int64_t y el producto con desc->size envolver size_t (antes este
    // camino genérico no validaba nada, a diferencia del primitivo).
    if (la < 0 || lb < 0 || la > INT64_MAX - lb) {
        fprintf(stderr, "tamaño de lista inválido en concatenación\n");
        exit(1);
    }
    WiniList *res = (WiniList *)check_malloc(sizeof(WiniList));
    res->rc = 1;
    wini_vivas_listas++;
    res->desc = desc;
    res->len = la + lb;
    if (res->len == 0) { res->data = NULL; return res; }
    size_t bytes = wini_bytes_gen(res->len, desc->size);
    res->data = check_malloc(bytes);
    size_t bytesA = (size_t)la * (size_t)desc->size;
    if (la > 0 && a->data) memcpy(res->data, a->data, bytesA);
    if (lb > 0 && b->data) memcpy((char *)res->data + bytesA, b->data, (size_t)lb * (size_t)desc->size);
    // Los elementos copiados ya existían en 'a'/'b': la lista nueva es un
    // dueño más de cada uno (mismo criterio que wini_retener_elementos_str).
    for (int64_t i = 0; i < res->len; i++) {
        wini_desc_retener_slot(desc, (char *)res->data + (size_t)i * (size_t)desc->size);
    }
    return res;
}

// ---- insertar / quitar in place (builtins 'insertar', 'quitar' y 'agregar') ----
// Mutación EN EL LUGAR (como append de Python): la lista original cambia
// y se devuelve el MISMO puntero, así valen los dos estilos:
//   agregar(l, x)        (sentencia: muta)
//   l = agregar(l, x)    (reasignación redundante pero válida)
// Los alias ven el cambio (misma referencia). El valor insertado se ADOPTA
// (el codegen ya lo retuvo si venía de otra ubicación, igual que en
// wini_list_set_str); al quitar, el elemento que sale se LIBERA (la lista
// pierde una referencia).
// Índices válidos: 0..len para insertar (i == len agrega al final),
// 0..len-1 para quitar. Fuera de rango -> IndexError capturable con la
// línea del fuente que llama (wini_list_index_error).
// Si la lista es NULL: insertar crea una lista nueva (solo sirve
// reasignando el resultado); quitar -> IndexError.

// Núcleo de inserción primitiva in place: agranda el arreglo, corre el
// sufijo con memmove (se solapa) y guarda el valor nuevo. Sin retenciones:
// los elementos siguen en el mismo objeto (mismos dueños) y el valor se
// adopta por contrato.
static WiniList *wini_list_insertar_base(WiniList *origen, int64_t idx, const void *valor,
                                         int64_t elemSize, int64_t linea) {
    int64_t n = origen ? origen->len : 0;
    if (idx < 0 || idx > n) {
        wini_list_index_error(idx, n, linea);
    }
    if (n >= INT64_MAX) {
        fprintf(stderr, "tamaño de lista inválido en insertar\n");
        exit(1);
    }
    if (!origen) {
        WiniList *nueva = (WiniList *)check_malloc(sizeof(WiniList));
        nueva->desc = NULL;
        nueva->len = 1;
        nueva->rc = 1;
        wini_vivas_listas++;
        nueva->data = check_malloc(wini_bytes_gen(1, elemSize));
        if (valor) memcpy(nueva->data, valor, (size_t)elemSize);
        return nueva;
    }
    int64_t nuevoLen = n + 1;
    origen->data = check_realloc(origen->data, wini_bytes_gen(nuevoLen, elemSize));
    char *dst = (char *)origen->data;
    size_t bytesPref = wini_bytes_gen(idx, elemSize);
    int64_t resto = n - idx;
    if (resto > 0) {
        memmove(dst + bytesPref + (size_t)elemSize, dst + bytesPref,
                wini_bytes_gen(resto, elemSize));
    }
    if (valor) memcpy(dst + bytesPref, valor, (size_t)elemSize);
    origen->len = nuevoLen;
    return origen;
}

WiniList *wini_list_insert_i64(WiniList *list, int64_t idx, int64_t valor, int64_t linea) {
    return wini_list_insertar_base(list, idx, &valor, (int64_t)sizeof(int64_t), linea);
}

WiniList *wini_list_insert_f64(WiniList *list, int64_t idx, double valor, int64_t linea) {
    return wini_list_insertar_base(list, idx, &valor, (int64_t)sizeof(double), linea);
}

WiniList *wini_list_insert_i1(WiniList *list, int64_t idx, int8_t valor, int64_t linea) {
    return wini_list_insertar_base(list, idx, &valor, (int64_t)sizeof(int8_t), linea);
}

WiniList *wini_list_insert_str(WiniList *list, int64_t idx, WiniString *valor, int64_t linea) {
    return wini_list_insertar_base(list, idx, &valor, (int64_t)sizeof(WiniString *), linea);
}

WiniList *wini_list_insert_gen(WiniList *list, int64_t idx, const void *valor,
                              const WiniElemDesc *desc, int64_t linea) {
    int64_t n = list ? list->len : 0;
    if (idx < 0 || idx > n) {
        wini_list_index_error(idx, n, linea);
    }
    if (n < 0 || n >= INT64_MAX) {
        fprintf(stderr, "tamaño de lista inválido en insertar\n");
        exit(1);
    }
    if (!list) {
        WiniList *nueva = (WiniList *)check_malloc(sizeof(WiniList));
        nueva->rc = 1;
        wini_vivas_listas++;
        nueva->desc = desc;
        nueva->len = 1;
        nueva->data = check_malloc(wini_bytes_gen(1, desc->size));
        // El valor se adopta (el codegen lo retuvo si era compartido).
        if (valor) memcpy(nueva->data, valor, (size_t)desc->size);
        return nueva;
    }
    int64_t nuevoLen = n + 1;
    list->data = check_realloc(list->data, wini_bytes_gen(nuevoLen, desc->size));
    char *dst = (char *)list->data;
    size_t bytesPref = (size_t)idx * (size_t)desc->size;
    int64_t resto = n - idx;
    if (resto > 0) {
        memmove(dst + bytesPref + (size_t)desc->size, dst + bytesPref,
                (size_t)resto * (size_t)desc->size);
    }
    if (valor) memcpy(dst + bytesPref, valor, (size_t)desc->size);
    list->len = nuevoLen;
    // Sin retener/liberar: los elementos siguen en el mismo objeto y el
    // valor nuevo se adopta por contrato.
    return list;
}

WiniList *wini_list_remove(WiniList *list, int64_t idx, int tipo, int64_t linea) {
    int64_t n = list ? list->len : 0;
    if (idx < 0 || idx >= n) {
        wini_list_index_error(idx, n, linea);
    }
    int64_t elemSize;
    switch (tipo) {
        case 0: elemSize = (int64_t)sizeof(int64_t); break;
        case 1: elemSize = (int64_t)sizeof(double); break;
        case 2: elemSize = (int64_t)sizeof(int8_t); break;
        case 3: elemSize = (int64_t)sizeof(WiniString *); break;
        default:
            fprintf(stderr, "tipo de lista no soportado para quitar\n");
            exit(1);
    }
    // La lista pierde una referencia al elemento que sale.
    if (tipo == 3) {
        wini_liberar_cadena(((WiniString **)list->data)[idx]);
    }
    char *dst = (char *)list->data;
    size_t bytesPref = wini_bytes_gen(idx, elemSize);
    int64_t resto = n - idx - 1;
    if (resto > 0) {
        memmove(dst + bytesPref, dst + bytesPref + (size_t)elemSize,
                wini_bytes_gen(resto, elemSize));
    }
    list->len = n - 1;
    if (list->len == 0) {
        free(list->data);
        list->data = NULL;
        return list;
    }
    list->data = check_realloc(list->data, wini_bytes_gen(list->len, elemSize));
    return list;
}

WiniList *wini_list_remove_gen(WiniList *list, int64_t idx, const WiniElemDesc *desc, int64_t linea) {
    int64_t n = list ? list->len : 0;
    if (idx < 0 || idx >= n) {
        wini_list_index_error(idx, n, linea);
    }
    // La lista pierde las referencias del elemento que sale.
    wini_desc_liberar_slot(desc, (char *)list->data + (size_t)idx * (size_t)desc->size);
    char *dst = (char *)list->data;
    size_t bytesPref = (size_t)idx * (size_t)desc->size;
    int64_t resto = n - idx - 1;
    if (resto > 0) {
        memmove(dst + bytesPref, dst + bytesPref + (size_t)desc->size,
                (size_t)resto * (size_t)desc->size);
    }
    list->len = n - 1;
    if (list->len == 0) {
        free(list->data);
        list->data = NULL;
        return list;
    }
    list->data = check_realloc(list->data, wini_bytes_gen(list->len, desc->size));
    return list;
}


// ---- diccionarios ----

WiniDict *wini_dict_new(void) {
    WiniDict *d = (WiniDict *)check_malloc(sizeof(WiniDict));
    d->len = 0;
    d->cap = 0;
    d->claves = NULL;
    d->valores = NULL;
    d->rc = 1;
    wini_vivas_diccs++;
    d->descVal = NULL; // valores primitivos hasta el primer set_*_gen
    return d;
}

WiniDict *wini_dict_retener(WiniDict *dict) {
    if (!dict) return NULL;
    wini_rc_chequear_overflow(dict->rc);
    dict->rc++;
    return dict;
}

int64_t wini_dict_len(WiniDict *dict) {
    if (!dict) return 0;
    return dict->len;
}

// Búsqueda de índice por clave (una implementación por tipo de clave).
static int64_t wini_dict_find_i64(WiniDict *d, int64_t clave) {
    // Tolerante a NULL como wini_dict_find_str: un diccionario inexistente
    // se comporta como uno vacío en vez de colgar el proceso. Todo el
    // bridge (wini_bridge.c) asume que un handle NULL es válido.
    if (!d || !d->claves) return -1;
    int64_t *claves = (int64_t *)d->claves;
    for (int64_t i = 0; i < d->len; i++) {
        if (claves[i] == clave) return i;
    }
    return -1;
}

static int64_t wini_dict_find_f64(WiniDict *d, double clave) {
    if (!d || !d->claves) return -1;
    double *claves = (double *)d->claves;
    for (int64_t i = 0; i < d->len; i++) {
        if (claves[i] == clave) return i;
    }
    return -1;
}

static int64_t wini_dict_find_i1(WiniDict *d, int8_t clave) {
    if (!d || !d->claves) return -1;
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
    // Se valida newCap * keySize (y * valSize) ANTES de realloc: sin este
    // chequeo, un 'cap * 2' desbordado o un producto que envuelve size_t
    // le pide a realloc un tamaño pequeño, tiene éxito, y la escritura de
    // la entrada nueva se va de lasHeap. Se reutiliza wini_bytes_gen, el
    // mismo helper que ya usan las listas genéricas para esto.
    if (d->cap > INT64_MAX / 2) {
        fprintf(stderr, "diccionario demasiado grande en el runtime\n");
        exit(1);
    }
    int64_t newCap = d->cap == 0 ? 4 : d->cap * 2;
    size_t bytesClaves = wini_bytes_gen(newCap, (int64_t)keySize);
    size_t bytesValores = wini_bytes_gen(newCap, (int64_t)valSize);
    void *nuevasClaves = realloc(d->claves, bytesClaves);
    void *nuevosValores = realloc(d->valores, bytesValores);
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

// Genera wini_dict_keys_<KSUF> reusando el constructor de WiniList del tipo.
// Un diccionario NULL (o todavía sin claves) devuelve una lista vacía en vez
// de desreferenciar el puntero: el bridge trata un handle NULL como válido.
#define WINI_DICT_KEYS(KSUF, KTYPE, LISTNEWFN, KRETAIN_EACH)                \
    WiniList *wini_dict_keys_##KSUF(WiniDict *d) {                         \
        if (!d) return LISTNEWFN(NULL, 0);                                 \
        WiniList *lista = LISTNEWFN((KTYPE *)d->claves, d->len);           \
        KRETAIN_EACH(lista);                                               \
        return lista;                                                      \
    }

// Genera wini_dict_values_<VSUF> reusando el constructor de WiniList
#define WINI_DICT_VALUES(VSUF, VTYPE, LISTNEWFN, VRETAIN_EACH)             \
    WiniList *wini_dict_values_##VSUF(WiniDict *d) {                       \
        if (!d) return LISTNEWFN(NULL, 0);                                 \
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

// ---- diccionarios con valores genéricos (lista o estructura) ----

// Genera wini_dict_set_<KSUF>_gen: guarda una COPIA del valor genérico
// (desc->size bytes) y adopta sus referencias (el codegen ya las retuvo
// si el valor venía de otra ubicación, igual que wini_list_set_gen). La
// clave sigue las reglas de WINI_DICT_SET (se adopta; en reasignación se
// libera la entrante). Además guarda 'desc' en el diccionario (descVal)
// para que liberar/imprimir no necesiten parámetros extra.
#define WINI_DICT_SET_GEN(KSUF, KTYPE, KRELEASE)                              \
    void wini_dict_set_##KSUF##_gen(WiniDict *d, KTYPE clave,                 \
                                     const void *valor,                       \
                                     const WiniElemDesc *desc) {              \
        if (!d || !desc || !valor) {                                          \
            fprintf(stderr, "wini_dict_set_" #KSUF "_gen: diccionario, descriptor o valor nulo\n"); \
            exit(1);                                                          \
        }                                                                     \
        int64_t idx = wini_dict_find_##KSUF(d, clave);                        \
        if (idx >= 0) {                                                       \
            void *slot = (char *)d->valores + (size_t)idx * (size_t)desc->size; \
            wini_desc_liberar_slot(desc, slot);                               \
            memcpy(slot, valor, (size_t)desc->size);                          \
            d->descVal = desc;                                                \
            KRELEASE(clave);                                                  \
            return;                                                           \
        }                                                                     \
        wini_dict_grow(d, sizeof(KTYPE), (size_t)desc->size);                 \
        ((KTYPE *)d->claves)[d->len] = clave;                                 \
        memcpy((char *)d->valores + (size_t)d->len * (size_t)desc->size,      \
               valor, (size_t)desc->size);                                    \
        d->descVal = desc;                                                    \
        d->len++;                                                             \
    }

WINI_DICT_SET_GEN(i64, int64_t, WINI_NOOP_RELEASE)
WINI_DICT_SET_GEN(f64, double, WINI_NOOP_RELEASE)
WINI_DICT_SET_GEN(i1, int8_t, WINI_NOOP_RELEASE)
WINI_DICT_SET_GEN(str, WiniString *, wini_liberar_cadena)

// Genera wini_dict_slot_<KSUF>_gen: dirección del slot del valor de
// 'clave' (KeyError capturable si falta, como wini_dict_get_*). Lectura
// prestada: el valor sigue siendo del diccionario. Para claves cadena
// consume UNA referencia, igual que wini_dict_get_str_* (misma razón:
// el camino de excepción hace longjmp y el caller nunca llegaría a
// liberar una clave fresca).
#define WINI_DICT_SLOT_GEN(KSUF, KTYPE, KRELEASE)                             \
    void *wini_dict_slot_##KSUF##_gen(WiniDict *d, KTYPE clave,               \
                                       const WiniElemDesc *desc,               \
                                       int64_t linea) {                       \
        (void)linea;                                                          \
        if (!desc) {                                                          \
            fprintf(stderr, "wini_dict_slot_" #KSUF "_gen: descriptor nulo\n"); \
            exit(1);                                                          \
        }                                                                     \
        int64_t idx = wini_dict_find_##KSUF(d, clave);                        \
        if (idx < 0) wini_dict_key_error_##KSUF(clave); /* no retorna */      \
        void *slot = (char *)d->valores + (size_t)idx * (size_t)desc->size;   \
        KRELEASE(clave);                                                      \
        return slot;                                                          \
    }

WINI_DICT_SLOT_GEN(i64, int64_t, WINI_NOOP_RELEASE)
WINI_DICT_SLOT_GEN(f64, double, WINI_NOOP_RELEASE)
WINI_DICT_SLOT_GEN(i1, int8_t, WINI_NOOP_RELEASE)
WINI_DICT_SLOT_GEN(str, WiniString *, wini_liberar_cadena)

// Valores como lista genérica nueva e independiente (cada elemento
// copiado se retiene: la lista resultante y el diccionario pueden
// liberarse en cualquier orden). Un dict NULL o vacío da lista vacía.
WiniList *wini_dict_values_gen(WiniDict *d, const WiniElemDesc *desc) {
    if (!desc) {
        fprintf(stderr, "wini_dict_values_gen: descriptor nulo\n");
        exit(1);
    }
    if (!d || d->len == 0) {
        return wini_list_new_gen(desc, NULL, 0);
    }
    WiniList *lista = wini_list_new_gen(desc, d->valores, d->len);
    for (int64_t i = 0; i < lista->len; i++) {
        wini_desc_retener_slot(desc, (char *)lista->data + (size_t)i * (size_t)desc->size);
    }
    return lista;
}

// wini_dict_slot_str devuelve la DIRECCIÓN de la ranura que guarda el valor
// de 'clave', para que quien la reciba pueda dejarle NULL (o escribir otro
// valor) sin liberar antes lo que ahí estaba.
//
// Existe por el caso 'liberar(d[clave])' en código Wini: el builtin suelta
// una referencia del valor, pero la ranura del diccionario seguía
// apuntando a la memoria liberada, y un 'liberar(d)' posterior la liberaba
// otra vez (doble free). Devuelve NULL si la clave no está en el diccionario
// (entonces no hay nada que anular) o si el diccionario es NULL.
//
// Solo para claves de tipo cadena, que es el único caso que el codegen
// necesita anular (los valores son cadenas).
WiniString **wini_dict_slot_str(WiniDict *d, WiniString *clave) {
    if (!d || !d->claves || !d->valores) return NULL;
    int64_t i = wini_dict_find_str(d, clave);
    if (i < 0) return NULL;
    WiniString **valores = (WiniString **)d->valores;
    return &valores[i];
}

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
    // '-step' no se puede calcular si step == INT64_MIN (desborda el
    // signo). Se detecta y se avisa acá, antes de usarlo en el cálculo de
    // la longitud, en vez de seguir con comportamiento indefinido.
    if (step == INT64_MIN) {
        wini_lanzar("ValueError", wini_string_new("step demasiado pequeno", (int64_t)strlen("step demasiado pequeno")), 0);
    }
    // Calcular la longitud. El truco clásico '(end - start + step - 1) / step'
    // desborda con signo (comportamiento indefinido en C) en cuanto el
    // rango se acerca a los extremos de int64_t, y el resultado —con el
    // desbordamiento ya ocurrido— puede dar negativo. Un 'len' negativo se
    // guardaba tal cual en list->len y convertía todos los bucles que
    // recorren la lista (liberar, imprimir) en no-ops silenciosos que
    // fugaban todos los elementos. Se calcula dividiendo la distancia por
    // el paso con resto, que no desborda, y se valida antes de usarlo.
    int64_t len = 0;
    if (step > 0) {
        if (start < end) {
            // distance = end - start, sin desborde porque end > start.
            int64_t distancia = end - start;
            len = distancia / step;
            if (distancia % step != 0) len++;
        }
    } else {
        if (start > end) {
            // distancia = start - end, sin desborde porque start > end.
            int64_t distancia = start - end;
            int64_t paso = -step;  // step != INT64_MIN gracias al check de abajo
            len = distancia / paso;
            if (distancia % paso != 0) len++;
        }
    }
    if (len < 0) {
        wini_lanzar("ValueError", wini_string_new("rango demasiado grande", (int64_t)strlen("rango demasiado grande")), 0);
    }
    // Crear la lista
    WiniList *list = (WiniList*)check_malloc(sizeof(WiniList));
    list->desc = NULL;
    list->len = len;
    list->rc = 1;           // <-- faltaba en el caso len == 0 (antes del return)
    wini_vivas_listas++;
    if (len == 0) {
        list->data = NULL;
        return list;
    }
    size_t bytes = wini_bytes_gen(len, (int64_t)sizeof(int64_t));
    int64_t *data = (int64_t*)check_malloc(bytes);
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
    // verdad cuando ya no queda ningún dueño.
    //
    // Si 'rc' ya estaba en 0 (uso incorrecto: liberar más veces de las que
    // se retuvo), NO hay que volver a hacer free(): el bloque ya se
    // liberó y 's' es un puntero colgante, así que ni siquiera es válido
    // leer 's->rc' — y menos lo que hace el código anterior, que caía
    // igual al free() y provocaba un doble free (o sea, corrupción de
    // memoria) en vez de ser inocuo como prometía el comentario.
    if (s->rc <= 0) return;
    s->rc--;
    if (s->rc > 0) return;
    wini_vivas_cadenas--;
    if (s->data) free(s->data);
    free(s);
}

// Profundidad máxima de anidamiento que se sigue al liberar una lista de
// listas. El código generado por Wini nunca pasa de unas pocas decenas (la
// profundidad la fija el anidamiento de la anotación de tipo), así que el
// tope no afecta a los programas normales; existe para que una lista
// construída a mano desde C, con una cadena de 'desc->sub' larga o
// circular, no desborde el stack con una recursión sin límite.
#define WINI_PROFUNDIDAD_MAX 256

static void wini_liberar_lista_prof(WiniList *lista, int tipo, int profundidad) {
    if (!lista) return;
    // Mismo criterio que wini_liberar_cadena: si el contador ya estaba en
    // 0 el bloque ya se liberó y 'lista' es un puntero colgante; volver a
    // hacer free() sería un doble free.
    if (lista->rc <= 0) return;
    lista->rc--;
    if (lista->rc > 0) return;
    if (profundidad >= WINI_PROFUNDIDAD_MAX) {
        fprintf(stderr, "liberacion de lista: se supero la profundidad maxima (%d); "
                        "quedan referencias sin liberar\n", WINI_PROFUNDIDAD_MAX);
        // El encabezado queda fugado a propósito (no se puede liberar sin
        // recursar): el contador de vivas NO se decrementa para que
        // wini_memoria_reporte() delate la fuga en vez de ocultarla.
        return;
    }
    if (lista->desc) {
        // Lista genérica: cada elemento libera lo suyo según su
        // descriptor ('tipo' no se usa: manda lo que la lista declara).
        const WiniElemDesc *d = lista->desc;
        if (lista->data) {
            for (int64_t i = 0; i < lista->len; i++) {
                void *slot = (char *)lista->data + (size_t)i * (size_t)d->size;
                if (d->kind == WINI_ELEM_LISTA) {
                    wini_liberar_lista_prof(*(WiniList **)slot, d->sub, profundidad + 1);
                } else if (d->liberar) {
                    d->liberar(slot);
                }
            }
            free(lista->data);
        }
        wini_vivas_listas--;
        free(lista);
        return;
    }
    if (tipo == 3 && lista->data) { // str: liberar una referencia de cada WiniString
        WiniString **elems = (WiniString **)lista->data;
        for (int64_t i = 0; i < lista->len; i++) {
            wini_liberar_cadena(elems[i]);
        }
    }
    if (lista->data) free(lista->data);
    wini_vivas_listas--;
    free(lista);
}

void wini_liberar_lista(WiniList *lista, int tipo) {
    wini_liberar_lista_prof(lista, tipo, 0);
}

// Contador de anidamiento de wini_liberar_diccionario en curso. Los
// valores genéricos se liberan vía thunks que pueden volver acá (una
// estructura con campo diccionario): ante un ciclo de referencias
// (diccionario que se alcanza a sí mismo) la recursión sería infinita.
// Igual que WINI_PROFUNDIDAD_MAX en listas, el tope convierte un
// desborde de stack en una fuga avisada. Es un contador estático porque
// la liberación es sincrónica (ningún liberar_* lanza); el runtime ya
// asume un solo hilo.
static int wini_liberar_dic_prof = 0;

void wini_liberar_diccionario(WiniDict *dict, int tipoClave, int tipoValor) {
    if (!dict) return;
    // Mismo criterio que wini_liberar_cadena: rc en 0 = bloque ya
    // liberado; liberarlo otra vez sería un doble free.
    if (dict->rc <= 0) return;
    dict->rc--;
    if (dict->rc > 0) return;
    if (wini_liberar_dic_prof >= WINI_PROFUNDIDAD_MAX) {
        fprintf(stderr, "liberacion de diccionario: se supero la profundidad maxima (%d); "
                        "quedan referencias sin liberar\n", WINI_PROFUNDIDAD_MAX);
        // El encabezado queda fugado a propósito (igual que en la lista);
        // el contador no se tocó (se vuelve antes de incrementarlo) para
        // no envenenar liberaciones futuras no relacionadas con el ciclo.
        return;
    }
    wini_liberar_dic_prof++;
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
    if (tipoValor == WINI_TIPO_GENERICO && dict->valores) { // valores lista/estructura
        const WiniElemDesc *desc = dict->descVal;
        if (!desc) {
            // Solo llegable desde C externo (el codegen siempre guardó el
            // descriptor en el primer set_*_gen): no hay cómo liberar los
            // valores sin saber su layout; se avisa en vez de corromper.
            fprintf(stderr, "wini_liberar_diccionario: diccionario genérico sin descriptor; valores no liberados\n");
        } else {
            for (int64_t i = 0; i < dict->len; i++) {
                wini_desc_liberar_slot(desc, (char *)dict->valores + (size_t)i * (size_t)desc->size);
            }
        }
    }
    if (dict->claves) free(dict->claves);
    if (dict->valores) free(dict->valores);
    wini_vivas_diccs--;
    free(dict);
    wini_liberar_dic_prof--;
}

// ---- raíces de excepción (ver runtime.h) ----
//
// Cada entrada apunta a la RANURA (alloca o campo) donde vive un valor
// heap propiedad de una función Wini en curso, más cómo liberarlo. El
// codegen registra una por cada variable local heap al entrar a la
// función (una sola vez: los allocas viven en el bloque de entrada) y
// poda las suyas al retornar; 'lanzar'/'relanzar' liberan antes de saltar
// lo de invocaciones abandonadas (fid mayor al destino) y conservan lo
// demás.
//
// Invariante: en el camino normal las entradas solo se DESCARTAN (nunca
// se liberan: el 'liberar' explícito ya dejó NULL en esas ranuras, y las
// reasignaciones guardaron el valor nuevo). Solo el desenrollado de una
// excepción libera, y lo hace con todos los frames todavía vivos, así que
// ninguna ranura leída apunta a un stack muerto.
typedef struct {
    void *ranura;  // dirección del puntero (WiniString**/WiniList**/WiniDict**)
    int32_t kind;  // 0=cadena, 1=lista, 2=diccionario
    int32_t aux1;  // lista: tipo de elemento; dicc: tipo de clave
    int32_t aux2;  // dicc: tipo de valor
    // fid de la invocación dueña (ver wini_invocacion_nueva): el
    // desenrollado libera solo lo estrictamente más nuevo (llamados
    // abandonados) y conserva lo propio y lo exterior (siguen vivos).
    int64_t fid;
} WiniRaiz;

static WiniRaiz *wini_raices = NULL;
static int64_t wini_raices_len = 0;
static int64_t wini_raices_cap = 0;

// Contador de invocaciones de funciones Wini, estrictamente creciente:
// como los llamados anidan, un fid mayor implica invocación más nueva
// (más interna en la pila). El desenrollado lo usa para distinguir lo
// abandonado (fid mayor al destino: se libera) de lo que sobrevive
// (fid menor o igual: invocación destino y exteriores, no se toca).
static int64_t wini_fid_siguiente = 0;

int64_t wini_invocacion_nueva(void) {
    if (wini_fid_siguiente == INT64_MAX) {
        fprintf(stderr, "demasiadas invocaciones anidadas en el runtime\n");
        exit(1);
    }
    return wini_fid_siguiente++;
}

void wini_raiz_agregar(void *ranura, int32_t kind, int32_t aux1, int32_t aux2, int64_t fid) {
    if (!ranura) return;
    if (kind < 0 || kind > 2) {
        fprintf(stderr, "wini_raiz_agregar: kind inválido %d\n", kind);
        exit(1);
    }
    if (wini_raices_len >= wini_raices_cap) {
        int64_t nuevaCap = wini_raices_cap == 0 ? 64 : wini_raices_cap * 2;
        if (nuevaCap <= 0 || (uint64_t)nuevaCap > SIZE_MAX / sizeof(WiniRaiz)) {
            fprintf(stderr, "pila de raíces demasiado grande en el runtime\n");
            exit(1);
        }
        WiniRaiz *nuevas = (WiniRaiz *)realloc(wini_raices, (size_t)nuevaCap * sizeof(WiniRaiz));
        if (!nuevas) { fprintf(stderr, "sin memoria para pila de raíces\n"); exit(1); }
        wini_raices = nuevas;
        wini_raices_cap = nuevaCap;
    }
    wini_raices[wini_raices_len].ranura = ranura;
    wini_raices[wini_raices_len].kind = kind;
    wini_raices[wini_raices_len].aux1 = aux1;
    wini_raices[wini_raices_len].aux2 = aux2;
    wini_raices[wini_raices_len].fid = fid;
    wini_raices_len++;
}

int64_t wini_raices_profundidad(void) {
    return wini_raices_len;
}

void wini_raices_podar(int64_t prof) {
    // Solo descarta, NUNCA libera (camino normal: ver nota arriba). El
    // clamp en ambos extremos deja la pila en un estado válido aunque el
    // llamador pase una profundidad vieja o futura. Las descartadas se
    // anulan: la pila es global y el LeakSanitizer la recorre; punteros
    // rancios a ranuras muertas esconderían fugas reales (en particular
    // ciclos, que solo se detectan por contadores si no).
    if (prof < 0) prof = 0;
    if (prof < wini_raices_len) {
        for (int64_t i = prof; i < wini_raices_len; i++) {
            wini_raices[i].ranura = NULL;
        }
        wini_raices_len = prof;
    }
}

// Libera las raíces de invocaciones abandonadas y las quita de la pila,
// conservando las demás. Solo la usa el desenrollado de excepciones, con
// los frames todavía vivos.
//
// 'fidObjetivo' es el fid de la invocación destino del salto (o -1 si no
// hay destino y el programa termina): se libera lo de invocaciones
// estrictamente más nuevas (fid mayor: llamados que no van a resumir) y
// se conserva lo de la invocación destino (sus locales siguen vivas: el
// 'capturar' puede usarlas) y lo de las exteriores (siguen en la pila).
// Las entradas conservadas mantienen su lugar: futuros desenrollados las
// reevalúan con el mismo criterio.
static void wini_raices_desenrollar(int64_t fidObjetivo) {
    int64_t j = 0;
    int64_t n = wini_raices_len;
    for (int64_t i = 0; i < n; i++) {
        WiniRaiz *r = &wini_raices[i];
        if (r->fid > fidObjetivo) {
            if (!r->ranura) continue;
            // Ranuras en NULL = ya liberadas a mano ('liberar' anula la
            // variable): cada liberar_* tolera NULL, así que no hace falta
            // chequear antes de llamar.
            switch (r->kind) {
                case 0:
                    wini_liberar_cadena(*(WiniString **)r->ranura);
                    break;
                case 1:
                    wini_liberar_lista(*(WiniList **)r->ranura, r->aux1);
                    break;
                case 2:
                    wini_liberar_diccionario(*(WiniDict **)r->ranura, r->aux1, r->aux2);
                    break;
                default:
                    break;
            }
        } else {
            if (j != i) wini_raices[j] = *r;
            j++;
        }
    }
    // Anular la cola compactada (ver nota en wini_raices_podar).
    for (int64_t k = j; k < n; k++) {
        wini_raices[k].ranura = NULL;
    }
    wini_raices_len = j;
}

// ---- excepciones (intentar / capturar / finalmente / lanzar) ----

struct WiniTryFrame {
    jmp_buf buf;
    struct WiniTryFrame *anterior;
    // Fid de la invocación dueña del frame (ver wini_invocacion_nueva):
    // un 'lanzar' que salte hasta acá libera antes lo de invocaciones
    // más nuevas y conserva lo de esta y las exteriores.
    int64_t fid;
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

void *wini_try_push(int64_t fid) {
    struct WiniTryFrame *f = (struct WiniTryFrame *)check_malloc(sizeof(struct WiniTryFrame));
    f->anterior = wini_try_pila;
    f->fid = fid;
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
        // Sin manejadores: antes de terminar se devuelven las raíces
        // vivas (la excepción no capturada tampoco fuga locales).
        wini_raices_desenrollar(-1);
        wini_terminar_no_capturada();
    }
    struct WiniTryFrame *f = wini_try_pila;
    wini_try_pila = f->anterior;
    wini_frame_recien_saltado = f;
    // Los frames que se abandonan (incluida la función que lanza, si el
    // destino vive en otra) siguen en el stack en este punto: lo de sus
    // invocaciones se libera ahora, nunca después del salto. Se conserva
    // lo de la invocación DESTINO (f: ahí se resume, sus ranuras deben
    // estar intactas) y lo de las exteriores. Nótese: es f->fid, no el
    // del frame de abajo (ese excluiría al propio destino y lo liberaría
    // antes de aterrizar: use-after-free en el 'capturar').
    wini_raices_desenrollar(f->fid);
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
    // Dos guardas: un 'tipo' NULL Harvard desreferenciarlo, y comparar
    // contra "" cuando NO hay ninguna excepción en curso haría que
    // wini_exc_tipo[0] == '\0' (el estado "sin excepción") coincidiera con
    // una cadena vacía, un falso positivo silencioso.
    if (!tipo || !*tipo) return 0;
    if (!wini_exc_tipo[0]) return 0;
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

// ---- diagnóstico de memoria ----

int64_t wini_memoria_cadenas_vivas(void) { return wini_vivas_cadenas; }
int64_t wini_memoria_listas_vivas(void) { return wini_vivas_listas; }
int64_t wini_memoria_diccionarios_vivos(void) { return wini_vivas_diccs; }

void wini_memoria_reporte(void) {
    fprintf(stderr, "wini memoria: cadenas vivas=%lld listas vivas=%lld diccionarios vivos=%lld\n",
            (long long)wini_vivas_cadenas, (long long)wini_vivas_listas, (long long)wini_vivas_diccs);
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
