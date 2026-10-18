#ifndef TIEMPO_H
#define TIEMPO_H

#include "wini_bridge.h"

/* ---- Reloj ----
 *
 * Contratos comunes: los argumentos cadena se CONSUMEN siempre (ver
 * wini_bridge.h); lo devuelto es NUEVO (rc=1, dueño = quien llama).
 * Todo en hora LOCAL del sistema (ver partes_*), salvo marca_ms que es
 * monotonía pura para medir intervalos.
 */

/* Segundos Unix (época 1970-01-01 UTC). */
WINI_ENTERO  tiempo_unix(void);
/* Milisegundos monótonos (CLOCK_MONOTONIC en POSIX, GetTickCount64 en
 * Windows): solo sirve para restar (medir duraciones), nunca como hora. */
WINI_ENTERO  marca_ms(void);
/* Duerme 'seg' segundos (fracción incluida). Negativo: -1 sin dormir.
 * Devuelve 0 si durmió completo. */
WINI_ENTERO  dormirx(WINI_DECIMAL seg);

/* ---- Partes de la fecha/hora local ----
 * Cada una devuelve el componente (año completo, mes 1-12, día 1-31,
 * hora 0-23, minuto/segundo 0-59, semana 0-6 con domingo=0). */
WINI_ENTERO  anio_actual(void);
WINI_ENTERO  mes_actual(void);
WINI_ENTERO  dia_actual(void);
WINI_ENTERO  hora_actual(void);
WINI_ENTERO  minuto_actual(void);
WINI_ENTERO  segundo_actual(void);
WINI_ENTERO  dia_semana_actual(void);

/* ---- Formato libre ----
 * strftime() con el formato dado (p. ej. "%Y-%m-%d %H:%M:%S"). 'fmt' se
 * consume. "" si el formato no produjo nada o falló. */
WINI_CADENA  formato_horax(WINI_CADENA fmt);

/* ---- Información de versión ---- */
WINI_CADENA  include__version_tiempo(void);

#endif /* TIEMPO_H */
