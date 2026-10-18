#define _POSIX_C_SOURCE 200809L

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <time.h>

#if defined(_WIN32) || defined(_WIN64)
    #include <windows.h>
    #include <direct.h>
    #define WINI_MKDIR(p)  _mkdir(p)
    #define WINI_RMDIR(p)  _rmdir(p)
    #define WINI_POPEN(c)  _popen(c, "r")
    #define WINI_PCLOSE(p) _pclose(p)
#else
    #include <sys/wait.h>
    #include <sys/stat.h>
    #include <unistd.h>
    #include <dirent.h>
    #define WINI_MKDIR(p)  mkdir(p, 0755)
    #define WINI_RMDIR(p)  rmdir(p)
    #define WINI_POPEN(c)  popen(c, "r")
    #define WINI_PCLOSE(c) pclose(c)
#endif

#include "wini_bridge.h"
#include "sistema.h"

/* ===================================================================== */
/* Procesos                                                              */
/* ===================================================================== */

/* Ejecuta 'linea' en el shell del sistema.
 * - 'linea' se consume siempre (vacía o NULL: devuelve -1).
 * - 'salida' verdadero: muestra la salida del comando por stdout tal cual
 *   (agregando '\n' final si falta) y devuelve el código de salida.
 * - 'salida' falso: redirige todo a nulo (silencioso) y devuelve el código.
 * En POSIX el retorno es el exit status (0 = éxito); si lo mató una señal,
 * 128 + número de señal. En Windows es el código crudo. -1 = error propio
 * (comando vacío, popen/system falló, sin memoria). */
WINI_ENTERO ejecutar(WINI_CADENA linea, WINI_BOOLEANO salida) {
    if (!linea || linea->len == 0) {
        wini_bridge_liberar_str(linea);
        return -1;
    }

    const char *cstr = wini_bridge_cstr(linea);
    int rc = 0;

    if (salida) {
        FILE *p = WINI_POPEN(cstr);
        if (!p) {
            wini_bridge_liberar_str(linea);
            return -1;
        }

        size_t cap = 4096, len = 0;
        char *buf = (char *)malloc(cap);
        if (!buf) {
            WINI_PCLOSE(p);
            wini_bridge_liberar_str(linea);
            return -1;
        }

        size_t n;
        while ((n = fread(buf + len, 1, cap - len - 1, p)) > 0) {
            len += n;
            if (len + 1 >= cap) {
                cap *= 2;
                char *tmp = (char *)realloc(buf, cap);
                if (!tmp) break;
                buf = tmp;
            }
        }
        buf[len] = '\0';

        rc = WINI_PCLOSE(p);

        if (len > 0) {
            fwrite(buf, 1, len, stdout);
            if (buf[len - 1] != '\n') fputc('\n', stdout);
        }
        free(buf);
    } else {
        size_t n = strlen(cstr);
#if defined(_WIN32) || defined(_WIN64)
        char *silencioso = (char *)malloc(n + 20);
        if (!silencioso) {
            wini_bridge_liberar_str(linea);
            return -1;
        }
        sprintf(silencioso, "%s >NUL 2>&1", cstr);
#else
        char *silencioso = (char *)malloc(n + 32);
        if (!silencioso) {
            wini_bridge_liberar_str(linea);
            return -1;
        }
        sprintf(silencioso, "%s >/dev/null 2>&1", cstr);
#endif
        rc = system(silencioso);
        free(silencioso);
    }

    wini_bridge_liberar_str(linea);

#if defined(_WIN32) || defined(_WIN64)
    return (WINI_ENTERO)rc;
#else
    if (rc == -1) return -1;
    if (WIFEXITED(rc))   return (WINI_ENTERO)WEXITSTATUS(rc);
    if (WIFSIGNALED(rc)) return (WINI_ENTERO)(128 + WTERMSIG(rc));
    return -1;
#endif
}

/* 1 = Windows, 2 = MacOS, 3 = Linux, 4 = otro Unix, 0 = desconocido.
 * No consume nada (sin parámetros). */
