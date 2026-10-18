/* =====================================================================
 * azar.c — generador pseudoaleatorio determinista (LCG) para Wini
 * ---------------------------------------------------------------------
 * Implementado en C sobre un LCG de 31 bits (constante MINSTD). Mismo
 * resultado en todas las plataformas: determinista y reproducible.
 *
 * Compilar (desde la raíz del proyecto wini_compilador):
 *
 *   cc -O2 -fPIC -Wall -Wextra -Iruntime -I"modulos/azar/codigo C" \
 *       -c "modulos/azar/codigo C/azar.c" -o /tmp/azar.o
 *   ar rcs modulos/azar/libazar.a /tmp/azar.o
 *
 * winic enlaza automáticamente libazar.a al importar "azar/azar".
 *
 * Contratos (igual que sistema.c / tiempo.c / regex.c):
 * - WINI_LISTA de entrada se consume (se libera una referencia).
 * - 'estado' se MUTA en el lugar (misma semántica que la versión Wini
 *   pura: la lista viaja entre llamadas y avanza el generador).
 * - Lo devuelto es NUEVO (rc=1).
 * ===================================================================== */

#include <stdlib.h>
#include <string.h>

#include "wini_bridge.h"
#include "azar.h"

/* Constantes del LCG (MINSTD, 31 bits). */
#define AZAR_MODULO   2147483647LL
#define AZAR_MULTIPLICADOR 1103515245LL
#define AZAR_INCREMENTO 12345LL

/* ---------------------------------------------------------------- */
/* azar_semilla                                                     */
/* ---------------------------------------------------------------- */

WINI_LISTA azar_semilla(WINI_ENTERO inicial) {
    int64_t s = inicial % (int64_t)AZAR_MODULO;
    if (s <= 0) {
        s = s + (int64_t)(AZAR_MODULO - 1); /* 2147483646 */
    }
    int64_t datos[1] = { s };
    return wini_list_new_i64(datos, 1);
}

/* ---------------------------------------------------------------- */
/* azar_siguiente                                                   */
/* ---------------------------------------------------------------- */

WINI_LISTA azar_siguiente(WINI_LISTA estado) {
    if (!estado || estado->len < 1) {
        /* Estado inválido: crear uno nuevo con semilla 1 y retornarlo. */
        int64_t datos[1] = { 1 };
        return wini_list_new_i64(datos, 1);
    }
    int64_t s = ((int64_t *)estado->data)[0];
    s = (s * AZAR_MULTIPLICADOR + AZAR_INCREMENTO) % (int64_t)AZAR_MODULO;
    if (s <= 0) {
        s = s + (int64_t)(AZAR_MODULO - 1);
    }
    ((int64_t *)estado->data)[0] = s; /* muta en el lugar */
    return estado; /* mismo puntero, como el builtin 'agregar' */
}

/* ---------------------------------------------------------------- */
/* azar_rango                                                       */
/* ---------------------------------------------------------------- */

WINI_ENTERO azar_rango(WINI_LISTA estado, WINI_ENTERO minimo, WINI_ENTERO maximo) {
    if (!estado || estado->len < 1) {
        return 0;
    }
    if (maximo < minimo) {
        wini_liberar_lista(estado, 0); /* tipo 0 = i64, lista de estado */
        const char *msg = "azar_rango: maximo menor que minimo";
        wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), 0);
        /* no retorna */
    }
    int64_t s = ((int64_t *)estado->data)[0];
    s = (s * AZAR_MULTIPLICADOR + AZAR_INCREMENTO) % (int64_t)AZAR_MODULO;
    if (s <= 0) {
        s = s + (int64_t)(AZAR_MODULO - 1);
    }
    ((int64_t *)estado->data)[0] = s; /* avanza el estado */
    int64_t span = maximo - minimo + 1;
    return (WINI_ENTERO)(minimo + (s % span));
}

/* ---------------------------------------------------------------- */
/* azar_decimal                                                     */
/* ---------------------------------------------------------------- */

WINI_DECIMAL azar_decimal(WINI_LISTA estado) {
    if (!estado || estado->len < 1) {
        return 0.0;
    }
    int64_t s = ((int64_t *)estado->data)[0];
    s = (s * AZAR_MULTIPLICADOR + AZAR_INCREMENTO) % (int64_t)AZAR_MODULO;
    if (s <= 0) {
        s = s + (int64_t)(AZAR_MODULO - 1);
    }
    ((int64_t *)estado->data)[0] = s;
    return (WINI_DECIMAL)(s % 1000000) / 1000000.0;
}

/* ---------------------------------------------------------------- */
/* azar_booleano                                                    */
/* ---------------------------------------------------------------- */

