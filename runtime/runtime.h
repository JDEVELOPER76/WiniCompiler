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
//
// LÍMITE CONOCIDO (ciclos): un conteo de referencias nunca libera un
// ciclo (un valor que se alcanza a sí mismo siguiendo referencias). El
// runtime rechaza la auto-referencia directa ('l[i] = l' en una lista
// genérica) con un ValueError capturable; los ciclos indirectos
// (a[0] = b; b[0] = a) o vía estructuras quedan a cargo del programa:
// hay que romper el ciclo a mano antes de liberar, o esa memoria se fuga.
// Ver wini_memoria_reporte() para detectarlos en pruebas.
//
// LÍMITE CONOCIDO (excepciones): las variables locales no tienen limpieza
// automática en el camino normal (siguen requiriendo 'liberar' explícito),
// pero el desenrollado de una excepción SÍ las libera: cada local heap
// viva está registrada como "raíz" y 'lanzar'/'relanzar' devuelven esas
// referencias antes del salto, así que una excepción nunca fuga locales
// (ver wini_raiz_agregar). Para código que debe ser estricto con la
// memoria en el camino SIN excepciones, liberar antes de lanzar o usar
// 'finalmente' para el liberar sigue siendo necesario.
WiniString *wini_string_retener(WiniString *s);

// Concatenación de cadenas. Solo LEE los operandos (no consume ninguna
// de sus referencias) y SIEMPRE devuelve una referencia nueva de la que
// el caller es dueño exclusivo (rc propio, igual que wini_string_new):
// incluso cuando un operando es NULL —camino que antes devolvía el
// mismo puntero del otro sin retener, y que producía doble free /
// use-after-free cuando el codegen liberaba el resultado además del
// operando original— el valor devuelto es una retención adicional del
// operando no nulo, nunca un alias sin dueño propio.
WiniString* wini_string_concat(WiniString* a, WiniString* b);
// Conversiones a cadena
WiniString* wini_i64_to_string(int64_t v);
WiniString* wini_f64_to_string(double v);
WiniString* wini_bool_to_string(int8_t v);
int64_t wini_string_to_i64(WiniString *s, int64_t linea);
double wini_string_to_f64(WiniString *s, int64_t linea);
void wini_print_i64(int64_t value);
void wini_print_f64(double value);
void wini_print_bool(_Bool value);
void wini_print_string(WiniString *value);
// Imprime una lista con salto de línea final. Para listas genéricas (que
// llevan su propio descriptor) 'tipo' se ignora; ver WINI_TIPO_GENERICO.
// Las listas de estructuras no tienen impresión: se muestran como <?>.
void wini_print_list(void* list, int tipo);
void wini_print_dict(void* dict, int tipoClave, int tipoValor);
WiniString* wini_read_string(WiniString* prompt);

// ---- verificaciones aritméticas ----
// Verifica que el divisor de una división/módulo ENTERO no sea cero.
// Si lo es, lanza un ErrorMatematico capturable ("división por cero")
// a través de wini_lanzar (con la línea del fuente que pasa el codegen).
// Si no lo es, no hace nada. Sin esta verificación el programa moriría
// con SIGFPE, que no se puede capturar desde Wini ('intentar/capturar').
// Solo se necesita para enteros: la división de decimales IEEE-754
// produce infinito/NaN, no una señal.
void wini_check_divisor(int64_t dividendo, int64_t divisor, int64_t linea);

// Verifica el divisor de una división o módulo DECIMAL. IEEE-754 no rompe el
// proceso ante un divisor cero (el resultado sería infinito), pero el
// lenguaje lo trata como error igual que en el entero, para que
// 'a / b' se comporte igual sin importar el tipo declarado de los operandos.
void wini_check_divisor_decimal(double divisor, int64_t linea);

// ---- listas ----
//
// Hay dos "sabores" de lista, que comparten el mismo struct WiniList:
//
//  1) Listas PRIMITIVAS (desc == NULL): elementos entero/decimal/booleano/
//     cadena. El tipo de elemento NO se guarda en la lista: lo pasa el
//     código que llama (el codegen, que lo conoce en compilación) como el
//     parámetro 'tipo' de wini_liberar_lista / wini_print_list /
//     wini_list_concat (0=i64, 1=f64, 2=i1, 3=str).
//
//  2) Listas GENÉRICAS (desc != NULL): elementos que son otra lista
//     ('lista<lista<T>>') o una estructura ('lista<Punto>'). Se
//     describen a sí mismas con un WiniElemDesc, así que liberar/imprimir
//     funcionan sin importar el 'tipo' que se pase (por convención
//     WINI_TIPO_GENERICO). Esto permite anidar a cualquier profundidad
//     (lista<lista<lista<entero>>>, lista<lista<Punto>>, ...) sin
//     multiplicar códigos de tipo.