WINI_ENTERO plataforma(void) {
#if defined(_WIN32) || defined(_WIN64)
    return 1;
#elif defined(__APPLE__) && defined(__MACH__)
    return 2;
#elif defined(__linux__)
    return 3;
#elif defined(__unix__) || defined(__unix)
    return 4;
#else
    return 0;
#endif
}

/* ===================================================================== */
/* Archivos: predicados                                                  */
/* ===================================================================== */

/* Verdadero si 'ruta' existe (archivo o carpeta). 'ruta' se consume;
 * vacía o NULL: falso. */
WINI_BOOLEANO existe(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return 0;
    }
    const char *cstr = wini_bridge_cstr(ruta);
    WINI_BOOLEANO ok;
#if defined(_WIN32) || defined(_WIN64)
    DWORD attr = GetFileAttributesA(cstr);
    ok = (attr != INVALID_FILE_ATTRIBUTES) ? 1 : 0;
#else
    struct stat st;
    ok = (stat(cstr, &st) == 0) ? 1 : 0;
#endif
    wini_bridge_liberar_str(ruta);
    return ok;
}

/* Verdadero si 'ruta' existe y es un directorio. 'ruta' se consume;
 * vacía o NULL: falso. */
WINI_BOOLEANO es_directorio(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return 0;
    }
    const char *cstr = wini_bridge_cstr(ruta);
    WINI_BOOLEANO ok;
#if defined(_WIN32) || defined(_WIN64)
    DWORD attr = GetFileAttributesA(cstr);
    ok = (attr != INVALID_FILE_ATTRIBUTES &&
          (attr & FILE_ATTRIBUTE_DIRECTORY)) ? 1 : 0;
#else
    struct stat st;
    ok = (stat(cstr, &st) == 0 && S_ISDIR(st.st_mode)) ? 1 : 0;
#endif
    wini_bridge_liberar_str(ruta);
    return ok;
}

/* ===================================================================== */
/* Archivos: acciones                                                    */
/* ===================================================================== */

/* Crea el archivo vacío (o lo trunca si ya existía, modo "w"). 'ruta' se
 * consume; vacía o NULL: falso. Falso también si no se puede crear
 * (permiso, carpeta inexistente: NO crea carpetas padre). */
WINI_BOOLEANO crear_archivox(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return 0;
    }
    const char *cstr = wini_bridge_cstr(ruta);
    FILE *file = fopen(cstr, "w");
    WINI_BOOLEANO ok;
    if (!file) {
        ok = 0;
    } else {
        fclose(file);
        ok = 1;
    }
    wini_bridge_liberar_str(ruta);
    return ok;
}

/* Borra el archivo. 'ruta' se consume; vacía o NULL: falso. Falso si no
 * existía o no se pudo borrar. */
WINI_BOOLEANO eliminar_archivox(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return 0;
    }
    const char *cstr = wini_bridge_cstr(ruta);
    WINI_BOOLEANO ok = (remove(cstr) == 0) ? 1 : 0;
    wini_bridge_liberar_str(ruta);
    return ok;
}

/* Renombra/mueve dentro del mismo sistema de archivos. Ambas se consumen;
 * alguna vacía o NULL: falso. */
WINI_BOOLEANO renombrar_archivox(WINI_CADENA vieja, WINI_CADENA nueva) {
    WINI_BOOLEANO ok = 0;
    if (vieja && nueva && vieja->len > 0 && nueva->len > 0) {
        const char *cstr_v = wini_bridge_cstr(vieja);
        const char *cstr_n = wini_bridge_cstr(nueva);
        ok = (rename(cstr_v, cstr_n) == 0) ? 1 : 0;
    }
    wini_bridge_liberar_str(vieja);
    wini_bridge_liberar_str(nueva);
    return ok;
}

/* Copia binaria (modo "rb"/"wb", 8 KiB por pasada). Ambas se consumen;
 * alguna vacía o NULL, origen ilegible o destino no escribible: falso
 * (el destino puede quedar a medio escribir). */
