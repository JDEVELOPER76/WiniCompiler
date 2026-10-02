'use strict';

// ---------------------------------------------------------------------------
// Análisis ligero de documentos Cwin/Wini basado en expresiones regulares.
// Extrae: funciones, funciones externas, estructuras, importaciones,
// comentarios de documentación encima de cada declaración y el docstring
// `#@` (que el parser del compilador también consume).
// No sustituye al compilador: solo alimenta hover, autocompletado,
// símbolos y diagnósticos de importaciones.
// ---------------------------------------------------------------------------

const fs = require('fs');

// Extensiones que el compilador reconoce como módulos/fuente.
const EXT_MODULO = ['.cwn', '.wn', '.wini'];

// Divide por un separador respetando el anidado de <>, (), [] y {}.
function dividirTopLevel(texto, sep) {
  const partes = [];
  let nivel = 0;
  let actual = '';
  for (const ch of texto) {
    if ('<([{'.includes(ch)) nivel++;
    else if ('>)}]'.includes(ch)) nivel = Math.max(0, nivel - 1);
    if (ch === sep && nivel === 0) {
      partes.push(actual);
      actual = '';
    } else {
      actual += ch;
    }
  }
  if (actual.trim() !== '' || partes.length) partes.push(actual);
  return partes.map((p) => p.trim()).filter(Boolean);
}

