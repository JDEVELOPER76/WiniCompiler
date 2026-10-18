/* =====================================================================
 * tiempo.c — hora, fecha y medición de intervalos para Wini
 * ---------------------------------------------------------------------
 * Compilar (desde la raíz del proyecto wini_compilador):
 *
 *   cc -O2 -fPIC -Wall -Wextra -Iruntime -I"modulos/tiempo/codigo C" \
 *      -c "modulos/tiempo/codigo C/tiempo.c" -o /tmp/tiempo.o
 *   ar rcs modulos/tiempo/libtiempo.a /tmp/tiempo.o
 *
 * winic enlaza automáticamente el .a que esté junto al módulo (ver
 * wini/modulos.go): no hay que pasar nada a mano, basta con
 * importar "tiempo/tiempo" en el .wn.
 *
 * Convenciones (igual que modulos/system/codigo C/sistema.c):
 * - Los WINI_CADENA de entrada se CONSUMEN siempre (se libera una
 *   referencia); lo devuelto es NUEVO (rc=1).
 * - Todo es hora LOCAL salvo marca_ms (monótono, solo para restar).
 * - El runtime es de un solo hilo: se usa localtime() sin _r.
 * ===================================================================== */

#define _POSIX_C_SOURCE 200809L

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#if defined(_WIN32) || defined(_WIN64)
    #include <windows.h>
#else
    #include <errno.h>
#endif

#include "wini_bridge.h"
#include "tiempo.h"

/* ===================================================================== */
/* Reloj                                                                 */
/* ===================================================================== */

/* Segundos Unix (time(NULL)). No falla en la práctica; (time_t)-1 sería
 * -1 de todos modos, indistinguible y correcto igual. */
WINI_ENTERO tiempo_unix(void) {
    return (WINI_ENTERO)time(NULL);
}

/* Milisegundos monótonos: la diferencia entre dos llamadas mide una
 * duración real aunque alguien cambie el reloj del sistema en el medio.
 * En Windows GetTickCount64 (ms desde un origen arbitrario, también
 * monótono). Solo vale para restar; nunca como hora absoluta. */
WINI_ENTERO marca_ms(void) {
#if defined(_WIN32) || defined(_WIN64)
    return (WINI_ENTERO)GetTickCount64();
#else
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) {
        return -1;
    }
    return (WINI_ENTERO)ts.tv_sec * 1000 + (WINI_ENTERO)(ts.tv_nsec / 1000000);
#endif
}

/* Duerme 'seg' segundos con fracción (0.5 = medio segundo). Negativo:
 * -1 sin dormir (un sleep negativo no significa nada). En POSIX usa
 * nanosleep (reintentable ante EINTR); en Windows Sleep (ms, trunca la
 * fracción hacia abajo). Devuelve 0 si durmió completo. */
WINI_ENTERO dormirx(WINI_DECIMAL seg) {
    if (!(seg >= 0.0)) {
        return -1;
    }
#if defined(_WIN32) || defined(_WIN64)
    if (seg > 0.0) {
        double ms = seg * 1000.0;
        if (ms > 0.0) Sleep((DWORD)ms);
    }
    return 0;
#else
    struct timespec req;
    req.tv_sec = (time_t)seg;
    req.tv_nsec = (long)((seg - (double)req.tv_sec) * 1000000000.0);
    if (req.tv_nsec < 0) req.tv_nsec = 0;
    if (req.tv_nsec >= 1000000000L) req.tv_nsec = 999999999L;
    while (nanosleep(&req, &req) != 0) {
        if (errno != EINTR) return -1;
    }
    return 0;
#endif
}

/* ===================================================================== */
/* Partes de la fecha/hora local                                         */
/* ===================================================================== */

/* Helper: desglose local de 'ahora'. Devuelve 1 con *t lleno, o 0 si
 * falló (entonces cada parte devuelve 0, indistinguible de la
 * medianoche del 1900-01-01: aceptable para un reloj de pared). */
