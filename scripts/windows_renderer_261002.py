"""Version-locked 26.1002 renderer adapter for the c4 router feature set.

Keep upstream profile-v2/public profiles and new thread sections intact. Validate
all anchors before writing any bundle; a different desktop must not be guessed.
"""
from __future__ import annotations

import json
from pathlib import Path
import re

from windows_renderer_26915 import remap

VERSION = "26.1002.52244"


def replace(source: str, old: str, new: str) -> str:
    count = source.count(old)
    if count != 1:
        raise RuntimeError(f"26.1002 anchor count {count}: {old[:100]}")
    return source.replace(old, new, 1)


def account_menu_item_alias(source: str) -> str:
    matches = re.findall(
        r'\(0,\$\.jsx\)\(([\w$]+),\{leftIconAsset:ke,"aria-label":e,'
        r'className:`opacity-50`,disabled:n,onSelect:r,children:S\},`email`\)',
        source,
    )
    if len(matches) != 1:
        raise RuntimeError(f"Expected one 26.1002 native MenuItem, found {len(matches)}")
    return matches[0]


def _replace_function(source: str, start: str, end: str, replacement: str) -> str:
    if source.count(start) != 1 or source.count(end) != 1:
        raise RuntimeError("26.1002 native function boundaries changed")
    begin = source.index(start)
    finish = source.index(end, begin + len(start))
    return source[:begin] + replacement + source[finish:]