WINI_BOOLEANO copiar_archivox(WINI_CADENA origen, WINI_CADENA destino) {
    WINI_BOOLEANO ok = 0;
    FILE *src = NULL, *dst = NULL;
    char *buffer = NULL;

    if (!origen || !destino || origen->len == 0 || destino->len == 0) goto fin;

    const char *cstr_origen  = wini_bridge_cstr(origen);
    const char *cstr_destino = wini_bridge_cstr(destino);

    src = fopen(cstr_origen, "rb");
    if (!src) goto fin;

    dst = fopen(cstr_destino, "wb");
    if (!dst) goto fin;

    buffer = (char *)malloc(8192);
    if (!buffer) goto fin;

    size_t bytes;
    while ((bytes = fread(buffer, 1, 8192, src)) > 0) {
        if (fwrite(buffer, 1, bytes, dst) != bytes) goto fin;
    }
    if (ferror(src)) goto fin;

    ok = 1;

fin:
    if (buffer) free(buffer);
    if (src) fclose(src);
    if (dst) fclose(dst);
    wini_bridge_liberar_str(origen);
    wini_bridge_liberar_str(destino);
    return ok;
}

/* Mueve (rename, y si cruza dispositivos copia+borra). Ambas se consumen;
 * alguna vacía o NULL: falso. */
WINI_BOOLEANO mover_archivox(WINI_CADENA origen, WINI_CADENA destino) {
    WINI_BOOLEANO ok = 0;
    if (origen && destino && origen->len > 0 && destino->len > 0) {
        const char *cstr_o = wini_bridge_cstr(origen);
        const char *cstr_d = wini_bridge_cstr(destino);
        if (rename(cstr_o, cstr_d) == 0) {
            ok = 1;
        } else {
#if !defined(_WIN32)
            if (errno == EXDEV) {
                ok = copiar_archivox(wini_bridge_retener_str(origen),
                                     wini_bridge_retener_str(destino));
                if (ok) {
                    remove(cstr_o);
                }
            }
#endif
        }
    }
    wini_bridge_liberar_str(origen);
    wini_bridge_liberar_str(destino);
    return ok;
}

/* Tamaño en bytes, o -1 si no existe, no es archivo regular o no se pudo
 * consultar. 'ruta' se consume; vacía o NULL: -1. */
WINI_ENTERO peso_archivox(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return -1;
    }
    const char *cstr = wini_bridge_cstr(ruta);
    WINI_ENTERO tam = -1;
#if defined(_WIN32) || defined(_WIN64)
    WIN32_FILE_ATTRIBUTE_DATA info;
    if (GetFileAttributesExA(cstr, GetFileExInfoStandard, &info)) {
        LARGE_INTEGER li;
        li.LowPart  = info.nFileSizeLow;
        li.HighPart = info.nFileSizeHigh;
        tam = (WINI_ENTERO)li.QuadPart;
    }
#else
    struct stat st;
    if (stat(cstr, &st) == 0 && S_ISREG(st.st_mode)) {
        tam = (WINI_ENTERO)st.st_size;
    }
#endif
    wini_bridge_liberar_str(ruta);
    return tam;
}

/* ===================================================================== */
/* Contenido                                                             */
/* ===================================================================== */

/* Lee el archivo COMPLETO a memoria (binario seguro: conserva NULs
 * intermedios, la longitud va en WiniString.len). 'ruta' se consume.
 * Devuelve cadena vacía ante CUALQUIER error (no existe, es directorio,
 * sin permiso, fallo de lectura): no distingue "vacío" de "error", así
 * que para un mensaje útil conviene existe_archivo() antes. OJO: carga
 * todo de una vez; no usar con archivos gigantes. */
WINI_CADENA leer_archivox(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return wini_bridge_str("");
    }

    const char *cstr = wini_bridge_cstr(ruta);
    FILE *file = fopen(cstr, "rb");
    if (!file) {
        wini_bridge_liberar_str(ruta);
        return wini_bridge_str("");
    }

    if (fseek(file, 0, SEEK_END) != 0) {
        fclose(file);
        wini_bridge_liberar_str(ruta);
        return wini_bridge_str("");
    }
    long size = ftell(file);
    if (size < 0) {
        fclose(file);
        wini_bridge_liberar_str(ruta);
        return wini_bridge_str("");
    }
    rewind(file);

    char *buffer = (char *)malloc((size_t)size + 1);
    if (!buffer) {
        fclose(file);
        wini_bridge_liberar_str(ruta);
        return wini_bridge_str("");
    }

    size_t leidos = fread(buffer, 1, (size_t)size, file);
    buffer[leidos] = '\0';
    fclose(file);

    WINI_CADENA resultado = wini_bridge_str_n(buffer, (int64_t)leidos);
    free(buffer);
    wini_bridge_liberar_str(ruta);
    return resultado;
}

