import * as vscode from 'vscode';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';
import { execFile, execFileSync } from 'child_process';

// ─────────────────────────────────────────────────────────────
// Modelo
// ─────────────────────────────────────────────────────────────

interface ParamDoc {
	name: string;
	type: string;
	/** Valor por defecto en texto (si lo tiene). */
	defaultValue?: string;
	/** true si es `*nombre` (variádico a la Wini). */
	variadic?: boolean;
}

interface WiniFunction {
	name: string;
	params: ParamDoc[];
	returnType: string;
	/** Nombre de archivo para mostrar (p.ej. `stdlib.cwn`). */
	file: string;
	/** Ruta absoluta (para "ir a definición"). */
	rutaAbs?: string;
	line: number;
	description: string;
	origin: 'stdlib' | 'modulo' | 'documento';
	/** Firma con valores por defecto, para el detalle. */
	signature?: string;
}

interface WiniCampo {
	name: string;
	type: string;
	line: number;
}

interface WiniEstructura {
	name: string;
	fields: WiniCampo[];
	file: string;
	rutaAbs?: string;
	line: number;
	description: string;
	origin: 'modulo' | 'documento';
}

interface WiniExterna {
	name: string;
	simboloC: string;
	params: ParamDoc[];
	returnType: string;
	variadicaC: boolean;
	file: string;
	rutaAbs?: string;
	line: number;
	description: string;
	origin: 'modulo' | 'documento';
}

interface WiniModulo {
	/** Nombre "importable" en notación con puntos (p.ej. libstd.stdlib). */
	nombre: string;
	/** Último segmento del nombre (p.ej. stdlib), útil para el filtrado. */
	etiqueta: string;
	/** Ruta absoluta del archivo del módulo. */
	ruta: string;
	/** De dónde salió: del sistema (junto a winic) o del workspace. */
	origen: 'sistema' | 'workspace';
	/** Cuántas funciones declara (para el hover). */
	funciones: number;
	/** Declaraciones del módulo para completar `modulo.funcion`. */
	declaraciones: WiniFunction[];
	estructuras: WiniEstructura[];
	externas: WiniExterna[];
}

