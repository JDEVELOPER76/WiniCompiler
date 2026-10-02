#ifndef WINI_RUNTIME_H
#define WINI_RUNTIME_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef struct {
    int64_t len;
    char *data;
    int64_t rc; // conteo de referencias: cuántas variables/listas/diccionarios apuntan aquí
} WiniString;

// ---- strings (ya existentes) ----
WiniString *wini_string_new(const char *src, int64_t len);

// ---- longitud y acceso por índice de una cadena ----
// Usadas por 'para ... en' cuando el iterable es una cadena: recorre sus
// bytes uno a uno, cada uno envuelto en una WiniString propia de 1 byte
// (Wini no tiene un tipo 'caracter' separado). No hace nada especial con
// UTF-8: es acceso a bytes, igual que el resto del runtime de cadenas.
int64_t wini_string_len(WiniString *s);
WiniString *wini_string_get_char(WiniString *s, int64_t idx, int64_t linea);

// ---- conteo de referencias (retener / liberar) ----
//
// Cadenas, listas y diccionarios viven en el heap y se comparten por
// puntero: asignar una variable a otra ('b = a'), guardar un valor dentro
// de una lista/diccionario, o leerlo de vuelta, NO copia los datos; todas
// esas ubicaciones terminan apuntando al mismo bloque de memoria. Antes,
// 'liberar' hacía un free() incondicional, así que dos ubicaciones que
// compartían el mismo puntero producían un doble-free o un
// use-after-free en cuanto una de ellas lo liberaba.
//
// Ahora cada bloque lleva un contador 'rc' (inicializado en 1 al crearlo).
// 'wini_*_retener' lo incrementa: se usa cada vez que un valor ya
// existente (no recién construido) se copia a una nueva ubicación que
// puede sobrevivir de forma independiente (otra variable, un elemento de
// lista/diccionario, un valor leído de una lista/diccionario y guardado
// en una variable). 'wini_liberar_*' ahora decrementa el contador y solo
// libera la memoria real cuando llega a 0.
//
// Esto NO es un recolector de basura ni un borrow-checker: sigue siendo
// responsabilidad del código Wini (o del codegen) llamar a 'liberar' por
// cada referencia que retuvo. Lo que cambia es que compartir un mismo
// valor entre varias ubicaciones deja de ser un crash inmediato y pasa a
// ser, en el peor caso, una fuga de memoria si falta algún 'liberar' —
// mucho más seguro que un doble-free o un puntero colgante.
WiniString *wini_string_retener(WiniString *s);

// Concatenación de cadenas
WiniString* wini_string_concat(WiniString* a, WiniString* b);
// Conversiones a cadena
WiniString* wini_i64_to_string(int64_t v);
WiniString* wini_f64_to_string(double v);
WiniString* wini_bool_to_string(int8_t v);
void wini_print_i64(int64_t value);
void wini_print_f64(double value);
void wini_print_bool(_Bool value);
void wini_print_string(WiniString *value);
void wini_print_list(void* list, int tipo);
void wini_print_dict(void* dict, int tipoClave, int tipoValor);
WiniString* wini_read_string(WiniString* prompt);

// ---- listas ----
// Representación opaca de una lista: { len, data* }
typedef struct {
    int64_t len;
    void *data;   // puntero al array de elementos
    int64_t rc;   // conteo de referencias (ver nota junto a WiniString)
} WiniList;

WiniList *wini_list_retener(WiniList *lista);

// Crear lista a partir de un array de elementos (el caller pasa el array)
WiniList *wini_list_new_i64(int64_t *data, int64_t len);
WiniList *wini_list_new_f64(double *data, int64_t len);
WiniList *wini_list_new_i1(int8_t *data, int64_t len);
WiniList *wini_list_new_str(WiniString **data, int64_t len);

// Largo de una lista
int64_t wini_list_len(WiniList *list);

// Obtener elemento
int64_t wini_list_get_i64(WiniList *list, int64_t idx, int64_t linea);
double  wini_list_get_f64(WiniList *list, int64_t idx, int64_t linea);
int8_t  wini_list_get_i1 (WiniList *list, int64_t idx, int64_t linea);
WiniString* wini_list_get_str(WiniList *list, int64_t idx, int64_t linea);

// Asignar elemento
void wini_list_set_i64(WiniList *list, int64_t idx, int64_t value, int64_t linea);
void wini_list_set_f64(WiniList *list, int64_t idx, double  value, int64_t linea);
void wini_list_set_i1 (WiniList *list, int64_t idx, int8_t  value, int64_t linea);
void wini_list_set_str(WiniList *list, int64_t idx, WiniString *value, int64_t linea);
// Concatenar dos listas del mismo tipo de elemento (código tipo 0=i64,1=f64,2=i1,3=str)
WiniList* wini_list_concat(WiniList* a, WiniList* b, int tipo);

