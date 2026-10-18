/* =====================================================================
 * texto.c — utilidades de cadena (stdlib unificada: std + texto) para Wini
 * ---------------------------------------------------------------------
 * Implementado en C para ser rápido en cadenas largas y listas grandes.
 *
 * Compilar (desde la raíz del proyecto wini_compilador):
 *
 *   cc -O2 -fPIC -Wall -Wextra -Iruntime -I"modulos/libstd/codigo C" \
 *       -c "modulos/libstd/codigo C/texto.c" -o /tmp/texto.o
 *   ar rcs modulos/libstd/libtexto.a /tmp/texto.o
 *
 * winic enlaza automáticamente libtexto.a al importar "libstd/texto"
 * (ver wini/modulos.go: detecta los .a/.o de cualquier módulo que
 * declare al menos una 'externa').
 *
 * Contratos (igual que sistema.c / tiempo.c / regex.c):
 * - Los WINI_CADENA de entrada se CONSUMEN siempre (se libera una
 *   referencia); lo devuelto es NUEVO (rc=1).
 * - La WINI_LISTA de entrada se consume; los elementos de una lista
 *   devuelta ya están retenidos.
 * ===================================================================== */

#define _POSIX_C_SOURCE 200809L

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "wini_bridge.h"
#include "texto.h"

/* wini_bridge_str_n (-> wini_string_new) COPIA el contenido de 'buf' en una
 * WiniString propia, así que 'buf' ya no es necesario: se libera para no
 * fugar memoria. 'buf' debe ser un malloc válido (incluso de un byte). */
static WINI_CADENA str_n_dueno(char *buf, int64_t len) {
    WINI_CADENA r = wini_bridge_str_n(buf, len);
    free(buf);
    return r;
}

/* ---------------------------------------------------------------- */
/* minúsculas / mayúsculas                                          */
/* ---------------------------------------------------------------- */

WINI_CADENA texto_minusculas(WINI_CADENA s) {
    if (!s) return wini_bridge_str("");
    const char *src = wini_bridge_cstr(s);
    int64_t len = wini_bridge_str_len(s);
    char *buf = (char *)malloc((size_t)len + 1);
    if (!buf) {
        wini_bridge_liberar_str(s);
        return wini_bridge_str("");
    }
    for (int64_t i = 0; i < len; i++) {
        char c = src[i];
        if (c >= 'A' && c <= 'Z') c = (char)(c + 32);
        buf[i] = c;
    }
    buf[len] = '\0';
    wini_bridge_liberar_str(s);
    return str_n_dueno(buf, len);
}

WINI_CADENA texto_mayusculas(WINI_CADENA s) {
    if (!s) return wini_bridge_str("");
    const char *src = wini_bridge_cstr(s);
    int64_t len = wini_bridge_str_len(s);
    char *buf = (char *)malloc((size_t)len + 1);
    if (!buf) {
        wini_bridge_liberar_str(s);
        return wini_bridge_str("");
    }
    for (int64_t i = 0; i < len; i++) {
        char c = src[i];
        if (c >= 'a' && c <= 'z') c = (char)(c - 32);
        buf[i] = c;
    }
    buf[len] = '\0';
    wini_bridge_liberar_str(s);
    return str_n_dueno(buf, len);
}

/* ---------------------------------------------------------------- */
/* unir lista<cadena> con separador                                 */
/* ---------------------------------------------------------------- */

WINI_CADENA texto_unir(WINI_LISTA lista, WINI_CADENA sep) {
    if (!sep) sep = wini_bridge_str("");
    int64_t seplen = sep ? sep->len : 0;
    int64_t n = lista ? wini_list_len(lista) : 0;

    /* largo total = suma de elementos + separadores entre ellos */
    int64_t total = 0;
    for (int64_t i = 0; i < n; i++) {
        WiniString *elem = wini_list_get_str(lista, i, 0);
        total += elem ? elem->len : 0;
    }
    if (n > 0 && seplen > 0) total += seplen * (n - 1);

    char *buf = (char *)malloc((size_t)total + 1);
    if (!buf) {
        wini_bridge_liberar_str(sep);
        if (lista) wini_liberar_lista(lista, WINI_TIPO_CADENA);
        return wini_bridge_str("");
    }
    int64_t pos = 0;
    for (int64_t i = 0; i < n; i++) {
        if (i > 0 && seplen > 0) {
            memcpy(buf + pos, sep->data, (size_t)seplen);
            pos += seplen;
        }
        WiniString *elem = wini_list_get_str(lista, i, 0);
        if (elem && elem->len > 0) {
            memcpy(buf + pos, elem->data, (size_t)elem->len);
            pos += elem->len;
        }
    }
    buf[pos] = '\0';
    wini_bridge_liberar_str(sep);
    if (lista) wini_liberar_lista(lista, WINI_TIPO_CADENA);
    return str_n_dueno(buf, pos);
}

