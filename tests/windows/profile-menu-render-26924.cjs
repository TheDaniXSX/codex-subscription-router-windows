// Packed 26.924 contract: exercise the injected UI and scoped reset boundary
// with synthetic accounts only. No real account credits are ever contacted.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const path = require('node:path');

function extractFunction(source, name) {
  const start = source.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `function ${name} exists`);
  let depth = 0, quote = '', begun = false;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    const c = source[i];
    if (quote) {
      if (c === '\\') i++;
      else if (c === quote) quote = '';
      continue;
    }
    if (c === '"' || c === "'" || c === '`') { quote = c; continue; }
    if (c === '{') { depth++; begun = true; }
    if (c === '}' && --depth === 0 && begun) return source.slice(start, i + 1);
  }
  throw Error(`Unterminated function ${name}`);
}

function makeHooks(initialValues) {
  const values = initialValues.slice();
  let cursor = 0;
  return {
    values,
    begin() { cursor = 0; },
    useState(initialValue) {
      const slot = cursor++;
      if (!(slot in values)) values[slot] = typeof initialValue === 'function' ? initialValue() : initialValue;
      return [values[slot], next => {
        values[slot] = typeof next === 'function' ? next(values[slot]) : next;
      }];
    },
    useCallback: fn => fn,
    useEffect: () => {},
  };
}

