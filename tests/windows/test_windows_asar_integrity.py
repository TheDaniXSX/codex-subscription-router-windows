from __future__ import annotations

import hashlib
from contextlib import ExitStack
import importlib.util
import json
import os
from pathlib import Path
import shutil
import struct
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock


SPEC = importlib.util.spec_from_file_location(
    "windows_asar_integrity", Path(__file__).resolve().parents[2] / "scripts/windows_asar_integrity.py"
)
assert SPEC and SPEC.loader
integrity = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(integrity)


def synthetic_asar(value: str) -> bytes:
    header = json.dumps({"files": {}, "fixture": value}).encode()
    padded = (len(header) + 3) & ~3
    return struct.pack("<IIII", 4, 8 + padded, 4 + padded, len(header)) + header + bytes(padded - len(header))


def synthetic_pe(digest: str) -> bytes:
    data = bytearray(1536)
    data[:2] = b"MZ"
    struct.pack_into("<I", data, 0x3C, 128)
    data[128:132] = b"PE\0\0"
    struct.pack_into("<H", data, 134, 1)
    struct.pack_into("<H", data, 148, 240)
    struct.pack_into("<H", data, 152, 0x20B)
    struct.pack_into("<I", data, 260, 16)
    struct.pack_into("<II", data, 280, 4096, 1024)
    struct.pack_into("<IIII", data, 400, 1024, 4096, 1024, 512)
    for relative, name, target in ((0, 0x80000080, 0x80000018), (24, 0x800000A0, 0x80000030), (48, 1033, 72)):
        struct.pack_into("<HHII", data, 512 + relative + 12, 0, 1, name, target)
    for relative, name in ((128, "INTEGRITY"), (160, "ELECTRONASAR")):
        encoded = name.encode("utf-16le")
        struct.pack_into("<H", data, 512 + relative, len(name))
        data[514 + relative:514 + relative + len(encoded)] = encoded
    payload = json.dumps([{"file": "resources\\app.asar", "alg": "SHA256", "value": digest}], separators=(",", ":")).encode()
    struct.pack_into("<IIII", data, 584, 4352, len(payload), 0, 0)
    data[768:768 + len(payload)] = payload
    return bytes(data)


class IntegrityTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.original = self.root / "ChatGPT.original.exe"
        self.runtime = self.root / "ChatGPT.real.exe"
        self.source = self.root / "source.asar"
        self.archive = self.root / "app.asar"
        self.source.write_bytes(synthetic_asar("official"))
        self.archive.write_bytes(synthetic_asar("router"))
        self.original.write_bytes(synthetic_pe(integrity.asar_header_sha256(self.source)))
        self.runtime.write_bytes(self.original.read_bytes())
        (self.root / "chrome.dll").write_bytes(bytes(64) + integrity.FUSE_SENTINEL + b"\x01\x09010011001")

    def patch(self):
        return integrity.patch_desktop_integrity(self.original, self.runtime, self.archive, self.source)

    def test_only_resource_digest_changes_and_original_remains_intact(self):
        before = self.original.read_bytes()
        metadata = self.patch()
        self.assertEqual(self.original.read_bytes(), before)
        slot, _ = integrity.integrity_hash_slot(before)
        self.assertEqual(self.runtime.read_bytes()[:slot], before[:slot])
        self.assertEqual(self.runtime.read_bytes()[slot + 64:], before[slot + 64:])
        self.assertEqual(metadata["originalDesktopSha256"], hashlib.sha256(before).hexdigest())
        self.assertTrue(metadata["integrityEnabled"])
        self.assertNotEqual(metadata["runtimeDesktopSha256"], metadata["originalDesktopSha256"])

    def test_rejects_arbitrary_executable_mutation(self):
        self.patch()
        data = bytearray(self.runtime.read_bytes())
        data[300] ^= 1
        self.runtime.write_bytes(data)
        with self.assertRaisesRegex(ValueError, "outside"):
            integrity.verify_desktop_integrity(self.original, self.runtime, self.archive)

    def test_rejects_disabled_integrity_fuse(self):
        self.patch()
        (self.root / "chrome.dll").write_bytes(bytes(64) + integrity.FUSE_SENTINEL + b"\x01\x09010001001")
        with self.assertRaisesRegex(ValueError, "remain enabled"):
            integrity.verify_desktop_integrity(self.original, self.runtime, self.archive)

    def test_rejects_wrong_official_asar_before_writing(self):
        before = self.runtime.read_bytes()
        with self.assertRaisesRegex(ValueError, "official ASAR"):
            integrity.patch_desktop_integrity(self.original, self.runtime, self.archive, self.archive)
        self.assertEqual(self.runtime.read_bytes(), before)

    def test_rejects_same_original_and_runtime(self):
        with self.assertRaisesRegex(ValueError, "preserved separately"):
            integrity.patch_desktop_integrity(self.original, self.original, self.archive, self.source)

    def test_rejects_hardlink_alias_without_modifying_original(self):
        alias = self.root / "hardlink.exe"
        try:
            os.link(self.original, alias)
        except OSError as error:
            self.skipTest(f"hardlinks unavailable: {error}")
        before = self.original.read_bytes()
        with self.assertRaisesRegex(ValueError, "preserved separately"):
            integrity.patch_desktop_integrity(self.original, alias, self.archive, self.source)
        self.assertEqual(self.original.read_bytes(), before)

    def test_installer_pipeline_rebinds_split_build_and_records_provenance(self):
        spec = importlib.util.spec_from_file_location("integrity_pipeline_patcher", Path(__file__).resolve().parents[2] / "scripts/patch_windows_app.py")
        assert spec and spec.loader
        patcher = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = patcher
        self.addCleanup(sys.modules.pop, spec.name, None)
        spec.loader.exec_module(patcher)
        source_app = self.root / "official-app"
        resources = source_app / "resources"
        (resources / "app.asar.unpacked").mkdir(parents=True)
        shutil.copyfile(self.original, source_app / "ChatGPT.exe")
        shutil.copyfile(self.root / "chrome.dll", source_app / "chrome.dll")
        shutil.copyfile(self.source, resources / "app.asar")
        (resources / "codex.exe").write_bytes(b"MZofficial-cli")
        executable = self.root / "compiled.exe"
        executable.write_bytes(b"MZrouter")
        source_info = SimpleNamespace(app_root=source_app, package_full_name="fixture", asar_version="26.901.51231", asar_build="8109", asar_sha256="fixture")

        def extract(command):
            assets = Path(command[-1]) / "webview/assets"
            assets.mkdir(parents=True)
            (assets / "app-primary-fixture.js").write_text("", encoding="utf-8")

        def install_archive(staged, archive, unpacked):
            shutil.copyfile(self.archive, staged / "resources/app.asar")

        def record_manifest(staged, source, destination, state, mux, launcher, preservation, backup, port):
            self.assertEqual((staged / "ChatGPT.original.exe").read_bytes(), self.original.read_bytes())
            checked = integrity.verify_desktop_integrity(staged / "ChatGPT.original.exe", staged / "ChatGPT.real.exe", staged / "resources/app.asar")
            self.assertEqual(preservation["desktopIntegrity"], checked)
            return {"preservation": preservation}

        with ExitStack() as stack:
            for name, value in {
                "inspect_source": source_info, "validate_approved_source": None,
                "ensure_asar_tool": Path("fixture-asar"), "resolve_state_root": self.root / "state",
                "prepare_control_token": ("fixture", False), "prepare_executable": executable,
                "patch_owl_config": None, "patch_extracted_asar": None,
                "repack_asar": Path("fixture-unpacked"), "verify_preserved_windows_resources": {},
            }.items():
                stack.enter_context(mock.patch.object(patcher, name, return_value=value))
            stack.enter_context(mock.patch.object(patcher, "run", side_effect=extract))
            stack.enter_context(mock.patch.object(patcher, "install_repacked_asar", side_effect=install_archive))
            stack.enter_context(mock.patch.object(patcher, "write_build_manifest", side_effect=record_manifest))
            result = patcher.patch_app(source_app, self.root / "installed", executable, executable, force=False, dry_run=True, allow_untested_source=False, control_port=55001)
        self.assertTrue(result["dryRun"])
        self.assertTrue(result["preservation"]["desktopIntegrity"]["integrityEnabled"])
        self.assertFalse((self.root / "installed").exists())

    def test_rejects_wrong_resource_name_and_truncated_directory(self):
        data = self.original.read_bytes().replace("INTEGRITY".encode("utf-16le"), "INTEGRITX".encode("utf-16le"))
        with self.assertRaisesRegex(ValueError, "INTEGRITY"):
            integrity.integrity_hash_slot(data)
        with self.assertRaises(ValueError):
            integrity.integrity_hash_slot(self.original.read_bytes()[:600])

    def test_rejects_changed_asar_after_patch(self):
        self.patch()
        self.archive.write_bytes(synthetic_asar("tampered"))
        with self.assertRaisesRegex(ValueError, "wrong hash"):
            integrity.verify_desktop_integrity(self.original, self.runtime, self.archive)

    def test_rejects_header_allocation_bomb(self):
        self.archive.write_bytes(struct.pack("<IIII", 4, 0xFFFFFFFC, 0xFFFFFFF8, 0xFFFFFFF0))
        with self.assertRaisesRegex(ValueError, "framing"):
            integrity.asar_header_sha256(self.archive)


if __name__ == "__main__":
    unittest.main()
