"""Exact 26.924 Windows renderer profile; fail closed when a binding moves."""

from __future__ import annotations

import json
from pathlib import Path
import re

from windows_renderer_26903 import usage_modal_opener_alias
from windows_renderer_26915 import remap, replace


def account_menu_item_alias(source: str) -> str:
    matches = re.findall(
        r'\(0,Y\.jsx\)\(([\w$]+),\{leftIconAsset:j,"aria-label":e,'
        r'className:`opacity-50`,disabled:t,onSelect:r,children:_\},`email`\)',
        source,
    )
    if len(matches) != 1:
        raise RuntimeError(
            f"Expected one 26.924 native profile menu item, found {len(matches)}"
        )
    return matches[0]


def _replace_function(text: str, start: str, end: str, replacement: str, label: str) -> str:
    if text.count(start) != 1 or text.count(end) != 1:
        raise RuntimeError(f"Expected one 26.924 {label} function boundary")
    begin = text.index(start)
    finish = text.index(end, begin + len(start))
    if finish <= begin:
        raise RuntimeError(f"Invalid 26.924 {label} function boundaries")
    return text[:begin] + replacement + text[finish:]


def _patch_turn_usage(assets: Path, root: Path, token: str, control_port: int) -> None:
    def one(pattern: str, marker: str) -> Path:
        matches = [
            path
            for path in assets.glob(pattern)
            if marker in path.read_text(encoding="utf-8")
        ]
        if len(matches) != 1:
            raise RuntimeError(
                f"Expected one 26.924 {pattern} with marker, found {len(matches)}"
            )
        return matches[0]

    usage_path = one("conversation-blocks-*.js", "function DE(e){let t=(0,kE.c)(67)")
    usage = usage_path.read_text(encoding="utf-8")
    usage_component = (root / "ui" / "turn-usage.js").read_text(encoding="utf-8")
    usage_component = remap(usage_component, {"React": "AE"})
    usage_component = usage_component.replace(
        "__CODEX_MUX_CONTROL_PORT__", str(control_port)
    ).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    usage = replace(usage, "function DE(e){", usage_component + "\nfunction DE(e){")
    # Both native components memoize their JSX. The added identity props must
    # invalidate those caches when React reuses a row during navigation.
    usage = replace(usage, "function DE(e){let t=(0,kE.c)(67)", "function DE(e){let t=(0,kE.c)(69)")
    usage = replace(usage, "let Se;t[38]!==se", "let Se;t[67]!==e.conversationId||t[68]!==e.turnId||t[38]!==se")
    usage = replace(usage, "t[46]=oe,t[47]=Se", "t[46]=oe,t[47]=Se,t[67]=e.conversationId,t[68]=e.turnId")
    usage = replace(usage, "function SE(e){let t=(0,CE.c)(15)", "function SE(e){let t=(0,CE.c)(17)")
    usage = replace(usage, "let p;return t[8]!==n||t[9]!==r", "let p;return t[15]!==ci||t[16]!==ti||t[8]!==n||t[9]!==r")
    usage = replace(usage, "t[13]=a,t[14]=p):p=t[14],p}var CE,wE", "t[13]=a,t[14]=p,t[15]=ci,t[16]=ti):p=t[14],p}var CE,wE")
    usage = replace(
        usage,
        "workedForItem:oe,isCollapsed:ue,previousTurnNumber:v",
        "workedForItem:oe,conversationId:e.conversationId,turnId:e.turnId,"
        "isCollapsed:ue,previousTurnNumber:v",
    )
    usage = replace(
        usage,
        "workedForItem:a,isCollapsed:o,previousTurnNumber:s",
        "workedForItem:a,conversationId:ci,turnId:ti,isCollapsed:o,previousTurnNumber:s",
    )
    usage = replace(
        usage,
        "workedForItem:a,isCollapsed:o,onToggle:f})",
        "workedForItem:a,turnUsage:(0,wE.jsx)(CodexMuxTurnUsage,{threadId:ci,turnId:ti}),"
        "isCollapsed:o,onToggle:f})",
    )

    disclosure_path = one("collapsed-turn-disclosure-*.js", "function b(e){")
    disclosure = disclosure_path.read_text(encoding="utf-8")
    disclosure = replace(
        disclosure,
        "let O;return t[20]===E?O=t[21]",
        "if(e.turnUsage!=null)E=(0,S.jsx)(`div`,{className:`inline-flex max-w-full "
        "flex-wrap items-center gap-2`,children:[E,e.turnUsage]});"
        "let O;return t[20]===E?O=t[21]",
    )

    turn_path = one("local-conversation-turn-*.js", "function gc(e){")
    turn = turn_path.read_text(encoding="utf-8")
    turn = replace(
        turn,
        "workedForItem:di,hasFinalAssistantStarted:",
        "workedForItem:di,conversationId:d,turnId:h,hasFinalAssistantStarted:",
    )

    # Validate every source anchor before writing any of the three bundles.
    usage_path.write_text(usage, encoding="utf-8")
    disclosure_path.write_text(disclosure, encoding="utf-8")
    turn_path.write_text(turn, encoding="utf-8")


