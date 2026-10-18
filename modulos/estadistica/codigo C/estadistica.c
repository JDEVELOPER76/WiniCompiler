/* =====================================================================
 * estadistica.c — estadística descriptiva para Wini (en C)
 * ---------------------------------------------------------------------
 * Ver estadistica.h para el comando de compilación y los contratos.
 * ===================================================================== */

#include <stdlib.h>
#include <string.h>
#include <math.h>

#include "wini_bridge.h"
#include "estadistica.h"

/* Comparador para qsort de decimales (ascendente). */
static int cmp_f64(const void *pa, const void *pb) {
    double a = *(const double *)pa;
    double b = *(const double *)pb;
    if (a < b) return -1;
    if (a > b) return 1;
    return 0;
}

/* ---------------------------------------------------------------- */
/* sumas                                                            */
/* ---------------------------------------------------------------- */

WINI_DECIMAL est_suma(WINI_LISTA l) {
    if (!l) return 0.0;
    int64_t n = wini_list_len(l);
    double r = 0.0;
    for (int64_t i = 0; i < n; i++) {
        r += wini_list_get_f64(l, i, 0);
    }
    wini_liberar_lista(l, 1); /* tipo 1 = f64 */
    return r;
}

WINI_ENTERO est_suma_enteros(WINI_LISTA l) {
    if (!l) return 0;
    int64_t n = wini_list_len(l);
    int64_t r = 0;
    for (int64_t i = 0; i < n; i++) {
        r += wini_list_get_i64(l, i, 0);
    }
    wini_liberar_lista(l, 0); /* tipo 0 = i64 */
    return r;
}

/* ---------------------------------------------------------------- */
/* media                                                            */
/* ---------------------------------------------------------------- */

WINI_DECIMAL est_media(WINI_LISTA l) {
    if (!l) return 0.0;
    int64_t n = wini_list_len(l);
    if (n == 0) {
        wini_liberar_lista(l, 1);
        return 0.0;
    }
    double r = 0.0;
    for (int64_t i = 0; i < n; i++) {
        r += wini_list_get_f64(l, i, 0);
    }
    wini_liberar_lista(l, 1);
    return r / (double)n;
}

WINI_DECIMAL est_media_enteros(WINI_LISTA l) {
    if (!l) return 0.0;
    int64_t n = wini_list_len(l);
    if (n == 0) {
        wini_liberar_lista(l, 0);
        return 0.0;
    }
    int64_t r = 0;
    for (int64_t i = 0; i < n; i++) {
        r += wini_list_get_i64(l, i, 0);
    }
    wini_liberar_lista(l, 0);
    return (WINI_DECIMAL)r / (WINI_DECIMAL)n;
}

/* ---------------------------------------------------------------- */
/* min / max                                                        */
/* ---------------------------------------------------------------- */

WINI_DECIMAL est_min(WINI_LISTA l) {
    if (!l) return 0.0;
    int64_t n = wini_list_len(l);
    if (n == 0) {
        wini_liberar_lista(l, 1);
        return 0.0;
    }
    double r = wini_list_get_f64(l, 0, 0);
    for (int64_t i = 1; i < n; i++) {
        double v = wini_list_get_f64(l, i, 0);
        if (v < r) r = v;
    }
    wini_liberar_lista(l, 1);
    return r;
}

WINI_DECIMAL est_max(WINI_LISTA l) {
    if (!l) return 0.0;
    int64_t n = wini_list_len(l);
    if (n == 0) {
        wini_liberar_lista(l, 1);
        return 0.0;
    }
    double r = wini_list_get_f64(l, 0, 0);
    for (int64_t i = 1; i < n; i++) {
        double v = wini_list_get_f64(l, i, 0);
        if (v > r) r = v;
    }
    wini_liberar_lista(l, 1);
    return r;
}

WINI_ENTERO est_min_entero(WINI_LISTA l) {
    if (!l) return 0;
    int64_t n = wini_list_len(l);
    if (n == 0) {
        wini_liberar_lista(l, 0);
        return 0;
    }
    int64_t r = wini_list_get_i64(l, 0, 0);
    for (int64_t i = 1; i < n; i++) {
        int64_t v = wini_list_get_i64(l, i, 0);
        if (v < r) r = v;
    }
    wini_liberar_lista(l, 0);
    return r;
}

WINI_ENTERO est_max_entero(WINI_LISTA l) {
    if (!l) return 0;
    int64_t n = wini_list_len(l);
    if (n == 0) {
        wini_liberar_lista(l, 0);
        return 0;
    }
    int64_t r = wini_list_get_i64(l, 0, 0);
    for (int64_t i = 1; i < n; i++) {
        int64_t v = wini_list_get_i64(l, i, 0);
        if (v > r) r = v;
    }
    wini_liberar_lista(l, 0);
    return r;
}

/* ---------------------------------------------------------------- */
/* varianza y desviación estándar (poblacional)                     */
/* ---------------------------------------------------------------- */

WINI_DECIMAL est_varianza(WINI_LISTA l) {
    if (!l) return 0.0;
    int64_t n = wini_list_len(l);
    if (n == 0) {
        wini_liberar_lista(l, 1);
        return 0.0;
    }
    double media = 0.0;
    for (int64_t i = 0; i < n; i++) {
        media += wini_list_get_f64(l, i, 0);
    }
    media /= (double)n;
    double s2 = 0.0;
    for (int64_t i = 0; i < n; i++) {
        double d = wini_list_get_f64(l, i, 0) - media;
        s2 += d * d;
    }
    wini_liberar_lista(l, 1);
    return s2 / (double)n;
}

WINI_DECIMAL est_desviacion(WINI_LISTA l) {
    double v = est_varianza(l); /* est_varianza ya libera l */
    return sqrt(v);
}

/* ---------------------------------------------------------------- */
/* mediana                                                          */
/* ---------------------------------------------------------------- */

WINI_DECIMAL est_mediana(WINI_LISTA l) {
    if (!l) return 0.0;
    int64_t n = wini_list_len(l);
    if (n == 0) {
        wini_liberar_lista(l, 1);
        return 0.0;
    }
    double *datos = (double *)malloc((size_t)n * sizeof(double));
    if (!datos) {
        wini_liberar_lista(l, 1);
        return 0.0;
    }
    for (int64_t i = 0; i < n; i++) {
        datos[i] = wini_list_get_f64(l, i, 0);
    }
    wini_liberar_lista(l, 1);
    qsort(datos, (size_t)n, sizeof(double), cmp_f64);
    double r;
    if (n % 2 == 1) {
        r = datos[n / 2];
    } else {
        r = (datos[n / 2 - 1] + datos[n / 2]) / 2.0;
    }
    free(datos);
    return r;
}

/* ---------------------------------------------------------------- */
/* versión                                                          */
/* ---------------------------------------------------------------- */

WINI_CADENA est_version(void) {
    return wini_bridge_str("estadistica 1.0 (C + math.h)");
}
