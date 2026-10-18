/* =====================================================================
 * regex.c — expresiones regulares POSIX para Wini
 * ---------------------------------------------------------------------
 * Compilar (desde la raíz del proyecto wini_compilador):
 *
 *   cc -O2 -fPIC -Wall -Wextra -Iruntime -I"modulos/regex/codigo C" \
 *      -c "modulos/regex/codigo C/regex.c" -o /tmp/regex.o
 *   ar rcs modulos/regex/libregex.a /tmp/regex.o
 *
 * winic enlaza automáticamente el .a que esté junto al módulo (ver
 * wini/modulos.go): no hay que pasar nada a mano, basta con
 * importar "regex/regex" en el .wn.
 *
 * Convenciones (igual que modulos/system/codigo C/sistema.c):
 * - Los WINI_CADENA de entrada se CONSUMEN siempre (se libera una
 *   referencia); lo devuelto es NUEVO (rc=1).
 * - Sintaxis EXTENDED de POSIX (como grep -E): a+, a*, a?, a{2,4},
 *   (grupo), a|b, [clase], [^para negar], ^ ancla inicio, $ ancla fin,
 *   . cualquier salvo salto de línea. Los grupos se cuentan desde 1
 *   (el 0 es todo el match).
 * - Plataformas: POSIX usa <regex.h> del sistema. En MSVC no-MinGW no
 *   hay <regex.h>: todo devuelve error limpio (-1/falso/""/lista
 *   vacía) en vez de no compilar el módulo.
 * - El runtime Wini es de un solo hilo: la tabla y el último error son
 *   estáticos globales sin locks.
 * ===================================================================== */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(_WIN32) && !defined(__MINGW32__)
#define WINI_SIN_REGEX 1
#else
#include <regex.h>
#endif

#include "wini_bridge.h"
#include "wini_regex.h"

/* Cantidad de grupos que se extraen (0 = match completo + 9 grupos). */
#define WINI_REGEX_GRUPOS 10

#ifndef WINI_SIN_REGEX

/* Tabla de patrones compilados, indexada por id. Una entrada libre
 * tiene compilado = 0 y se reutiliza. */
typedef struct {
    int compilado;
    regex_t re;
} EntradaRegex;

static EntradaRegex *tabla = NULL;
static int64_t tabla_cap = 0;

/* Texto del último error de regex_compilarx (malloc, con NUL). */
static char *ultimo_error = NULL;

static void guardar_error(int codigo, const regex_t *re) {
    char buf[256];
    size_t n = regerror(codigo, re, buf, sizeof(buf));
    (void)n;
    free(ultimo_error);
    ultimo_error = (char *)malloc(strlen(buf) + 1);
    if (ultimo_error) strcpy(ultimo_error, buf);
}

/* Reserva una entrada libre (o crece la tabla) y devuelve su índice,
 * o -1 sin memoria. */
static int64_t reservar_entrada(void) {
    for (int64_t i = 0; i < tabla_cap; i++) {
        if (!tabla[i].compilado) return i;
    }
    int64_t nueva_cap = tabla_cap == 0 ? 8 : tabla_cap * 2;
    EntradaRegex *nueva =
        (EntradaRegex *)realloc(tabla, (size_t)nueva_cap * sizeof(EntradaRegex));
    if (!nueva) return -1;
    for (int64_t i = tabla_cap; i < nueva_cap; i++) nueva[i].compilado = 0;
    int64_t idx = tabla_cap;
    tabla = nueva;
    tabla_cap = nueva_cap;
    return idx;
}

/* Entrada válida y compilada, o NULL (id inválido, liberado o fuera). */
static EntradaRegex *entrada_valida(int64_t id) {
    if (id < 0 || id >= tabla_cap) return NULL;
    if (!tabla[id].compilado) return NULL;
    return &tabla[id];
}

#endif /* !WINI_SIN_REGEX */

/* ===================================================================== */
/* Compilación                                                           */
/* ===================================================================== */