module.exports = async function verify26924({asar, archive, entries, initial}) {
  const read = name => asar.extractFile(archive, name.replace(/^\//, '').split('/').join(path.sep)).toString();
  const menuPath = entries.find(p => /\/profile-dropdown-items-[a-f0-9]+\.js$/.test(p));
  assert.ok(menuPath, '26.924 profile dropdown bundle exists');
  const menu = read(menuPath);
  assert.ok(menu.includes('function CodexMuxAccountMenu() {'), 'router account menu is injected into the lazy native menu bundle');
  assert.ok(menu.includes('(0,q.jsx)(CodexMuxAccountMenu,{})'), 'router controls are rendered beside native usage items');
  assert.ok(menu.includes('(0,Y.jsx)(A,{leftIconAsset:j,"aria-label":e'), '26.924 native MenuItem binding is used');
  assert.ok(menu.includes('gn(modalScope, CodexMuxUsageModal, {})'), 'usage action opens the native modal scope');
  assert.ok(menu.includes('globalThis.CodexMuxUseResetAccountState=CodexMuxUseResetAccountState;'), 'reset-account hook is exported for the lazy modal');
  assert.ok(menu.includes('globalThis.CodexMuxProfileAvatarStack = (props) =>'), 'profile stack retains its JSX wrapper');
  assert.ok(menu.includes('globalThis.CodexMuxPluginScope = () =>'), 'plugin scope retains its JSX wrapper');
  assert.ok(!menu.includes('globalThis.CodexMuxProfileAvatarStack=CodexMuxProfileAvatarStack;'), 'profile hook is not invoked as a parent hook');
  assert.ok(!menu.includes('globalThis.CodexMuxPluginScope=CodexMuxPluginScope;'), 'plugin hook is not invoked as a parent hook');
  assert.ok(!menu.includes('window.prompt('), 'no unsupported prompt API is used');

  const componentStart = menu.indexOf('const CODEX_MUX_API =');
  const componentEnd = menu.indexOf('function In(e)', componentStart);
  assert.ok(componentStart >= 0 && componentEnd > componentStart, 'injected menu component is bounded by the native component');
  const component = menu.slice(componentStart, componentEnd);
  const jsx = (type, props, key) => ({type, props, key});
  const accounts = [
    {id:'primary',label:'Primary',enabled:true,connected:true,controller:true,status:'ready',rateLimits:{primary:{usedPercent:20,windowDurationMins:300},secondary:{usedPercent:40,windowDurationMins:10080}}},
    {id:'second',label:'Subscription 2',enabled:true,connected:true,status:'ready',rateLimits:{primary:{usedPercent:10,windowDurationMins:300},secondary:{usedPercent:30,windowDurationMins:10080}}},
  ];
  const modalState = {value:{modals:[]}};
  const scope = {set:(_atom, update) => { modalState.value = update(modalState.value); }};
  const calls = [];
  const accountHookState = makeHooks([
    accounts, false, '', '', '', null, false, 'second', {mode:'auto'},
    {enabled:false,records:[]}, false,
    {revision:0,saving:false,refreshPending:false}, null, {busy:false},
  ]);
  const accountFetch = async (url, options = {}) => {
    calls.push({url:String(url),...options});
    const parsed = new URL(String(url));
    if (parsed.pathname.endsWith('/accounts') && !options.method) return {ok:true,json:async()=>({accounts})};
    if (parsed.pathname === '/v1/accounts/second' && options.method === 'PATCH') return {ok:true,json:async()=>({account:{...accounts[1],label:JSON.parse(options.body).label}})};
    if (parsed.pathname === '/v1/routing-mode' && options.method === 'PATCH') return {ok:true,json:async()=>({routingMode:JSON.parse(options.body)})};
    if (parsed.pathname.endsWith('/rate-limit-resets/consume')) return {ok:true,json:async()=>({code:'reset',credit:{id:'fixture-credit'}})};
    if (parsed.pathname.endsWith('/rate-limit-resets')) return {ok:true,json:async()=>({available_count:1,credits:[{id:'fixture-credit',status:'available'}]})};
    throw Error(`Unexpected synthetic request ${options.method || 'GET'} ${parsed.pathname}`);
  };
  const sandbox = {
    AbortController, TextDecoder, TextEncoder, URL, URLSearchParams,
    setTimeout, clearTimeout, setInterval, clearInterval, fetch:accountFetch,
    document:{querySelector:()=>({focus(){}})},
    Y:{jsx,jsxs:jsx,Fragment:'fragment'},
    Kn:accountHookState,
    ge:()=>scope, Ye:{},
    gn:(target,ModalComponent,props)=>target.set(null,previous=>({...previous,modals:[...previous.modals,{ModalComponent,props}]})),
    A:props=>({type:'NativeMenuItem',props}),
    At:props=>({type:'NativeRateLimitResetModal',props}),
    Ue:{Separator:props=>({type:'NativeMenuSeparator',props})},
  };
  sandbox.window = sandbox;
  const context = vm.createContext(sandbox);
  vm.runInContext(component, context);
  const expectedToken = vm.runInContext('CODEX_MUX_TOKEN', context);
  assert.ok(typeof expectedToken === 'string' && expectedToken.length > 0, 'packed control token is present');
  const assertToken = call => assert.ok(
    call.headers['X-Codex-Mux-Token'] === expectedToken,
    'synthetic request authenticates with the packed token (value redacted)',
  );
  const renderMenu = () => {
    accountHookState.begin();
    return context.CodexMuxAccountMenu().props.children;
  };
  const find = action => renderMenu().find(row => row?.props?.['data-codex-mux-action'] === action);
  const event = {preventDefault(){},stopPropagation(){}};

  const usage = find('usage');
  assert.ok(usage, 'Usage remaining button renders with the native menu item');
  usage.props.onSelect(event);
  assert.equal(modalState.value.modals.length, 1, 'usage button opens a modal');
  const modal = modalState.value.modals[0].ModalComponent(modalState.value.modals[0].props);
  assert.equal(modal.type, context.At, 'usage action keeps the official reset modal component');
  assert.equal(calls.length, 0, 'opening the dialog does not consume a reset');

  find('rename').props.onSelect(event);
  const form = find('rename-form');
  assert.ok(form, 'Rename subscription opens an inline form');
  form.props.children[1].props.onChange({target:{value:'Personal'}});
  await find('rename-form').props.onSubmit(event);
  const rename = calls.find(call => call.method === 'PATCH' && new URL(call.url).pathname === '/v1/accounts/second');
  assert.ok(rename, 'Rename sends an account PATCH');
  assert.equal(JSON.parse(rename.body).label, 'Personal');
  assertToken(rename);

  find('routing-mode').props.onSelect(event);
  const options = renderMenu().find(row => row?.props?.id === 'codex-mux-routing-options');
  assert.ok(options, 'routing mode expands into its selector');
  assert.equal(options.props.children.length, 3, 'selector offers Auto and both connected subscriptions');
  const secondOption = options.props.children.find(row => row?.props?.['data-codex-mux-routing-option'] === 'second');
  assert.ok(secondOption, 'a concrete subscription is selectable');
  secondOption.props.onSelect(event);
  await new Promise(resolve => setImmediate(resolve));
  const routing = calls.find(call => call.method === 'PATCH' && new URL(call.url).pathname === '/v1/routing-mode');
  assert.deepEqual(JSON.parse(routing.body), {mode:'account',accountId:'second'});
  assert.equal(renderMenu().find(row => row?.props?.['data-codex-mux-action'] === 'routing-mode').props['aria-label'], 'Routing mode: Subscription 2');

  // Drive the reset selector's actual hook state, then exercise the eager API
  // bridge and the packed native query/mutation with mocked HTTP responses.
  context.__codexMuxConnectedAccounts = accounts;
  const resetHookState = makeHooks([accounts,'primary',{},false,{active:true,generation:0}]);
  context.Kn = resetHookState;
  context.CodexMuxUseResetAccountState();
  assert.equal(context.__codexMuxResetAccountId, 'primary');
  const selector = context.__codexMuxResetAccountSelector;
  const selectorTree = selector.type(selector.props);
  const resetGroup = selectorTree.props.children[1];
  const secondReset = resetGroup.props.children.find(row => row?.props?.['aria-label'] === 'Use Subscription 2 for usage resets');
  assert.ok(secondReset, 'reset UI exposes connected accounts');
  secondReset.props.onClick();
  resetHookState.begin();
  context.CodexMuxUseResetAccountState();
  assert.equal(context.__codexMuxResetAccountId, 'second', 'the reset selector updates its scoped account');

  const bootstrapStart = initial.lastIndexOf(';(()=>{const p=`http://127.0.0.1:');
  assert.ok(bootstrapStart >= 0, 'eager API bridge is installed in app-initial');
  const bootstrapEnd = initial.indexOf('})();', bootstrapStart);
  assert.ok(bootstrapEnd > bootstrapStart, 'eager API bridge is bounded');
  vm.runInContext(initial.slice(bootstrapStart, bootstrapEnd + 4), context);
  assert.equal(typeof context.codexMuxRateLimitResets, 'function');
  const queryClient = {writes:[],setQueryData(key,update){this.writes.push({key:Array.from(key),update});}};
  const invalidations = [];
  Object.assign(context, {
    ko:()=>{}, Z:()=>{}, oh:value=>value,
    Ds:{ONE_MINUTE:60000,FIVE_SECONDS:5000}, Fpn:value=>value,
    kr:()=>queryClient, nr:()=>key=>invalidations.push(Array.from(key)),
    eu:value=>value, spn:(previous,code,id)=>({previous,code,id}),
  });
  vm.runInContext(extractFunction(initial,'Ppn')+';'+extractFunction(initial,'Lpn'), context);
  context.__codexMuxResetAccountId = 'second';
  const resetQuery = context.Ppn();
  assert.deepEqual(Array.from(resetQuery.queryKey), ['rate-limit-reset-credits','second']);
  const credits = await resetQuery.queryFn();
  assert.equal(credits.available_count, 1);
  const readCall = calls.at(-1);
  assert.match(readCall.url, /\/v1\/accounts\/second\/rate-limit-resets$/);
  assertToken(readCall);
  const mutation = context.Lpn();
  context.__codexMuxResetAccountId = 'primary'; // A selection change must not retarget an in-flight redemption.
  const input = {creditId:'fixture-credit',redeemRequestId:'fixture-request'};
  const result = await mutation.mutationFn(input);
  mutation.onSuccess(result,input);
  const redemption = calls.at(-1);
  assert.match(redemption.url, /\/v1\/accounts\/second\/rate-limit-resets\/consume$/);
  assert.equal(redemption.method, 'POST');
  assertToken(redemption);
  assert.deepEqual(JSON.parse(redemption.body), input);
  assert.ok(queryClient.writes.some(write => write.key.join('/') === 'rate-limit-reset-credits/second'));
  assert.ok(invalidations.some(key => key.join('/') === 'rate-limit-reset-credits/second'));
  assert.ok(!calls.some(call => call.url.includes('/accounts/primary/rate-limit-resets/consume')));

  const profile = read(entries.find(p => /\/profile-[a-f0-9]+\.js$/.test(p)));
  const menuDependency = `import "./${path.basename(menuPath)}";`;
  assert.ok(profile.startsWith(menuDependency), 'profile chunk statically waits for the lazy UI helpers before evaluating');
  assert.ok(profile.includes('CodexMuxProfileAvatarStack?.({onSelect:()=>re()})'), 'profile selection refreshes the native profile query');
  assert.ok(!profile.includes('onSelect:()=>h.refetch()'), 'profile refresh does not call a non-query view object');
  const pluginsPath = entries.find(p => /\/plugins-settings-b59e65830e0f\.js$/.test(p));
  const plugins = pluginsPath && read(pluginsPath);
  assert.ok(plugins && plugins.startsWith(menuDependency), 'plugin chunk statically waits for the lazy UI helpers before evaluating');
  assert.ok(plugins.includes('globalThis.CodexMuxPluginScope?.()'), 'plugin settings include account scope');
  const modalPath = entries.find(p => /\/modal-impl-[a-f0-9]+\.js$/.test(p));
  const modalImpl = read(modalPath);
  assert.ok(modalImpl.includes('globalThis.CodexMuxUseResetAccountState?.()'), 'reset dialog initializes the account selector');
  assert.ok(modalImpl.includes('window.__codexMuxSelectedUsageWindows??v'), 'usage windows follow the selected subscription');
  assert.ok(initial.includes(`globalThis.__codexMuxRendererUiReady??=import("./${path.basename(menuPath)}")`), 'eager entry preloads shared UI globals used by profile/plugins/thread');

  // Reproduce the navigation race: the eager prewarm is still pending when
  // the user opens Profile/Plugins. A static ESM dependency must evaluate the
  // helper module before either chunk's body reads its globals.
  globalThis.__codexMuxRendererUiReady = new Promise(() => {});
  const helperModule = `globalThis.CodexMuxProfileAvatarStack=()=>{};globalThis.CodexMuxPluginScope=()=>{};`;
  const helperUrl = `data:text/javascript,${encodeURIComponent(helperModule)}`;
  const profileRaceUrl = `data:text/javascript,${encodeURIComponent(`import "${helperUrl}";globalThis.__codexMuxProfileReadyAtRender=typeof globalThis.CodexMuxProfileAvatarStack==="function";`)}`;
  const pluginsRaceUrl = `data:text/javascript,${encodeURIComponent(`import "${helperUrl}";globalThis.__codexMuxPluginsReadyAtRender=typeof globalThis.CodexMuxPluginScope==="function";`)}`;
  await import(profileRaceUrl);
  await import(pluginsRaceUrl);
  assert.equal(globalThis.__codexMuxProfileReadyAtRender, true, 'Profile can navigate before the app-initial prewarm promise settles');
  assert.equal(globalThis.__codexMuxPluginsReadyAtRender, true, 'Plugins can navigate before the app-initial prewarm promise settles');
  delete globalThis.__codexMuxRendererUiReady;
  delete globalThis.__codexMuxProfileReadyAtRender;
  delete globalThis.__codexMuxPluginsReadyAtRender;

  const sharedPath = entries.find(p => /\/app-shared-[a-f0-9]+\.js$/.test(p));
  assert.ok(sharedPath && read(sharedPath).includes('t=globalThis.codexMuxScopePluginRequest?.(e,t)??t'), 'app-server plugin requests apply account scope');
  const threadPath = entries.find(p => /\/local-conversation-thread-[a-f0-9]+\.js$/.test(p) && read(p).includes('const CODEX_MUX_THREAD_API ='));
  assert.ok(threadPath, 'last-request subscription component is injected into the thread bundle');
  const thread = read(threadPath);
  assert.ok(thread.includes('const CodexMuxThreadReact=cM;'), 'thread hooks use the 26.924 React binding');
  assert.ok(thread.includes('globalThis.__codexMuxRendererUiReady.then'), 'thread waits for lazy SSE helpers if it mounts first');
  assert.ok(thread.includes('children:[j,b,M,N,(0,SD.jsx)(CodexMuxThreadSubscription,{}),A,I]'), 'attribution is placed in the native local-thread summary');
  console.log('PASS: 26.924 lazy menu Rename, Auto/account selector, native usage modal, account-scoped reset query/redemption, profile refresh, plugin scope and thread helper readiness.');
};