static int tiempo_local(struct tm *t) {
    time_t ahora = time(NULL);
    if (ahora == (time_t)-1) return 0;
#if defined(_WIN32) || defined(_WIN64)
    /* localtime es suficiente: el runtime Wini es de un solo hilo. */
    struct tm *p = localtime(&ahora);
    if (!p) return 0;
    *t = *p;
#else
    if (!localtime_r(&ahora, t)) return 0;
#endif
    return 1;
}

/* Año completo (2026), mes 1-12, día 1-31. */
WINI_ENTERO anio_actual(void) {
    struct tm t;
    if (!tiempo_local(&t)) return 0;
    return (WINI_ENTERO)(t.tm_year + 1900);
}

/* Mes 1-12. */
WINI_ENTERO mes_actual(void) {
    struct tm t;
    if (!tiempo_local(&t)) return 0;
    return (WINI_ENTERO)(t.tm_mon + 1);
}

/* Día del mes 1-31. */
WINI_ENTERO dia_actual(void) {
    struct tm t;
    if (!tiempo_local(&t)) return 0;
    return (WINI_ENTERO)t.tm_mday;
}

/* Hora 0-23. */
WINI_ENTERO hora_actual(void) {
    struct tm t;
    if (!tiempo_local(&t)) return 0;
    return (WINI_ENTERO)t.tm_hour;
}

/* Minuto 0-59. */
WINI_ENTERO minuto_actual(void) {
    struct tm t;
    if (!tiempo_local(&t)) return 0;
    return (WINI_ENTERO)t.tm_min;
}

/* Segundo 0-60 (60 solo en segundos intercalares). */
WINI_ENTERO segundo_actual(void) {
    struct tm t;
    if (!tiempo_local(&t)) return 0;
    return (WINI_ENTERO)t.tm_sec;
}

/* Día de la semana 0-6 (0 = domingo), como localtime. */
WINI_ENTERO dia_semana_actual(void) {
    struct tm t;
    if (!tiempo_local(&t)) return 0;
    return (WINI_ENTERO)t.tm_wday;
}

/* ===================================================================== */
/* Formato libre                                                         */
/* ===================================================================== */

/* strftime() con el formato dado. 'fmt' se consume siempre. El buffer
 * arranca en 128 y se duplica hasta 64 KiB; si ni así entra (formato
 * absurdo) o falla localtime, devuelve "". */
WINI_CADENA formato_horax(WINI_CADENA fmt) {
    if (!fmt || fmt->len == 0) {
        wini_bridge_liberar_str(fmt);
        return wini_bridge_str("");
    }
    time_t ahora = time(NULL);
    if (ahora == (time_t)-1) {
        wini_bridge_liberar_str(fmt);
        return wini_bridge_str("");
    }
    struct tm t;
#if defined(_WIN32) || defined(_WIN64)
    struct tm *p = localtime(&ahora);
    if (!p) {
        wini_bridge_liberar_str(fmt);
        return wini_bridge_str("");
    }
    t = *p;
#else
    if (!localtime_r(&ahora, &t)) {
        wini_bridge_liberar_str(fmt);
        return wini_bridge_str("");
    }
#endif

    const char *cstr = wini_bridge_cstr(fmt);
    size_t cap = 128;
    char *buf = NULL;
    size_t n = 0;
    for (;;) {
        char *nuevo = (char *)realloc(buf, cap);
        if (!nuevo) break;
        buf = nuevo;
        n = strftime(buf, cap, cstr, &t);
        if (n > 0) break;
        if (cap >= (size_t)65536) break;
        cap *= 2;
    }
    wini_bridge_liberar_str(fmt);
    if (n == 0) {
        free(buf);
        return wini_bridge_str("");
    }
    WINI_CADENA resultado = wini_bridge_str_n(buf, (int64_t)n);
    free(buf);
    return resultado;
}

/* ===================================================================== */
/* Versión                                                               */
/* ===================================================================== */

/* Versión del módulo como cadena nueva (hoy "__1.wini()"). */
WINI_CADENA include__version_tiempo(void) {
    return wini_bridge_str("__1.wini()");
}
