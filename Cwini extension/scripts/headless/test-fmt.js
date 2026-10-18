// Prueba headless del formateador, ocurrenciasDePalabra y enCodigo.
// Uso: node scripts/headless/test-fmt.js   (compila antes con npm run compile)
const path = require('path');
const Module = require('module');

const stubPath = path.join(__dirname, 'vscode-stub.js');
const origResolve = Module._resolveFilename;
Module._resolveFilename = function (request, ...rest) {
	if (request === 'vscode') { return stubPath; }
	return origResolve.call(this, request, ...rest);
};

const extDir = path.join(__dirname, '..', '..', 'out');
const ext = require(path.join(extDir, 'extension.js'));
const vscode = require(stubPath);
vscode.EndOfLine = { LF: 1, CRLF: 2 };

const fails = [];
const ok = (cond, msg) => { console.log((cond ? 'PASS' : 'FAIL') + ' ' + msg); if (!cond) { fails.push(msg); } };

// Documento falso mínimo para el formateador.
function docFake(texto) {
	const lineas = texto.split('\n');
	return {
		lineCount: lineas.length,
		lineAt: i => ({ text: lineas[i] }),
		getText: () => texto,
		eol: vscode.EndOfLine.LF,
		positionAt: o => ({ line: 0, character: o })
	};
}

const entrada = [
	'funcion suma(a: entero, b: entero) -> entero:',
	'\t\tretornar a + b',            // indentación de más
	'',
	'estructura Punto:',
	'    x: entero',
	'    y: entero',
	'',
	'funcion principal() -> entero:',
	'  si 1 == 1:',
	'        escribir("hola")',
	'  sino:',
	'        escribir("adios")',
	'  mientras falso:',
	'        continuar',
	'  intentar:',
	'        lanzar ValueError("x")',
	'  capturar ValueError e:',
	'        escribir(e)',
	'  finalmente:',
	'        escribir("ok")',
	'  retornar 0   ',                // espacios sobrando al final
	''
].join('\n');

const f = new ext.FormateadorWini();
const edits = f.provideDocumentFormattingEdits(docFake(entrada));
ok(edits.length === 1, 'formateador produce un único TextEdit');
const salida = edits[0].newText;
console.log('--- salida ---\n' + salida + '-------------');
const esperado = [
	'funcion suma(a: entero, b: entero) -> entero:',
	'    retornar a + b',
	'',
	'estructura Punto:',
	'    x: entero',
	'    y: entero',
	'',
	'funcion principal() -> entero:',
	'    si 1 == 1:',
	'        escribir("hola")',
	'    sino:',
	'        escribir("adios")',
	'    mientras falso:',
	'        continuar',
	'    intentar:',
	'        lanzar ValueError("x")',
	'    capturar ValueError e:',
	'        escribir(e)',
	'    finalmente:',
	'        escribir("ok")',
	'    retornar 0',
	''
].join('\n');
ok(salida === esperado, 're-indentación estructural correcta');

// Idempotente: formatear dos veces da lo mismo.
const edits2 = f.provideDocumentFormattingEdits(docFake(salida));
ok(edits2.length === 0, 'formateador idempotente (segunda pasada sin cambios)');

// Llamada multilínea (dentro de paréntesis) no se toca.
const multi = [
	'funcion principal() -> entero:',
	'    escribir(max(1,',
	'            2,',
	'            3))',
	'    retornar 0',
	''
].join('\n');
const edits3 = f.provideDocumentFormattingEdits(docFake(multi));
ok(edits3.length === 0, 'líneas de continuación entre paréntesis intactas');

// ocurrenciasDePalabra: no cuenta dentro de strings ni comentarios.
const textoDoc = 'x = foo + 1  # foo en comentario\nescribir("foo")\ny = foo\n';
const doc = docFake(textoDoc);
const rangos = ext.ocurrenciasDePalabra(doc, 'foo');
ok(rangos.length === 2, 'ocurrenciasDePalabra: 2 en código (4 textualmente: comentario, string y 2 en código)');
ok(rangos.length === 2 && rangos[0].start.line === 0 && rangos[0].start.character === 4 && rangos[1].start.line === 2, 'ocurrencias en las posiciones correctas');

ok(ext.enCodigo('escribir("a#b") # com', 11) === false, 'enCodigo detecta # dentro de string');
ok(ext.enCodigo('escribir("a#b") # com', 17) === false, 'enCodigo detecta comentario');
ok(ext.enCodigo('x = 1  # fin', 3) === true, 'enCodigo en código');

console.log(fails.length === 0 ? 'TODO OK FMT' : 'FALLOS: ' + fails.length);
process.exit(fails.length === 0 ? 0 : 1);
