from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "patch_windows_app.py"
QUALIFIER_SCRIPT = ROOT / "scripts" / "qualify_windows_capabilities.py"
FIXTURE = Path(__file__).parent / "fixtures" / "capabilities" / "windows-contract.json"
SPEC = importlib.util.spec_from_file_location("capability_patcher", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
patcher = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = patcher
SPEC.loader.exec_module(patcher)
QUALIFIER_SPEC = importlib.util.spec_from_file_location(
    "capability_qualifier", QUALIFIER_SCRIPT
)
assert QUALIFIER_SPEC is not None and QUALIFIER_SPEC.loader is not None
qualifier = importlib.util.module_from_spec(QUALIFIER_SPEC)
sys.modules[QUALIFIER_SPEC.name] = qualifier
QUALIFIER_SPEC.loader.exec_module(qualifier)


def write_text(path: Path, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(value, encoding="utf-8")


def write_pe(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(b"MZsynthetic-contract-fixture")


def build_capability_fixture(root: Path) -> Path:
    definition = json.loads(FIXTURE.read_text(encoding="utf-8"))
    resources = root / "resources"
    cua = resources / "cua_node"
    package = cua / "bin" / "node_modules" / "@oai" / "cua"

    write_text(cua / "manifest.json", json.dumps(definition["cuaManifest"]))
    write_pe(cua / "bin" / "node.exe")
    write_pe(cua / "bin" / "node_repl.exe")
    (cua / "bin" / "node_modules").mkdir(parents=True, exist_ok=True)
    write_text(package / "package.json", json.dumps(definition["cuaPackage"]))
    write_text(
        package / "dist" / "lib" / "js" / "oai_js_cua" / "src" / "index.js",
        "export {};",
    )
    write_pe(package / "bin" / "windows" / "codex-computer-use.exe")
    internal = package / "dist" / "project" / "cua" / "sky_js" / "src" / "targets" / "windows" / "internal"
    write_text(internal / "helper_transport.js", definition["helperTransportSource"])
    write_text(internal / "computer_use_client.js", definition["nativePipeClientSource"])
    write_pe(resources / "codex-code-mode-host.exe")
    return resources


def build_appshots_fixture(root: Path) -> Path:
    main = root / ".vite" / "build" / "main-synthetic.js"
    bridge = "V=y&&a.a.isInternal(i)?Uje(g):null,ne=new PAe"
    feature = (
        "let s=br(e);I&&H.windowsCaptureNativeBridge==null&&(s.appshotsEnabled=!1),"
        "I&&!a.a.isInternal(c)&&(s.appshotsEnabled=!1),Re.setDesktopFeatureAvailability(s);"
    )
    write_text(main, bridge + ";" + feature)
    return main


class ComputerUseStaticContractTests(unittest.TestCase):
    def test_standalone_qualification_requires_exact_approved_digest(self) -> None:
        expected = patcher.TESTED_SOURCE_BUILDS["26.820.9563.0"]
        result = {
            "treeSha256": expected["cua_tree_sha256"],
            "nodeVersion": expected["cua_node_version"],
            "runtimeVersion": expected["cua_runtime_version"],
            "packageVersion": expected["cua_package_version"],
        }
        qualifier.validate_approved_computer_use("26.820.9563.0", result)
        result["treeSha256"] = "0" * 64
        with self.assertRaisesRegex(RuntimeError, "tree digest"):
            qualifier.validate_approved_computer_use("26.820.9563.0", result)

    def test_synthetic_windows_contract_passes_without_starting_a_process(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            result = patcher.inspect_computer_use_contract(
                build_capability_fixture(Path(temporary))
            )
        self.assertEqual(result["qualification"], "static-contract-only")
        self.assertEqual(result["stdio"], "three-pipe-json-lines")
        self.assertEqual(result["nativePipe"], "length-prefixed-json-rpc")
        self.assertEqual(result["outboundFrameLimitBytes"], 8 * 1024 * 1024)
        self.assertEqual(result["inboundFrameLimitBytes"], 64 * 1024 * 1024)
        self.assertRegex(result["treeSha256"], r"^[0-9a-f]{64}$")

    def test_rejects_path_escape_and_wrong_file_type(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            resources = build_capability_fixture(Path(temporary))
            manifest_path = resources / "cua_node" / "manifest.json"
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            manifest["node_path"] = "../outside.exe"
            write_text(manifest_path, json.dumps(manifest))
            with self.assertRaisesRegex(RuntimeError, "node_path"):
                patcher.inspect_computer_use_contract(resources)

        with tempfile.TemporaryDirectory() as temporary:
            resources = build_capability_fixture(Path(temporary))
            helper = (
                resources
                / "cua_node"
                / "bin"
                / "node_modules"
                / "@oai"
                / "cua"
                / "bin"
                / "windows"
                / "codex-computer-use.exe"
            )
            helper.unlink()
            helper.mkdir()
            with self.assertRaisesRegex(RuntimeError, "regular file"):
                patcher.inspect_computer_use_contract(resources)

    def test_rejects_oversized_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            resources = build_capability_fixture(Path(temporary))
            manifest = resources / "cua_node" / "manifest.json"
            manifest.write_text("{" + " " * patcher.MAX_CUA_MANIFEST_BYTES + "}", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "invalid size"):
                patcher.inspect_computer_use_contract(resources)

    def test_rejects_missing_pipe_bounds_or_hidden_stdio(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            resources = build_capability_fixture(Path(temporary))
            internal = (
                resources
                / "cua_node"
                / "bin"
                / "node_modules"
                / "@oai"
                / "cua"
                / "dist"
                / "project"
                / "cua"
                / "sky_js"
                / "src"
                / "targets"
                / "windows"
                / "internal"
            )
            helper = internal / "helper_transport.js"
            helper.write_text(
                helper.read_text(encoding="utf-8").replace("windowsHide:!0", "windowsHide:!1"),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(RuntimeError, "windowsHide"):
                patcher.inspect_computer_use_contract(resources)

        with tempfile.TemporaryDirectory() as temporary:
            resources = build_capability_fixture(Path(temporary))
            native = (
                resources
                / "cua_node"
                / "bin"
                / "node_modules"
                / "@oai"
                / "cua"
                / "dist"
                / "project"
                / "cua"
                / "sky_js"
                / "src"
                / "targets"
                / "windows"
                / "internal"
                / "computer_use_client.js"
            )
            native.write_text(
                native.read_text(encoding="utf-8").replace("67108864", "0"),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(RuntimeError, "67108864"):
                patcher.inspect_computer_use_contract(resources)

    def test_rejects_shell_or_automation_fallback(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            resources = build_capability_fixture(Path(temporary))
            helper = next(resources.rglob("helper_transport.js"))
            helper.write_text(
                helper.read_text(encoding="utf-8") + ';spawn("powershell.exe");',
                encoding="utf-8",
            )
            with self.assertRaisesRegex(RuntimeError, "forbidden fallback"):
                patcher.inspect_computer_use_contract(resources)


class AppshotsStaticContractTests(unittest.TestCase):
    def test_patch_is_strict_opt_in_and_requires_native_bridge(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            extracted = Path(temporary)
            main = build_appshots_fixture(extracted)
            patcher.patch_windows_appshots_gate(extracted)
            result = patcher.verify_windows_appshots_contract(extracted)
            patched = main.read_text(encoding="utf-8")

        self.assertFalse(result["defaultEnabled"])
        self.assertEqual(result["enabledValue"], "1")
        self.assertEqual(patched.count('CODEX_ROUTER_ENABLE_APPSHOTS==="1"'), 2)
        self.assertIn("H.windowsCaptureNativeBridge!=null", patched)

    def test_rejects_truthy_or_unconditional_enablement(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            extracted = Path(temporary)
            main = build_appshots_fixture(extracted)
            patcher.patch_windows_appshots_gate(extracted)
            main.write_text(
                main.read_text(encoding="utf-8").replace(
                    'process.env.CODEX_ROUTER_ENABLE_APPSHOTS==="1"',
                    "process.env.CODEX_ROUTER_ENABLE_APPSHOTS",
                ),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(RuntimeError, "strict opt-in"):
                patcher.verify_windows_appshots_contract(extracted)


if __name__ == "__main__":
    unittest.main()
