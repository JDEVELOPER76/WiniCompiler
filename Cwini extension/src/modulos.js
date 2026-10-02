'use strict';

// ---------------------------------------------------------------------------
// Sistema de módulos mejorado.
//
// 1) Localiza el compilador en silencio ejecutando `where cwini` (Windows)
//    o `which cwini` (Linux/macOS) con execFile + windowsHide: no abre
//    terminal, no muestra salida, no molesta. Prueba también `winic`
//    (nombre del binario según main.go) y respeta la ruta configurada.
// 2) Con la carpeta del compilador (y la del proyecto / el archivo actual
//    / las carpetas extra configuradas) busca módulos .cwn / .wn / .wini
//    y extrae las funciones que exporta cada uno, para hover,
//    autocompletado, definición y diagnósticos de `importar`.
// ---------------------------------------------------------------------------

const path = require('path');
const fs = require('fs');
const { execFile } = require('child_process');
const analisis = require('./analisis');

const EXTS = analisis.EXT_MODULO;
const TTL_MS = 30000;

let cacheCompilador = null; // { valor, cuando }
let cacheModulos = null;    // { valor, cuando, raices }

// Ejecuta un comando de forma silenciosa: sin ventana (windowsHide), sin
// salida visible, con timeout. Devuelve las líneas de stdout o null.
function ejecutarSilencioso(cmd, args) {
  return new Promise((resolve) => {
    try {
      execFile(cmd, args, { windowsHide: true, timeout: 4000, encoding: 'utf8' }, (err, stdout) => {
        if (err) return resolve(null);
        const lineas = String(stdout || '')
          .split(/\r?\n/)
          .map((s) => s.trim())
          .filter(Boolean);
        resolve(lineas.length ? lineas : null);
      });
    } catch {
      resolve(null);
    }
  });
}

// Localiza el compilador: primero la ruta configurada; si no, el comando
// silencioso `where cwini` / `which cwini` (y `winic` como alternativa).
async function buscarCompilador(rutaConfig, forzar) {
  if (!forzar && cacheCompilador && Date.now() - cacheCompilador.cuando < TTL_MS) {
    return cacheCompilador.valor;
  }
  let valor = null;
  if (rutaConfig && String(rutaConfig).trim()) {
    const ruta = String(rutaConfig).trim();
    if (fs.existsSync(ruta)) {
      valor = { ruta, dir: path.dirname(ruta), alternativas: [ruta], fuente: 'configuración (cwin.compilador.ruta)' };
    }
  }
  if (!valor) {
    const donde = process.platform === 'win32' ? 'where' : 'which';
    for (const nombre of ['cwini', 'winic']) {
      const lineas = await ejecutarSilencioso(donde, [nombre]);
      if (lineas && lineas.length) {
        valor = {
          ruta: lineas[0], alternativas: lineas, dir: path.dirname(lineas[0]),
          fuente: `comando silencioso: ${donde} ${nombre}`,
        };
        break;
      }
    }
  }
  cacheCompilador = { valor, cuando: Date.now() };
  return valor;
}

function invalidarCompilador() {
  cacheCompilador = null;
}

function esArchivoModulo(archivo) {
  return EXTS.includes(path.extname(archivo).toLowerCase());
}

// Camina una carpeta hasta cierta profundidad y junta archivos módulo.
function listarArchivos(dir, profundidad) {
  const salida = [];
  let entradas;
  try {
    entradas = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return salida;
  }
  for (const e of entradas) {
    if (e.name.startsWith('.') || e.name === 'node_modules' || e.name === 'build') continue;
    const ruta = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (profundidad < 4) salida.push(...listarArchivos(ruta, profundidad + 1));
    } else if (esArchivoModulo(e.name)) {
      salida.push(ruta);
    }
  }
  return salida;
}