/* Agrega 'contenido' AL FINAL del archivo (modo "a"; lo crea si no
 * existe). Ambas se consumen. Verdadero si se escribieron todos los
 * bytes; falso si no se pudo abrir/escribir. Para REESCRIBIR desde cero
 * usar escribir_archivox. */
WINI_BOOLEANO agregar_a_archivox(WINI_CADENA ruta, WINI_CADENA contenido) {
    WINI_BOOLEANO ok = 0;
    FILE *file = NULL;

    if (!ruta || !contenido || ruta->len == 0) goto fin;

    const char *cstr_ruta      = wini_bridge_cstr(ruta);
    const char *cstr_contenido = wini_bridge_cstr(contenido);

    file = fopen(cstr_ruta, "a");
    if (!file) goto fin;

    size_t n = contenido->len;
    if (n > 0) {
        if (fwrite(cstr_contenido, 1, n, file) != n) goto fin;
    }
    ok = 1;

fin:
    if (file) fclose(file);
    wini_bridge_liberar_str(ruta);
    wini_bridge_liberar_str(contenido);
    return ok;
}

/* Sobrescribe el archivo con 'contenido' (modo "w": trunca si existía,
 * lo crea si no). Misma convención que agregar_a_archivox en todo
 * (consume ambas, falso si no se pudo abrir/escribir). Es la primitiva
 * para emitir archivos generados (p. ej. un .ll): no necesita borrar
 * antes con eliminar_archivox. */
WINI_BOOLEANO escribir_archivox(WINI_CADENA ruta, WINI_CADENA contenido) {
    WINI_BOOLEANO ok = 0;
    FILE *file = NULL;

    if (!ruta || !contenido || ruta->len == 0) goto fin;

    const char *cstr_ruta      = wini_bridge_cstr(ruta);
    const char *cstr_contenido = wini_bridge_cstr(contenido);

    file = fopen(cstr_ruta, "w");
    if (!file) goto fin;

    size_t n = contenido->len;
    if (n > 0) {
        if (fwrite(cstr_contenido, 1, n, file) != n) goto fin;
    }
    ok = 1;

fin:
    if (file) fclose(file);
    wini_bridge_liberar_str(ruta);
    wini_bridge_liberar_str(contenido);
    return ok;
}

/* ===================================================================== */
/* Directorios                                                           */
/* ===================================================================== */

/* Carpeta de trabajo actual como cadena nueva (rc=1). Vacía si falla
 * (buffer de 4 KiB: rutas más largas dan ""). */
WINI_CADENA ruta_actualx(void) {
    char buffer[4096];
#if defined(_WIN32) || defined(_WIN64)
    if (!_getcwd(buffer, (int)sizeof(buffer))) return wini_bridge_str("");
#else
    if (!getcwd(buffer, sizeof(buffer))) return wini_bridge_str("");
#endif
    return wini_bridge_str(buffer);
}

/* Crea UN nivel de carpeta (0755 en POSIX; no es recursiva: el padre debe
 * existir). 'ruta' se consume; vacía o NULL: falso. Falso si ya existía.
 * Ver nota de Windows en listar_archivosx para MSVC no-MinGW. */
WINI_BOOLEANO crear_directoriox(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return 0;
    }
    const char *cstr = wini_bridge_cstr(ruta);
    WINI_BOOLEANO ok;
#if defined(_WIN32) || defined(_WIN64)
    ok = CreateDirectoryA(cstr, NULL) ? 1 : 0;
#else
    ok = (WINI_MKDIR(cstr) == 0) ? 1 : 0;
