'use strict';

// ---------------------------------------------------------------------------
// Cwin Language Support (Wini) — cliente de la extensión.
// Proveedores: hover (builtins + comentarios de funciones + keywords +
// módulos), autocompletado, definición, símbolos y diagnósticos de
// importaciones. Sistema de módulos con detección silenciosa del compilador
// (`where cwini` / `which cwini`) y búsqueda de módulos .cwn/.wn/.wini.
// ---------------------------------------------------------------------------

const vscode = require('vscode');
const path = require('path');
const fs = require('fs');
const { DOCS_BUILTINS, CONVERSIONES, DOCS_KEYWORDS, PALABRAS, TIPOS_DATO } = require('./builtins');
const analisis = require('./analisis');
const modulos = require('./modulos');

const SELECTORES = [{ language: 'cwin' }, { language: 'wini' }];
const TTL_ARCHIVO = 10000;

const estado = { compilador: null, contadorModulos: 0, escaneoEnVuelo: false };
let coleccionDiag = null;
let barra = null;
const cacheArchivos = new Map(); // fsPath -> { cuando, doc }

// ---------------------------------------------------------------------- util

function config() {
  return vscode.workspace.getConfiguration('cwin');
}

function opcionesModulos(documento) {
  return {
    carpetasExtra: config().get('modulos.carpetasExtra') || [],
    buscarJuntoAlCompilador: config().get('modulos.buscarJuntoAlCompilador') !== false,
    compilador: estado.compilador,
    carpetasProyecto: (vscode.workspace.workspaceFolders || []).map((f) => f.uri.fsPath),
    carpetaActual: documento ? path.dirname(documento.uri.fsPath) : '',
  };
}

function analisisDe(uri) {
  const clave = uri.fsPath;
  const hit = cacheArchivos.get(clave);
  if (hit && Date.now() - hit.cuando < TTL_ARCHIVO) return hit.doc;
  const doc = analisis.parsearArchivo(clave);
  cacheArchivos.set(clave, { cuando: Date.now(), doc });
  return doc;
}

function analisisVivo(documento) {
  return analisis.parsearDocumento(documento.getText());
}

function md(texto) {
  const s = new vscode.MarkdownString(texto);
  s.isTrusted = true;
  return s;
}

function bloqueDoc(docText) {
  if (!docText) return '';
  const lineas = docText.split('\n').map((l) => `> ${l}`.trimEnd()).join('\n');
  return `\n${lineas}\n`;
}

function acotarRaiz(rango) {
  return rango > 300 ? 300 : rango;
}

// ------------------------------------------------------------- hover: piezas

function hoverBuiltin(palabra) {
  const d = DOCS_BUILTINS[palabra];
  const partes = [];
  partes.push(`### \`${d.firma}\``);
  partes.push(d.retorno ? `**Integrada** \`${d.categoria}\` → devuelve \`${d.retorno}\`` : `**Integrada** \`${d.categoria}\``);
  partes.push(d.resumen);
  if (d.params && d.params.length) {
    partes.push('**Parámetros**\n');
    for (const p of d.params) partes.push(`- \`${p.nombre}\` — ${p.tipo}: ${p.desc}`);
  }
  if (d.ejemplo) partes.push('**Ejemplo**\n```wini\n' + d.ejemplo + '\n```');
  return new vscode.Hover(md(partes.join('\n\n')));
}

function hoverConversion(conv) {
  const partes = [];
  partes.push(`### \`${conv.firma}\``);
  partes.push(`**Conversión integrada** → devuelve \`${conv.retorno}\``);
  partes.push(conv.resumen);
  if (conv.ejemplo) partes.push('**Ejemplo**\n```wini\n' + conv.ejemplo + '\n```');
  return new vscode.Hover(md(partes.join('\n\n')));
}

function hoverKeyword(palabra) {
  const texto = DOCS_KEYWORDS[palabra];
  if (!texto) return null;
  return new vscode.Hover(md(`### \`${palabra}\`\n\n**Palabra clave de Cwin**\n\n${texto}`));
}