// ---- diccionarios ----
// Representación opaca y genérica: { len, cap, claves*, valores* }.
// Los tipos concretos de 'claves' y 'valores' (int64_t, double, int8_t o
// WiniString*) son conocidos en tiempo de compilación por el codegen de
// Wini, que llama siempre a la variante tipada correspondiente (sufijos
// i64/f64/i1/str tanto para la clave como para el valor).
typedef struct {
    int64_t len;
    int64_t cap;
    void *claves;
    void *valores;
    int64_t rc; // conteo de referencias (ver nota junto a WiniString)
} WiniDict;

WiniDict *wini_dict_retener(WiniDict *dict);

// Crear un diccionario vacío. No depende de los tipos de clave/valor:
// las 16 combinaciones comparten la misma construcción inicial.
WiniDict *wini_dict_new(void);

// Largo de un diccionario
int64_t wini_dict_len(WiniDict *dict);

// ¿Contiene la clave? (una función por tipo de clave)
int8_t wini_dict_contains_i64(WiniDict *dict, int64_t clave);
int8_t wini_dict_contains_f64(WiniDict *dict, double clave);
int8_t wini_dict_contains_i1(WiniDict *dict, int8_t clave);
int8_t wini_dict_contains_str(WiniDict *dict, WiniString *clave);

// Insertar o actualizar (una función por combinación clave/valor)
void wini_dict_set_i64_i64(WiniDict *dict, int64_t clave, int64_t valor);
void wini_dict_set_i64_f64(WiniDict *dict, int64_t clave, double valor);
void wini_dict_set_i64_i1(WiniDict *dict, int64_t clave, int8_t valor);
void wini_dict_set_i64_str(WiniDict *dict, int64_t clave, WiniString *valor);
void wini_dict_set_f64_i64(WiniDict *dict, double clave, int64_t valor);
void wini_dict_set_f64_f64(WiniDict *dict, double clave, double valor);
void wini_dict_set_f64_i1(WiniDict *dict, double clave, int8_t valor);
void wini_dict_set_f64_str(WiniDict *dict, double clave, WiniString *valor);
void wini_dict_set_i1_i64(WiniDict *dict, int8_t clave, int64_t valor);
void wini_dict_set_i1_f64(WiniDict *dict, int8_t clave, double valor);
void wini_dict_set_i1_i1(WiniDict *dict, int8_t clave, int8_t valor);
void wini_dict_set_i1_str(WiniDict *dict, int8_t clave, WiniString *valor);
void wini_dict_set_str_i64(WiniDict *dict, WiniString *clave, int64_t valor);
void wini_dict_set_str_f64(WiniDict *dict, WiniString *clave, double valor);
void wini_dict_set_str_i1(WiniDict *dict, WiniString *clave, int8_t valor);
void wini_dict_set_str_str(WiniDict *dict, WiniString *clave, WiniString *valor);

