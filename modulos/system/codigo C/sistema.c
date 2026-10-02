#define _POSIX_C_SOURCE 200809L

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>

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

/* ===================================================================== */
/* Directorios                                                           */
/* ===================================================================== */

WINI_CADENA ruta_actualx(void) {
    char buffer[4096];
#if defined(_WIN32) || defined(_WIN64)
    if (!_getcwd(buffer, (int)sizeof(buffer))) return wini_bridge_str("");
#else
    if (!getcwd(buffer, sizeof(buffer))) return wini_bridge_str("");
#endif
    return wini_bridge_str(buffer);
}

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

WINI_LISTA listar_archivosx(WINI_CADENA ruta) {
    if (!ruta || ruta->len == 0) {
        wini_bridge_liberar_str(ruta);
        return wini_bridge_list_cadena(NULL, 0);
    }

#if defined(_WIN32) && !defined(__MINGW32__)
    wini_bridge_liberar_str(ruta);
    return wini_bridge_list_cadena(NULL, 0);
#else
    const char *cstr = wini_bridge_cstr(ruta);
    DIR *dir = opendir(cstr);
    if (!dir) {
        wini_bridge_liberar_str(ruta);
        return wini_bridge_list_cadena(NULL, 0);
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
            return wini_bridge_list_cadena(NULL, 0);
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
            return wini_bridge_list_cadena(NULL, 0);
        }
        buf[i++] = s;
    }
    closedir(dir);

    WINI_LISTA lista = wini_bridge_list_cadena(buf, n);
    free(buf);

    wini_bridge_liberar_str(ruta);
    return lista;
#endif
}

/* ===================================================================== */
/* Sistema                                                               */
/* ===================================================================== */

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

WINI_CADENA include__version(void) {
    return wini_bridge_str("__2.wini()");
}