function hoverFuncion(f, archivo) {
  const partes = [];
  partes.push(`### \`${f.firma}\``);
  partes.push(`**${f.clase === 'externa' ? 'Función nativa (externa)' : 'Función'}** · [${path.basename(archivo)}:${f.linea}](file://${encodeURI(archivo)})`);
  const doc = f.doc || f.docstring;
  if (doc) partes.push(bloqueDoc(doc));
  if (f.params && f.params.length) {
    partes.push('**Parámetros**\n');
    for (const p of f.params) {
      const defecto = p.defecto ? ` (por defecto: \`${p.defecto}\`)` : '';
      partes.push(`- \`${p.nombre}\` — ${p.tipo || 'sin tipo'}${defecto}`);
    }
  }
  partes.push(`**Devuelve:** ${f.retorno || 'nulo'}`);
  if (f.clase === 'externa') partes.push('Se enlaza con las bibliotecas .a/.o de su misma carpeta (autodetectadas por el compilador).');
  return new vscode.Hover(md(partes.join('\n\n')));
}

function hoverEstructura(e) {
  const partes = [];
  partes.push(`### \`estructura ${e.nombre}\``);
  partes.push(`**Estructura** · línea ${e.linea}`);
  if (e.doc) partes.push(bloqueDoc(e.doc));
  if (e.campos.length) {
    partes.push('**Campos**\n');
    for (const c of e.campos) partes.push(`- \`${c.nombre}\` — ${c.tipo}`);
  } else {
    partes.push('Sin campos declarados (palabra reservada nueva del lenguaje).');
  }
  return new vscode.Hover(md(partes.join('\n\n')));
}

async function hoverImportacion(documento, rango, palabra) {
  const res = await modulos.resolver(palabra, opcionesModulos(documento));
  if (res.ok && res.mod) {
    const funcs = res.mod.funciones.map((f) => `- \`${f.nombre}\``).join('\n');
    return new vscode.Hover(md(
      `### 📦 Módulo \`${palabra}\`\n\n**Ruta:** \`${res.ruta}\`\n\n**Funciones que exporta:**\n${funcs || '_ninguna_'}\n\nAl importar, sus funciones quedan disponibles directamente (espacio de nombres plano, igual que hace el compilador).`
    ));
  }
  const raices = res.buscadoEn.map((r) => `- \`${r}\``).join('\n');
  return new vscode.Hover(md(
    `### 📦 Módulo \`${palabra}\`\n\n⚠️ **No encontrado.** Se buscó un archivo \`${palabra}.cwn\` / \`.wn\` / \`.wini\` en:\n${raices || '- (sin raíces)'}\n\nColócalo junto a este archivo, en una carpeta \`modulos/\`, junto al compilador detectado, o agrega la carpeta en \`cwin.modulos.carpetasExtra\`.`
  ));
}

// ------------------------------------------------------- búsqueda workspace

async function archivosDelProyecto() {
  try {
    const uris = await vscode.workspace.findFiles('**/*.{cwn,wn,wini}', '**/{node_modules,build,.git}/**', 200);
    return uris;
  } catch {
    return [];
  }
}

async function buscarFuncion(palabra, documento) {
  const vivo = analisisVivo(documento);
  const local = vivo.funciones.find((f) => f.nombre === palabra);
  if (local) return { f: local, archivo: documento.uri.fsPath };
  for (const imp of vivo.importaciones) {
    const res = await modulos.resolver(imp.nombre, opcionesModulos(documento));
    if (res.ok) {
      const hit = res.mod.funciones.find((x) => x.nombre === palabra);
      if (hit) {
        const doc = analisis.parsearArchivo(res.ruta);
        const f = doc.funciones.find((x) => x.nombre === palabra);
        if (f) return { f, archivo: res.ruta };
      }
    }
  }
  for (const uri of await archivosDelProyecto()) {
    if (uri.fsPath === documento.uri.fsPath) continue;
    const doc = analisisDe(uri);
    const f = doc.funciones.find((x) => x.nombre === palabra);
    if (f) return { f, archivo: uri.fsPath };
  }
  return null;
}

async function buscarEstructura(palabra, documento) {
  const vivo = analisisVivo(documento);
  let e = vivo.estructuras.find((x) => x.nombre === palabra);
  if (e) return e;
  for (const uri of await archivosDelProyecto()) {
    if (uri.fsPath === documento.uri.fsPath) continue;
    const doc = analisisDe(uri);
    e = doc.estructuras.find((x) => x.nombre === palabra);
    if (e) return e;
  }
  return null;
}

// ------------------------------------------------------------------ proveedor de hover

