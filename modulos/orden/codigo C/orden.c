/* =====================================================================
 * orden.c — quicksort y búsqueda binaria para Wini (en C)
 * ---------------------------------------------------------------------
 * Ver orden.h para el comando de compilación y los contratos.
 * ===================================================================== */

#include <stdlib.h>
#include <string.h>

#include "wini_bridge.h"
#include "orden.h"

/* ===================================================================== */
/* Quicksort in-place sobre enteros                                      */
/* ===================================================================== */

static void qs_i64(int64_t *a, int64_t izq, int64_t der) {
    while (izq < der) {
        int64_t i = izq, j = der;
        int64_t piv = a[(izq + der) / 2];
        while (i <= j) {
            while (a[i] < piv) i++;
            while (a[j] > piv) j--;
            if (i <= j) {
                int64_t t = a[i]; a[i] = a[j]; a[j] = t;
                i++; j--;
            }
        }
        /* Recursión por la mitad menor, iteración por la mayor (cola). */
        if (j - izq < der - i) {
            qs_i64(a, izq, j);
            izq = i;
        } else {
            qs_i64(a, i, der);
            der = j;
        }
    }
}

WINI_LISTA ord_enteros(WINI_LISTA l) {
    if (!l) return wini_list_new_i64(NULL, 0);
    int64_t n = wini_list_len(l);
    if (n <= 1) {
        wini_liberar_lista(l, 0);
        /* lista con 0 o 1 elemento: devolver copia nueva */
        if (n == 1) {
            int64_t v = wini_list_get_i64(l, 0, 0);
            wini_liberar_lista(l, 0);
            int64_t d[1] = { v };
            return wini_list_new_i64(d, 1);
        }
        return wini_list_new_i64(NULL, 0);
    }
    int64_t *datos = (int64_t *)malloc((size_t)n * sizeof(int64_t));
    if (!datos) {
        wini_liberar_lista(l, 0);
        return wini_list_new_i64(NULL, 0);
    }
    for (int64_t i = 0; i < n; i++) {
        datos[i] = wini_list_get_i64(l, i, 0);
    }
    wini_liberar_lista(l, 0);
    qs_i64(datos, 0, n - 1);
    WINI_LISTA r = wini_list_new_i64(datos, n);
    free(datos);
    return r;
}

WINI_LISTA ord_enteros_desc(WINI_LISTA l) {
    WINI_LISTA asc = ord_enteros(l);
    if (!asc) return wini_list_new_i64(NULL, 0);
    int64_t n = wini_list_len(asc);
    if (n <= 1) return asc;
    int64_t *datos = (int64_t *)malloc((size_t)n * sizeof(int64_t));
    if (!datos) {
        wini_liberar_lista(asc, 0);
        return wini_list_new_i64(NULL, 0);
    }
    for (int64_t i = 0; i < n; i++) {
        datos[i] = wini_list_get_i64(asc, n - 1 - i, 0);
    }
    wini_liberar_lista(asc, 0);
    WINI_LISTA r = wini_list_new_i64(datos, n);
    free(datos);
    return r;
}

/* ===================================================================== */
/* Quicksort in-place sobre decimales                                    */
/* ===================================================================== */

static void qs_f64(double *a, int64_t izq, int64_t der) {
    while (izq < der) {
        int64_t i = izq, j = der;
        double piv = a[(izq + der) / 2];
        while (i <= j) {
            while (a[i] < piv) i++;
            while (a[j] > piv) j--;
            if (i <= j) {
                double t = a[i]; a[i] = a[j]; a[j] = t;
                i++; j--;
            }
        }
        if (j - izq < der - i) {
            qs_f64(a, izq, j);
            izq = i;
        } else {
            qs_f64(a, i, der);
            der = j;
        }
    }
}

WINI_LISTA ord_decimales(WINI_LISTA l) {
    if (!l) return wini_list_new_f64(NULL, 0);
    int64_t n = wini_list_len(l);
    if (n <= 1) {
        if (n == 1) {
            double v = wini_list_get_f64(l, 0, 0);
            wini_liberar_lista(l, 1);
            double d[1] = { v };
            return wini_list_new_f64(d, 1);
        }
        wini_liberar_lista(l, 1);
        return wini_list_new_f64(NULL, 0);
    }
    double *datos = (double *)malloc((size_t)n * sizeof(double));
    if (!datos) {
        wini_liberar_lista(l, 1);
        return wini_list_new_f64(NULL, 0);
    }
    for (int64_t i = 0; i < n; i++) {
        datos[i] = wini_list_get_f64(l, i, 0);
    }
    wini_liberar_lista(l, 1);
    qs_f64(datos, 0, n - 1);
    WINI_LISTA r = wini_list_new_f64(datos, n);
    free(datos);
    return r;
}

WINI_LISTA ord_decimales_desc(WINI_LISTA l) {
    WINI_LISTA asc = ord_decimales(l);
    if (!asc) return wini_list_new_f64(NULL, 0);
    int64_t n = wini_list_len(asc);
    if (n <= 1) return asc;
    double *datos = (double *)malloc((size_t)n * sizeof(double));
    if (!datos) {
        wini_liberar_lista(asc, 1);
        return wini_list_new_f64(NULL, 0);
    }
    for (int64_t i = 0; i < n; i++) {
        datos[i] = wini_list_get_f64(asc, n - 1 - i, 0);
    }
    wini_liberar_lista(asc, 1);
    WINI_LISTA r = wini_list_new_f64(datos, n);
    free(datos);
    return r;
}