#endif
    wini_bridge_liberar_str(ruta);
    return ok;
}

/* Borra una carpeta VACÍA. 'ruta' se consume; vacía o NULL: falso.
 * Falso si no está vacía o no se pudo borrar. */
WINI_BOOLEANO eliminar_directoriox(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return 0;
    }
    const char *cstr = wini_bridge_cstr(ruta);
    WINI_BOOLEANO ok;
#if defined(_WIN32) || defined(_WIN64)
    ok = RemoveDirectoryA(cstr) ? 1 : 0;
#else
    ok = (WINI_RMDIR(cstr) == 0) ? 1 : 0;
#endif
    wini_bridge_liberar_str(ruta);
    return ok;
}

/* Cambia la carpeta de trabajo del PROCESO (afecta a todo lo que corra
 * después, incluidas rutas relativas y subprocesos). 'ruta' se consume;
 * vacía o NULL: falso. */
WINI_BOOLEANO cambiar_carpetax(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return 0;
    }
    const char *cstr = wini_bridge_cstr(ruta);
#if defined(_WIN32) || defined(_WIN64)
    WINI_BOOLEANO ok = (_chdir(cstr) == 0) ? 1 : 0;
#else
    WINI_BOOLEANO ok = (chdir(cstr) == 0) ? 1 : 0;
#endif
    wini_bridge_liberar_str(ruta);
    return ok;
}

/* Nombres (sin "." ni "..") como lista<cadena> NUEVA (rc=1). 'ruta' se
 * consume. Lista vacía si no se pudo abrir/leer (mismo criterio que
 * leer_archivox: no distingue "vacía" de "error"). ORDEN NO GARANTIZADO
 * (el del sistema de archivos): ordenar en Wini si importa. En MSVC
 * no-MinGW devuelve siempre vacía (sin dirent). */
WINI_LISTA listar_archivosx(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return wini_list_new_str(NULL, 0);
    }

#if defined(_WIN32) && !defined(__MINGW32__)
    wini_bridge_liberar_str(ruta);
    return wini_list_new_str(NULL, 0);
#else
    const char *cstr = wini_bridge_cstr(ruta);
    DIR *dir = opendir(cstr);
    if (!dir) {
        wini_bridge_liberar_str(ruta);
        return wini_list_new_str(NULL, 0);
    }

    struct dirent *entry;
    int64_t n = 0;
    while ((entry = readdir(dir)) != NULL) {
        if (strcmp(entry->d_name, ".") != 0 &&
            strcmp(entry->d_name, "..") != 0) {
            n++;
        }
    }

    WiniString **buf = NULL;
    if (n > 0) {
        buf = (WiniString **)malloc((size_t)n * sizeof(WiniString *));
        if (!buf) {
            closedir(dir);
            wini_bridge_liberar_str(ruta);
            return wini_list_new_str(NULL, 0);
        }
    }

    rewinddir(dir);
    int64_t i = 0;
    while ((entry = readdir(dir)) != NULL && i < n) {
        if (strcmp(entry->d_name, ".") == 0 ||
            strcmp(entry->d_name, "..") == 0) continue;

        WINI_CADENA s = wini_bridge_str(entry->d_name);
        if (!s) {
            for (int64_t j = 0; j < i; j++) wini_bridge_liberar_str(buf[j]);
            free(buf);
            closedir(dir);
            wini_bridge_liberar_str(ruta);
            return wini_list_new_str(NULL, 0);
        }
        buf[i++] = s;
    }
    closedir(dir);

    WINI_LISTA lista = wini_list_new_str(buf, n);
    free(buf);

    wini_bridge_liberar_str(ruta);
    return lista;
#endif
}

/* ===================================================================== */
/* Salida                                                              */
/* ===================================================================== */

/* Imprime 'texto' TAL CUAL por stdout (sin agregar nada) y, si 'salto'
 * es verdadero, un '\n' final. Es el printf(3) sin formato: el formateo
 * se arma del lado Wini (c"...") y acá solo se vuelca. 'texto' se
 * consume siempre. Devuelve los bytes escritos o -1 si falló la
 * escritura. Ver imprimir() en sistem.cwn para la versión con salto
 * predeterminado. */
