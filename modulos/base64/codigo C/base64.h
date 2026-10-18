#ifndef BASE64_H
#define BASE64_H

#include "wini_bridge.h"

/* =====================================================================
 * base64.h — base64 y hexadecimal para Wini (implementado en C)
 * ---------------------------------------------------------------------
 * Codificación/decodificación rápida sobre lista<entero> (bytes 0-255).
 *
 *   importar "base64/base64" como b64
 *   cadena s = b64.codificar([72, 101, 108, 108, 111])   # "SGVsbG8="
 *   lista<entero> b = b64.decodificar("SGVsbG8=")          # [72,101,108,108,111]
 *   cadena h = b64.codificar_hex([72, 101])                # "4865"
 *   lista<entero> b2 = b64.decodificar_hex("4865")         # [72, 101]
 *
 * Contratos (igual que el resto de módulos C):
 * - WINI_LISTA de entrada se consume; lo devuelto es NUEVO.
 * - b64_decodificar / b64_decodificar_hex lanzan ValueError ante entrada
 *   inválida (capturable con 'intentar/capturar').
 * ===================================================================== */

/* Codifica lista<entero> a base64 estándar (RFC 4648, con '=' de relleno).
 * 'bytes' se consume. Devuelve cadena nueva. */
WINI_CADENA b64_codificar(WINI_LISTA bytes);

/* Decodifica base64 a lista<entero>. Ignora whitespace y '='. Lanza
 * ValueError ante carácter inválido. 'texto' se consume. */
WINI_LISTA  b64_decodificar(WINI_CADENA texto);

/* Codifica lista<entero> a hexadecimal (minúsculas). 'bytes' se consume. */
WINI_CADENA b64_codificar_hex(WINI_LISTA bytes);

/* Decodifica hexadecimal a lista<entero>. Ignora espacios y saltos.
 * Lanza ValueError ante longitud impar o dígito inválido. 'texto' se consume. */
WINI_LISTA  b64_decodificar_hex(WINI_CADENA texto);

/* Versión del módulo. */
WINI_CADENA b64_version(void);

#endif /* BASE64_H */
