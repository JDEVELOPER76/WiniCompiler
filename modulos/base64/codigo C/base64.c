/* =====================================================================
 * base64.c — base64 y hexadecimal para Wini (implementado en C)
 * ---------------------------------------------------------------------
 * Ver base64.h para el comando de compilación y los contratos.
 * ===================================================================== */

#include <stdlib.h>
#include <string.h>

#include "wini_bridge.h"
#include "base64.h"

/* Alfabeto base64 estándar (RFC 4648). */
static const char B64_ALFABETO[] =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/* Tabla inversa: valor 0-63 para cada byte, -1 si no es base64 válido. */
static int8_t B64_VALOR[256];

static void b64_inicializar_tabla(void) {
    for (int i = 0; i < 256; i++) B64_VALOR[i] = -1;
    for (int i = 0; i < 64; i++) B64_VALOR[(unsigned char)B64_ALFABETO[i]] = (int8_t)i;
}

/* Valor de un dígito hexadecimal (0-15), o -1 si inválido. */
static int8_t hex_valor(char c) {
    if (c >= '0' && c <= '9') return (int8_t)(c - '0');
    if (c >= 'a' && c <= 'f') return (int8_t)(c - 'a' + 10);
    if (c >= 'A' && c <= 'F') return (int8_t)(c - 'A' + 10);
    return -1;
}

/* ---------------------------------------------------------------- */
/* b64_codificar                                                    */
/* ---------------------------------------------------------------- */

WINI_CADENA b64_codificar(WINI_LISTA bytes) {
    static int inicializado = 0;
    if (!inicializado) { b64_inicializar_tabla(); inicializado = 1; }

    if (!bytes) return wini_bridge_str("");
    int64_t n = wini_list_len(bytes);

    /* Largo de salida: 4 caracteres por cada 3 bytes, con relleno. */
    int64_t out_len = ((n + 2) / 3) * 4;
    char *out = (char *)malloc((size_t)out_len + 1);
    if (!out) {
        wini_liberar_lista(bytes, 0); /* tipo 0 = i64 */
        return wini_bridge_str("");
    }

    int64_t o = 0;
    int64_t i = 0;
    while (i < n) {
        int64_t b0 = wini_list_get_i64(bytes, i, 0) & 0xFF;
        int64_t b1 = 0, b2 = 0;
        int grupos = 1;
        if (i + 1 < n) { b1 = wini_list_get_i64(bytes, i + 1, 0) & 0xFF; grupos = 2; }
        if (i + 2 < n) { b2 = wini_list_get_i64(bytes, i + 2, 0) & 0xFF; grupos = 3; }

        int64_t entero24 = (b0 << 16) | (b1 << 8) | b2;

        out[o++] = B64_ALFABETO[(entero24 >> 18) & 0x3F];
        out[o++] = B64_ALFABETO[(entero24 >> 12) & 0x3F];
        if (grupos >= 2) out[o++] = B64_ALFABETO[(entero24 >> 6) & 0x3F];
        else            out[o++] = '=';
        if (grupos >= 3) out[o++] = B64_ALFABETO[entero24 & 0x3F];
        else            out[o++] = '=';

        i += 3;
    }
    out[o] = '\0';

    wini_liberar_lista(bytes, 0);
    WINI_CADENA r = wini_bridge_str_n(out, o);
    free(out);
    return r;
}

/* ---------------------------------------------------------------- */
/* b64_decodificar                                                  */
/* ---------------------------------------------------------------- */

WINI_LISTA b64_decodificar(WINI_CADENA texto) {
    static int inicializado = 0;
    if (!inicializado) { b64_inicializar_tabla(); inicializado = 1; }

    if (!texto) return wini_list_new_str(NULL, 0);
    int64_t n = wini_bridge_str_len(texto);
    const char *src = wini_bridge_cstr(texto);

    /* Contamos cuántos caracteres base64 válidos hay (ignorando whitespace y '='). */
    int64_t validos = 0;
    for (int64_t i = 0; i < n; i++) {
        char c = src[i];
        if (c == '=' || c == ' ' || c == '\n' || c == '\r' || c == '\t') continue;
        if (B64_VALOR[(unsigned char)c] >= 0) validos++;
    }

    int64_t out_max = ((validos + 3) / 4) * 3; /* techo: grupos de 4 -> 3 bytes */
    int64_t *datos = (int64_t *)malloc((size_t)(out_max > 0 ? out_max : 1) * sizeof(int64_t));
    if (!datos) {
        wini_bridge_liberar_str(texto);
        return wini_list_new_i64(NULL, 0);
    }

    int64_t o = 0;
    int64_t i = 0;
    while (i < n) {
        char c0 = src[i];
        if (c0 == '=' || c0 == ' ' || c0 == '\n' || c0 == '\r' || c0 == '\t') { i++; continue; }
        int8_t v0 = B64_VALOR[(unsigned char)c0];
        if (v0 < 0) {
            wini_bridge_liberar_str(texto);
            free(datos);
            const char *msg = "b64_decodificar: carácter base64 inválido";
            wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), 0);
            /* no retorna */
        }

        /* Leemos hasta 4 caracteres base64 (c0..c3), saltando whitespace/'=' entre medio. */
        int8_t v1 = 0, v2 = -1, v3 = -1;
        int tiene1 = 0, tiene2 = 0, tiene3 = 0;

        int64_t j = i + 1;
        while (j < n) {
            char c = src[j];
            if (c == '=' || c == ' ' || c == '\n' || c == '\r' || c == '\t') { j++; continue; }
            int8_t v = B64_VALOR[(unsigned char)c];
            if (v < 0) {
                wini_bridge_liberar_str(texto);
                free(datos);
                const char *msg = "b64_decodificar: carácter base64 inválido";
                wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), 0);
            }
            if (!tiene1) { v1 = v; tiene1 = 1; }
            else if (!tiene2) { v2 = v; tiene2 = 1; }
            else { v3 = v; tiene3 = 1; break; }
            j++;
        }

        if (!tiene1) {
            wini_bridge_liberar_str(texto);
            free(datos);
            return wini_list_new_i64(NULL, 0);
        }

        int64_t entero24 = ((int64_t)v0 << 18) | ((int64_t)v1 << 12);
        if (tiene2) entero24 |= ((int64_t)v2 << 6);
        if (tiene3) entero24 |= (int64_t)v3;

        datos[o++] = (entero24 >> 16) & 0xFF;
        if (tiene2) datos[o++] = (entero24 >> 8) & 0xFF;
        if (tiene3) datos[o++] = entero24 & 0xFF;

        i = j + 1;
    }

    wini_bridge_liberar_str(texto);
    WINI_LISTA r = wini_list_new_i64(datos, o);
    free(datos);
    return r;
}

