/* =====================================================================
 * dl.c — carga dinámica de librerías (.so / .dll) para Wini
 * ---------------------------------------------------------------------
 * Compilar (desde la raíz del proyecto wini_compilador):
 *
 *   cc -O2 -fPIC -Wall -Wextra -Iruntime -I"modulos/dl/codigo C" \
 *       -c "modulos/dl/codigo C/dl.c" -o /tmp/dl.o
 *   ar rcs modulos/dl/libdl.a /tmp/dl.o
 *
 * winic enlaza automáticamente libdl.a al importar "dl/dl" (ver
 * wini/modulos.go).
 *
 * Contratos (igual que sistema.c / tiempo.c / regex.c):
 * - Los WINI_CADENA de entrada se CONSUMEN siempre; lo devuelto es NUEVO.
 * ===================================================================== */

#define _POSIX_C_SOURCE 200809L

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(_WIN32) || defined(_WIN64)
    #include <windows.h>
    typedef HMODULE DL_NATIVE;
    #define DL_OPEN(ruta)     LoadLibraryA(ruta)
    #define DL_CLOSE(nat)     FreeLibrary(nat)
    #define DL_SYM(nat, nom)  GetProcAddress(nat, nom)
    #define DL_NATIVO_NUL     ((HMODULE)NULL)
#else
    #include <dlfcn.h>
    typedef void *DL_NATIVE;
    #define DL_OPEN(ruta)     dlopen(ruta, RTLD_NOW | RTLD_LOCAL)
    #define DL_CLOSE(nat)     dlclose(nat)
    #define DL_SYM(nat, nom)  dlsym(nat, nom)
    #define DL_NATIVO_NUL     ((void *)NULL)
#endif

#include "wini_bridge.h"
#include "dl.h"

/* Tabla de librerías abiertas. Índice 0 = libre/inválido. Los handles
 * que devuelve dl_open son índices 1..DL_MAX_LIBS. */
static DL_NATIVE nativos[DL_MAX_LIBS];
static int usados[DL_MAX_LIBS];     /* 1 = esta entrada está en uso */

/* Último error (NUL-terminado). */
static char ultimo_error[512];

static void set_error(const char *msg) {
    strncpy(ultimo_error, msg ? msg : "", sizeof(ultimo_error) - 1);
    ultimo_error[sizeof(ultimo_error) - 1] = '\0';
}

static void clear_error(void) {
    ultimo_error[0] = '\0';
}

/* Busca una entrada libre en la tabla. Devuelve el índice (1-based) o
 * -1 si la tabla está llena. */
static int entrada_libre(void) {
    for (int i = 0; i < DL_MAX_LIBS; i++) {
        if (!usados[i]) return i + 1; /* handle = índice + 1 */
    }
    return -1;
}

/* Resuelve el puntero nativo a partir del handle. Devuelve NULL si el
 * handle es inválido o la entrada no está en uso. */
static DL_NATIVE resolver_nativo(WINI_ENTERO handle) {
    int idx = (int)handle - 1;
    if (idx < 0 || idx >= DL_MAX_LIBS) return DL_NATIVO_NUL;
    if (!usados[idx]) return DL_NATIVO_NUL;
    return nativos[idx];
}

/* ---------------------------------------------------------------- */
/* dl_open                                                          */
/* ---------------------------------------------------------------- */

WINI_ENTERO dl_open(WINI_CADENA ruta) {
    clear_error();
    if (!ruta || ruta->len == 0) {
        set_error("dl_open: ruta vacía");
        return 0;
    }
    const char *cstr = wini_bridge_cstr(ruta);

    int handle = entrada_libre();
    if (handle < 0) {
        set_error("dl_open: demasiadas librerías abiertas (aumentá DL_MAX_LIBS)");
        wini_bridge_liberar_str(ruta);
        return 0;
    }

    DL_NATIVE nat = DL_OPEN(cstr);
    if (!nat) {
#if defined(_WIN32) || defined(_WIN64)
        set_error("dl_open: no se pudo cargar la librería (LoadLibrary falló)");
#else
        const char *e = dlerror();
        set_error(e ? e : "dl_open: dlopen falló sin mensaje");
#endif
        wini_bridge_liberar_str(ruta);
        return 0;
    }

    int idx = handle - 1;
    nativos[idx] = nat;
    usados[idx] = 1;
    wini_bridge_liberar_str(ruta);
    return (WINI_ENTERO)handle;
}

/* ---------------------------------------------------------------- */
/* dl_close                                                         */
/* ---------------------------------------------------------------- */

WINI_BOOLEANO dl_close(WINI_ENTERO handle) {
    clear_error();
    DL_NATIVE nat = resolver_nativo(handle);
    if (!nat) {
        set_error("dl_close: handle inválido");
        return 0;
    }
    int rc = DL_CLOSE(nat);
    int idx = (int)handle - 1;
    usados[idx] = 0;
    nativos[idx] = DL_NATIVO_NUL;
    if (rc != 0) {
#if !defined(_WIN32) && !defined(_WIN64)
        const char *e = dlerror();
        set_error(e ? e : "dl_close: dlclose falló");
#endif
        return 0;
    }
    return 1;
}

