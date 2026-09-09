"""Fail-closed renderer adaptation for the split 26.903 desktop bundles."""
from pathlib import Path
import re


def patch_renderer(extracted: Path, token: str, control_port: int) -> None:
    assets = extracted / "webview" / "assets"
    root = Path(__file__).resolve().parent.parent

    def one(pattern: str, marker: str = "") -> Path:
        paths = [p for p in assets.glob(pattern) if marker in p.read_text(encoding="utf-8")]
        if len(paths) != 1:
            raise RuntimeError(f"expected one {pattern} bundle for {marker!r}, found {len(paths)}")
        return paths[0]

    def replace(text: str, old: str, new: str) -> str:
        if text.count(old) != 1:
            raise RuntimeError(f"26.903 anchor count {text.count(old)}: {old[:120]}")
        return text.replace(old, new, 1)

    def remap(text: str, names: dict[str, str]) -> str:
        # These names are renderer aliases, not user strings or account identifiers.
        for old, new in names.items():
            text, count = re.subn(r"(?<![\w$])" + re.escape(old) + r"(?![\w$])", new, text)
            if not count:
                raise RuntimeError(f"unused 26.903 component alias: {old}")
        return text

    index = extracted / "webview" / "index.html"
    index.write_text(replace(index.read_text(encoding="utf-8"), "connect-src &#39;self&#39;",
                             f"connect-src &#39;self&#39; http://127.0.0.1:{control_port}"), encoding="utf-8")
    primary_path = one("app-primary-*.js")
    primary = primary_path.read_text(encoding="utf-8")
    component = (root / "ui" / "account-menu.js").read_text(encoding="utf-8")
    component = component.replace("__CODEX_MUX_CONTROL_PORT__", str(control_port)).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    component = remap(component, {"e7": "dq", "kXc": "Pyn", "QLs": "tG", "Lo": "fo",
                                 "Q": "HE", "BW": "Zv", "_H": "xl", "CH": "of",
                                 "jLa": "uB", "S2": "MG"})
    for old, new in {"list-apps": "app/list", "list-installed-apps": "app/installed",
                     "read-apps": "app/read", "list-mcp-server-status": "mcpServerStatus/list",
                     "login-mcp-server": "mcpServer/oauth/login"}.items():
        component = replace(component, f'"{old}"', f'"{new}"')
    component += "\n" + "\n".join(f"globalThis.{name}={name};" for name in (
        "codexMuxScopePluginRequest", "codexMuxProfileData", "codexMuxRateLimitResets",
        "codexMuxConsumeRateLimitReset")) + "\n"
    anchor = "function kyn(e){let t=(0,uq.c)(223),{sidebarFooter:n,triggerButton:r}=e"
    primary = replace(primary, anchor, component + anchor)
    primary = replace(primary, "usageItems:wt", "usageItems:(0,dq.jsx)(CodexMuxAccountMenu,{})")
    for old in ("sideOffset:6,triggerButton:Ot,onOpenChange:c,children:[F,null]",
                "open:s,onOpenChange:c,contentWidth:`panel`,triggerButton:Ot,children:zt"):
        primary = replace(primary, old, old.replace("onOpenChange:c", "onOpenChange:CodexMuxProfileMenuOpenChange(c)"))
    anchor = "function tG(e){let t=(0,Rdn.c)(20),{defaultResetCreditsOpen:n,initialAvailableCount:r,isRateLimitReached:i,onClose:a,onResetComplete:o}=e"
    primary = replace(primary, anchor, anchor.replace("{let t=", "{CodexMuxUseResetAccountState();let t="))
    anchor = "let v=_,y;return t[13]!==n||t[14]!==d||t[15]!==g||t[16]!==v||t[17]!==r||t[18]!==m?(y=(0,Bdn.jsx)(Adn,{defaultResetCreditsOpen:n,errorMessage:d,initialAvailableCount:r,isResetting:m,onClose:g,onResetCredit:v}),t[13]=n,t[14]=d,t[15]=g,t[16]=v,t[17]=r,t[18]=m,t[19]=y):y=t[19],y}"
    primary = replace(primary, anchor, "let v=_;return (0,Bdn.jsx)(Adn,{defaultResetCreditsOpen:n,errorMessage:d,initialAvailableCount:r,isResetting:m,onClose:g,onResetCredit:v})}")
    primary = replace(primary, "let y=v;if(g!=null){", "let y=window.__codexMuxSelectedUsageWindows??v;if(g!=null){")
    anchor = "let ge;t[46]===me?ge=t[47]:(ge=(0,eG.jsxs)(Eb,{children:[me,he]}),t[46]=me,t[47]=ge);"
    primary = replace(primary, anchor, "let ge=(0,eG.jsxs)(Eb,{children:[me,he,window.__codexMuxResetAccountSelector??null]});")
    for message in ("You’re out of Codex and Work usage", "You’ve used all Codex and Work usage", "You’ve reached your usage limit"):
        primary = replace(primary, f"defaultMessage:`{message}`", "defaultMessage:`All connected subscriptions are depleted`")
    primary_path.write_text(primary, encoding="utf-8")

    initial_path = one("app-initial-*.js")
    initial = initial_path.read_text(encoding="utf-8")
    anchor = "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);"
    initial = replace(initial, anchor, anchor.replace("{if(", "{t=globalThis.codexMuxScopePluginRequest?.(e,t)??t;if("))
    initial = replace(initial, "let e=await vO.safeGet(`/wham/profiles/me`)", "let e=await(globalThis.codexMuxProfileData?globalThis.codexMuxProfileData(globalThis.__codexMuxSelectedProfileAccountId??null):vO.safeGet(`/wham/profiles/me`))")
    query = "function x5i(){let e=(0,lq.c)(1);dz(),sb(null);let t;return e[0]===Symbol.for(`react.memo_cache_sentinel`)?(t={queryKey:[`rate-limit-reset-credits`],queryFn:C5i,select:S5i,refetchInterval:nD.ONE_MINUTE,staleTime:nD.FIVE_SECONDS},e[0]=t):t=e[0],gb(t)}"
    initial = replace(initial, query, "function x5i(){dz();sb(null);let e=window.__codexMuxResetAccountId;return gb({queryKey:[`rate-limit-reset-credits`,e??`primary`],queryFn:e?()=>globalThis.codexMuxRateLimitResets(e):C5i,select:S5i,refetchInterval:nD.ONE_MINUTE,staleTime:nD.FIVE_SECONDS})}")
    mutation = "function w5i(){let e=(0,lq.c)(3),t=fb(),n=eD(),r;return e[0]!==n||e[1]!==t?(r={mutationFn:T5i,onSuccess:(e,r)=>{let{creditId:i}=r,a=e.code;if(a===`reset`||a===`already_redeemed`){let n=e.code===`reset`?e.credit?.id??i:i;t.setQueryData([`rate-limit-reset-credits`],e=>s8i(e,a,n))}Promise.all([n([`rate-limit-status`]),n([`rate-limit-reset-credits`])])}},e[0]=n,e[1]=t,e[2]=r):r=e[2],yb(r)}"
    initial = replace(initial, mutation, "function w5i(){let e=fb(),t=eD(),n=window.__codexMuxResetAccountId,r=[`rate-limit-reset-credits`,n??`primary`];return yb({mutationFn:n?i=>globalThis.codexMuxConsumeRateLimitReset(n,i):T5i,onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){let t=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>s8i(e,o,t))}Promise.all([t([`rate-limit-status`]),t(r)])}})}")
    initial_path.write_text(initial, encoding="utf-8")

    profile_path = one("profile-*.js", "className:`flex flex-col items-center`")
    profile = profile_path.read_text(encoding="utf-8")
    anchor = 'let St;t[91]!==yt||t[92]!==xt?(St=(0,$.jsx)(`section`,{"aria-busy":yt,className:`flex flex-col items-center`,children:xt}),t[91]=yt,t[92]=xt,t[93]=St):St=t[93];'
    profile = replace(profile, anchor, anchor.replace("children:xt", "children:globalThis.CodexMuxProfileAvatarStack?.({onSelect:()=>M.refetch()})??xt"))
    profile_path.write_text(profile, encoding="utf-8")
    plugin_path = one("plugins-settings-*.js", "action:F,children:w})")
    plugin_path.write_text(replace(plugin_path.read_text(encoding="utf-8"), "action:F,children:w})", "action:F,children:[globalThis.CodexMuxPluginScope?.()??null,w]})"), encoding="utf-8")

    anchor = "function iE(e){let t=(0,sE.c)(37),{onForceShow:n,isVisible:r,registerEnvironmentActionCommands:i,onOpenBackgroundAgent:a,onOpenPullRequestSidePanel:o,onOpenSubagentsPanel:s}=e"
    thread_path = one("local-conversation-thread-*.js", anchor)
    thread = thread_path.read_text(encoding="utf-8")
    component = (root / "ui" / "thread-subscription.js").read_text(encoding="utf-8")
    component = component.replace("__CODEX_MUX_CONTROL_PORT__", str(control_port)).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    component = remap(component, {"$n": "ve", "sr": "ds", "TE": "CodexMuxThreadReact", "zE": "cE", "K": "Z"})
    component = replace(component, "function CodexMuxThreadSubscription() {",
                        "function CodexMuxThreadSubscription() {\n  const CodexMuxThreadReact=De();")
    thread = replace(thread, anchor, component + "\n" + anchor)
    thread = replace(thread, "children:[w,p,T,E,C,D]", "children:[w,p,T,E,(0,cE.jsx)(CodexMuxThreadSubscription,{}),C,D]")
    thread_path.write_text(thread, encoding="utf-8")

