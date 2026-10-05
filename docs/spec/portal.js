(function () {
  'use strict';
  const normalize = value => String(value).normalize('NFD').replace(/[\u0300-\u036f]/g, '').toLowerCase().replace(/\s+/g, ' ').trim();
  const matches = (text, query, statuses, state) => {
    const haystack = normalize(text);
    return normalize(query).split(' ').filter(Boolean).every(word => haystack.includes(word)) && (!state || statuses.split(' ').includes(state));
  };
  const getStoredTheme = storage => {
    try { const value = storage.getItem('router-spec-theme'); return ['light', 'dark', 'system'].includes(value) ? value : 'system'; }
    catch { return 'system'; }
  };
  if (typeof module !== 'undefined' && module.exports) module.exports = { normalize, matches, getStoredTheme };
  if (typeof document === 'undefined') return;
  const search = document.getElementById('search');
  const state = document.getElementById('state-filter');
  const clear = document.getElementById('clear-filters');
  const count = document.getElementById('result-count');
  const empty = document.getElementById('empty-results');
  const articles = [...document.querySelectorAll('.doc-section')];
  const nav = [...document.querySelectorAll('nav li[data-section]')];
  const texts = new Map(articles.map(a => [a.id, a.textContent + ' ' + a.dataset.chapter]));
  function filter() {
    let visible = 0;
    for (const a of articles) { a.hidden = !matches(texts.get(a.id), search.value, a.dataset.status, state.value); if (!a.hidden) visible++; }
    // Preserve chapter headings when a matching child is visible, without retaining unrelated chapter text.
    for (const a of articles.filter(a => a.dataset.level === '2' && a.hidden)) {
      if (articles.some(child => child.dataset.chapter === a.dataset.chapter && child.dataset.level === '3' && !child.hidden)) {
        a.hidden = false; a.classList.add('heading-only');
      } else a.classList.remove('heading-only');
    }
    for (const a of articles) if (matches(texts.get(a.id), search.value, a.dataset.status, state.value)) a.classList.remove('heading-only');
    for (const item of nav) item.hidden = document.getElementById(item.dataset.section)?.hidden ?? false;
    count.textContent = search.value || state.value ? `${visible} de ${articles.length} secciones coinciden` : `${articles.length} secciones · documento completo`;
    empty.hidden = visible > 0; clear.hidden = !search.value && !state.value;
  }
  search.addEventListener('input', filter); state.addEventListener('change', filter);
  clear.addEventListener('click', () => { search.value = ''; state.value = ''; filter(); search.focus(); });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && document.activeElement === search) { search.value = ''; filter(); }
    if (event.key === '/' && !event.ctrlKey && !event.metaKey && !event.altKey && !/INPUT|TEXTAREA|SELECT/.test(document.activeElement?.tagName ?? '') && !document.activeElement?.isContentEditable) { event.preventDefault(); search.focus(); }
  });
  const themeButton = document.getElementById('theme-toggle');
  let storage;
  try { storage = globalThis.localStorage; } catch { storage = undefined; }
  let theme = getStoredTheme(storage);
  function applyTheme() {
    if (theme === 'system') document.documentElement.removeAttribute('data-theme'); else document.documentElement.dataset.theme = theme;
    const label = { system: 'sistema', light: 'claro', dark: 'oscuro' }[theme];
    themeButton.textContent = `Tema: ${label}`; themeButton.setAttribute('aria-label', `Cambiar tema; actual: ${label}`);
  }
  themeButton.addEventListener('click', () => { theme = { system: 'light', light: 'dark', dark: 'system' }[theme]; try { storage?.setItem('router-spec-theme', theme); } catch {} applyTheme(); });
  applyTheme(); filter();
  const links = [...document.querySelectorAll('nav a')];
  function highlight(id) {
    for (const link of links) { const active = link.hash === '#' + id; link.classList.toggle('active', active); if (active) link.setAttribute('aria-current', 'location'); else link.removeAttribute('aria-current'); }
  }
  for (const link of links) link.addEventListener('click', () => highlight(link.hash.slice(1)));
  if ('IntersectionObserver' in globalThis) {
    const observer = new IntersectionObserver(entries => {
      const first = entries.filter(e => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0];
      if (first) highlight(first.target.id);
    }, { rootMargin: '-72px 0px -65% 0px', threshold: 0 });
    document.querySelectorAll('.doc-section h2, .doc-section h3, .doc-section h4').forEach(h => observer.observe(h));
  }
  if (location.hash) highlight(location.hash.slice(1));
}());