async function proveerHover(documento, posicion) {
  try {
    const rango = documento.getWordRangeAtPosition(posicion, /[A-Za-z_][\w.]*/);
    if (!rango) return null;
    const palabra = documento.getText(rango);
    const linea = documento.lineAt(posicion.line).text;
    const antes = linea.slice(0, rango.start.character).trim();

    if (antes.startsWith('#')) return null; // dentro de un comentario
    if (antes.startsWith('importar')) return hoverImportacion(documento, rango, palabra);

    if (DOCS_BUILTINS[palabra]) return hoverBuiltin(palabra);
    const conv = CONVERSIONES.find((c) => c.nombre === palabra || c.alias.includes(palabra));
    if (conv) return hoverConversion(conv);
    const kw = hoverKeyword(palabra);
    if (kw) return kw;

    const hitF = await buscarFuncion(palabra, documento);
    if (hitF) return hoverFuncion(hitF.f, hitF.archivo);
    const hitE = await buscarEstructura(palabra, documento);
    if (hitE) return hoverEstructura(hitE);
    return null;
  } catch (e) {
    console.error('[cwin] hover:', e);
    return null;
  }
}

// --------------------------------------------------------- proveedor de completado

function itemPalabra(palabra, docs) {
  const item = new vscode.CompletionItem(palabra, vscode.CompletionItemKind.Keyword);
  if (docs) item.documentation = md(`**Palabra clave de Cwin**\n\n${docs}`);
  return item;
}

function itemBuiltin(palabra, d) {
  const item = new vscode.CompletionItem(palabra, vscode.CompletionItemKind.Function);
  item.detail = d.firma;
  const partes = [`**Integrada** \`${d.categoria}\``, d.resumen];
  if (d.ejemplo) partes.push('```wini\n' + d.ejemplo + '\n```');
  item.documentation = md(partes.join('\n\n'));
  item.insertText = new vscode.SnippetString(`${palabra}($0)`);
  item.kind = vscode.CompletionItemKind.Function;
  return item;
}

function itemConversion(conv) {
  const item = new vscode.CompletionItem(conv.nombre, vscode.CompletionItemKind.Function);
  item.detail = conv.firma;
  item.documentation = md(`${conv.resumen}\n\n\`\`\`wini\n${conv.ejemplo}\n\`\`\``);
  item.insertText = new vscode.SnippetString(`${conv.nombre}($0)`);
  if (conv.alias.length) item.label = { label: conv.nombre, description: `alias: ${conv.alias.join(', ')}` };
  return item;
}

function itemFuncion(f, detalle) {
  const item = new vscode.CompletionItem(f.nombre, vscode.CompletionItemKind.Function);
  item.detail = `${f.firma} · ${detalle}`;
  const docs = [];
  if (f.doc || f.docstring) docs.push(`> ${(f.doc || f.docstring).split('\n').join('\n> ')}`);
  const ps = (f.params || []).map((p, i) => `\n- \`${p.nombre}\` — ${p.tipo || 'sin tipo'}${p.defecto ? ` = ${p.defecto}` : ''}`);
  if (ps.length) docs.push(`**Parámetros:**${ps.join('')}`);
  if (f.retorno) docs.push(`**Devuelve:** \`${f.retorno}\``);
  if (docs.length) item.documentation = md(docs.join('\n'));
  const placeholders = (f.params || []).map((p, i) => `\${${i + 1}:${p.nombre}}`).join(', ');
  item.insertText = new vscode.SnippetString(`${f.nombre}(${placeholders || '$0'})`);
  return item;
}

function itemModulo(m) {
  const item = new vscode.CompletionItem(m.nombre, vscode.CompletionItemKind.Module);
  item.detail = m.ruta;
  const funcs = m.funciones.map((f) => `- \`${f.nombre}\``).join('\n');
  item.documentation = md(`**Módulo Cwin**\n\n\`${m.ruta}\`\n\n**Exporta:**\n${funcs || '_ninguna_'}`);
  item.insertText = m.nombre;
  return item;
}

