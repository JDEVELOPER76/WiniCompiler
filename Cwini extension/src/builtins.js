'use strict';

// ---------------------------------------------------------------------------
// Documentación de las funciones integradas (builtins) del compilador Cwin.
// Fuente de verdad: parser.go, verificador.go y codegen.go del compilador
// (los nombres especiales manejados en emitirLlamada / verificadorLlamada).
// ---------------------------------------------------------------------------

const DOCS_BUILTINS = {
  escribir: {
    categoria: 'sentencia integrada',
    firma: 'escribir(valor1, valor2, ...)',
    retorno: '',
    resumen:
      'Escribe uno o varios valores en la salida estándar. Acepta cualquier cantidad de argumentos y de cualquier tipo: entero, decimal, booleano, cadena, listas y diccionarios.',
    params: [
      { nombre: 'valores...', tipo: 'cualquiera', desc: 'Uno o más valores a mostrar, en orden.' },
    ],
    ejemplo: 'escribir("Hola, mundo")\nescribir("suma: ", 2 + 3)\nescribir(mi_lista)',
  },

  leer: {
    categoria: 'integrada',
    firma: 'leer(prompt: cadena)',
    retorno: 'cadena',
    resumen:
      'Muestra un mensaje en pantalla y lee una línea escrita por la persona usuaria. Siempre devuelve una cadena; usa las conversiones (entero, decimal, ...) para transformarla.',
    params: [
      { nombre: 'prompt', tipo: 'cadena', desc: 'Mensaje que se muestra antes de leer.' },
    ],
    ejemplo: 'nombre: cadena = leer("¿Cómo te llamas? ")\nescribir("Hola, ", nombre)',
  },

  tipo: {
    categoria: 'integrada',
    firma: 'tipo(valor)',
    retorno: 'cadena',
    resumen:
      "Devuelve el nombre del tipo de dato de un valor. Como Cwin es de tipado estático, el nombre se conoce en tiempo de compilación: 'entero', 'decimal', 'booleano', 'cadena', 'lista<entero>', 'diccionario<cadena, entero>', ...",
    params: [
      { nombre: 'valor', tipo: 'cualquiera', desc: 'Valor del que se quiere saber el tipo.' },
    ],
    ejemplo: 'escribir(tipo(42))    // entero\nescribir(tipo(3.14))  // decimal',
  },

  claves: {
    categoria: 'integrada',
    firma: 'claves(d: diccionario<K, V>)',
    retorno: 'lista<K>',
    resumen:
      'Devuelve la lista de claves de un diccionario, con el tipo de las claves como tipo de elemento.',
    params: [
      { nombre: 'd', tipo: 'diccionario<K, V>', desc: 'Diccionario del que extraer las claves.' },
    ],
    ejemplo: 'para k en claves(mis_datos):\n    escribir(k)',
  },

  valores: {
    categoria: 'integrada',
    firma: 'valores(d: diccionario<K, V>)',
    retorno: 'lista<V>',
    resumen:
      'Devuelve la lista de valores de un diccionario, con el tipo de los valores como tipo de elemento.',
    params: [
      { nombre: 'd', tipo: 'diccionario<K, V>', desc: 'Diccionario del que extraer los valores.' },
    ],
    ejemplo: 'para v en valores(mis_datos):\n    escribir(v)',
  },

  rango: {
    categoria: 'integrada',
    firma: 'rango([inicio: entero, ]fin: entero[, paso: entero])',
    retorno: 'lista<entero>',
    resumen:
      'Genera una lista de enteros. Con 1 argumento va de 0 a fin-1; con 2, de inicio a fin-1; con 3, avanza de paso en paso. Acepta 1, 2 o 3 argumentos enteros, nada más ni nada menos.',
    params: [
      { nombre: 'inicio', tipo: 'entero', desc: 'Primer valor (opcional, por defecto 0).' },
      { nombre: 'fin', tipo: 'entero', desc: 'Límite excluido.' },
      { nombre: 'paso', tipo: 'entero', desc: 'Salto entre valores (opcional, por defecto 1).' },
    ],
    ejemplo: 'para i en rango(0, 10):\n    escribir(i)\n# rango(5) == [0, 1, 2, 3, 4]',
  },

  liberar: {
    categoria: 'sentencia integrada',
    firma: 'liberar(valor)',
    retorno: '',
    resumen:
      'Libera la memoria de una cadena, lista o diccionario. Si el argumento es una variable, su puntero queda anulado: volver a usarla da nulo ("puntero nulo") en vez de tocar memoria ya liberada.',
    params: [
      { nombre: 'valor', tipo: 'cadena | lista | diccionario', desc: 'Valor a liberar.' },
    ],
    ejemplo: 'liberar(datos_grandes)\nescribir(datos_grandes)  // imprime [] o <nulo>, sin segfault',
  },
};