// Cómo interpretar el slot de un elemento de una lista genérica.
enum {
    WINI_ELEM_LISTA  = 1, // el slot es un WiniList* (retener/liberar los hace el propio runtime)
    WINI_ELEM_OPACO  = 2  // el slot es un valor de estructura (retener/liberar vía thunks del codegen)
};

// Valor de 'tipo' que se pasa a wini_liberar_lista / wini_print_list /
// wini_list_concat cuando la lista es genérica (se ignora: manda 'desc').
#define WINI_TIPO_GENERICO 4

// Descriptor de elemento de una lista genérica. Lo emite el codegen como
// constante global (una por tipo de elemento distinto): su layout tiene
// que coincidir EXACTAMENTE con el que arma codegen.go (descriptorElemento).
typedef struct WiniElemDesc {
    int64_t size;  // bytes que ocupa un elemento dentro del arreglo (sizeof del tipo LLVM)
    int32_t kind;  // WINI_ELEM_LISTA o WINI_ELEM_OPACO
    int32_t sub;   // WINI_ELEM_LISTA: código de tipo de los elementos de la lista interna (o WINI_TIPO_GENERICO)
    void (*retener)(void *slot); // WINI_ELEM_OPACO: retiene las referencias heap que contiene el valor; NULL si no tiene
    void (*liberar)(void *slot); // WINI_ELEM_OPACO: libera esas referencias; NULL si no tiene
} WiniElemDesc;