WINI_ENTERO regex_compilarx(WINI_CADENA patron) {
#ifdef WINI_SIN_REGEX
    wini_bridge_liberar_str(patron);
    return -1;
#else
    if (!patron) return -1;
    const char *cstr = wini_bridge_cstr(patron);
    int64_t id = reservar_entrada();
    if (id < 0) {
        wini_bridge_liberar_str(patron);
        return -1;
    }
    int rc = regcomp(&tabla[id].re, cstr, REG_EXTENDED);
    wini_bridge_liberar_str(patron);
    if (rc != 0) {
        guardar_error(rc, &tabla[id].re);
        return -1;
    }
    tabla[id].compilado = 1;
    return id;
#endif
}

WINI_BOOLEANO regex_liberarx(WINI_ENTERO id) {
#ifdef WINI_SIN_REGEX
    (void)id;
    return 0;
#else
    EntradaRegex *e = entrada_valida(id);
    if (!e) return 0;
    regfree(&e->re);
    e->compilado = 0;
    return 1;
#endif
}

WINI_CADENA regex_ultimo_errorx(void) {
#ifdef WINI_SIN_REGEX
    return wini_bridge_str("expresiones regulares no disponibles en esta plataforma");
#else
    if (!ultimo_error) return wini_bridge_str("");
    return wini_bridge_str(ultimo_error);
#endif
}

/* Ejecuta el patrón sobre 'texto' llenando 'grupos' (WINI_REGEX_GRUPOS
 * entradas). Devuelve 1 si hubo match, 0 si no, -1 si el id es inválido.
 * No toca referencias (el caller libera 'texto' por su cuenta). */
#ifndef WINI_SIN_REGEX
static int ejecutar_grupo(EntradaRegex *e, const char *texto, regmatch_t *grupos) {
    for (int i = 0; i < WINI_REGEX_GRUPOS; i++) {
        grupos[i].rm_so = -1;
        grupos[i].rm_eo = -1;
    }
    return regexec(&e->re, texto, WINI_REGEX_GRUPOS, grupos, 0) == 0 ? 1 : 0;
}
#endif

/* ===================================================================== */
/* Búsqueda                                                              */
/* ===================================================================== */

WINI_BOOLEANO regex_coincidex(WINI_ENTERO id, WINI_CADENA texto) {
#ifdef WINI_SIN_REGEX
    (void)id;
    wini_bridge_liberar_str(texto);
    return 0;
#else
    EntradaRegex *e = entrada_valida(id);
    if (!e || !texto) {
        wini_bridge_liberar_str(texto);
        return 0;
    }
    regmatch_t grupos[WINI_REGEX_GRUPOS];
    int hay = ejecutar_grupo(e, wini_bridge_cstr(texto), grupos);
    wini_bridge_liberar_str(texto);
    return hay ? 1 : 0;
#endif
}

/* Coincide con TODO el texto: grupos[0] debe abarcar desde el byte 0
 * hasta el final. 'texto' se consume. */
WINI_BOOLEANO regex_match_completox(WINI_ENTERO id, WINI_CADENA texto) {
#ifdef WINI_SIN_REGEX
    (void)id;
    wini_bridge_liberar_str(texto);
    return 0;
#else
    EntradaRegex *e = entrada_valida(id);
    if (!e || !texto) {
        wini_bridge_liberar_str(texto);
        return 0;
    }
    regmatch_t grupos[WINI_REGEX_GRUPOS];
    const char *cstr = wini_bridge_cstr(texto);
    int64_t len = wini_bridge_str_len(texto);
    int hay = ejecutar_grupo(e, cstr, grupos);
    int completo = hay && grupos[0].rm_so == 0 && grupos[0].rm_eo == len;
    wini_bridge_liberar_str(texto);
    return completo ? 1 : 0;
#endif
}

WINI_CADENA regex_extraerx(WINI_ENTERO id, WINI_CADENA texto) {
#ifdef WINI_SIN_REGEX
    (void)id;
    wini_bridge_liberar_str(texto);
    return wini_bridge_str("");
#else
    EntradaRegex *e = entrada_valida(id);
    if (!e || !texto) {
        wini_bridge_liberar_str(texto);
        return wini_bridge_str("");
    }
    const char *cstr = wini_bridge_cstr(texto);
    regmatch_t grupos[WINI_REGEX_GRUPOS];
    WINI_CADENA resultado;
    if (ejecutar_grupo(e, cstr, grupos) && grupos[0].rm_so >= 0) {
        int64_t ini = (int64_t)grupos[0].rm_so;
        int64_t fin = (int64_t)grupos[0].rm_eo;
        resultado = wini_string_new(cstr + ini, fin - ini);
    } else {
        resultado = wini_string_new("", 0);
    }
    wini_bridge_liberar_str(texto);
    return resultado;
#endif
}

