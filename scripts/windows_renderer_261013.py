"""Version-locked 26.1013 renderer adapter for the c4 router feature set.

Keep upstream profile-v2/public profiles and new thread sections intact. Validate
all anchors before writing any bundle; a different desktop must not be guessed.
"""
from __future__ import annotations

import json
from pathlib import Path
import re

from windows_renderer_26915 import remap

VERSION = "26.1013.40934"


def replace(source: str, old: str, new: str) -> str:
    count = source.count(old)
    if count != 1:
        raise RuntimeError(f"26.1013 anchor count {count}: {old[:100]}")
    return source.replace(old, new, 1)


def account_menu_item_alias(source: str) -> str:
    matches = re.findall(
        r'\(0,\$\.jsx\)\(([\w$]+),\{leftIconAsset:A,"aria-label":e,'
        r'className:`opacity-50`,disabled:n,onSelect:r,children:_\},`email`\)',
        source,
    )
    if len(matches) != 1:
        raise RuntimeError(f"Expected one 26.1013 native MenuItem, found {len(matches)}")
    return matches[0]


def _replace_function(source: str, start: str, end: str, replacement: str) -> str:
    if source.count(start) != 1 or source.count(end) != 1:
        raise RuntimeError("26.1013 native function boundaries changed")
    begin = source.index(start)
    finish = source.index(end, begin + len(start))
    return source[:begin] + replacement + source[finish:]


