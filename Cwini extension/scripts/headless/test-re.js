// Test headless de descubrimiento de módulos y autocompletado `alias.`.
// Uso: npm run test:headless   (compila primero con npm run compile)
// Reproduce el caso real: `importar "regex/regex" como re` + `re.<cursor>`
// con VS Code lanzado desde el lanzador gráfico (sin WINI_PATH en entorno).
const path = require('path');
const Module = require('module');

// Intercepta require('vscode') -> stub local (sin VS Code corriendo).
const stubPath = path.join(__dirname, 'vscode-stub.js');
const origResolve = Module._resolveFilename;
Module._resolveFilename = function (request, ...rest) {
	if (request === 'vscode') {
		return stubPath;
	}
	return origResolve.call(this, request, ...rest);
};

const extDir = path.join(__dirname, '..', '..', 'out');
const ext = require(path.join(extDir, 'extension.js'));
const vscode = require(stubPath);

const fails = [];
const ok = (cond, msg) => { console.log((cond ? 'PASS' : 'FAIL') + ' ' + msg); if (!cond) { fails.push(msg); } };

// 1) Descubrimiento sin WINI_PATH en entorno (como VS Code desde lanzador)
const savedWini = process.env.WINI_PATH;
delete process.env.WINI_PATH;
const dirs = ext.directoriosDeModulos(path.join('__sin_extension__'));
const share = path.join(process.env.HOME || process.env.USERPROFILE || '', '.local', 'share', 'wini', 'modulos');
const shareWin = process.platform === 'win32' ? null : share;
if (shareWin) {
	ok(dirs.includes(shareWin), 'directoriosDeModulos incluye share instalado: ' + shareWin);
}
for (const d of dirs) {
	ext.escanearDirectorioModulos(d, 'sistema');
}
const mods = ext.modulosDescubiertos();
const regex = mods.find(m => m.ruta.replace(/\\/g, '/').endsWith('regex/regex.cwn'));
ok(!!regex, 'regex/regex.cwn descubierto (' + mods.length + ' módulos en total)');
if (savedWini !== undefined) {
	process.env.WINI_PATH = savedWini;
}

// 2) El módulo trae funciones (incluidas externas FFI)
if (regex) {
	const nombres = [...regex.declaraciones.map(f => f.name), ...regex.externas.map(e => e.name)];
	for (const fn of ['coincide', 'extraer', 'compilar', 'coincide_en', 'reemplazar']) {
		ok(nombres.includes(fn), 'regex expone ' + fn);
	}
} else {
	ok(false, 'regex expone coincide (módulo no descubierto)');
}

// 3) Autocompletado tras `re.`
const texto = 'importar "regex/regex" como re\n\nfuncion principal() -> entero:\n    re.';
const lineas = texto.split('\n');
const doc = {
	languageId: 'wini',
	uri: { fsPath: path.join('__proyecto_demo__', 'demo.wn') },
	getText: () => texto,
	lineAt: (l) => ({ text: lineas[l] }),
	lineCount: lineas.length,
	getWordRangeAtPosition: () => undefined, // cursor tras '.', como VS Code real
};
const prov = new ext.SugerenciasWini();
const pos = new vscode.Position(3, '    re.'.length);
const lista = prov.provideCompletionItems(doc, pos);
const labels = lista.items.map(i => (typeof i.label === 'string' ? i.label : i.label.label));
for (const fn of ['coincide', 'extraer', 'reemplazar', 'compilar', 'error']) {
	ok(labels.includes(fn), 're. sugiere ' + fn);
}
ok(!labels.includes('escribir'), 're. NO mezcla globales (solo lo del módulo)');
console.log('total items tras re.:', labels.length);

// 4) Sin volcado: carácter trigger con prefijo vacío no devuelve nada
// (las sugerencias llegan al escribir o con Ctrl+Espacio).
const Invoke = { triggerKind: 0 };
const PorCaracter = (ch) => ({ triggerKind: 1, triggerCharacter: ch });
function hacerDoc(texto, cursorLine, cursorChar) {
	const lineas = texto.split('\n');
	return {
		languageId: 'wini',
		uri: { fsPath: path.join('__proyecto_demo__', 'demo.wn') },
		getText: () => texto,
		lineAt: (l) => ({ text: lineas[l] }),
		lineCount: lineas.length,
		getWordRangeAtPosition: () => undefined,
	};
}
{
	const t = 'importar "';
	const d = hacerDoc(t, 0, t.length);
	const r = prov.provideCompletionItems(d, new vscode.Position(0, t.length), undefined, PorCaracter('"'));
	ok(r.items.length === 0, 'importar " + trigger no vuelca el catálogo (' + r.items.length + ' items)');
	const r2 = prov.provideCompletionItems(d, new vscode.Position(0, t.length), undefined, Invoke);
	ok(r2.items.length > 0, 'importar " + Ctrl+Espacio sí lista (' + r2.items.length + ' items)');
}
{
	const t = '    re.';
	const texto4 = 'importar "regex/regex" como re\n\nfuncion principal() -> entero:\n' + t;
	const d = hacerDoc(texto4, 3, t.length);
	const r = prov.provideCompletionItems(d, new vscode.Position(3, t.length), undefined, PorCaracter('.'));
	const labs = r.items.map(i => (typeof i.label === 'string' ? i.label : i.label.label));
	ok(labs.length > 0, 're. + punto YA lista lo del módulo (' + labs.length + ' items)');
	ok(labs.includes('coincide'), 're. + punto sugiere funciones (coincide)');
	ok(labs.includes('error'), 're. + punto sugiere estructuras (error)');
	ok(!labs.includes('escribir'), 're. + punto NO mezcla globales (solo lo del módulo)');
	const t2 = '    re.c';
	const texto42 = 'importar "regex/regex" como re\n\nfuncion principal() -> entero:\n' + t2;
	const d2 = hacerDoc(texto42, 3, t2.length);
	const r2 = prov.provideCompletionItems(d2, new vscode.Position(3, t2.length), undefined, Invoke);
	const labs2 = r2.items.map(i => (typeof i.label === 'string' ? i.label : i.label.label));
	ok(labs2.includes('coincide'), 're.c sugiere coincide');
}

