"""Fail-closed source and compatibility checks for the Codex 26.924 profile."""

import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import patch_windows_app as patcher
import windows_renderer_26924 as renderer

TURN_USAGE_FIXTURE = (
    "function DE(e){let t=(0,kE.c)(67);"
    "let Se;t[38]!==se;workedForItem:oe,isCollapsed:ue,previousTurnNumber:v;"
    "t[46]=oe,t[47]=Se;"
    "function SE(e){let t=(0,CE.c)(15);"
    "workedForItem:a,isCollapsed:o,previousTurnNumber:s;"
    "let p;return t[8]!==n||t[9]!==r;"
    "workedForItem:a,isCollapsed:o,onToggle:f})"
    "t[13]=a,t[14]=p):p=t[14],p}var CE,wE"
)


class Renderer26924Tests(unittest.TestCase):
    def test_turn_usage_footer_uses_native_turn_identity_and_is_a_button_sibling(self):
        with tempfile.TemporaryDirectory() as temporary:
            assets = Path(temporary) / "assets"
            assets.mkdir()
            conversation = assets / "conversation-blocks-fixture.js"
            conversation.write_text(
                TURN_USAGE_FIXTURE,
                encoding="utf-8",
            )
            disclosure = assets / "collapsed-turn-disclosure-fixture.js"
            disclosure.write_text(
                "function b(e){const disclosure=(0,S.jsxs)(r,{children:[_,w,T]});"
                "let O;return t[20]===E?O=t[21]",
                encoding="utf-8",
            )
            turn = assets / "local-conversation-turn-fixture.js"
            turn.write_text(
                "function gc(e){workedForItem:di,hasFinalAssistantStarted:",
                encoding="utf-8",
            )

            renderer._patch_turn_usage(assets, ROOT, "synthetic-token", 51234)

            conversation_text = conversation.read_text(encoding="utf-8")
            self.assertIn("function CodexMuxTurnUsage", conversation_text)
            self.assertIn("AE.createElement", conversation_text)
            self.assertIn("synthetic-token", conversation_text)
            self.assertIn("http://127.0.0.1:51234/v1/usage", conversation_text)
            self.assertIn("conversationId:e.conversationId,turnId:e.turnId", conversation_text)
            self.assertIn("t[67]!==e.conversationId||t[68]!==e.turnId", conversation_text)
            self.assertIn("t[15]!==ci||t[16]!==ti", conversation_text)
            self.assertIn("t[15]=ci,t[16]=ti", conversation_text)
            self.assertIn(
                "turnUsage:(0,wE.jsx)(CodexMuxTurnUsage,{threadId:ci,turnId:ti})",
                conversation_text,
            )
            disclosure_text = disclosure.read_text(encoding="utf-8")
            self.assertIn("children:[E,e.turnUsage]", disclosure_text)
            self.assertIn("children:[_,w,T]", disclosure_text)
            self.assertLess(
                disclosure_text.index("children:[_,w,T]"),
                disclosure_text.index("children:[E,e.turnUsage]"),
            )
            self.assertIn(
                "conversationId:d,turnId:h,hasFinalAssistantStarted:",
                turn.read_text(encoding="utf-8"),
            )

    def test_turn_usage_anchor_drift_fails_before_writing_any_bundle(self):
        with tempfile.TemporaryDirectory() as temporary:
            assets = Path(temporary) / "assets"
            assets.mkdir()
            sources = {
                "conversation-blocks-fixture.js": TURN_USAGE_FIXTURE,
                "collapsed-turn-disclosure-fixture.js": (
                    "function b(e){let O;return t[20]===E?O=t[21]"
                ),
                "local-conversation-turn-fixture.js": (
                    "function gc(e){workedForItem:DIFFERENT,hasFinalAssistantStarted:"
                ),
            }
            for name, source in sources.items():
                (assets / name).write_text(source, encoding="utf-8")
            before = {
                path.name: path.read_text(encoding="utf-8")
                for path in assets.iterdir()
            }

            with self.assertRaisesRegex(RuntimeError, "26.915 anchor count 0"):
                renderer._patch_turn_usage(assets, ROOT, "synthetic-token", 51234)

            self.assertEqual(
                before,
                {path.name: path.read_text(encoding="utf-8") for path in assets.iterdir()},
            )

    def test_every_source_identity_field_is_pinned(self):
        package_version = "26.924.2738.0"
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
            '(0,Y.jsx)(A,{leftIconAsset:j,"aria-label":e,className:`opacity-50`,'
            'disabled:t,onSelect:r,children:_},`email`)'
        )
        self.assertEqual(renderer.account_menu_item_alias(menu_item), "A")
        for invalid in (menu_item * 2, menu_item.replace("(0,Y.jsx)", "(0,q.jsx)"), "const A={};"):
            with self.subTest(invalid=invalid[:32]):
                with self.assertRaises(RuntimeError):
                    renderer.account_menu_item_alias(invalid)
        native_usage = "gn(c,At,{defaultResetCreditsOpen:!0,initialAvailableCount:en})"
        self.assertEqual(patcher_usage_alias(native_usage), "gn")

    def test_appshots_gate_preserves_upstream_policy_and_defaults_off(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "package.json").write_text(
                json.dumps({"version": "26.924.22138"}), encoding="utf-8"
            )
            build = root / ".vite" / "build"
            build.mkdir(parents=True)
            assets = root / "webview" / "assets"
            assets.mkdir(parents=True)
            (assets / "app-primary-fixture.js").write_text("", encoding="utf-8")
            path = build / "main-fixture.js"
            original = (
                "let i={appshotsEnabled:Fr(e)};"
                "R&&G.windowsCaptureNativeBridge==null&&(i.appshotsEnabled=!1),"
                "He.setDesktopFeatureAvailability(i);"
            )
            path.write_text(original, encoding="utf-8")
            patcher.patch_windows_appshots_gate(root)
            patched = path.read_text(encoding="utf-8")
            self.assertIn("appshotsEnabled:Fr(e)", patched)
            self.assertIn(
                'i.appshotsEnabled=i.appshotsEnabled&&process.env.CODEX_ROUTER_ENABLE_APPSHOTS==="1"',
                patched,
            )
            self.assertIn(
                '&&G.windowsCaptureNativeBridge!=null),He.setDesktopFeatureAvailability(i);',
                patched,
            )
            self.assertFalse(patcher.verify_windows_appshots_contract(root)["defaultEnabled"])

            # The router may only further restrict upstream policy: Windows
            # eligibility, exact opt-in, and a native bridge are all required.
            conditions = (
                (False, "1", True, False),
                (True, "true", True, False),
                (True, "1", False, False),
                (True, "1", True, True),
            )
            for upstream_policy, opt_in, bridge, expected in conditions:
                with self.subTest(
                    upstream_policy=upstream_policy,
                    opt_in=opt_in,
                    bridge=bridge,
                ):
                    self.assertEqual(
                        upstream_policy and opt_in == "1" and bridge,
                        expected,
                    )

            # Duplicate or drifted anchors must stop patching without writing
            # a partially modified main bundle.
            original_anchor = (
                "R&&G.windowsCaptureNativeBridge==null&&(i.appshotsEnabled=!1),"
                "He.setDesktopFeatureAvailability(i);"
            )
            duplicate = original.replace(original_anchor, original_anchor * 2)
            path.write_text(duplicate, encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "found 2"):
                patcher.patch_windows_appshots_gate(root)
            self.assertEqual(path.read_text(encoding="utf-8"), duplicate)

            drifted = original.replace("G.windowsCaptureNativeBridge", "G.captureBridge")
            path.write_text(drifted, encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "found 0"):
                patcher.patch_windows_appshots_gate(root)
            self.assertEqual(path.read_text(encoding="utf-8"), drifted)

    def test_bootstrap_disables_updater_without_skipping_policy_resolution(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "package.json").write_text(
                json.dumps({"version": "26.924.22138"}), encoding="utf-8"
            )
            build = root / ".vite" / "build"
            build.mkdir(parents=True)
            path = build / "bootstrap-fixture.js"
            source = (
                "CODEX_ELECTRON_USER_DATA_PATH CODEX_ELECTRON_USER_DATA_PATH;"
                "({enableUpdater:j.shouldIncludeUpdater(d,process.platform,process.env)});"
                "l.app.setName(`Codex`),"
                "l.app.setPath(`userData`,n({appDataPath:l.app.getPath(`appData`),buildFlavor:ek,env:process.env}));"
                "if(await n.initialize(),r&&o&&xO(),r||i){"
                "try{await run()}catch(e){await n.startUpdaterAfterStartupFailure(),await ZO(e)};"
                "process.platform===`win32`&&l.app.setAppUserModelId(Qe(ek));"
            )
            path.write_text(source, encoding="utf-8")

            patcher.patch_windows_bootstrap(root)
            patched = path.read_text(encoding="utf-8")
            self.assertIn("enableUpdater:!1", patched)
            self.assertEqual(patched.count("await n.initialize()"), 1)
            self.assertIn("r&&o&&xO()", patched)
            self.assertNotIn("startUpdaterAfterStartupFailure", patched)
            self.assertIn("await ZO(e)", patched)
            self.assertIn(
                "setAppUserModelId(`com.openai.codex.subscription-router`)", patched
            )

            policy = "enableUpdater:j.shouldIncludeUpdater(d,process.platform,process.env)"
            duplicate_source = source.replace(policy, policy + ";" + policy)
            path.write_text(duplicate_source, encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "copied updater policy anchor, found 2"):
                patcher.patch_windows_bootstrap(root)
            self.assertEqual(path.read_text(encoding="utf-8"), duplicate_source)

            missing_recovery = source.replace(
                "await n.startUpdaterAfterStartupFailure(),", ""
            )
            path.write_text(missing_recovery, encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "copied updater recovery anchor, found 0"):
                patcher.patch_windows_bootstrap(root)
            self.assertEqual(path.read_text(encoding="utf-8"), missing_recovery)

    def test_runtime_cache_and_logs_fall_back_to_router_owned_paths(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "package.json").write_text(
                json.dumps({"version": "26.924.22138"}), encoding="utf-8"
            )
            build = root / ".vite" / "build"
            build.mkdir(parents=True)
            network = build / "application-network-startup-fixture.js"
            network.write_text(
                "function En(e){let t=process.env.LOCALAPPDATA??(0,o.join)"
                "((0,f.homedir)(),`AppData`,`Local`);return(0,o.join)(t,...e)}",
                encoding="utf-8",
            )
            bootstrap = build / "bootstrap-fixture.js"
            bootstrap.write_text(
                "if(t===`win32`){let e=n.LOCALAPPDATA??(0,f.join)(r,`AppData`,`Local`);"
                "return(0,f.join)(e,`Codex`,`Logs`)}",
                encoding="utf-8",
            )
            worker = build / "worker.js"
            worker.write_text(
                "if(t===`win32`){let e=n.LOCALAPPDATA??(0,g.join)(r,`AppData`,`Local`);"
                "return(0,g.join)(e,`Codex`,`Logs`)}",
                encoding="utf-8",
            )

            patcher.patch_windows_runtime_paths(root)
            network_text = network.read_text(encoding="utf-8")
            self.assertIn("`runtime-cache`", network_text)
            self.assertIn("CODEX_MUX_HOME", network_text)
            self.assertIn("`Codex Subscription Router`", network_text)
            self.assertIn("e[0]===`OpenAI`&&e[1]===`Codex`", network_text)
            self.assertNotIn("`OpenAI`,`Codex`,...e", network_text)
            for path in (bootstrap, worker):
                text = path.read_text(encoding="utf-8")
                self.assertIn("Codex Subscription Router/logs", text)
                self.assertIn("CODEX_MUX_HOME", text)
                self.assertIn("process.env.CODEX_MUX_HOME?`logs`", text)
                self.assertNotIn("`Codex`,`Logs`", text)

    def test_native_messaging_keeps_official_registry_isolated(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "package.json").write_text(
                json.dumps({"version": "26.924.22138"}), encoding="utf-8"
            )
            build = root / ".vite" / "build"
            build.mkdir(parents=True)
            assets = root / "webview" / "assets"
            assets.mkdir(parents=True)
            (assets / "app-primary-fixture.js").write_text("", encoding="utf-8")
            path = build / "src-fixture.js"
            source = (
                "function oY(e){if(process.platform!==`win32`)return;let t=`${jq}\\\\${e}`;"
                "try{await Oq(`reg`,[`query`,t])}catch{return}await Oq(`reg`,[`delete`,t,`/f`])}"
                "function sY(e){let t=e.manifestPath;process.platform===`win32`&&t!=null&&"
                "await Oq(`reg`,[`add`,`${jq}\\\\${e.nativeHostName}`,`/ve`,`/t`,`REG_SZ`,"
                "`/d`,t,`/f`])}"
                "case`win32`:return n.di(`windows`).map(t=>(0,s.join)(f.default.homedir(),"
                "t,`${e}.json`));"
                "function yJ(e){let t=bJ();return[...t==null?[]:[t],(0,i.join)(e.codexHome,Mq)]"
                ".filter((e,t,n)=>n.indexOf(e)===t)}"
                "case`win32`:return(0,i.join)(process.env.LOCALAPPDATA??"
                "(0,i.join)(r.default.homedir(),`AppData`,`Local`),`OpenAI`,`Codex`,Mq);"
            )
            path.write_text(patcher._native_anchor(root, source), encoding="utf-8")

            patcher.patch_windows_native_messaging_isolation(root)
            patched = path.read_text(encoding="utf-8")
            self.assertNotIn("reg`,[`add`", patched)
            self.assertNotIn("reg`,[`delete`", patched)
            self.assertIn("case`win32`:return[];", patched)
            self.assertIn("CODEX_MUX_HOME", patched)
            self.assertNotIn("`OpenAI`,`Codex`", patched)

            # The negative-form guard belongs to older bundles and must not be
            # accepted as the reviewed 26.924 native-messaging anchor.
            wrong_guard = source.replace(
                "process.platform===`win32`&&t!=null&&",
                "process.platform!==`win32`||t==null||",
            )
            wrong_guard = patcher._native_anchor(root, wrong_guard)
            path.write_text(wrong_guard, encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "registry add anchor, found 0"):
                patcher.patch_windows_native_messaging_isolation(root)
            self.assertEqual(path.read_text(encoding="utf-8"), wrong_guard)

    def test_native_minifier_aliases_are_version_scoped(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "package.json").write_text(
                json.dumps({"version": "26.924.22138"}), encoding="utf-8"
            )
            assets = root / "webview" / "assets"
            assets.mkdir(parents=True)
            (assets / "app-primary-fixture.js").write_text("", encoding="utf-8")
            self.assertEqual(
                patcher._native_anchor(root, "oY sY jq Oq Fy yJ bJ Mq i r"),
                "oy sy j_ O_ di yv bv M_ s f",
            )

    def test_manifest_node_suffix_must_match_the_preserved_binary(self):
        with tempfile.TemporaryDirectory() as temporary:
            resources = Path(temporary)
            cua = resources / "cua_node"
            manifest = {
                "platform": "windows",
                "arch": "x64",
                "target": "windows-x64",
                "node_path": "bin/node.exe",
                "node_repl_path": "bin/node_repl.exe",
                "node_modules": "bin/node_modules",
                "node_version": "24.21.0-cua.1",
                "node_binary_version": "24.21.0",
                "runtime_archive_version": "0.0.24/20260924074400-f52ea85e2a98",
                "runtime_archive_name": "runtime-windows-x64.zip",
            }
            self._write_json(cua / "manifest.json", manifest)
            self._write_pe(cua / "bin" / "node.exe")
            self._write_pe(cua / "bin" / "node_repl.exe")
            package = cua.joinpath(*patcher.CUA_PACKAGE_RELATIVE.parts)
            self._write_json(
                package / "package.json",
                {"name": "@oai/cua", "type": "module", "version": "0.2.5", "main": "index.js"},
            )
            (package / "index.js").write_text("export {};", encoding="utf-8")
            helper = package.joinpath(*patcher.CUA_HELPER_TRANSPORT_RELATIVE.parts)
            helper.parent.mkdir(parents=True, exist_ok=True)
            helper.write_text(
                'stdio:["pipe","pipe","pipe"] windowsHide:!0 .stdin.write( .stdout.on( '
                '.stderr.on( CODEX_HOME computer-use request timed out',
                encoding="utf-8",
            )
            native_pipe = package.joinpath(*patcher.CUA_NATIVE_PIPE_CLIENT_RELATIVE.parts)
            native_pipe.parent.mkdir(parents=True, exist_ok=True)
            native_pipe.write_text(
                "SKY_CUA_NATIVE_PIPE SKY_CUA_NATIVE_PIPE_DIRECTORY createConnection "
                "--parent-pid 8388608 67108864 Computer Use native pipe is unavailable "
                "Computer Use native pipe frame is too large",
                encoding="utf-8",
            )
            self._write_pe(package / "bin" / "windows" / "codex-computer-use.exe")
            self._write_pe(resources / "codex-code-mode-host.exe")

            result = patcher.inspect_computer_use_contract(resources)
            self.assertEqual(result["nodeVersion"], "24.21.0")
            self.assertEqual(result["nodeManifestVersion"], "24.21.0-cua.1")
            manifest["node_binary_version"] = "24.20.0"
            self._write_json(cua / "manifest.json", manifest)
            with self.assertRaisesRegex(RuntimeError, "versions disagree"):
                patcher.inspect_computer_use_contract(resources)

    @staticmethod
    def _write_json(path: Path, value: object) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value), encoding="utf-8")

    @staticmethod
    def _write_pe(path: Path) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(b"MZ\x00fixture")


def patcher_usage_alias(source: str) -> str:
    from windows_renderer_26903 import usage_modal_opener_alias

    return usage_modal_opener_alias(source, "At")


if __name__ == "__main__":
    unittest.main()
