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

/* ---- Contenido ---- */
WINI_CADENA    leer_archivox(WINI_CADENA ruta);
WINI_BOOLEANO  agregar_a_archivox(WINI_CADENA ruta, WINI_CADENA contenido);

/* ---- Directorios ---- */
WINI_CADENA    ruta_actualx(void);
WINI_BOOLEANO  crear_directoriox(WINI_CADENA ruta);
WINI_BOOLEANO  eliminar_directoriox(WINI_CADENA ruta);
WINI_BOOLEANO  cambiar_carpetax(WINI_CADENA ruta);
WINI_LISTA     listar_archivosx(WINI_CADENA ruta);

/* ---- Sistema ---- */
WINI_CADENA    arquitecturax(void);

/* ---- Información de versión ---- */
WINI_CADENA    include__version(void);

#endif /* SISTEMA_H */