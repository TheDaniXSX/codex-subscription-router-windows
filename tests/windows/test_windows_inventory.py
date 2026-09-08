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
            "case`win32`:return[]; function yJ(e){if(process.platform===`win32`)return process.env.CODEX_MUX_HOME?"
            "[(0,i.join)(process.env.CODEX_MUX_HOME,Mq)]:[]; "
            "case`win32`:return(0,i.join)(process.env.CODEX_MUX_HOME??(0,i.join)(process.env.LOCALAPPDATA??"
            "(0,i.join)(r.default.homedir(),`AppData`,`Local`),`Codex Subscription Router`),Mq); "
            "if(process.platform===`win32`)return;"
        )
        bundle.write_text(markers, encoding="utf-8")
        locale = root / "native-menu-locales" / "en.json"
        locale.parent.mkdir(parents=True, exist_ok=True)
        locale.write_text("{}", encoding="utf-8")

    def test_asar_verifier_requires_complete_current_isolation_profile(self) -> None:
        node = shutil.which("node")
        asar_cli = REPOSITORY_ROOT / "node_modules" / "@electron" / "asar" / "bin" / "asar.mjs"
        if node is None or not asar_cli.is_file():
            self.skipTest("node and the pinned @electron/asar dependency are required")
        source = Path(self.temporary.name) / "current-asar"
        self._write_build_asar_source(source)
        bundle = source / ".vite" / "build" / "src-fixture.js"
        current = bundle.read_text(encoding="utf-8")
        for old, new in (("oY", "LZ"), ("sY", "RZ"), ("yJ", "XX"), ("Mq", "lX")):
            current = current.replace(old, new)
        archive = Path(self.temporary.name) / "current.asar"
        # Load only the verifier's inspection functions: no installed app,
        # signatures, state, or inventory participates in this focused test.
        command = r"""
$ast = [System.Management.Automation.Language.Parser]::ParseFile($args[0], [ref]$null, [ref]$null)
$names = @('Add-Check', 'Find-TextMarkersInFile', 'Invoke-CapturedProcess', 'Test-AsarArchive')
foreach ($definition in $ast.FindAll({param($node) $node -is [System.Management.Automation.Language.FunctionDefinitionAst]}, $false)) {
    if ($names -contains $definition.Name) { Invoke-Expression $definition.Extent.Text }
}
$script:Checks = New-Object 'System.Collections.Generic.List[object]'
Test-AsarArchive -AsarPath $args[1] -Root $args[2] -ControlPort 60001 -LegacyControlPort $false
$script:Checks.ToArray() | ConvertTo-Json -Compress
"""
        probe = Path(self.temporary.name) / "probe.ps1"
        probe.write_text(command, encoding="utf-8")
        variants = (
            ("current", current, True),
            ("mixed aliases", current.replace("function RZ(e){return}", "function sY(e){return}"), False),
            ("missing state isolation", current.replace("CODEX_MUX_HOME,lX", "LOCALAPPDATA,lX"), False),
            ("active registry mutation", current + " function LZ(e){if(process.platform!==`win32`)return;", False),
            ("active manifest lookup", current + " case`win32`:return Pb(`windows`).map", False),
        )
        for label, text, expected in variants:
            with self.subTest(label=label):
                bundle.write_text(text, encoding="utf-8")
                subprocess.run([node, str(asar_cli), "pack", str(source), str(archive)], check=True, capture_output=True)
                result = subprocess.run(
                    [self.shell, "-NoProfile", "-NonInteractive", "-File", str(probe), str(VERIFY_SCRIPT), str(archive), str(REPOSITORY_ROOT)],
                    capture_output=True, text=True, encoding="utf-8-sig", check=False,
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                checks = json.loads(result.stdout)
                self.assertEqual(all(check["Passed"] for check in checks), expected, checks)

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

    def test_default_source_discovery_validates_direct_parent_package_identity(self) -> None:
        build, _ = self._make_historical_build()
        manifest_path = build / "app" / "codex-mux-build.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        manifest["sourcePath"] = str(self.package / "app")
        manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        arguments = [
            self.shell, "-NoProfile", "-NonInteractive", "-File", str(VERIFY_SCRIPT),
            "-BuildPath", str(build), "-StateRoot", str(Path(self.temporary.name) / "state"),
            "-SkipSmokeTest", "-SkipSignatureValidation", "-SkipAclValidation",
        ]

        def verify() -> subprocess.CompletedProcess[str]:
            return subprocess.run(arguments, cwd=REPOSITORY_ROOT, text=True, encoding="utf-8-sig", capture_output=True, check=False)

        result = verify()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Live source package identity, architecture, and publisher are exact", result.stdout)
        package_manifest = self.package / "AppxManifest.xml"
        original_manifest = package_manifest.read_text(encoding="utf-8")
        package_manifest.write_text(original_manifest.replace(PUBLISHER, "CN=Not OpenAI"), encoding="utf-8")
        result = verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("[FAIL] Live source package identity", result.stdout)
        package_manifest.unlink()
        result = verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("A loose app directory has no Appx identity", result.stdout)

    def test_historical_verifier_authenticates_asar_bound_runtime_and_original(self) -> None:
        from test_windows_asar_integrity import integrity, synthetic_pe

        self._write("app/ChatGPT.exe", synthetic_pe("0" * 64))
        self._write("app/chrome.dll", bytes(64) + integrity.FUSE_SENTINEL + b"\x01\x09010011001")
        build, inventory = self._make_historical_build()
        app = build / "app"
        original = app / "ChatGPT.original.exe"
        runtime = app / "ChatGPT.real.exe"
        runtime.replace(original)
        archive = app / "resources" / "app.asar"
        source = original.read_bytes()
        slot, _ = integrity.integrity_hash_slot(source)
        runtime.write_bytes(source[:slot] + integrity.asar_header_sha256(archive).encode("ascii") + source[slot + 64:])
        manifest_path = app / "codex-mux-build.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        manifest["preservation"]["desktopIntegrity"] = integrity.verify_desktop_integrity(original, runtime, archive)
        manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        arguments = [
            self.shell, "-NoProfile", "-NonInteractive", "-File", str(VERIFY_SCRIPT),
            "-BuildPath", str(build), "-StateRoot", str(Path(self.temporary.name) / "state"),
            "-OfflineHistorical", "-SourceInventoryPath", str(inventory), "-SkipSmokeTest",
            "-SkipSignatureValidation", "-SkipAclValidation",
        ]
        result = subprocess.run(arguments, cwd=REPOSITORY_ROOT, text=True, encoding="utf-8-sig", capture_output=True, check=False)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("exact resource-only modification are verified", result.stdout)
        previous = runtime.read_bytes()
        runtime.write_bytes(previous[:300] + bytes([previous[300] ^ 1]) + previous[301:])
        result = subprocess.run(arguments, cwd=REPOSITORY_ROOT, text=True, encoding="utf-8-sig", capture_output=True, check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("outside the authorized ASAR integrity hash", result.stdout + result.stderr)

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