WINI_ENTERO imprimirx(WINI_CADENA texto, WINI_BOOLEANO salto) {
    if (!texto) {
        return -1;
    }
    const char *cstr = wini_bridge_cstr(texto);
    size_t n = (size_t)texto->len;
    if (n > 0) {
        if (fwrite(cstr, 1, n, stdout) != n) {
            wini_bridge_liberar_str(texto);
            return -1;
        }
    }
    if (salto) {
        if (fputc('\n', stdout) == EOF) {
            wini_bridge_liberar_str(texto);
            return -1;
        }
        n++;
    }
    fflush(stdout);
    wini_bridge_liberar_str(texto);
    return (WINI_ENTERO)n;
}

/* ===================================================================== */
/* Sistema                                                               */
/* ===================================================================== */

/* "x86_64", "x86", "arm64", "arm" o "desconocida", como cadena nueva. */
WINI_CADENA arquitecturax(void) {
#if defined(__x86_64__) || defined(_M_X64)
    return wini_bridge_str("x86_64");
#elif defined(__i386) || defined(_M_IX86)
    return wini_bridge_str("x86");
#elif defined(__aarch64__)
    return wini_bridge_str("arm64");
#elif defined(__arm__) || defined(_M_ARM)
    return wini_bridge_str("arm");
#else
    return wini_bridge_str("desconocida");
#endif
}

/* Versión del módulo como cadena nueva (hoy "__2.wini()"). */
WINI_CADENA include__version(void) {
    return wini_bridge_str("__2.wini()");
}

/* ===================================================================== */
/* Entorno                                                               */
/* ===================================================================== */

/* Valor de la variable de entorno 'nombre'. 'nombre' se consume.
 * Devuelve cadena nueva (el valor) o "" si no existe. */
WINI_CADENA entornox(WINI_CADENA nombre) {
    if (!nombre || nombre->len == 0) {
        wini_bridge_liberar_str(nombre);
        return wini_bridge_str("");
    }
    const char *cstr = wini_bridge_cstr(nombre);
#if defined(_WIN32) || defined(_WIN64)
    char buf[4096];
    DWORD n = GetEnvironmentVariableA(cstr, buf, sizeof(buf));
    wini_bridge_liberar_str(nombre);
    if (n == 0 || n > sizeof(buf)) return wini_bridge_str("");
    return wini_bridge_str_n(buf, (int64_t)n);
#else
    const char *valor = getenv(cstr);
    wini_bridge_liberar_str(nombre);
    if (!valor) return wini_bridge_str("");
    return wini_bridge_str(valor);
#endif
}

/* Define/sobrescribe la variable de entorno 'nombre' = 'valor'.
 * Ambas se consumen. Devuelve verdadero si se pudo. */
WINI_BOOLEANO definir_entornox(WINI_CADENA nombre, WINI_CADENA valor) {
    if (!nombre || nombre->len == 0) {
        wini_bridge_liberar_str(nombre);
        wini_bridge_liberar_str(valor);
        return 0;
    }
    const char *cnom = wini_bridge_cstr(nombre);
    const char *cval = valor ? wini_bridge_cstr(valor) : "";
#if defined(_WIN32) || defined(_WIN64)
    int ok = _putenv_s(cnom, cval) == 0 ? 1 : 0;
#else
    int ok = setenv(cnom, cval, 1) == 0 ? 1 : 0;
#endif
    wini_bridge_liberar_str(nombre);
    wini_bridge_liberar_str(valor);
    return ok;
}

/* Elimina la variable de entorno 'nombre'. 'nombre' se consume.
 * Devuelve verdadero si se hizo (o ya no existía). */
WINI_BOOLEANO quitar_entornox(WINI_CADENA nombre) {
    if (!nombre || nombre->len == 0) {
        wini_bridge_liberar_str(nombre);
        return 0;
    }
    const char *cstr = wini_bridge_cstr(nombre);
#if defined(_WIN32) || defined(_WIN64)
    /* En MSVC no hay unsetenv: se establece a vacío. */
    int ok = _putenv_s(cstr, "") == 0 ? 1 : 0;
#else
    int ok = unsetenv(cstr) == 0 ? 1 : 0;
#endif
    wini_bridge_liberar_str(nombre);
    return ok;
}

