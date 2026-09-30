"""Exact 26.928 Windows renderer profile; fail closed when a binding moves."""

from __future__ import annotations

import json
from pathlib import Path
import re

from windows_renderer_26903 import usage_modal_opener_alias
from windows_renderer_26915 import remap, replace


def account_menu_item_alias(source: str) -> str:
    matches = re.findall(
        r'\(0,\$\.jsx\)\(([\w$]+),\{leftIconAsset:Tt,"aria-label":e,'
        r'className:`opacity-50`,disabled:n,onSelect:i,children:v\},`email`\)',
        source,
    )
    if len(matches) != 1:
        raise RuntimeError(
            f"Expected one 26.928 native profile menu item, found {len(matches)}"
        )
    return matches[0]


def _replace_function(text: str, start: str, end: str, replacement: str, label: str) -> str:
    if text.count(start) != 1 or text.count(end) != 1:
        raise RuntimeError(f"Expected one 26.928 {label} function boundary")
    begin = text.index(start)
    finish = text.index(end, begin + len(start))
    if finish <= begin:
        raise RuntimeError(f"Invalid 26.928 {label} function boundaries")
    return text[:begin] + replacement + text[finish:]


def patch_renderer(extracted: Path, token: str, control_port: int) -> None:
    if json.loads((extracted / "package.json").read_text(encoding="utf-8"))["version"] != "26.928.21956":
        raise RuntimeError("Unsupported 26.928 renderer version")
    assets = extracted / "webview" / "assets"
    root = Path(__file__).resolve().parent.parent

    def one(pattern: str, marker: str = "") -> Path:
        matches = [
            path
            for path in assets.glob(pattern)
            if marker in path.read_text(encoding="utf-8")
        ]
        if len(matches) != 1:
            raise RuntimeError(
                f"Expected one 26.928 {pattern} with marker, found {len(matches)}"
            )
        return matches[0]

    index = extracted / "webview" / "index.html"
    index.write_text(
        replace(
            index.read_text(encoding="utf-8"),
            "connect-src &#39;self&#39;",
            f"connect-src &#39;self&#39; http://127.0.0.1:{control_port}",
        ),
        encoding="utf-8",
    )

    # As in 26.924, the router controls sit in the profile dropdown chunk,
    # beside the native usage items, where the MenuItem and modal bindings live.
    menu_path = one("profile-dropdown-items-*.js", "function Pi(e)")
    menu = menu_path.read_text(encoding="utf-8")
    menu_item = account_menu_item_alias(menu)
    modal_opener = usage_modal_opener_alias(menu, "kn")
    component = (root / "ui" / "account-menu.js").read_text(encoding="utf-8")
    component = remap(
        component,
        {
            "e7": "Q",
            "kXc": "Gi",
            "QLs": "kn",
            "Lo": "n",
            "Q": "je",
            "BW": modal_opener,
            "_H": menu_item,
            "CH": "V",
            "jLa": "CodexMuxResolveProfileImage",
            "S2": "CodexMuxRouterIcon",
        },
    )
    component = component.replace(
        "__CODEX_MUX_CONTROL_PORT__", str(control_port)
    ).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    for old, new in {
        '"list-apps"': '"app/list"',
        '"list-installed-apps"': '"app/installed"',
        '"read-apps"': '"app/read"',
        '"list-mcp-server-status"': '"mcpServerStatus/list"',
        '"login-mcp-server"': '"mcpServer/oauth/login"',
    }.items():
        component = replace(component, old, new)
    component += (
        "\n"
        "function CodexMuxResolveProfileImage(url){return typeof url===`string`&&url.length>0?url:null}\n"
        "function CodexMuxRouterIcon(e){return(0,Q.jsx)(`svg`,{viewBox:`0 0 20 20`,fill:`none`,"
        '"aria-hidden":!0,...e,children:(0,Q.jsx)(`path`,{d:`M3 14.5h14M5 12V8m5 4V4m5 8V6`,'
        "stroke:`currentColor`,strokeWidth:1.5,strokeLinecap:`round`})})}\n"
        + "\n".join(
            f"globalThis.{name}={name};"
            for name in (
                "codexMuxScopePluginRequest",
                "codexMuxProfileData",
                "codexMuxRateLimitResets",
                "codexMuxConsumeRateLimitReset",
                "CodexMuxUseResetAccountState",
            )
        )
        + "\n"
    )
    menu = replace(
        menu,
        "function Pi(e){",
        component + "\nfunction Pi(e){",
    )
    # usageItems is `b`; the router menu follows it, as in 26.924.
    menu = replace(
        menu,
        "children:[i,N,P,c,ee,te,b,ie,a,null,L,se,ce,le]",
        "children:[i,N,P,c,ee,te,b,(0,Q.jsx)(CodexMuxAccountMenu,{}),ie,a,null,L,se,ce,le]",
    )
    menu_path.write_text(menu, encoding="utf-8")

    # These non-visual APIs are installed in the eager app-initial chunk. The
    # dropdown itself is lazy-loaded, but request routing and profile queries
    # must already be scoped before a user first opens that menu.
    initial_path = one("app-initial-*.js", "function ber()")
    initial = initial_path.read_text(encoding="utf-8")
    initial = _replace_function(
        initial,
        "function ber(){",
        "function xer(e)",
        "function ber(){Th(),W(null);let e=globalThis.__codexMuxResetAccountId;return wf({queryKey:[`rate-limit-reset-credits`,e??`primary`],queryFn:e?()=>globalThis.codexMuxRateLimitResets(e):Ser,select:xer,refetchInterval:Wd.ONE_MINUTE,staleTime:Wd.FIVE_SECONDS})}",
        "rate-limit reset query",
    )
    initial = _replace_function(
        initial,
        "function Cer(){",
        "function wer(e)",
        "function Cer(){let e=to(),t=jm(),n=globalThis.__codexMuxResetAccountId,r=[`rate-limit-reset-credits`,n??`primary`];return Ih({mutationFn:n?i=>globalThis.codexMuxConsumeRateLimitReset(n,i):wer,onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){let t=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>q9n(e,o,t))}Promise.all([t([`rate-limit-status`]),t(r)])}})}",
        "rate-limit reset mutation",
    )
    profile_fetch = "let e=await vf.safeGet(`/wham/profiles/me`)"
    initial = replace(
        initial,
        profile_fetch,
        "let e=await(globalThis.codexMuxProfileData?globalThis.codexMuxProfileData(globalThis.__codexMuxSelectedProfileAccountId??null):vf.safeGet(`/wham/profiles/me`))",
    )
    bootstrap_api = (
        "\n;(()=>{const p=`http://127.0.0.1:"
        + str(control_port)
        + "/v1`,t=`"
        + token
        + "`;const r=async(path,options={})=>{const response=await fetch(p+path,{...options,headers:{\"Content-Type\":\"application/json\",\"X-Codex-Mux-Token\":t,...options.headers}});const body=await response.json().catch(()=>({}));if(!response.ok)throw Error(body.error||`Request failed (${response.status})`);return body};"
        "globalThis.__codexMuxPluginAccountId??=`primary`;globalThis.codexMuxScopePluginRequest=(method,params)=>{const id=globalThis.__codexMuxPluginAccountId;if(!id||![`app/list`,`app/installed`,`app/read`,`mcpServerStatus/list`,`mcpServer/oauth/login`].includes(method)||(params!=null&&(typeof params!==`object`||Array.isArray(params))))return params;return{...(params||{}),codexMuxAccountId:id}};"
        "globalThis.codexMuxProfileData=async id=>{const result=await r(`/profile/combined${id?`?accountId=${encodeURIComponent(id)}`:``}`);globalThis.__codexMuxCombinedProfileAccounts=result.accounts||[];return result.profile};"
        "globalThis.codexMuxRateLimitResets=id=>r(`/accounts/${encodeURIComponent(id)}/rate-limit-resets`);"
        "globalThis.codexMuxConsumeRateLimitReset=async(id,input)=>{const result=await r(`/accounts/${encodeURIComponent(id)}/rate-limit-resets/consume`,{method:`POST`,body:JSON.stringify({creditId:input.creditId??null,redeemRequestId:input.redeemRequestId})});globalThis.__codexMuxRefreshResetAccounts?.();return result};})();\n"
    )
    # The menu is a lazy chunk, but its global UI helpers are also consumed by
    # the profile, plugin settings, and conversation-thread bundles. Prewarm it
    # from the eager entry point so direct navigation to those surfaces does
    # not permanently render null helpers or miss the thread SSE subscription.
    bootstrap_api += (
        f'globalThis.__codexMuxRendererUiReady??=import("./{menu_path.name}")'
        ".then(()=>true,()=>false);\n"
    )
    if "//# sourceMappingURL=app-initial-fd3c4b862660.js.map" not in initial:
        raise RuntimeError("26.928 app-initial source map marker changed")
    initial += bootstrap_api
    initial_path.write_text(initial, encoding="utf-8")

    shared_path = one("app-shared-*.js", "AppServerRequestClient is missing a message dispatcher")
    shared = shared_path.read_text(encoding="utf-8")
    shared = replace(
        shared,
        "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);",
        "async sendRequest(e,t,n){t=globalThis.codexMuxScopePluginRequest?.(e,t)??t;if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);",
    )
    shared_path.write_text(shared, encoding="utf-8")

    modal_path = one("modal-impl-*.js", "function Et(e)")
    modal = modal_path.read_text(encoding="utf-8")
    modal = replace(
        modal,
        "function Et(e){let t=(0,Dt.c)(19)",
        "function Et(e){globalThis.CodexMuxUseResetAccountState?.();let t=(0,Dt.c)(19)",
    )
    modal = replace(
        modal,
        "let x=b;if(v!=null){",
        "let x=window.__codexMuxSelectedUsageWindows??b;if(v!=null){",
    )
    modal = replace(
        modal,
        "):x=t[18],x}var Dt,Ot,kt;",
        "):x=t[18],globalThis.__codexMuxResetAccountSelector?(0,kt.jsxs)(kt.Fragment,{children:[globalThis.__codexMuxResetAccountSelector,x]}):x}var Dt,Ot,kt;",
    )
    modal_path.write_text(modal, encoding="utf-8")

    # 26.928 no longer ships the static Codex/Work depletion banner messages
    # that older profiles rewrote, so there is no banner patch here.

    profile_path = one("profile-*.js", "children:[Yr,Zr]")
    profile = profile_path.read_text(encoding="utf-8")
    # app-initial starts this same lazy import, but profile navigation may
    # evaluate before that promise settles. Make the shared UI bundle a real
    # module dependency so its global React wrappers exist before this chunk's
    # top-level component code can render.
    menu_dependency = f'import "./{menu_path.name}";\n'
    if menu_dependency in profile:
        raise RuntimeError("26.928 profile UI dependency was already injected")
    # Refresh the same way as the page's own retry button.
    profile = replace(
        profile,
        "children:[Yr,Zr]",
        "children:[globalThis.CodexMuxProfileAvatarStack?.({onSelect:()=>void(r?y.refetch():bt.refetch())})??null,Yr,Zr]",
    )
    profile_path.write_text(menu_dependency + profile, encoding="utf-8")

    plugins_path = one("plugins-settings-cbe96e4a6efb.js", "action:M,children:me")
    plugins = plugins_path.read_text(encoding="utf-8")
    if menu_dependency in plugins:
        raise RuntimeError("26.928 plugins UI dependency was already injected")
    plugins = replace(
        plugins,
        "action:M,children:me",
        "action:M,children:(0,J.jsxs)(J.Fragment,{children:[globalThis.CodexMuxPluginScope?.()??null,me]})",
    )
    plugins_path.write_text(menu_dependency + plugins, encoding="utf-8")

    thread_path = one(
        "local-conversation-thread-*.js",
        "function hE(e){let t=(0,vE.c)(60)",
    )
    thread = thread_path.read_text(encoding="utf-8")
    thread_component = (root / "ui" / "thread-subscription.js").read_text(
        encoding="utf-8"
    )
    thread_component = replace(
        thread_component,
        "const subscribeEvents = globalThis.codexMuxSubscribeEvents;",
        "const subscribeEvents = globalThis.codexMuxSubscribeEvents || "
        "(globalThis.__codexMuxRendererUiReady ? options => {let stop=()=>{};"
        "let cancelled=false;void globalThis.__codexMuxRendererUiReady.then(()=>{"
        "if(cancelled)return;const subscribe=globalThis.codexMuxSubscribeEvents;"
        "if(typeof subscribe===`function`)stop=subscribe(options)});"
        "return()=>{cancelled=true;stop()}} : null);",
    )
    component = remap(
        thread_component,
        {"$n": "h", "sr": "wo", "TE": "CodexMuxThreadReact", "zE": "bE", "K": "Q"},
    )
    component = component.replace(
        "__CODEX_MUX_CONTROL_PORT__", str(control_port)
    ).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    component = replace(
        component,
        "function CodexMuxThreadSubscription() {",
        "function CodexMuxThreadSubscription() {\n  const CodexMuxThreadReact=yE;",
    )
    thread = replace(
        thread,
        "function hE(e){",
        component + "\nfunction hE(e){",
    )
    # After the task usage section (`N`), as in 26.924.
    thread = replace(
        thread,
        "children:[j,M,y,N,P,A,L]",
        "children:[j,M,y,N,(0,bE.jsx)(CodexMuxThreadSubscription,{}),P,A,L]",
    )
    thread_path.write_text(thread, encoding="utf-8")