WINI_BOOLEANO azar_booleano(WINI_LISTA estado) {
    if (!estado || estado->len < 1) {
        return 0;
    }
    int64_t s = ((int64_t *)estado->data)[0];
    s = (s * AZAR_MULTIPLICADOR + AZAR_INCREMENTO) % (int64_t)AZAR_MODULO;
    if (s <= 0) {
        s = s + (int64_t)(AZAR_MODULO - 1);
    }
    ((int64_t *)estado->data)[0] = s;
    return (WINI_BOOLEANO)(s % 2 == 0 ? 1 : 0);
}

/* ---------------------------------------------------------------- */
/* azar_elegir                                                      */
/* ---------------------------------------------------------------- */

WINI_CADENA azar_elegir(WINI_LISTA estado, WINI_LISTA l) {
    if (!estado || estado->len < 1) {
        return wini_bridge_str("");
    }
    int64_t n = l ? wini_list_len(l) : 0;
    if (n == 0) {
        wini_liberar_lista(estado, 0);
        const char *msg = "azar_elegir: lista vacía";
        wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), 0);
        /* no retorna */
    }
    int64_t s = ((int64_t *)estado->data)[0];
    s = (s * AZAR_MULTIPLICADOR + AZAR_INCREMENTO) % (int64_t)AZAR_MODULO;
    if (s <= 0) {
        s = s + (int64_t)(AZAR_MODULO - 1);
    }
    ((int64_t *)estado->data)[0] = s;
    int64_t idx = s % n;
    WiniString *elem = wini_list_get_str(l, idx, 0);
    WINI_CADENA resultado = wini_string_retener(elem); /* el caller es dueño */
    return resultado;
}

/* ---------------------------------------------------------------- */
/* azar_barajar                                                     */
/* ---------------------------------------------------------------- */

WINI_LISTA azar_barajar(WINI_LISTA estado, WINI_LISTA l) {
    int64_t n = l ? wini_list_len(l) : 0;
    /* Crea copias nuevas de cada cadena: la lista barajada es totalmente
     * independiente de la original (no comparten WiniString). */
    WiniString **elems = NULL;
    if (n > 0) {
        elems = (WiniString **)malloc((size_t)n * sizeof(WiniString *));
        if (!elems) {
            wini_liberar_lista(estado, 0);
            return wini_list_new_str(NULL, 0);
        }
        for (int64_t i = 0; i < n; i++) {
            WiniString *orig = wini_list_get_str(l, i, 0);
            elems[i] = wini_string_new(orig ? orig->data : "", orig ? orig->len : 0);
        }
    }
    /* Fisher-Yates: desde el final hacia el principio. */
    for (int64_t i = n - 1; i > 0; i--) {
        if (!estado || estado->len < 1) break;
        int64_t s = ((int64_t *)estado->data)[0];
        s = (s * AZAR_MULTIPLICADOR + AZAR_INCREMENTO) % (int64_t)AZAR_MODULO;
        if (s <= 0) {
            s = s + (int64_t)(AZAR_MODULO - 1);
        }
        ((int64_t *)estado->data)[0] = s;
        int64_t j = s % (i + 1);
        WiniString *tmp = elems[i];
        elems[i] = elems[j];
        elems[j] = tmp;
    }
    WINI_LISTA lista = wini_list_new_str(elems, n);
    if (elems) free(elems);
    return lista;
}

/* ---------------------------------------------------------------- */
/* azar_cadena                                                      */
/* ---------------------------------------------------------------- */

WINI_CADENA azar_cadena(WINI_LISTA estado, WINI_ENTERO n, WINI_CADENA alfabeto) {
    if (!estado || estado->len < 1) {
        if (alfabeto) wini_bridge_liberar_str(alfabeto);
        return wini_bridge_str("");
    }
    int64_t alen = alfabeto ? wini_bridge_str_len(alfabeto) : 0;
    const char *adatos = alfabeto ? wini_bridge_cstr(alfabeto) : "";
    if (alen == 0 || n <= 0) {
        if (alfabeto) wini_bridge_liberar_str(alfabeto);
        return wini_bridge_str("");
    }
    char *buf = (char *)malloc((size_t)n + 1);
    if (!buf) {
        if (alfabeto) wini_bridge_liberar_str(alfabeto);
        return wini_bridge_str("");
    }
    for (int64_t i = 0; i < n; i++) {
        int64_t s = ((int64_t *)estado->data)[0];
        s = (s * AZAR_MULTIPLICADOR + AZAR_INCREMENTO) % (int64_t)AZAR_MODULO;
        if (s <= 0) {
            s = s + (int64_t)(AZAR_MODULO - 1);
        }
        ((int64_t *)estado->data)[0] = s;
        buf[i] = adatos[(size_t)(s % alen)];
    }
    buf[n] = '\0';
    if (alfabeto) wini_bridge_liberar_str(alfabeto);
    WINI_CADENA r = wini_bridge_str_n(buf, n);
    free(buf);
    return r;
}

/* ---------------------------------------------------------------- */
/* versión                                                          */
/* ---------------------------------------------------------------- */

WINI_CADENA azar_version(void) {
    return wini_bridge_str("azar 1.0 (LCG MINSTD, C)");
}
