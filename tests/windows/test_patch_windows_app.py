from __future__ import annotations

import importlib.util
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "patch_windows_app.py"
SPEC = importlib.util.spec_from_file_location("patch_windows_app", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
patcher = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = patcher
SPEC.loader.exec_module(patcher)


def write_pe(path: Path, payload: bytes = b"payload") -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(b"MZ" + payload)


class ArgumentTests(unittest.TestCase):
    def test_contract_flags_are_stable(self) -> None:
        args = patcher.parse_args(
            [
                "--source",
                "source",
                "--destination",
                "destination",
                "--mux",
                "mux.exe",
                "--launcher",
                "launcher.exe",
                "--control-port",
                "61234",
                "--force",
                "--dry-run",
                "--allow-untested-source",
            ]
        )
        self.assertEqual(args.source, Path("source"))
        self.assertEqual(args.destination, Path("destination"))
        self.assertEqual(args.mux, Path("mux.exe"))
        self.assertEqual(args.launcher, Path("launcher.exe"))
        self.assertEqual(args.control_port, 61234)
        self.assertTrue(args.force)
        self.assertTrue(args.dry_run)
        self.assertTrue(args.allow_untested_source)


class PathSafetyTests(unittest.TestCase):
    def test_state_root_is_not_resolved_through_msix_virtualization(self) -> None:
        configured = r"C:\Users\example\AppData\Local\Codex Subscription Router"
        with mock.patch.dict(os.environ, {"CODEX_ROUTER_DATA_DIR": configured}):
            with mock.patch.object(Path, "resolve", side_effect=AssertionError("must not resolve")):
                actual = patcher.resolve_state_root()
        self.assertEqual(str(actual), os.path.abspath(os.path.normpath(configured)))

    def test_normalizes_package_root_app_root_and_executable(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "OpenAI.Codex_package"
            app = root / "app"
            (app / "resources").mkdir(parents=True)
            (app / "resources" / "app.asar").write_bytes(b"asar")
            (root / "AppxManifest.xml").write_text("<Package/>", encoding="utf-8")
            write_pe(app / "ChatGPT.exe")

            resolved_root = root.resolve(strict=True)
            resolved_app = app.resolve(strict=True)
            expected = (resolved_root, resolved_app)
            self.assertEqual(patcher.normalize_source_path(root), expected)
            self.assertEqual(patcher.normalize_source_path(app), expected)
            self.assertEqual(patcher.normalize_source_path(app / "ChatGPT.exe"), expected)

    def test_rejects_in_place_and_nested_destination(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / "source"
            source.mkdir()
            with self.assertRaisesRegex(RuntimeError, "never patched in place"):
                patcher.validate_source_destination(source, source)
            with self.assertRaisesRegex(RuntimeError, "never patched in place"):
                patcher.validate_source_destination(source, source / "patched")

    def test_rejects_destination_parent_of_source(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary) / "parent"
            source = parent / "source"
            source.mkdir(parents=True)
            with self.assertRaisesRegex(RuntimeError, "cannot be a parent"):
                patcher.validate_source_destination(source, parent)


class SourceApprovalTests(unittest.TestCase):
    def make_source(self, **overrides: str) -> object:
        expected = patcher.TESTED_SOURCE_BUILDS["26.820.9563.0"]
        values = {
            "package_root": None,
            "app_root": Path("C:/source/app"),
            "package_name": "OpenAI.Codex",
            "package_version": "26.820.9563.0",
            "package_full_name": "OpenAI.Codex_26.820.9563.0_x64_test",
            "asar_version": expected["asar_version"],
            "asar_build": expected["asar_build"],
            "asar_sha256": expected["asar_sha256"],
            "codex_sha256": expected["codex_sha256"],
            "chatgpt_sha256": expected["chatgpt_sha256"],
            "codex_launcher_sha256": expected["codex_launcher_sha256"],
            "windows_account_sha256": expected["windows_account_sha256"],
            "cua_tree_sha256": expected["cua_tree_sha256"],
            "cua_node_version": expected["cua_node_version"],
            "cua_runtime_version": expected["cua_runtime_version"],
            "cua_package_version": expected["cua_package_version"],
        }
        values.update(overrides)
        return patcher.SourceInfo(**values)

    def test_accepts_exact_approved_build(self) -> None:
        patcher.validate_approved_source(self.make_source(), False)

    def test_rejects_hash_mismatch_without_escape_hatch(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "not approved"):
            patcher.validate_approved_source(self.make_source(asar_sha256="0" * 64), False)

    def test_rejects_computer_use_tree_mismatch_without_escape_hatch(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "Computer Use tree SHA-256"):
            patcher.validate_approved_source(
                self.make_source(cua_tree_sha256="0" * 64), False
            )

    def test_allow_untested_does_not_raise(self) -> None:
        with mock.patch("sys.stderr"):
            patcher.validate_approved_source(
                self.make_source(package_version="99.0.0.0"), True
            )


class TokenTests(unittest.TestCase):
    def test_prepare_does_not_write_new_token(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "state"
            token, is_new = patcher.prepare_control_token(root)
            self.assertRegex(token, r"^[0-9a-f]{64}$")
            self.assertTrue(is_new)
            self.assertFalse(root.exists())

    def test_persist_is_exclusive_and_idempotent(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "state"
            token = "a" * 64
            self.assertTrue(patcher.persist_control_token(root, token))
            self.assertFalse(patcher.persist_control_token(root, token))
            self.assertEqual((root / "control-token").read_text(encoding="utf-8"), token)

    def test_rejects_invalid_existing_token(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "control-token").write_text("not-a-token", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "invalid control token"):
                patcher.prepare_control_token(root)


class AnchorTests(unittest.TestCase):
    def test_replace_unique_fails_closed(self) -> None:
        self.assertEqual(patcher.replace_unique("abc", "b", "B", "test"), "aBc")
        for text in ("abcabc", "xyz"):
            with self.assertRaisesRegex(RuntimeError, "expected one test anchor"):
                patcher.replace_unique(text, "abc", "x", "test")

    def test_identifier_replacement_uses_js_boundaries(self) -> None:
        result = patcher.replace_identifiers("e7.jsx e70 $n(sr)", {"e7": "p8", "$n": "Rt"})
        self.assertEqual(result, "p8.jsx e70 Rt(sr)")

    def test_runtime_and_log_roots_are_isolated(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            build = Path(temporary) / ".vite" / "build"
            build.mkdir(parents=True)
            runtime_anchor = (
                "function fF(e){return(0,i.join)(process.env.LOCALAPPDATA??"
                "(0,i.join)((0,r.homedir)(),`AppData`,`Local`),...e)}"
            )
            (build / "src-test.js").write_text(runtime_anchor, encoding="utf-8")
            logger_anchor = (
                "(0,i.join)(n.LOCALAPPDATA??(0,i.join)(a,`AppData`,`Local`),`Codex`,`Logs`)"
            )
            (build / "file-based-logger-test.js").write_text(logger_anchor, encoding="utf-8")
            worker_anchor = (
                "(0,E.join)(n.LOCALAPPDATA??(0,E.join)(r,`AppData`,`Local`),`Codex`,`Logs`)"
            )
            (build / "worker.js").write_text(worker_anchor, encoding="utf-8")

            patcher.patch_windows_runtime_paths(Path(temporary))

            self.assertIn("CODEX_MUX_HOME", (build / "src-test.js").read_text(encoding="utf-8"))
            self.assertIn("runtime-cache", (build / "src-test.js").read_text(encoding="utf-8"))
            self.assertNotIn("`Codex`,`Logs`", (build / "file-based-logger-test.js").read_text(encoding="utf-8"))
            self.assertNotIn("`Codex`,`Logs`", (build / "worker.js").read_text(encoding="utf-8"))

    def test_native_messaging_never_mutates_official_windows_artifacts(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            build = Path(temporary) / ".vite" / "build"
            build.mkdir(parents=True)
            delete = (
                "function oY(e){if(process.platform!==`win32`)return;let t=`${jq}\\\\${e}`;"
                "try{await Oq(`reg`,[`query`,t])}catch{return}await Oq(`reg`,[`delete`,t,`/f`])}"
            )
            add = (
                "function sY(e){let t=e.manifestPath;process.platform!==`win32`||t==null||"
                "await Oq(`reg`,[`add`,`${jq}\\\\${e.nativeHostName}`,`/ve`,`/t`,`REG_SZ`,"
                "`/d`,t,`/f`])}"
            )
            read = (
                "case`win32`:return Fy(`windows`).map(t=>(0,i.join)(r.default.homedir(),"
                "t,`${e}.json`));"
            )
            state = (
                "function yJ(e){let t=bJ();return[...t==null?[]:[t],(0,i.join)(e.codexHome,Mq)]"
                ".filter((e,t,n)=>n.indexOf(e)===t)}"
            )
            global_state = (
                "case`win32`:return(0,i.join)(process.env.LOCALAPPDATA??"
                "(0,i.join)(r.default.homedir(),`AppData`,`Local`),`OpenAI`,`Codex`,Mq);"
            )
            source = build / "src-test.js"
            source.write_text(
                "async " + delete + "async " + add + read + state + global_state,
                encoding="utf-8",
            )

            patcher.patch_windows_native_messaging_isolation(Path(temporary))

            result = source.read_text(encoding="utf-8")
            for anchor in (delete, add, read, state, global_state):
                self.assertNotIn(anchor, result)
            self.assertIn("async function oY(e){return}", result)
            self.assertIn("async function sY(e){return}", result)
            self.assertIn("case`win32`:return[];", result)
            self.assertIn("CODEX_MUX_HOME", result)

    def test_owl_config_gets_independent_name_without_version_change(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            app = Path(temporary)
            config = app / "resources" / "owl-app.ini"
            config.parent.mkdir(parents=True)
            config.write_text(
                "[Owl]\nUserDataDirectoryName=Codex\nAppVersion=26.820.71523\n",
                encoding="utf-8",
            )
            patcher.patch_owl_config(app)
            result = config.read_text(encoding="utf-8")
            self.assertIn("UserDataDirectoryName=Codex Subscription Router", result)
            self.assertIn("AppVersion=26.820.71523", result)

    def test_integration_guard_accepts_disabled_windows_protocol(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            build = Path(temporary) / ".vite" / "build"
            build.mkdir(parents=True)
            (build / "window-all-closed-test.js").write_text(
                "function C(){if(process.platform===`win32`)return;"
                "app.setAsDefaultProtocolClient(`codex`)}",
                encoding="utf-8",
            )
            patcher.verify_windows_integration_isolation(Path(temporary))

    def test_integration_guard_rejects_explorer_registration_anchor(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            build = root / ".vite" / "build"
            build.mkdir(parents=True)
            (build / "window-all-closed-test.js").write_text(
                "if(process.platform===`win32`)return;setAsDefaultProtocolClient()",
                encoding="utf-8",
            )
            (root / "bad.bin").write_bytes(b"OpenProjectInCodex")
            with self.assertRaisesRegex(RuntimeError, "Explorer integration"):
                patcher.verify_windows_integration_isolation(root)


class StagingTests(unittest.TestCase):
    def test_preservation_manifest_includes_every_sensitive_tree(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            staged = root / "staged"
            helper_names = (
                "codex.exe",
                "codex-code-mode-host.exe",
                "codex-command-runner.exe",
                "codex-windows-sandbox-setup.exe",
            )
            for name in helper_names:
                (source / "resources").mkdir(parents=True, exist_ok=True)
                (source / "resources" / name).write_bytes(("source-" + name).encode())
                staged_name = "codex.real.exe" if name == "codex.exe" else name
                (staged / "resources").mkdir(parents=True, exist_ok=True)
                (staged / "resources" / staged_name).write_bytes(("source-" + name).encode())
            for tree_name in ("cua_node", "native", "app.asar.unpacked"):
                relative = Path("resources") / tree_name / "payload.bin"
                (source / relative).parent.mkdir(parents=True, exist_ok=True)
                (staged / relative).parent.mkdir(parents=True, exist_ok=True)
                (source / relative).write_bytes(tree_name.encode())
                (staged / relative).write_bytes(tree_name.encode())

            cua_digest = patcher.tree_digest(
                patcher.tree_file_hashes(source / "resources" / "cua_node")
            )
            with mock.patch.object(
                patcher,
                "inspect_computer_use_contract",
                return_value={"treeSha256": cua_digest},
            ):
                preservation = patcher.verify_preserved_windows_resources(source, staged)

            self.assertEqual(
                set(preservation["preservedResourceTrees"]),
                {"cua_node", "native", "app.asar.unpacked"},
            )
            self.assertEqual(preservation["computerUse"]["treeSha256"], cua_digest)

    def test_control_port_is_high_and_override_is_validated(self) -> None:
        with mock.patch.object(patcher.secrets, "randbelow", return_value=17):
            self.assertEqual(patcher.resolve_control_port(None), 49169)
        self.assertEqual(patcher.resolve_control_port(65535), 65535)
        for invalid in (0, 48123, 65536):
            with self.subTest(port=invalid):
                with self.assertRaisesRegex(ValueError, "between 49152 and 65535"):
                    patcher.resolve_control_port(invalid)

    def test_swaps_desktop_and_codex_executables(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            staged = root / "staged"
            write_pe(staged / "ChatGPT.exe", b"desktop")
            write_pe(staged / "resources" / "codex.exe", b"codex")
            launcher = root / "launcher.exe"
            mux = root / "mux.exe"
            write_pe(launcher, b"launcher")
            write_pe(mux, b"mux")

            patcher.swap_executables(staged, mux, launcher)

            self.assertEqual((staged / "ChatGPT.exe").read_bytes(), launcher.read_bytes())
            self.assertEqual((staged / "ChatGPT.real.exe").read_bytes(), b"MZdesktop")
            self.assertEqual((staged / "resources" / "codex.exe").read_bytes(), mux.read_bytes())
            self.assertEqual(
                (staged / "resources" / "codex.real.exe").read_bytes(), b"MZcodex"
            )

    def test_atomic_install_requires_force_for_existing_destination(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            staged = root / "staged"
            destination = root / "destination"
            staged.mkdir()
            destination.mkdir()
            with self.assertRaisesRegex(RuntimeError, "pass --force"):
                patcher.atomic_install(staged, destination, force=False)

    def test_atomic_install_creates_recoverable_backup(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            staged = root / "staged"
            destination = root / "Router"
            staged.mkdir()
            destination.mkdir()
            (staged / "new").write_text("new", encoding="utf-8")
            (destination / "old").write_text("old", encoding="utf-8")

            backup = patcher.atomic_install(staged, destination, force=True)

            self.assertIsNotNone(backup)
            assert backup is not None
            self.assertTrue((destination / "new").is_file())
            self.assertTrue((backup / "old").is_file())
            self.assertEqual(backup.parent.parent.name, ".codex-subscription-router-backups")

    def test_planned_backup_matches_published_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            staged = root / "staged"
            destination = root / "Router"
            staged.mkdir()
            destination.mkdir()
            (destination / "old").write_text("old", encoding="utf-8")
            planned = patcher.plan_backup_path(destination)
            (staged / patcher.BUILD_MANIFEST_NAME).write_text(
                json.dumps({"backupPath": str(planned)}), encoding="utf-8"
            )

            actual = patcher.atomic_install(
                staged, destination, force=True, planned_backup=planned
            )

            self.assertEqual(actual, planned)
            self.assertTrue((planned / "old").is_file())
            published = json.loads(
                (destination / patcher.BUILD_MANIFEST_NAME).read_text(encoding="utf-8")
            )
            self.assertEqual(published["backupPath"], str(planned))

    def test_atomic_install_rolls_back_failed_commit(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            staged = root / "staged"
            destination = root / "Router"
            staged.mkdir()
            destination.mkdir()
            (destination / "old").write_text("old", encoding="utf-8")
            original_rename = Path.rename

            def failing_rename(path: Path, target: Path) -> Path:
                if path == staged:
                    raise OSError("simulated commit failure")
                return original_rename(path, target)

            with mock.patch.object(Path, "rename", failing_rename):
                with self.assertRaisesRegex(OSError, "simulated"):
                    patcher.atomic_install(staged, destination, force=True)
            self.assertTrue((destination / "old").is_file())

    def test_build_manifest_writes_secret_free_launcher_sidecar(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            staged = root / "app"
            (staged / "resources").mkdir(parents=True)
            (staged / "resources" / "app.asar").write_bytes(b"patched")
            mux = root / "mux.exe"
            launcher = root / "launcher.exe"
            write_pe(mux)
            write_pe(launcher)
            expected = patcher.TESTED_SOURCE_BUILDS["26.820.9563.0"]
            source = patcher.SourceInfo(
                package_root=None,
                app_root=root / "source",
                package_name="OpenAI.Codex",
                package_version="26.820.9563.0",
                package_full_name="test-package",
                asar_version=expected["asar_version"],
                asar_build=expected["asar_build"],
                asar_sha256=expected["asar_sha256"],
                codex_sha256=expected["codex_sha256"],
            )
            state = root / "state"

            backup = root / ".codex-subscription-router-backups" / "stamp" / "destination"
            patcher.write_build_manifest(
                staged,
                source,
                root / "destination",
                state,
                mux,
                launcher,
                {},
                backup,
                61234,
            )

            config_path = staged / "resources" / "codex-router" / "launcher-config.json"
            config = json.loads(config_path.read_text(encoding="utf-8"))
            self.assertEqual(
                config,
                {"schemaVersion": 2, "stateRoot": str(state), "controlPort": 61234},
            )
            self.assertNotIn("token", config_path.read_text(encoding="utf-8").lower())
            metadata = json.loads((staged / patcher.BUILD_MANIFEST_NAME).read_text(encoding="utf-8"))
            self.assertEqual(metadata["launcherSha256"], patcher.sha256_file(launcher))
            self.assertEqual(metadata["profilePath"], str(state / "Profile"))
            self.assertEqual(metadata["backupPath"], str(backup))
            self.assertEqual(metadata["controlPort"], 61234)
            self.assertFalse(
                metadata["capabilityQualification"]["appshots"]["defaultEnabled"]
            )
            self.assertEqual(
                metadata["capabilityQualification"]["computerUse"]["qualification"],
                "not-evaluated",
            )


if __name__ == "__main__":
    unittest.main()