/* Directorio home del usuario. Devuelve cadena nueva o "" si no se pudo. */
WINI_CADENA directorio_homex(void) {
#if defined(_WIN32) || defined(_WIN64)
    char buf[4096];
    DWORD n = GetEnvironmentVariableA("USERPROFILE", buf, sizeof(buf));
    if (n == 0 || n > sizeof(buf)) {
        n = GetEnvironmentVariableA("HOME", buf, sizeof(buf));
        if (n == 0 || n > sizeof(buf)) return wini_bridge_str("");
    }
    return wini_bridge_str_n(buf, (int64_t)n);
#else
    const char *home = getenv("HOME");
    if (!home) return wini_bridge_str("");
    return wini_bridge_str(home);
#endif
}

/* ===================================================================== */
/* Directorios recursivos                                                */
/* ===================================================================== */

/* Crea todos los directorios intermedios necesarios (mkdir -p).
 * 'ruta' se consume. Devuelve verdadero si se creó o ya existía. */
WINI_BOOLEANO crear_directorios_recursivosx(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return 0;
    }
    const char *cstr = wini_bridge_cstr(ruta);

    char *copia = (char *)malloc((size_t)ruta->len + 1);
    if (!copia) {
        wini_bridge_liberar_str(ruta);
        return 0;
    }
    memcpy(copia, cstr, ruta->len);
    copia[ruta->len] = '\0';

#if defined(_WIN32) || defined(_WIN64)
    for (char *p = copia; *p; p++) {
        if (*p == '\\') *p = '/';
    }
#endif

    char acumulado[4096] = "";
    char *inicio = copia;

#if !defined(_WIN32) && !defined(_WIN64)
    if (*inicio == '/') {
        strncat(acumulado, "/", sizeof(acumulado) - 1);
        inicio++;
    }
#else
    if (copia[0] && copia[1] == ':') {
        snprintf(acumulado, sizeof(acumulado), "%c:", copia[0]);
        inicio = copia + 2;
        if (*inicio == '/') inicio++;
    }
#endif

    WINI_BOOLEANO ok = 1;
    char *token = strtok(inicio, "/");
    while (token && ok) {
        size_t len = strlen(acumulado);
        if (len > 0) {
            char ultimo = acumulado[len - 1];
            int agregar_sep = 1;
#if defined(_WIN32) || defined(_WIN64)
            if (ultimo == '\\') agregar_sep = 0;
            if (len == 2 && acumulado[1] == ':') agregar_sep = 0;
#else
            if (ultimo == '/') agregar_sep = 0;
#endif
            if (agregar_sep) {
#if defined(_WIN32) || defined(_WIN64)
                strncat(acumulado, "\\", sizeof(acumulado) - len - 1);
#else
                strncat(acumulado, "/", sizeof(acumulado) - len - 1);
#endif
            }
        }
        strncat(acumulado, token, sizeof(acumulado) - strlen(acumulado) - 1);

#if defined(_WIN32) || defined(_WIN64)
        if (GetFileAttributesA(acumulado) == INVALID_FILE_ATTRIBUTES) {
            if (!CreateDirectoryA(acumulado, NULL)) ok = 0;
        }
#else
        if (mkdir(acumulado, 0755) != 0 && errno != EEXIST) ok = 0;
#endif
        token = strtok(NULL, "/");
    }

    free(copia);
    wini_bridge_liberar_str(ruta);
    return ok;
}

/* Elimina un directorio y todo su contenido (rm -rf).
 * 'ruta' se consume. Devuelve verdadero si se eliminó o no existía. */
WINI_BOOLEANO eliminar_arbolx(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return 0;
    }
    const char *cstr = wini_bridge_cstr(ruta);

