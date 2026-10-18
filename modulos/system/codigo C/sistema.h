#ifndef SISTEMA_H
#define SISTEMA_H

#include "wini_bridge.h"

/* ---- Procesos ---- */
WINI_ENTERO    ejecutar(WINI_CADENA linea, WINI_BOOLEANO salida);
WINI_ENTERO    plataforma(void);

/* ---- Archivos: predicados ---- */
WINI_BOOLEANO  existe(WINI_CADENA ruta);
WINI_BOOLEANO  es_directorio(WINI_CADENA ruta);

/* ---- Archivos: acciones ---- */
WINI_BOOLEANO  crear_archivox(WINI_CADENA ruta);
WINI_BOOLEANO  eliminar_archivox(WINI_CADENA ruta);
WINI_BOOLEANO  renombrar_archivox(WINI_CADENA vieja, WINI_CADENA nueva);
WINI_BOOLEANO  copiar_archivox(WINI_CADENA origen, WINI_CADENA destino);
WINI_BOOLEANO  mover_archivox(WINI_CADENA origen, WINI_CADENA destino);
WINI_ENTERO    peso_archivox(WINI_CADENA ruta);

/* ---- Contenido ----
 *
 * Contratos comunes: 'ruta' y 'contenido' se CONSUMEN siempre (se libera
 * una referencia, venga de variable o de literal); el caller que siga
 * usando esas variables debe retenerlas antes de llamar (ver
 * wini_bridge.h). Los errores de E/S devuelven cadena vacía / falso / -1
 * en vez de lanzar: distinguir "vacío" de "error" con existe_archivo().
 */
WINI_CADENA    leer_archivox(WINI_CADENA ruta);
WINI_BOOLEANO  agregar_a_archivox(WINI_CADENA ruta, WINI_CADENA contenido);
/* Sobrescribe (trunca) o crea. Ver escribir_archivox en sistema.c. */
WINI_BOOLEANO  escribir_archivox(WINI_CADENA ruta, WINI_CADENA contenido);

/* ---- Directorios ---- */
WINI_CADENA    ruta_actualx(void);
WINI_BOOLEANO  crear_directoriox(WINI_CADENA ruta);
WINI_BOOLEANO  eliminar_directoriox(WINI_CADENA ruta);
WINI_BOOLEANO  cambiar_carpetax(WINI_CADENA ruta);
WINI_LISTA     listar_archivosx(WINI_CADENA ruta);

/* ---- Salida ----
 * Ver imprimirx en sistema.c. El salto final va como parámetro explícito
 * acá; el valor por defecto vive en el wrapper Wini (las 'externa' no
 * admiten defaults: romperían el ABI de C). */
WINI_ENTERO    imprimirx(WINI_CADENA texto, WINI_BOOLEANO salto);

/* ---- Sistema ---- */
WINI_CADENA    arquitecturax(void);

/* ---- Entorno ---- */
/* Valor de la variable de entorno 'nombre' (cadena nueva) o "" si no
 * existe. 'nombre' se consume. */
WINI_CADENA    entornox(WINI_CADENA nombre);
/* Define/sobrescribe la variable de entorno 'nombre' = 'valor'.
 * Devuelve verdadero si se pudo. Ambas se consumen. */
WINI_BOOLEANO  definir_entornox(WINI_CADENA nombre, WINI_CADENA valor);
/* Elimina la variable de entorno 'nombre'. Devuelve verdadero si se hizo.
 * 'nombre' se consume. */
WINI_BOOLEANO  quitar_entornox(WINI_CADENA nombre);
/* Directorio home del usuario (cadena nueva) o "" si no se pudo. */
WINI_CADENA    directorio_homex(void);

/* ---- Directorios recursivos ---- */
/* Crea todos los directorios intermedios necesarios (mkdir -p).
 * Devuelve verdadero si se creó o ya existía. 'ruta' se consume. */
WINI_BOOLEANO  crear_directorios_recursivosx(WINI_CADENA ruta);
/* Elimina un directorio y todo su contenido (rm -rf).
 * Devuelve verdadero si se eliminó o no existía. 'ruta' se consume. */
WINI_BOOLEANO  eliminar_arbolx(WINI_CADENA ruta);

/* ---- Temp y rutas ---- */
/* Directorio temporal del sistema. */
WINI_CADENA    temp_dirx(void);
/* Separador de ruta: "/" en Unix, "\\" en Windows. */
WINI_CADENA    separador_rutax(void);

/* ---- Fecha de modificación ---- */
/* Fecha de última modificación de 'ruta' (formato legible) o "" si falló.
 * 'ruta' se consume. */
WINI_CADENA    fecha_modificacionx(WINI_CADENA ruta);

/* ---- Información de versión ---- */
WINI_CADENA    include__version(void);

#endif /* SISTEMA_H */