/* ---------------------------------------------------------------- */
/* partir cadena por separador                                      */
/* ---------------------------------------------------------------- */

WINI_LISTA texto_partir(WINI_CADENA s, WINI_CADENA sep) {
    if (!s) {
        if (sep) wini_bridge_liberar_str(sep);
        return wini_list_new_str(NULL, 0);
    }
    int64_t n = wini_bridge_str_len(s);
    const char *src = wini_bridge_cstr(s);
    if (!sep) sep = wini_bridge_str("");
    int64_t m = sep ? sep->len : 0;

    /* contamos cuántas partes habrá (mismo algoritmo que txt_partir) */
    int64_t count = 0;
    if (m == 0) {
        count = 1;
    } else {
        int64_t i = 0;
        while (i <= n - m) {
            int64_t j = 0;
            int coincide = 1;
            while (j < m) {
                if (src[i + j] != sep->data[j]) { coincide = 0; break; }
                j++;
            }
            if (coincide) { count++; i += m; }
            else i++;
        }
        count++; /* la última parte */
    }

    /* wini_list_new_str NO retiene los elementos, así que retenemos uno por
     * cada cadena: al final soltamos nuestra referencia temporal y la lista
     * queda como dueña única (rc -> 1). */
    WiniString **elems = (WiniString **)malloc((size_t)count * sizeof(WiniString *));
    if (!elems) {
        wini_bridge_liberar_str(s);
        wini_bridge_liberar_str(sep);
        return wini_list_new_str(NULL, 0);
    }
    int64_t k = 0;
    if (m == 0) {
        elems[k++] = wini_bridge_str_n(src, n);
    } else {
        int64_t ini = 0;
        int64_t i = 0;
        while (i <= n - m) {
            int64_t j = 0;
            int coincide = 1;
            while (j < m) {
                if (src[i + j] != sep->data[j]) { coincide = 0; break; }
                j++;
            }
            if (coincide) {
                elems[k++] = wini_bridge_str_n(src + ini, i - ini);
                ini = i + m;
                i = i + m;
            } else {
                i++;
            }
        }
        elems[k++] = wini_bridge_str_n(src + ini, n - ini);
    }
    for (int64_t i = 0; i < count; i++) {
        wini_string_retener(elems[i]);  /* rc: 2 (nosotros + futura lista) */
    }
    WINI_LISTA lista = wini_list_new_str(elems, count); /* strings siguen en rc=2 */
    for (int64_t i = 0; i < count; i++) {
        wini_liberar_cadena(elems[i]);  /* rc: 1 (la lista es ahora dueña) */
    }
    free(elems);

    wini_bridge_liberar_str(s);
    wini_bridge_liberar_str(sep);
    return lista;
}

/* ---------------------------------------------------------------- */
/* reemplazar todas las ocurrencias                                 */
/* ---------------------------------------------------------------- */