/* ===================================================================== */
/* Quicksort in-place sobre cadenas (orden lexicográfico por strcmp)     */
/* ===================================================================== */

static void qs_str(WiniString **a, int64_t izq, int64_t der) {
    while (izq < der) {
        int64_t i = izq, j = der;
        const char *piv = a[(izq + der) / 2]->data;
        while (i <= j) {
            while (strcmp(a[i]->data, piv) < 0) i++;
            while (strcmp(a[j]->data, piv) > 0) j--;
            if (i <= j) {
                WiniString *t = a[i]; a[i] = a[j]; a[j] = t;
                i++; j--;
            }
        }
        if (j - izq < der - i) {
            qs_str(a, izq, j);
            izq = i;
        } else {
            qs_str(a, i, der);
            der = j;
        }
    }
}

WINI_LISTA ord_cadenas(WINI_LISTA l) {
    if (!l) return wini_list_new_str(NULL, 0);
    int64_t n = wini_list_len(l);
    if (n <= 1) {
        if (n == 1) {
            WiniString *v = wini_string_retener(wini_list_get_str(l, 0, 0));
            wini_liberar_lista(l, WINI_TIPO_CADENA);
            WiniString *d[1] = { v };
            WINI_LISTA r = wini_list_new_str(d, 1);
            /* wini_list_new_str no retiene; nosotros retuvimos, así que
             * la lista es dueña. La ref retenida es la que usa la lista. */
            return r;
        }
        wini_liberar_lista(l, WINI_TIPO_CADENA);
        return wini_list_new_str(NULL, 0);
    }
    WiniString **datos = (WiniString **)malloc((size_t)n * sizeof(WiniString *));
    if (!datos) {
        wini_liberar_lista(l, WINI_TIPO_CADENA);
        return wini_list_new_str(NULL, 0);
    }
    for (int64_t i = 0; i < n; i++) {
        datos[i] = wini_string_retener(wini_list_get_str(l, i, 0));
    }
    wini_liberar_lista(l, WINI_TIPO_CADENA);
    qs_str(datos, 0, n - 1);
    WINI_LISTA r = wini_list_new_str(datos, n);
    /* wini_list_new_str copia punteros sin retener; nosotros retuvimos
     * cada uno antes (y wini_liberar_lista(l,...) bajó la de l), así que
     * cada elemento tiene rc=1 y la lista es dueña. No hay que liberar
     * nada más: los datos[i] son los mismos punteros que r->data[i]. */
    free(datos);
    return r;
}

WINI_LISTA ord_cadenas_desc(WINI_LISTA l) {
    WINI_LISTA asc = ord_cadenas(l);
    if (!asc) return wini_list_new_str(NULL, 0);
    int64_t n = wini_list_len(asc);
    if (n <= 1) return asc;
    WiniString **datos = (WiniString **)malloc((size_t)n * sizeof(WiniString *));
    if (!datos) {
        wini_liberar_lista(asc, WINI_TIPO_CADENA);
        return wini_list_new_str(NULL, 0);
    }
    for (int64_t i = 0; i < n; i++) {
        datos[i] = wini_string_retener(wini_list_get_str(asc, n - 1 - i, 0));
    }
    wini_liberar_lista(asc, WINI_TIPO_CADENA);
    WINI_LISTA r = wini_list_new_str(datos, n);
    /* wini_list_new_str copia punteros sin retener; nosotros retuvimos
     * cada uno antes (y wini_liberar_lista(asc,...) bajó la de asc), así
     * que cada elemento tiene rc=1 y la lista es dueña. No liberar más. */
    free(datos);
    return r;
}

/* ===================================================================== */
/* Búsqueda binaria                                                      */
/* ===================================================================== */

WINI_ENTERO ord_busqueda_binaria_enteros(WINI_LISTA l, WINI_ENTERO x) {
    if (!l) return -1;
    int64_t n = wini_list_len(l);
    int64_t izq = 0, der = n - 1;
    while (izq <= der) {
        int64_t medio = (izq + der) / 2;
        int64_t v = wini_list_get_i64(l, medio, 0);
        if (v == x) {
            wini_liberar_lista(l, 0);
            return (WINI_ENTERO)medio;
        }
        if (v < x) izq = medio + 1;
        else der = medio - 1;
    }
    wini_liberar_lista(l, 0);
    return -1;
}

WINI_ENTERO ord_busqueda_binaria_cadenas(WINI_LISTA l, WINI_CADENA x) {
    if (!l) {
        if (x) wini_bridge_liberar_str(x);
        return -1;
    }
    int64_t n = wini_list_len(l);
    const char *xd = x ? wini_bridge_cstr(x) : "";
    int64_t izq = 0, der = n - 1;
    while (izq <= der) {
        int64_t medio = (izq + der) / 2;
        WiniString *vm = wini_list_get_str(l, medio, 0);
        int c = vm ? strcmp(vm->data, xd) : 1;
        if (c == 0) {
            wini_liberar_lista(l, WINI_TIPO_CADENA);
            if (x) wini_bridge_liberar_str(x);
            return (WINI_ENTERO)medio;
        }
        if (c < 0) izq = medio + 1;
        else der = medio - 1;
    }
    wini_liberar_lista(l, WINI_TIPO_CADENA);
    if (x) wini_bridge_liberar_str(x);
    return -1;
}

/* ---------------------------------------------------------------- */
/* versión                                                          */
/* ---------------------------------------------------------------- */

WINI_CADENA ord_version(void) {
    return wini_bridge_str("orden 1.0 (quicksort, C)");
}
