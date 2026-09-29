// Exercise the actual patched 26.924 footer bindings, independently of the
// lazy profile menu. All usage and accounts below are synthetic.
const assert = require('node:assert/strict');
const vm = require('node:vm');

function extractFunction(source, name) {
  const start = source.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `native ${name} exists`);
  let depth = 0, quote = '';
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    const c = source[i];
    if (quote) {
      if (c === '\\') i++;
      else if (c === quote) quote = '';
    } else if (c === '"' || c === "'" || c === '`') quote = c;
    else if (c === '{') depth++;
    else if (c === '}' && --depth === 0) return source.slice(start, i + 1);
  }
  throw Error(`Unterminated native ${name}`);
}

function walk(node, predicate, result = []) {
  if (Array.isArray(node)) for (const child of node) walk(child, predicate, result);
  else if (node && typeof node === 'object') {
    if (predicate(node)) result.push(node);
    walk(node.props?.children, predicate, result);
  }
  return result;
}

module.exports = async function verifyTurnUsage26924({read, entries}) {
  const sourcePath = entries.find(p => /\/conversation-blocks-[a-f0-9]+\.js$/.test(p));
  const disclosurePath = entries.find(p => /\/collapsed-turn-disclosure-[a-f0-9]+\.js$/.test(p));
  const turnPath = entries.find(p => /\/local-conversation-turn-[a-f0-9]+\.js$/.test(p));
  assert.ok(sourcePath && disclosurePath && turnPath, 'exact native footer bundles exist');
  const source = read(sourcePath);
  const disclosure = read(disclosurePath);
  assert.ok(read(turnPath).includes('workedForItem:di,conversationId:d,turnId:h,hasFinalAssistantStarted:'), 'turn identity enters the activity component');
  assert.ok(source.includes('t[67]!==e.conversationId||t[68]!==e.turnId'), 'activity memo cache includes identity');
  assert.ok(source.includes('t[15]!==ci||t[16]!==ti'), 'disclosure memo cache includes identity');
  const componentStart = source.indexOf('const CODEX_MUX_USAGE_API =');
  const componentEnd = source.indexOf('function DE(e)', componentStart);
  assert.ok(componentStart >= 0 && componentEnd > componentStart, 'usage implementation is injected before the activity function');
  const component = source.slice(componentStart, componentEnd);
  assert.ok(!component.includes('__CODEX_MUX_CONTROL_TOKEN__'), 'usage control token is substituted');
  assert.ok(!component.includes('codexMuxSubscribeEvents'), 'usage component does not require lazy menu initialization');
  const jsx = (type, props) => ({type, props});
  const memo = n => Array(n).fill(Symbol.for('react.memo_cache_sentinel'));
  const seCache = memo(17), disclosureCache = memo(22);
  let cursor = 0;
  const key = 'synthetic-thread\u0000synthetic-turn';
  const view = {
    rootTurnId: 'synthetic-root', threadId: 'synthetic-thread', turnId: 'synthetic-turn',
    revision: 1, calibrationEnabled: false, totalUsageComplete: true,
    requests: [], agents: [], observations: [], estimatedShort: {}, estimatedWeekly: {}, totalApiCostUsd: 0.001,
  };
  const values = [{ key, loading: false, view }, false, false, '', { key, loaded: false }, { current: null }];
  const React = {
    createElement(type, props, ...children) { return { type, props: {...props, children} }; },
    useState(initial) {
      const slot = cursor++;
      if (!(slot in values)) values[slot] = initial;
      return [values[slot], value => { values[slot] = typeof value === 'function' ? value(values[slot]) : value; }];
    },
    useRef() { return values[cursor++]; },
    useEffect() {},
  };
  const calls = [];
  const context = vm.createContext({
    AbortController, Date, Intl, Number, String, encodeURIComponent, setTimeout, clearTimeout,
    AE: React, CE: {c: () => seCache}, wE: {jsx}, zm: 'NativeDisclosure',
    Wi: () => ({logProductEvent(){}}), Pr: {}, dn: () => ({}),
    x: {c: () => disclosureCache}, S: {jsx, jsxs: jsx, Fragment: 'fragment'},
    a: (...names) => names.filter(Boolean).join(' '), y: 'NativeWorkedFor', n: 'NativeChevron', r: 'button',
    fetch: async (url, options) => {
      calls.push({url: String(url), options});
      return {ok: true, json: async () => options?.method === 'PATCH' ? {...view, calibrationEnabled: true, revision: 2} : {buckets: []}};
    },
  });
  vm.runInContext(component, context);
  vm.runInContext(extractFunction(source, 'SE') + ';' + extractFunction(disclosure, 'b'), context);
  const nativeProps = {conversationId: view.threadId, turnId: view.turnId, collapsedMessageCount: 2, deniedActionCount: 0, workedDurationMs: 1000, workedForItem: null, isCollapsed: true, onToggle(){}};
  const nativeElement = context.SE(nativeProps);
  assert.equal(nativeElement.props.turnUsage.type, context.CodexMuxTurnUsage, 'native disclosure references a defined injected component');
  const row = context.b(nativeElement.props);
  const container = row.props.children[0];
  assert.equal(container.props.children[1], nativeElement.props.turnUsage, 'usage follows Worked for as a sibling');
  assert.equal(walk(container.props.children[0], n => n.type === context.CodexMuxTurnUsage).length, 0, 'usage is not nested in the native disclosure button');
  cursor = 0;
  const usageTree = context.CodexMuxTurnUsage(nativeElement.props.turnUsage.props);
  const buttons = walk(usageTree, n => n.type === 'button');
  assert.deepEqual(buttons.map(n => n.props['data-codex-mux-usage-action']), ['calibration', 'observed', 'estimated', 'api-cost']);
  assert.equal(buttons[0].props['aria-pressed'], false, 'calibration starts off');
  buttons[0].props.onClick();
  await new Promise(setImmediate);
  assert.match(calls[0].url, /\/v1\/usage\/calibration$/);
  assert.deepEqual(JSON.parse(calls[0].options.body), {rootId: 'synthetic-root', included: true, revision: 1});
  assert.ok(calls[0].options.headers['X-Codex-Mux-Token'] === vm.runInContext('CODEX_MUX_USAGE_TOKEN', context), 'calibration uses the embedded local control token');
  const changed = context.SE({...nativeProps, turnId: 'next-turn'});
  assert.equal(changed.props.turnUsage.props.turnId, 'next-turn', 'same native memoized component receives new identity');
  console.log('PASS: 26.924 native Worked for sibling controls, identity memo invalidation, Pro20 usage rendering and authenticated opt-in.');
};