// 5) Referencia corta al insertar un import del sistema
{
	const t = 'importar "reg';
	const d = hacerDoc(t, 0, t.length);
	const r = prov.provideCompletionItems(d, new vscode.Position(0, t.length), undefined, Invoke);
	const item = r.items.find(i => {
		const ft = i.filterText || '';
		return ft.includes('regex/regex') || (typeof i.label === 'string' ? i.label : i.label.label).includes('regex');
	});
	ok(!!item, 'importar "reg filtra hasta regex');
	if (item) {
		const ins = typeof item.insertText === 'string' ? item.insertText : item.insertText.value;
		ok(ins === 'regex/regex"', 'inserta forma corta (' + ins + '), no ruta absoluta al share');
	}
}

// 6) Sin alias no se sugieren las funciones del módulo (peladas)
{
	const texto6 = 'importar "regex/regex" como re\n\nfuncion principal() -> entero:\n    esc';
	const d = hacerDoc(texto6, 3, '    esc'.length);
	const r = prov.provideCompletionItems(d, new vscode.Position(3, '    esc'.length), undefined, Invoke);
	const labs = r.items.map(i => (typeof i.label === 'string' ? i.label : i.label.label));
	ok(!labs.includes('coincide'), 'pelado NO sugiere coincide (va con re.)');
	ok(labs.includes('escribir'), 'pelado SÍ sugiere builtins');
}

// 7) Sin duplicados por raíces solapadas
{
	const rutas = mods.map(m => m.ruta);
	ok(new Set(rutas).size === rutas.length, 'cada archivo aparece una sola vez (' + rutas.length + ' módulos)');
}

// 8) Excepciones como sugerencias: tras `capturar` y tras `lanzar`,
// sin volcado al escribir el espacio, filtradas al escribir
{
	const base = 'funcion principal() -> entero:\n    intentar:\n        escribir(1)\n    ';
	const excLabels = (r) => r.items.map(i => (typeof i.label === 'string' ? i.label : i.label.label));
	{
		const t = base + 'capturar ';
		const d = hacerDoc(t, 3, t.length);
		const r = prov.provideCompletionItems(d, new vscode.Position(3, t.length), undefined, PorCaracter(' '));
		ok(r.items.length === 9, 'capturar + espacio lista las 9 sin ocultar (' + r.items.length + ' items)');
		const r2 = prov.provideCompletionItems(d, new vscode.Position(3, t.length), undefined, Invoke);
		ok(r2.items.length === 9, 'capturar + Ctrl+Espacio lista las 9 (' + r2.items.length + ')');
	}
	{
		const t = base + 'capturar Val';
		const d = hacerDoc(t, 3, t.length);
		const r = prov.provideCompletionItems(d, new vscode.Position(3, t.length), undefined, Invoke);
		ok(excLabels(r).includes('ValueError'), 'capturar Val sugiere ValueError');
	}
	{
		const t = base + 'lanzar ';
		// `lanzar` va fuera del capturar en este texto de prueba
		const t2 = 'funcion principal() -> entero:\n    lanzar ';
		const d = hacerDoc(t2, 1, t2.length);
		const r = prov.provideCompletionItems(d, new vscode.Position(1, t2.length), undefined, PorCaracter(' '));
		ok(r.items.length === 9, 'lanzar + espacio lista las 9 sin ocultar (' + r.items.length + ' items)');
		const t3 = 'funcion principal() -> entero:\n    lanzar Key';
		const d3 = hacerDoc(t3, 1, t3.length);
		const r3 = prov.provideCompletionItems(d3, new vscode.Position(1, t3.length), undefined, Invoke);
		ok(excLabels(r3).includes('KeyError'), 'lanzar Key sugiere KeyError');
		const r4 = prov.provideCompletionItems(d, new vscode.Position(1, t2.length), undefined, Invoke);
		ok(r4.items.length === 9, 'lanzar + Ctrl+Espacio lista las 9 (' + r4.items.length + ')');
	}
}

// 9) Verde: las excepciones llevan token semántico `type` (2)
{
	const texto9 = 'funcion principal() -> entero:\n    intentar:\n        escribir(1)\n    capturar ValueError:\n        escribir(2)\n    retornar 0\n';
	const lineas9 = texto9.split('\n');
	const offs = [0];
	for (const l of lineas9) {
		offs.push(offs[offs.length - 1] + l.length + 1);
	}
	const doc9 = {
		languageId: 'wini',
		uri: { fsPath: path.join('__proyecto_demo__', 'demo.wn') },
		getText: () => texto9,
		positionAt: (off) => {
			let l = 0;
			while (l + 1 < offs.length && offs[l + 1] <= off) {
				l++;
			}
			return new vscode.Position(l, off - offs[l]);
		},
	};
	const sem = new ext.ModulosSemanticTokens();
	const built = sem.provideDocumentSemanticTokens(doc9);
	const tok = built.data.find(t => t.len === 'ValueError'.length && t.type === 2);
	ok(!!tok, 'ValueError clasificado como token type (verde del tema)');
}
if (fails.length) {
	console.error('FALLOS:', fails.length);
	process.exit(1);
}
console.log('TODO OK');
