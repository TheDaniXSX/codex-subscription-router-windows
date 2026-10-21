"""Exact, fail-closed native desktop adaptations for Codex ASAR 26.1013.40934.

Only the copied desktop is changed. Official helpers, capability eligibility,
transport protocols and native binaries remain upstream-owned. The one
exception: the composer prediction check also accepts the router's loopback
Responses provider; its gate, setting, config and version checks stay upstream.
"""

from pathlib import Path


UPDATER_POLICY = "enableUpdater:n.i.shouldIncludeUpdater(p,process.platform,process.env)"
UPDATER_STARTUP = "if(await n.initialize(),r&&a&&Mk(),r||i){"
UPDATER_RECOVERY = "await n.startUpdaterAfterStartupFailure(),await e.waitForInteractiveLaunch()&&await uA(r)"
APP_ID = "y.app.setAppUserModelId(n.r(pA))"
CACHE = "function ut(e){let t=process.env.LOCALAPPDATA??(0,l.join)((0,u.homedir)(),`AppData`,`Local`);return(0,l.join)(t,...e)}"
CACHE_PRIVATE = "function ut(e){let t=process.env.CODEX_MUX_HOME?(0,l.join)(process.env.CODEX_MUX_HOME,`runtime-cache`):(0,l.join)(process.env.LOCALAPPDATA??(0,l.join)((0,u.homedir)(),`AppData`,`Local`),`Codex Subscription Router`,`runtime-cache`);return(0,l.join)(t,...(e[0]===`OpenAI`&&e[1]===`Codex`?e.slice(2):e))}"
LOG = "if(t===`win32`){let e=n.LOCALAPPDATA??(0,a.join)(r,`AppData`,`Local`);return(0,a.join)(e,`Codex`,`Logs`)}"
LOG_PRIVATE = "if(t===`win32`){let e=n.LOCALAPPDATA??(0,a.join)(r,`AppData`,`Local`);return(0,a.join)(process.env.CODEX_MUX_HOME??e,process.env.CODEX_MUX_HOME?`logs`:`Codex Subscription Router/logs`)}"
REGISTRY_DELETE = "async function wU(e){if(process.platform!==`win32`)return;let t=`${YV}\\\\${e}`;try{await KV(`reg`,[`query`,t])}catch{return}await KV(`reg`,[`delete`,t,`/f`])}"
REGISTRY_ADD = "async function TU(e){let t=e.manifestPath;process.platform===`win32`&&t!=null&&await KV(`reg`,[`add`,`${YV}\\\\${e.nativeHostName}`,`/ve`,`/t`,`REG_SZ`,`/d`,t,`/f`])}"
MANIFEST_PATHS = "case`win32`:return n.jr(`windows`).map(t=>(0,h.join)(g.default.homedir(),t,`${e}.json`));"
STATE_PATHS = "function LH(e){let t=RH();return[...t==null?[]:[t],(0,h.join)(e.codexHome,XV)].filter((e,t,n)=>n.indexOf(e)===t)}"
STATE_PATHS_PRIVATE = "function LH(e){if(process.platform===`win32`)return process.env.CODEX_MUX_HOME?[(0,h.join)(process.env.CODEX_MUX_HOME,XV)]:[];let t=RH();return[...t==null?[]:[t],(0,h.join)(e.codexHome,XV)].filter((e,t,n)=>n.indexOf(e)===t)}"
GLOBAL_STATE = "case`win32`:return(0,h.join)(process.env.LOCALAPPDATA??(0,h.join)(g.default.homedir(),`AppData`,`Local`),`OpenAI`,`Codex`,XV);"
GLOBAL_STATE_PRIVATE = "case`win32`:return(0,h.join)(process.env.CODEX_MUX_HOME??(0,h.join)(process.env.LOCALAPPDATA??(0,h.join)(g.default.homedir(),`AppData`,`Local`),`Codex Subscription Router`),XV);"
WORKER_LOG = "if(t===`win32`){let e=n.LOCALAPPDATA??(0,g.join)(r,`AppData`,`Local`);return(0,g.join)(e,`Codex`,`Logs`)}"
WORKER_LOG_PRIVATE = "if(t===`win32`){let e=n.LOCALAPPDATA??(0,g.join)(r,`AppData`,`Local`);return(0,g.join)(process.env.CODEX_MUX_HOME??e,process.env.CODEX_MUX_HOME?`logs`:`Codex Subscription Router/logs`)}"
APPSHOTS = "z&&G.windowsCaptureNativeBridge==null&&(i.appshotsEnabled=!1),Ke.setDesktopFeatureAvailability(i);"
APPSHOTS_PRIVATE = 'z&&(i.appshotsEnabled=i.appshotsEnabled&&process.env.CODEX_ROUTER_ENABLE_APPSHOTS==="1"&&G.windowsCaptureNativeBridge!=null),Ke.setDesktopFeatureAvailability(i);'
PREDICTION_PROVIDER = "g.modelProvider===`openai`?l?.includes(g.model)&&(_=`unsupported_model`):_=`unsupported_provider`"
PREDICTION_PROVIDER_PRIVATE = "(g.modelProvider===`openai`||g.modelProvider===`codex_router_spend`)?l?.includes(g.model)&&(_=`unsupported_model`):_=`unsupported_provider`"


