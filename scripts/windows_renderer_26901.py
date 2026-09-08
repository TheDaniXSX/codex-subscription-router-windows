"""Fail-closed renderer adaptation for the split 26.901 desktop bundles."""
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
            raise RuntimeError(f"26.901 anchor count {text.count(old)}: {old[:120]}")
        return text.replace(old, new, 1)

    def remap(text: str, names: dict[str, str]) -> str:
        # These names are renderer aliases, not user strings or account identifiers.
        for old, new in names.items():
            text, count = re.subn(r"(?<![\w$])" + re.escape(old) + r"(?![\w$])", new, text)
            if not count:
                raise RuntimeError(f"unused 26.901 component alias: {old}")
        return text

    index = extracted / "webview" / "index.html"
    index.write_text(replace(index.read_text(encoding="utf-8"), "connect-src &#39;self&#39;",
                             f"connect-src &#39;self&#39; http://127.0.0.1:{control_port}"), encoding="utf-8")
    primary_path = one("app-primary-*.js")
    primary = primary_path.read_text(encoding="utf-8")
    component = (root / "ui" / "account-menu.js").read_text(encoding="utf-8")
    component = component.replace("__CODEX_MUX_CONTROL_PORT__", str(control_port)).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    component = remap(component, {"e7": "cY", "kXc": "ehn", "QLs": "tq", "Lo": "zx",
                                 "Q": "qv", "BW": "QC", "_H": "xl", "CH": "Sy",
                                 "jLa": "gV", "S2": "jq"})
    for old, new in {"list-apps": "app/list", "list-installed-apps": "app/installed",
                     "read-apps": "app/read", "list-mcp-server-status": "mcpServerStatus/list",
                     "login-mcp-server": "mcpServer/oauth/login"}.items():
        component = replace(component, f'"{old}"', f'"{new}"')
    component += "\n" + "\n".join(f"globalThis.{name}={name};" for name in (
        "codexMuxScopePluginRequest", "codexMuxProfileData", "codexMuxRateLimitResets",
        "codexMuxConsumeRateLimitReset")) + "\n"
    anchor = "function Ymn(e){let t=(0,sY.c)(223),{sidebarFooter:n,triggerButton:r}=e"
    primary = replace(primary, anchor, component + anchor)
    primary = replace(primary, "usageItems:Dt", "usageItems:(0,cY.jsx)(CodexMuxAccountMenu,{})")
    for old in ("sideOffset:6,triggerButton:jt,onOpenChange:c,children:[F,null]",
                "open:s,onOpenChange:c,contentWidth:`panel`,triggerButton:jt,children:Ht"):
        primary = replace(primary, old, old.replace("onOpenChange:c", "onOpenChange:CodexMuxProfileMenuOpenChange(c)"))
    anchor = "function tq(e){let t=(0,Qon.c)(20),{defaultResetCreditsOpen:n,initialAvailableCount:r,isRateLimitReached:i,onClose:a,onResetComplete:o}=e"
    primary = replace(primary, anchor, anchor.replace("{let t=", "{CodexMuxUseResetAccountState();let t="))
    anchor = "let v=_,y;return t[13]!==n||t[14]!==d||t[15]!==g||t[16]!==v||t[17]!==r||t[18]!==m?(y=(0,esn.jsx)(Won,{defaultResetCreditsOpen:n,errorMessage:d,initialAvailableCount:r,isResetting:m,onClose:g,onResetCredit:v}),t[13]=n,t[14]=d,t[15]=g,t[16]=v,t[17]=r,t[18]=m,t[19]=y):y=t[19],y}"
    primary = replace(primary, anchor, "let v=_;return (0,esn.jsx)(Won,{defaultResetCreditsOpen:n,errorMessage:d,initialAvailableCount:r,isResetting:m,onClose:g,onResetCredit:v})}")
    primary = replace(primary, "let y=v;if(g!=null){", "let y=window.__codexMuxSelectedUsageWindows??v;if(g!=null){")
    anchor = "let _e;t[46]===he?_e=t[47]:(_e=(0,eq.jsxs)(mw,{children:[he,ge]}),t[46]=he,t[47]=_e);"
    primary = replace(primary, anchor, "let _e=(0,eq.jsxs)(mw,{children:[he,ge,window.__codexMuxResetAccountSelector??null]});")
    for message in ("You’re out of Codex and Work usage", "You’ve used all Codex and Work usage", "You’ve reached your usage limit"):
        primary = replace(primary, f"defaultMessage:`{message}`", "defaultMessage:`All connected subscriptions are depleted`")
    primary_path.write_text(primary, encoding="utf-8")

    initial_path = one("app-initial-*.js")
    initial = initial_path.read_text(encoding="utf-8")
    anchor = "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);"
    initial = replace(initial, anchor, anchor.replace("{if(", "{t=globalThis.codexMuxScopePluginRequest?.(e,t)??t;if("))
    initial = replace(initial, "let e=await _O.safeGet(`/wham/profiles/me`)", "let e=await(globalThis.codexMuxProfileData?globalThis.codexMuxProfileData(globalThis.__codexMuxSelectedProfileAccountId??null):_O.safeGet(`/wham/profiles/me`))")
    query = "function v2i(){let e=(0,VK.c)(1);WR(),Cb(null);let t;return e[0]===Symbol.for(`react.memo_cache_sentinel`)?(t={queryKey:[`rate-limit-reset-credits`],queryFn:b2i,select:y2i,refetchInterval:lD.ONE_MINUTE,staleTime:lD.FIVE_SECONDS},e[0]=t):t=e[0],Mb(t)}"
    initial = replace(initial, query, "function v2i(){WR();Cb(null);let e=window.__codexMuxResetAccountId;return Mb({queryKey:[`rate-limit-reset-credits`,e??`primary`],queryFn:e?()=>globalThis.codexMuxRateLimitResets(e):b2i,select:y2i,refetchInterval:lD.ONE_MINUTE,staleTime:lD.FIVE_SECONDS})}")
    mutation = "function x2i(){let e=(0,VK.c)(3),t=Ob(),n=sD(),r;return e[0]!==n||e[1]!==t?(r={mutationFn:S2i,onSuccess:(e,r)=>{let{creditId:i}=r,a=e.code;if(a===`reset`||a===`already_redeemed`){let n=e.code===`reset`?e.credit?.id??i:i;t.setQueryData([`rate-limit-reset-credits`],e=>i0i(e,a,n))}Promise.all([n([`rate-limit-status`]),n([`rate-limit-reset-credits`])])}},e[0]=n,e[1]=t,e[2]=r):r=e[2],Fb(r)}"
    initial = replace(initial, mutation, "function x2i(){let e=Ob(),t=sD(),n=window.__codexMuxResetAccountId,r=[`rate-limit-reset-credits`,n??`primary`];return Fb({mutationFn:n?i=>globalThis.codexMuxConsumeRateLimitReset(n,i):S2i,onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){let t=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>i0i(e,o,t))}Promise.all([t([`rate-limit-status`]),t(r)])}})}")
    initial_path.write_text(initial, encoding="utf-8")

    profile_path = one("profile-*.js", "className:`flex flex-col items-center`")
    profile = profile_path.read_text(encoding="utf-8")
    anchor = 'let wt;t[91]!==St||t[92]!==Ct?(wt=(0,$.jsx)(`section`,{"aria-busy":St,className:`flex flex-col items-center`,children:Ct}),t[91]=St,t[92]=Ct,t[93]=wt):wt=t[93];'
    profile = replace(profile, anchor, anchor.replace("children:Ct", "children:globalThis.CodexMuxProfileAvatarStack?.({onSelect:()=>M.refetch()})??Ct"))
    profile_path.write_text(profile, encoding="utf-8")
    plugin_path = one("plugins-settings-*.js", "action:F,children:w})")
    plugin_path.write_text(replace(plugin_path.read_text(encoding="utf-8"), "action:F,children:w})", "action:F,children:[globalThis.CodexMuxPluginScope?.()??null,w]})"), encoding="utf-8")

    anchor = "function uT(e){let t=(0,pT.c)(43),{onForceShow:n,isVisible:r,registerEnvironmentActionCommands:i,onOpenBackgroundAgent:a,onOpenPullRequestSidePanel:o,onOpenSubagentsPanel:s}=e"
    thread_path = one("local-conversation-thread-*.js", anchor)
    thread = thread_path.read_text(encoding="utf-8")
    component = (root / "ui" / "thread-subscription.js").read_text(encoding="utf-8")
    component = component.replace("__CODEX_MUX_CONTROL_PORT__", str(control_port)).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    component = remap(component, {"$n": "Fo", "sr": "Mr", "TE": "ob", "zE": "mT", "K": "Z"})
    thread = replace(thread, anchor, component + "\n" + anchor)
    thread = replace(thread, "children:[d,f,p,m,h,g,_,v,y,b,x,S,C,w,T,E,D]", "children:[d,f,p,m,(0,mT.jsx)(CodexMuxThreadSubscription,{}),h,g,_,v,y,b,x,S,C,w,T,E,D]")
    thread_path.write_text(thread, encoding="utf-8")