WINI_CADENA texto_reemplazar(WINI_CADENA s, WINI_CADENA viejo, WINI_CADENA nuevo) {
    if (!s) {
        if (viejo) wini_bridge_liberar_str(viejo);
        if (nuevo) wini_bridge_liberar_str(nuevo);
        return wini_bridge_str("");
    }
    int64_t n = wini_bridge_str_len(s);
    const char *src = wini_bridge_cstr(s);
    if (!viejo) viejo = wini_bridge_str("");
    if (!nuevo) nuevo = wini_bridge_str("");
    int64_t m = viejo ? viejo->len : 0;
    int64_t nn = nuevo ? nuevo->len : 0;

    if (m == 0) {
        wini_bridge_liberar_str(s);
        wini_bridge_liberar_str(viejo);
        wini_bridge_liberar_str(nuevo);
        return wini_bridge_str_n(src, n);
    }

    /* pasada 1: contar reemplazos y calcular largo final */
    int64_t total = 0;
    int64_t i = 0;
    while (i <= n - m) {
        int64_t j = 0;
        int coincide = 1;
        while (j < m) {
            if (src[i + j] != viejo->data[j]) { coincide = 0; break; }
            j++;
        }
        if (coincide) {
            total += nn;
            i += m;
        } else {
            total++;
            i++;
        }
    }
    total += (n - i); /* resto */

    char *buf = (char *)malloc((size_t)total + 1);
    if (!buf) {
        wini_bridge_liberar_str(s);
        wini_bridge_liberar_str(viejo);
        wini_bridge_liberar_str(nuevo);
        return wini_bridge_str("");
    }
    int64_t pos = 0;
    i = 0;
    while (i <= n - m) {
        int64_t j = 0;
        int coincide = 1;
        while (j < m) {
            if (src[i + j] != viejo->data[j]) { coincide = 0; break; }
            j++;
        }
        if (coincide) {
            memcpy(buf + pos, nuevo->data, (size_t)nn);
            pos += nn;
            i += m;
        } else {
            buf[pos] = src[i];
            pos++;
            i++;
        }
    }
    while (i < n) {
        buf[pos] = src[i];
        pos++;
        i++;
    }
    buf[pos] = '\0';

    wini_bridge_liberar_str(s);
    wini_bridge_liberar_str(viejo);
    wini_bridge_liberar_str(nuevo);
    return str_n_dueno(buf, pos);
}

/* ---------------------------------------------------------------- */
/* contiene / contar                                                */
/* ---------------------------------------------------------------- */

WINI_ENTERO texto_contar(WINI_CADENA s, WINI_CADENA sub) {
    if (!s || !sub) {
        if (s) wini_bridge_liberar_str(s);
        if (sub) wini_bridge_liberar_str(sub);
        return 0;
    }
    int64_t n = wini_bridge_str_len(s);
    const char *src = wini_bridge_cstr(s);
    int64_t m = wini_bridge_str_len(sub);
    const char *subd = wini_bridge_cstr(sub);
    if (m == 0) {
        wini_bridge_liberar_str(s);
        wini_bridge_liberar_str(sub);
        return 0;
    }
    int64_t total = 0;
    int64_t i = 0;
    while (i <= n - m) {
        int64_t j = 0;
        int coincide = 1;
        while (j < m) {
            if (src[i + j] != subd[j]) { coincide = 0; break; }
            j++;
        }
        if (coincide) {
            total++;
            i += m;
        } else {
            i++;
        }
    }
    wini_bridge_liberar_str(s);
    wini_bridge_liberar_str(sub);
    return total;
}

WINI_BOOLEANO texto_contiene(WINI_CADENA s, WINI_CADENA sub) {
    int r = texto_contar(s, sub) > 0 ? 1 : 0; /* texto_contar ya consumió s y sub */
    return r;
}

/* ---------------------------------------------------------------- */
/* recortar espacios                                                */
/* ---------------------------------------------------------------- */

WINI_CADENA texto_recortar(WINI_CADENA s) {
    if (!s) return wini_bridge_str("");
    int64_t n = wini_bridge_str_len(s);
    const char *src = wini_bridge_cstr(s);
    int64_t ini = 0;
    while (ini < n && src[ini] == ' ') ini++;
    int64_t fin = n;
    while (fin > ini && src[fin - 1] == ' ') fin--;
    int64_t len = fin - ini;
    if (len == 0) {
        wini_bridge_liberar_str(s);
        return wini_bridge_str("");
    }
    char *buf = (char *)malloc((size_t)len + 1);
    if (!buf) {
        wini_bridge_liberar_str(s);
        return wini_bridge_str("");
    }
    memcpy(buf, src + ini, (size_t)len);
    buf[len] = '\0';
    wini_bridge_liberar_str(s);
    return str_n_dueno(buf, len);
}

/* ---------------------------------------------------------------- */
/* invertir                                                         */
/* ---------------------------------------------------------------- */

