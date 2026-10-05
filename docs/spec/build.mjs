import { readFile, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { dirname, basename, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const states = ['Implementado', 'Cubierto', 'Pendiente', 'Local', 'Histórico'];
export const escapeHtml = value => String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const normalize = value => value.normalize('NFD').replace(/[\u0300-\u036f]/g, '').toLowerCase();

export function safeHref(value) {
  const href = value.trim();
  if (!href || /[\u0000-\u0020\u007f\\]/.test(href) || href.startsWith('//')) return null;
  let decoded = href;
  try { decoded = decodeURIComponent(href); } catch { return null; }
  if (/[\u0000-\u001f\u007f\\]/.test(decoded) || decoded.startsWith('//')) return null;
  const scheme = /^([a-z][a-z\d+.-]*):/i.exec(decoded);
  return scheme && !/^https?$/i.test(scheme[1]) ? null : href;
}

export function inline(source, depth = 0) {
  if (depth > 6) return escapeHtml(source);
  let result = '';
  for (let i = 0; i < source.length;) {
    const tail = source.slice(i);
    if (tail[0] === '\\' && tail.length > 1) { result += escapeHtml(tail[1]); i += 2; continue; }
    const ticks = /^`+/.exec(tail);
    if (ticks) {
      const end = source.indexOf(ticks[0], i + ticks[0].length);
      if (end >= 0) { result += '<code>' + escapeHtml(source.slice(i + ticks[0].length, end)) + '</code>'; i = end + ticks[0].length; continue; }
    }
    const link = /^\[([^\]\n]+)\]\((?:<([^>\n]*)>|([^\s)\n]+))(?:\s+"([^"\n]*)")?\)/.exec(tail);
    if (link) {
      const href = safeHref(link[2] ?? link[3]);
      const label = inline(link[1], depth + 1);
      result += href ? `<a href="${escapeHtml(href)}"${/^https?:/i.test(href) ? ' rel="noopener noreferrer"' : ''}${link[4] ? ` title="${escapeHtml(link[4])}"` : ''}>${label}</a>` : label;
      i += link[0].length; continue;
    }
    const marker = /^(\*\*|__|\*|_)/.exec(tail);
    if (marker) {
      const end = source.indexOf(marker[0], i + marker[0].length);
      if (end > i + marker[0].length) {
        const tag = marker[0].length === 2 ? 'strong' : 'em';
        result += `<${tag}>${inline(source.slice(i + marker[0].length, end), depth + 1)}</${tag}>`;
        i = end + marker[0].length; continue;
      }
    }
    result += escapeHtml(source[i++]);
  }
  return result;
}

export function tableCells(line) {
  const cells = []; let current = ''; let tick = false;
  const text = line.trim().replace(/^\|/, '').replace(/(?<!\\)\|$/, '');
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '\\' && text[i + 1] === '|') { current += '\\|'; i++; }
    else if (text[i] === '`') { tick = !tick; current += '`'; }
    else if (text[i] === '|' && !tick) { cells.push(current.trim()); current = ''; }
    else current += text[i];
  }
  cells.push(current.trim()); return cells;
}

function statusTags(source) {
  const found = new Set();
  for (const line of source.split('\n')) {
    const plain = line.replace(/\*\*|__/g, '').trim();
    const metadata = /^(?:Estado|Estados|Cobertura|Verificación|Alcance)\s*:\s*(.*)$/i.exec(plain);
    const candidates = metadata ? [metadata[1]] : plain.startsWith('|') ? tableCells(plain) : [];
    for (const candidate of candidates) for (const state of states) {
      if (new RegExp(`(?:^|[^a-z])${normalize(state)}(?:$|[^a-z])`).test(normalize(candidate))) found.add(state);
    }
  }
  return [...found];
}

function renderBlocks(lines, makeHeading) {
  let html = '';
  const isTable = i => i + 1 < lines.length && lines[i].includes('|') && tableCells(lines[i + 1]).every(c => /^:?-{3,}:?$/.test(c));
  const listMatch = line => /^(\s*)([-+*]|\d+[.)])\s+(.+)$/.exec(line);
  const isSpecial = i => /^\s*```/.test(lines[i]) || /^#{1,4}\s/.test(lines[i]) || !!listMatch(lines[i]) || isTable(i);
  function renderList(start, indent) {
    const first = listMatch(lines[start]); const ordered = /^\d/.test(first[2]);
    const tag = ordered ? 'ol' : 'ul'; let out = `<${tag}${ordered && parseInt(first[2]) !== 1 ? ` start="${parseInt(first[2])}"` : ''}>`; let i = start;
    while (i < lines.length) {
      const item = listMatch(lines[i]);
      if (!item || item[1].length !== indent || /^\d/.test(item[2]) !== ordered) break;
      out += '<li>' + inline(item[3]); i++;
      while (i < lines.length) {
        const child = listMatch(lines[i]);
        if (child && child[1].length > indent) { const next = renderList(i, child[1].length); out += next.html; i = next.end; }
        else if (lines[i].trim() && /^\s+/.test(lines[i]) && !child) { out += ' ' + inline(lines[i].trim()); i++; }
        else break;
      }
      out += '</li>';
      if (!lines[i]?.trim() && listMatch(lines[i + 1] ?? '')?.[1].length === indent) i++;
    }
    return { html: out + `</${tag}>`, end: i };
  }
  for (let i = 0; i < lines.length;) {
    if (!lines[i].trim()) { i++; continue; }
    const fence = /^\s*```([\w+-]*)\s*$/.exec(lines[i]);
    if (fence) {
      const code = []; i++;
      while (i < lines.length && !/^\s*```\s*$/.test(lines[i])) code.push(lines[i++]);
      if (i < lines.length) i++;
      html += `<pre${fence[1] ? ` data-language="${escapeHtml(fence[1])}"` : ''}><code>${escapeHtml(code.join('\n'))}</code></pre>`; continue;
    }
    const heading = /^(#{1,4})\s+(.+?)\s*#*\s*$/.exec(lines[i]);
    if (heading) { html += makeHeading(heading[1].length, heading[2]); i++; continue; }
    if (isTable(i)) {
      const header = tableCells(lines[i]); const align = tableCells(lines[i + 1]); i += 2;
      const alignment = n => align[n]?.startsWith(':') && align[n]?.endsWith(':') ? 'center' : align[n]?.endsWith(':') ? 'right' : 'left';
      html += '<div class="table-scroll" tabindex="0" role="region" aria-label="Tabla de especificaciones"><table><thead><tr>';
      html += header.map((cell, n) => `<th scope="col" class="align-${alignment(n)}">${inline(cell)}</th>`).join('') + '</tr></thead><tbody>';
      while (i < lines.length && lines[i].trim() && lines[i].includes('|')) {
        const row = tableCells(lines[i++]);
        html += '<tr>' + header.map((_, n) => `<td class="align-${alignment(n)}">${inline(row[n] ?? '')}</td>`).join('') + '</tr>';
      }
      html += '</tbody></table></div>'; continue;
    }
    const item = listMatch(lines[i]);
    if (item) { const list = renderList(i, item[1].length); html += list.html; i = list.end; continue; }
    const paragraph = [lines[i++].trim()];
    while (i < lines.length && lines[i].trim() && !isSpecial(i)) paragraph.push(lines[i++].trim());
    html += `<p>${inline(paragraph.join(' '))}</p>`;
  }
  return html;
}

export function parseMarkdown(source) {
  if (typeof source !== 'string' || source.includes('\0')) throw new Error('La fuente debe ser texto Markdown UTF-8.');
  const lines = source.replace(/^\uFEFF/, '').replace(/\r\n?/g, '\n').split('\n');
  const used = new Map(); const headings = []; const sections = []; let title = 'Especificación del router'; let chapter = ''; let active; let fenced = false;
  const slug = text => {
    const base = normalize(text.replace(/[*_`]/g, '')).replace(/[^a-z\d]+/g, '-').replace(/^-|-$/g, '') || 'seccion';
    const count = (used.get(base) ?? 0) + 1; used.set(base, count); return base + (count > 1 ? `-${count}` : '');
  };
  const headingHtml = (level, text) => {
    const id = slug(text); headings.push({ level, text: text.replace(/[*_`]/g, ''), id, section: active?.id });
    return `<h${level} id="${id}">${inline(text)}<a class="heading-link" href="#${id}" aria-label="Enlace a ${escapeHtml(text.replace(/[*_`]/g, ''))}">#</a></h${level}>`;
  };
  const finish = () => {
    if (!active) return;
    active.status = statusTags(active.lines.join('\n'));
    active.html += renderBlocks(active.lines, headingHtml); delete active.lines;
    sections.push(active);
  };
  for (const line of lines) {
    if (!active && !line.trim()) continue;
    if (/^\s*```/.test(line)) fenced = !fenced;
    const h = !fenced && /^(#{1,3})\s+(.+?)\s*#*\s*$/.exec(line);
    if (h && h[1].length === 1 && sections.length === 0 && !active) { title = h[2].replace(/[*_`]/g, ''); continue; }
    if (h && h[1].length >= 2) {
      finish(); if (h[1].length === 2) chapter = h[2];
      active = { id: `article-${sections.length + 1}`, level: h[1].length, chapter, label: h[2], lines: [], html: '' };
      active.html = headingHtml(active.level, h[2]); continue;
    }
    active ??= { id: `article-${sections.length + 1}`, level: 2, chapter: '', label: 'Introducción', lines: [], html: '' };
    active.lines.push(line);
  }
  finish();
  return { title, headings, sections };
}

