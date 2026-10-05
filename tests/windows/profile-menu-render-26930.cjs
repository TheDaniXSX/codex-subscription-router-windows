// Offline contract against actual patched 26.930 bundles. Synthetic accounts
// only; the official app, real authentication and credits are never contacted.
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

module.exports = async function verify26930({asar,archive,entries,initial}) {
  const read = name => asar.extractFile(archive,name.replace(/^\//,'').split('/').join(path.sep)).toString();
  const choose = (pattern,marker) => entries.find(name=>pattern.test(name)&&(!marker||read(name).includes(marker)));
  const menuPath = choose(/\/profile-dropdown-items-[a-f0-9]+\.js$/,'const CODEX_MUX_API =');
  assert.ok(menuPath,'26.930 router menu bundle exists');
  const menu = read(menuPath);
  assert.ok(menu.includes('(0,$.jsx)(CodexMuxAccountMenu,{})'),'menu is mounted with official JSX binding');
  assert.ok(menu.includes('pe(modalScope, CodexMuxUsageModal, {})'),'usage opens official scoped modal');
  assert.ok(menu.includes('_Yt as CodexMuxQueryClient'),'plugin cache hook is explicitly imported');
  assert.ok(menu.includes('globalThis.CodexMuxProfileAvatarStack = (props) =>'),'profile wrapper retains React component isolation');
  assert.ok(menu.includes('globalThis.CodexMuxPluginScope = () =>'),'plugin wrapper retains React component isolation');
  assert.ok(!menu.includes('window.prompt('),'Rename does not use unsupported prompt');
  const start = menu.indexOf('const CODEX_MUX_API =');
  const end = menu.indexOf('function Ri(e)',start);
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
    $:{jsx,jsxs:jsx,Fragment:'fragment'},Gi:state,ut:()=>scope,_:{},H:{Separator:'separator'},
    U:'NativeMenuItem',Zn:'NativeResetModal',CodexMuxQueryClient:()=>({invalidateQueries:async()=>{}}),
    pe:(target,ModalComponent,props)=>target.set(null,p=>({...p,modals:[...p.modals,{ModalComponent,props}]})),
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

  // Execute the real native container, including its early sidebarFooter
  // return and Pi's additionalItems slot. A marker in the legacy branch alone
  // cannot prove that the menu used by the current desktop mounts the router.
  const nativeAtoms={u:'desktop',ye:'pet',Ji:'workspace',Ir:'profile-enabled',Ot:'limits'};
  const nativeHooks=hooks([null]);
  const nativeMenuScope={query:{fetch:async()=>({})},get:()=>({success(){},danger(){}})};
  const nativeContext=vm.createContext({
    ...nativeAtoms,Wi:{c:n=>Array(n).fill(Symbol.for('react.memo_cache_sentinel'))},
    Ii:{c:n=>Array(n).fill(Symbol.for('react.memo_cache_sentinel'))},
    $:{jsx,jsxs:jsx,Fragment:'fragment'},Q:{jsx,jsxs:jsx,Fragment:'fragment'},
    Gi:{...nativeHooks,Fragment:'fragment'},ut:()=>nativeMenuScope,_:{},
    It:atom=>atom===nativeAtoms.u?true:atom===nativeAtoms.ye?false:atom===nativeAtoms.Ir?{isEnabled:false,isLoading:false}:atom===nativeAtoms.Ot?{data:null,isLoading:false}:null,
    it:()=>()=>{},F:()=>()=>{},h:()=>({accountId:'primary',email:null,userId:null,authMethod:'chatgpt',planAtLogin:'plus',requiresAuth:true}),
    Gt:()=>({data:{accounts:[]}}),tn:()=>({data:{id:'primary',structure:'personal',plan_type:'plus'},isError:false}),
    gt:()=>({getContext:()=>({user:{customIDs:{}}})}),Ne:()=>false,Oe:()=>false,
    re:()=>({data:{plan:'plus',accountId:'primary'}}),xt:()=>false,cn:()=>false,Dn:()=>null,
    sr:()=>({isProfileVisible:false}),Un:()=>()=>'',Nn:()=>null,yt:()=>null,
    T:{},sn:{},fr:'host',p:'primary',Be:atom=>atom==='host'?{data:null}:null,kr:()=>null,
    wr:()=>({modelSettings:{model:null}}),he:()=>({formatMessage:({defaultMessage})=>defaultMessage}),
    mn:()=>false,Jt:()=>[],Sr:()=>null,kn:()=>[],ln:()=>false,
    jr:()=>({isUsageSettingsVisible:false,isUsageSettingsAccessLoading:false}),Kt:()=>({data:undefined}),Ui:()=>null,
    Vi:()=>false,en:()=>()=>{},bn:()=>'desktop',on:()=>null,Bn:()=>null,Cn:()=>'Pro',
    U:'NativeMenuItem',L:'NativeMessage',H:{Separator:'separator',ItemIcon:'icon'},
    ii:'NativeAdditional',Wn:'NativeUsage',yr:'NativeAvatar',Me:'NativeAvatarFallback',
    CodexMuxAccountMenu:context.CodexMuxAccountMenu,dt:{dispatchMessage(){}},
    Qr:{icon:'icon'},w:(...values)=>values.filter(Boolean).join(' '),nt:'NativeIcon',
    ne:{},se:{},We:{},Nt:{},Je:{},mt:{},Ee:{},Qe:{},ft:{},wi:'NativeHelp',
  });
  vm.runInContext(extractFunction(menu,'Ri')+';'+extractFunction(menu,'Pi')+';'+extractFunction(menu,'Fi'),nativeContext);
  const expandNative=node=>{
    if(Array.isArray(node))return node.map(expandNative);
    if(node==null||typeof node!=='object')return node;
    if(node.type===context.CodexMuxAccountMenu)return node;
    if(typeof node.type==='function')return expandNative(node.type(node.props));
    return {...node,props:{...node.props,children:expandNative(node.props?.children)}};
  };
  const flatten=node=>Array.isArray(node)?node.flatMap(flatten):node==null||typeof node!=='object'?[]:[node,...flatten(node.props?.children)];
  for(const sidebarFooter of [undefined,{profileIdentity:{displayName:'Fixture owner',isProfileAvailable:true}}]){
    nativeHooks.begin();
    const tree=expandNative(nativeContext.Ri({sidebarFooter,open:true,onClose:()=>{}}));
    const mounted=flatten(tree).filter(node=>node.type===context.CodexMuxAccountMenu);
    assert.equal(mounted.length,1,`${sidebarFooter?'sidebarFooter':'legacy'} menu mounts exactly one account manager`);
    state.begin();
    const visible=flatten(mounted[0].type(mounted[0].props));
    for(const action of ['add','routing-mode','rename','usage'])assert.ok(visible.some(node=>node.props?.['data-codex-mux-action']===action),`${action} is reachable from actual native menu`);
    assert.ok(visible.some(node=>node.props?.['data-codex-mux-account-id']==='second'),'secondary subscription is reachable');
    assert.ok(flatten(tree).some(node=>node.type==='NativeMenuItem'&&node.props.children?.props?.id==='codex.profileDropdown.settingsPage'),'native Settings is retained');
  }
  const sidebarSlot='children:[qn,J,(0,$.jsx)(CodexMuxAccountMenu,{}),null,m,h]';
  assert.ok(menu.includes(sidebarSlot),'active sidebar slot is uniquely patched');
  vm.runInContext(extractFunction(menu.replace(sidebarSlot,'children:[qn,J,null,m,h]'),'Ri'),nativeContext);
  nativeHooks.begin();
  const regressionTree=expandNative(nativeContext.Ri({sidebarFooter:{profileIdentity:{displayName:'Fixture owner'}},open:true,onClose:()=>{}}));
  assert.equal(flatten(regressionTree).filter(node=>node.type===context.CodexMuxAccountMenu).length,0,'negative control reproduces missing account manager with legacy-only patch');
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
  context.__codexMuxConnectedAccounts=accounts;context.Gi=resetState;
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
  Object.assign(context,{Ei:()=>{},$:()=>{},Qu:value=>value,rl:{ONE_MINUTE:60000,FIVE_SECONDS:5000},ksr:value=>value,Xl:()=>queryClient,bf:()=>key=>invalidations.push(Array.from(key)),es:value=>value,rsr:(previous,code,id)=>({previous,code,id})});
  vm.runInContext(extractFunction(initial,'Osr')+';'+extractFunction(initial,'jsr'),context);
  const query=context.Osr();assert.deepEqual(Array.from(query.queryKey),['rate-limit-reset-credits','second']);
  assert.equal((await query.queryFn()).available_count,1);authenticated(calls.at(-1));
  const mutation=context.jsr();context.__codexMuxResetAccountId='primary';
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
  assert.ok(profile.includes('Tt=(!i||W&&!G)&&ne===`chatgpt`'),'aggregate query is restricted to owner and not preview');
  assert.ok(profile.includes('Ot=i&&(!W||G)?gt:Dt.data??gt'),'public and preview profiles remain native');
  assert.ok(profile.includes('onSelect:()=>Dt.refetch()'),'selected profile refreshes real query');
  const plugins=read(choose(/\/plugins-settings-[a-f0-9]+\.js$/,'globalThis.CodexMuxPluginScope'));
  assert.ok(plugins.startsWith(dependency),'plugin helpers are ready on direct navigation');
  const modal=read(choose(/\/modal-impl-[a-f0-9]+\.js$/));
  assert.ok(modal.includes('globalThis.CodexMuxUseResetAccountState?.()'));
  assert.ok(modal.includes('window.__codexMuxSelectedUsageWindows??v'));
  assert.ok(modal.includes('if(g!=null&&(!globalThis.__codexMuxResetAccountId||globalThis.__codexMuxResetAccountId===`primary`))'),'secondary selection cannot open primary checkout');
  assert.ok(modal.startsWith(dependency),'modal helpers exist before first render, keeping hook order stable');
  assert.ok(modal.includes('codexMuxResetDispatchAccountId===`primary`&&(je('),'secondary redemption uses captured dispatch identity for quota optimism');
  assert.ok(modal.includes('O=e=>{if(globalThis.__codexMuxResetAccountId&&globalThis.__codexMuxResetAccountId!==`primary`)return;'),'captured purchase callback is guarded');
  assert.ok(modal.includes('const codexMuxResetBody='),'picker rerender gets a fresh native body instead of stale compiler-cached JSX');

  // Run the native purchase wrapper, not only a marker assertion. Retained
  // Primary callbacks must also refuse a click after selecting Secondary.
  let checkout=0,upgrade=0,closed=0;
  Object.assign(context,{
    vt:{c:n=>Array(n).fill(Symbol.for('react.memo_cache_sentinel'))},
    te:()=>scope,i:{},k:()=>false,ie:{PROLITE:'PROLITE',PRO:'PRO',PROMAX:'PROMAX'},Re:{},
    De:()=>()=>{},xe:()=>()=>{checkout++;},ue:()=>()=>{upgrade++;},re:()=>({}),
    A:()=>({get:()=>true}),He:()=>true,Me:()=>false,
    Y:()=>({data:{isEnabled:false}}),q:()=>({data:{payment_methods:[]}}),
    fe:()=>true,ce:()=>{},me:'https://fixture.invalid',yt:{jsx},lt:'NativeUsageBody',
  });
  vm.runInContext(extractFunction(modal,'_t'),context);
  context.__codexMuxResetAccountId='primary';
  const purchase=context._t({currentPlan:'PLUS',onClose:()=>{closed++;}}).props;
  assert.equal(typeof purchase.onAddCredits,'function');
  assert.equal(typeof purchase.onUpgradePlan,'function');
  context.__codexMuxResetAccountId='second';
  purchase.onAddCredits(event);purchase.onUpgradePlan(event);
  assert.deepEqual([checkout,upgrade,closed],[0,0,0],'Secondary never purchases, upgrades or closes via retained Primary callbacks');
  context.__codexMuxResetAccountId='primary';
  purchase.onAddCredits(event);purchase.onUpgradePlan(event);
  assert.deepEqual([checkout,upgrade,closed],[1,1,2],'Primary native purchase actions are preserved');

  // Drive the actual packed outer modal while a redemption is in flight.
  // Its identity comes from the captured scoped mutation, not mutable globals.
  let finishRedemption,optimism=0,quotaWrites=0,completed=0;
  let mutationAccount='second';
  const nativeScope={query:{getData:()=>({account_id:'primary',primary:{usedPercent:50}})},set:()=>{quotaWrites++;},get:atom=>atom==='toast'?{success:()=>{}}:{dataUpdatedAt:1}};
  Object.assign(context,{
    CodexMuxUseResetAccountState:()=>{},te:()=>nativeScope,w:()=>({formatMessage:()=>''}),
    Ot:hooks([]),We:()=>({redeem:()=>new Promise(resolve=>{finishRedemption=resolve;})}),
    pe:()=>({isPending:false,codexMuxAccountId:mutationAccount,mutateAsync:async()=>{}}),
    G:'limits',j:'toast',tt:'optimism',F:{},t:()=>{},Ue:()=>'',
    je:()=>{optimism++;},Fe:()=>[{}],kt:{jsx,jsxs:jsx,Fragment:'fragment'},xt:'NativeResetBody',_:'Message',
  });
  vm.runInContext(extractFunction(modal,'Et'),context);
  context.__codexMuxResetAccountId='second';
  const pendingBody=context.Et({initialAvailableCount:1,onClose:()=>{},onResetComplete:()=>{completed++;}}).props.children[2];
  const pending=pendingBody.props.onResetCredit('fixture-credit',1);
  context.__codexMuxResetAccountId='primary';
  finishRedemption({status:'reset',availableCountBefore:1,creditId:'fixture-credit',resetType:'all'});
  assert.equal((await pending).status,'completed');
  assert.deepEqual([optimism,quotaWrites,completed],[0,0,0],'Secondary redemption does not update Primary or call its completion effect after picker changes');
  mutationAccount='primary';context.Ot=hooks([]);
  const primaryBody=context.Et({initialAvailableCount:1,onClose:()=>{},onResetComplete:()=>{completed++;}}).props.children[2];
  const pendingPrimary=primaryBody.props.onResetCredit('fixture-credit',1);
  context.__codexMuxResetAccountId='second';
  finishRedemption({status:'reset',availableCountBefore:1,creditId:'fixture-credit',resetType:'all'});
  await pendingPrimary;
  assert.deepEqual([optimism,quotaWrites,completed],[1,1,1],'Primary redemption still updates Primary and completes if picker changes');

  assert.ok(read(choose(/\/app-shared-[a-f0-9]+\.js$/)).includes('t=globalThis.codexMuxScopePluginRequest?.(e,t)??t'));
  const thread=read(choose(/\/local-conversation-thread-[a-f0-9]+\.js$/,'const CODEX_MUX_THREAD_API ='));
  assert.ok(thread.includes('const CodexMuxThreadReact=Yr(),CodexMuxThreadJsx=mi();'));
  assert.ok(thread.includes('children:[y,b,x,S,C,w,(0,xE.jsx)(CodexMuxThreadSubscription,{}),T,E,D]'));
  assert.ok(thread.includes('globalThis.__codexMuxRendererUiReady.then'),'thread SSE waits safely for lazy helpers');
  assert.ok(initial.includes(`import("./${path.basename(menuPath)}").then(()=>true,()=>false)`));
  console.log('PASS: 26.930 packed Rename, device login, Auto/account mode, reset picker and captured redemption, secondary safety guards, self-v2 profiles, plugin scope and last-request attribution.');
};