// Obtener valor por clave; termina el programa con KeyError si no existe.
// Nota de ownership para set/update: cuando una clave ya existe, la
// referencia de la clave entrante pasa a propiedad del diccionario y se
// libera en el camino de reasignación ("valor viejo" y "clave nueva" no
// pueden coexistir y la clave nueva ya no es del caller). Si el caller
// quiere seguir usando esa clave después del set, debe retenerla antes de
// pasarla al runtime. Los literales/fresh values recién construidos ya
// vienen con su propia referencia y no requieren retenida adicional.
//
// Nota de ownership para GET, específica de las 4 variantes con clave
// tipo cadena (wini_dict_get_str_*, más abajo): a diferencia de las
// demás variantes (clave entero/decimal/booleano, que son tipos por
// valor sin ownership que transferir), wini_dict_get_str_* SIEMPRE
// consume exactamente una referencia de 'clave' — la libera
// internamente antes de retornar, tanto si la clave se encontró como si
// no (KeyError). El caller NUNCA debe llamar wini_liberar_cadena sobre
// la clave que pasó a wini_dict_get_str_*; si quiere seguir usando esa
// clave después de la llamada (viene de una variable, no de un literal
// recién construido), debe retenerla ANTES de pasarla, igual que con
// 'set'. (Antes, estas funciones no tocaban 'clave' en absoluto y el
// caller era responsable de liberarla — pero esa convención hacía que
// una clave recién construida se perdiera para siempre si la búsqueda
// lanzaba KeyError, porque 'lanzar' hace longjmp y el caller nunca
// llegaba a ejecutar su propio liberar posterior a la llamada; ver el
// historial de este archivo.)
int64_t     wini_dict_get_i64_i64(WiniDict *dict, int64_t clave);
double      wini_dict_get_i64_f64(WiniDict *dict, int64_t clave);
int8_t      wini_dict_get_i64_i1(WiniDict *dict, int64_t clave);
WiniString* wini_dict_get_i64_str(WiniDict *dict, int64_t clave);
int64_t     wini_dict_get_f64_i64(WiniDict *dict, double clave);
double      wini_dict_get_f64_f64(WiniDict *dict, double clave);
int8_t      wini_dict_get_f64_i1(WiniDict *dict, double clave);
WiniString* wini_dict_get_f64_str(WiniDict *dict, double clave);
int64_t     wini_dict_get_i1_i64(WiniDict *dict, int8_t clave);
double      wini_dict_get_i1_f64(WiniDict *dict, int8_t clave);
int8_t      wini_dict_get_i1_i1(WiniDict *dict, int8_t clave);
WiniString* wini_dict_get_i1_str(WiniDict *dict, int8_t clave);
int64_t     wini_dict_get_str_i64(WiniDict *dict, WiniString *clave); // consume 'clave', ver nota arriba
double      wini_dict_get_str_f64(WiniDict *dict, WiniString *clave); // consume 'clave', ver nota arriba
int8_t      wini_dict_get_str_i1(WiniDict *dict, WiniString *clave);  // consume 'clave', ver nota arriba
WiniString* wini_dict_get_str_str(WiniDict *dict, WiniString *clave); // consume 'clave', ver nota arriba

// Claves / valores como WiniList del tipo correspondiente
WiniList* wini_dict_keys_i64(WiniDict *dict);
WiniList* wini_dict_keys_f64(WiniDict *dict);
WiniList* wini_dict_keys_i1(WiniDict *dict);
WiniList* wini_dict_keys_str(WiniDict *dict);
WiniList* wini_dict_values_i64(WiniDict *dict);
WiniList* wini_dict_values_f64(WiniDict *dict);
WiniList* wini_dict_values_i1(WiniDict *dict);
WiniList* wini_dict_values_str(WiniDict *dict);

//range
// Crea una lista con los números desde 'start' hasta 'end-1', con paso 'step'.
// Si 'step' es 0, lanza una ValueError capturable a través de wini_lanzar.
WiniList* wini_range_i64(int64_t start, int64_t end, int64_t step);

// ---- argumentos de línea de comandos ----
// Convierte el argv de C (char**) recibido por main() en una WiniList de
// cadenas Wini. Usada por el wrapper 'main -> principal' que genera el
// codegen cuando 'principal' declara los parámetros opcionales
// (argc:entero, argv:lista<cadena>).
WiniList* wini_args_to_list(int64_t argc, char **argv);
// ---- comparación de cadenas ----
int8_t wini_string_eq(WiniString *a, WiniString *b);
int8_t wini_string_ne(WiniString *a, WiniString *b);
int8_t wini_string_lt(WiniString *a, WiniString *b);
int8_t wini_string_le(WiniString *a, WiniString *b);
int8_t wini_string_gt(WiniString *a, WiniString *b);
int8_t wini_string_ge(WiniString *a, WiniString *b);

// ---- gestión de memoria (liberar) ----
// Códigos de tipo de elemento, consistentes con wini_print_list/wini_print_dict:
// 0=i64, 1=f64, 2=i1 (booleano), 3=str (cadena)

// Libera UNA referencia a una WiniString (decrementa 'rc'). Solo cuando
// el contador llega a 0 se libera de verdad el buffer interno y la
// estructura. Tolera puntero nulo. Llamar a esto más veces de las que se
// "retuvo" el valor sigue siendo incorrecto (subcuenta el rc), pero ya no
// provoca un doble-free mientras exista al menos otra referencia viva.
void wini_liberar_cadena(WiniString *s);

// Libera UNA referencia a una WiniList (decrementa 'rc'). Solo al llegar
// a 0 se libera una referencia de cada WiniString* contenida (si
// tipo == 3), el arreglo de datos y la estructura. Tolera puntero nulo.
void wini_liberar_lista(WiniList *lista, int tipo);

// Libera UNA referencia a un WiniDict (decrementa 'rc'). Solo al llegar a
// 0 se libera una referencia de cada WiniString* de claves/valores (si
// tipoClave/tipoValor == 3), los arreglos y la estructura. Tolera
// puntero nulo.
void wini_liberar_diccionario(WiniDict *dict, int tipoClave, int tipoValor);