// Cola de la firma: todo lo que va después del ')' — extrae el tipo de
// retorno tolerando `: tipo`, `-> tipo`, `: tipo:` y comentarios finales.
function parsearTipoRetorno(cola) {
  let t = String(cola || '').replace(/#.*$/, '').trim();
  if (!t) return '';
  if (t.startsWith('->')) t = t.slice(2).trim();
  else if (t.startsWith(':')) t = t.slice(1).trim();
  return t.replace(/:\s*$/, '').trim();
}

// `a: entero` / `*resto: lista<entero>` / `b: entero = 5`
function parsearParametros(texto) {
  return dividirTopLevel(texto, ',').map((p) => {
    if (p === '...') return { nombre: '...', tipo: '', variadico: true, defecto: '' }; // variádico a la C (externa)
    const variadico = p.startsWith('*');
    if (variadico) p = p.slice(1).trim();
    const m = p.match(/^([A-Za-z_]\w*)\s*:\s*([\s\S]+?)(?:\s*=\s*([\s\S]+))?$/);
    if (!m) return { nombre: p, tipo: '', variadico, defecto: '' };
    return { nombre: m[1], tipo: m[2].trim(), variadico, defecto: (m[3] || '').trim() };
  });
}

function indentacionDe(linea) {
  return linea.length - linea.trimLeft().length;
}

// Comentarios `#` consecutivos inmediatamente encima de la declaración
// (se pueden saltar líneas en blanco entre el bloque y la declaración).
function docArriba(lineas, indice) {
  const partes = [];
  let j = indice - 1;
  while (j >= 0) {
    const t = lineas[j].trim();
    if (t === '') {
      if (partes.length === 0) { j--; continue; } // aún no encontramos comentarios
      break;
    }
    if (t.startsWith('#')) {
      partes.unshift(t.replace(/^#@?\s?/, ''));
      j--;
      continue;
    }
    break;
  }
  return partes.join('\n');
}

// Docstring del cuerpo: líneas `#@ ...` (o una cadena de texto) al inicio
// del bloque — el compilador también las consume como documentación.
function docCuerpo(lineas, indice, indentacion) {
  const partes = [];
  for (let j = indice + 1; j < lineas.length; j++) {
    const l = lineas[j];
    const t = l.trim();
    if (t === '') continue;
    if (indentacionDe(l) <= indentacion) break;
    if (t.startsWith('#@')) {
      partes.push(t.replace(/^#@?\s?/, ''));
      continue;
    }
    if (partes.length === 0 && /^["']/.test(t)) {
      partes.push(t.replace(/^["']|["']$/g, ''));
      continue;
    }
    break;
  }
  return partes.join('\n');
}

// Última línea (0-based) del bloque indentado que abre la declaración.
function finDeBloque(lineas, indice, indentacion) {
  let ultimo = indice;
  for (let j = indice + 1; j < lineas.length; j++) {
    const t = lineas[j].trim();
    if (t === '') continue;
    if (indentacionDe(lineas[j]) > indentacion) ultimo = j;
    else break;
  }
  return ultimo;
}

function construirFirma(clase, nombre, params, retorno) {
  const ps = params.map((p) => {
    if (p.nombre === '...') return '...'; // variádico a la C
    const nom = (p.variadico ? '*' : '') + p.nombre;
    return p.defecto ? `${nom}: ${p.tipo} = ${p.defecto}` : `${nom}: ${p.tipo}`;
  });
  const cola = retorno ? `: ${retorno}` : '';
  return `${clase} ${nombre}(${ps.join(', ')})${cola}`;
}

function parsearDocumento(texto) {
  const lineas = String(texto || '').split(/\r?\n/);
  const res = { lineas, funciones: [], estructuras: [], importaciones: [] };
  let estructuraAbierta = null;

  for (let i = 0; i < lineas.length; i++) {
    const linea = lineas[i];
    const sinComentario = linea.replace(/#.*$/, '');
    let m;

    if ((m = sinComentario.match(/^\s*funcion\s+([A-Za-z_]\w*)\s*\(([^)]*)\)\s*(.*)$/))) {
      const indentacion = indentacionDe(lineas[i]);
      res.funciones.push({
        tipoNodo: 'funcion', clase: 'funcion', nombre: m[1],
        params: parsearParametros(m[2] || ''), retorno: parsearTipoRetorno(m[3] || ''),
        firma: construirFirma('funcion', m[1], parsearParametros(m[2] || ''), parsearTipoRetorno(m[3] || '')),
        linea: i + 1, fin: finDeBloque(lineas, i, indentacion) + 1, indentacion,
        doc: docArriba(lineas, i), docstring: docCuerpo(lineas, i, indentacion),
      });
      estructuraAbierta = null;
      continue;
    }

    if ((m = sinComentario.match(/^\s*externa\s+([A-Za-z_]\w*)\s*\(([^)]*)\)\s*(.*)$/))) {
      const indentacion = indentacionDe(lineas[i]);
      res.funciones.push({
        tipoNodo: 'funcion', clase: 'externa', nombre: m[1],
        params: parsearParametros(m[2] || ''), retorno: parsearTipoRetorno(m[3] || ''),
        firma: construirFirma('externa', m[1], parsearParametros(m[2] || ''), parsearTipoRetorno(m[3] || '')),
        linea: i + 1, fin: i + 1, indentacion,
        doc: docArriba(lineas, i), docstring: '',
      });
      estructuraAbierta = null;
      continue;
    }

    if ((m = sinComentario.match(/^\s*estructura\s+([A-Za-z_]\w*)\s*:?\s*$/))) {
      const indentacion = indentacionDe(lineas[i]);
      const item = {
        tipoNodo: 'estructura', nombre: m[1], linea: i + 1,
        fin: finDeBloque(lineas, i, indentacion) + 1, indentacion,
        campos: [], doc: docArriba(lineas, i),
      };
      res.estructuras.push(item);
      estructuraAbierta = item;
      continue;
    }

    if ((m = sinComentario.match(/^\s*importar\s+(?:"([^"]+)"|'([^']+)'|([A-Za-z_][\w.]*))(?:\s+como\s+([A-Za-z_]\w*))?\s*$/))) {
      res.importaciones.push({
        nombre: m[1] || m[2] || m[3], cita: !!(m[1] || m[2]),
        alias: m[4] || '', linea: i + 1,
      });
      continue;
    }

    // Campo de una estructura abierta: `nombre: tipo`
    if (estructuraAbierta) {
      const fm = linea.match(/^\s+([A-Za-z_]\w*)\s*:\s*([^\n#]+?)\s*$/);
      if (fm && indentacionDe(linea) > estructuraAbierta.indentacion) {
        estructuraAbierta.campos.push({ nombre: fm[1], tipo: fm[2].trim() });
        continue;
      }
      if (linea.trim() !== '' && indentacionDe(linea) === 0) estructuraAbierta = null;
    }
  }
  return res;
}

function parsearArchivo(ruta) {
  try {
    return parsearDocumento(fs.readFileSync(ruta, 'utf8'));
  } catch {
    return { lineas: [], funciones: [], estructuras: [], importaciones: [] };
  }
}

module.exports = { parsearDocumento, parsearArchivo, dividirTopLevel, EXT_MODULO };