// Identificadores válidos en Wini (incluye acentos del español)
const WORD_RE = /[A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*/;

// Extensiones que el compilador reconoce como módulos importables.
const EXTENSIONES_MODULO = ['.wini', '.cwn', '.wn'];

// ─────────────────────────────────────────────────────────────
// Palabras reservadas, tipos, builtins y excepciones
// ─────────────────────────────────────────────────────────────

const KEYWORDS: ReadonlyArray<readonly [string, string]> = [
	['si', 'Ejecuta un bloque si se cumple la condición'],
	['sino', 'Rama alternativa de un si (sino cond: es else-if; sino: solo es else final)'],
	['demas', 'Rama final de un si (equivalente a sino:)'],
	['mientras', 'Bucle que se repite mientras la condición se cumpla'],
	['para', 'Bucle sobre una lista, diccionario o cadena'],
	['en', 'Separa la variable del iterable en un para / operador de pertenencia'],
	['funcion', 'Declara una función'],
	['externa', 'Declara una función externa (FFI con C)'],
	['estructura', 'Declara una estructura'],
	['importar', 'Importa un módulo o biblioteca'],
	['como', 'Alias de importación o variable de captura'],
	['retornar', 'Devuelve un valor desde una función'],
	['romper', 'Sale del bucle actual'],
	['continuar', 'Salta a la siguiente iteración del bucle'],
	['intentar', 'Bloque de manejo de errores'],
	['capturar', 'Captura la excepción de un bloque intentar'],
	['finalmente', 'Bloque que siempre se ejecuta tras intentar/capturar'],
	['lanzar', 'Lanza una excepción'],
	['relanzar', 'Vuelve a lanzar la excepción capturada (solo dentro de capturar)'],
	['liberar', 'Libera una referencia (cadena, lista, diccionario o estructura)'],
	['y', 'Operador lógico Y (AND)'],
	['o', 'Operador lógico O (OR)'],
	['no', 'Negación lógica'],
	['verdadero', 'Valor booleano verdadero'],
	['falso', 'Valor booleano falso'],
	['nulo', 'Valor nulo (válido para cadena, lista o diccionario)']
];

const TIPOS: ReadonlyArray<readonly [string, string]> = [
	['entero', 'Número entero'],
	['decimal', 'Número decimal'],
	['cadena', 'Cadena de texto'],
	['booleano', 'Booleano (verdadero/falso)'],
	['vacio', 'Sin valor; solo válido como retorno de función'],
	['lista', 'Lista tipada: lista<entero>, lista<cadena>, …'],
	['diccionario', 'Diccionario tipado: diccionario<cadena, entero>, …']
];

const EXCEPCIONES: ReadonlyArray<readonly [string, string]> = [
	['ErrorMatematico', 'División o módulo por cero en enteros'],
	['EOFError', 'Fin de entrada inesperado (runtime)'],
	['IndexError', 'Índice de lista/cadena fuera de rango'],
	['KeyError', 'Clave de diccionario inexistente'],
	['ValueError', 'Valor inválido'],
	['TypeError', 'Error de tipos'],
	['AttributeError', 'Atributo o campo inexistente'],
	['ImportError', 'Error al importar un módulo'],
	['RuntimeError', 'Error en tiempo de ejecución']
];

interface BuiltinDoc {
	name: string;
	detail: string;
	doc: string;
	snippet: string;
}

const BUILTINS: BuiltinDoc[] = [
	{
		name: 'escribir',
		detail: 'escribir(args…)',
		doc: 'Escribe los argumentos en la salida estándar. Acepta cualquier cantidad y tipo.',
		snippet: 'escribir(${1:valor})'
	},
	{
		name: 'leer',
		detail: 'leer(prompt: cadena): cadena',
		doc: 'Muestra el prompt y lee una línea. Devuelve cadena; conviértela con entero(…) o decimal(…) si esperas un número.',
		snippet: 'leer(${1:prompt})'
	},
	{
		name: 'tipo',
		detail: 'tipo(valor): cadena',
		doc: 'Devuelve el nombre del tipo del valor como cadena.',
		snippet: 'tipo(${1:valor})'
	},
	{
		name: 'rango',
		detail: 'rango(fin | inicio, fin [, paso]): lista<entero>',
		doc: 'Genera una lista de enteros. Acepta 1, 2 o 3 argumentos enteros (fin | inicio, fin | inicio, fin, paso).',
		snippet: 'rango(${1:fin})'
	},
	{
		name: 'longitud',
		detail: 'longitud(x: cadena | lista | diccionario): entero',
		doc: 'Cantidad de caracteres (cadena), elementos (lista) o pares clave-valor (diccionario).',
		snippet: 'longitud(${1:valor})'
	},
	{
		name: 'agregar',
		detail: 'agregar(lista, elemento): lista',
		doc: 'Agrega el elemento al final, mutando la lista (como append de Python). Devuelve la misma lista: valen agregar(l, x) y l = agregar(l, x). El elemento debe ser del tipo de la lista.',
		snippet: 'agregar(${1:lista}, ${2:elemento})'
	},
	{
		name: 'insertar',
		detail: 'insertar(lista, indice, elemento): lista',
		doc: 'Inserta el elemento en la posición indicada (0..longitud; longitud agrega al final), mutando la lista. Devuelve la misma lista: valen insertar(l, i, x) y l = insertar(l, i, x). Fuera de rango: IndexError.',
		snippet: 'insertar(${1:lista}, ${2:indice}, ${3:elemento})'
	},
	{
		name: 'quitar',
		detail: 'quitar(lista, indice): lista',
		doc: 'Quita el elemento de la posición indicada (0..longitud-1), mutando la lista. Devuelve la misma lista: valen quitar(l, i) y l = quitar(l, i). Fuera de rango: IndexError.',
		snippet: 'quitar(${1:lista}, ${2:indice})'
	},
	{
		name: 'vacia',
		detail: 'vacia(x: cadena | lista | diccionario): booleano',
		doc: 'Verdadero si la cadena, lista o diccionario no tiene elementos.',
		snippet: 'vacia(${1:valor})'
	},
	{
		name: 'primero',
		detail: 'primero(lista): elemento',
		doc: 'Primer elemento de la lista. En lista vacía lanza IndexError (capturable con intentar/capturar).',
		snippet: 'primero(${1:lista})'
	},
	{
		name: 'ultimo',
		detail: 'ultimo(lista): elemento',
		doc: 'Último elemento de la lista. En lista vacía lanza IndexError (capturable con intentar/capturar).',
		snippet: 'ultimo(${1:lista})'
	},
	{
		name: 'claves',
		detail: 'claves(diccionario): lista',
		doc: 'Devuelve la lista de claves del diccionario.',
		snippet: 'claves(${1:diccionario})'
	},
	{
		name: 'valores',
		detail: 'valores(diccionario): lista',
		doc: 'Devuelve la lista de valores del diccionario.',
		snippet: 'valores(${1:diccionario})'
	},
	{
		name: 'liberar',
		detail: 'liberar(x)',
		doc: 'Libera una cadena, lista, diccionario o estructura.',
		snippet: 'liberar(${1:variable})'
	},
	{
		name: 'entero',
		detail: 'entero(x): entero',
		doc: 'Conversión explícita a entero.',
		snippet: 'entero(${1:valor})'
	},
	{
		name: 'decimal',
		detail: 'decimal(x): decimal',
		doc: 'Conversión explícita a decimal.',
		snippet: 'decimal(${1:valor})'
	},
	{
		name: 'cadena',
		detail: 'cadena(x): cadena',
		doc: 'Conversión explícita a cadena.',
		snippet: 'cadena(${1:valor})'
	},
	{
		name: 'booleano',
		detail: 'booleano(x): booleano',
		doc: 'Conversión explícita a booleano.',
		snippet: 'booleano(${1:valor})'
	}
];

// ─────────────────────────────────────────────────────────────
// Estado global (caché de stdlib + módulos descubiertos)
// ─────────────────────────────────────────────────────────────

const stdlib = new Map<string, WiniFunction>();
const modulos = new Map<string, WiniModulo>();

let canal: vscode.OutputChannel | undefined;
function log(msg: string): void {
	if (!canal) {
		canal = vscode.window.createOutputChannel('Wini');
	}
	canal.appendLine(msg);
}

// ─────────────────────────────────────────────────────────────
// Configuración
// ─────────────────────────────────────────────────────────────

function cfg<T>(clave: string, defecto: T): T {
	return vscode.workspace.getConfiguration('wini').get<T>(clave, defecto);
}

// ─────────────────────────────────────────────────────────────
// Parsers tolerantes (funciones, estructuras, externas)
// ─────────────────────────────────────────────────────────────

/** Parte una lista de parámetros por comas, respetando <>, [], {}, () y strings. */
function partirParametros(raw: string): string[] {
	const piezas: string[] = [];
	let actual = '';
	let ang = 0, cor = 0, lla = 0, par = 0;
	let enStr: string | undefined;
	for (let i = 0; i < raw.length; i++) {
		const ch = raw[i];
		if (enStr) {
			actual += ch;
			if (ch === '\\' && i + 1 < raw.length) {
				actual += raw[++i];
			} else if (ch === enStr) {
				enStr = undefined;
			}
			continue;
		}
		if (ch === '"' || ch === "'") {
			enStr = ch;
			actual += ch;
			continue;
		}
		if (ch === '<') { ang++; }
		else if (ch === '>') { ang = Math.max(0, ang - 1); }
		else if (ch === '[') { cor++; }
		else if (ch === ']') { cor = Math.max(0, cor - 1); }
		else if (ch === '{') { lla++; }
		else if (ch === '}') { lla = Math.max(0, lla - 1); }
		else if (ch === '(') { par++; }
		else if (ch === ')') { par = Math.max(0, par - 1); }
		if (ch === ',' && ang === 0 && cor === 0 && lla === 0 && par === 0) {
			piezas.push(actual);
			actual = '';
			continue;
		}
		actual += ch;
	}
	if (actual.trim().length > 0) {
		piezas.push(actual);
	}
	return piezas;
}

/** Parte `nombre: tipo = default` en sus componentes, respetando <> y strings. */
function partirParametro(pieza: string): ParamDoc {
	let texto = pieza.trim();
	let variadic = false;
	if (texto.startsWith('*')) {
		variadic = true;
		texto = texto.slice(1).trim();
	}
	// Separa el default por el primer `=` fuera de <>, brackets y strings.
	let nombreTipo = texto;
	let def: string | undefined;
	{
		let ang = 0, cor = 0, lla = 0, par = 0;
		let enStr: string | undefined;
		for (let i = 0; i < texto.length; i++) {
			const ch = texto[i];
			if (enStr) {
				if (ch === '\\') { i++; }
				else if (ch === enStr) { enStr = undefined; }
				continue;
			}
			if (ch === '"' || ch === "'") { enStr = ch; continue; }
			if (ch === '<') { ang++; continue; }
			if (ch === '>') { ang = Math.max(0, ang - 1); continue; }
			if (ch === '[') { cor++; continue; }
			if (ch === ']') { cor = Math.max(0, cor - 1); continue; }
			if (ch === '{') { lla++; continue; }
			if (ch === '}') { lla = Math.max(0, lla - 1); continue; }
			if (ch === '(') { par++; continue; }
			if (ch === ')') { par = Math.max(0, par - 1); continue; }
			if (ch === '=' && ang === 0 && cor === 0 && lla === 0 && par === 0) {
				nombreTipo = texto.slice(0, i).trim();
				def = texto.slice(i + 1).trim();
				break;
			}
		}
	}
	// Separa nombre:tipo por el primer `:` fuera de <>.
	let nombre = nombreTipo.trim();
	let tipo = 'desconocido';
	{
		let ang = 0;
		for (let i = 0; i < nombreTipo.length; i++) {
			const ch = nombreTipo[i];
			if (ch === '<') { ang++; continue; }
			if (ch === '>') { ang = Math.max(0, ang - 1); continue; }
			if (ch === ':' && ang === 0) {
				nombre = nombreTipo.slice(0, i).trim();
				tipo = nombreTipo.slice(i + 1).trim() || 'desconocido';
				break;
			}
		}
	}
	const mNombre = nombre.match(/^[A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*$/);
	if (!mNombre) {
		return { name: pieza.trim(), type: 'desconocido', defaultValue: def, variadic };
	}
	const p: ParamDoc = { name: nombre, type: tipo };
	if (def !== undefined) {
		p.defaultValue = def;
	}
	if (variadic) {
		p.variadic = true;
	}
	return p;
}

function comentarioEncima(lineas: string[], indiceDef: number): string {
	let description = '';
	for (let i = indiceDef - 1; i >= 0; i--) {
		const t = lineas[i].trim();
		if (t.startsWith('#')) {
			description = t.replace(/^#+\s*/, '') + (description ? '\n' + description : '');
		} else if (t === '') {
			continue;
		} else {
			break;
		}
	}
	return description;
}

function parseWiniFunctions(content: string, fileName: string, origin: 'stdlib' | 'modulo' | 'documento', rutaAbs?: string): WiniFunction[] {
	const out: WiniFunction[] = [];
	const lineas = content.split(/\r?\n/);
	// Acepta `funcion f(...) -> tipo:`, `funcion f(...): tipo:` y sin retorno.
	// El `(\.\.\.)?` cubre firmas externas mal ubicadas; no molesta.
	const re = /^[ \t]*funcion[ \t]+([A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*)[ \t]*\(([^)]*)\)[ \t]*(?:(?:->|:)[ \t]*([A-Za-z_\u00C0-\u017F][\w.\[\]<>, \t|]*?))?[ \t]*:[ \t]*(?:#.*)?$/;
	for (let i = 0; i < lineas.length; i++) {
		const m = lineas[i].match(re);
		if (!m) {
			continue;
		}
		const params: ParamDoc[] = [];
		const raw = m[2].trim();
		if (raw.length > 0) {
			for (const piece of partirParametros(raw)) {
				if (piece.trim()) {
					params.push(partirParametro(piece));
				}
			}
		}
		const returnType = (m[3] || '').trim();
		const ps = params.map(p =>
			`${p.variadic ? '*' : ''}${p.name}: ${p.type}${p.defaultValue !== undefined ? ' = ' + p.defaultValue : ''}`
		).join(', ');
		out.push({
			name: m[1],
			params,
			returnType,
			file: fileName,
			rutaAbs,
			line: i + 1,
			description: comentarioEncima(lineas, i),
			origin,
			signature: `funcion ${m[1]}(${ps})${returnType ? ' -> ' + returnType : ''}:`
		});
	}
	return out;
}

function parseWiniEstructuras(content: string, fileName: string, origin: 'modulo' | 'documento', rutaAbs?: string): WiniEstructura[] {
	const out: WiniEstructura[] = [];
	const lineas = content.split(/\r?\n/);
	const reDef = /^[ \t]*estructura[ \t]+([A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*)[ \t]*:[ \t]*(?:#.*)?$/;
	for (let i = 0; i < lineas.length; i++) {
		const m = lineas[i].match(reDef);
		if (!m) {
			continue;
		}
		const indentBase = lineas[i].search(/[^ \t]/);
		const fields: WiniCampo[] = [];
		for (let j = i + 1; j < lineas.length; j++) {
			const l = lineas[j];
			if (l.trim() === '' || l.trim().startsWith('#')) {
				continue;
			}
			const ind = l.search(/[^ \t]/);
			if (ind <= indentBase) {
				break;
			}
			const fm = l.trim().match(/^([A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*)[ \t]*:[ \t]*([\w.\[\]<>, \t|]+?)[ \t]*(?:#.*)?$/);
			if (fm) {
				fields.push({ name: fm[1], type: fm[2].trim(), line: j + 1 });
			} else {
				break;
			}
		}
		out.push({
			name: m[1],
			fields,
			file: fileName,
			rutaAbs,
			line: i + 1,
			description: comentarioEncima(lineas, i),
			origin
		});
	}
	return out;
}

function parseWiniExternas(content: string, fileName: string, origin: 'modulo' | 'documento', rutaAbs?: string): WiniExterna[] {
	const res: WiniExterna[] = [];
	const lineas = content.split(/\r?\n/);
	const re = /^[ \t]*externa[ \t]+(?:"([^"]+)"[ \t]+como[ \t]+)?([A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*)[ \t]*\(([^)]*)\)[ \t]*(?:(?:->|:)[ \t]*([\w.\[\]<>, \t|]*?))?[ \t]*:?[ \t]*(?:#.*)?$/;
	for (let i = 0; i < lineas.length; i++) {
		const m = lineas[i].match(re);
		if (!m) {
			continue;
		}
		const params: ParamDoc[] = [];
		const raw = m[3].trim();
		if (raw.length > 0 && raw !== '...') {
			for (const piece of partirParametros(raw)) {
				if (piece.trim() && piece.trim() !== '...') {
					params.push(partirParametro(piece));
				}
			}
		}
		res.push({
			name: m[2],
			simboloC: m[1] || m[2],
			params,
			returnType: (m[4] || '').trim(),
			variadicaC: raw.includes('...'),
			file: fileName,
			rutaAbs,
			line: i + 1,
			description: comentarioEncima(lineas, i),
			origin
		});
	}
	return res;
}

// ─────────────────────────────────────────────────────────────
// Documentación estilo "Función · stdlib.wini:292"
// ─────────────────────────────────────────────────────────────

function paramsTexto(params: ParamDoc[]): string {
	return params.map(p => `${p.variadic ? '*' : ''}${p.name}: ${p.type}${p.defaultValue !== undefined ? ' = ' + p.defaultValue : ''}`).join(', ');
}

/** Una función es deprecada si alguna línea de su comentario empieza con `# DEPRECADO`. */
function esDeprecada(f: WiniFunction): boolean {
	return f.description.split('\n').some(l => l.startsWith('DEPRECADO'));
}

function marcarDeprecada(item: vscode.CompletionItem, f: WiniFunction): void {
	if (esDeprecada(f)) {
		item.tags = [vscode.CompletionItemTag.Deprecated];
	}
}

function funcDetail(f: WiniFunction): string {
	if (f.signature) {
		return f.signature;
	}
	return `funcion ${f.name}(${paramsTexto(f.params)})${f.returnType ? ' -> ' + f.returnType : ''}:`;
}

function funcDoc(f: WiniFunction): vscode.MarkdownString {
	const md = new vscode.MarkdownString();
	if (f.description) {
		md.appendText(f.description);
		md.appendText('\n\n');
	}
	const etiqueta = f.origin === 'modulo' ? 'Módulo' : f.origin === 'stdlib' ? 'Función' : 'Documento';
	md.appendMarkdown(`**${etiqueta}** · \`${f.file}:${f.line}\`\n\n`);
	md.appendCodeblock(funcDetail(f), 'wini');
	if (f.params.length > 0) {
		md.appendMarkdown('\n\n**Parámetros**\n');
		for (const p of f.params) {
			md.appendMarkdown(`\n- \`${p.variadic ? '*' : ''}${p.name}\` — ${p.type}${p.defaultValue !== undefined ? ` = ${p.defaultValue}` : ''}`);
		}
	}
	if (f.returnType) {
		md.appendMarkdown(`\n\n**Devuelve:** \`${f.returnType}\``);
	}
	return md;
}

function structDoc(e: WiniEstructura): vscode.MarkdownString {
	const md = new vscode.MarkdownString();
	if (e.description) {
		md.appendText(e.description);
		md.appendText('\n\n');
	}
	md.appendMarkdown(`**Estructura** · \`${e.file}:${e.line}\`\n\n`);
	const cuerpo = e.fields.map(c => `    ${c.name}: ${c.type}`).join('\n');
	md.appendCodeblock(`estructura ${e.name}:\n${cuerpo}`, 'wini');
	return md;
}

function externaDoc(e: WiniExterna): vscode.MarkdownString {
	const md = new vscode.MarkdownString();
	if (e.description) {
		md.appendText(e.description);
		md.appendText('\n\n');
	}
	md.appendMarkdown(`**Externa** · \`${e.file}:${e.line}\` · símbolo C \`${e.simboloC}\`\n\n`);
	md.appendCodeblock(`externa "${e.simboloC}" como ${e.name}(${paramsTexto(e.params)}${e.variadicaC ? ', ...' : ''})${e.returnType ? ': ' + e.returnType : ''}`, 'wini');
	return md;
}

// ─────────────────────────────────────────────────────────────
// Localización de winic (con configuración y caché)
// ─────────────────────────────────────────────────────────────

let cacheWinic: string | undefined;
let cacheWinicOk = false;

const NOMBRES_WINIC = process.platform === 'win32'
	? ['winic.exe', 'winic.cmd', 'winic.bat', 'winic']
	: ['winic'];

function ejecutar(cmd: string, args: string[]): string | undefined {
	try {
		const salida = execFileSync(cmd, args, {
			encoding: 'utf8',
			timeout: 5000,
			stdio: ['ignore', 'pipe', 'ignore']
		});
		return typeof salida === 'string' ? salida : undefined;
	} catch {
		return undefined;
	}
}

/** Limpia la caché de ubicación de winic (al cambiar la configuración). */
export function olvidarWinic(): void {
	cacheWinic = undefined;
	cacheWinicOk = false;
}

function esEjecutableWinic(ruta: string): boolean {
	try {
		if (!fs.statSync(ruta).isFile()) {
			return false;
		}
		if (process.platform !== 'win32') {
			fs.accessSync(ruta, fs.constants.X_OK);
		}
		return true;
	} catch {
		return false;
	}
}

/** Busca `nombres` en el PATH: `which`/`where` y recorrido manual. */
function buscarEnPath(nombres: string[]): string | undefined {
	const sonda = process.platform === 'win32' ? 'where' : 'which';
	for (const nombre of nombres) {
		const salida = ejecutar(sonda, [nombre]);
		if (salida) {
			for (const linea of salida.split(/\r?\n/)) {
				const ruta = linea.trim();
				if (ruta && esEjecutableWinic(ruta)) {
					return ruta;
				}
			}
		}
	}
	const pathEnv = process.env.PATH || '';
	for (const dir of pathEnv.split(path.delimiter)) {
		if (!dir) {
			continue;
		}
		for (const nombre of nombres) {
			const candidato = path.join(dir, nombre);
			if (esEjecutableWinic(candidato)) {
				return candidato;
			}
		}
	}
	return undefined;
}

/** Busca un winic dentro de `dir` (sin bajar a subcarpetas). */
function buscarEnDir(dir: string): string | undefined {
	for (const nombre of NOMBRES_WINIC) {
		const candidato = path.join(dir, nombre);
		if (esEjecutableWinic(candidato)) {
			return candidato;
		}
	}
	return undefined;
}

/**
 * Búsqueda global (config + PATH), con caché: vale para cualquier proyecto.
 * No incluye la búsqueda local (workspace/cercanos), que depende del archivo.
 */
function buscarWinicGlobal(): string | undefined {
	if (cacheWinicOk) {
		return cacheWinic;
	}
	// 1) Config explícita `wini.winicPath`.
	const configurado = cfg<string>('winicPath', '').trim();
	if (configurado) {
		const resuelto = configurado.startsWith('~')
			? path.join(os.homedir(), configurado.slice(1))
			: configurado;
		if (resuelto.includes('/') || resuelto.includes(path.sep)) {
			if (esEjecutableWinic(resuelto)) {
				cacheWinic = resuelto;
				cacheWinicOk = true;
				return resuelto;
			}
			log(`Wini: wini.winicPath apunta a '${configurado}' pero no es un ejecutable; sigo buscando.`);
		} else {
			// Nombre suelto (p.ej. `winic`): se resuelve contra el PATH,
			// no contra el directorio de trabajo del extension host.
			const porNombre = buscarEnPath([resuelto]);
			if (porNombre) {
				cacheWinic = porNombre;
				cacheWinicOk = true;
				return porNombre;
			}
			log(`Wini: wini.winicPath='${configurado}' no se encontró en el PATH; sigo buscando.`);
		}
	}
	// 2) PATH del entorno (which/where + recorrido manual).
	const enPath = buscarEnPath(NOMBRES_WINIC);
	cacheWinic = enPath;
	cacheWinicOk = true;
	return enPath;
}

/**
 * Búsqueda local (sin caché): raíz del workspace, carpetas hacia arriba
 * desde `desde` y sitios comunes. Cubre el caso típico que falla: VS Code
 * abierto desde el lanzador gráfico no hereda el PATH del shell, pero el
 * `winic` suele estar junto al proyecto.
 */
function buscarWinicLocal(desde?: string): string | undefined {
	for (const carpeta of vscode.workspace.workspaceFolders || []) {
		const c = buscarEnDir(carpeta.uri.fsPath);
		if (c) {
			return c;
		}
	}
	const inicio = desde || vscode.window.activeTextEditor?.document.uri.fsPath;
	if (inicio) {
		let dir = path.dirname(inicio);
		for (let i = 0; i < 6; i++) {
			const c = buscarEnDir(dir);
			if (c) {
				return c;
			}
			const padre = path.dirname(dir);
			if (padre === dir) {
				break;
			}
			dir = padre;
		}
	}
	if (process.platform !== 'win32') {
		for (const d of [path.join(os.homedir(), '.local', 'bin'), '/usr/local/bin']) {
			const c = buscarEnDir(d);
			if (c) {
				return c;
			}
		}
	}
	return undefined;
}

// Localiza el ejecutable `winic`: config `wini.winicPath` > PATH
// (which/where) > raíz del workspace > carpetas cercanas al archivo >
// sitios comunes. `desde` (opcional) es el archivo de referencia para la
// búsqueda cercana; los resultados locales no se cachean porque dependen
// del proyecto abierto.
export function buscarEjecutableWinic(desde?: string): string | undefined {
	return buscarWinicGlobal() || buscarWinicLocal(desde);
}

// Devuelve los módulos descubiertos (para diagnóstico y pruebas).
export function modulosDescubiertos(): WiniModulo[] {
	return [...modulos.values()];
}

function estructurasDescubiertas(): WiniEstructura[] {
	const out: WiniEstructura[] = [];
	for (const m of modulos.values()) {
		out.push(...m.estructuras);
	}
	return out;
}

// Directorios donde buscar módulos: junto a winic, WINI_PATH, config extra,
// carpeta `modulos/` del workspace y la propia carpeta de la extensión.
export function directoriosDeModulos(extensionPath: string): string[] {
	const dirs = new Set<string>();

	const winic = buscarEjecutableWinic();
	if (winic) {
		const base = path.dirname(winic);
		dirs.add(path.join(base, 'modulos'));
		dirs.add(path.join(base, 'modules'));
		dirs.add(path.join(base, '..', 'modulos'));
		// Layout 'make install': <prefijo>/bin/winic + <prefijo>/share/wini.
		dirs.add(path.join(base, '..', 'share', 'wini', 'modulos'));
		dirs.add(path.join(base, '..', 'share', 'wini'));
	}

	// Ubicaciones estándar: valen aunque winic aún no se haya localizado
	// o VS Code no heredara el entorno del shell (lanzador gráfico).
	dirs.add(path.join(os.homedir(), '.local', 'share', 'wini', 'modulos'));
	dirs.add(path.join(os.homedir(), '.wini', 'modulos'));
	if (process.platform !== 'win32') {
		dirs.add('/usr/local/share/wini/modulos');
		dirs.add('/usr/share/wini/modulos');
	}

	const winipath = process.env.WINI_PATH;
	if (winipath) {
		for (const d of winipath.split(path.delimiter)) {
			if (d) {
				dirs.add(d);
			}
		}
	}
	const extra = cfg<string>('winiPathExtra', '');
	if (extra) {
		for (const d of extra.split(path.delimiter)) {
			const t = d.trim();
			if (t) {
				dirs.add(t);
			}
		}
	}

	for (const carpeta of vscode.workspace.workspaceFolders || []) {
		dirs.add(path.join(carpeta.uri.fsPath, 'modulos'));
	}

	dirs.add(path.join(extensionPath, 'modulos'));
	dirs.add(path.join(extensionPath, 'stdlib'));

	return [...dirs].filter(d => {
		try {
			return fs.statSync(d).isDirectory();
		} catch {
			return false;
		}
	});
}

// Recorre un directorio y devuelve los archivos de módulo Wini.
function archivosDeModulo(dir: string, profundidad = 0): string[] {
	const salida: string[] = [];
	if (profundidad > 6) {
		return salida;
	}
	let entradas: fs.Dirent[];
	try {
		entradas = fs.readdirSync(dir, { withFileTypes: true });
	} catch {
		return salida;
	}
	for (const e of entradas) {
		if (e.name.startsWith('.')) {
			continue;
		}
		if (e.name === 'node_modules' || e.name === 'build' || e.name === '.git' || e.name === 'out') {
			continue;
		}
		const completo = path.join(dir, e.name);
		if (e.isDirectory()) {
			salida.push(...archivosDeModulo(completo, profundidad + 1));
		} else if (EXTENSIONES_MODULO.includes(path.extname(e.name).toLowerCase())) {
			salida.push(completo);
		}
	}
	return salida;
}

// Convierte una ruta relativa a nombre de módulo con puntos.
function nombreDeModulo(raiz: string, archivo: string): string {
	const rel = path.relative(raiz, archivo);
	const sinExt = rel.slice(0, rel.length - path.extname(rel).length);
	return sinExt.split(path.sep).join('.');
}

// Rutas de módulo ya registradas (absolutas): evita que el mismo archivo
// aparezca duplicado cuando dos raíces se solapan. Se limpia en
// cargarStdlib junto con `modulos`.
const rutasModulosVistas = new Map<string, string>();

function registrarModulo(m: WiniModulo): void {
	const previo = modulos.get(m.nombre);
	if (!previo) {
		modulos.set(m.nombre, m);
		return;
	}
	// El workspace gana al sistema: un módulo local con el mismo nombre
	// reemplaza al descubierto junto a winic.
	if (m.origen === 'workspace' && previo.origen === 'sistema') {
		modulos.set(m.nombre, m);
	}
}

function agregarModuloDesdeArchivo(raiz: string, archivo: string, origen: 'sistema' | 'workspace'): void {
	// Rutas ya registradas: dos raíces pueden solaparse (p.ej.
	// `.../share/wini` y `.../share/wini/modulos`, o `modulos/` del
	// workspace escaneado como sistema y como workspace) y sin esto el
	// mismo archivo aparece varias veces en `importar`.
	const clave = path.resolve(archivo);
	const yaVisto = rutasModulosVistas.get(clave);
	if (yaVisto !== undefined) {
		// El workspace manda: si entró como sistema y ahora viene como
		// workspace, se reemplaza (mismo archivo, mejor origen/nombre).
		if (origen === 'workspace' && modulos.get(yaVisto)?.origen === 'sistema') {
			modulos.delete(yaVisto);
		} else {
			return;
		}
	}
	try {
		const contenido = fs.readFileSync(archivo, 'utf8');
		const base = path.basename(archivo);
		const funciones = parseWiniFunctions(contenido, base, 'modulo', archivo);
		const estructuras = parseWiniEstructuras(contenido, base, 'modulo', archivo);
		const externas = parseWiniExternas(contenido, base, 'modulo', archivo);
		const nombre = nombreDeModulo(raiz, archivo);
		registrarModulo({
			nombre,
			etiqueta: nombre.split('.').pop() || nombre,
			ruta: archivo,
			origen,
			funciones: funciones.length,
			declaraciones: funciones,
			estructuras,
			externas
		});
		rutasModulosVistas.set(clave, nombre);
	} catch (err) {
		console.error(`Wini: no se pudo leer el módulo ${archivo}`, err);
	}
}

// Escanea recursivamente un directorio y registra cada módulo encontrado.
export function escanearDirectorioModulos(raiz: string, origen: 'sistema' | 'workspace'): void {
	for (const archivo of archivosDeModulo(raiz)) {
		agregarModuloDesdeArchivo(raiz, archivo, origen);
	}
}

// ─────────────────────────────────────────────────────────────
// Carga de stdlib + módulos (workspace y sistema)
// ─────────────────────────────────────────────────────────────

const EXCLUIR_BUSQUEDA = '{**/node_modules/**,**/build/**,**/.git/**,**/out/**}';

// Carga stdlib + módulos. Exportada para diagnóstico y pruebas.
export async function cargarStdlib(context: vscode.ExtensionContext): Promise<void> {
	stdlib.clear();
	modulos.clear();
	rutasModulosVistas.clear();

	// 1) Módulos junto a `winic` (encontrado con which/where) y en WINI_PATH.
	const winic = buscarEjecutableWinic();
	for (const dir of directoriosDeModulos(context.extensionPath)) {
		escanearDirectorioModulos(dir, 'sistema');
	}

	// 2) stdlib del workspace y de la extensión (histórico: stdlib.wini).
	const candidatos: vscode.Uri[] = [];
	const vistos = new Set<string>();
	try {
		const patrones = ['**/stdlib.wini', '**/stdlib.cwn', '**/stdlib.wn'];
		for (const patron of patrones) {
			for (const uri of await vscode.workspace.findFiles(patron, EXCLUIR_BUSQUEDA, 20)) {
				if (!vistos.has(uri.fsPath)) {
					vistos.add(uri.fsPath);
					candidatos.push(uri);
				}
			}
		}
	} catch (err) {
		console.error('Wini: error buscando stdlib en el workspace', err);
	}
	for (const rel of ['stdlib.wini', path.join('stdlib', 'stdlib.wini'), 'stdlib.cwn']) {
		const p = path.join(context.extensionPath, rel);
		if (!vistos.has(p) && fs.existsSync(p)) {
			vistos.add(p);
			candidatos.push(vscode.Uri.file(p));
		}
	}
	for (const uri of candidatos) {
		try {
			const data = await vscode.workspace.fs.readFile(uri);
			const text = Buffer.from(data).toString('utf8');
			for (const f of parseWiniFunctions(text, path.basename(uri.fsPath), 'stdlib', uri.fsPath)) {
				if (!stdlib.has(f.name)) {
					stdlib.set(f.name, f);
				}
			}
		} catch (err) {
			console.error(`Wini: no se pudo leer ${uri.fsPath}`, err);
		}
	}

	// 3) Módulos .wn/.wini/.cwn del propio workspace (relativos a su raíz).
	const raices = vscode.workspace.workspaceFolders || [];
	for (const carpeta of raices) {
		const raiz = carpeta.uri.fsPath;
		try {
			const uris = await vscode.workspace.findFiles(
				new vscode.RelativePattern(carpeta, '**/*.{wini,cwn,wn}'),
				EXCLUIR_BUSQUEDA,
				500
			);
			for (const uri of uris) {
				if (!uri.fsPath.startsWith(raiz)) {
					continue;
				}
				agregarModuloDesdeArchivo(raiz, uri.fsPath, 'workspace');
			}
		} catch (err) {
			console.error('Wini: error buscando módulos en el workspace', err);
		}
	}

	const viaWinic = winic ? `winic: ${winic}` : 'winic no está en el PATH';
	const nEst = [...modulos.values()].reduce((a, m) => a + m.estructuras.length, 0);
	log(`Wini: ${modulos.size} módulo(s), ${stdlib.size} función(es) de stdlib y ${nEst} estructura(s) · ${viaWinic}`);
}

// ─────────────────────────────────────────────────────────────
// Recopila stdlib + módulos + documento actual
// ─────────────────────────────────────────────────────────────

type ModoSugerencia = 'importados' | 'stdlib' | 'siempre';

/**
 * Qué funciones se sugieren sin importar (config `wini.sugerirSinImportar`):
 * - `importados`: solo builtins, documento e importados (verdad del compilador).
 * - `stdlib`: lo anterior más la stdlib clásica.
 * - `siempre`: todo lo descubierto (comportamiento anterior a v0.6.4).
 */
function modoSugerencias(): ModoSugerencia {
	const m = cfg<string>('sugerirSinImportar', 'importados');
	return m === 'siempre' || m === 'stdlib' ? m : 'importados';
}

function externaComoFuncion(e: WiniExterna, origin: 'modulo' | 'documento'): WiniFunction {
	return {
		name: e.name,
		params: e.params,
		returnType: e.returnType,
		file: e.file,
		rutaAbs: e.rutaAbs,
		line: e.line,
		description: e.description || `Función externa (símbolo C ${e.simboloC}).`,
		origin
	};
}

function recopilarFunciones(document: vscode.TextDocument): Map<string, WiniFunction> {
	const mapa = new Map<string, WiniFunction>();
	const modo = modoSugerencias();
	if (modo !== 'importados') {
		for (const f of stdlib.values()) {
			mapa.set(f.name, f);
		}
	}
	for (const b of BUILTINS) {
		if (!mapa.has(b.name)) {
			mapa.set(b.name, {
				name: b.name,
				params: [],
				returnType: '',
				file: '(builtin)',
				line: 0,
				description: b.doc,
				origin: 'documento',
				signature: b.detail
			});
		}
	}
	try {
		const base = path.basename(document.uri.fsPath);
		for (const f of parseWiniFunctions(document.getText(), base, 'documento', document.uri.fsPath)) {
			mapa.set(f.name, f);
		}
		for (const e of parseWiniExternas(document.getText(), base, 'documento', document.uri.fsPath)) {
			if (!mapa.has(e.name)) {
				mapa.set(e.name, externaComoFuncion(e, 'documento'));
			}
		}
		if (modo === 'siempre') {
			for (const m of modulos.values()) {
				for (const f of m.declaraciones) {
					if (!mapa.has(f.name)) {
						mapa.set(f.name, f);
					}
				}
				for (const e of m.externas) {
					if (!mapa.has(e.name)) {
						mapa.set(e.name, externaComoFuncion(e, 'modulo'));
					}
				}
			}
		}
	} catch (err) {
		console.error('Wini: error analizando el documento actual', err);
	}
	return mapa;
}

function recopilarEstructuras(document: vscode.TextDocument): Map<string, WiniEstructura> {
	const mapa = new Map<string, WiniEstructura>();
	for (const e of estructurasDescubiertas()) {
		if (!mapa.has(e.name)) {
			mapa.set(e.name, e);
		}
	}
	try {
		for (const e of parseWiniEstructuras(document.getText(), path.basename(document.uri.fsPath), 'documento', document.uri.fsPath)) {
			mapa.set(e.name, e);
		}
	} catch (err) {
		console.error('Wini: error analizando estructuras del documento', err);
	}
	return mapa;
}

/** Heurística: ¿de qué estructura es la variable `nombre` en este documento? */
function estructuraDeVariable(document: vscode.TextDocument, nombre: string): WiniEstructura | undefined {
	const texto = document.getText();
	const estructuras = recopilarEstructuras(document);
	// `Nombre v = Nombre(`, `Nombre v: Nombre`, `v = Nombre(`, `tipo v = Nombre(`
	const patrones = [
		new RegExp(`([A-Za-z_\\u00C0-\\u017F][\\w\\u00C0-\\u017F]*)\\s+${nombre}\\s*=\\s*([A-Za-z_\\u00C0-\\u017F][\\w\\u00C0-\\u017F]*)\\s*\\(`, 'm'),
		new RegExp(`${nombre}\\s*=\\s*([A-Za-z_\\u00C0-\\u017F][\\w\\u00C0-\\u017F]*)\\s*\\(`, 'm'),
		new RegExp(`([A-Za-z_\\u00C0-\\u017F][\\w\\u00C0-\\u017F]*)\\s+${nombre}\\s*:[ \\t]*([A-Za-z_\\u00C0-\\u017F][\\w\\u00C0-\\u017F]*)`, 'm')
	];
	for (const re of patrones) {
		const m = texto.match(re);
		if (m) {
			for (const grupo of m.slice(1)) {
				const e = estructuras.get(grupo);
				if (e) {
					return e;
				}
			}
		}
	}
	return undefined;
}

// ¿El cursor está escribiendo el nombre/ruta de un `importar`?
export function enContextoImportar(document: vscode.TextDocument, position: vscode.Position): boolean {
	const linea = document.lineAt(position.line).text.slice(0, position.character);
	return /(^|\s)importar[ \t]+(?:"[^"]*|[^\s#]*)$/.test(linea);
}

/** ¿El cursor está tras `capturar` (para sugerir excepciones)? */
function enContextoCapturar(document: vscode.TextDocument, position: vscode.Position): boolean {
	const linea = document.lineAt(position.line).text.slice(0, position.character);
	return /(^|\s)capturar[ \t]+[A-Za-z_]*$/.test(linea);
}

/** ¿El cursor está tras `lanzar` (para sugerir excepciones)? */
function enContextoLanzar(document: vscode.TextDocument, position: vscode.Position): boolean {
	const linea = document.lineAt(position.line).text.slice(0, position.character);
	return /(^|\s)lanzar[ \t]+[A-Za-z_]*$/.test(linea);
}

/** ¿El cursor está declarando un tipo (`: <aquí>`, `-> <aquí>`, `lista<…>`)? */
function enContextoTipo(document: vscode.TextDocument, position: vscode.Position): boolean {
	const linea = document.lineAt(position.line).text.slice(0, position.character);
	return /(:|->|<)[ \t]*[A-Za-z_]*$/.test(linea);
}

// Rango que cubre lo que el usuario ya escribió como nombre/ruta dentro de
// un `importar` (desde después de `importar ` hasta el cursor). La comilla de
// apertura (si ya se escribió) queda FUERA del rango: si entrara, el texto
// filtrado empezaría con `"` y VS Code descartaría todas las sugerencias al
// no matchear ningún filterText.
export function rangoImportar(document: vscode.TextDocument, position: vscode.Position): vscode.Range | undefined {
	const antes = document.lineAt(position.line).text.slice(0, position.character);
	const m = antes.match(/(^|\s)importar[ \t]+"?/);
	if (!m || m.index === undefined) {
		return undefined;
	}
	const inicio = m.index + m[0].length;
	return new vscode.Range(new vscode.Position(position.line, inicio), position);
}

function prefijoImportar(document: vscode.TextDocument, position: vscode.Position): string {
	const antes = document.lineAt(position.line).text.slice(0, position.character);
	const m = antes.match(/(^|\s)importar[ \t]+("?)([^"]*)$/);
	return m ? m[3].replace(/\\/g, '/') : '';
}

function referenciasModulo(document: vscode.TextDocument, modulo: WiniModulo): string[] {
	const ruta = rutaImportable(document, modulo.ruta).replace(/\.(?:wini|cwn|wn)$/i, '');
	const referencias = new Set([
		modulo.nombre,
		modulo.etiqueta,
		ruta,
		ruta.replace(/\//g, '.'),
		`modulos/${modulo.nombre}`,
		`modulos.${modulo.nombre}`
	]);
	return [...referencias];
}

function normalizarRuta(p: string): string {
	return path.normalize(p.replace(/\\/g, '/')).toLowerCase();
}

/** Resuelve la referencia de un `importar` al módulo descubierto. */
function resolverModuloDeReferencia(document: vscode.TextDocument, referencia: string): WiniModulo | undefined {
	const ref = referencia.replace(/\\/g, '/').replace(/\.(?:wini|cwn|wn)$/i, '');
	const todos = [...modulos.values()];
	// 1) Ruta relativa real al documento (`./x`, `../x`, `modulos/x`).
	if (ref.includes('/') || ref.startsWith('.')) {
		const base = path.dirname(document.uri.fsPath);
		const abs = path.resolve(base, ref);
		const candidatos = [abs, ...EXTENSIONES_MODULO.map(e => abs + e)];
		for (const m of todos) {
			const nm = normalizarRuta(m.ruta.replace(/\.(?:wini|cwn|wn)$/i, ''));
			for (const c of candidatos) {
				if (nm === normalizarRuta(c)) {
					return m;
				}
			}
		}
		// 1b) Relativa a cada raíz del workspace (p.ej. `modulos/milib` con el
		// proyecto abierto aunque el documento esté en otra subcarpeta).
		for (const carpeta of vscode.workspace.workspaceFolders || []) {
			const absW = path.resolve(carpeta.uri.fsPath, ref);
			const candidatosW = [absW, ...EXTENSIONES_MODULO.map(e => absW + e)];
			for (const m of todos) {
				const nm = normalizarRuta(m.ruta.replace(/\.(?:wini|cwn|wn)$/i, ''));
				for (const c of candidatosW) {
					if (nm === normalizarRuta(c)) {
						return m;
					}
				}
			}
		}
	}
	// 2) Por nombre, etiqueta o sufijo de ruta (comportamiento anterior).
	const porNombre = todos.find(m => {
		const ruta = m.ruta.replace(/\\/g, '/').replace(/\.(?:wini|cwn|wn)$/i, '');
		return m.nombre === ref || m.etiqueta === ref ||
			ruta.endsWith('/' + ref) || path.basename(ruta) === ref;
	});
	if (porNombre) {
		return porNombre;
	}
	// 3) Último segmento (`importar "sub/milib"` cuando el archivo vive en otra
	// raíz): mejor una candidata que ninguna para el alias.
	const ultimo = ref.split('/').pop() || ref;
	return todos.find(m => m.etiqueta === ultimo);
}

function modulosImportados(document: vscode.TextDocument): WiniModulo[] {
	const importados: WiniModulo[] = [];
	const vistos = new Set<string>();
	const re = /^\s*importar\s+(?:"([^"]+)"|([^\s#]+))(?:\s+como\s+([A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*))?/gm;
	let match: RegExpExecArray | null;
	while ((match = re.exec(document.getText())) !== null) {
		const referencia = (match[1] || match[2] || '').replace(/\\/g, '/');
		const alias = match[3];
		const modulo = resolverModuloDeReferencia(document, referencia);
		if (modulo && !vistos.has(modulo.nombre)) {
			vistos.add(modulo.nombre);
			importados.push(alias ? { ...modulo, etiqueta: alias } : modulo);
		}
	}
	return importados;
}

/** Base (builtins, documento, modo) + funciones de los módulos importados. */
function mapaFuncionesDisponibles(document: vscode.TextDocument): Map<string, WiniFunction> {
	const mapa = recopilarFunciones(document);
	for (const modulo of modulosImportados(document)) {
		for (const funcion of modulo.declaraciones) {
			if (!mapa.has(funcion.name)) {
				mapa.set(funcion.name, funcion);
			}
		}
		for (const e of modulo.externas) {
			if (!mapa.has(e.name)) {
				mapa.set(e.name, externaComoFuncion(e, 'modulo'));
			}
		}
	}
	return mapa;
}

function funcionesDisponibles(document: vscode.TextDocument): WiniFunction[] {
	return [...mapaFuncionesDisponibles(document).values()];
}

/** Estructuras del documento + módulos importados (o todas, en modo `siempre`). */
function estructurasDisponibles(document: vscode.TextDocument): WiniEstructura[] {
	if (modoSugerencias() === 'siempre') {
		return [...recopilarEstructuras(document).values()];
	}
	const mapa = new Map<string, WiniEstructura>();
	try {
		for (const e of parseWiniEstructuras(document.getText(), path.basename(document.uri.fsPath), 'documento', document.uri.fsPath)) {
			mapa.set(e.name, e);
		}
	} catch (err) {
		console.error('Wini: error analizando estructuras del documento', err);
	}
	for (const m of modulosImportados(document)) {
		for (const e of m.estructuras) {
			if (!mapa.has(e.name)) {
				mapa.set(e.name, e);
			}
		}
	}
	return [...mapa.values()];
}

// Ruta relativa al documento actual, lista para insertar entre comillas.
export function rutaImportable(document: vscode.TextDocument, archivo: string): string {
	const base = path.dirname(document.uri.fsPath);
	let rel = path.relative(base, archivo);
	const ext = path.extname(rel);
	if (EXTENSIONES_MODULO.includes(ext.toLowerCase())) {
		rel = rel.slice(0, rel.length - ext.length);
	}
	return rel.split(path.sep).join('/');
}

// Referencia lista para insertar en un `importar`: si el módulo es del
// sistema (bajo una raíz global que winic también busca: WINI_PATH,
// share/wini, junto a winic), se usa su nombre corto (`system/sistem`,
// `regex/regex`) en vez de la ruta relativa al documento
// (`../../.local/share/...`), que compila igual pero es ilegible.
// Los del workspace siguen con ruta relativa al documento (siempre válida).
export function referenciaCortaModulo(document: vscode.TextDocument, m: WiniModulo): string {
	if (m.origen === 'sistema') {
		return m.nombre.split('.').join('/');
	}
	return rutaImportable(document, m.ruta);
}

// ─────────────────────────────────────────────────────────────
// Proveedor de sugerencias (autocompletado)
// ─────────────────────────────────────────────────────────────

function snippetDeFuncion(f: WiniFunction): vscode.SnippetString {
	if (f.params.length === 0) {
		return new vscode.SnippetString(`${f.name}($0)`);
	}
	const args = f.params.map((p, i) => `\${${i + 1}:${p.name}}`).join(', ');
	return new vscode.SnippetString(`${f.name}(${args})$0`);
}

export class SugerenciasWini implements vscode.CompletionItemProvider {
	provideCompletionItems(
		document: vscode.TextDocument,
		position: vscode.Position,
		_token?: vscode.CancellationToken,
		context?: vscode.CompletionContext
	): vscode.CompletionList {
		const items: vscode.CompletionItem[] = [];
		try {
			const wordRange = document.getWordRangeAtPosition(position, WORD_RE);
			const range = wordRange ? new vscode.Range(wordRange.start, position) : undefined;
			const importando = enContextoImportar(document, position);
			const rangoModulo = importando ? rangoImportar(document, position) : undefined;
			const importados = modulosImportados(document);
			const lineaAntes = document.lineAt(position.line).text.slice(0, position.character);
			const calificadorMatch = lineaAntes.match(/(?:^|[^\w.])([A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*)\.([\w\u00C0-\u017F]*)$/);
			const calificador = calificadorMatch?.[1];
			const moduloCalificado = calificador
				? importados.find(m => m.etiqueta === calificador || m.nombre === calificador)
				: undefined;

			// Disparo por carácter (`,", espacio…): con prefijo vacío
			// no se vuelca todo el catálogo; las sugerencias aparecen al
			// escribir (o con Ctrl+Espacio, que llega como Invoke).
			const porCaracter = context?.triggerKind === vscode.CompletionTriggerKind.TriggerCharacter;
			if (importando && !prefijoImportar(document, position) && porCaracter) {
				return new vscode.CompletionList([], false);
			}
			const enCapturarAqui = !importando && enContextoCapturar(document, position);
			const enLanzarAqui = !importando && enContextoLanzar(document, position);
			// NOTA: `capturar`/`lanzar` NO se suprimen con prefijo vacío:
			// son solo 9 excepciones y ocultarlas tras el espacio confunde
			// ("no me sale los nombres"). La supresión queda solo para
			// catálogos grandes (`importar`, `alias.`).

			// Rango que empieza DESPUÉS del punto (`mat.|f`): si el reemplazo
			// cubriera `mat` completo, elegir una sugerencia borraría el alias.
			const posPunto = calificador ? lineaAntes.lastIndexOf('.') : -1;
			const rangoPunto = posPunto >= 0
				? new vscode.Range(new vscode.Position(position.line, posPunto + 1), position)
				: range;

			// 1) Campos de estructura tras `variable.` (heurística por declaración).
			if (calificador && !moduloCalificado && !importando) {
				const est = estructuraDeVariable(document, calificador);
				if (est) {
					let i = 0;
					for (const c of est.fields) {
						const item = new vscode.CompletionItem(c.name, vscode.CompletionItemKind.Field);
						item.detail = `${c.name}: ${c.type} · ${est.name}`;
						item.documentation = new vscode.MarkdownString(`Campo de la estructura \`${est.name}\` (${est.file}:${est.line})`);
						item.sortText = '0' + String(i++).padStart(4, '0');
						if (rangoPunto) {
							item.range = rangoPunto;
						}
						items.push(item);
					}
					return new vscode.CompletionList(items, true);
				}
			}

			// 1b) `alias.` / `modulo.` de un módulo importado: SOLO lo de ese
			// módulo (funciones, estructuras y externas), nada global.
			if (moduloCalificado && !importando) {
				const solo: vscode.CompletionItem[] = [];
				const ponerRango = (item: vscode.CompletionItem) => {
					if (rangoPunto) {
						item.range = rangoPunto;
					}
				};
				let qi = 0;
				for (const f of moduloCalificado.declaraciones) {
					const item = new vscode.CompletionItem(f.name, vscode.CompletionItemKind.Function);
					item.detail = `${funcDetail(f)} · ${f.file}`;
					item.documentation = funcDoc(f);
					item.insertText = snippetDeFuncion(f);
					item.sortText = '0' + String(qi++).padStart(4, '0');
					marcarDeprecada(item, f);
					ponerRango(item);
					solo.push(item);
				}
				for (const e of moduloCalificado.estructuras) {
					const item = new vscode.CompletionItem(e.name, vscode.CompletionItemKind.Class);
					item.detail = `estructura ${e.name} · ${e.file}`;
					item.documentation = structDoc(e);
					item.insertText = new vscode.SnippetString(`${e.name}($0)`);
					item.sortText = '0' + String(qi++).padStart(4, '0');
					ponerRango(item);
					solo.push(item);
				}
				for (const ex of moduloCalificado.externas) {
					const item = new vscode.CompletionItem(ex.name, vscode.CompletionItemKind.Function);
					item.detail = `externa "${ex.simboloC}" · ${ex.file}`;
					item.documentation = externaDoc(ex);
					item.insertText = snippetDeFuncion({
						name: ex.name, params: ex.params, returnType: ex.returnType,
						file: ex.file, line: ex.line, description: ex.description, origin: 'modulo'
					});
					item.sortText = '0' + String(qi++).padStart(4, '0');
					ponerRango(item);
					solo.push(item);
				}
				return new vscode.CompletionList(solo, false);
			}

			// 2) Módulos (en `importar` con filtro por prefijo; fuera, nombre corto).
			const prefijo = importando ? prefijoImportar(document, position) : '';
			const enCapturar = enCapturarAqui;
			const enLanzar = enLanzarAqui;
			const enTipo = !importando && enContextoTipo(document, position);
			let mi = 0;
			for (const m of modulos.values()) {
				if (enCapturar || enLanzar || enTipo) {
					continue;
				}
				const referencias = referenciasModulo(document, m);
				if (importando && prefijo &&
					!referencias.some(referencia => referencia.toLowerCase().startsWith(prefijo.toLowerCase()))) {
					continue;
				}
				const item = new vscode.CompletionItem(m.nombre, vscode.CompletionItemKind.Module);
				item.detail = `Módulo Wini (${m.origen})`;
				const md = new vscode.MarkdownString();
				md.appendMarkdown(`**Módulo** · \`${m.ruta}\`\n\n`);
				md.appendMarkdown(`${m.funciones} función(es), ${m.estructuras.length} estructura(s), ${m.externas.length} externa(s)`);
				item.documentation = md;
				if (importando) {
					const ruta = referenciaCortaModulo(document, m);
					// Si la comilla de apertura ya está escrita (quedó fuera del
					// rango), no se repite; si el cursor ya tiene comilla de
					// cierre delante, tampoco se agrega otra.
					const tieneComilla = /importar[ \t]+"[^"]*$/.test(lineaAntes);
					const cierraYa = document.lineAt(position.line).text.slice(position.character).startsWith('"');
					item.insertText = (tieneComilla ? ruta : `"${ruta}`) + (cierraYa ? '' : '"');
					item.filterText = referencias.join(' ');
				} else {
					item.insertText = m.etiqueta;
				}
				item.sortText = (importando ? '0' : '5') + String(mi++).padStart(4, '0');
				const r = rangoModulo || range;
				if (r) {
					item.range = r;
				}
				items.push(item);
			}

			if (importando) {
				return new vscode.CompletionList(items, true);
			}

			// 3) Excepciones tras `capturar` y tras `lanzar`. Siempre se listan
			// (son 9): el editor filtra por lo escrito. filterText explícito
			// para que el filtrado no dependa de cómo se detectó la palabra.
			if (enCapturar || enLanzar) {
				let ei = 0;
				for (const [nombre, desc] of EXCEPCIONES) {
					const item = new vscode.CompletionItem(nombre, vscode.CompletionItemKind.Class);
					item.detail = desc;
					item.insertText = nombre;
					item.filterText = nombre;
					item.sortText = '0' + String(ei++).padStart(4, '0');
					if (range) {
						item.range = range;
					}
					items.push(item);
				}
				return new vscode.CompletionList(items, true);
			}

			// 4) Funciones peladas: SOLO builtins + documento (+ stdlib según
			// modo). Las de módulos importados NO se sugieren sin su alias:
			// se llaman como `alias.funcion` (sección 1b). Así no se mezcla
			// lo de todos los módulos en cada palabra que se escribe.
			// (Hover y ayuda de firma sí las resuelven peladas, porque el
			// compilador las acepta en el espacio global.)
			let n = 0;
			const funciones = [...recopilarFunciones(document).values()];
			for (const f of funciones) {
				const esBuiltin = (f.file === '(builtin)');
				const item = new vscode.CompletionItem(f.name, vscode.CompletionItemKind.Function);
				item.detail = esBuiltin && f.signature ? f.signature : `${funcDetail(f)} · ${f.file}`;
				item.documentation = funcDoc(f);
				item.insertText = snippetDeFuncion(f);
				item.sortText = (f.origin === 'stdlib' || f.origin === 'modulo' ? '1' : '2') + String(n++).padStart(4, '0');
				marcarDeprecada(item, f);
				if (range) {
					item.range = range;
				}
				items.push(item);
			}

			// 5) Estructuras (documento + importados; todas en modo `siempre`).
			const estructuras = estructurasDisponibles(document);
			let ei = 0;
			for (const e of estructuras) {
				const item = new vscode.CompletionItem(e.name, vscode.CompletionItemKind.Class);
				item.detail = `estructura ${e.name} · ${e.file}`;
				item.documentation = structDoc(e);
				item.insertText = new vscode.SnippetString(`${e.name}($0)`);
				item.sortText = '2' + String(ei++).padStart(4, '0');
				if (range) {
					item.range = range;
				}
				items.push(item);
			}

			// 6) Tipos (en contexto de tipo van primero).
			let ti = 0;
			for (const [nombre, desc] of TIPOS) {
				const item = new vscode.CompletionItem(nombre, vscode.CompletionItemKind.TypeParameter);
				item.detail = desc;
				item.insertText = nombre;
				item.sortText = (enTipo ? '0' : '4') + String(ti++).padStart(4, '0');
				if (range) {
					item.range = range;
				}
				items.push(item);
			}

			// 7) Palabras reservadas.
			let k = 0;
			for (const [palabra, desc] of KEYWORDS) {
				const item = new vscode.CompletionItem(palabra, vscode.CompletionItemKind.Keyword);
				item.detail = desc;
				item.insertText = palabra;
				item.sortText = '3' + String(k++).padStart(4, '0');
				if (range) {
					item.range = range;
				}
				items.push(item);
			}

		} catch (err) {
			console.error('Wini: error generando sugerencias', err);
		}
		return new vscode.CompletionList(items, true);
	}
}

// ─────────────────────────────────────────────────────────────
// Hover: documentación de funciones, módulos, estructuras y builtins
// ─────────────────────────────────────────────────────────────

function hoverDePalabra(document: vscode.TextDocument, nombre: string, r: vscode.Range): vscode.Hover | undefined {
	// Palabra reservada / tipo / excepción / builtin.
	const kw = KEYWORDS.find(([p]) => p === nombre);
	if (kw) {
		const md = new vscode.MarkdownString();
		md.appendMarkdown(`**Palabra reservada** \`${nombre}\`\n\n${kw[1]}`);
		return new vscode.Hover(md, r);
	}
	const tp = TIPOS.find(([p]) => p === nombre);
	if (tp) {
		const md = new vscode.MarkdownString();
		md.appendMarkdown(`**Tipo** \`${nombre}\`\n\n${tp[1]}`);
		return new vscode.Hover(md, r);
	}
	const ex = EXCEPCIONES.find(([p]) => p === nombre);
	if (ex) {
		const md = new vscode.MarkdownString();
		md.appendMarkdown(`**Excepción** \`${nombre}\`\n\n${ex[1]}`);
		return new vscode.Hover(md, r);
	}
	const b = BUILTINS.find(x => x.name === nombre);
	if (b) {
		const md = new vscode.MarkdownString();
		md.appendMarkdown(`**Builtin** · \`${b.detail}\`\n\n${b.doc}`);
		return new vscode.Hover(md, r);
	}
	const est = recopilarEstructuras(document).get(nombre);
	if (est) {
		return new vscode.Hover(structDoc(est), r);
	}
	return undefined;
}

class HoverWini implements vscode.HoverProvider {
	provideHover(document: vscode.TextDocument, position: vscode.Position): vscode.Hover | undefined {
		const r = document.getWordRangeAtPosition(position, WORD_RE);
		if (!r) {
			return undefined;
		}
		const nombre = document.getText(r);
		const linea = document.lineAt(position.line).text;
		const calificado = linea.match(
			new RegExp(`([A-Za-z_\\u00C0-\\u017F][\\w\\u00C0-\\u017F]*)\\.${nombre}\\b`)
		);
		if (calificado) {
			// a) `modulo.funcion`
			const modulo = modulosImportados(document).find(m =>
				m.etiqueta === calificado[1] || m.nombre === calificado[1]
			);
			const funcion = modulo?.declaraciones.find(f => f.name === nombre);
			if (funcion) {
				return new vscode.Hover(funcDoc(funcion), r);
			}
			const estMod = modulo?.estructuras.find(e => e.name === nombre);
			if (estMod) {
				return new vscode.Hover(structDoc(estMod), r);
			}
			// b) `variable.campo`
			const est = estructuraDeVariable(document, calificado[1]);
			if (est) {
				const campo = est.fields.find(c => c.name === nombre);
				if (campo) {
					const md = new vscode.MarkdownString();
					md.appendMarkdown(`**Campo** \`${est.name}.${campo.name}: ${campo.type}\` · \`${est.file}:${est.line}\``);
					return new vscode.Hover(md, r);
				}
			}
		}

		const modulo = modulosImportados(document).find(m =>
			m.etiqueta === nombre || m.nombre === nombre
		) || modulos.get(nombre);
		if (modulo) {
			const md = new vscode.MarkdownString();
			md.appendMarkdown(`**Módulo Wini** · \`${modulo.ruta}\`\n\n`);
			md.appendMarkdown(`Origen: ${modulo.origen} · ${modulo.funciones} función(es), ${modulo.estructuras.length} estructura(s)\n\n`);
			md.appendMarkdown(`Para importarlo: \`importar "${referenciaCortaModulo(document, modulo)}"\``);
			return new vscode.Hover(md, r);
		}

		// Externas del documento o de módulos importados.
		for (const m of modulosImportados(document)) {
			const e = m.externas.find(x => x.name === nombre);
			if (e) {
				return new vscode.Hover(externaDoc(e), r);
			}
		}
		try {
			for (const e of parseWiniExternas(document.getText(), '', 'documento')) {
				if (e.name === nombre) {
					return new vscode.Hover(externaDoc(e), r);
				}
			}
		} catch { /* ignorar */ }

		const f = mapaFuncionesDisponibles(document).get(nombre);
		if (f && f.file !== '(builtin)') {
			return new vscode.Hover(funcDoc(f), r);
		}
		return hoverDePalabra(document, nombre, r) || (f ? new vscode.Hover(funcDoc(f), r) : undefined);
	}
}

// ─────────────────────────────────────────────────────────────
// Firma de función: ayuda con parámetros al escribir "("
// ─────────────────────────────────────────────────────────────

class FirmaWini implements vscode.SignatureHelpProvider {
	provideSignatureHelp(
		document: vscode.TextDocument,
		position: vscode.Position
	): vscode.SignatureHelp | undefined {
		const text = document.getText(new vscode.Range(new vscode.Position(0, 0), position));
		let depth = 0;
		let openIdx = -1;
		let commas = 0;
		let enStr: string | undefined;
		for (let i = text.length - 1; i >= 0; i--) {
			const ch = text[i];
			if (enStr) {
				if (ch === enStr && text[i - 1] !== '\\') {
					enStr = undefined;
				}
				continue;
			}
			if (ch === '"' || ch === "'") {
				enStr = ch;
				continue;
			}
			if (ch === ')') {
				depth++;
			} else if (ch === '(') {
				if (depth === 0) {
					openIdx = i;
					break;
				}
				depth--;
			} else if (ch === ',' && depth === 0) {
				commas++;
			} else if ((ch === '\n' || ch === ':') && depth > 0) {
				// Salida de seguridad: no buscar más allá de la sentencia.
				break;
			}
		}
		if (openIdx < 0) {
			return undefined;
		}
		const m = text.slice(0, openIdx).match(/([A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*)\s*$/);
		if (!m) {
			return undefined;
		}
		const f = mapaFuncionesDisponibles(document).get(m[1]);
		if (!f) {
			return undefined;
		}
		const b = BUILTINS.find(x => x.name === m[1]);
		const detalle = f.file === '(builtin)' && b ? b.detail : funcDetail(f);
		const sig = new vscode.SignatureInformation(detalle, funcDoc(f));
		if (f.params.length > 0) {
			sig.parameters = f.params.map(p => new vscode.ParameterInformation(
				`${p.variadic ? '*' : ''}${p.name}: ${p.type}${p.defaultValue !== undefined ? ' = ' + p.defaultValue : ''}`
			));
			const help = new vscode.SignatureHelp();
			help.signatures = [sig];
			help.activeSignature = 0;
			help.activeParameter = Math.min(commas, f.params.length - 1);
			return help;
		}
		if (b) {
			const help = new vscode.SignatureHelp();
			help.signatures = [sig];
			help.activeSignature = 0;
			help.activeParameter = 0;
			return help;
		}
		return undefined;
	}
}

// ─────────────────────────────────────────────────────────────
// Símbolos del documento (vista de esquema / outline)
// ─────────────────────────────────────────────────────────────

class SimbolosWini implements vscode.DocumentSymbolProvider {
	provideDocumentSymbols(document: vscode.TextDocument): vscode.DocumentSymbol[] {
		const simbolos: vscode.DocumentSymbol[] = [];
		const texto = document.getText();
		const lineas = texto.split(/\r?\n/);
		// Importaciones primero.
		const reImport = /^\s*importar\s+(?:"([^"]+)"|([^\s#]+))(?:\s+como\s+([A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*))?/;
		lineas.forEach((l, i) => {
			const m = l.match(reImport);
			if (m) {
				const nombre = m[3] || m[1] || m[2];
				const rango = new vscode.Range(i, 0, i, l.length);
				simbolos.push(new vscode.DocumentSymbol(
					nombre, m[1] || m[2] || '', vscode.SymbolKind.Module, rango, rango
				));
			}
		});
		for (const e of parseWiniEstructuras(texto, '', 'documento')) {
			const rango = new vscode.Range(e.line - 1, 0, e.line - 1, lineas[e.line - 1]?.length || 0);
			const sym = new vscode.DocumentSymbol(e.name, 'estructura', vscode.SymbolKind.Class, rango, rango);
			for (const c of e.fields) {
				const rc = new vscode.Range(c.line - 1, 0, c.line - 1, lineas[c.line - 1]?.length || 0);
				sym.children.push(new vscode.DocumentSymbol(c.name, c.type, vscode.SymbolKind.Field, rc, rc));
			}
			simbolos.push(sym);
		}
		for (const f of parseWiniFunctions(texto, '', 'documento')) {
			const rango = new vscode.Range(f.line - 1, 0, f.line - 1, lineas[f.line - 1]?.length || 0);
			simbolos.push(new vscode.DocumentSymbol(
				f.name, funcDetail(f), vscode.SymbolKind.Function, rango, rango
			));
		}
		for (const e of parseWiniExternas(texto, '', 'documento')) {
			const rango = new vscode.Range(e.line - 1, 0, e.line - 1, lineas[e.line - 1]?.length || 0);
			simbolos.push(new vscode.DocumentSymbol(
				e.name, `externa "${e.simboloC}"`, vscode.SymbolKind.Function, rango, rango
			));
		}
		return simbolos;
	}
}

// ─────────────────────────────────────────────────────────────
// Ir a definición (funciones, estructuras, módulos)
// ─────────────────────────────────────────────────────────────

class DefinicionWini implements vscode.DefinitionProvider {
	provideDefinition(document: vscode.TextDocument, position: vscode.Position): vscode.Definition | undefined {
		const r = document.getWordRangeAtPosition(position, WORD_RE);
		if (!r) {
			return undefined;
		}
		const nombre = document.getText(r);
		const linea = document.lineAt(position.line).text;

		// 1) Dentro de un `importar "..."`: ir al archivo del módulo.
		if (/(^|\s)importar\b/.test(linea)) {
			const mImp = linea.match(/importar\s+(?:"([^"]+)"|([^\s#]+))/);
			if (mImp) {
				const mod = resolverModuloDeReferencia(document, mImp[1] || mImp[2]);
				if (mod) {
					return new vscode.Location(vscode.Uri.file(mod.ruta), new vscode.Position(0, 0));
				}
			}
		}

		// 2) `modulo.nombre`: definición dentro del archivo del módulo.
		const calificado = linea.match(new RegExp(`([A-Za-z_\\u00C0-\\u017F][\\w\\u00C0-\\u017F]*)\\.${nombre}\\b`));
		if (calificado) {
			const mod = modulosImportados(document).find(m =>
				m.etiqueta === calificado[1] || m.nombre === calificado[1]
			);
			if (mod) {
				const f = mod.declaraciones.find(x => x.name === nombre);
				if (f?.rutaAbs) {
					return new vscode.Location(vscode.Uri.file(f.rutaAbs), new vscode.Position(Math.max(0, f.line - 1), 0));
				}
				const e = mod.estructuras.find(x => x.name === nombre);
				if (e?.rutaAbs) {
					return new vscode.Location(vscode.Uri.file(e.rutaAbs), new vscode.Position(Math.max(0, e.line - 1), 0));
				}
				return new vscode.Location(vscode.Uri.file(mod.ruta), new vscode.Position(0, 0));
			}
		}

		// 3) Módulo importado por su alias/nombre.
		const mod = modulosImportados(document).find(m => m.etiqueta === nombre || m.nombre === nombre)
			|| modulos.get(nombre);
		if (mod && !calificado) {
			// Solo si la palabra es realmente un módulo (no una función del documento).
			const esFuncion = parseWiniFunctions(document.getText(), '', 'documento').some(f => f.name === nombre);
			if (!esFuncion) {
				return new vscode.Location(vscode.Uri.file(mod.ruta), new vscode.Position(0, 0));
			}
		}

		// 4) Función/estructura del documento actual.
		const texto = document.getText();
		for (const f of parseWiniFunctions(texto, '', 'documento')) {
			if (f.name === nombre) {
				return new vscode.Location(document.uri, new vscode.Position(f.line - 1, 0));
			}
		}
		for (const e of parseWiniEstructuras(texto, '', 'documento')) {
			if (e.name === nombre) {
				return new vscode.Location(document.uri, new vscode.Position(e.line - 1, 0));
			}
		}
		for (const e of parseWiniExternas(texto, '', 'documento')) {
			if (e.name === nombre) {
				return new vscode.Location(document.uri, new vscode.Position(e.line - 1, 0));
			}
		}

		// 5) Función de un módulo importado.
		for (const m of modulosImportados(document)) {
			const f = m.declaraciones.find(x => x.name === nombre);
			if (f?.rutaAbs) {
				return new vscode.Location(vscode.Uri.file(f.rutaAbs), new vscode.Position(Math.max(0, f.line - 1), 0));
			}
		}
		return undefined;
	}
}

// ─────────────────────────────────────────────────────────────
// Plegado de bloques (indentación: la línea dedentada cierra)
// ─────────────────────────────────────────────────────────────

class PlegadoWini implements vscode.FoldingRangeProvider {
	provideFoldingRanges(document: vscode.TextDocument): vscode.FoldingRange[] {
		const rangos: vscode.FoldingRange[] = [];
		const n = document.lineCount;
		const pila: Array<{ indent: number; inicio: number }> = [];
		for (let i = 0; i < n; i++) {
			const texto = document.lineAt(i).text;
			if (texto.trim() === '' || texto.trim().startsWith('#')) {
				continue;
			}
			const indent = texto.search(/[^ \t]/);
			const abre = /:\s*(#.*)?$/.test(texto.trimEnd());
			while (pila.length > 0 && indent <= pila[pila.length - 1].indent) {
				const b = pila.pop()!;
				if (b.inicio < i - 1) {
					rangos.push(new vscode.FoldingRange(b.inicio, i - 1));
				}
			}
			if (abre) {
				pila.push({ indent, inicio: i });
			}
		}
		while (pila.length > 0) {
			const b = pila.pop()!;
			if (b.inicio < n - 1) {
				rangos.push(new vscode.FoldingRange(b.inicio, n - 1));
			}
		}
		return rangos;
	}
}

// ─────────────────────────────────────────────────────────────
// Tokens semánticos (módulos, funciones, tipos, parámetros, variables)
// ─────────────────────────────────────────────────────────────

const TOKEN_TYPES = ['namespace', 'function', 'type', 'parameter', 'variable'];
const TOKEN_MODIFIERS: string[] = [];
const LEYENDA_MODULOS = new vscode.SemanticTokensLegend(TOKEN_TYPES, TOKEN_MODIFIERS);

/** ¿La columna `col` (0-based) de `linea` está dentro de un string o comentario? */
export function enCodigo(linea: string, col: number): boolean {
	let enStr: string | undefined;
	for (let i = 0; i < col && i < linea.length; i++) {
		const ch = linea[i];
		if (enStr) {
			if (ch === '\\') { i++; continue; }
			if (ch === enStr) { enStr = undefined; }
			continue;
		}
		if (ch === '#') { return false; }
		if (ch === '"' || ch === "'") { enStr = ch; }
	}
	return enStr === undefined;
}

function escapeRegExp(s: string): string {
	return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** Palabra completa `palabra` (fuera de strings y comentarios) como rangos. */
export function ocurrenciasDePalabra(document: vscode.TextDocument, palabra: string): vscode.Range[] {
	const rangos: vscode.Range[] = [];
	const re = new RegExp(`(?<![\\w\\u00C0-\\u017F])${escapeRegExp(palabra)}(?![\\w\\u00C0-\\u017F])`, 'g');
	for (let i = 0; i < document.lineCount; i++) {
		const linea = document.lineAt(i).text;
		re.lastIndex = 0;
		let m: RegExpExecArray | null;
		while ((m = re.exec(linea)) !== null) {
			if (enCodigo(linea, m.index)) {
				rangos.push(new vscode.Range(i, m.index, i, m.index + palabra.length));
			}
		}
	}
	return rangos;
}

export class ModulosSemanticTokens implements vscode.DocumentSemanticTokensProvider {
	provideDocumentSemanticTokens(document: vscode.TextDocument): vscode.SemanticTokens {
		const builder = new vscode.SemanticTokensBuilder(LEYENDA_MODULOS);
		const nombresModulo = new Set<string>();
		for (const m of modulos.values()) {
			nombresModulo.add(m.nombre);
			nombresModulo.add(m.etiqueta);
		}
		const funciones = new Set(recopilarFunciones(document).keys());
		const estructuras = new Set(recopilarEstructuras(document).keys());
		const tipos = new Set([...TIPOS.map(([t]) => t), ...EXCEPCIONES.map(([t]) => t)]);
		const lineas = document.getText().split(/\r?\n/);
		const reToken = /[A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*(?:\.[A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*)*/g;
		const reFuncionDef = /^[ \t]*funcion[ \t]+[A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*[ \t]*\(([^)]*)\)/;
		const reDeclaracion = /^[ \t]*(entero|decimal|cadena|booleano|vacio|lista<[^>]*>|diccionario<[^>]*>|[A-ZÁÉÍÓÚÑ][\w\u00C0-\u017F]*)[ \t]+([A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*)/;
		const rePara = /^[ \t]*para[ \t]+([A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*)[ \t]+en\b/;
		for (let num = 0; num < lineas.length; num++) {
			const linea = lineas[num];
			const entradas: Array<{ pos: number; len: number; tipo: number }> = [];
			// identificadores conocidos (módulos, funciones, tipos)
			reToken.lastIndex = 0;
			let match: RegExpExecArray | null;
			while ((match = reToken.exec(linea)) !== null) {
				if (!enCodigo(linea, match.index)) {
					continue;
				}
				const token = match[0];
				const cabeza = token.split('.')[0];
				if (nombresModulo.has(token)) {
					entradas.push({ pos: match.index, len: token.length, tipo: 0 });
				} else if (nombresModulo.has(cabeza)) {
					entradas.push({ pos: match.index, len: cabeza.length, tipo: 0 });
				} else if (funciones.has(token)) {
					entradas.push({ pos: match.index, len: token.length, tipo: 1 });
				} else if (estructuras.has(token) || tipos.has(token)) {
					entradas.push({ pos: match.index, len: token.length, tipo: 2 });
				}
			}
			// parámetros de la firma (en la misma línea de la declaración)
			const md = linea.match(reFuncionDef);
			if (md) {
				let cursor = linea.indexOf('(') + 1;
				const raw = md[1];
				if (raw.trim().length > 0) {
					for (const pieza of partirParametros(raw)) {
						const p = pieza.trim();
						if (p.length > 0) {
							const info = partirParametro(p);
							const dentro = p.indexOf(info.name);
							if (info.name && dentro >= 0) {
								entradas.push({ pos: cursor + dentro, len: info.name.length, tipo: 3 });
							}
						}
						cursor += pieza.length + 1;
					}
				}
			}
			// variables declaradas con tipo y variables de `para`
			const mv = linea.match(reDeclaracion);
			if (mv && !/^[ \t]*funcion\b/.test(linea)) {
				entradas.push({ pos: mv[0].length - mv[2].length, len: mv[2].length, tipo: 4 });
			}
			const mp = linea.match(rePara);
			if (mp) {
				entradas.push({ pos: mp[0].length - mp[1].length, len: mp[1].length, tipo: 4 });
			}
			entradas.sort((a, b) => a.pos - b.pos);
			for (const e of entradas) {
				builder.push(num, e.pos, e.len, e.tipo, 0);
			}
		}
		return builder.build();
	}
}

// ─────────────────────────────────────────────────────────────
// Referencias (Find All References / Peek), destacado y renombrar
// ─────────────────────────────────────────────────────────────

function esPalabraReservadaONucleo(p: string): boolean {
	return KEYWORDS.some(([x]) => x === p) || TIPOS.some(([x]) => x === p) ||
		EXCEPCIONES.some(([x]) => x === p) || BUILTINS.some(b => b.name === p);
}

function esLineaDeclaracion(document: vscode.TextDocument, linea: number, palabra: string): boolean {
	const t = document.lineAt(linea).text;
	return new RegExp(`^[ \\t]*(?:funcion[ \\t]+${escapeRegExp(palabra)}\\b|estructura[ \\t]+${escapeRegExp(palabra)}\\b|externa\\b[^\\n]*\\bcomo[ \\t]+${escapeRegExp(palabra)}\\b)`).test(t);
}

export class ReferenciasWini implements vscode.ReferenceProvider {
	async provideReferences(
		document: vscode.TextDocument,
		position: vscode.Position,
		context: vscode.ReferenceContext
	): Promise<vscode.Location[]> {
		const r = document.getWordRangeAtPosition(position, WORD_RE);
		if (!r) {
			return [];
		}
		const palabra = document.getText(r);
		if (esPalabraReservadaONucleo(palabra)) {
			return [];
		}
		const locs: vscode.Location[] = [];
		for (const rg of ocurrenciasDePalabra(document, palabra)) {
			if (!context.includeDeclaration && esLineaDeclaracion(document, rg.start.line, palabra)) {
				continue;
			}
			locs.push(new vscode.Location(document.uri, rg));
		}
		// Resto de archivos Wini del workspace (incluye los módulos del proyecto).
		const vistos = new Set<string>([document.uri.fsPath]);
		const re = new RegExp(`(?<![\\w\\u00C0-\\u017F])${escapeRegExp(palabra)}(?![\\w\\u00C0-\\u017F])`, 'g');
		for (const carpeta of vscode.workspace.workspaceFolders || []) {
			if (locs.length > 200) {
				break;
			}
			let uris: vscode.Uri[] = [];
			try {
				uris = await vscode.workspace.findFiles(
					new vscode.RelativePattern(carpeta, '**/*.{wn,wini,cwn}'),
					EXCLUIR_BUSQUEDA,
					200
				);
			} catch {
				continue;
			}
			for (const uri of uris) {
				if (locs.length > 200 || vistos.has(uri.fsPath)) {
					continue;
				}
				vistos.add(uri.fsPath);
				let texto: string;
				try {
					texto = Buffer.from(await vscode.workspace.fs.readFile(uri)).toString('utf8');
				} catch {
					continue;
				}
				const lineas = texto.split(/\r?\n/);
				for (let i = 0; i < lineas.length; i++) {
					re.lastIndex = 0;
					let m: RegExpExecArray | null;
					while ((m = re.exec(lineas[i])) !== null) {
						if (enCodigo(lineas[i], m.index)) {
							locs.push(new vscode.Location(uri, new vscode.Range(i, m.index, i, m.index + palabra.length)));
						}
					}
				}
			}
		}
		return locs;
	}
}

export class DestacadoWini implements vscode.DocumentHighlightProvider {
	provideDocumentHighlights(document: vscode.TextDocument, position: vscode.Position): vscode.DocumentHighlight[] {
		const r = document.getWordRangeAtPosition(position, WORD_RE);
		if (!r) {
			return [];
		}
		const palabra = document.getText(r);
		if (esPalabraReservadaONucleo(palabra)) {
			return [];
		}
		return ocurrenciasDePalabra(document, palabra)
			.map(rg => new vscode.DocumentHighlight(rg, vscode.DocumentHighlightKind.Text));
	}
}

/** ¿El símbolo está definido en ESTE archivo? (lo único que se renombra) */
function esSimboloRenombrable(document: vscode.TextDocument, palabra: string): boolean {
	const texto = document.getText();
	const p = escapeRegExp(palabra);
	const reDecl = new RegExp(
		`^[ \\t]*(?:funcion[ \\t]+${p}\\b|estructura[ \\t]+${p}\\b|externa\\b[^\\n]*\\bcomo[ \\t]+${p}\\b)`, 'm'
	);
	if (reDecl.test(texto)) {
		return true;
	}
	const reAlias = new RegExp(`^[ \\t]*importar\\b[^\\n]*\\bcomo[ \\t]+${p}\\b`, 'm');
	if (reAlias.test(texto)) {
		return true;
	}
	// Variable declarada con tipo (entero x, lista<entero> x, MiStruct x…)
	const reVar = new RegExp(
		`^[ \\t]*(?:entero|decimal|cadena|booleano|vacio|lista<[^>]*>|diccionario<[^>]*>|[A-ZÁÉÍÓÚÑ][\\w\\u00C0-\\u017F]*)[ \\t]+${p}[ \\t]*(?:=|$)`, 'm'
	);
	if (reVar.test(texto)) {
		return true;
	}
	// Parámetro de alguna firma del documento
	return new RegExp(`\\b${p}[ \\t]*:[ \\t]*(?:entero|decimal|cadena|booleano|lista<|diccionario<|[A-ZÁÉÍÓÚÑ])`).test(texto);
}

export class RenombrarWini implements vscode.RenameProvider {
	prepareRename(document: vscode.TextDocument, position: vscode.Position): { range: vscode.Range; placeholder: string } {
		const r = document.getWordRangeAtPosition(position, WORD_RE);
		if (!r) {
			throw new Error('Wini: no hay ningún símbolo bajo el cursor.');
		}
		const palabra = document.getText(r);
		if (esPalabraReservadaONucleo(palabra)) {
			throw new Error(`Wini: '${palabra}' es una palabra reservada o builtin; no se puede renombrar.`);
		}
		if (!esSimboloRenombrable(document, palabra)) {
			throw new Error('Wini: solo se renombran símbolos definidos en este archivo (funciones, estructuras, externas, alias de importar, variables y parámetros).');
		}
		return { range: r, placeholder: palabra };
	}

	provideRenameEdits(document: vscode.TextDocument, position: vscode.Position, newName: string): vscode.WorkspaceEdit {
		const r = document.getWordRangeAtPosition(position, WORD_RE);
		if (!r) {
			return new vscode.WorkspaceEdit();
		}
		const viejo = document.getText(r);
		if (!/^[A-Za-z_\u00C0-\u017F][\w\u00C0-\u017F]*$/.test(newName)) {
			throw new Error(`Wini: '${newName}' no es un identificador válido.`);
		}
		if (!esSimboloRenombrable(document, viejo)) {
			throw new Error(`Wini: '${viejo}' no se puede renombrar.`);
		}
		const edit = new vscode.WorkspaceEdit();
		for (const rg of ocurrenciasDePalabra(document, viejo)) {
			edit.replace(document.uri, rg, newName);
		}
		return edit;
	}
}

// ─────────────────────────────────────────────────────────────
// Símbolos del espacio de trabajo (Ctrl+T)
// ─────────────────────────────────────────────────────────────

export class SimbolosEspacioWini implements vscode.WorkspaceSymbolProvider {
	async provideWorkspaceSymbols(query: string): Promise<vscode.SymbolInformation[]> {
		const out: vscode.SymbolInformation[] = [];
		const q = query.trim().toLowerCase();
		if (!q) {
			return out;
		}
		const agregar = (nombre: string, contenedor: string, kind: vscode.SymbolKind, ruta: string, linea: number): void => {
			if (out.length >= 100 || !nombre.toLowerCase().includes(q)) {
				return;
			}
			out.push(new vscode.SymbolInformation(
				nombre, kind, contenedor,
				new vscode.Location(vscode.Uri.file(ruta), new vscode.Position(Math.max(0, linea - 1), 0))
			));
		};
		// Módulos descubiertos (workspace + sistema).
		for (const m of modulos.values()) {
			for (const f of m.declaraciones) {
				agregar(f.name, m.etiqueta, vscode.SymbolKind.Function, m.ruta, f.line);
			}
			for (const e of m.estructuras) {
				agregar(e.name, m.etiqueta, vscode.SymbolKind.Class, m.ruta, e.line);
			}
			for (const x of m.externas) {
				agregar(x.name, m.etiqueta, vscode.SymbolKind.Function, m.ruta, x.line);
			}
		}
		// Documentos abiertos (por si el workspace cambió y aún no se reescaneó).
		for (const doc of vscode.workspace.textDocuments) {
			if (doc.languageId !== 'wini' || doc.isUntitled || out.length >= 100) {
				continue;
			}
			const base = path.basename(doc.uri.fsPath);
			for (const f of parseWiniFunctions(doc.getText(), base, 'documento', doc.uri.fsPath)) {
				if (f.name.toLowerCase().includes(q)) {
					out.push(new vscode.SymbolInformation(
						f.name, vscode.SymbolKind.Function, base,
						new vscode.Location(doc.uri, new vscode.Position(Math.max(0, f.line - 1), 0))
					));
				}
			}
		}
		return out;
	}
}

// ─────────────────────────────────────────────────────────────
// Acciones de código: auto-import estilo Pylance
// ─────────────────────────────────────────────────────────────

/** Posición donde insertar un nuevo `importar`: al final de los que ya hay. */
function lineaDeInsercionImportar(document: vscode.TextDocument): vscode.Position {
	const re = /^[ \t]*importar\b/;
	let ultima = -1;
	for (let i = 0; i < document.lineCount; i++) {
		if (re.test(document.lineAt(i).text)) {
			ultima = i;
		}
	}
	if (ultima >= 0) {
		return new vscode.Position(ultima, document.lineAt(ultima).text.length);
	}
	// Sin imports: al principio, tras cabeceras de comentario (estilo Python).
	let inicio = 0;
	for (let i = 0; i < document.lineCount; i++) {
		const t = document.lineAt(i).text.trim();
		if (t.startsWith('#') || t === '') {
			inicio = i + 1;
			continue;
		}
		break;
	}
	return new vscode.Position(inicio, 0);
}

export class AccionesWini implements vscode.CodeActionProvider {
	static readonly metadata: vscode.CodeActionProviderMetadata = {
		documentation: [{
			kind: vscode.CodeActionKind.QuickFix,
			command: { title: 'Wini: recargar módulos', command: 'wini.recargarStdlib' }
		}]
	};

	provideCodeActions(document: vscode.TextDocument, range: vscode.Range): vscode.CodeAction[] {
		const acciones: vscode.CodeAction[] = [];
		try {
			const palabraRango = document.getWordRangeAtPosition(range.start, WORD_RE);
			if (!palabraRango) {
				return acciones;
			}
			const palabra = document.getText(palabraRango);
			if (esPalabraReservadaONucleo(palabra)) {
				return acciones;
			}
			// Si ya resuelve (documento, builtins, importados), no hay nada que arreglar.
			if (mapaFuncionesDisponibles(document).has(palabra)) {
				return acciones;
			}
			if (new Set(estructurasDisponibles(document).map(e => e.name)).has(palabra)) {
				return acciones;
			}
			const importados = modulosImportados(document);
			let candidatos = 0;
			for (const m of modulos.values()) {
				if (candidatos >= 3) {
					break;
				}
				const laTiene = m.declaraciones.some(f => f.name === palabra) ||
					m.estructuras.some(e => e.name === palabra) ||
					m.externas.some(x => x.name === palabra);
				if (!laTiene || importados.some(x => x.nombre === m.nombre)) {
					continue;
				}
				candidatos++;
				const ref = referenciaCortaModulo(document, m);
				const accion = new vscode.CodeAction(
					`Importar '${palabra}' de ${m.etiqueta}`,
					vscode.CodeActionKind.QuickFix
				);
				accion.isPreferred = true;
				const pos = lineaDeInsercionImportar(document);
				const enLineaExistente = /^[ \t]*importar\b/.test(document.lineAt(pos.line).text);
				const edit = new vscode.WorkspaceEdit();
				edit.insert(
					document.uri,
					pos,
					enLineaExistente ? `\nimportar "${ref}" como ${m.etiqueta}` : `importar "${ref}" como ${m.etiqueta}\n`
				);
				accion.edit = edit;
				acciones.push(accion);
			}
		} catch (err) {
			console.error('Wini: error generando acciones de código', err);
		}
		return acciones;
	}
}

// ─────────────────────────────────────────────────────────────
// Formateador (indentación estructural, estilo Black)
// ─────────────────────────────────────────────────────────────

/** ¿La línea útil (sin comentario final) termina en ':'? */
function terminaConDosPuntos(linea: string): boolean {
	let enStr: string | undefined;
	let util = '';
	for (let i = 0; i < linea.length; i++) {
		const ch = linea[i];
		if (enStr) {
			if (ch === '\\') { i++; continue; }
			if (ch === enStr) { enStr = undefined; }
			continue;
		}
		if (ch === '#') {
			break;
		}
		if (ch === '"' || ch === "'") {
			enStr = ch;
			continue;
		}
		util += ch;
	}
	return util.trimEnd().endsWith(':');
}

/** Balance de paréntesis/corchetes/llaves de la línea (fuera de strings y comentarios). */
function balanceLinea(linea: string): number {
	let d = 0;
	let enStr: string | undefined;
	for (let i = 0; i < linea.length; i++) {
		const ch = linea[i];
		if (enStr) {
			if (ch === '\\') { i++; continue; }
			if (ch === enStr) { enStr = undefined; }
			continue;
		}
		if (ch === '#') {
			break;
		}
		if (ch === '"' || ch === "'") {
			enStr = ch;
			continue;
		}
		if (ch === '(' || ch === '[' || ch === '{') {
			d++;
		} else if (ch === ')' || ch === ']' || ch === '}') {
			d--;
		}
	}
	return d;
}

export class FormateadorWini implements vscode.DocumentFormattingEditProvider {
	provideDocumentFormattingEdits(document: vscode.TextDocument): vscode.TextEdit[] {
		const tam = Math.max(1, cfg<number>('format.indentSize', 4));
		const reindentar = cfg<boolean>('format.reindent', true);
		const recortarFinal = cfg<boolean>('format.trimTrailing', true);
		const tabsAEspacios = cfg<boolean>('format.tabsToSpaces', true);

		const lineas: string[] = [];
		let d = 0;
		// Pila de bloques abiertos: `nivel` es el nivel de indentación de
		// salida (entero); `origOpener` la indentación original de la línea
		// que abrió el bloque. Wini cierra los bloques por indentación
		// (no hay `fin`): una línea cuya indentación original es menor o
		// igual que la del apertor cierra el bloque.
		const pila: Array<{ nivel: number; origOpener: number }> =
			[{ nivel: 0, origOpener: -1 }];
		for (let i = 0; i < document.lineCount; i++) {
			const original = document.lineAt(i).text;
			let linea = tabsAEspacios ? original.replace(/\t/g, ' '.repeat(tam)) : original;
			if (recortarFinal) {
				linea = linea.replace(/[ \t]+$/, '');
			}
			const dAntes = d;
			d += balanceLinea(linea);
			if (dAntes > 0) {
				// Línea de continuación (llamada multilínea…): se respeta tal cual.
				lineas.push(linea);
				continue;
			}
			const recortada = linea.trim();
			if (recortada === '') {
				lineas.push('');
				continue;
			}
			if (!reindentar) {
				lineas.push(linea);
				continue;
			}
			const indentOriginal = Math.max(0, linea.search(/[^ ]/));
			// Cerrar los bloques cuya indentación original ya no se respeta.
			while (pila.length > 1 && indentOriginal <= pila[pila.length - 1].origOpener) {
				pila.pop();
			}
			const nivel = pila[pila.length - 1].nivel;
			lineas.push(' '.repeat(nivel * tam) + recortada);
			if (terminaConDosPuntos(recortada)) {
				pila.push({ nivel: nivel + 1, origOpener: indentOriginal });
			}
		}
		const eol = document.eol === vscode.EndOfLine.CRLF ? '\r\n' : '\n';
		// lineas viene de un split del texto completo: si el documento termina en
		// salto de línea, el último elemento ya es '' y join(eol) lo restaura.
		const nuevo = lineas.join(eol);
		if (nuevo === document.getText()) {
			return [];
		}
		return [vscode.TextEdit.replace(
			new vscode.Range(document.positionAt(0), document.positionAt(document.getText().length)),
			nuevo
		)];
	}
}

// ─────────────────────────────────────────────────────────────
// CodeLens: "Ejecutar" sobre función principal (estilo Python)
// ─────────────────────────────────────────────────────────────

export class LentesWini implements vscode.CodeLensProvider {
	provideCodeLenses(document: vscode.TextDocument): vscode.CodeLens[] {
		const lentes: vscode.CodeLens[] = [];
		try {
			for (const f of parseWiniFunctions(document.getText(), '', 'documento')) {
				if (f.name === 'principal') {
					const rango = new vscode.Range(f.line - 1, 0, f.line - 1, document.lineAt(Math.min(f.line - 1, document.lineCount - 1)).text.length);
					lentes.push(new vscode.CodeLens(rango, { title: '$(play) Ejecutar', command: 'wini.ejecutar' }));
					lentes.push(new vscode.CodeLens(rango, { title: '$(check) Comprobar', command: 'wini.comprobar' }));
				}
			}
		} catch {
			/* documento sin funciones: no hay lentes */
		}
		return lentes;
	}
}

// ─────────────────────────────────────────────────────────────
// Diagnósticos con winic (`-llvm` sin enlazar)
// ─────────────────────────────────────────────────────────────

let diagnosticos: vscode.DiagnosticCollection | undefined;
const temporizadores = new Map<string, NodeJS.Timeout>();

function parsearSalidaWinic(salida: string): Array<{ linea: number; columna?: number; mensaje: string; aviso: boolean }> {
	const out: Array<{ linea: number; columna?: number; mensaje: string; aviso: boolean }> = [];
	for (const cruda of salida.split(/\r?\n/)) {
		const lineaTexto = cruda.trim();
		if (!lineaTexto) {
			continue;
		}
		const esAviso = /^aviso:/i.test(lineaTexto);
		const cuerpo = lineaTexto.replace(/^aviso:\s*/i, '').replace(/^error(\s+compilando)?:\s*/i, '');
		// Formas: `… (línea N[, columna M])`, `línea N: …`
		let m = cuerpo.match(/\(l[ií]nea (\d+)(?:,\s*columna (\d+))?\)/i);
		if (m) {
			out.push({
				linea: parseInt(m[1], 10),
				columna: m[2] ? parseInt(m[2], 10) : undefined,
				mensaje: cuerpo,
				aviso: esAviso
			});
			continue;
		}
		m = cuerpo.match(/^[Ll][ií]nea (\d+)\s*:\s*(.*)$/);
		if (m) {
			out.push({ linea: parseInt(m[1], 10), mensaje: m[2] || cuerpo, aviso: esAviso });
			continue;
		}
		out.push({ linea: 0, mensaje: cuerpo, aviso: esAviso });
	}
	return out;
}

// ─────────────────────────────────────────────────────────────
// Entorno WINI_PATH para invocar a winic
// ─────────────────────────────────────────────────────────────
//
// VS Code abierto desde el lanzador gráfico no hereda el PATH ni el
// WINI_PATH del shell (~/.bashrc). El diagnóstico (execFile) usa el
// entorno del proceso de VS Code, que puede no traerlos. Estas funciones
// reconstruyen un WINI_PATH razonable (WINI_PATH existente +
// wini.winiPathExtra + carpetas junto a winic + share estándar +
// modulos/ del workspace) y lo inyectan al invocar a winic y en los
// comandos de terminal.

// Raíces con módulos Wini para componer WINI_PATH. Solo existentes.
function raicesWiniPath(): string[] {
	const dirs: string[] = [];
	const seen = new Set<string>();
	const agregar = (d?: string) => {
		if (!d) {
			return;
		}
		const t = d.trim();
		if (!t || seen.has(t)) {
			return;
		}
		seen.add(t);
		try {
			if (fs.statSync(t).isDirectory()) {
				dirs.push(t);
			}
		} catch {
			// No existe: se omite (el compilador también la omitiría).
		}
	};
	if (process.env.WINI_PATH) {
		for (const d of process.env.WINI_PATH.split(path.delimiter)) {
			agregar(d);
		}
	}
	const extra = cfg<string>('winiPathExtra', '');
	if (extra) {
		for (const d of extra.split(path.delimiter)) {
			agregar(d);
		}
	}
	const winic = buscarEjecutableWinic();
	if (winic) {
		const base = path.dirname(winic);
		agregar(path.join(base, 'modulos'));
		agregar(path.join(base, '..', 'share', 'wini', 'modulos'));
	}
	agregar(path.join(os.homedir(), '.local', 'share', 'wini', 'modulos'));
	if (process.platform !== 'win32') {
		agregar('/usr/local/share/wini/modulos');
		agregar('/usr/share/wini/modulos');
	}
	for (const carpeta of vscode.workspace.workspaceFolders || []) {
		agregar(path.join(carpeta.uri.fsPath, 'modulos'));
	}
	return dirs;
}

// Entorno para invocar a winic con WINI_PATH garantizado.
function envConWiniPath(): NodeJS.ProcessEnv {
	const env: NodeJS.ProcessEnv = { ...process.env };
	const compuesto = raicesWiniPath().join(path.delimiter);
	if (compuesto) {
		env.WINI_PATH = compuesto;
	}
	return env;
}

// Prefijo `export WINI_PATH=… &&` para los comandos enviados al terminal
// integrado (solo POSIX; en Windows el terminal hereda el entorno de VS Code).
function prefijoEnvTerminal(): string {
	if (process.platform === 'win32') {
		return '';
	}
	const compuesto = raicesWiniPath().join(path.delimiter);
	if (!compuesto) {
		return '';
	}
	return `export WINI_PATH="${compuesto.replace(/"/g, '\\"')}" && `;
}

function comprobarDocumento(document: vscode.TextDocument, manual = false): void {
	if (document.languageId !== 'wini' || !diagnosticos) {
		return;
	}
	if (document.isUntitled) {
		if (manual) {
			void vscode.window.showWarningMessage('Wini: guarda el archivo antes de comprobarlo.');
		}
		return;
	}
	const winic = buscarEjecutableWinic(document.uri.fsPath);
	if (!winic) {
		if (manual) {
			avisarSinWinic();
		}
		return;
	}
	const archivo = document.uri.fsPath;
	const tmpLl = path.join(os.tmpdir(), `wini-check-${process.pid}-${Date.now()}.ll`);
	execFile(winic, ['-llvm', tmpLl, archivo], { timeout: 30000, env: envConWiniPath() }, (error, _stdout, stderr) => {
		fs.unlink(tmpLl, () => { /* ignorar */ });
		const lista: vscode.Diagnostic[] = [];
		const max = Math.max(1, cfg<number>('maxProblems', 50));
		// winic imprime errores y avisos en stderr; el exit 0 también puede traer avisos.
		const salida = (stderr || '') + '\n' + ((error as unknown as { stdout?: string })?.stdout || '');
		if (!error && !salida.trim()) {
			diagnosticos!.set(document.uri, []);
			return;
		}
		for (const d of parsearSalidaWinic(salida)) {
			if (lista.length >= max) {
				break;
			}
			const nLineas = document.lineCount;
			const lin = d.linea > 0 ? Math.min(d.linea - 1, nLineas - 1) : 0;
			let rango: vscode.Range;
			try {
				const textoLinea = document.lineAt(lin).text;
				if (d.columna && d.columna > 0) {
					const col = Math.min(d.columna - 1, textoLinea.length);
					const palabra = document.getWordRangeAtPosition(new vscode.Position(lin, col), WORD_RE);
					rango = palabra || new vscode.Range(lin, col, lin, Math.min(col + 1, textoLinea.length));
				} else {
					rango = new vscode.Range(lin, 0, lin, Math.max(1, textoLinea.length));
				}
			} catch {
				rango = new vscode.Range(lin, 0, lin, 1);
			}
			lista.push(new vscode.Diagnostic(
				rango,
				d.mensaje,
				d.aviso ? vscode.DiagnosticSeverity.Warning : vscode.DiagnosticSeverity.Error
			));
		}
		diagnosticos!.set(document.uri, lista);
		if (manual) {
			const errores = lista.filter(x => x.severity === vscode.DiagnosticSeverity.Error).length;
			const avisos = lista.length - errores;
			if (errores === 0 && avisos === 0) {
				void vscode.window.showInformationMessage('Wini: sin errores.');
			} else {
				void vscode.window.showWarningMessage(`Wini: ${errores} error(es), ${avisos} aviso(s).`);
			}
		}
	});
}

function programarComprobacion(document: vscode.TextDocument, demoraMs: number): void {
	const clave = document.uri.toString();
	const previo = temporizadores.get(clave);
	if (previo) {
		clearTimeout(previo);
	}
	temporizadores.set(clave, setTimeout(() => {
		temporizadores.delete(clave);
		comprobarDocumento(document);
	}, demoraMs));
}

// ─────────────────────────────────────────────────────────────
// Comandos: compilar / ejecutar / IR
// ─────────────────────────────────────────────────────────────

function editorWini(): vscode.TextEditor | undefined {
	const editor = vscode.window.activeTextEditor;
	if (!editor || editor.document.languageId !== 'wini') {
		void vscode.window.showWarningMessage('Wini: abre un archivo .wn, .wini o .cwn primero.');
		return undefined;
	}
	return editor;
}

async function guardarSiHaceFalta(document: vscode.TextDocument): Promise<boolean> {
	if (!document.isUntitled && !document.isDirty) {
		return true;
	}
	if (document.isUntitled) {
		void vscode.window.showWarningMessage('Wini: guarda el archivo antes de compilarlo.');
		return false;
	}
	await document.save();
	return true;
}

function terminalWini(): vscode.Terminal {
	const existente = vscode.window.terminals.find(t => t.name === 'Wini');
	return existente || vscode.window.createTerminal('Wini');
}

async function comandoCompilar(): Promise<void> {
	const editor = editorWini();
	if (!editor) {
		return;
	}
	if (!await guardarSiHaceFalta(editor.document)) {
		return;
	}
	const winic = buscarEjecutableWinic(editor.document.uri.fsPath);
	if (!winic) {
		avisarSinWinic();
		return;
	}
	const archivo = editor.document.uri.fsPath;
	const t = terminalWini();
	t.show(true);
	t.sendText(`${prefijoEnvTerminal()}"${winic}" "${archivo}"`);
	comprobarDocumento(editor.document);
}

async function comandoEjecutar(): Promise<void> {
	const editor = editorWini();
	if (!editor) {
		return;
	}
	if (!await guardarSiHaceFalta(editor.document)) {
		return;
	}
	const winic = buscarEjecutableWinic(editor.document.uri.fsPath);
	if (!winic) {
		avisarSinWinic();
		return;
	}
	const archivo = editor.document.uri.fsPath;
	const base = path.basename(archivo, path.extname(archivo));
	const dir = path.dirname(archivo);
	const bin = path.join(dir, 'build', base);
	const t = terminalWini();
	t.show(true);
	if (process.platform === 'win32') {
		t.sendText(`& "${winic}" "${archivo}"`);
		if (cfg<boolean>('runAfterBuild', true)) {
			t.sendText(`& "${bin}.exe"`);
		}
	} else {
		t.sendText(`${prefijoEnvTerminal()}"${winic}" "${archivo}"`);
		if (cfg<boolean>('runAfterBuild', true)) {
			t.sendText(`"${bin}"`);
		}
	}
	comprobarDocumento(editor.document);
}

async function comandoMostrarIR(): Promise<void> {
	const editor = editorWini();
	if (!editor) {
		return;
	}
	if (!await guardarSiHaceFalta(editor.document)) {
		return;
	}
	const winic = buscarEjecutableWinic(editor.document.uri.fsPath);
	if (!winic) {
		avisarSinWinic();
		return;
	}
	const archivo = editor.document.uri.fsPath;
	const salida = archivo + '.ll';
	const t = terminalWini();
	t.show(true);
	t.sendText(`${prefijoEnvTerminal()}"${winic}" -llvm "${salida}" "${archivo}"`);
}

// ─────────────────────────────────────────────────────────────
// Barra de estado + aviso cuando falta winic
// ─────────────────────────────────────────────────────────────

let barraWinic: vscode.StatusBarItem | undefined;

function actualizarBarraWinic(): void {
	if (!barraWinic) {
		return;
	}
	const winic = buscarEjecutableWinic();
	if (winic) {
		barraWinic.text = '$(check) Wini';
		barraWinic.tooltip = `winic: ${winic}\nClic para compilar y ejecutar el archivo actual`;
		barraWinic.command = 'wini.ejecutar';
		barraWinic.backgroundColor = undefined;
	} else {
		barraWinic.text = '$(alert) Wini sin winic';
		barraWinic.tooltip = 'No encontré winic (clic para ver cómo resolverlo)';
		barraWinic.command = 'wini.configurarWinic';
		barraWinic.backgroundColor = new vscode.ThemeColor('statusBarItem.warningBackground');
	}
	barraWinic.show();
}

async function avisarSinWinic(): Promise<void> {
	const eleccion = await vscode.window.showWarningMessage(
		'Wini: no encontré el compilador winic. Lo busco en wini.winicPath, el PATH, la raíz del proyecto y carpetas cercanas.',
		'Abrir ajustes',
		'Reintentar'
	);
	if (eleccion === 'Abrir ajustes') {
		void vscode.commands.executeCommand('workbench.action.openSettings', 'wini.winicPath');
	} else if (eleccion === 'Reintentar') {
		olvidarWinic();
		actualizarBarraWinic();
		const editor = vscode.window.activeTextEditor;
		if (editor?.document.languageId === 'wini') {
			comprobarDocumento(editor.document, true);
		}
	}
}

/** Vuelve a comprobar los editores Wini visibles (tras recargar módulos o winic). */
function recomprobarVisibles(): void {
	if (!diagnosticos || !cfg<boolean>('diagnosticsOnSave', true)) {
		return;
	}
	for (const editor of vscode.window.visibleTextEditors) {
		if (editor.document.languageId === 'wini' && !editor.document.isUntitled) {
			comprobarDocumento(editor.document);
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Activación
// ─────────────────────────────────────────────────────────────

export function activate(context: vscode.ExtensionContext): void {
	if (!canal) {
		canal = vscode.window.createOutputChannel('Wini');
	}
	barraWinic = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
	context.subscriptions.push(barraWinic);
	// Carga en segundo plano: no bloquea la activación
	void cargarStdlib(context).then(() => {
		actualizarBarraWinic();
		if (vscode.window.activeTextEditor?.document.languageId === 'wini') {
			comprobarDocumento(vscode.window.activeTextEditor.document);
		}
	});

	diagnosticos = vscode.languages.createDiagnosticCollection('wini');
	context.subscriptions.push(diagnosticos);

	const selector: vscode.DocumentSelector = { language: 'wini' };

	// Recarga con debounce ante cambios de configuración, archivos o guardados.
	let recargaTimer: NodeJS.Timeout | undefined;
	const programarRecarga = () => {
		if (recargaTimer) {
			clearTimeout(recargaTimer);
		}
		recargaTimer = setTimeout(() => {
			void cargarStdlib(context).then(() => {
				actualizarBarraWinic();
				recomprobarVisibles();
			});
		}, 1200);
	};

	context.subscriptions.push(
		// '.' y '_' como disparadores extra; las letras disparan por quickSuggestions
		vscode.languages.registerCompletionItemProvider('wini', new SugerenciasWini(), '.', '_', '"', "'", '/', '<', ' ', ':'),
		vscode.languages.registerHoverProvider('wini', new HoverWini()),
		vscode.languages.registerSignatureHelpProvider('wini', new FirmaWini(), '(', ','),
		vscode.languages.registerDocumentSemanticTokensProvider(selector, new ModulosSemanticTokens(), LEYENDA_MODULOS),
		vscode.languages.registerDocumentSymbolProvider(selector, new SimbolosWini()),
		vscode.languages.registerDefinitionProvider(selector, new DefinicionWini()),
		vscode.languages.registerFoldingRangeProvider(selector, new PlegadoWini()),
		vscode.languages.registerReferenceProvider(selector, new ReferenciasWini()),
		vscode.languages.registerDocumentHighlightProvider(selector, new DestacadoWini()),
		vscode.languages.registerRenameProvider(selector, new RenombrarWini()),
		vscode.languages.registerWorkspaceSymbolProvider(new SimbolosEspacioWini()),
		vscode.languages.registerCodeActionsProvider(selector, new AccionesWini(), AccionesWini.metadata),
		vscode.languages.registerDocumentFormattingEditProvider(selector, new FormateadorWini()),
		vscode.languages.registerCodeLensProvider(selector, new LentesWini()),
		vscode.workspace.onDidChangeConfiguration(e => {
			if (e.affectsConfiguration('wini')) {
				olvidarWinic();
				programarRecarga();
			}
		}),
		vscode.workspace.createFileSystemWatcher('**/*.{wn,wini,cwn}').onDidCreate(programarRecarga),
		vscode.workspace.createFileSystemWatcher('**/*.{wn,wini,cwn}').onDidDelete(programarRecarga),
		vscode.workspace.onDidOpenTextDocument(doc => {
			if (doc.languageId === 'wini' && cfg<boolean>('diagnosticsOnSave', true)) {
				comprobarDocumento(doc);
			}
		}),
		vscode.workspace.onDidChangeTextDocument(e => {
			if (e.document.languageId === 'wini' && cfg<boolean>('diagnosticsOnType', false)) {
				programarComprobacion(e.document, 800);
			}
		}),
		vscode.workspace.onDidSaveTextDocument(doc => {
			if (doc.languageId !== 'wini') {
				return;
			}
			if (/\.(wn|wini|cwn)$/i.test(doc.fileName)) {
				programarRecarga();
			}
			if (cfg<boolean>('diagnosticsOnSave', true)) {
				comprobarDocumento(doc);
			}
		}),
		vscode.commands.registerCommand('wini.recargarStdlib', async () => {
			await cargarStdlib(context);
			actualizarBarraWinic();
			recomprobarVisibles();
			const winic = buscarEjecutableWinic();
			void vscode.window.showInformationMessage(
				`Wini: ${modulos.size} módulo(s) y ${stdlib.size} función(es) disponibles` +
				(winic ? ` · winic: ${winic}` : ' · winic no encontrado (clic en la barra de estado para resolverlo)')
			);
		}),
		vscode.commands.registerCommand('wini.configurarWinic', avisarSinWinic),
		vscode.commands.registerCommand('wini.comprobar', () => {
			const editor = editorWini();
			if (editor) {
				comprobarDocumento(editor.document, true);
			}
		}),
		vscode.commands.registerCommand('wini.compilar', comandoCompilar),
		vscode.commands.registerCommand('wini.ejecutar', comandoEjecutar),
		vscode.commands.registerCommand('wini.mostrarIR', comandoMostrarIR)
	);
}

export function deactivate(): void {
	for (const t of temporizadores.values()) {
		clearTimeout(t);
	}
	temporizadores.clear();
}
