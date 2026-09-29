"""Fail-closed coordination adapter for the reviewed Windows 26.924 desktop."""

from __future__ import annotations

import json
from pathlib import Path


CONSTRUCTOR = (
    "constructor(e){this.filePath=e,this.state=Dr(this.filePath),"
    "this.persistDebounced=d(()=>{this.persistScheduledForAtoms&&"
    "(this.persistScheduledForAtoms=!1,this.queueGlobalStatePersistence())},fr)}"
)
SYNC_METHOD = "sendPersistedAtomState(e,t){let n=this.globalState.get(`electron-persisted-atom-state`)??{}"
HELPER_NAME = "codex-router-shared-global-state.cjs"
ATOM_METHOD = (
    "updatePersistedAtomState(e,t,n,r){if(t===`unread-thread-ids-by-host-v1`||jZ(t)&&!this.browserHostManager.isPrimaryWindowPersistenceOwner(e))return;"
    "let i=this.globalState.get(`electron-persisted-atom-state`)??{},a=r==null?n:uVe(i[t]??(r.legacyStorageKey==null?void 0:i[r.legacyStorageKey]),r);"
    "a===void 0?delete i[t]:Object.defineProperty(i,t,{configurable:!0,enumerable:!0,value:a,writable:!0}),this.globalState.set(`electron-persisted-atom-state`,i),this.broadcastPersistedAtomUpdate(e,t,a)}"
)
GLOBAL_GET = '"get-global-state":async({key:e})=>({value:this.getGlobalStateValue(e)})'
GLOBAL_SET = (
    '"set-global-state":async({key:e,value:t})=>{if(_H.has(e))throw Error(`Direct writes to ${e} are not allowed`);'
    'if(e===r.Ma.THREAD_PROJECT_ASSIGNMENTS||e===r.Ma.PROJECTLESS_THREAD_IDS)return{success:!1};'
    'if(e===r.Ma.QUEUED_FOLLOW_UPS){await this.globalState.updateAndPersist(e,()=>t);try{this.windowManager.sendMessageToAllWindows({type:`global-state-updated`,keys:[e]})}'
    'catch(e){gH().warning(`Failed to broadcast committed queue update`,{safe:{},sensitive:{error:e}})}return{success:!0}}return this.setGlobalStateValue(e,t),{success:!0}}'
)
ATOM_SEND = "function peo(e,t,n){e===`unread-thread-ids-by-host-v1`&&Kp.isHostBacked()||aa.dispatchMessage(`persisted-atom-update`,{key:e,value:n==null?t:void 0,deleted:n==null&&t===void 0,recordUpdate:n})}"
ATOM_SYNC = "aa.subscribe(`persisted-atom-sync`,({state:s,canWritePrimaryWindowTabPersistence:c})=>{if(u)return;"
ATOM_UPDATE = "aa.subscribe(`persisted-atom-updated`,({key:e,value:t,deleted:i,savedCustomSectionOperationId:s})=>{let c=i?void 0:t;"
GLOBAL_TRANSFORM_GET = "let{value:i}=await r(`get-global-state`,{params:{key:t}}),a=n(i)"
GLOBAL_TRANSFORM_SET = "r(`set-global-state`,{params:{key:t,value:a}})"
GLOBAL_DIRECT_SET = "(r?.fetchFromHost??fK)(`set-global-state`,{params:{key:t,value:n}})"


def _one(root: Path, pattern: str, marker: str) -> Path:
    paths = [path for path in root.glob(pattern) if marker in path.read_text(encoding="utf-8")]
    if len(paths) != 1:
        raise RuntimeError(f"Expected one 26.924 shared-state {pattern}, found {len(paths)}")
    return paths[0]


def _replace(source: str, old: str, new: str) -> str:
    count = source.count(old)
    if count != 1:
        raise RuntimeError(f"26.924 shared-state anchor count {count}: {old[:90]}")
    return source.replace(old, new, 1)