WINI_LISTA regex_capturasx(WINI_ENTERO id, WINI_CADENA texto) {
#ifdef WINI_SIN_REGEX
    (void)id;
    wini_bridge_liberar_str(texto);
    return wini_list_new_str(NULL, 0);
#else
    EntradaRegex *e = entrada_valida(id);
    if (!e || !texto) {
        wini_bridge_liberar_str(texto);
        return wini_list_new_str(NULL, 0);
    }
    const char *cstr = wini_bridge_cstr(texto);
    regmatch_t grupos[WINI_REGEX_GRUPOS];
    if (!ejecutar_grupo(e, cstr, grupos)) {
        wini_bridge_liberar_str(texto);
        return wini_list_new_str(NULL, 0);
    }
    /* Los elementos se crean frescos (rc=1) y la lista los adopta sin
     * retener: wini_list_new_str copia los punteros tal cual. */
    WiniString *partes[WINI_REGEX_GRUPOS];
    for (int i = 0; i < WINI_REGEX_GRUPOS; i++) {
        if (grupos[i].rm_so >= 0) {
            int64_t ini = (int64_t)grupos[i].rm_so;
            int64_t fin = (int64_t)grupos[i].rm_eo;
            partes[i] = wini_string_new(cstr + ini, fin - ini);
        } else {
            partes[i] = wini_string_new("", 0);
        }
    }
    WiniList *lista = wini_list_new_str(partes, WINI_REGEX_GRUPOS);
    wini_bridge_liberar_str(texto);
    return lista;
#endif
}

WINI_CADENA regex_reemplazarx(WINI_ENTERO id, WINI_CADENA texto, WINI_CADENA reemplazo) {
#ifdef WINI_SIN_REGEX
    (void)id;
    wini_bridge_liberar_str(texto);
    wini_bridge_liberar_str(reemplazo);
    return wini_bridge_str("");
#else
    EntradaRegex *e = entrada_valida(id);
    if (!e || !texto || !reemplazo) {
        wini_bridge_liberar_str(texto);
        wini_bridge_liberar_str(reemplazo);
        return wini_bridge_str("");
    }
    const char *ctexto = wini_bridge_cstr(texto);
    regmatch_t grupos[WINI_REGEX_GRUPOS];
    WINI_CADENA resultado;
    if (!ejecutar_grupo(e, ctexto, grupos) || grupos[0].rm_so < 0) {
        /* Sin match: copia intacta del texto (el caller ya no lo tiene). */
        resultado = wini_string_new(ctexto, texto->len);
        wini_bridge_liberar_str(texto);
        wini_bridge_liberar_str(reemplazo);
        return resultado ? resultado : wini_bridge_str("");
    }
    int64_t ini = (int64_t)grupos[0].rm_so;
    int64_t fin = (int64_t)grupos[0].rm_eo;
    /* El reemplazo se recorre una vez para medir y otra para copiar:
     * \N es el grupo N (vacío si no participó), \\ es barra literal,
     * y una barra al final se copia tal cual. */
    const char *cremp = wini_bridge_cstr(reemplazo);
    int64_t extra = 0;
    for (const char *p = cremp; *p; p++) {
        if (*p == '\\' && p[1] >= '1' && p[1] <= '9') {
            int g = p[1] - '0';
            if (g < WINI_REGEX_GRUPOS && grupos[g].rm_so >= 0) {
                extra += (int64_t)(grupos[g].rm_eo - grupos[g].rm_so) - 2;
            } else {
                extra -= 2;
            }
            p++;
        }
    }
    int64_t total = ini + (texto->len - fin) + (reemplazo->len + extra);
    if (total < 0) total = 0;
    char *buf = (char *)malloc((size_t)total + 1);
    if (!buf) {
        wini_bridge_liberar_str(texto);
        wini_bridge_liberar_str(reemplazo);
        return wini_bridge_str("");
    }
    char *o = buf;
    memcpy(o, ctexto, (size_t)ini);
    o += ini;
    for (const char *p = cremp; *p; p++) {
        if (*p == '\\' && p[1] >= '1' && p[1] <= '9') {
            int g = p[1] - '0';
            if (g < WINI_REGEX_GRUPOS && grupos[g].rm_so >= 0) {
                int64_t n = (int64_t)(grupos[g].rm_eo - grupos[g].rm_so);
                memcpy(o, ctexto + grupos[g].rm_so, (size_t)n);
                o += n;
            }
            p++;
        } else if (*p == '\\' && p[1] == '\\') {
            *o++ = '\\';
            p++;
        } else {
            *o++ = *p;
        }
    }
    memcpy(o, ctexto + fin, (size_t)(texto->len - fin));
    o += texto->len - fin;
    *o = '\0';
    resultado = wini_string_new(buf, (int64_t)(o - buf));
    free(buf);
    wini_bridge_liberar_str(texto);
    wini_bridge_liberar_str(reemplazo);
    return resultado ? resultado : wini_bridge_str("");
#endif
}