def _replace(text: str, old: str, new: str, label: str) -> str:
    count = text.count(old)
    if count != 1:
        raise RuntimeError(f"expected one 26.1013 {label} anchor, found {count}")
    return text.replace(old, new, 1)


def _bundle(extracted: Path, pattern: str) -> Path:
    paths = list((extracted / ".vite" / "build").glob(pattern))
    if len(paths) != 1:
        raise RuntimeError(f"expected one 26.1013 {pattern} bundle, found {len(paths)}")
    path = paths[0]
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 64 * 1024 * 1024:
        raise RuntimeError(f"invalid 26.1013 desktop bundle: {path.name}")
    return path


def patch_updater(text: str) -> str:
    text = _replace(text, UPDATER_POLICY, "enableUpdater:!1", "copied updater policy")
    # initialize() resolves the disabled updater's launch policy; removing it
    # can leave startup/UI callers waiting forever. Never enable recovery.
    if text.count(UPDATER_STARTUP) != 1:
        raise RuntimeError("26.1013 updater initialization contract changed")
    return _replace(text, UPDATER_RECOVERY, "await e.waitForInteractiveLaunch()&&await uA(r)", "copied updater recovery")


def patch_app_id(text: str) -> str:
    return _replace(text, APP_ID, "y.app.setAppUserModelId(`com.openai.codex.subscription-router`)", "AppUserModelID")


def patch_runtime_paths(extracted: Path) -> None:
    network = _bundle(extracted, "application-network-startup-*.js")
    # The desktop log root moved out of bootstrap into the shared file logger
    # chunk that bootstrap and early-bootstrap both require.
    logger = _bundle(extracted, "file-based-logger-*.js")
    worker = _bundle(extracted, "worker.js")
    # Validate every anchor first: drift cannot partially rewrite this group.
    changes = [(network, _replace(network.read_text(encoding="utf-8"), CACHE, CACHE_PRIVATE, "private runtime cache"))]
    # The worker keeps its own path alias; each bundle gets its exact anchor.
    for path, old, new in ((logger, LOG, LOG_PRIVATE), (worker, WORKER_LOG, WORKER_LOG_PRIVATE)):
        changes.append((path, _replace(path.read_text(encoding="utf-8"), old, new, "private logs")))
    for path, text in changes:
        path.write_text(text, encoding="utf-8")


def patch_native_messaging_isolation(extracted: Path) -> None:
    # Registration still lives in one of several src-* chunks. Do not touch
    # similarly named functions or legitimate workspace-cache paths elsewhere.
    paths = [p for p in (extracted / ".vite" / "build").glob("src-*.js")
             if REGISTRY_DELETE in p.read_text(encoding="utf-8")]
    if len(paths) != 1:
        raise RuntimeError(f"expected one 26.1013 native registration bundle, found {len(paths)}")
    path = _bundle(extracted, paths[0].name)
    text = path.read_text(encoding="utf-8")
    replacements = (
        (REGISTRY_DELETE, "async function wU(e){return}", "native registry delete"),
        (REGISTRY_ADD, "async function TU(e){return}", "native registry add"),
        (MANIFEST_PATHS, "case`win32`:return[];", "native manifest paths"),
        (STATE_PATHS, STATE_PATHS_PRIVATE, "native state paths"),
        (GLOBAL_STATE, GLOBAL_STATE_PRIVATE, "native global state"),
    )
    for old, new, label in replacements:
        text = _replace(text, old, new, label)
    for old, _, _ in replacements:
        if old in text:
            raise RuntimeError("an official 26.1013 native registration anchor remained")
    path.write_text(text, encoding="utf-8")


def patch_appshots_gate(extracted: Path) -> None:
    path = _bundle(extracted, "main-*.js")
    text = _replace(path.read_text(encoding="utf-8"), APPSHOTS, APPSHOTS_PRIVATE, "Windows Appshots opt-in")
    path.write_text(text, encoding="utf-8")


def patch_prediction_provider(extracted: Path) -> None:
    # thread/resume reports codex_router_spend for every spending history, so
    # upstream would end each prediction as unsupported_provider. The model
    # list check still applies; any other provider is still rejected. The
    # mux leaves the prediction fork itself unbound (it rejects overrides).
    path = _bundle(extracted, "main-*.js")
    text = _replace(path.read_text(encoding="utf-8"), PREDICTION_PROVIDER, PREDICTION_PROVIDER_PRIVATE, "composer prediction provider")
    path.write_text(text, encoding="utf-8")
