"""Version-scoped native isolation and source qualification for Codex 26.930."""

import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import patch_windows_app as patcher
import qualify_windows_capabilities as capabilities
import windows_native_26930 as native


class Native26930Tests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / "package.json").write_text(json.dumps({"version": "26.930.41038"}), encoding="utf-8")
        self.build = self.root / ".vite" / "build"
        self.build.mkdir(parents=True)

    def bundle(self, name, source):
        path = self.build / name
        path.write_text(source, encoding="utf-8")
        return path

    def test_every_source_identity_field_is_pinned(self):
        fields = patcher.TESTED_SOURCE_BUILDS["26.930.4958.0"]
        patcher.validate_approved_source(SimpleNamespace(package_version="26.930.4958.0", **fields), False)
        for field in fields:
            with self.subTest(field=field):
                source = SimpleNamespace(package_version="26.930.4958.0", **fields)
                setattr(source, field, "unreviewed")
                with self.assertRaises(RuntimeError):
                    patcher.validate_approved_source(source, False)

    def test_bootstrap_keeps_disabled_updater_policy_resolution(self):
        source = (
            "CODEX_ELECTRON_USER_DATA_PATH CODEX_ELECTRON_USER_DATA_PATH;"
            + native.UPDATER_POLICY + ";"
            "p.app.setName(`Codex`),p.app.setPath(`userData`,Dwe({appDataPath:p.app.getPath(`appData`),buildFlavor:Q9,env:process.env}));"
            + native.UPDATER_STARTUP + "run()}catch(e){" + native.UPDATER_RECOVERY + "};"
            "process.platform===`win32`&&(delete process.env.CODEX_WINDOWS_REGISTERED_CORE,delete process.env.CODEX_WINDOWS_SANDBOX_PACKAGE_FAMILY,"
            + native.APP_ID + ");"
        )
        path = self.bundle("bootstrap-fixture.js", source)
        patcher.patch_windows_bootstrap(self.root)
        patched = path.read_text(encoding="utf-8")
        self.assertIn("enableUpdater:!1", patched)
        self.assertEqual(patched.count("await n.initialize()"), 1)
        self.assertIn("await X9(e)", patched)
        self.assertNotIn("startUpdaterAfterStartupFailure", patched)
        self.assertIn("setAppUserModelId(`com.openai.codex.subscription-router`)", patched)
        self.assertIn("setName(`Codex Subscription Router`)", patched)
        self.assertIn("delete process.env.CODEX_WINDOWS_REGISTERED_CORE", patched)
        for invalid in (source.replace(native.UPDATER_POLICY, native.UPDATER_POLICY * 2), source.replace(native.UPDATER_STARTUP, ""), source.replace(native.UPDATER_RECOVERY, "")):
            path.write_text(invalid, encoding="utf-8")
            with self.assertRaises(RuntimeError):
                patcher.patch_windows_bootstrap(self.root)
            self.assertEqual(path.read_text(encoding="utf-8"), invalid)

    def test_cache_and_logs_are_private_with_safe_fallbacks(self):
        network = self.bundle("application-network-startup-fixture.js", native.CACHE)
        bootstrap = self.bundle("bootstrap-fixture.js", native.LOG)
        worker = self.bundle("worker.js", native.LOG)
        patcher.patch_windows_runtime_paths(self.root)
        self.assertEqual(network.read_text(encoding="utf-8"), native.CACHE_PRIVATE)
        for path in (bootstrap, worker):
            self.assertEqual(path.read_text(encoding="utf-8"), native.LOG_PRIVATE)

    def test_runtime_anchor_drift_does_not_partially_rewrite_files(self):
        network = self.bundle("application-network-startup-fixture.js", native.CACHE)
        bootstrap = self.bundle("bootstrap-fixture.js", native.LOG)
        self.bundle("worker.js", native.LOG * 2)
        with self.assertRaisesRegex(RuntimeError, "private logs anchor, found 2"):
            patcher.patch_windows_runtime_paths(self.root)
        self.assertEqual(network.read_text(encoding="utf-8"), native.CACHE)
        self.assertEqual(bootstrap.read_text(encoding="utf-8"), native.LOG)

    def test_native_registry_and_state_are_isolated_without_global_rewrites(self):
        originals = (native.REGISTRY_DELETE, native.REGISTRY_ADD, native.MANIFEST_PATHS, native.STATE_PATHS, native.GLOBAL_STATE)
        unrelated = "const workspace=(0,g.join)(t,`OpenAI`,`Codex`,`workspaces`);"
        source = "\n".join(originals) + unrelated
        path = self.bundle("bootstrap-fixture.js", source)
        patcher.patch_windows_native_messaging_isolation(self.root)
        patched = path.read_text(encoding="utf-8")
        self.assertIn("async function AI(e){return}", patched)
        self.assertIn("async function jI(e){return}", patched)
        self.assertIn("case`win32`:return[];", patched)
        self.assertIn(native.STATE_PATHS_PRIVATE, patched)
        self.assertIn(native.GLOBAL_STATE_PRIVATE, patched)
        self.assertIn(unrelated, patched)
        for anchor in originals:
            self.assertNotIn(anchor, patched)
            for invalid in (source.replace(anchor, ""), source.replace(anchor, anchor * 2)):
                with self.subTest(anchor=anchor[:30]):
                    path.write_text(invalid, encoding="utf-8")
                    with self.assertRaises(RuntimeError):
                        patcher.patch_windows_native_messaging_isolation(self.root)
                    self.assertEqual(path.read_text(encoding="utf-8"), invalid)

    def test_appshots_only_restricts_upstream_policy(self):
        source = "let i={appshotsEnabled:upstreamPolicy(e)};" + native.APPSHOTS
        path = self.bundle("main-fixture.js", source)
        patcher.patch_windows_appshots_gate(self.root)
        patched = path.read_text(encoding="utf-8")
        self.assertIn("appshotsEnabled:upstreamPolicy(e)", patched)
        self.assertIn(native.APPSHOTS_PRIVATE, patched)
        self.assertFalse(patcher.verify_windows_appshots_contract(self.root)["defaultEnabled"])
        for policy, opt_in, bridge, expected in ((False, "1", True, False), (True, "true", True, False), (True, "1", False, False), (True, "1", True, True)):
            self.assertEqual(policy and opt_in == "1" and bridge, expected)
        for invalid in (source.replace(native.APPSHOTS, ""), source.replace(native.APPSHOTS, native.APPSHOTS * 2)):
            path.write_text(invalid, encoding="utf-8")
            with self.assertRaises(RuntimeError):
                patcher.patch_windows_appshots_gate(self.root)
            self.assertEqual(path.read_text(encoding="utf-8"), invalid)

    def test_protocol_registration_still_skips_windows(self):
        path = self.bundle("bootstrap-fixture.js", "function w(){if(process.platform===`win32`)return;e.setAsDefaultProtocolClient(t)}")
        patcher.verify_windows_integration_isolation(self.root)
        path.write_text(path.read_text(encoding="utf-8") + "OpenProjectInCodex", encoding="utf-8")
        with self.assertRaisesRegex(RuntimeError, "self-registering Explorer"):
            patcher.verify_windows_integration_isolation(self.root)

    def test_capability_qualification_uses_exact_new_runtime(self):
        profile = patcher.TESTED_SOURCE_BUILDS["26.930.4958.0"]
        fields = {"treeSha256": "cua_tree_sha256", "nodeVersion": "cua_node_version", "nodeManifestVersion": "cua_node_manifest_version", "runtimeVersion": "cua_runtime_version", "packageVersion": "cua_package_version"}
        contract = {actual: profile[expected] for actual, expected in fields.items()}
        capabilities.validate_approved_computer_use("26.930.4958.0", contract)
        for field in fields:
            with self.subTest(field=field), self.assertRaises(RuntimeError):
                capabilities.validate_approved_computer_use("26.930.4958.0", {**contract, field: "unreviewed"})


if __name__ == "__main__":
    unittest.main()
