"""Fail-closed source and compatibility checks for the Codex 26.928 profile."""

import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import patch_windows_app as patcher
import windows_renderer_26928 as renderer


class Renderer26928Tests(unittest.TestCase):
    def test_every_source_identity_field_is_pinned(self):
        package_version = "26.928.2636.0"
        fields = patcher.TESTED_SOURCE_BUILDS[package_version]
        patcher.validate_approved_source(
            SimpleNamespace(package_version=package_version, **fields), False
        )
        for field in fields:
            with self.subTest(field=field):
                source = SimpleNamespace(package_version=package_version, **fields)
                setattr(source, field, "unreviewed")
                with self.assertRaises(RuntimeError):
                    patcher.validate_approved_source(source, False)

    def test_profile_menu_and_usage_modal_bindings_fail_closed(self):
        menu_item = (
            '(0,$.jsx)(t,{leftIconAsset:Tt,"aria-label":e,className:`opacity-50`,'
            'disabled:n,onSelect:i,children:v},`email`)'
        )
        self.assertEqual(renderer.account_menu_item_alias(menu_item), "t")
        for invalid in (menu_item * 2, menu_item.replace("(0,$.jsx)", "(0,Q.jsx)"), "const t={};"):
            with self.subTest(invalid=invalid[:32]):
                with self.assertRaises(RuntimeError):
                    renderer.account_menu_item_alias(invalid)
        native_usage = "Ye(c,kn,{defaultResetCreditsOpen:!0,initialAvailableCount:en})"
        self.assertEqual(patcher_usage_alias(native_usage), "Ye")

    def test_renderer_rejects_other_versions(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "package.json").write_text(
                json.dumps({"version": "26.924.22138"}), encoding="utf-8"
            )
            with self.assertRaisesRegex(RuntimeError, "Unsupported 26.928 renderer version"):
                renderer.patch_renderer(root, "token", 49826)

    def test_appshots_gate_preserves_upstream_policy_and_defaults_off(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self._split_root(temporary)
            path = root / ".vite" / "build" / "main-fixture.js"
            anchor = (
                "L&&H.windowsCaptureNativeBridge==null&&(i.appshotsEnabled=!1),"
                "We.setDesktopFeatureAvailability(i);"
            )
            original = "let i=si(e);" + anchor
            path.write_text(original, encoding="utf-8")
            patcher.patch_windows_appshots_gate(root)
            patched = path.read_text(encoding="utf-8")
            self.assertIn("let i=si(e);", patched)
            self.assertIn(
                'L&&(i.appshotsEnabled=i.appshotsEnabled&&process.env.CODEX_ROUTER_ENABLE_APPSHOTS==="1"'
                '&&H.windowsCaptureNativeBridge!=null),We.setDesktopFeatureAvailability(i);',
                patched,
            )
            self.assertFalse(patcher.verify_windows_appshots_contract(root)["defaultEnabled"])

            # The 26.924 gate names must not satisfy the 26.928 profile.
            drifted = original.replace("H.windowsCaptureNativeBridge", "G.windowsCaptureNativeBridge")
            path.write_text(drifted, encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "found 0"):
                patcher.patch_windows_appshots_gate(root)
            self.assertEqual(path.read_text(encoding="utf-8"), drifted)

    def test_bootstrap_disables_updater_without_skipping_policy_resolution(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self._split_root(temporary)
            path = root / ".vite" / "build" / "bootstrap-fixture.js"
            source = (
                "CODEX_ELECTRON_USER_DATA_PATH CODEX_ELECTRON_USER_DATA_PATH;"
                "({enableUpdater:u.t.shouldIncludeUpdater(f,process.platform,process.env)});"
                "p.app.setName(r.zt(Q9)),"
                "p.app.setPath(`userData`,n({appDataPath:p.app.getPath(`appData`),buildFlavor:Q9,env:process.env}));"
                "if(await n.initialize(),r&&a&&ETe(),r||i){"
                "try{await run()}catch(e){await n.startUpdaterAfterStartupFailure(),await X9(e)};"
                "process.platform===`win32`&&p.app.setAppUserModelId(vY(Q9));"
            )
            path.write_text(source, encoding="utf-8")

            patcher.patch_windows_bootstrap(root)
            patched = path.read_text(encoding="utf-8")
            self.assertIn("enableUpdater:!1", patched)
            self.assertEqual(patched.count("await n.initialize()"), 1)
            self.assertIn("r&&a&&ETe()", patched)
            self.assertNotIn("startUpdaterAfterStartupFailure", patched)
            self.assertIn("await X9(e)", patched)
            self.assertIn("p.app.setName(`Codex Subscription Router`)", patched)
            self.assertIn(
                "setAppUserModelId(`com.openai.codex.subscription-router`)", patched
            )

            missing_recovery = source.replace(
                "await n.startUpdaterAfterStartupFailure(),", ""
            )
            path.write_text(missing_recovery, encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "copied updater recovery anchor, found 0"):
                patcher.patch_windows_bootstrap(root)
            self.assertEqual(path.read_text(encoding="utf-8"), missing_recovery)

    def test_runtime_cache_and_logs_fall_back_to_router_owned_paths(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self._split_root(temporary)
            build = root / ".vite" / "build"
            network = build / "application-network-startup-fixture.js"
            network.write_text(
                "function pn(e){let t=process.env.LOCALAPPDATA??(0,c.join)"
                "((0,m.homedir)(),`AppData`,`Local`);return(0,c.join)(t,...e)}",
                encoding="utf-8",
            )
            logs = (
                "if(t===`win32`){let e=n.LOCALAPPDATA??(0,g.join)(r,`AppData`,`Local`);"
                "return(0,g.join)(e,`Codex`,`Logs`)}"
            )
            bootstrap = build / "bootstrap-fixture.js"
            bootstrap.write_text(logs, encoding="utf-8")
            worker = build / "worker.js"
            worker.write_text(logs, encoding="utf-8")

            patcher.patch_windows_runtime_paths(root)
            network_text = network.read_text(encoding="utf-8")
            self.assertIn("function pn(e){let t=process.env.CODEX_MUX_HOME??(0,c.join)(", network_text)
            self.assertIn("(0,c.join)(t,`runtime-cache`,...e.slice(2))", network_text)
            self.assertIn("e[0]===`OpenAI`&&e[1]===`Codex`", network_text)
            for path in (bootstrap, worker):
                text = path.read_text(encoding="utf-8")
                self.assertIn("process.env.CODEX_MUX_HOME?`logs`:`Codex Subscription Router/logs`", text)
                self.assertNotIn("`Codex`,`Logs`", text)

    def test_native_messaging_moved_into_bootstrap_is_isolated(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self._split_root(temporary)
            build = root / ".vite" / "build"
            source = (
                "async function oY(e){if(process.platform!==`win32`)return;let t=`${jq}\\\\${e}`;"
                "try{await Oq(`reg`,[`query`,t])}catch{return}await Oq(`reg`,[`delete`,t,`/f`])}"
                "async function sY(e){let t=e.manifestPath;process.platform===`win32`&&t!=null&&"
                "await Oq(`reg`,[`add`,`${jq}\\\\${e.nativeHostName}`,`/ve`,`/t`,`REG_SZ`,"
                "`/d`,t,`/f`])}"
                "case`win32`:return r.Br(`windows`).map(t=>(0,g.join)(C.default.homedir(),"
                "t,`${e}.json`));"
                "function yJ(e){let t=bJ();return[...t==null?[]:[t],(0,i.join)(e.codexHome,Mq)]"
                ".filter((e,t,n)=>n.indexOf(e)===t)}"
                "case`win32`:return(0,i.join)(process.env.LOCALAPPDATA??"
                "(0,i.join)(r.default.homedir(),`AppData`,`Local`),`OpenAI`,`Codex`,Mq);"
            )
            remapped = patcher._native_anchor(root, source.replace("r.Br(`windows`)", "Fy(`windows`)"))
            # The manifest anchor is exact for 26.928 (`r.Br`), not remapped.
            remapped = remapped.replace("Br(`windows`)", "r.Br(`windows`)")

            # 26.928 keeps this code in bootstrap; the older src bundle is ignored.
            stale = build / "src-fixture.js"
            stale.write_text(remapped, encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "registry delete anchor, found 0"):
                patcher.patch_windows_native_messaging_isolation(root)
            stale.unlink()

            path = build / "bootstrap-fixture.js"
            path.write_text(remapped, encoding="utf-8")
            patcher.patch_windows_native_messaging_isolation(root)
            patched = path.read_text(encoding="utf-8")
            self.assertIn("async function OI(e){return}", patched)
            self.assertIn("async function kI(e){return}", patched)
            self.assertNotIn("reg`,[`add`", patched)
            self.assertNotIn("reg`,[`delete`", patched)
            self.assertIn("case`win32`:return[];", patched)
            self.assertIn("(0,g.join)(process.env.CODEX_MUX_HOME,eF)", patched)
            self.assertNotIn("`OpenAI`,`Codex`", patched)

    def test_native_minifier_aliases_are_version_scoped(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self._split_root(temporary)
            self.assertEqual(
                patcher._native_anchor(root, "oY sY jq Oq Fy yJ bJ Mq i r"),
                "OI kI $P XP Br VF HF eF g C",
            )

    @staticmethod
    def _split_root(temporary: str) -> Path:
        root = Path(temporary)
        (root / "package.json").write_text(
            json.dumps({"version": "26.928.21956"}), encoding="utf-8"
        )
        (root / ".vite" / "build").mkdir(parents=True)
        assets = root / "webview" / "assets"
        assets.mkdir(parents=True)
        (assets / "app-primary-fixture.js").write_text("", encoding="utf-8")
        return root


def patcher_usage_alias(source: str) -> str:
    from windows_renderer_26903 import usage_modal_opener_alias

    return usage_modal_opener_alias(source, "kn")


if __name__ == "__main__":
    unittest.main()