def patch_renderer(extracted: Path, token: str, control_port: int) -> None:
    if json.loads((extracted / "package.json").read_text(encoding="utf-8"))["version"] != VERSION:
        raise RuntimeError("Unsupported 26.1013 renderer version")
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
            raise RuntimeError(f"Expected one 26.1013 {pattern}, found {len(matches)}")
        return matches[0]

    menu_path, menu = one("profile-dropdown-items-*.js", "function sa(e)")
    initial_path, initial = one("app-initial-*.js", "function cxa()")
    shared_path, shared = one("app-shared-*.js", "fZ as nst,")
    modal_path, modal = one("modal-impl-*.js", "function Ct(e)")
    profile_path, profile = one("profile-*.js", "children:[gi,_i,vi,yi,bi,xi,Ci]")
    plugins_path, plugins = one("plugins-settings-*.js", "action:O,children:y")
    thread_path, thread = one("local-conversation-thread-*.js", "function OT(e){let t=(0,AT.c)(59)")

    # These exact exports are reviewed against the official 26.1013 chunk. The
    # query client stays in app-shared; importing it avoids an undefined `lt`.
    if "fZ as nst," not in shared or "nst as Oc," not in initial:
        raise RuntimeError("26.1013 native query-client export changed")
    if "Nt(u,ir,{defaultResetCreditsOpen:!0" not in menu:
        raise RuntimeError("26.1013 native usage modal opener changed")
    menu_item = account_menu_item_alias(menu)
    component = remap((root / "ui" / "account-menu.js").read_text(encoding="utf-8"), {
        "e7": "$", "kXc": "ma", "QLs": "ir", "Lo": "b", "Q": "o",
        "BW": "Nt", "_H": menu_item, "CH": "z", "lt": "CodexMuxQueryClient",
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
    menu = replace(menu, "function sa(e){", component + "function sa(e){")
    menu = replace(menu, "children:[Y,Fn,Hn,null,Un,Wn,null,null,Gn,qn,Qn,$n]",
                   "children:[Y,Fn,(0,$.jsx)(CodexMuxAccountMenu,{}),Hn,null,Un,Wn,null,null,Gn,qn,Qn,$n]")
    # The current sidebar passes sidebarFooter and returns through its footer
    # list before reaching the legacy list above. Mount the same isolated React
    # component in its native additionalItems slot, without changing either layout.
    menu = replace(menu, "children:[jn,Dn,null,g,_]",
                   "children:[jn,Dn,(0,$.jsx)(CodexMuxAccountMenu,{}),null,g,_]")
    pending[menu_path] = f'import{{nst as CodexMuxQueryClient}}from"./{shared_path.name}";\n' + menu

    # Nonvisual APIs must be ready before the lazy menu mounts. Redemptions
    # capture their selected account at hook creation, not when HTTP resolves.
    # 26.1013 compiles both hooks with a memo cache keyed on nothing; the scoped
    # account can change between renders, so the replacements stay uncached.
    initial = _replace_function(initial, "function cxa(){", "function lxa(e)",
        "function cxa(){DT(),q(null);let e=globalThis.__codexMuxResetAccountId;return Za({queryKey:[`rate-limit-reset-credits`,e??`primary`],queryFn:e?()=>globalThis.codexMuxRateLimitResets(e):uxa,select:lxa,refetchInterval:Zi.ONE_MINUTE,staleTime:Zi.FIVE_SECONDS})}")
    initial = _replace_function(initial, "function dxa(){", "function fxa(e)",
        "function dxa(){let e=Oc(),t=Kh(),n=globalThis.__codexMuxResetAccountId,r=[`rate-limit-reset-credits`,n??`primary`];let codexMuxMutation=Pi({mutationFn:n?i=>globalThis.codexMuxConsumeRateLimitReset(n,i):fxa,onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){let t=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>Fba(e,o,t))}Promise.all([t([`rate-limit-status`]),t(r)])}});return{...codexMuxMutation,codexMuxAccountId:n??`primary`}}")
    initial = replace(initial, "let e=await mg.safeGet(`/wham/profiles/me`)",
        "let e=await(globalThis.codexMuxProfileData?globalThis.codexMuxProfileData(globalThis.__codexMuxSelectedProfileAccountId??null):mg.safeGet(`/wham/profiles/me`))")
    # The request client now lives next to these hooks; scope it in the same
    # bundle so plugin/app requests still carry the selected subscription.
    initial = replace(initial,
        "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);",
        "async sendRequest(e,t,n){t=globalThis.codexMuxScopePluginRequest?.(e,t)??t;if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);")
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

    modal = replace(modal, "let b=y;if(_!=null){",
        "let b=window.__codexMuxSelectedUsageWindows??y;if(_!=null&&(!globalThis.__codexMuxResetAccountId||globalThis.__codexMuxResetAccountId===`primary`)){")
    # Upstream checkout and optimism belong to the desktop's primary identity.
    # Do not make a secondary picker selection buy/reset on the primary account.
    # Defend callbacks too, including a callback captured before the picker
    # switched away from Primary. Rendering no checkout is not the only guard.
    modal = replace(modal, "O=e=>{let t=se(f,`personal`);",
        "O=e=>{if(globalThis.__codexMuxResetAccountId&&globalThis.__codexMuxResetAccountId!==`primary`)return;let t=se(f,`personal`);")
    modal = replace(modal, "void 0:e=>{i(),d({currentPlan:r,defaultTab:`personal`",
        "void 0:e=>{if(globalThis.__codexMuxResetAccountId&&globalThis.__codexMuxResetAccountId!==`primary`)return;i(),d({currentPlan:r,defaultTab:`personal`")
    # Remove only this compiler cache: mutable picker globals cannot invalidate
    # cached JSX/callbacks. Capture the mutation's actual scoped account before
    # awaiting redemption, even if the user switches the picker while in flight.
    modal = _replace_function(modal, "function Ct(e){", "var wt,Tt,Et;",
        "function Ct(e){globalThis.CodexMuxUseResetAccountState?.();let{defaultResetCreditsOpen:r,initialAvailableCount:i,isRateLimitReached:s,onClose:c,onResetComplete:l}=e,u=o(n),d=m(),[f]=(0,Tt.useState)(Ve),[p,h]=(0,Tt.useState)(null),g=ze(),_=g.isPending;let y=()=>{_||c()},x=async(e,t)=>{const codexMuxResetDispatchAccountId=g.codexMuxAccountId??`primary`;let n=t===void 0?i??0:t;if(_||n===0)return{status:`failed`};h(null);let r=u.query.getData(Y),o=await f.redeem({availableCount:n,consume:g.mutateAsync,creditId:e});switch(o.status){case`in_flight`:return{status:`failed`};case`transport_error`:return h(d.formatMessage({id:`codex.rateLimitResetModal.error`,defaultMessage:`Couldn’t reset usage. Please try again`,description:`Error shown when resetting Codex usage fails`})),{creditId:o.creditId,status:`retry`};case`rejected`:return h(Be(o.code,d)),{status:`failed`};case`reset`:{let e=Math.max(o.availableCountBefore-1,0);return h(null),fe(u,R,{availableCountBefore:o.availableCountBefore,componentType:`modal`,isRateLimitReached:s,redemptionMethod:o.creditId==null?`automatic`:`selected_credit`,remainingCount:e}),codexMuxResetDispatchAccountId===`primary`&&(te(u,r,o.resetType),_e(r??null).length>0&&u.set(Qe,r?.account_id??null,u.get(Y).dataUpdatedAt)),u.get(O).success((0,Et.jsx)(a,{id:`codex.upsellBanner.resetUsage.success`,defaultMessage:`Usage reset. You have {remainingCount, plural, one {# left} other {# left}}`,description:`Toast shown after a Codex rate limit reset is used`,values:{remainingCount:e}}),{hasCloseButton:!1}),codexMuxResetDispatchAccountId===`primary`&&l(e),{creditId:o.creditId,remainingCount:e,status:`completed`}}}};const codexMuxResetBody=(0,Et.jsx)(vt,{defaultResetCreditsOpen:r,errorMessage:p,initialAvailableCount:i,isResetting:_,onClose:y,onResetCredit:x});return(0,Et.jsxs)(Et.Fragment,{children:[globalThis.__codexMuxResetAccountSelector??null,globalThis.__codexMuxResetAccountId&&globalThis.__codexMuxResetAccountId!==`primary`?(0,Et.jsx)(`div`,{role:`status`,className:`px-4 py-2 text-sm text-token-text-secondary`,children:`Available resets belong to the selected subscription. To buy credits, use that subscription in the official app.`}):null,codexMuxResetBody]})}")
    dependency = f'import "./{menu_path.name}";\n'
    pending[modal_path] = dependency + modal

    # Aggregate activity only for the owner's private/self view; public profile
    # v2, preview/showcase, edit controls and upstream details remain native.
    profile = replace(profile, "Rt=!a&&G===`chatgpt`", "Rt=(!a||de&&!X)&&G===`chatgpt`")
    profile = replace(profile, "Vt=a?jt:Bt.data", "Vt=a&&(!de||X)?jt:Bt.data??jt")
    profile = replace(profile, "children:[gi,_i,vi,yi,bi,xi,Ci]",
        "children:[(!a||de&&!X)?globalThis.CodexMuxProfileAvatarStack?.({onSelect:()=>Bt.refetch()})??null:null,gi,_i,vi,yi,bi,xi,Ci]")
    pending[profile_path] = dependency + profile
    pending[plugins_path] = dependency + replace(plugins, "action:O,children:y",
        "action:O,children:(0,yo.jsxs)(yo.Fragment,{children:[globalThis.CodexMuxPluginScope?.()??null,y]})")

    thread_component = (root / "ui" / "thread-subscription.js").read_text(encoding="utf-8")
    thread_component = replace(thread_component, "const subscribeEvents = globalThis.codexMuxSubscribeEvents;",
        "const subscribeEvents = globalThis.codexMuxSubscribeEvents || (globalThis.__codexMuxRendererUiReady ? options => {let stop=()=>{};let cancelled=false;void globalThis.__codexMuxRendererUiReady.then(()=>{if(cancelled)return;const subscribe=globalThis.codexMuxSubscribeEvents;if(typeof subscribe===`function`)stop=subscribe(options)});return()=>{cancelled=true;stop()}} : null);")
    thread_component = remap(thread_component, {
        "$n": "ee", "sr": "Hl", "TE": "CodexMuxThreadReact", "zE": "CodexMuxThreadJsx", "K": "Z",
    }).replace("__CODEX_MUX_CONTROL_PORT__", str(control_port)).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    thread_component = replace(thread_component, "function CodexMuxThreadSubscription() {",
        "function CodexMuxThreadSubscription() {\n  const CodexMuxThreadReact=Yn(),CodexMuxThreadJsx=Un();")
    thread = replace(thread, "function OT(e){", thread_component + "\nfunction OT(e){")
    # This shared section list serves both upstream thread panel variants.
    pending[thread_path] = replace(thread, "children:[y,b,x,S,C,w,T,E]",
        "children:[y,b,x,S,C,(0,MT.jsx)(CodexMuxThreadSubscription,{}),w,T,E]")
    index = extracted / "webview" / "index.html"
    pending[index] = replace(index.read_text(encoding="utf-8"), "connect-src &#39;self&#39;",
        f"connect-src &#39;self&#39; http://127.0.0.1:{control_port}")
    # No partial publication when a later native anchor fails validation.
    for target, content in pending.items():
        target.write_text(content, encoding="utf-8")