// ---- excepciones (intentar / capturar / finalmente / lanzar) ----
//
// Se implementan con setjmp/longjmp: cada 'intentar' que empieza a
// ejecutarse apila un "frame" (wini_try_push) y llama a setjmp
// DIRECTAMENTE en el código generado (nunca dentro de una función de
// este runtime: setjmp solo es válido en el stack frame que lo invoca).
// 'wini_try_push' devuelve un puntero opaco al área de memoria donde
// debe guardarse ese jmp_buf; su tamaño y layout quedan ocultos del
// codegen. 'lanzar' arma la excepción y salta (longjmp) al frame más
// cercano; si no hay ninguno activo, el programa termina imprimiendo el
// error. No hay jerarquía de tipos: 'capturar Tipo' compara el nombre
// tal cual contra el usado en 'lanzar Tipo(...)'.

// Apila un nuevo frame de manejo de excepciones y devuelve el puntero
// donde el código generado debe hacer 'setjmp'. Debe emparejarse con
// exactamente un wini_try_pop() (al terminar de usarlo sin excepción) o
// dejar que una excepción lo consuma (wini_relanzar ya lo desapila).
void *wini_try_push(void);

// Desapila el frame más reciente SIN saltar a él: se usa cuando el
// bloque 'intentar' terminó de ejecutarse (con o sin haber sido
// capturada una excepción más adentro) y ese frame ya no es un destino
// válido para ningún 'lanzar' futuro.
void wini_try_pop(void);

// Libera el frame que 'wini_relanzar' acaba de usar para saltar hasta
// este punto (nunca antes/después del salto en sí, por eso lo maneja el
// runtime y no el codegen). Debe llamarse exactamente una vez, justo al
// entrar al despacho de 'capturar' de un 'intentar', inmediatamente
// después de que su propio 'setjmp' haya devuelto no-cero.
void wini_try_pop_saltado(void);

// Arma la excepción actual (tipo + mensaje + línea) y salta al frame más
// cercano (equivalente a llamar wini_relanzar luego de fijar los datos).
// 'tipo' es un literal C (no un WiniString ni cuenta como referencia);
// 'mensaje' SÍ es un WiniString y esta función toma posesión de esa
// referencia (el codegen no debe liberarla ni retenerla después de
// llamar a 'lanzar': si el valor venía de una variable compartida, debe
// retenerse ANTES de pasarlo, igual que con cualquier otra función del
// runtime que absorbe una referencia). No retorna nunca.
void wini_lanzar(const char *tipo, WiniString *mensaje, int64_t linea);

// Vuelve a lanzar la excepción actualmente en vuelo (para cuando ningún
// 'capturar' hizo match): salta al SIGUIENTE frame más cercano, o
// termina el programa si no queda ninguno. Solo tiene sentido llamarla
// justo después de que el 'setjmp' de un 'intentar' devolvió no-cero.
void wini_relanzar(void);

// Nombre de tipo de la excepción actualmente en vuelo, para comparar con
// el de cada 'capturar' (ver wini_excepcion_es_tipo). Solo válido justo
// después de que el 'setjmp' de un 'intentar' devolvió no-cero.
int8_t wini_excepcion_es_tipo(const char *tipo);

// Mensaje de la excepción actualmente en vuelo, transfiriendo su referencia
// a quien llama. El runtime conserva una referencia auxiliar hasta que el
// catch termina normalmente o llama a 'relanzar', para poder preservar el
// mensaje original en ambos caminos.
WiniString *wini_excepcion_tomar_mensaje(void);

// Descarta la excepción actualmente en vuelo sin quedarse con su
// mensaje (libera esa referencia). Se usa al entrar a un
// 'capturar Tipo:' sin 'como var'.
void wini_excepcion_descartar(void);

// ---- API de inspección para puentes externos (wini_bridge, FFI) ----
//
// Copia los datos de la excepción actualmente en vuelo a los buffers del
// caller. No transfiere ownership de nada: el mensaje se COPIA con malloc
// (el caller debe hacer free()). 'tipo_out' y 'mensaje_out' pueden ser
// NULL si no interesan. La longitud de 'tipo_out' se pasa en 'tipo_cap';
// el tipo se trunca si no cabe (con NUL final garantizado).
//
// Pensada para ser llamada desde código C que NO forma parte del runtime
// (p.ej. wini_bridge.c) y que necesita leer el estado de la excepción sin
// depender de las variables internas, que son privadas del runtime.
void wini_excepcion_actual(char *tipo_out, size_t tipo_cap,
                           char **mensaje_out, int64_t *linea_out);

#endif