/* ===================================================================== */
/* Recorrido de matches + reemplazo global + división + conteo        */
/* ===================================================================== */

/* Busca desde el offset 'desde' (bytes). Devuelve 1 con ini/fin, o 0.
 * Con REG_NOTBOL salvo al inicio para que ^ solo ancle al principio
 * real ($ ancla al NUL final, que siempre es el fin verdadero). */
static int buscar_desde(EntradaRegex *e, const char *texto, int64_t desde,
                        int64_t *ini, int64_t *fin) {
    regmatch_t g[1];
    int flags = desde == 0 ? 0 : REG_NOTBOL;
    if (regexec(&e->re, texto + desde, 1, g, flags) != 0) return 0;
    if (g[0].rm_so < 0) return 0;
    *ini = desde + (int64_t)g[0].rm_so;
    *fin = desde + (int64_t)g[0].rm_eo;
    return 1;
}

/* Buffer creciente para armar resultados. */
typedef struct {
    char *p;
    size_t len;
    size_t cap;
} BufSalida;

static int buf_agregar(BufSalida *b, const char *s, size_t n) {
    if (n == 0) return 1;
    if (b->len + n + 1 > b->cap) {
        size_t nc = b->cap ? b->cap * 2 : 64;
        while (nc < b->len + n + 1) nc *= 2;
        char *q = (char *)realloc(b->p, nc);
        if (!q) return 0;
        b->p = q;
        b->cap = nc;
    }
    memcpy(b->p + b->len, s, n);
    b->len += n;
    return 1;
}

/* Expande 'rempl' (sintaxis \1..\9, \\) sobre el match actual al final
 * de 'salida'. 'ctexto' es el texto completo para copiar grupos.
 * Devuelve 1 o 0 sin memoria. */
static int expandir_reemplazo(BufSalida *salida, const char *rempl,
                              const char *ctexto, const regmatch_t *grupos) {
    for (const char *p = rempl; *p; p++) {
        if (*p == '\\' && p[1] >= '1' && p[1] <= '9') {
            int g = p[1] - '0';
            if (g < WINI_REGEX_GRUPOS && grupos[g].rm_so >= 0) {
                int64_t n = (int64_t)(grupos[g].rm_eo - grupos[g].rm_so);
                if (!buf_agregar(salida, ctexto + grupos[g].rm_so, (size_t)n)) {
                    return 0;
                }
            }
            p++;
        } else if (*p == '\\' && p[1] == '\\') {
            if (!buf_agregar(salida, "\\", 1)) return 0;
            p++;
        } else {
            if (!buf_agregar(salida, p, 1)) return 0;
        }
    }
    return 1;
}