/* ---------------------------------------------------------------- */
/* dl_sym                                                           */
/* ---------------------------------------------------------------- */

WINI_ENTERO dl_sym(WINI_ENTERO handle, WINI_CADENA nombre) {
    clear_error();
    DL_NATIVE nat = resolver_nativo(handle);
    if (!nat) {
        set_error("dl_sym: handle inválido");
        return 0;
    }
    if (!nombre || nombre->len == 0) {
        set_error("dl_sym: nombre vacío");
        return 0;
    }
    const char *cnom = wini_bridge_cstr(nombre);

    void *sym = DL_SYM(nat, cnom);
    wini_bridge_liberar_str(nombre);
    if (!sym) {
#if !defined(_WIN32) && !defined(_WIN64)
        const char *e = dlerror();
        set_error(e ? e : "dl_sym: símbolo no encontrado");
#endif
        return 0;
    }
    return (WINI_ENTERO)(intptr_t)sym;
}

/* ---------------------------------------------------------------- */
/* dl_error                                                         */
/* ---------------------------------------------------------------- */

WINI_CADENA dl_error(void) {
    /* dl_error devuelve una copia nueva; no consume nada. */
    return wini_bridge_str(ultimo_error);
}

/* ---------------------------------------------------------------- */
/* Helpers de llamada a función                                     */
/* ---------------------------------------------------------------- */

/* Firma: int f(int, int). */
typedef int (*dl_fn_entero)(int, int);

/* Firma: char * f(char *). El runtime Wini es dueño de la cadena
 * devuelta: la copiamos en una WiniString nueva y liberamos el buffer
 * que la lib devolviera si es necesario (no sabemos si usa malloc, así
 * que solo tomamos el contenido y copiamos). */
typedef char *(*dl_fn_cadena)(char *);

/* ---------------------------------------------------------------- */
/* dl_call_entero                                                   */
/* ---------------------------------------------------------------- */

WINI_ENTERO dl_call_entero(WINI_ENTERO handle, WINI_CADENA nombre,
                           WINI_ENTERO a, WINI_ENTERO b) {
    clear_error();
    DL_NATIVE nat = resolver_nativo(handle);
    if (!nat) {
        set_error("dl_call_entero: handle inválido");
        return -1;
    }
    if (!nombre || nombre->len == 0) {
        set_error("dl_call_entero: nombre vacío");
        return -1;
    }
    const char *cnom = wini_bridge_cstr(nombre);

    void *sym = DL_SYM(nat, cnom);
    wini_bridge_liberar_str(nombre);
    if (!sym) {
#if !defined(_WIN32) && !defined(_WIN64)
        const char *e = dlerror();
        set_error(e ? e : "dl_call_entero: símbolo no encontrado");
#endif
        return -1;
    }

    dl_fn_entero fn = (dl_fn_entero)(intptr_t)sym;
    return (WINI_ENTERO)fn((int)a, (int)b);
}

/* ---------------------------------------------------------------- */
/* dl_call_cadena                                                   */
/* ---------------------------------------------------------------- */

WINI_CADENA dl_call_cadena(WINI_ENTERO handle, WINI_CADENA nombre,
                           WINI_CADENA s) {
    clear_error();
    DL_NATIVE nat = resolver_nativo(handle);
    if (!nat) {
        set_error("dl_call_cadena: handle inválido");
        if (s) wini_bridge_liberar_str(s);
        return wini_bridge_str("");
    }
    if (!nombre || nombre->len == 0) {
        set_error("dl_call_cadena: nombre vacío");
        if (s) wini_bridge_liberar_str(s);
        return wini_bridge_str("");
    }
    const char *cnom = wini_bridge_cstr(nombre);
    const char *cstr = s ? wini_bridge_cstr(s) : "";

    void *sym = DL_SYM(nat, cnom);
    wini_bridge_liberar_str(nombre);
    if (!sym) {
#if !defined(_WIN32) && !defined(_WIN64)
        const char *e = dlerror();
        set_error(e ? e : "dl_call_cadena: símbolo no encontrado");
#endif
        if (s) wini_bridge_liberar_str(s);
        return wini_bridge_str("");
    }

    dl_fn_cadena fn = (dl_fn_cadena)(intptr_t)sym;
    char *res = fn((char *)cstr); /* cstr apunta a 's', que aún está viva */
    if (s) wini_bridge_liberar_str(s);
    if (!res) return wini_bridge_str("");
    WINI_CADENA out = wini_bridge_str(res);
    return out;
}

/* ---------------------------------------------------------------- */
/* versión                                                          */
/* ---------------------------------------------------------------- */

WINI_CADENA dl_version(void) {
    return wini_bridge_str("dl 1.0 (dlopen/dlsym / LoadLibrary)");
}