def patch_renderer(extracted: Path, token: str, control_port: int) -> None:
    if json.loads((extracted / "package.json").read_text(encoding="utf-8"))["version"] != VERSION:
        raise RuntimeError("Unsupported 26.1002 renderer version")
    if not re.fullmatch(r"[A-Za-z0-9_-]{32,256}", token):
        raise RuntimeError("Invalid renderer control token")
    if not isinstance(control_port, int) or not 1 <= control_port <= 65535:
        raise RuntimeError("Invalid renderer control port")
    assets = extracted / "webview" / "assets"
    root = Path(__file__).resolve().parent.parent
    pending: dict[Path, str] = {}

    def one(pattern: str, marker: str) -> tuple[Path, str]:
        matches = [(p, p.read_text(encoding="utf-8")) for p in assets.glob(pattern)]
        matches = [(p, text) for p, text in matches if marker in text]
        if len(matches) != 1:
            raise RuntimeError(f"Expected one 26.1002 {pattern}, found {len(matches)}")
        return matches[0]

    menu_path, menu = one("profile-dropdown-items-*.js", "function Gi(e)")
    initial_path, initial = one("app-initial-*.js", "function Ldr()")
    shared_path, shared = one("app-shared-*.js", "AppServerRequestClient is missing a message dispatcher")
    modal_path, modal = one("modal-impl-*.js", "function Et(e)")
    profile_path, profile = one("profile-*.js", "children:[ri,ii,si,pi,mi,hi,_i]")
    plugins_path, plugins = one("plugins-settings-*.js", "action:T,children:g")
    thread_path, thread = one("local-conversation-thread-*.js", "function cE(e){let t=(0,uE.c)(59)")

    # These exact exports are reviewed against the official 26.1002 chunk. The
    # query client moved to app-shared; importing it avoids an undefined `lt`.
    if "hP as s$t," not in shared or "s$t as Zm," not in initial:
        raise RuntimeError("26.1002 native query-client export changed")
    if "O(p,Xt,{defaultResetCreditsOpen:!0" not in menu:
        raise RuntimeError("26.1002 native usage modal opener changed")
    menu_item = account_menu_item_alias(menu)
    component = remap((root / "ui" / "account-menu.js").read_text(encoding="utf-8"), {
        "e7": "$", "kXc": "Qi", "QLs": "Xt", "Lo": "We", "Q": "rt",
        "BW": "O", "_H": menu_item, "CH": "H", "lt": "CodexMuxQueryClient",
        "jLa": "CodexMuxResolveProfileImage", "S2": "CodexMuxRouterIcon",
    })
    component = component.replace("__CODEX_MUX_CONTROL_PORT__", str(control_port)).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    for old, new in {
        "list-apps": "app/list", "list-installed-apps": "app/installed", "read-apps": "app/read",
        "list-mcp-server-status": "mcpServerStatus/list", "login-mcp-server": "mcpServer/oauth/login",
    }.items():
        component = replace(component, f'"{old}"', f'"{new}"')
    component += (
        "\nfunction CodexMuxResolveProfileImage(url){return typeof url===`string`&&url.length>0?url:null}\n"
        "function CodexMuxRouterIcon(e){return(0,$.jsx)(`svg`,{viewBox:`0 0 20 20`,fill:`none`,"
        '"aria-hidden":!0,...e,children:(0,$.jsx)(`path`,{d:`M3 14.5h14M5 12V8m5 4V4m5 8V6`,'
        "stroke:`currentColor`,strokeWidth:1.5,strokeLinecap:`round`})})}\n"
        + "\n".join(f"globalThis.{name}={name};" for name in (
            "codexMuxScopePluginRequest", "codexMuxProfileData", "codexMuxRateLimitResets",
            "codexMuxConsumeRateLimitReset", "CodexMuxUseResetAccountState",
        )) + "\n"
    )
    menu = replace(menu, "function Gi(e){", component + "function Gi(e){")
    menu = replace(menu, "children:[Y,Bn,qn,null,Yn,Xn,null,null,Zn,$n,er,tr]",
                   "children:[Y,Bn,(0,$.jsx)(CodexMuxAccountMenu,{}),qn,null,Yn,Xn,null,null,Zn,$n,er,tr]")
    # The current sidebar passes sidebarFooter and returns through Pi before
    # reaching the legacy list above. Mount the same isolated React component
    # in its native additionalItems slot, without changing either menu layout.
    menu = replace(menu, "children:[Pn,Dn,null,f,p]",
                   "children:[Pn,Dn,(0,$.jsx)(CodexMuxAccountMenu,{}),null,f,p]")
    pending[menu_path] = f'import{{s$t as CodexMuxQueryClient}}from"./{shared_path.name}";\n' + menu

    # Nonvisual APIs must be ready before the lazy menu mounts. Redemptions
    # capture their selected account at hook creation, not when HTTP resolves.
    initial = _replace_function(initial, "function Ldr(){", "function Rdr(e)",
        "function Ldr(){ks(),J(null);let e=globalThis.__codexMuxResetAccountId;return ch({queryKey:[`rate-limit-reset-credits`,e??`primary`],queryFn:e?()=>globalThis.codexMuxRateLimitResets(e):zdr,select:Rdr,refetchInterval:wn.ONE_MINUTE,staleTime:wn.FIVE_SECONDS})}")
    initial = _replace_function(initial, "function Bdr(){", "function Vdr(e)",
        "function Bdr(){let e=Zm(),t=Mc(),n=globalThis.__codexMuxResetAccountId,r=[`rate-limit-reset-credits`,n??`primary`];let codexMuxMutation=na({mutationFn:n?i=>globalThis.codexMuxConsumeRateLimitReset(n,i):Vdr,onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){let t=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>fdr(e,o,t))}Promise.all([t([`rate-limit-status`]),t(r)])}});return{...codexMuxMutation,codexMuxAccountId:n??`primary`}}")
    initial = replace(initial, "let e=await Bh.safeGet(`/wham/profiles/me`)",
        "let e=await(globalThis.codexMuxProfileData?globalThis.codexMuxProfileData(globalThis.__codexMuxSelectedProfileAccountId??null):Bh.safeGet(`/wham/profiles/me`))")
    initial += (
        "\n;(()=>{const p=`http://127.0.0.1:" + str(control_port) + "/v1`,t=`" + token + "`;"
        'const r=async(path,options={})=>{const response=await fetch(p+path,{...options,headers:{"Content-Type":"application/json","X-Codex-Mux-Token":t,...options.headers}});const body=await response.json().catch(()=>({}));if(!response.ok)throw Error(body.error||`Request failed (${response.status})`);return body};'
        "globalThis.__codexMuxPluginAccountId??=`primary`;globalThis.codexMuxScopePluginRequest=(method,params)=>{const id=globalThis.__codexMuxPluginAccountId;if(!id||![`app/list`,`app/installed`,`app/read`,`mcpServerStatus/list`,`mcpServer/oauth/login`].includes(method)||(params!=null&&(typeof params!==`object`||Array.isArray(params))))return params;return{...(params||{}),codexMuxAccountId:id}};"
        "globalThis.codexMuxProfileData=async id=>{const result=await r(`/profile/combined${id?`?accountId=${encodeURIComponent(id)}`:``}`);globalThis.__codexMuxCombinedProfileAccounts=result.accounts||[];return result.profile};"
        "globalThis.codexMuxRateLimitResets=id=>r(`/accounts/${encodeURIComponent(id)}/rate-limit-resets`);"
        "globalThis.codexMuxConsumeRateLimitReset=async(id,input)=>{const result=await r(`/accounts/${encodeURIComponent(id)}/rate-limit-resets/consume`,{method:`POST`,body:JSON.stringify({creditId:input.creditId??null,redeemRequestId:input.redeemRequestId})});if(result.code===`reset`||result.code===`already_redeemed`)globalThis.__codexMuxRefreshResetAccounts?.();return result};})();\n"
        f'globalThis.__codexMuxRendererUiReady??=import("./{menu_path.name}").then(()=>true,()=>false);\n'
    )
    pending[initial_path] = initial
    pending[shared_path] = replace(shared,
        "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);",
        "async sendRequest(e,t,n){t=globalThis.codexMuxScopePluginRequest?.(e,t)??t;if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);")

    modal = replace(modal, "let y=v;if(g!=null){",
        "let y=window.__codexMuxSelectedUsageWindows??v;if(g!=null&&(!globalThis.__codexMuxResetAccountId||globalThis.__codexMuxResetAccountId===`primary`)){")
    # Upstream checkout and optimism belong to the desktop's primary identity.
    # Do not make a secondary picker selection buy/reset on the primary account.
    # Defend callbacks too, including a callback captured before the picker
    # switched away from Primary. Rendering no checkout is not the only guard.
    modal = replace(modal, "k=e=>{let t=G(p,`personal`);",
        "k=e=>{if(globalThis.__codexMuxResetAccountId&&globalThis.__codexMuxResetAccountId!==`primary`)return;let t=G(p,`personal`);")
    modal = replace(modal, "void 0:e=>{r(),f({currentPlan:n,defaultTab:`personal`",
        "void 0:e=>{if(globalThis.__codexMuxResetAccountId&&globalThis.__codexMuxResetAccountId!==`primary`)return;r(),f({currentPlan:n,defaultTab:`personal`")
    # Remove only this compiler cache: mutable picker globals cannot invalidate
    # cached JSX/callbacks. Capture the mutation's actual scoped account before
    # awaiting redemption, even if the user switches the picker while in flight.
    modal = _replace_function(modal, "function Et(e){", "var Dt,Ot,kt;",
        "function Et(e){globalThis.CodexMuxUseResetAccountState?.();let{defaultResetCreditsOpen:n,initialAvailableCount:r,isRateLimitReached:i,onClose:a,onResetComplete:o}=e,s=M(L),l=c(),[u]=(0,Ot.useState)(We),[f,p]=(0,Ot.useState)(null),m=je(),h=m.isPending;let g=()=>{h||a()},v=async(e,t)=>{const codexMuxResetDispatchAccountId=m.codexMuxAccountId??`primary`;let n=t===void 0?r??0:t;if(h||n===0)return{status:`failed`};p(null);let a=s.query.getData(N),c=await u.redeem({availableCount:n,consume:m.mutateAsync,creditId:e});switch(c.status){case`in_flight`:return{status:`failed`};case`transport_error`:return p(l.formatMessage({id:`codex.rateLimitResetModal.error`,defaultMessage:`Couldn’t reset usage. Please try again`,description:`Error shown when resetting Codex usage fails`})),{creditId:c.creditId,status:`retry`};case`rejected`:return p(Ue(c.code,l)),{status:`failed`};case`reset`:{let e=Math.max(c.availableCountBefore-1,0);return p(null),oe(s,I,{availableCountBefore:c.availableCountBefore,componentType:`modal`,isRateLimitReached:i,redemptionMethod:c.creditId==null?`automatic`:`selected_credit`,remainingCount:e}),codexMuxResetDispatchAccountId===`primary`&&(q(s,a,c.resetType),Ve(a??null).length>0&&s.set(tt,a?.account_id??null,s.get(N).dataUpdatedAt)),s.get(ne).success((0,kt.jsx)(d,{id:`codex.upsellBanner.resetUsage.success`,defaultMessage:`Usage reset. You have {remainingCount, plural, one {# left} other {# left}}`,description:`Toast shown after a Codex rate limit reset is used`,values:{remainingCount:e}}),{hasCloseButton:!1}),codexMuxResetDispatchAccountId===`primary`&&o(e),{creditId:c.creditId,remainingCount:e,status:`completed`}}}};const codexMuxResetBody=(0,kt.jsx)(xt,{defaultResetCreditsOpen:n,errorMessage:f,initialAvailableCount:r,isResetting:h,onClose:g,onResetCredit:v});return(0,kt.jsxs)(kt.Fragment,{children:[globalThis.__codexMuxResetAccountSelector??null,globalThis.__codexMuxResetAccountId&&globalThis.__codexMuxResetAccountId!==`primary`?(0,kt.jsx)(`div`,{role:`status`,className:`px-4 py-2 text-sm text-token-text-secondary`,children:`Available resets belong to the selected subscription. To buy credits, use that subscription in the official app.`}):null,codexMuxResetBody]})}")
    dependency = f'import "./{menu_path.name}";\n'
    pending[modal_path] = dependency + modal

    # Aggregate activity only for the owner's private/self view; public profile
    # v2, preview/showcase, edit controls and upstream details remain native.
    profile = replace(profile, "Dt=!r&&R===`chatgpt`", "Dt=(!r||K&&!q)&&R===`chatgpt`")
    profile = replace(profile, "At=r?xt:kt.data", "At=r&&(!K||q)?xt:kt.data??xt")
    profile = replace(profile, "children:[ri,ii,si,pi,mi,hi,_i]",
        "children:[(!r||K&&!q)?globalThis.CodexMuxProfileAvatarStack?.({onSelect:()=>kt.refetch()})??null:null,ri,ii,si,pi,mi,hi,_i]")
    pending[profile_path] = dependency + profile
    pending[plugins_path] = dependency + replace(plugins, "action:T,children:g",
        "action:T,children:(0,yo.jsxs)(yo.Fragment,{children:[globalThis.CodexMuxPluginScope?.()??null,g]})")

    thread_component = (root / "ui" / "thread-subscription.js").read_text(encoding="utf-8")
    thread_component = replace(thread_component, "const subscribeEvents = globalThis.codexMuxSubscribeEvents;",
        "const subscribeEvents = globalThis.codexMuxSubscribeEvents || (globalThis.__codexMuxRendererUiReady ? options => {let stop=()=>{};let cancelled=false;void globalThis.__codexMuxRendererUiReady.then(()=>{if(cancelled)return;const subscribe=globalThis.codexMuxSubscribeEvents;if(typeof subscribe===`function`)stop=subscribe(options)});return()=>{cancelled=true;stop()}} : null);")
    thread_component = remap(thread_component, {
        "$n": "$i", "sr": "Sr", "TE": "CodexMuxThreadReact", "zE": "CodexMuxThreadJsx", "K": "Z",
    }).replace("__CODEX_MUX_CONTROL_PORT__", str(control_port)).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    thread_component = replace(thread_component, "function CodexMuxThreadSubscription() {",
        "function CodexMuxThreadSubscription() {\n  const CodexMuxThreadReact=Gi(),CodexMuxThreadJsx=Ja();")
    thread = replace(thread, "function cE(e){", thread_component + "\nfunction cE(e){")
    # This shared section list serves both upstream thread panel variants.
    pending[thread_path] = replace(thread, "children:[y,b,x,S,C,w,T,E]",
        "children:[y,b,x,S,C,(0,fE.jsx)(CodexMuxThreadSubscription,{}),w,T,E]")
    index = extracted / "webview" / "index.html"
    pending[index] = replace(index.read_text(encoding="utf-8"), "connect-src &#39;self&#39;",
        f"connect-src &#39;self&#39; http://127.0.0.1:{control_port}")
    # No partial publication when a later native anchor fails validation.
    for target, content in pending.items():
        target.write_text(content, encoding="utf-8")