/* Ejecuta grupos completos (para reemplazar_todo, que necesita \N). */
WINI_CADENA regex_reemplazar_todox(WINI_ENTERO id, WINI_CADENA texto, WINI_CADENA reemplazo) {
#ifdef WINI_SIN_REGEX
    (void)id;
    wini_bridge_liberar_str(texto);
    wini_bridge_liberar_str(reemplazo);
    return wini_bridge_str("");
#else
    EntradaRegex *e = entrada_valida(id);
    if (!e || !texto || !reemplazo) {
        wini_bridge_liberar_str(texto);
        wini_bridge_liberar_str(reemplazo);
        return wini_bridge_str("");
    }
    const char *ctexto = wini_bridge_cstr(texto);
    const char *cremp = wini_bridge_cstr(reemplazo);
    int64_t len = texto->len;
    BufSalida salida = {NULL, 0, 0};
    int64_t pos = 0, ultimo = 0;
    int ok = 1;
    while (pos < len) {
        regmatch_t grupos[WINI_REGEX_GRUPOS];
        for (int i = 0; i < WINI_REGEX_GRUPOS; i++) {
            grupos[i].rm_so = -1;
            grupos[i].rm_eo = -1;
        }
        /* NOTBOL salvo al inicio para que ^ solo ancle al principio
         * real ($ ancla al NUL final, que siempre es el fin verdadero). */
        int flags = pos == 0 ? 0 : REG_NOTBOL;
        if (regexec(&e->re, ctexto + pos, WINI_REGEX_GRUPOS, grupos, flags) != 0 ||
            grupos[0].rm_so < 0) {
            break;
        }
        int64_t ini = pos + (int64_t)grupos[0].rm_so;
        int64_t fin = pos + (int64_t)grupos[0].rm_eo;
        if (fin == ini) {
            /* Match vacío: se saltea (avanza 1 byte). Sin esto, un
             * patrón como a* colgaría acá para siempre. Ver nota en
             * wini_regex.h. */
            pos++;
            continue;
        }
        if (!buf_agregar(&salida, ctexto + ultimo, (size_t)(ini - ultimo))) {
            ok = 0;
            break;
        }
        /* Los grupos salen relativos a ctexto+pos: se trasladan para
         * que expandir_reemplazo los lea sobre ctexto. */
        for (int i = 0; i < WINI_REGEX_GRUPOS; i++) {
            if (grupos[i].rm_so >= 0) {
                grupos[i].rm_so += (regoff_t)pos;
                grupos[i].rm_eo += (regoff_t)pos;
            }
        }
        if (!expandir_reemplazo(&salida, cremp, ctexto, grupos)) {
            ok = 0;
            break;
        }
        ultimo = pos = fin;
    }
    if (ok && !buf_agregar(&salida, ctexto + ultimo, (size_t)(len - ultimo))) {
        ok = 0;
    }
    WINI_CADENA resultado;
    if (!ok) {
        free(salida.p);
        resultado = wini_bridge_str("");
    } else if (!salida.p) {
        resultado = wini_string_new("", 0);
    } else {
        salida.p[salida.len] = '\0';
        resultado = wini_string_new(salida.p, (int64_t)salida.len);
        free(salida.p);
        if (!resultado) resultado = wini_bridge_str("");
    }
    wini_bridge_liberar_str(texto);
    wini_bridge_liberar_str(reemplazo);
    return resultado;
#endif
}