def patch_shared_global_state(extracted: Path) -> dict:
    extracted = Path(extracted)
    package = json.loads((extracted / "package.json").read_text(encoding="utf-8"))
    if package.get("version") != "26.924.22138":
        raise RuntimeError("Shared desktop state supports only reviewed 26.924.22138")
    build = extracted / ".vite" / "build"
    policy = _one(build, "policy-*.js", "var wr=class{logger=r.Lt(`global-state`)")
    main = _one(build, "main-*.js", SYNC_METHOD)
    policy_source = policy.read_text(encoding="utf-8")
    main_source = main.read_text(encoding="utf-8")
    assets = extracted / "webview" / "assets"
    initial = _one(assets, "app-initial-*.js", ATOM_SEND)
    shared = _one(assets, "app-shared-*.js", GLOBAL_TRANSFORM_GET)
    initial_source = initial.read_text(encoding="utf-8")
    shared_source = shared.read_text(encoding="utf-8")
    helper = (Path(__file__).resolve().parents[1] / "ui" / "shared-global-state.cjs").read_text(encoding="utf-8")
    if HELPER_NAME in policy_source or HELPER_NAME in main_source or (build / HELPER_NAME).exists():
        raise RuntimeError("Shared desktop state adapter is already present")

    patched_policy = _replace(
        policy_source,
        CONSTRUCTOR,
        CONSTRUCTOR[:-1] + f';require("./{HELPER_NAME}").install(this)' + "}",
    )
    patched_main = _replace(
        main_source,
        SYNC_METHOD,
        "sendPersistedAtomState(e,t){"
        f'require("./{HELPER_NAME}").bindRenderer(this,e,message=>{{'
        "let window=p.BrowserWindow.fromWebContents(e);"
        "if(window!=null&&!window.isDestroyed())void p.dialog.showMessageBox(window,{"
        "type:`warning`,title:`Estado compartido de Codex`,message,buttons:[`Aceptar`],noLink:!0"
        "}).catch(()=>{})});"
        "let n=this.globalState.get(`electron-persisted-atom-state`)??{}",
    )
    patched_main = _replace(patched_main, ATOM_METHOD,
        "updatePersistedAtomState(e,t,n,r,s){if(t===`unread-thread-ids-by-host-v1`||jZ(t)&&!this.browserHostManager.isPrimaryWindowPersistenceOwner(e))return;"
        f'return require("./{HELPER_NAME}").setRendererAtom(this,e,t,n,r,s,uVe)' + "}")
    patched_main = _replace(patched_main,
        "this.updatePersistedAtomState(e,t.key,t.deleted?void 0:t.value,t.recordUpdate)",
        "await this.updatePersistedAtomState(e,t.key,t.deleted?void 0:t.value,t.recordUpdate,t.csrBase)")
    patched_main = _replace(patched_main, GLOBAL_GET,
        '"get-global-state":async({key:e})=>({value:this.getGlobalStateValue(e),'
        f'csrBase:require("./{HELPER_NAME}").rendererSnapshot(this,e)' + "})")
    patched_main = _replace(patched_main, GLOBAL_SET,
        '"set-global-state":async({key:e,value:t,csrBase:n})=>{if(_H.has(e))throw Error(`Direct writes to ${e} are not allowed`);'
        'if(e===r.Ma.THREAD_PROJECT_ASSIGNMENTS||e===r.Ma.PROJECTLESS_THREAD_IDS)return{success:!1};'
        f'return require("./{HELPER_NAME}").setRendererGlobal(this,e,t,n)' + "}")
    patched_initial = _replace(initial_source, ATOM_SEND,
        "function peo(e,t,n){if(e===`unread-thread-ids-by-host-v1`&&Kp.isHostBacked())return;"
        "let csrBase=Object.hasOwn(csrSharedAtomBases,e)?{present:!0,value:csrSharedAtomBases[e]}:{present:!1};"
        "t===void 0?delete csrSharedAtomBases[e]:csrSharedAtomBases[e]=JSON.parse(JSON.stringify(t));"
        "aa.dispatchMessage(`persisted-atom-update`,{key:e,value:n==null?t:void 0,deleted:n==null&&t===void 0,recordUpdate:n,csrBase})}")
    patched_initial = _replace(patched_initial, ATOM_SYNC,
        ATOM_SYNC + "csrSharedAtomBases=JSON.parse(JSON.stringify(s??{}));")
    patched_initial = _replace(patched_initial, ATOM_UPDATE,
        ATOM_UPDATE + "i?delete csrSharedAtomBases[e]:csrSharedAtomBases[e]=JSON.parse(JSON.stringify(t));")
    patched_initial = "var csrSharedAtomBases=Object.create(null);\n" + patched_initial
    patched_shared = _replace(shared_source, GLOBAL_TRANSFORM_GET,
        "let{value:i,csrBase:c}=await r(`get-global-state`,{params:{key:t}}),a=n(i)")
    patched_shared = _replace(patched_shared, GLOBAL_TRANSFORM_SET,
        "r(`set-global-state`,{params:{key:t,value:a,csrBase:c}})")
    patched_shared = _replace(patched_shared, GLOBAL_DIRECT_SET,
        "(r?.fetchFromHost??fK)(`set-global-state`,{params:{key:t,value:n,csrBase:a?.csrBase}})")
    # All anchors and source inputs are validated before writing any bundle.
    policy.write_text(patched_policy, encoding="utf-8")
    main.write_text(patched_main, encoding="utf-8")
    initial.write_text(patched_initial, encoding="utf-8")
    shared.write_text(patched_shared, encoding="utf-8")
    (build / HELPER_NAME).write_text(helper, encoding="utf-8")
    return {
        "version": 1,
        "sourceVersion": package["version"],
        "protocol": "filesystem-leaf-cas-v1",
        "helper": f".vite/build/{HELPER_NAME}",
        "policyBundle": policy.name,
        "mainBundle": main.name,
        "rendererBundles": [initial.name, shared.name],
        "rendererWrites": "snapshot-required-leaf-cas",
        "arrays": "atomic-compare-and-set",
        "conflicts": "preserve-external-and-journal-local",
    }