WINI_CADENA texto_invertir(WINI_CADENA s) {
    if (!s) return wini_bridge_str("");
    int64_t n = wini_bridge_str_len(s);
    const char *src = wini_bridge_cstr(s);
    char *buf = (char *)malloc((size_t)n + 1);
    if (!buf) {
        wini_bridge_liberar_str(s);
        return wini_bridge_str("");
    }
    for (int64_t i = 0; i < n; i++) buf[i] = src[n - 1 - i];
    buf[n] = '\0';
    wini_bridge_liberar_str(s);
    return str_n_dueno(buf, n);
}

/* ---------------------------------------------------------------- */
/* capitalizar                                                      */
/* ---------------------------------------------------------------- */

WINI_CADENA texto_capitalizar(WINI_CADENA s) {
    if (!s) return wini_bridge_str("");
    int64_t n = wini_bridge_str_len(s);
    if (n == 0) {
        wini_bridge_liberar_str(s);
        return wini_bridge_str("");
    }
    const char *src = wini_bridge_cstr(s);
    char *buf = (char *)malloc((size_t)n + 1);
    if (!buf) {
        wini_bridge_liberar_str(s);
        return wini_bridge_str("");
    }
    char c0 = src[0];
    if (c0 >= 'a' && c0 <= 'z') c0 = (char)(c0 - 32);
    buf[0] = c0;
    for (int64_t i = 1; i < n; i++) {
        char c = src[i];
        if (c >= 'A' && c <= 'Z') c = (char)(c + 32);
        buf[i] = c;
    }
    buf[n] = '\0';
    wini_bridge_liberar_str(s);
    return str_n_dueno(buf, n);
}

/* ---------------------------------------------------------------- */
/* conversiones numéricas                                           */
/* ---------------------------------------------------------------- */

WINI_ENTERO texto_a_entero(WINI_CADENA s) {
    if (!s || !s->data || s->len == 0) {
        if (s) wini_bridge_liberar_str(s);
        return 0;
    }
    const char *src = s->data;
    int64_t n = s->len;
    int64_t i = 0;
    while (i < n && (src[i] == ' ' || src[i] == '\t')) i++;
    int neg = 0;
    if (i < n && src[i] == '-') { neg = 1; i++; }
    else if (i < n && src[i] == '+') { i++; }
    int64_t r = 0;
    int tuvoDigito = 0;
    while (i < n) {
        char c = src[i];
        if (c < '0' || c > '9') break;
        r = r * 10 + (c - '0');
        tuvoDigito = 1;
        i++;
    }
    while (i < n) {
        if (src[i] != ' ' && src[i] != '\t') {
            wini_bridge_liberar_str(s);
            return 0;
        }
        i++;
    }
    wini_bridge_liberar_str(s);
    if (!tuvoDigito) return 0;
    return neg ? -r : r;
}

WINI_DECIMAL texto_a_decimal(WINI_CADENA s) {
    if (!s || !s->data || s->len == 0) {
        if (s) wini_bridge_liberar_str(s);
        return 0.0;
    }
    const char *src = s->data;
    int64_t n = s->len;
    int64_t i = 0;
    while (i < n && (src[i] == ' ' || src[i] == '\t')) i++;
    int neg = 0;
    if (i < n && src[i] == '-') { neg = 1; i++; }
    else if (i < n && src[i] == '+') { i++; }
    double r = 0.0;
    int tuvoDigito = 0;
    while (i < n) {
        char c = src[i];
        if (c == '.' || c == ',') {
            i++;
            double lugar = 0.1;
            while (i < n) {
                char d = src[i];
                if (d < '0' || d > '9') break;
                r = r + (double)(d - '0') * lugar;
                lugar = lugar / 10.0;
                tuvoDigito = 1;
                i++;
            }
            break;
        }
        if (c < '0' || c > '9') break;
        r = r * 10.0 + (double)(c - '0');
        tuvoDigito = 1;
        i++;
    }
    while (i < n) {
        if (src[i] != ' ' && src[i] != '\t') {
            wini_bridge_liberar_str(s);
            return 0.0;
        }
        i++;
    }
    wini_bridge_liberar_str(s);
    if (!tuvoDigito) return 0.0;
    return neg ? -r : r;
}

/* ---------------------------------------------------------------- */
/* versión                                                          */
/* ---------------------------------------------------------------- */

WINI_CADENA texto_version(void) {
    return wini_bridge_str("libstd/texto 1.0 (stdlib + texto unificados, C)");
}
