// Offline contract against actual patched 26.1013 bundles. Synthetic accounts
// only; the official app, real authentication and credits are never contacted.
// Derived from the 26.1007 contract; the native menu container and purchase
// wrapper are checked by their exact patched markers instead of being executed.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const path = require('node:path');

function extractFunction(source, name) {
  const start = source.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `native function ${name} exists`);
  let depth = 0, quote = '';
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    const c = source[i];
    if (quote) { if (c === '\\') i++; else if (c === quote) quote = ''; continue; }
    if (c === '"' || c === "'" || c === '`') { quote = c; continue; }
    if (c === '{') depth++;
    if (c === '}' && --depth === 0) return source.slice(start, i + 1);
  }
  throw Error(`Unterminated native function ${name}`);
}

function hooks(initial) {
  const values = [...initial];
  let cursor = 0;
  return {values,begin(){cursor=0;},useState(value){const slot=cursor++;if(!(slot in values))values[slot]=typeof value==='function'?value():value;return [values[slot],next=>{values[slot]=typeof next==='function'?next(values[slot]):next;}];},useCallback:fn=>fn,useEffect:()=>{}};
}

module.exports = async function verify261013({asar,archive,entries,initial}) {
  const read = name => asar.extractFile(archive,name.replace(/^\//,'').split('/').join(path.sep)).toString();
  const choose = (pattern,marker) => entries.find(name=>pattern.test(name)&&(!marker||read(name).includes(marker)));
  const menuPath = choose(/\/profile-dropdown-items-[a-f0-9]+\.js$/,'const CODEX_MUX_API =');
  assert.ok(menuPath,'26.1013 router menu bundle exists');
  const menu = read(menuPath);
  assert.ok(menu.includes('Nt(modalScope, CodexMuxUsageModal, {})'),'usage opens official scoped modal');
  assert.ok(menu.includes('nst as CodexMuxQueryClient'),'plugin cache hook is explicitly imported');
  assert.ok(menu.includes('globalThis.CodexMuxProfileAvatarStack = (props) =>'),'profile wrapper retains React component isolation');
  assert.ok(menu.includes('globalThis.CodexMuxPluginScope = () =>'),'plugin wrapper retains React component isolation');
  assert.ok(!menu.includes('window.prompt('),'Rename does not use unsupported prompt');
  // Both native containers mount the router: the legacy list and the sidebar
  // footer's additional-items slot used by the current desktop.
  assert.ok(menu.includes('children:[Y,Fn,(0,$.jsx)(CodexMuxAccountMenu,{}),Hn,null,Un,Wn,null,null,Gn,qn,Qn,$n]'),'legacy menu slot is patched');
  assert.ok(menu.includes('children:[jn,Dn,(0,$.jsx)(CodexMuxAccountMenu,{}),null,g,_]'),'sidebar menu slot is patched');
  const start = menu.indexOf('const CODEX_MUX_API =');
  const end = menu.indexOf('function sa(e)',start);
  assert.ok(start>=0&&end>start,'injected code boundaries are exact');
  const jsx = (type,props,key)=>({type,props,key});
  const accounts = [
    {id:'primary',label:'Primary',enabled:true,connected:true,controller:true,status:'ready',rateLimits:{primary:{usedPercent:20,windowDurationMins:300},secondary:{usedPercent:40,windowDurationMins:10080}}},
    {id:'second',label:'Subscription 2',enabled:true,connected:true,status:'ready',rateLimits:{primary:{usedPercent:10,windowDurationMins:300},secondary:{usedPercent:30,windowDurationMins:10080}}},
  ];
  const calls=[];
  const modals=[];
  const scope={set:(_atom,update)=>{const state=update({modals});modals.splice(0,modals.length,...state.modals);}};
  const state=hooks([accounts,false,'','','',null,false,'second',{mode:'auto'},{enabled:false,records:[]},false,{revision:0,saving:false,refreshPending:false},null,{busy:false}]);
  const sandbox={
    AbortController,TextEncoder,TextDecoder,URL,URLSearchParams,setTimeout,clearTimeout,setInterval,clearInterval,
    document:{querySelector:()=>({focus(){}})},
    $:{jsx,jsxs:jsx,Fragment:'fragment'},ma:state,b:()=>scope,o:{},z:{Separator:'separator'},
    O:'NativeMenuItem',ir:'NativeResetModal',CodexMuxQueryClient:()=>({invalidateQueries:async()=>{}}),
    Nt:(target,ModalComponent,props)=>target.set(null,p=>({...p,modals:[...p.modals,{ModalComponent,props}]})),
    fetch:async(url,options={})=>{
      calls.push({url:String(url),...options});
      const pathname=new URL(String(url)).pathname;
      let body;
      if(pathname.endsWith('/accounts')&&!options.method)body={accounts};
      else if(pathname==='/v1/accounts/second'&&options.method==='PATCH')body={account:{...accounts[1],label:JSON.parse(options.body).label}};
      else if(pathname==='/v1/routing-mode'&&options.method==='PATCH')body={routingMode:JSON.parse(options.body)};
      else if(pathname.endsWith('/rate-limit-resets/consume'))body={code:'reset',credit:{id:'fixture-credit'}};
      else if(pathname.endsWith('/rate-limit-resets'))body={available_count:1,credits:[{id:'fixture-credit',status:'available'}]};
      else if(pathname.endsWith('/login'))body={login:{loginId:'fixture-login',verificationUrl:'https://fixture.invalid',userCode:'fixture-code'}};
      else throw Error(`Unexpected synthetic request ${options.method||'GET'} ${pathname}`);
      return {ok:true,json:async()=>body};
    },
  };
  sandbox.window=sandbox;
  const context=vm.createContext(sandbox);
  vm.runInContext(menu.slice(start,end),context);
  const token=vm.runInContext('CODEX_MUX_TOKEN',context);
  const authenticated=call=>assert.equal(call.headers['X-Codex-Mux-Token'],token,'request uses packed token (redacted)');
  const render=()=>{state.begin();return context.CodexMuxAccountMenu().props.children;};
  const find=action=>render().find(row=>row?.props?.['data-codex-mux-action']===action);

  const event={preventDefault(){},stopPropagation(){}};
  find('usage').props.onSelect(event);
  assert.equal(modals.length,1);
  assert.equal(modals[0].ModalComponent(modals[0].props).type,'NativeResetModal');
  assert.equal(calls.length,0,'opening usage never redeems or buys');
  find('rename').props.onSelect(event);
  find('rename-form').props.children[1].props.onChange({target:{value:'Personal'}});
  await find('rename-form').props.onSubmit(event);
  const rename=calls.find(c=>c.method==='PATCH'&&new URL(c.url).pathname==='/v1/accounts/second');
  assert.equal(JSON.parse(rename.body).label,'Personal');authenticated(rename);
  find('routing-mode').props.onSelect(event);
  const options=render().find(row=>row?.props?.id==='codex-mux-routing-options');
  assert.equal(options.props.children.length,3,'Auto plus each subscription');
  options.props.children.find(row=>row.props['data-codex-mux-routing-option']==='second').props.onSelect(event);
  await new Promise(resolve=>setImmediate(resolve));
  const routing=calls.find(c=>c.method==='PATCH'&&new URL(c.url).pathname==='/v1/routing-mode');
  assert.deepEqual(JSON.parse(routing.body),{mode:'account',accountId:'second'});authenticated(routing);
  const login=await context.codexMuxStartAccountLogin('second');
  assert.equal(login.accountId,'second');
  assert.equal(JSON.parse(calls.at(-1).body).mode,'chatgptDeviceCode');authenticated(calls.at(-1));

  const resetState=hooks([accounts,'primary',{},false,{active:true,generation:0}]);
  context.__codexMuxConnectedAccounts=accounts;context.ma=resetState;
  context.CodexMuxUseResetAccountState();
  const picker=context.__codexMuxResetAccountSelector;
  picker.type(picker.props).props.children[1].props.children[1].props.onClick();
  resetState.begin();context.CodexMuxUseResetAccountState();
  assert.equal(context.__codexMuxResetAccountId,'second');
  const bootstrapStart=initial.lastIndexOf(';(()=>{const p=`http://127.0.0.1:');
  const bootstrapEnd=initial.indexOf('})();',bootstrapStart);
  assert.ok(bootstrapStart>=0&&bootstrapEnd>bootstrapStart,'nonvisual APIs are eager');
  vm.runInContext(initial.slice(bootstrapStart,bootstrapEnd+4),context);
  const queryClient={writes:[],setQueryData(key,update){this.writes.push({key:Array.from(key),update});}};
  const invalidations=[];
  Object.assign(context,{DT:()=>{},q:()=>{},Za:value=>value,Zi:{ONE_MINUTE:60000,FIVE_SECONDS:5000},lxa:value=>value,Oc:()=>queryClient,Kh:()=>key=>invalidations.push(Array.from(key)),Pi:value=>value,Fba:(previous,code,id)=>({previous,code,id})});
  vm.runInContext(extractFunction(initial,'cxa')+';'+extractFunction(initial,'dxa'),context);
  const query=context.cxa();assert.deepEqual(Array.from(query.queryKey),['rate-limit-reset-credits','second']);
  assert.equal((await query.queryFn()).available_count,1);authenticated(calls.at(-1));
  const mutation=context.dxa();context.__codexMuxResetAccountId='primary';
  const input={creditId:'fixture-credit',redeemRequestId:'fixture-redemption'};
  const result=await mutation.mutationFn(input);mutation.onSuccess(result,input);
  assert.match(calls.at(-1).url,/\/accounts\/second\/rate-limit-resets\/consume$/);authenticated(calls.at(-1));
  assert.deepEqual(JSON.parse(calls.at(-1).body),input);
  assert.ok(queryClient.writes.some(v=>v.key.join('/')==='rate-limit-reset-credits/second'));
  assert.ok(invalidations.some(v=>v.join('/')==='rate-limit-reset-credits/second'));
  assert.ok(!calls.some(v=>v.url.includes('/accounts/primary/rate-limit-resets/consume')));

  const dependency=`import "./${path.basename(menuPath)}";`;
  const profile=read(choose(/\/profile-[a-f0-9]+\.js$/,'CodexMuxProfileAvatarStack'));
  assert.ok(profile.startsWith(dependency),'profile helpers are ready on direct navigation');
  assert.ok(profile.includes('Rt=(!a||de&&!X)&&G===`chatgpt`'),'aggregate query is restricted to owner and not preview');
  assert.ok(profile.includes('Vt=a&&(!de||X)?jt:Bt.data??jt'),'public and preview profiles remain native');
  assert.ok(profile.includes('onSelect:()=>Bt.refetch()'),'selected profile refreshes real query');
  const plugins=read(choose(/\/plugins-settings-[a-f0-9]+\.js$/,'globalThis.CodexMuxPluginScope'));
  assert.ok(plugins.startsWith(dependency),'plugin helpers are ready on direct navigation');
  const modal=read(choose(/\/modal-impl-[a-f0-9]+\.js$/));
  assert.ok(modal.includes('globalThis.CodexMuxUseResetAccountState?.()'));
  assert.ok(modal.includes('window.__codexMuxSelectedUsageWindows??y'));
  assert.ok(modal.includes('if(_!=null&&(!globalThis.__codexMuxResetAccountId||globalThis.__codexMuxResetAccountId===`primary`))'),'secondary selection cannot open primary checkout');
  assert.ok(modal.startsWith(dependency),'modal helpers exist before first render, keeping hook order stable');
  assert.ok(modal.includes('codexMuxResetDispatchAccountId===`primary`&&(te('),'secondary redemption uses captured dispatch identity for quota optimism');
  assert.ok(modal.includes('O=e=>{if(globalThis.__codexMuxResetAccountId&&globalThis.__codexMuxResetAccountId!==`primary`)return;'),'captured credit purchase callback is guarded');
  assert.ok(modal.includes('void 0:e=>{if(globalThis.__codexMuxResetAccountId&&globalThis.__codexMuxResetAccountId!==`primary`)return;i(),d({currentPlan:r'),'captured upgrade callback is guarded');
  assert.ok(modal.includes('const codexMuxResetBody='),'picker rerender gets a fresh native body instead of stale compiler-cached JSX');

  // Drive the actual packed outer modal while a redemption is in flight.
  // Its identity comes from the captured scoped mutation, not mutable globals.
  let finishRedemption,optimism=0,quotaWrites=0,completed=0;
  let mutationAccount='second';
  const nativeScope={query:{getData:()=>({account_id:'primary',primary:{usedPercent:50}})},set:()=>{quotaWrites++;},get:atom=>atom==='toast'?{success:()=>{}}:{dataUpdatedAt:1}};
  Object.assign(context,{
    CodexMuxUseResetAccountState:()=>{},o:()=>nativeScope,n:{},m:()=>({formatMessage:()=>''}),
    Tt:hooks([]),Ve:()=>({redeem:()=>new Promise(resolve=>{finishRedemption=resolve;})}),
    ze:()=>({isPending:false,codexMuxAccountId:mutationAccount,mutateAsync:async()=>{}}),
    Y:'limits',O:'toast',Qe:'optimism',R:{},fe:()=>{},Be:()=>'',
    te:()=>{optimism++;},_e:()=>[{}],Et:{jsx,jsxs:jsx,Fragment:'fragment'},vt:'NativeResetBody',a:'Message',
  });
  vm.runInContext(extractFunction(modal,'Ct'),context);
  context.__codexMuxResetAccountId='second';
  const pendingBody=context.Ct({initialAvailableCount:1,onClose:()=>{},onResetComplete:()=>{completed++;}}).props.children[2];
  const pending=pendingBody.props.onResetCredit('fixture-credit',1);
  context.__codexMuxResetAccountId='primary';
  finishRedemption({status:'reset',availableCountBefore:1,creditId:'fixture-credit',resetType:'all'});
  assert.equal((await pending).status,'completed');
  assert.deepEqual([optimism,quotaWrites,completed],[0,0,0],'Secondary redemption does not update Primary or call its completion effect after picker changes');
  mutationAccount='primary';context.Tt=hooks([]);
  const primaryBody=context.Ct({initialAvailableCount:1,onClose:()=>{},onResetComplete:()=>{completed++;}}).props.children[2];
  const pendingPrimary=primaryBody.props.onResetCredit('fixture-credit',1);
  context.__codexMuxResetAccountId='second';
  finishRedemption({status:'reset',availableCountBefore:1,creditId:'fixture-credit',resetType:'all'});
  await pendingPrimary;
  assert.deepEqual([optimism,quotaWrites,completed],[1,1,1],'Primary redemption still updates Primary and completes if picker changes');

  // AppServerRequestClient lives in app-initial since 26.1013.
  assert.ok(initial.includes('t=globalThis.codexMuxScopePluginRequest?.(e,t)??t'));
  const thread=read(choose(/\/local-conversation-thread-[a-f0-9]+\.js$/,'const CODEX_MUX_THREAD_API ='));
  assert.ok(thread.includes('const CodexMuxThreadReact=Yn(),CodexMuxThreadJsx=Un();'));
  assert.ok(thread.includes('children:[y,b,x,S,C,(0,MT.jsx)(CodexMuxThreadSubscription,{}),w,T,E]'));
  assert.ok(thread.includes('globalThis.__codexMuxRendererUiReady.then'),'thread SSE waits safely for lazy helpers');
  assert.ok(initial.includes(`import("./${path.basename(menuPath)}").then(()=>true,()=>false)`));
  console.log('PASS: 26.1013 packed Rename, device login, Auto/account mode, reset picker and captured redemption, secondary safety guards, self-v2 profiles, plugin scope and last-request attribution.');
};