async function proveerCompletado(documento, posicion) {
  try {
    const linea = documento.lineAt(posicion.line).text;
    const textoAntes = linea.slice(0, posicion.character);

    // Contexto importar: sugerir módulos
    if (/^\s*importar\s+[\w."']*$/.test(textoAntes)) {
      const mods = await modulos.buscarModulos(opcionesModulos(documento));
      return mods.map(itemModulo);
    }

    // Tras un punto no hay miembros (espacio de nombres plano como el compilador)
    if (textoAntes.trimEnd().endsWith('.')) return [];

    const rango = documento.getWordRangeAtPosition(posicion, /[A-Za-z_]\w*/);
    const prefijo = rango ? documento.getText(rango) : '';
    if (prefijo && !/^[A-Za-z_]\w*$/.test(prefijo)) return [];

    const opciones = {
      matchOnPrefix: false,
    };
    const items = [];

    // 1) Integradas y sentencias
    for (const [nombre, d] of Object.entries(DOCS_BUILTINS)) items.push(itemBuiltin(nombre, d));
    for (const conv of CONVERSIONES) items.push(itemConversion(conv));

    // 2) Palabras clave y tipos
    for (const p of PALABRAS) items.push(itemPalabra(p, DOCS_KEYWORDS[p]));
    for (const t of TIPOS_DATO) {
      const item = new vscode.CompletionItem(t, vscode.CompletionItemKind.TypeParameter);
      item.detail = 'Tipo de dato';
      items.push(item);
    }

    // 3) Funciones y estructuras del documento
    const vivo = analisisVivo(documento);
    for (const f of vivo.funciones) items.push(itemFuncion(f, 'este archivo'));
    for (const e of vivo.estructuras) {
      const item = new vscode.CompletionItem(e.nombre, vscode.CompletionItemKind.Struct);
      item.detail = `estructura · línea ${e.linea}`;
      if (e.doc) item.documentation = md(bloqueDoc(e.doc));
      items.push(item);
    }

    // 4) Funciones de los módulos importados
    for (const imp of vivo.importaciones) {
      const res = await modulos.resolver(imp.nombre, opcionesModulos(documento));
      if (res.ok) {
        const doc = analisis.parsearArchivo(res.ruta);
        for (const f of doc.funciones) items.push(itemFuncion(f, `módulo ${imp.nombre}`));
      }
    }

    // 5) Funciones del resto del proyecto
    for (const uri of await archivosDelProyecto()) {
      if (uri.fsPath === documento.uri.fsPath) continue;
      const doc = analisisDe(uri);
      for (const f of doc.funciones) items.push(itemFuncion(f, path.basename(uri.fsPath)));
    }

    // Dedup por etiqueta+detalle (las del documento ganan)
    const vistos = new Set();
    const finales = [];
    for (const item of items) {
      const clave = typeof item.label === 'string' ? item.label : item.label.label;
      if (vistos.has(clave)) continue;
      vistos.add(clave);
      finales.push(item);
    }
    opciones.matchOnPrefix = true;
    void opciones;
    return finales.slice(0, acotarRaiz(400));
  } catch (e) {
    console.error('[cwin] completado:', e);
    return [];
  }
}

// ----------------------------------------------------- proveedor de definición

async function proveerDefinicion(documento, posicion) {
  try {
    const rango = documento.getWordRangeAtPosition(posicion, /[A-Za-z_][\w.]*/);
    if (!rango) return null;
    const palabra = documento.getText(rango);
    const linea = documento.lineAt(posicion.line).text;
    const antes = linea.slice(0, rango.start.character).trim();
    if (antes.startsWith('#')) return null;

    // En un importar: saltar al archivo del módulo
    if (antes.startsWith('importar')) {
      const res = await modulos.resolver(palabra, opcionesModulos(documento));
      if (res.ok) return new vscode.Location(vscode.Uri.file(res.ruta), new vscode.Position(0, 0));
      return null;
    }

    const vivo = analisisVivo(documento);
    let f = vivo.funciones.find((x) => x.nombre === palabra);
    if (f) return new vscode.Location(documento.uri, new vscode.Position(f.linea - 1, 0));
    let e = vivo.estructuras.find((x) => x.nombre === palabra);
    if (e) return new vscode.Location(documento.uri, new vscode.Position(e.linea - 1, 0));

    const hitF = await buscarFuncion(palabra, documento);
    if (hitF) return new vscode.Location(vscode.Uri.file(hitF.archivo), new vscode.Position(hitF.f.linea - 1, 0));
    const hitE = await buscarEstructura(palabra, documento);
    if (hitE) return new vscode.Location(documento.uri, new vscode.Position(hitE.linea - 1, 0));
    return null;
  } catch (err) {
    console.error('[cwin] definición:', err);
    return null;
  }
}

// -------------------------------------------------------- proveedor de símbolos

function simbolo(clase, nombre, tipo, lineaInicio, lineaFin, documento) {
  const inicio = new vscode.Position(Math.max(0, lineaInicio - 1), 0);
  const fin = new vscode.Position(Math.min(documento.lineCount - 1, Math.max(lineaInicio - 1, lineaFin - 1)), 0);
  const s = new vscode.DocumentSymbol(nombre, '', tipo, new vscode.Range(inicio, fin), new vscode.Range(inicio, inicio.translate(0, Math.min(nombre.length + clase.length + 9, documento.lineAt(inicio).text.length))));
  return s;
}

function proveerSimbolos(documento) {
  try {
    const vivo = analisisVivo(documento);
    const simbolos = [];
    for (const f of vivo.funciones) {
      simbolos.push(simbolo(f.clase, f.nombre, vscode.SymbolKind.Function, f.linea, f.fin, documento));
    }
    for (const e of vivo.estructuras) {
      simbolos.push(simbolo('estructura', e.nombre, vscode.SymbolKind.Interface, e.linea, e.fin, documento));
    }
    for (const imp of vivo.importaciones) {
      simbolos.push(simbolo('importar', imp.nombre, vscode.SymbolKind.Module, imp.linea, imp.linea, documento));
    }
    return simbolos;
  } catch (e) {
    console.error('[cwin] símbolos:', e);
    return [];
  }
}

// ------------------------------------------------------------------ diagnósticos

async function diagnosticar(documento) {
  if (!coleccionDiag) return;
  try {
    const avisos = [];
    if (config().get('diagnosticos.importaciones') !== false) {
      const vivo = analisisVivo(documento);
      for (const imp of vivo.importaciones) {
        const res = await modulos.resolver(imp.nombre, opcionesModulos(documento));
        if (!res.ok) {
          const linea = documento.lineAt(Math.min(imp.linea - 1, documento.lineCount - 1));
          const idx = Math.max(0, linea.text.indexOf(imp.nombre));
          const rango = new vscode.Range(imp.linea - 1, idx, imp.linea - 1, idx + imp.nombre.length);
          const d = new vscode.Diagnostic(
            rango,
            `Módulo '${imp.nombre}' no encontrado. Se buscó ${imp.nombre}.cwn / .wn / .wini junto a este archivo, en carpetas 'modulos/', junto al compilador detectado y en 'cwin.modulos.carpetasExtra'.`,
            vscode.DiagnosticSeverity.Warning
          );
          d.source = 'Cwin módulos';
          avisos.push(d);
        }
      }
    }
    coleccionDiag.set(documento.uri, avisos);
  } catch (e) {
    console.error('[cwin] diagnósticos:', e);
  }
}

function diagnosticarConRetardo(documento) {
  if (!documento || !SELECTORES.some((s) => s.language === documento.languageId)) return;
  const clave = documento.uri.toString();
  if (temporizadores[clave]) clearTimeout(temporizadores[clave]);
  temporizadores[clave] = setTimeout(() => diagnosticar(documento), 400);
}

// ------------------------------------------------------------------ barra de estado

async function actualizarBarra() {
  if (!barra) return;
  try {
    estado.compilador = await modulos.buscarCompilador(config().get('compilador.ruta'), false);
  } catch (e) {
    console.error('[cwin] compilador:', e);
  }
  try {
    const mods = await modulos.buscarModulos(opcionesModulos(null));
    estado.contadorModulos = mods.length;
  } catch (e) {
    estado.contadorModulos = 0;
  }
  barra.text = estado.compilador ? `$(gear) cwini ✓ ${estado.contadorModulos} mód.` : '$(gear) cwini ✗';
  const ruta = estado.compilador
    ? `Compilador: \`${estado.compilador.ruta}\`\n(${estado.compilador.fuente})`
    : '**No se encontró el compilador.** Probado en silencio: `where cwini` / `which cwini` y también `winic`.';
  barra.tooltip = md(`**Cwin**\n\n${ruta}\n\nMódulos detectados: **${estado.contadorModulos}**\n\nClic para ver la lista de módulos.`);
  barra.show();
}

// ---------------------------------------------------------------------- comandos

async function comandoBuscarCompilador() {
  modulos.invalidarCompilador();
  estado.compilador = await modulos.buscarCompilador(config().get('compilador.ruta'), true);
  if (estado.compilador) {
    vscode.window.showInformationMessage(
      `Compilador Cwin encontrado: ${estado.compilador.ruta} (vía ${estado.compilador.fuente})`
    );
  } else {
    vscode.window.showWarningMessage(
      'No se encontró el compilador (probé en silencio "where cwini"/"which cwini" y también "winic"). Configura "cwin.compilador.ruta" o agrégalo al PATH.'
    );
  }
  await actualizarBarra();
}

async function comandoListarModulos() {
  const editor = vscode.window.activeTextEditor;
  const mods = await modulos.buscarModulos(opcionesModulos(editor && editor.document));
  if (!mods.length) {
    vscode.window.showInformationMessage(
      'No se encontraron módulos .cwn/.wn/.wini. Colócalos en una carpeta "modulos/", junto al archivo actual o junto al compilador detectado, o configura "cwin.modulos.carpetasExtra".'
    );
    return;
  }
  const items = mods.map((m) => ({
    label: `$(package) ${m.nombre}`,
    description: `${m.funciones.length} función(es)`,
    detail: m.ruta,
    mod: m,
  }));
  const elegido = await vscode.window.showQuickPick(items, {
    placeHolder: `Módulos Cwin (${mods.length}) — compilador: ${estado.compilador ? estado.compilador.ruta : 'no encontrado'}`,
  });
  if (elegido) {
    vscode.workspace.openTextDocument(vscode.Uri.file(elegido.mod.ruta)).then((d) => vscode.window.showTextDocument(d));
  }
}

async function comandoReescanear() {
  modulos.invalidarModulos();
  modulos.invalidarCompilador();
  cacheArchivos.clear();
  await actualizarBarra();
  for (const doc of vscode.workspace.textDocuments) diagnosticarConRetardo(doc);
  vscode.window.showInformationMessage('Cwin: módulos reescaneados.');
}

async function comandoActivarTema() {
  await vscode.workspace
    .getConfiguration('workbench')
    .update('colorTheme', 'Cwin Dark', vscode.ConfigurationTarget.Global);
  vscode.window.showInformationMessage('Tema "Cwin Dark" activado: todo el código Cwin tiene color, hasta paréntesis y comas.');
}

function preguntarTemaUnaVez(contexto) {
  if (contexto.globalState.get('cwin.preguntoTema')) return;
  contexto.globalState.update('cwin.preguntoTema', true);
  vscode.window
    .showInformationMessage(
      'Cwin: para que TODO el código tenga color (incluso paréntesis, comas y llaves), activa el tema incluido "Cwin Dark".',
      'Activar tema',
      'Ahora no'
    )
    .then(async (elegido) => {
      if (elegido === 'Activar tema') await comandoActivarTema();
    });
}

// ------------------------------------------------------------------------ activate

function activate(contexto) {
  coleccionDiag = vscode.languages.createDiagnosticCollection('cwin');
  contexto.subscriptions.push(coleccionDiag);

  barra = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 50);
  barra.command = 'cwin.listarModulos';
  contexto.subscriptions.push(barra);

  contexto.subscriptions.push(
    vscode.languages.registerHoverProvider(SELECTORES, { provideHover: proveerHover }),
    vscode.languages.registerCompletionItemProvider(SELECTORES, { provideCompletionItems: proveerCompletado }, '.'),
    vscode.languages.registerDefinitionProvider(SELECTORES, { provideDefinition: proveerDefinicion }),
    vscode.languages.registerDocumentSymbolProvider(SELECTORES, { provideDocumentSymbols: proveerSimbolos }),

    vscode.commands.registerCommand('cwin.buscarCompilador', comandoBuscarCompilador),
    vscode.commands.registerCommand('cwin.listarModulos', comandoListarModulos),
    vscode.commands.registerCommand('cwin.reescanearModulos', comandoReescanear),
    vscode.commands.registerCommand('cwin.activarTema', comandoActivarTema),

    vscode.workspace.onDidChangeTextDocument((ev) => diagnosticarConRetardo(ev.document)),
    vscode.workspace.onDidOpenTextDocument((d) => diagnosticarConRetardo(d)),
    vscode.workspace.onDidSaveTextDocument((d) => diagnosticarConRetardo(d)),
    vscode.workspace.onDidChangeConfiguration((ev) => {
      if (ev.affectsConfiguration('cwin')) comandoReescanear();
    })
  );

  const activo = vscode.window.activeTextEditor;
  if (activo) diagnosticarConRetardo(activo.document);
  actualizarBarra();
  preguntarTemaUnaVez(contexto);
}

function deactivate() {
  for (const t of Object.values(temporizadores)) clearTimeout(t);
}

module.exports = { activate, deactivate };
