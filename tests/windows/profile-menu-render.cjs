// Offline render contract against a real patched ASAR; no network or saved-state writes.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const path = require('node:path');
// These reviewed native functions contain no template interpolation or regex
// literals; bracket scanning isolates their body without evaluating the app.
function extractFunction(source, name) {
  const start = source.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `function ${name} exists`);
  let depth = 0, quote = '', begun = false;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    const c = source[i];
    if (quote) { if (c === '\\') i++; else if (c === quote) quote = ''; continue; }
    if (c === '"' || c === "'" || c === '`') { quote = c; continue; }
    if (c === '{') { depth++; begun = true; }
    if (c === '}' && --depth === 0 && begun) return source.slice(start, i + 1);
  }
  throw Error(`Unterminated function ${name}`);
}
async function main() {
  const asar = await import('@electron/asar');
  const archive = process.argv[2];
  assert.ok(archive, 'Usage: node tests/windows/profile-menu-render.cjs <patched app.asar>');
  const entries = asar.listPackage(archive).map(p=>p.replace(/\\/g,'/'));
  const primaryPath = entries.find(p => /\/app-primary-[^/]+\.js$/.test(p));
  const initialPath = entries.find(p => /\/app-initial-[^/]+\.js$/.test(p));
  const primary = asar.extractFile(archive, primaryPath.replace(/^\//, '').split('/').join(path.sep)).toString();
  const initial = asar.extractFile(archive, initialPath.replace(/^\//, '').split('/').join(path.sep)).toString();
  const version = JSON.parse(asar.extractFile(archive, 'package.json').toString()).version;
  if (version === '26.930.41038') {
    await require('./profile-menu-render-26930.cjs')({asar, archive, entries, initial});
    return;
  }
  if (version === '26.924.22138') {
    await require('./profile-menu-render-26924.cjs')({asar, archive, entries, initial});
    return;
  }
  if (version === '26.917.51856') {
    await require('./profile-menu-render-26917.cjs')({asar, archive, entries, primary, initial, extractFunction});
    return;
  }
  if (version === '26.915.31945') {
    await require('./profile-menu-render-26915.cjs')({asar, archive, entries, primary, initial, extractFunction});
    return;
  }
  if (version === '26.911.61220') {
    await require('./profile-menu-render-26911.cjs')({asar, archive, entries, primary, initial, extractFunction});
    return;
  }
  const updated = primary.includes('function Cbn(e){');
  const modern = primary.includes('function pGt(e){');
  if (modern) {
    assert.match(primary, /cX as kg[,}]/);
    const exported = initial.match(/([\w$]+) as cX[,}]/);
    assert.ok(exported, 'native menu component export is present');
    assert.ok(initial.includes(`function ${exported[1]}(e)`), 'menu binding resolves to a function');
    assert.match(primary, /Gz\.jsx\)\(kg,\{leftIconAsset:lE/);
    assert.match(initial, /LeftIcon:n,leftIconAsset:f/); // Legacy icon prop remains supported.
    assert.match(primary, /function CodexMuxAccountMenu\(\) \{\s+lR\(\);/);
    assert.match(primary, /ge=\(0,ML\.jsxs\)\(ML.Fragment,\{children:\[ge,window.__codexMuxResetAccountSelector\?\?null\]\}\)/);
    const profilePath = entries.find(p => /\/profile-[a-f0-9]+\.js$/.test(p));
    const profile = asar.extractFile(archive, profilePath.slice(1).split('/').join(path.sep)).toString();
    assert.match(profile, /codexMuxProfileQuery=h\(\),\{data:k\}=codexMuxProfileQuery/);
    assert.match(profile, /onSelect:\(\)=>codexMuxProfileQuery\.refetch\(\)/);
    const threadPath = entries.find(p => /\/local-conversation-thread-[a-f0-9]+\.js$/.test(p) &&
      asar.extractFile(archive, p.slice(1).split('/').join(path.sep)).toString().includes('const CODEX_MUX_THREAD_API ='));
    assert.ok(threadPath, 'thread subscription component was injected');
    const thread = asar.extractFile(archive, threadPath.slice(1).split('/').join(path.sep)).toString();
    assert.match(thread, /import\{t as r\}from"\.\/react-/);
    assert.match(thread, /const CodexMuxThreadReact=r\(\);/);
    assert.match(thread, /const route = vi\(S\);/);
    assert.match(thread, /children:\[T,p,E,D,\(0,oO.jsx\)\(CodexMuxThreadSubscription,\{\}\),w,O\]/);
    const component = thread.slice(thread.indexOf('const CODEX_MUX_THREAD_API ='), thread.indexOf('function nO(e)'));
    assert.match(component, /\/thread-spending\?threadId=/);
    assert.doesNotMatch(component, /\/thread-account\?threadId=/);
    const jsx = (type, props) => {
      assert.ok(typeof type === 'function' || typeof type === 'string', 'valid thread section element');
      return {type, props};
    };
    for (const spending of [null, {threadId:'task', request:{accountId:'second'}, account:{id:'second',label:'Subscription 2'}}]) {
      const context = vm.createContext({
        r:()=>({useState:()=>[spending,()=>{}],useEffect:()=>{}}),
        vi:()=>({value:{routeKind:'local-thread',conversationId:'task'}}), S:{},
        oO:{jsx,jsxs:jsx}, Z:{Section:()=>{}},
      });
      vm.runInContext(component, context);
      const rendered = context.CodexMuxThreadSubscription();
      assert.equal(rendered.props.title, 'Subscription');
      if (spending) assert.match(JSON.stringify(rendered), /Subscription 2/);
    }
  } else if (updated) {
    assert.match(primary, /YG as Zy[,}]/);
    const exported = initial.match(/([\w$]+) as YG[,}]/);
    assert.ok(exported, 'native menu component export is present');
    assert.ok(initial.includes(`function ${exported[1]}(e)`), 'menu binding resolves to a function');
    assert.match(primary, /xK\.jsx\)\(Zy,\{LeftIcon:oT/);
  } else {
  assert.match(primary, /GM as xl[,}]/);
  assert.match(initial, /xeo as GM[,}]/);
  assert.match(initial, /xeo=\(typeof navigator/); // Keybinding map, not a React component.
  assert.match(primary, /YG as Qy[,}]/);
  assert.match(initial, /jea as YG[,}]/);
  assert.match(initial, /function jea\(e\)/);
  assert.match(primary, /dq\.jsx\)\(Qy,\{LeftIcon:CT/); // Native menu uses Qy.
  }
  const start = primary.indexOf('const CODEX_MUX_API =');
  const end = primary.indexOf(modern ? 'function pGt(e)' : updated ? 'function Cbn(e)' : 'function kyn(e)', start);
  assert.ok(start >= 0 && end > start);
  const injected = primary.slice(start, end);
  if (modern) {
    assert.match(primary, /Pmt as ud[,}]/);
    const opener = initial.match(/([\w$]+) as Pmt[,}]/)?.[1];
    assert.ok(opener, 'native modal opener export exists');
    const openSource = extractFunction(initial, opener);
    assert.match(openSource, /\.set\(uL/);
    assert.match(openSource, /ModalComponent:t,props:n/);
    assert.doesNotMatch(injected, /window\.prompt\(/);
    async function exerciseButtons(code, expectModal) {
      const jsx = (type, props, key) => ({type, props, key});
      const accounts = [{id:'primary',label:'Primary',enabled:true,connected:true,controller:true}, {id:'second',label:'Subscription 2',enabled:true,connected:true}];
      const values = [accounts,false,'','','',null,false,'second'];
      let index = 0, modalState = {modals:[],nextKey:1}, initialized = false;
      const calls = [], invalidations = [];
      const scope = {set:(_atom, update)=>{modalState=update(modalState);}};
      const globals = {
        AbortController, TextDecoder, TextEncoder, setTimeout, clearTimeout, setInterval, clearInterval,
        document:{querySelector:()=>({focus(){}})}, window:{prompt(){throw Error('prompt() is not supported');}},
        Gz:{jsx,jsxs:jsx,Fragment:'fragment'}, Of:()=>scope, o_:{}, lR:()=>{}, cR:()=>{}, kg:()=>{}, tS:{Separator:()=>{}},
        Dp:()=>{}, // The previous binding only recorded analytics, not a modal.
        uL:{}, VNr:(a,b)=>a===b, PL:()=>{initialized=true;}, NL:()=>{},
        vGt:{useState:initial=>{const slot=index++;if(!(slot in values))values[slot]=typeof initial==='function'?initial():initial;return [values[slot],next=>{values[slot]=typeof next==='function'?next(values[slot]):next;}];},useCallback:f=>f,useEffect:()=>{}},
        KD:()=>{},fm:()=>{},Qx:{ONE_MINUTE:60000,FIVE_SECONDS:5000},xm:o=>o,aji:x=>x,oji:()=>{throw Error('unscoped credit read');},
        _m:()=>({setQueryData(){}}),Xx:()=>key=>invalidations.push(key),wm:o=>o,Wki:x=>x,cji:()=>{throw Error('unscoped credit redemption');},
        fetch:async(url, options={})=>{
          calls.push({url,...options});
          if (url.endsWith('/accounts')) return {ok:true,json:async()=>({accounts})};
          assert.match(url, /\/accounts\/second(?:\/rate-limit-resets(?:\/consume)?)?$/);
          return {ok:true,json:async()=>url.endsWith('/consume') ? {code:'reset',credit:{id:'fixture-credit'}} : {available_count:1,credits:[{id:'fixture-credit',status:'available'}]}};
        },
      };
      const c = vm.createContext(globals);
      vm.runInContext(`${openSource};globalThis.ud=${opener};`,c);
      vm.runInContext(code,c);
      const render = ()=>{index=0;return c.CodexMuxAccountMenu().props.children;};
      const find = action=>render().find(row=>row.props['data-codex-mux-action']===action);
      const event = {preventDefault(){},stopPropagation(){}};
      find('usage').props.onSelect(event);
      assert.equal(modalState.modals.length, expectModal ? 1 : 0, 'usage click opens native modal state');
      if (!expectModal) return;
      const close=()=>{};
      const view=modalState.modals[0].ModalComponent({onClose:close});
      assert.equal(initialized,true,'native lazy reset module initialized');
      assert.equal(view.type,c.NL); assert.equal(view.props.onClose,close);
      assert.equal(view.props.defaultResetCreditsOpen,true);
      assert.equal(calls.length,0,'opening cannot consume credits');
      find('rename').props.onSelect(event);
      find('rename-form').props.children[1].props.onChange({target:{value:'Personal'}});
      await find('rename-form').props.onSubmit(event);
      assert.equal(calls[0].method,'PATCH');
      assert.equal(JSON.parse(calls[0].body).label,'Personal');
      assert.ok(calls[0].headers['X-Codex-Mux-Token'],'rename is authenticated');
      vm.runInContext(extractFunction(initial,'iji')+';'+extractFunction(initial,'sji'),c);
      c.window.__codexMuxResetAccountId='second';
      const query=c.iji();
      assert.equal(query.queryKey[1],'second');
      const credits=await query.queryFn();
      assert.equal(credits.available_count,1);
      const mutation=c.sji();
      c.window.__codexMuxResetAccountId='primary'; // captured identity must not change mid-operation
      const input={creditId:'fixture-credit',redeemRequestId:'fixture-request'};
      const result=await mutation.mutationFn(input);
      mutation.onSuccess(result,input);
      const redemption=calls.at(-1);
      assert.match(redemption.url,/\/accounts\/second\/rate-limit-resets\/consume$/);
      assert.equal(redemption.method,'POST');
      assert.deepEqual(JSON.parse(redemption.body),input);
      assert.ok(redemption.headers['X-Codex-Mux-Token']);
      assert.ok(invalidations.some(key=>key[0]==='rate-limit-reset-credits'&&key[1]==='second'));
    }
    await exerciseButtons(injected,true);
    await exerciseButtons(injected.replace('ud(modalScope, CodexMuxUsageModal, {})','Dp(modalScope, CodexMuxUsageModal, {})'),false);
    console.log('PASS: real native modal opener, Rename click/save, and account-scoped simulated reset read/redemption; old analytics binding opens nothing.');
  }
  function render(code, expanded = false) {
    const jsx = (type, props, key) => {
      if (typeof type !== 'function' && typeof type !== 'string') throw Error('Invalid React element type: object (xl keybinding map)');
      return {type, props, key};
    };
    const values = [[{id:'primary', label:'Primary', enabled:true, connected:true, controller:true}], false, '', '', '', null, false, null, {mode:'account',accountId:'primary'}, {enabled:true,records:[]}, expanded];
    let index=0;
    const globals = {
      AbortController, TextDecoder, TextEncoder, setTimeout, clearTimeout, setInterval, clearInterval,
      document:{querySelector:()=>null}, fetch:()=>{throw Error('Network forbidden in this test');},
      dq:{jsx,jsxs:jsx,Fragment:'fragment'}, Zv:()=>{}, fo:()=>({}), HE:{}, MG:()=>{},
      xl:{'Ctrl-a':()=>{}}, Qy:()=>{}, of:{Separator:()=>{}},
      Pyn:{useState:initial=>{const n=index++;if(!(n in values))values[n]=typeof initial==='function'?initial():initial;return [values[n],()=>{}];},useCallback:f=>f,useEffect:()=>{}}
    };
    if (updated) Object.assign(globals, {xK:globals.dq, Obn:globals.Pyn, Hv:globals.Zv, Oe:globals.fo, zb:globals.HE, iG:globals.MG, Zy:globals.Qy, Rd:globals.of});
    if (modern) Object.assign(globals, {Gz:globals.dq, vGt:globals.Pyn, ud:globals.Zv, Of:globals.fo, o_:globals.HE, lR:()=>{}, cR:globals.MG, kg:globals.Qy, tS:globals.of});
    const c = vm.createContext(globals);
    vm.runInContext(code,c);
    return c.CodexMuxAccountMenu();
  }
  assert.doesNotThrow(()=>render(injected));
  assert.doesNotThrow(()=>render(injected, true));
  assert.throws(()=>render(injected.replace(modern ? /\bkg\b/g : updated ? /\bZy\b/g : /\bQy\b/g,'xl')), /Invalid React element type: object/);
  console.log('PASS: installed account menu and expanded routing selector accept native component bindings.');
  console.log('PASS: negative control reproduces React invalid-element-type with the old keybinding alias.');
}
// Assertion input may be a private patched bundle containing the control token.
// Never dump source snippets from AssertionError diagnostics into build logs.
main().catch(error=>{console.error(String(error.message).split(/\r?\n/)[0]);process.exitCode=1;});