def patch_renderer(extracted: Path, token: str, control_port: int) -> None:
    if json.loads((extracted / "package.json").read_text(encoding="utf-8"))["version"] != "26.924.22138":
        raise RuntimeError("Unsupported 26.924 renderer version")
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
                f"Expected one 26.924 {pattern} with marker, found {len(matches)}"
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

    # The profile dropdown was split out of app-initial in 26.924. Inject the
    # router controls beside the native usage items, where the native MenuItem
    # and modal-opening bindings are in scope.
    menu_path = one("profile-dropdown-items-*.js", "function In(e)")
    menu = menu_path.read_text(encoding="utf-8")
    menu_item = account_menu_item_alias(menu)
    modal_opener = usage_modal_opener_alias(menu, "At")
    component = (root / "ui" / "account-menu.js").read_text(encoding="utf-8")
    component = remap(
        component,
        {
            "e7": "Y",
            "kXc": "Kn",
            "QLs": "At",
            "Lo": "ge",
            "Q": "Ye",
            "BW": modal_opener,
            "_H": menu_item,
            "CH": "Ue",
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
        "function CodexMuxRouterIcon(e){return(0,Y.jsx)(`svg`,{viewBox:`0 0 20 20`,fill:`none`,"
        '"aria-hidden":!0,...e,children:(0,Y.jsx)(`path`,{d:`M3 14.5h14M5 12V8m5 4V4m5 8V6`,'
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
        "function In(e){",
        component + "\nfunction In(e){",
    )
    menu = replace(
        menu,
        "children:[r,w,s,T,E,b,te,i,null,k,ie,null,ae]",
        "children:[r,w,s,T,E,b,(0,q.jsx)(CodexMuxAccountMenu,{}),te,i,null,k,ie,null,ae]",
    )
    menu_path.write_text(menu, encoding="utf-8")

    # These non-visual APIs are installed in the eager app-initial chunk. The
    # dropdown itself is lazy-loaded, but request routing and profile queries
    # must already be scoped before a user first opens that menu.
    initial_path = one("app-initial-*.js", "function Ppn()")
    initial = initial_path.read_text(encoding="utf-8")
    initial = _replace_function(
        initial,
        "function Ppn(){",
        "function Fpn(e)",
        "function Ppn(){ko(),Z(null);let e=globalThis.__codexMuxResetAccountId;return oh({queryKey:[`rate-limit-reset-credits`,e??`primary`],queryFn:e?()=>globalThis.codexMuxRateLimitResets(e):Ipn,select:Fpn,refetchInterval:Ds.ONE_MINUTE,staleTime:Ds.FIVE_SECONDS})}",
        "rate-limit reset query",
    )
    initial = _replace_function(
        initial,
        "function Lpn(){",
        "function Rpn(e)",
        "function Lpn(){let e=kr(),t=nr(),n=globalThis.__codexMuxResetAccountId,r=[`rate-limit-reset-credits`,n??`primary`];return eu({mutationFn:n?i=>globalThis.codexMuxConsumeRateLimitReset(n,i):Rpn,onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){let t=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>spn(e,o,t))}Promise.all([t([`rate-limit-status`]),t(r)])}})}",
        "rate-limit reset mutation",
    )
    profile_fetch = "let e=await dc.safeGet(`/wham/profiles/me`)"
    initial = replace(
        initial,
        profile_fetch,
        "let e=await(globalThis.codexMuxProfileData?globalThis.codexMuxProfileData(globalThis.__codexMuxSelectedProfileAccountId??null):dc.safeGet(`/wham/profiles/me`))",
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
    if "//# sourceMappingURL=app-initial-ff48311587c5.js.map" not in initial:
        raise RuntimeError("26.924 app-initial source map marker changed")
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
        "let y=v;if(g!=null){",
        "let y=window.__codexMuxSelectedUsageWindows??v;if(g!=null){",
    )
    modal = replace(
        modal,
        "):y=t[18],y}var Dt,Ot,kt;",
        "):y=t[18],globalThis.__codexMuxResetAccountSelector?(0,kt.jsxs)(kt.Fragment,{children:[globalThis.__codexMuxResetAccountSelector,y]}):y}var Dt,Ot,kt;",
    )
    modal_path.write_text(modal, encoding="utf-8")

    banner_path = one("banner-*.js", "You’re out of Codex and Work usage")
    banner = banner_path.read_text(encoding="utf-8")
    for message in (
        "You’re out of Codex and Work usage",
        "You’ve used all Codex and Work usage",
        "You’ve reached your usage limit",
    ):
        banner = replace(
            banner,
            f"defaultMessage:`{message}`",
            "defaultMessage:`All connected subscriptions are depleted`",
        )
    banner_path.write_text(banner, encoding="utf-8")

    profile_path = one("profile-*.js", 'children:[wr,Tr]')
    profile = profile_path.read_text(encoding="utf-8")
    # app-initial starts this same lazy import, but profile navigation may
    # evaluate before that promise settles. Make the shared UI bundle a real
    # module dependency so its global React wrappers exist before this chunk's
    # top-level component code can render.
    menu_dependency = f'import "./{menu_path.name}";\n'
    if menu_dependency in profile:
        raise RuntimeError("26.924 profile UI dependency was already injected")
    profile = replace(
        profile,
        "children:[wr,Tr]",
        "children:[globalThis.CodexMuxProfileAvatarStack?.({onSelect:()=>re()})??null,wr,Tr]",
    )
    profile_path.write_text(menu_dependency + profile, encoding="utf-8")

    plugins_path = one("plugins-settings-b59e65830e0f.js", "action:oe,children:pe")
    plugins = plugins_path.read_text(encoding="utf-8")
    if menu_dependency in plugins:
        raise RuntimeError("26.924 plugins UI dependency was already injected")
    plugins = replace(
        plugins,
        "action:oe,children:pe",
        "action:oe,children:(0,J.jsxs)(J.Fragment,{children:[globalThis.CodexMuxPluginScope?.()??null,pe]})",
    )
    plugins_path.write_text(menu_dependency + plugins, encoding="utf-8")

    thread_path = one(
        "local-conversation-thread-*.js",
        "function _D(e){let t=(0,bD.c)(48)",
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
        {"$n": "zr", "sr": "Er", "TE": "CodexMuxThreadReact", "zE": "SD", "K": "Q"},
    )
    component = component.replace(
        "__CODEX_MUX_CONTROL_PORT__", str(control_port)
    ).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    component = replace(
        component,
        "function CodexMuxThreadSubscription() {",
        "function CodexMuxThreadSubscription() {\n  const CodexMuxThreadReact=cM;",
    )
    thread = replace(
        thread,
        "function _D(e){",
        component + "\nfunction _D(e){",
    )
    thread = replace(
        thread,
        "children:[j,b,M,N,A,I]",
        "children:[j,b,M,N,(0,SD.jsx)(CodexMuxThreadSubscription,{}),A,I]",
    )
    thread_path.write_text(thread, encoding="utf-8")

    # A collapsed completed turn exposes the same native conversationId and
    # turnId that the app-server uses for attribution. Keep the controls beside
    # the disclosure button, as siblings, and attach the usage component to the
    # native 26.924 collapsible activity row.
    _patch_turn_usage(assets, root, token, control_port)