export function renderDocument(model, { css, script, sourceName, hash }) {
  const navigation = model.headings.filter(h => h.level > 1).map(h => `<li class="nav-level-${h.level}" data-section="${h.section}"><a href="#${h.id}">${escapeHtml(h.text)}</a></li>`).join('');
  const articles = model.sections.map(s => `<article class="doc-section" id="${s.id}" data-level="${s.level}" data-chapter="${escapeHtml(s.chapter)}" data-status="${escapeHtml(s.status.map(normalize).join(' '))}">${s.status.length ? `<div class="badges" aria-label="Etiquetas de estado">${s.status.map(state => `<span class="badge badge-${normalize(state)}">${state}</span>`).join('')}</div>` : ''}${s.html}</article>`).join('\n');
  return `<!doctype html>
<html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><title>${escapeHtml(model.title)}</title><style>${css.replace(/<\/style/gi, '<\\/style')}</style></head>
<body><a class="skip-link" href="#content">Saltar al documento</a>
<header class="topbar"><a class="brand" href="#content"><span class="brand-mark" aria-hidden="true">R</span><span>Router<span class="brand-sub">Especificación técnica</span></span></a><button id="theme-toggle" type="button" aria-label="Cambiar tema">Tema: sistema</button></header>
<div class="layout"><aside class="sidebar"><div class="tools"><label for="search">Buscar en el documento</label><input id="search" type="search" placeholder="Función, requisito, componente…" autocomplete="off"><div class="filter-row"><label for="state-filter">Estado</label><select id="state-filter"><option value="">Todos</option>${states.map(state => `<option value="${normalize(state)}">${state}</option>`).join('')}</select></div><p id="result-count" class="result-count" role="status" aria-live="polite">Documento completo</p><button id="clear-filters" type="button" hidden>Limpiar filtros</button></div><nav aria-label="Índice del documento"><ol>${navigation}</ol></nav><p class="offline-note">Lectura local · sin dependencias de red</p></aside>
<main id="content" tabindex="-1"><div class="document-title"><p class="eyebrow">Fuente única · revisión trazable</p><h1>${escapeHtml(model.title)}</h1><p class="document-hint">Los estados distinguen implementación, cobertura de pruebas y asuntos pendientes; no equivalen a aceptación funcional.</p></div><noscript><p class="notice">El documento completo se puede leer sin JavaScript. Para buscar, utiliza Ctrl+F; los filtros requieren JavaScript.</p></noscript><p id="empty-results" class="notice" hidden>No hay secciones que coincidan. Prueba otras palabras o elimina el filtro de estado.</p>${articles}<footer class="document-footer"><p>Fuente: <code>${escapeHtml(sourceName)}</code></p><p>SHA-256 de la fuente: <code>${escapeHtml(hash)}</code></p><p>El HTML es una vista generada. Modifica la fuente Markdown para actualizarlo.</p></footer></main></div>
<script>${script.replace(/<\/script/gi, '<\\/script')}</script></body></html>\n`;
}

export async function build(sourcePath = resolve(here, 'SPECIFICATION.md'), outputPath = resolve(here, 'index.html')) {
  const buffer = await readFile(sourcePath);
  if (buffer.length > 2 * 1024 * 1024) throw new Error('La fuente supera el límite de 2 MiB.');
  const source = new TextDecoder('utf-8', { fatal: true }).decode(buffer);
  const model = parseMarkdown(source);
  const [css, script] = await Promise.all([readFile(resolve(here, 'portal.css'), 'utf8'), readFile(resolve(here, 'portal.js'), 'utf8')]);
  const hash = createHash('sha256').update(buffer).digest('hex');
  const html = renderDocument(model, { css, script, sourceName: basename(sourcePath), hash });
  await writeFile(outputPath, html, 'utf8');
  return { outputPath, hash, sections: model.sections.length, bytes: Buffer.byteLength(html) };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { const result = await build(process.argv[2], process.argv[3]); console.log(`Portal generado: ${result.outputPath} (${result.sections} secciones, ${result.bytes} bytes).`); }
  catch (error) { console.error(`No se ha generado el portal: ${error.message}`); process.exitCode = 1; }
}