/* ---------------------------------------------------------------- */
/* b64_codificar_hex                                                */
/* ---------------------------------------------------------------- */

WINI_CADENA b64_codificar_hex(WINI_LISTA bytes) {
    if (!bytes) return wini_bridge_str("");
    int64_t n = wini_list_len(bytes);
    static const char hex[] = "0123456789abcdef";
    char *out = (char *)malloc((size_t)n * 2 + 1);
    if (!out) {
        wini_liberar_lista(bytes, 0);
        return wini_bridge_str("");
    }
    int64_t o = 0;
    for (int64_t i = 0; i < n; i++) {
        int64_t v = wini_list_get_i64(bytes, i, 0) & 0xFF;
        out[o++] = hex[(v >> 4) & 0x0F];
        out[o++] = hex[v & 0x0F];
    }
    out[o] = '\0';
    wini_liberar_lista(bytes, 0);
    WINI_CADENA r = wini_bridge_str_n(out, o);
    free(out);
    return r;
}

/* ---------------------------------------------------------------- */
/* b64_decodificar_hex                                              */
/* ---------------------------------------------------------------- */

WINI_LISTA b64_decodificar_hex(WINI_CADENA texto) {
    if (!texto) return wini_list_new_i64(NULL, 0);
    int64_t n = wini_bridge_str_len(texto);
    const char *src = wini_bridge_cstr(texto);

    /* Contamos dígitos hex válidos (ignorando espacio, \n, \r, \t). */
    int64_t digitos = 0;
    for (int64_t i = 0; i < n; i++) {
        char c = src[i];
        if (c == ' ' || c == '\n' || c == '\r' || c == '\t') continue;
        if (hex_valor(c) >= 0) digitos++;
    }
    if (digitos % 2 != 0) {
        wini_bridge_liberar_str(texto);
        const char *msg = "b64_decodificar_hex: longitud impar";
        wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), 0);
        /* no retorna */
    }

    int64_t out_n = digitos / 2;
    int64_t *datos = (int64_t *)malloc((size_t)(out_n > 0 ? out_n : 1) * sizeof(int64_t));
    if (!datos) {
        wini_bridge_liberar_str(texto);
        return wini_list_new_i64(NULL, 0);
    }

    int64_t o = 0;
    int64_t i = 0;
    while (i < n) {
        char c = src[i];
        if (c == ' ' || c == '\n' || c == '\r' || c == '\t') { i++; continue; }
        int8_t hi = hex_valor(c);
        if (hi < 0) {
            wini_bridge_liberar_str(texto);
            free(datos);
            const char *msg = "b64_decodificar_hex: dígito hexadecimal inválido";
            wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), 0);
        }
        /* Buscamos el siguiente dígito hex (salteando whitespace). */
        int64_t j = i + 1;
        int8_t lo = -1;
        while (j < n) {
            char c2 = src[j];
            if (c2 == ' ' || c2 == '\n' || c2 == '\r' || c2 == '\t') { j++; continue; }
            lo = hex_valor(c2);
            if (lo < 0) {
                wini_bridge_liberar_str(texto);
                free(datos);
                const char *msg = "b64_decodificar_hex: dígito hexadecimal inválido";
                wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), 0);
            }
            break;
        }
        if (lo < 0) {
            wini_bridge_liberar_str(texto);
            free(datos);
            const char *msg = "b64_decodificar_hex: longitud impar";
            wini_lanzar("ValueError", wini_string_new(msg, (int64_t)strlen(msg)), 0);
        }
        datos[o++] = ((int64_t)hi << 4) | (int64_t)lo;
        i = j + 1;
    }

    wini_bridge_liberar_str(texto);
    WINI_LISTA r = wini_list_new_i64(datos, o);
    free(datos);
    return r;
}

/* ---------------------------------------------------------------- */
/* versión                                                          */
/* ---------------------------------------------------------------- */

WINI_CADENA b64_version(void) {
    return wini_bridge_str("base64 1.0 (RFC 4648, C)");
}