#if defined(_WIN32) || defined(_WIN64)
    WIN32_FIND_DATA findData;
    char buscar[4096];
    snprintf(buscar, sizeof(buscar), "%s\\*", cstr);
    HANDLE hFind = FindFirstFileA(buscar, &findData);
    if (hFind != INVALID_HANDLE_VALUE) {
        do {
            if (strcmp(findData.cFileName, ".") == 0 ||
                strcmp(findData.cFileName, "..") == 0) continue;
            char hijo[4096];
            snprintf(hijo, sizeof(hijo), "%s\\%s", cstr, findData.cFileName);
            WINI_CADENA hijo_c = wini_bridge_str(hijo);
            if (!eliminar_arbolx(hijo_c)) {
                FindClose(hFind);
                wini_bridge_liberar_str(ruta);
                return 0;
            }
        } while (FindNextFileA(hFind, &findData));
        FindClose(hFind);
    }
    if (GetFileAttributesA(cstr) & FILE_ATTRIBUTE_DIRECTORY) {
        if (!RemoveDirectoryA(cstr)) {
            wini_bridge_liberar_str(ruta);
            return 0;
        }
    } else {
        if (!DeleteFileA(cstr)) {
            wini_bridge_liberar_str(ruta);
            return 0;
        }
    }
#else
    struct stat st;
    if (stat(cstr, &st) != 0) {
        wini_bridge_liberar_str(ruta);
        return 1; /* no existe */
    }
    if (S_ISDIR(st.st_mode)) {
        DIR *d = opendir(cstr);
        if (d) {
            struct dirent *ent;
            while ((ent = readdir(d)) != NULL) {
                if (strcmp(ent->d_name, ".") == 0 ||
                    strcmp(ent->d_name, "..") == 0) continue;
                char hijo[4096];
                snprintf(hijo, sizeof(hijo), "%s/%s", cstr, ent->d_name);
                WINI_CADENA hijo_c = wini_bridge_str(hijo);
                if (!eliminar_arbolx(hijo_c)) {
                    closedir(d);
                    wini_bridge_liberar_str(ruta);
                    return 0;
                }
            }
            closedir(d);
        }
        if (rmdir(cstr) != 0) {
            wini_bridge_liberar_str(ruta);
            return 0;
        }
    } else {
        if (remove(cstr) != 0) {
            wini_bridge_liberar_str(ruta);
            return 0;
        }
    }
#endif
    wini_bridge_liberar_str(ruta);
    return 1;
}

/* ===================================================================== */
/* Temp y rutas                                                          */
/* ===================================================================== */

/* Directorio temporal del sistema. */
WINI_CADENA temp_dirx(void) {
#if defined(_WIN32) || defined(_WIN64)
    char buf[4096];
    DWORD n = GetTempPathA(sizeof(buf), buf);
    if (n == 0 || n > sizeof(buf)) return wini_bridge_str("");
    return wini_bridge_str_n(buf, (int64_t)n);
#else
    const char *tmp = getenv("TMPDIR");
    if (!tmp || tmp[0] == '\0') tmp = "/tmp";
    return wini_bridge_str(tmp);
#endif
}

/* Separador de ruta: "/" en Unix, "\\" en Windows. */
WINI_CADENA separador_rutax(void) {
#if defined(_WIN32) || defined(_WIN64)
    return wini_bridge_str("\\");
#else
    return wini_bridge_str("/");
#endif
}

/* ===================================================================== */
/* Fecha de modificación                                                 */
/* ===================================================================== */

/* Fecha de última modificación de 'ruta' (formato legible) o "" si falló.
 * 'ruta' se consume. */
WINI_CADENA fecha_modificacionx(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return wini_bridge_str("");
    }
    const char *cstr = wini_bridge_cstr(ruta);
    struct stat st;
    if (stat(cstr, &st) != 0) {
        wini_bridge_liberar_str(ruta);
        return wini_bridge_str("");
    }
    struct tm *tm = localtime(&st.st_mtime);
    if (!tm) {
        wini_bridge_liberar_str(ruta);
        return wini_bridge_str("");
    }
    char buf[64];
    strftime(buf, sizeof(buf), "%Y-%m-%d %H:%M:%S", tm);
    wini_bridge_liberar_str(ruta);
    return wini_bridge_str(buf);
}