from __future__ import annotations

import hashlib
import json
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
INVENTORY_SCRIPT = REPOSITORY_ROOT / "scripts" / "inventory_windows_source.ps1"
VERIFY_SCRIPT = REPOSITORY_ROOT / "scripts" / "verify_windows_build.ps1"
PACKAGE_LEAF = "OpenAI.Codex_1.2.3.4_x64__2p2nqsd0c76g0"
PUBLISHER = "CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B"


class WindowsSourceInventoryTests(unittest.TestCase):
    def setUp(self) -> None:
        shell = shutil.which("pwsh") or shutil.which("powershell.exe")
        if shell is None:
            self.skipTest("PowerShell is required")
        self.shell = shell
        self.temporary = tempfile.TemporaryDirectory()
        self.package = Path(self.temporary.name) / PACKAGE_LEAF
        self._write_fixture(PUBLISHER)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _write(self, relative: str, content: bytes) -> None:
        path = self.package / Path(relative)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)

    def _write_fixture(self, publisher: str) -> None:
        manifest = f"""<?xml version="1.0" encoding="utf-8"?>
<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10">
  <Identity Name="OpenAI.Codex" Publisher="{publisher}" Version="1.2.3.4" ProcessorArchitecture="x64" />
  <Properties>
    <DisplayName>Codex</DisplayName>
    <PublisherDisplayName>OpenAI</PublisherDisplayName>
    <Description>fixture</Description>
  </Properties>
  <Applications />
</Package>
"""
        self._write("AppxManifest.xml", manifest.encode("utf-8"))
        self._write("AppxSignature.p7x", b"fixture-signature")
        self._write("app/Codex.exe", b"MZdesktop-shim")
        self._write("app/ChatGPT.exe", b"MZdesktop")
        self._write("app/resources/app.asar", b"source-asar")
        self._write("app/resources/codex.exe", b"MZcodex")
        self._write("app/resources/codex-code-mode-host.exe", b"MZcode-mode")
        self._write("app/resources/codex-command-runner.exe", b"MZcommand-runner")
        self._write("app/resources/codex-windows-sandbox-setup.exe", b"MZsandbox")
        self._write("app/resources/native/windows-account.node", b"MZaccount")
        self._write("app/resources/cua_node/manifest.json", b'{"platform":"windows"}')
        self._write("app/resources/app.asar.unpacked/native/addon.node", b"MZaddon")

    def _inventory(self) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                self.shell,
                "-NoProfile",
                "-NonInteractive",
                "-File",
                str(INVENTORY_SCRIPT),
                "-PackageRoot",
                str(self.package),
                "-OutputFormat",
                "Json",
                "-SkipSignatures",
            ],
            cwd=REPOSITORY_ROOT,
            text=True,
            encoding="utf-8-sig",
            capture_output=True,
            check=False,
        )

    @staticmethod
    def _sha256(path: Path) -> str:
        return hashlib.sha256(path.read_bytes()).hexdigest()

    @classmethod
    def _tree_digest(cls, root: Path) -> str:
        digest = hashlib.sha256()
        files = sorted(
            (path for path in root.rglob("*") if path.is_file() and not path.is_symlink()),
            key=lambda path: path.relative_to(root).as_posix().lower(),
        )
        for path in files:
            relative = path.relative_to(root).as_posix()
            digest.update(relative.encode("utf-8"))
            digest.update(b"\0")
            digest.update(cls._sha256(path).encode("ascii"))
            digest.update(b"\n")
        return digest.hexdigest()

    def _make_historical_build(self) -> tuple[Path, Path]:
        inventory = self._inventory()
        self.assertEqual(inventory.returncode, 0, inventory.stderr)
        inventory_path = Path(self.temporary.name) / "source-inventory.json"
        inventory_path.write_text(inventory.stdout, encoding="utf-8")

        build = Path(self.temporary.name) / "build"
        app = build / "app"
        shutil.copytree(self.package / "app", app)
        (app / "ChatGPT.exe").replace(app / "ChatGPT.real.exe")
        (app / "ChatGPT.exe").write_bytes(b"MZrouter-launcher")
        resources = app / "resources"
        (resources / "codex.exe").replace(resources / "codex.real.exe")
        (resources / "codex.exe").write_bytes(b"MZrouter-mux")

        extracted = Path(self.temporary.name) / "asar-source"
        self._write_build_asar_source(extracted)
        asar_cli = REPOSITORY_ROOT / "node_modules" / "@electron" / "asar" / "bin" / "asar.mjs"
        node = shutil.which("node")
        if node is None or not asar_cli.is_file():
            self.skipTest("node and the pinned @electron/asar dependency are required")
        subprocess.run(
            [node, str(asar_cli), "pack", str(extracted), str(resources / "app.asar")],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
        )

        control_port = 60001
        launcher_config = resources / "codex-router" / "launcher-config.json"
        launcher_config.parent.mkdir(parents=True, exist_ok=True)
        launcher_config.write_text(
            json.dumps({"schemaVersion": 2, "stateRoot": str(Path(self.temporary.name) / "state"), "controlPort": control_port}),
            encoding="utf-8",
        )
        source_app = self.package / "app"
        source_hash = lambda relative: self._sha256(source_app / relative)
        manifest = {
            "schemaVersion": 2,
            "projectVersion": "test",
            "sourceVersion": "1.2.3.4",
            "sourcePackage": PACKAGE_LEAF,
            "sourcePath": "Z:/removed-windowsapps-version/app",
            "sourceAsarSha256": source_hash("resources/app.asar"),
            "sourceCodexSha256": source_hash("resources/codex.exe"),
            "sourceChatGptSha256": source_hash("ChatGPT.exe"),
            "sourceCodexLauncherSha256": source_hash("Codex.exe"),
            "sourceWindowsAccountSha256": source_hash("resources/native/windows-account.node"),
            "sourceSignerSubject": "fixture",
            "sourceSignerThumbprint": "0" * 40,
            "patchedAsarSha256": self._sha256(resources / "app.asar"),
            "muxSha256": self._sha256(resources / "codex.exe"),
            "launcherSha256": self._sha256(app / "ChatGPT.exe"),
            "controlPort": control_port,
            "launcherConfigPath": "resources/codex-router/launcher-config.json",
            "preservation": {
                "cliHelpers": {
                    name: self._sha256(resources / name)
                    for name in (
                        "codex.real.exe",
                        "codex-code-mode-host.exe",
                        "codex-command-runner.exe",
                        "codex-windows-sandbox-setup.exe",
                    )
                },
                "preservedResourceTrees": {
                    name: self._tree_digest(resources / name)
                    for name in ("cua_node", "native", "app.asar.unpacked")
                },
            },
            "windowsIntegrationIsolation": {
                "appxManifestCopied": False,
                "officialProtocolRegistrationDisabled": True,
                "officialExplorerVerbRegistrationCopied": False,
            },
        }
        (app / "codex-mux-build.json").write_text(json.dumps(manifest), encoding="utf-8")
        return build, inventory_path

    @staticmethod
    def _write_build_asar_source(root: Path) -> None:
        bundle = root / ".vite" / "build" / "src-fixture.js"
        bundle.parent.mkdir(parents=True, exist_ok=True)
        markers = (
            "CodexMuxAccountMenu CodexMuxThreadSubscription process.env.CODEX_MUX_HOME "
            "http://127.0.0.1:60001 function oY(e){return} function sY(e){return} "
            "case`win32`:return[]; function yJ(e){if(process.platform===`win32`)return process.env.CODEX_MUX_HOME? "
            "if(process.platform===`win32`)return;"
        )
        bundle.write_text(markers, encoding="utf-8")
        locale = root / "native-menu-locales" / "en.json"
        locale.parent.mkdir(parents=True, exist_ok=True)
        locale.write_text("{}", encoding="utf-8")

    def test_archived_package_inventory_is_complete_and_deterministic(self) -> None:
        first = self._inventory()
        self.assertEqual(first.returncode, 0, first.stderr)
        first_report = json.loads(first.stdout)
        second_report = json.loads(self._inventory().stdout)

        self.assertEqual(first_report["schemaVersion"], "2.0")
        self.assertTrue(first_report["identityValidation"]["passed"])
        self.assertEqual(
            first_report["preservedPayload"]["treeHash"],
            second_report["preservedPayload"]["treeHash"],
        )
        paths = [record["relativePath"] for record in first_report["preservedPayload"]["files"]]
        self.assertIn("resources/native/windows-account.node", paths)
        summaries = {entry["name"]: entry for entry in first_report["nativePayloadSummary"]}
        self.assertEqual(set(summaries), {"cua_node", "native", "app.asar.unpacked"})
        expected = hashlib.sha256(b"MZaccount").hexdigest()
        declared = {
            entry["relativePath"]: entry["hash"] for entry in first_report["hashes"]
        }
        self.assertEqual(declared["app\\resources\\native\\windows-account.node"], expected)

    def test_exact_publisher_mismatch_fails_closed(self) -> None:
        self._write_fixture("CN=Not OpenAI")
        result = self._inventory()
        self.assertEqual(result.returncode, 7, result.stderr)
        report = json.loads(result.stdout)
        self.assertEqual(report["status"], "IdentityMismatch")
        self.assertIn("expectedPublisherMatches", report["identityValidation"]["failures"])

    def test_tree_hash_changes_when_sensitive_payload_changes(self) -> None:
        before = json.loads(self._inventory().stdout)["preservedPayload"]["treeHash"]
        self._write("app/resources/native/windows-account.node", b"MZtampered")
        after = json.loads(self._inventory().stdout)["preservedPayload"]["treeHash"]
        self.assertNotEqual(before, after)

    def test_historical_verifier_needs_no_live_windowsapps_package(self) -> None:
        build, inventory = self._make_historical_build()
        result = subprocess.run(
            [
                self.shell,
                "-NoProfile",
                "-NonInteractive",
                "-File",
                str(VERIFY_SCRIPT),
                "-BuildPath",
                str(build),
                "-StateRoot",
                str(Path(self.temporary.name) / "state"),
                "-OfflineHistorical",
                "-SourceInventoryPath",
                str(inventory),
                "-SkipSmokeTest",
                "-SkipSignatureValidation",
                "-SkipAclValidation",
            ],
            cwd=REPOSITORY_ROOT,
            text=True,
            encoding="utf-8-sig",
            capture_output=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Historical verification is independent of WindowsApps retention", result.stdout)

        build_manifest_path = build / "app" / "codex-mux-build.json"
        build_manifest = json.loads(build_manifest_path.read_text(encoding="utf-8"))
        build_manifest["undeclaredArtifactSha256"] = "0" * 64
        build_manifest_path.write_text(json.dumps(build_manifest), encoding="utf-8")
        unknown_hash = subprocess.run(result.args, cwd=REPOSITORY_ROOT, text=True, encoding="utf-8-sig", capture_output=True, check=False)
        self.assertNotEqual(unknown_hash.returncode, 0)
        self.assertIn("unvalidated declaration undeclaredArtifactSha256", unknown_hash.stdout + unknown_hash.stderr)
        del build_manifest["undeclaredArtifactSha256"]
        build_manifest_path.write_text(json.dumps(build_manifest), encoding="utf-8")

        sidecar_path = build / "app" / "resources" / "codex-router" / "launcher-config.json"
        sidecar = json.loads(sidecar_path.read_text(encoding="utf-8"))
        sidecar["controlPort"] = 60002
        sidecar_path.write_text(json.dumps(sidecar), encoding="utf-8")
        wrong_port = subprocess.run(result.args, cwd=REPOSITORY_ROOT, text=True, encoding="utf-8-sig", capture_output=True, check=False)
        self.assertNotEqual(wrong_port.returncode, 0)
        self.assertIn("Launcher sidecar and build manifest agree on the control port", wrong_port.stdout + wrong_port.stderr)
        sidecar["controlPort"] = 60001
        sidecar_path.write_text(json.dumps(sidecar), encoding="utf-8")

        (build / "app" / "resources" / "native" / "windows-account.node").write_bytes(b"MZtampered")
        tampered = subprocess.run(result.args, cwd=REPOSITORY_ROOT, text=True, encoding="utf-8-sig", capture_output=True, check=False)
        self.assertNotEqual(tampered.returncode, 0)
        self.assertIn("sourceWindowsAccountSha256", tampered.stdout + tampered.stderr)


if __name__ == "__main__":
    unittest.main()