// Representación opaca de una lista: { len, data*, rc, desc* }
// (los campos nuevos van SIEMPRE al final: wini_bridge.c y cualquier
// código C externo que lea len/data/rc sigue siendo válido).
typedef struct {
    int64_t len;
    void *data;   // puntero al array de elementos
    int64_t rc;   // conteo de referencias (ver nota junto a WiniString)
    const WiniElemDesc *desc; // NULL = lista primitiva; != NULL = lista genérica
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

// Insertar un elemento en la posición 'idx' (0..len; idx == len agrega al
// final). Muta EN EL LUGAR y devuelve el MISMO puntero (valen tanto
// 'insertar(l, i, x)' como 'l = insertar(l, i, x)').
// El valor se ADOPTA (el codegen ya lo retuvo si venía de otra ubicación,
// igual que wini_list_set_str). Fuera de rango: IndexError capturable.
// Con lista NULL crea una lista nueva (solo sirve reasignando).
WiniList *wini_list_insert_i64(WiniList *list, int64_t idx, int64_t valor, int64_t linea);
WiniList *wini_list_insert_f64(WiniList *list, int64_t idx, double valor, int64_t linea);
WiniList *wini_list_insert_i1(WiniList *list, int64_t idx, int8_t valor, int64_t linea);
WiniList *wini_list_insert_str(WiniList *list, int64_t idx, WiniString *valor, int64_t linea);

// Quitar el elemento en la posición 'idx' (0..len-1). Muta EN EL LUGAR y
// devuelve el MISMO puntero; el elemento que sale se libera (la lista
// pierde una referencia). Fuera de rango: IndexError capturable. 'tipo'
// usa los mismos códigos que wini_list_concat.
WiniList *wini_list_remove(WiniList *list, int64_t idx, int tipo, int64_t linea);

// ---- listas genéricas (elementos lista o estructura) ----
// Contrato de propiedad (el mismo que wini_list_new_str / wini_list_set_str):
// las funciones que ALMACENAN un elemento (new_gen, set_gen) ADOPTAN las
// referencias heap que ese elemento contiene: el codegen ya las retuvo
// antes de la llamada si el valor venía de otra ubicación. Las que LEEN
// (slot) no transfieren nada: el elemento sigue siendo de la lista.

// Crea una lista genérica copiando 'len' elementos de 'data' (cada uno de
// 'desc->size' bytes). Con len == 0, 'data' puede ser NULL.
WiniList *wini_list_new_gen(const WiniElemDesc *desc, const void *data, int64_t len);

// Puntero al slot del elemento 'idx' (con chequeo de rango: lanza
// IndexError capturable, igual que wini_list_get_*). El codegen hace un
// load tipado desde ese puntero. Vale solo hasta la próxima modificación
// de la lista.
void *wini_list_slot(WiniList *list, int64_t idx, int64_t linea);

// Dirección de la ranura del elemento 'idx' en una lista<cadena>. La usa el
// builtin 'liberar(l[i])' para dejarle NULL después de soltar la
// referencia: si no, un 'liberar(l)' posterior liberaría dos veces la misma
// memoria (doble free). No libera el valor anterior ni lo consume.
WiniString **wini_list_slot_str(WiniList *list, int64_t idx, int64_t linea);

// Reemplaza el elemento 'idx' por el valor apuntado por 'valor'
// (desc->size bytes). Libera las referencias del elemento que se pisa y
// adopta las del nuevo. Fuera de rango: libera las del nuevo (como
// wini_list_set_str) y lanza IndexError. 'desc' viaja aparte porque la
// lista puede ser NULL.
void wini_list_set_gen(WiniList *list, int64_t idx, const void *valor, const WiniElemDesc *desc, int64_t linea);

// Concatena dos listas genéricas. Como 'desc' viene aparte, funciona
// aunque alguno (o ambos) sea NULL. Devuelve una lista NUEVA e
// independiente: cada elemento copiado se retiene, así que la lista
// resultante y las originales pueden liberarse en cualquier orden.
WiniList *wini_list_concat_gen(WiniList *a, WiniList *b, const WiniElemDesc *desc);

// Inserta el valor apuntado por 'valor' (desc->size bytes) en la posición
// 'idx' (0..len). Muta EN EL LUGAR y devuelve el MISMO puntero; adopta el
// valor nuevo. Fuera de rango: IndexError.
WiniList *wini_list_insert_gen(WiniList *list, int64_t idx, const void *valor,
                              const WiniElemDesc *desc, int64_t linea);

// Quita el elemento 'idx' (0..len-1). Muta EN EL LUGAR y devuelve el MISMO
// puntero; libera las referencias del elemento que sale. Fuera de rango:
// IndexError.
WiniList *wini_list_remove_gen(WiniList *list, int64_t idx, const WiniElemDesc *desc, int64_t linea);

// ---- diccionarios ----
// Representación opaca y genérica: { len, cap, claves*, valores* }.
// Los tipos concretos de 'claves' y 'valores' (int64_t, double, int8_t o
// WiniString*) son conocidos en tiempo de compilación por el codegen de
// Wini, que llama siempre a la variante tipada correspondiente (sufijos
// i64/f64/i1/str tanto para la clave como para el valor).
//
// Valores GENÉRICOS: el valor puede ser una lista o una estructura
// ('diccionario<cadena, lista<entero>>'). Esos diccionarios usan las
// variantes wini_dict_*_gen y se describen con un WiniElemDesc (el mismo
// de las listas genéricas): viaja en cada llamada (el codegen lo conoce
// por el tipo estático) y además queda guardado en 'descVal' para que
// liberar/imprimir funcionen sin parámetros extra. Las CLAVES siempre
// son primitivas (una lista no es comparable ni hashable).
typedef struct {
    int64_t len;
    int64_t cap;
    void *claves;
    void *valores;
    int64_t rc; // conteo de referencias (ver nota junto a WiniString)
    // Descriptor del tipo de VALOR cuando es genérico (lista o
    // estructura); NULL = valores primitivos. Va al final para no mover
    // los campos que ya leen wini_bridge.c y el código C externo.
    const WiniElemDesc *descVal;
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

// Devuelve la DIRECCIÓN de la ranura que guarda el valor de 'clave' (para
// valores de tipo cadena), o NULL si la clave no está o el diccionario es
// NULL. La usa el builtin 'liberar(d[clave])' para dejarle NULL a la ranura
// después de soltar la referencia: si no, un 'liberar(d)' posterior
// liberaría dos veces la misma memoria (doble free). NO libera el valor
// anterior ni consume la clave: solo da la dirección.
WiniString** wini_dict_slot_str(WiniDict *dict, WiniString *clave);

// ---- diccionarios con valores genéricos (lista o estructura) ----
//
// Mismo contrato de propiedad que las variantes primitivas: 'set'
// adopta el valor (el codegen ya retuvo si venía de otra ubicación) y la
// clave sigue las reglas de WINI_DICT_SET (se adopta siempre; en
// reasignación se libera la entrante). 'slot' no transfiere nada: el
// valor sigue siendo del diccionario (lectura prestada, como
// wini_list_slot); para claves cadena consume UNA referencia, igual que
// wini_dict_get_str_* (retener antes si la clave sigue usándose).
//
// 'desc' es el descriptor del tipo de valor (lo emite el codegen) y se
// guarda además en el diccionario (descVal) para liberar/imprimir.
void wini_dict_set_i64_gen(WiniDict *d, int64_t clave, const void *valor, const WiniElemDesc *desc);
void wini_dict_set_f64_gen(WiniDict *d, double clave, const void *valor, const WiniElemDesc *desc);
void wini_dict_set_i1_gen(WiniDict *d, int8_t clave, const void *valor, const WiniElemDesc *desc);
void wini_dict_set_str_gen(WiniDict *d, WiniString *clave, const void *valor, const WiniElemDesc *desc);

// Puntero al slot del valor de 'clave' (con chequeo de rango: KeyError
// capturable, igual que wini_dict_get_*). El codegen carga el valor
// tipado desde ahí. Vale solo hasta la próxima modificación del dict.
void *wini_dict_slot_i64_gen(WiniDict *d, int64_t clave, const WiniElemDesc *desc, int64_t linea);
void *wini_dict_slot_f64_gen(WiniDict *d, double clave, const WiniElemDesc *desc, int64_t linea);
void *wini_dict_slot_i1_gen(WiniDict *d, int8_t clave, const WiniElemDesc *desc, int64_t linea);
void *wini_dict_slot_str_gen(WiniDict *d, WiniString *clave, const WiniElemDesc *desc, int64_t linea);

// Valores como lista genérica (nueva e independiente: cada elemento se
// retiene, como wini_list_concat_gen). Funciona aunque el dict sea NULL.
WiniList *wini_dict_values_gen(WiniDict *d, const WiniElemDesc *desc);

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
// tipo == 3), el arreglo de datos y la estructura. Si la lista es
// genérica (desc != NULL) se libera además cada elemento según su
// descriptor (listas internas / valores de estructura) y 'tipo' se
// ignora. Tolera puntero nulo.
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
// donde el código generado debe hacer 'setjmp'. 'fid' es la identidad de
// la invocación dueña (ver wini_invocacion_nueva). Debe emparejarse con
// exactamente un wini_try_pop() (al terminar de usarlo sin excepción) o
// dejar que una excepción lo consuma (wini_relanzar ya lo desapila).
void *wini_try_push(int64_t fid);

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

// ---- raíces de excepción (limpieza automática al desenrollar) ----
//
// Un longjmp no corre destructores: si una excepción salta por encima del
// 'liberar' de una variable local viva, esa referencia se fugaba. Para
// evitarlo, el codegen registra cada variable local heap (cadenas,
// listas, diccionarios y campos heap de estructuras) como una RAÍZ: la
// dirección de su ranura más cómo liberarla. 'wini_lanzar' y
// 'wini_relanzar', ANTES de saltar (con todos los frames todavía vivos),
// liberan las raíces apiladas por encima del frame destino e ignoran las
// ranuras en NULL (ya liberadas a mano con 'liberar').
//
// Solo cubre el camino de EXCEPCIÓN: en el camino normal las raíces se
// descartan sin liberar (cada función poda las suyas al retornar y el
// 'liberar' explícito sigue siendo obligatorio ahí). Ver runtime.c.
//
// kinds para wini_raiz_agregar: 0=cadena, 1=lista (aux1=tipo de elemento,
// con 4=genérica), 2=diccionario (aux1=tipo clave, aux2=tipo valor).
// Códigos de tipo: los mismos de wini_liberar_lista/diccionario
// (0=i64, 1=f64, 2=i1, 3=str).
void wini_raiz_agregar(void *ranura, int32_t kind, int32_t aux1, int32_t aux2, int64_t fid);

// Identidad de invocación para las raíces (ver WiniRaiz en runtime.c):
// estrictamente creciente con el anidado de llamados (cada función Wini
// pide uno al entrar). Como los llamados anidan, un fid mayor implica
// invocación más nueva: el desenrollado libera lo de fid mayor al
// destino (llamados abandonados) y conserva lo propio y lo exterior.
int64_t wini_invocacion_nueva(void);

// Profundidad actual de la pila de raíces (cantidad de raíces
// registradas). Cada función la captura al entrar y poda hasta ella al
// retornar: así las raíces de una llamada no sobreviven a su retorno
// (sus ranuras apuntarían a un stack frame muerto).
int64_t wini_raices_profundidad(void);

// Descarta (SIN liberar) las raíces por encima de 'prof'. Solo para el
// camino normal (retornos): la limpieza de excepción la hace el propio
// 'lanzar'/'relanzar' antes de saltar.
void wini_raices_podar(int64_t prof);
//
// Con conteo manual, el error más común no es el doble-free (el rc lo
// absorbe) sino la FUGA: retener sin su liberar correspondiente, o un
// ciclo de referencias (ver nota junto a WiniString). Estos contadores
// registran cuántos encabezados de cada tipo siguen vivos: cada
// constructor suma uno, cada liberación real (rc llega a 0) resta uno.
// Retener/liberar parciales no los tocan. Costo despreciable (un
// incremento por objeto creado).
//
// Si al terminar el programa (o un test) algún contador no es 0, falta
// un 'liberar' o hay un ciclo sin romper. wini_memoria_reporte() vuelca
// los tres a stderr en una línea.
int64_t wini_memoria_cadenas_vivas(void);
int64_t wini_memoria_listas_vivas(void);
int64_t wini_memoria_diccionarios_vivos(void);
void wini_memoria_reporte(void);

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