WINI_LISTA regex_dividirx(WINI_ENTERO id, WINI_CADENA texto) {
#ifdef WINI_SIN_REGEX
    (void)id;
    wini_bridge_liberar_str(texto);
    return wini_list_new_str(NULL, 0);
#else
    EntradaRegex *e = entrada_valida(id);
    if (!e || !texto) {
        wini_bridge_liberar_str(texto);
        return wini_list_new_str(NULL, 0);
    }
    const char *ctexto = wini_bridge_cstr(texto);
    int64_t len = texto->len;
    /* Trozos como arreglo dinámico de frescas (la lista los adopta). */
    WiniString **partes = NULL;
    int64_t npartes = 0, cap = 0;
    int64_t pos = 0, ultimo = 0;
    int ok = 1;
    /* Macro local de limpieza ante falta de memoria. */
    while (pos < len) {
        int64_t ini, fin;
        if (!buscar_desde(e, ctexto, pos, &ini, &fin)) break;
        if (fin == ini) {
            pos++; /* match vacío: se saltea (ver reemplazar_todo) */
            continue;
        }
        if (ini > ultimo) {
            if (npartes >= cap) {
                int64_t ncap = cap == 0 ? 8 : cap * 2;
                WiniString **nuevo = (WiniString **)realloc(partes, (size_t)ncap * sizeof(WiniString *));
                if (!nuevo) { ok = 0; break; }
                partes = nuevo;
                cap = ncap;
            }
            partes[npartes] = wini_string_new(ctexto + ultimo, ini - ultimo);
            if (!partes[npartes]) { ok = 0; break; }
            npartes++;
        }
        ultimo = pos = fin;
    }
    if (ok && ultimo < len) {
        if (npartes >= cap) {
            int64_t ncap = cap == 0 ? 8 : cap * 2;
            WiniString **nuevo = (WiniString **)realloc(partes, (size_t)ncap * sizeof(WiniString *));
            if (!nuevo) { ok = 0; }
            else { partes = nuevo; cap = ncap; }
        }
        if (ok) {
            partes[npartes] = wini_string_new(ctexto + ultimo, len - ultimo);
            if (!partes[npartes]) { ok = 0; }
            else npartes++;
        }
    }
    WINI_LISTA resultado;
    if (!ok) {
        for (int64_t i = 0; i < npartes; i++) wini_liberar_cadena(partes[i]);
        free(partes);
        resultado = wini_list_new_str(NULL, 0);
    } else {
        /* wini_list_new_str COPIA los punteros (no los libera): con
         * npartes == 0 equivale a lista vacía; si falla la creación
         * (no retorna nunca: exit), no hay nada que hacer. */
        resultado = wini_list_new_str(partes, npartes);
        free(partes);
    }
    wini_bridge_liberar_str(texto);
    return resultado;
#endif
}

WINI_ENTERO regex_contarx(WINI_ENTERO id, WINI_CADENA texto) {
#ifdef WINI_SIN_REGEX
    (void)id;
    wini_bridge_liberar_str(texto);
    return 0;
#else
    EntradaRegex *e = entrada_valida(id);
    if (!e || !texto) {
        wini_bridge_liberar_str(texto);
        return 0;
    }
    const char *ctexto = wini_bridge_cstr(texto);
    int64_t len = texto->len;
    int64_t n = 0, pos = 0;
    while (pos < len) {
        int64_t ini, fin;
        if (!buscar_desde(e, ctexto, pos, &ini, &fin)) break;
        if (fin == ini) {
            pos++; /* match vacío: se saltea (ver reemplazar_todo) */
            continue;
        }
        n++;
        pos = fin;
    }
    wini_bridge_liberar_str(texto);
    return n;
#endif
}

/* Caracteres que en ERE abren sintaxis fuera de corchetes y hay que
 * escapar con \ para matchearlos literales. */
static int necesita_escape(char c) {
    switch (c) {
        case '.': case '^': case '$': case '*': case '+': case '?':
        case '(': case ')': case '[': case ']': case '{': case '}':
        case '|': case '\\':
            return 1;
        default:
            return 0;
    }
}

WINI_CADENA regex_escaparx(WINI_CADENA texto) {
    if (!texto) return wini_bridge_str("");
    const char *cstr = wini_bridge_cstr(texto);
    int64_t len = texto->len;
    /* Peor caso: todo escapable -> 2x + NUL. */
    char *buf = (char *)malloc((size_t)len * 2 + 1);
    if (!buf) {
        wini_bridge_liberar_str(texto);
        return wini_bridge_str("");
    }
    char *o = buf;
    for (int64_t i = 0; i < len; i++) {
        if (necesita_escape(cstr[i])) *o++ = '\\';
        *o++ = cstr[i];
    }
    *o = '\0';
    WINI_CADENA resultado = wini_string_new(buf, (int64_t)(o - buf));
    free(buf);
    wini_bridge_liberar_str(texto);
    return resultado ? resultado : wini_bridge_str("");
}

/* Versión del módulo como cadena nueva (hoy "__2.wini()"). */
WINI_CADENA include__version_regex(void) {
    return wini_bridge_str("__2.wini()");
}
