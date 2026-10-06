from __future__ import annotations

from contextlib import ExitStack
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("shared_preparation_patcher", ROOT / "scripts/patch_windows_app.py")
assert SPEC and SPEC.loader
patcher = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = patcher
SPEC.loader.exec_module(patcher)


class SharedPreparationTests(unittest.TestCase):
    def test_shared_binding_is_explicit_complete_and_disjoint(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            values = [root / "prod-data", root / "native", root / "dev-data", 1, "test-pair-001"]
            binding = patcher.validate_shared_binding(*values)
            self.assertEqual(binding["sharedProtocol"], 1)
            self.assertIsNone(patcher.validate_shared_binding(None, None, None, None, None))
            for index in range(5):
                bad = values.copy()
                bad[index] = None
                with self.assertRaises(ValueError):
                    patcher.validate_shared_binding(*bad)
            for change in ((3, 2), (4, "bad pair"), (0, Path("relative"))):
                bad = values.copy()
                bad[change[0]] = change[1]
                with self.assertRaises(ValueError):
                    patcher.validate_shared_binding(*bad)
            with self.assertRaisesRegex(RuntimeError, "overlap"):
                patcher.validate_shared_binding(root / "prod", root / "prod/home", root / "dev", 1, "test-pair-001")

    def test_prepared_destination_rejects_live_trees_and_existing_payloads(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            source, installed, state = root / "source", root / "installed", root / "state"
            candidate = root / "candidate"
            self.assertEqual(patcher.validate_prepared_destination(True, candidate, False, source, installed, state, None), candidate)
            for target in (source, installed, state, root, installed / "nested"):
                with self.assertRaises(RuntimeError):
                    patcher.validate_prepared_destination(True, target, False, source, installed, state, None)
            candidate.mkdir()
            with self.assertRaisesRegex(RuntimeError, "already exist"):
                patcher.validate_prepared_destination(True, candidate, False, source, installed, state, None)
            with self.assertRaises(ValueError):
                patcher.validate_prepared_destination(True, root / "new", True, source, installed, state, None)

    def test_prepare_retains_candidate_without_publishing_or_writing_live_token(self):
        for existing_token in (False, True):
            with self.subTest(existing_token=existing_token), tempfile.TemporaryDirectory(prefix="csr-prp-") as temporary:
                # TEMP may contain an 8.3 alias on the Windows CI runner;
                # compare against the canonical paths written by preparation.
                root = Path(temporary).resolve()
                source, destination, state, prepared = root / "source", root / "installed", root / "state", root / "prepared"
                resources = source / "resources"
                (resources / "app.asar.unpacked").mkdir(parents=True)
                (resources / "app.asar.unpacked/native.node").write_bytes(b"native")
                (resources / "app.asar").write_bytes(b"official-asar")
                (resources / "codex.exe").write_bytes(b"MZofficial-cli")
                (source / "ChatGPT.exe").write_bytes(b"MZofficial-desktop")
                executable = root / "compiled.exe"
                executable.write_bytes(b"MZrouter")
                destination.mkdir()
                (destination / "codex-mux-build.json").write_text("{}", encoding="utf-8")
                (destination / "keep.txt").write_text("running-installed-app", encoding="utf-8")
                if existing_token:
                    state.mkdir()
                    (state / "control-token").write_text("a" * 64 + "\n", encoding="utf-8")
                token_before = (state / "control-token").read_bytes() if existing_token else None
                original_manifest = (destination / "codex-mux-build.json").read_bytes()
                source_info = patcher.SourceInfo(package_root=None, app_root=source, package_name="OpenAI.Codex",
                    package_version="fixture", package_full_name="fixture", asar_version="fixture", asar_build="fixture",
                    asar_sha256=patcher.sha256_file(resources / "app.asar"), codex_sha256=patcher.sha256_file(resources / "codex.exe"))

                def repack(_asar, _extracted, output, _files):
                    output.write_bytes(b"patched-asar")
                    unpacked = output.with_name(output.name + ".unpacked")
                    unpacked.mkdir()
                    (unpacked / "native.node").write_bytes(b"native")
                    return unpacked

                with ExitStack() as stack:
                    for name, value in {
                        "inspect_source": source_info, "validate_approved_source": None,
                        "ensure_asar_tool": Path("fixture-asar"), "prepare_executable": executable,
                        "patch_owl_config": None, "patch_extracted_asar": None,
                        "verify_preserved_windows_resources": {}, "_is_split_renderer": False,
                        "require_tool": "node", "run": None,
                    }.items():
                        stack.enter_context(mock.patch.object(patcher, name, return_value=value))
                    stack.enter_context(mock.patch.object(patcher, "repack_asar", side_effect=repack))
                    publish = stack.enter_context(mock.patch.object(patcher, "atomic_install", side_effect=AssertionError("must not publish")))
                    persist = stack.enter_context(mock.patch.object(patcher, "persist_control_token", side_effect=AssertionError("must not write live token")))
                    result = patcher.patch_app(source, destination, executable, executable,
                        force=False, dry_run=False, allow_untested_source=False, control_port=55876,
                        state_root_path=state, prepare_only=True, prepared_destination=prepared)
                self.assertTrue(result["prepareOnly"])
                publish.assert_not_called()
                persist.assert_not_called()
                self.assertEqual((destination / "keep.txt").read_text(), "running-installed-app")
                self.assertEqual((destination / "codex-mux-build.json").read_bytes(), original_manifest)
                manifest = json.loads((prepared / "codex-mux-build.json").read_text())
                self.assertEqual(manifest["destination"], str(destination))
                self.assertEqual(manifest["profilePath"], str(state / "Profile"))
                self.assertIsNone(manifest["backupPath"])
                receipt = json.loads((prepared / "codex-mux-prepared.json").read_text())
                self.assertEqual(receipt["expectedInstalledManifestSha256"], hashlib.sha256(original_manifest).hexdigest())
                self.assertEqual(receipt["preparedManifestSha256"], patcher.sha256_file(prepared / "codex-mux-build.json"))
                self.assertEqual(receipt["controlTokenIsNew"], not existing_token)
                pending_token = prepared / "resources/codex-router/prepared-control-token"
                if existing_token:
                    self.assertEqual((state / "control-token").read_bytes(), token_before)
                    self.assertFalse(pending_token.exists())
                else:
                    self.assertFalse(state.exists())
                    self.assertEqual(receipt["controlTokenSha256"], hashlib.sha256(pending_token.read_text().strip().encode()).hexdigest())
                self.assertFalse((root / ".codex-subscription-router-backups").exists())

    def test_shared_manifests_keep_runtime_private_and_bind_both_channels(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            binding = patcher.validate_shared_binding(root / "prod-state", root / "native", root / "dev-data", 1, "test-pair-001")
            for channel in ("production", "development"):
                staged = root / channel
                (staged / "resources").mkdir(parents=True)
                (staged / "resources/app.asar").write_bytes(b"asar")
                executable = root / "compiled.exe"
                executable.write_bytes(b"MZcompiled")
                source_info = patcher.SourceInfo(package_root=None, app_root=root / "source", package_name="OpenAI.Codex",
                    package_version="fixture", package_full_name="fixture", asar_version="fixture", asar_build="fixture", asar_sha256="0" * 64, codex_sha256="0" * 64)
                runtime = root / (channel + "-runtime")
                manifest = patcher.write_build_manifest(staged, source_info, root / (channel + "-installed"), runtime,
                    executable, executable, {}, control_port=55876 if channel == "production" else 60834,
                    install_channel=channel, shared_binding=binding)
                sidecar = json.loads((staged / "resources/codex-router/launcher-config.json").read_text())
                self.assertEqual(sidecar["schemaVersion"], 3)
                self.assertEqual(sidecar["stateRoot"], str(runtime))
                self.assertEqual(manifest["profilePath"], str(runtime / "Profile"))
                self.assertEqual(sidecar["primaryCodexHome"], str(root / "native"))
                self.assertEqual(sidecar["primarySqliteHome"], str(root / "native"))
                self.assertEqual(manifest["calibrationEnabled"], channel == "development")
                for field, value in binding.items():
                    self.assertEqual(sidecar[field], value)
                    self.assertEqual(manifest[field], value)


if __name__ == "__main__":
    unittest.main()