// Nombre de módulo estilo compilador: ruta relativa a la raíz, con '.' como
// separador y sin la carpeta convencional 'modulos/' al principio.
function nombreDeModulo(rutaArchivo, raiz) {
  let rel = path.relative(raiz, rutaArchivo).replace(/\\/g, '/');
  rel = rel.replace(/\.(cwn|wn|wini)$/i, '');
  if (rel.startsWith('modulos/')) rel = rel.slice('modulos/'.length);
  return rel.replace(/\//g, '.');
}

function extraerFuncionesDeModulo(doc) {
  return doc.funciones.map((f) => ({
    nombre: f.nombre,
    firma: f.firma,
    doc: f.doc || f.docstring || '',
    linea: f.linea,
  }));
}

// Busca módulos en: el archivo actual, el proyecto (+ su carpeta modulos/),
// la carpeta del compilador detectado (+ modulos/) y las carpetas extra.
async function buscarModulos(opciones) {
  const opc = opciones || {};
  if (cacheModulos && Date.now() - cacheModulos.cuando < TTL_MS) return cacheModulos.valor;

  const raices = [];
  const empujar = (r) => {
    try {
      if (r && fs.existsSync(r) && fs.statSync(r).isDirectory() && !raices.includes(r)) raices.push(r);
    } catch { /* ignorar raíces imposibles */ }
  };

  const carpetaActual = opc.carpetaActual || '';
  empujar(carpetaActual);
  if (carpetaActual) empujar(path.join(carpetaActual, 'modulos'));
  for (const p of opc.carpetasProyecto || []) {
    empujar(p);
    empujar(path.join(p, 'modulos'));
  }
  if (opc.compilador && opc.buscarJuntoAlCompilador !== false) {
    empujar(opc.compilador.dir);
    empujar(path.join(opc.compilador.dir, 'modulos'));
    empujar(path.join(opc.compilador.dir, '..', 'modulos'));
  }
  for (const extra of opc.carpetasExtra || []) {
    empujar(extra);
    empujar(path.join(extra, 'modulos'));
  }

  const vistos = new Map();
  for (const raiz of raices) {
    for (const archivo of listarArchivos(raiz, 0)) {
      if (vistos.has(archivo)) continue;
      const doc = analisis.parsearArchivo(archivo);
      vistos.set(archivo, {
        nombre: nombreDeModulo(archivo, raiz),
        ruta: archivo,
        raiz,
        funciones: extraerFuncionesDeModulo(doc),
      });
    }
  }

  const valor = Array.from(vistos.values());
  cacheModulos = { valor, cuando: Date.now(), raices };
  return valor;
}

function invalidarModulos() {
  cacheModulos = null;
}

function raicesDeBusqueda() {
  return cacheModulos ? cacheModulos.raices.slice() : [];
}

// Resuelve un nombre de `importar`:
//  1) contra el registro de módulos encontrados;
//  2) como archivo relativo a la carpeta actual (igual que el compilador:
//     cada '.' es una subcarpeta y se prueba .cwn, .wn y .wini).
async function resolver(nombreModulo, opciones) {
  const modulosLista = await buscarModulos(opciones);
  const limpio = String(nombreModulo || '').trim().replace(/^["']|["']$/g, '');
  if (!limpio) return { ok: false, buscadoEn: raicesDeBusqueda() };

  const hit = modulosLista.find((m) => m.nombre === limpio);
  if (hit) return { ok: true, mod: hit, ruta: hit.ruta, buscadoEn: raicesDeBusqueda() };

  const carpetaActual = (opciones && opciones.carpetaActual) || '';
  if (carpetaActual) {
    const relativo = /[\\/]/.test(limpio) ? limpio : limpio.replace(/\./g, path.sep);
    for (const ext of EXTS) {
      const ruta = path.join(carpetaActual, relativo + ext);
      if (fs.existsSync(ruta)) {
        const doc = analisis.parsearArchivo(ruta);
        return {
          ok: true, ruta,
          mod: { nombre: limpio, ruta, funciones: extraerFuncionesDeModulo(doc) },
          buscadoEn: raicesDeBusqueda(),
        };
      }
    }
  }
  return { ok: false, buscadoEn: raicesDeBusqueda() };
}

module.exports = { buscarCompilador, buscarModulos, resolver, invalidarCompilador, invalidarModulos, ejecutarSilencioso, raicesDeBusqueda };
