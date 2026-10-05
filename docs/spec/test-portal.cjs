const test = require('node:test');
const assert = require('node:assert/strict');
const { mkdtemp, readFile, writeFile, rm } = require('node:fs/promises');
const { tmpdir } = require('node:os');
const { join } = require('node:path');
const { runInNewContext } = require('node:vm');
const helpers = require('./portal.js');
const generator = import('./build.mjs');

test('Markdown controlado: títulos, listas, tabla y código preservan semántica', async () => {
  const { parseMarkdown } = await generator;
  const doc = parseMarkdown('# Especificación\n\n## Resumen\nTexto **fuerte** y *énfasis*.\n\n### ROU-001\nEstado: Implementado · Cubierto\n\n- Cuenta\n  - Secundaria\n- Petición\n\n1. Inicio\n2. Fin\n\n#### Evidencia\n| Campo | Estado |\n| :--- | ---: |\n| `a|b` | Local |\n\n```text\n<agente> -> cuenta\n## No es título\n```');
  assert.equal(doc.title, 'Especificación');
  assert.deepEqual(doc.headings.map(h => h.level), [2, 3, 4]);
  assert.match(doc.sections[0].html, /<strong>fuerte<\/strong>/);
  const section = doc.sections[1];
  assert.deepEqual(section.status, ['Implementado', 'Cubierto', 'Local']);
  assert.match(section.html, /<ul><li>Cuenta<ul><li>Secundaria<\/li><\/ul><\/li>/);
  assert.match(section.html, /<ol><li>Inicio<\/li><li>Fin<\/li><\/ol>/);
  assert.match(section.html, /<th scope="col" class="align-right">Estado/);
  assert.match(section.html, /<code>a\|b<\/code>/);
  assert.match(section.html, /&lt;agente&gt; -&gt; cuenta\n## No es título/);
});

test('Anclas únicas y etiquetas sólo desde metadatos o celdas', async () => {
  const { parseMarkdown } = await generator;
  const doc = parseMarkdown('# Título\n## Arquitectura\nNo está Implementado; esto no es metadata.\n### Mis cuentas\n**Estado:** Pendiente\n### Mis cuentas\nVerificación: Histórico');
  assert.deepEqual(doc.headings.map(h => h.id), ['arquitectura', 'mis-cuentas', 'mis-cuentas-2']);
  assert.deepEqual(doc.sections.map(s => s.status), [[], ['Pendiente'], ['Histórico']]);
});

test('HTML crudo y enlaces ejecutables nunca se convierten en contenido activo', async () => {
  const { inline, safeHref, parseMarkdown } = await generator;
  assert.equal(safeHref('javascript:alert(1)'), null);
  assert.equal(safeHref('java%73cript:alert'), null);
  assert.equal(safeHref('data:text/html,hello'), null);
  assert.equal(safeHref('//evil.invalid/a'), null);
  assert.equal(safeHref('https://safe.invalid/a%0a'), null);
  assert.equal(safeHref('file:///C:/secret'), null);
  assert.equal(safeHref('../research/evidence.md'), '../research/evidence.md');
  assert.equal(safeHref('../research/My%20Evidence.md'), '../research/My%20Evidence.md');
  assert.equal(safeHref('#rou-001'), '#rou-001');
  assert.equal(safeHref('https://github.com/a/b'), 'https://github.com/a/b');
  const html = inline('<img src=x onerror=alert(1)> [x](javascript:alert) [Git](https://github.com/a/b "Fuente")');
  assert.ok(!html.includes('<img'));
  assert.ok(!html.includes('href="javascript'));
  assert.match(html, /&lt;img/);
  assert.match(html, /rel="noopener noreferrer" title="Fuente"/);
  assert.match(parseMarkdown('## Seguridad\n```html\n</script><img onerror=x>\n```').sections[0].html, /&lt;\/script&gt;/);
});

test('Buscador independiente de acentos, AND de palabras y filtro de estado', () => {
  assert.equal(helpers.normalize(' PETICIÓN  histórica '), 'peticion historica');
  assert.ok(helpers.matches('Petición de una cuenta secundaria', 'cuenta petición', 'local pendiente', 'pendiente'));
  assert.ok(!helpers.matches('Petición secundaria', 'cuenta petición', 'pendiente', 'pendiente'));
  assert.ok(!helpers.matches('Petición cuenta', 'cuenta', 'implementado', 'pendiente'));
  assert.equal(helpers.getStoredTheme({ getItem: () => { throw new Error('file:// storage denied'); } }), 'system');
  assert.equal(helpers.getStoredTheme({ getItem: () => 'unknown' }), 'system');
  assert.equal(helpers.getStoredTheme({ getItem: () => 'dark' }), 'dark');
});

test('Interacciones del portal: búsqueda, cabeceras, vacío, limpieza, teclado y tema sin storage', async () => {
  const elements = new Map(); const documentEvents = {};
  const element = (id, textContent = '', dataset = {}) => {
    const classes = new Set(); const attributes = new Map();
    const el = { id, textContent, dataset, value: '', hidden: false, events: {}, tagName: 'DIV',
      classList: { add: c => classes.add(c), remove: c => classes.delete(c), contains: c => classes.has(c), toggle: (c, active) => active ? classes.add(c) : classes.delete(c) },
      setAttribute: (name, value) => attributes.set(name, value), removeAttribute: name => { attributes.delete(name); if (name === 'data-theme') delete dataset.theme; },
      addEventListener: (name, callback) => { el.events[name] = callback; }, focus: () => { doc.activeElement = el; } };
    elements.set(id, el); return el;
  };
  for (const id of ['search', 'state-filter', 'clear-filters', 'result-count', 'empty-results', 'theme-toggle']) element(id);
  const root = element('root');
  const chapter = element('chapter', 'Resumen general', { level: '2', chapter: 'Resumen', status: '' });
  const account = element('account', 'Selección de cuenta', { level: '3', chapter: 'Resumen', status: 'implementado cubierto' });
  const voice = element('voice', 'Modo voz', { level: '3', chapter: 'Resumen', status: 'pendiente' });
  const articles = [chapter, account, voice]; const nav = articles.map(a => element('nav-' + a.id, '', { section: a.id }));
  const doc = { activeElement: root, documentElement: root, getElementById: id => elements.get(id),
    querySelectorAll: selector => selector === '.doc-section' ? articles : selector === 'nav li[data-section]' ? nav : [],
    addEventListener: (name, callback) => { documentEvents[name] = callback; } };
  const context = { document: doc, location: { hash: '' } };
  Object.defineProperty(context, 'localStorage', { get() { throw new Error('file:// denied'); } });
  runInNewContext(await readFile(join(__dirname, 'portal.js'), 'utf8'), context);
  const search = elements.get('search'); const state = elements.get('state-filter'); const count = elements.get('result-count');
  search.value = 'voz'; search.events.input();
  assert.equal(voice.hidden, false); assert.equal(account.hidden, true);
  assert.equal(chapter.hidden, false); assert.ok(chapter.classList.contains('heading-only'));
  assert.equal(nav[1].hidden, true); assert.match(count.textContent, /1 de 3/);
  state.value = 'implementado'; state.events.change();
  assert.equal(elements.get('empty-results').hidden, false); assert.equal(chapter.hidden, true);
  elements.get('clear-filters').events.click();
  assert.equal(search.value, ''); assert.equal(state.value, ''); assert.equal(account.hidden, false);
  assert.ok(!chapter.classList.contains('heading-only')); assert.equal(doc.activeElement, search);
  search.value = 'cuenta'; search.events.input(); documentEvents.keydown({ key: 'Escape' });
  assert.equal(search.value, ''); assert.equal(voice.hidden, false);
  doc.activeElement = root; let prevented = false;
  documentEvents.keydown({ key: '/', preventDefault: () => { prevented = true; } });
  assert.equal(prevented, true); assert.equal(doc.activeElement, search);
  elements.get('theme-toggle').events.click(); assert.equal(root.dataset.theme, 'light');
  elements.get('theme-toggle').events.click(); assert.equal(root.dataset.theme, 'dark');
  elements.get('theme-toggle').events.click(); assert.equal(root.dataset.theme, undefined);
});

test('HTML autocontenido reproducible: visible sin JavaScript, accesible y sin fetch/CDN', async () => {
  const { build } = await generator;
  const directory = await mkdtemp(join(tmpdir(), 'router-spec-portal-'));
  try {
    const source = join(directory, 'SPECIFICATION.md'); const output = join(directory, 'index.html');
    await writeFile(source, '# Documento\n\n## Resumen\nContenido visible.\n\n### Petición\nEstado: Pendiente\nTexto final.');
    const result = await build(source, output); const first = await readFile(output, 'utf8');
    await build(source, output); assert.equal(first, await readFile(output, 'utf8'));
    assert.match(first, /<html lang="es">/);
    assert.match(first, /<article[^>]*>.*Contenido visible\./s);
    assert.match(first, /aria-live="polite"/);
    assert.match(first, /Saltar al documento/);
    assert.match(first, /<noscript>/);
    assert.match(first, /@media print/);
    assert.match(first, /prefers-color-scheme/);
    assert.match(first, new RegExp(result.hash));
    assert.ok(!/<(?:script|link)[^>]*(?:src|href)=/i.test(first));
    assert.ok(!/\b(?:fetch|XMLHttpRequest|WebSocket)\s*\(/.test(first));
    assert.ok(result.bytes < 100000);
    await writeFile(source, Buffer.from([0xff]));
    await assert.rejects(build(source, output));
    await writeFile(source, 'x'.repeat(2 * 1024 * 1024 + 1));
    await assert.rejects(build(source, output), /2 MiB/);
  } finally { await rm(directory, { recursive: true, force: true }); }
});