// Conversiones: nombre canónico + alias en inglés que acepta el parser
// (todo se resuelve al mismo nombre canónico en el compilador).
const CONVERSIONES = [
  { nombre: 'entero', alias: ['int'], firma: 'entero(valor)', retorno: 'entero', resumen: 'Convierte un valor al tipo entero (trunca los decimales). Alias en inglés: int(valor).', ejemplo: 'n: entero = entero("42")' },
  { nombre: 'decimal', alias: ['float'], firma: 'decimal(valor)', retorno: 'decimal', resumen: 'Convierte un valor al tipo decimal (coma flotante de doble precisión). Alias en inglés: float(valor).', ejemplo: 'x: decimal = decimal("3.14")' },
  { nombre: 'cadena', alias: ['str'], firma: 'cadena(valor)', retorno: 'cadena', resumen: 'Convierte un valor (número, booleano, etc.) a su representación como cadena de texto. Alias en inglés: str(valor).', ejemplo: 'mensaje: cadena = cadena(2 + 3)  // "5"' },
  { nombre: 'booleano', alias: ['bool'], firma: 'booleano(valor)', retorno: 'booleano', resumen: 'Convierte un valor a booleano (verdadero / falso). Alias en inglés: bool(valor).', ejemplo: 'b: booleano = booleano(1)' },
  { nombre: 'lista', alias: ['list'], firma: 'lista(valor)', retorno: 'lista', resumen: 'Conversión al tipo lista. Alias en inglés: list(valor).', ejemplo: 'l: lista<entero> = [1, 2, 3]' },
  { nombre: 'diccionario', alias: ['dict'], firma: 'diccionario(valor)', retorno: 'diccionario', resumen: 'Conversión al tipo diccionario. Alias en inglés: dict(valor).', ejemplo: 'd: diccionario<cadena, entero> = diccionario(vacio)' },
];

// Documentación breve de palabras clave (hover sobre la palabra).
const DOCS_KEYWORDS = {
  funcion: 'Define una función con nombre, sus parámetros tipados (`nombre: tipo`, con valores por defecto opcionales y un único variádico `*nombre: tipo`) y su tipo de retorno opcional. El cuerpo va indentado en el bloque que abre la firma.',
  externa: 'Declara una función nativa (C/LLVM) que vive en una biblioteca .a/.o de la misma carpeta: no tiene cuerpo en Cwin, solo su firma. Puede ser variádica a la C con `...`.',
  estructura: 'Palabra reservada nueva: agrupa campos con nombre y tipo en una estructura de datos.',
  vacio: 'Palabra reservada nueva: tipo especial que indica que una función no devuelve ningún valor.',
  si: 'Ejecuta su bloque (indentado tras los dos puntos) solo si la condición es verdadera.',
  sino: 'Alternativa del `si`: su bloque se ejecuta cuando la condición es falsa.',
  demas: 'Cadena final de alternativas del `si` (equivalente a else if / elif).',
  mientras: 'Repite su bloque mientras la condición sea verdadera.',
  para: 'Recorre los elementos de un iterable (por ejemplo una lista devuelta por `rango`) asignándolos a una variable, uno por vuelta, con `en`.',
  en: 'Operador lógico de pertenencia/recorrido: `para x en iterable`.',
  romper: 'Abandona el bucle más interno de inmediato.',
  continuar: 'Salta a la siguiente vuelta del bucle más interno.',
  retornar: 'Devuelve un valor de la función y termina su ejecución.',
  intentar: 'Inicia un bloque protegido frente a errores en tiempo de ejecución.',
  capturar: 'Captura el error lanzado dentro del `intentar` correspondiente.',
  finalmente: 'Bloque que se ejecuta siempre, haya o no error, tras `intentar`/`capturar`.',
  lanzar: 'Lanza un error en tiempo de ejecución con el valor indicado.',
  relanzar: 'Relanza el error capturado para que lo maneje un `intentar` más afuera.',
  como: 'Alias en `importar x como y` y en otras formas con nombre.',
  importar: "Importa un módulo (biblioteca de funciones) y trae sus funciones al espacio de nombres plano del programa. Formas: `importar nombre`, `importar a.b.c` (cada punto es una subcarpeta), `importar \"ruta\"` y `importar x como y`.",
  tipo: "Sentencia/función integrada: `tipo(x)` devuelve el nombre del tipo de `x` como cadena.",
  escribir: 'Sentencia integrada: escribe valores en la salida estándar.',
  leer: 'Integrada: muestra un prompt y lee una línea del teclado; devuelve cadena.',
  liberar: 'Integrada: libera la memoria de cadenas, listas y diccionarios.',
  y: 'Operador lógico Y (conjunción).',
  o: 'Operador lógico O (disyunción).',
  no: 'Operador lógico de negación.',
  verdadero: 'Constante booleana verdadera.',
  falso: 'Constante booleana falsa.',
  nulo: 'Constante de ausencia de valor.',
};

// Lista plana de palabras clave para autocompletado.
const PALABRAS = [
  'si', 'sino', 'demas', 'mientras', 'para', 'en', 'romper', 'continuar',
  'funcion', 'retornar', 'externa', 'estructura', 'vacio', 'tipo',
  'importar', 'como', 'intentar', 'capturar', 'finalmente', 'lanzar', 'relanzar',
  'escribir', 'leer', 'liberar', 'y', 'o', 'no',
  'verdadero', 'falso', 'nulo',
];

// Tipos de dato (español + alias inglés) para autocompletado.
const TIPOS_DATO = [
  'entero', 'decimal', 'cadena', 'booleano', 'lista', 'diccionario',
  'int', 'float', 'str', 'bool', 'list', 'dict', 'vacio',
];

module.exports = { DOCS_BUILTINS, CONVERSIONES, DOCS_KEYWORDS, PALABRAS, TIPOS_DATO };
