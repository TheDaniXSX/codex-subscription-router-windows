#!/usr/bin/env python3
"""Build an isolated, recoverable Windows copy of the Codex desktop app.

The official Appx/MSIX installation is always treated as read-only.  This tool
copies its unpackaged Electron application to a sibling staging directory,
validates and patches that copy, and commits it with a same-volume rename.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import secrets
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import uuid
import xml.etree.ElementTree as ET
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path, PurePosixPath


PROJECT_ROOT = Path(__file__).resolve().parent.parent
PROJECT_VERSION = (PROJECT_ROOT / "VERSION").read_text(encoding="utf-8").strip()
MINIMUM_CONTROL_PORT = 49152
MAXIMUM_CONTROL_PORT = 65535
PRODUCT_NAME = "Codex Subscription Router"
BUILD_MANIFEST_NAME = "codex-mux-build.json"
DEFAULT_DESTINATION = (
    Path(os.environ.get("LOCALAPPDATA", Path.home() / "AppData" / "Local"))
    / "Programs"
    / PRODUCT_NAME
)
DEFAULT_STATE_ROOT = (
    Path(os.environ.get("LOCALAPPDATA", Path.home() / "AppData" / "Local"))
    / "Programs"
    / f"{PRODUCT_NAME} Data"
)

# Package version is the externally installed Appx version.  The inner ASAR
# version/build are recorded separately because OpenAI currently ships them at
# different version numbers.
TESTED_SOURCE_BUILDS: dict[str, dict[str, str]] = {
    "26.820.9563.0": {
        "asar_version": "26.820.71523",
        "asar_build": "7226",
        "asar_sha256": "e353c580ef4939d36f4ae32a35c896d089205c1d06b9f711cf78ffa4a3578a8a",
        "codex_sha256": "799ff77125c47b0736ceb36e9b33975bb93d4162bca663730f3a4c90faf2add9",
        "chatgpt_sha256": "4ec11307b67796338d666f40c431b2804e41669576d3bc350dece8703bf4a114",
        "codex_launcher_sha256": "d99a17c753d4d554d4e69a31018366b2b46929e4c2bc72a31f1785c8e827b710",
        "windows_account_sha256": "bd71836a5784f5d808e324578af2dbd1c5fa724d42ec6460a2bbd80756a765a2",
        "cua_tree_sha256": "2726d48210704778798c61a8df08e8747d8adde0a721464e334acab72eead1cf",
        "cua_node_version": "24.19.0",
        "cua_runtime_version": "0.0.9/20260825190732-1bc5ee2d44ce-pr-1350514",
        "cua_package_version": "0.2.3-202608251207-pr-1350514-1bc5ee2d44ce",
    },
}

REQUIRED_UNPACKED_FILES = (
    "node_modules/better-sqlite3/build/Release/better_sqlite3.node",
    "node_modules/node-pty/build/Release/conpty.node",
)

MAX_CUA_MANIFEST_BYTES = 64 * 1024
MAX_CUA_PACKAGE_BYTES = 256 * 1024
MAX_CUA_CONTRACT_SOURCE_BYTES = 2 * 1024 * 1024
MAX_CUA_TREE_BYTES = 1024 * 1024 * 1024
MAX_CUA_FILE_BYTES = 256 * 1024 * 1024
MAX_CUA_FILE_COUNT = 20_000
MAX_APPSHOTS_BUNDLE_BYTES = 64 * 1024 * 1024

CUA_PACKAGE_RELATIVE = PurePosixPath("bin/node_modules/@oai/cua")
CUA_HELPER_TRANSPORT_RELATIVE = PurePosixPath(
    "dist/project/cua/sky_js/src/targets/windows/internal/helper_transport.js"
)
CUA_NATIVE_PIPE_CLIENT_RELATIVE = PurePosixPath(
    "dist/project/cua/sky_js/src/targets/windows/internal/computer_use_client.js"
)


@dataclass(frozen=True)
class SourceInfo:
    package_root: Path | None
    app_root: Path
    package_name: str
    package_version: str
    package_full_name: str
    asar_version: str
    asar_build: str
    asar_sha256: str
    codex_sha256: str
    chatgpt_sha256: str = ""
    codex_launcher_sha256: str = ""
    signer_subject: str = ""
    signer_thumbprint: str = ""
    windows_account_sha256: str = ""
    cua_tree_sha256: str = ""
    cua_node_version: str = ""
    cua_runtime_version: str = ""
    cua_package_version: str = ""

    @property
    def resources(self) -> Path:
        return self.app_root / "resources"


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", action="version", version=f"%(prog)s {PROJECT_VERSION}")
    parser.add_argument(
        "--source",
        type=Path,
        help="Appx package root, its app directory, or ChatGPT.exe (auto-detected if omitted).",
    )
    parser.add_argument("--destination", type=Path, default=DEFAULT_DESTINATION)
    parser.add_argument(
        "--mux",
        type=Path,
        help="Prebuilt codex-mux.exe; otherwise ./cmd/codex-mux is built with Go.",
    )
    parser.add_argument(
        "--launcher",
        type=Path,
        help="Prebuilt launcher; otherwise ./cmd/windows-launcher is built with Go.",
    )
    parser.add_argument(
        "--control-port",
        type=int,
        help=(
            "Reserved loopback control port (49152..65535). If omitted, the patcher "
            "uses a cryptographically random high port."
        ),
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Replace an existing destination after moving it to a recoverable backup.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Perform discovery, build, extraction, anchor, and layout checks without installing.",
    )
    parser.add_argument(
        "--allow-untested-source",
        action="store_true",
        help="Allow an unknown version/hash; all structural and semantic anchors remain mandatory.",
    )
    return parser.parse_args(argv)


def resolve_control_port(value: int | None) -> int:
    if value is None:
        return MINIMUM_CONTROL_PORT + secrets.randbelow(
            MAXIMUM_CONTROL_PORT - MINIMUM_CONTROL_PORT + 1
        )
    if not MINIMUM_CONTROL_PORT <= value <= MAXIMUM_CONTROL_PORT:
        raise ValueError(
            f"control port must be between {MINIMUM_CONTROL_PORT} and {MAXIMUM_CONTROL_PORT}"
        )
    return value


def run(command: list[str], *, cwd: Path | None = None) -> None:
    subprocess.run(command, cwd=cwd, check=True)


def output(command: list[str]) -> str:
    return subprocess.check_output(command, text=True, encoding="utf-8").strip()


def require_tool(name: str) -> str:
    discovered = shutil.which(name)
    if discovered is not None:
        return discovered
    if os.name == "nt" and name.lower() in {"go", "go.exe"}:
        program_files = Path(os.environ.get("ProgramFiles", "C:/Program Files"))
        standard = program_files / "Go" / "bin" / "go.exe"
        if standard.is_file():
            return str(standard)
    raise RuntimeError(f"required tool not found: {name}")


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def tree_file_hashes(root: Path) -> dict[str, str]:
    if not root.is_dir():
        raise RuntimeError(f"required resource directory is missing: {root}")
    return {
        path.relative_to(root).as_posix(): sha256_file(path)
        for path in sorted(root.rglob("*"), key=lambda item: item.as_posix().lower())
        if path.is_file() and not path.is_symlink()
    }


def tree_difference(expected: dict[str, str], actual: dict[str, str]) -> str:
    missing = sorted(set(expected) - set(actual))[:5]
    extra = sorted(set(actual) - set(expected))[:5]
    changed = sorted(
        path for path in set(expected) & set(actual) if expected[path] != actual[path]
    )[:5]
    return f"missing={missing}, extra={extra}, hash-different={changed}"


def tree_digest(file_hashes: dict[str, str]) -> str:
    digest = hashlib.sha256()
    for name, file_hash in file_hashes.items():
        digest.update(name.encode("utf-8"))
        digest.update(b"\0")
        digest.update(file_hash.encode("ascii"))
        digest.update(b"\n")
    return digest.hexdigest()


def _is_linklike(path: Path) -> bool:
    if path.is_symlink():
        return True
    is_junction = getattr(path, "is_junction", None)
    if is_junction is not None and is_junction():
        return True
    try:
        attributes = getattr(path.stat(follow_symlinks=False), "st_file_attributes", 0)
    except (FileNotFoundError, OSError):
        return False
    return bool(attributes & getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0))


def _require_regular_file(path: Path, label: str) -> Path:
    if _is_linklike(path) or not path.is_file():
        raise RuntimeError(f"{label} must be a regular file: {path}")
    return path


def _read_bounded_text(path: Path, maximum: int, label: str) -> str:
    _require_regular_file(path, label)
    size = path.stat().st_size
    if size <= 0 or size > maximum:
        raise RuntimeError(f"{label} has invalid size {size}; maximum is {maximum}")
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as error:
        raise RuntimeError(f"{label} is not valid UTF-8: {path}") from error


def _read_bounded_json(path: Path, maximum: int, label: str) -> dict[str, object]:
    try:
        result = json.loads(_read_bounded_text(path, maximum, label))
    except json.JSONDecodeError as error:
        raise RuntimeError(f"{label} is not valid JSON: {path}") from error
    if not isinstance(result, dict):
        raise RuntimeError(f"{label} must contain a JSON object: {path}")
    return result


def _safe_relative_file(root: Path, value: object, label: str) -> Path:
    if not isinstance(value, str) or not value or len(value) > 512:
        raise RuntimeError(f"{label} must be a non-empty bounded relative path")
    normalized = value.replace("\\", "/")
    relative = PurePosixPath(normalized)
    if (
        relative.is_absolute()
        or ":" in normalized
        or any(part in {"", ".", ".."} for part in relative.parts)
    ):
        raise RuntimeError(f"{label} must stay inside its declared root: {value!r}")
    return _require_regular_file(root.joinpath(*relative.parts), label)


def inspect_computer_use_contract(resources: Path) -> dict[str, object]:
    """Validate the copied Windows CUA layout without launching any helper.

    This is deliberately a static contract.  It proves provenance, bounded
    configuration, PE/file types, stdio/native-pipe framing, and the absence of
    an injected shell fallback.  It does not claim that Windows consent,
    capture, input, or secure-desktop behavior was exercised end to end.
    """

    cua_root = resources / "cua_node"
    if _is_linklike(cua_root) or not cua_root.is_dir():
        raise RuntimeError(f"Computer Use runtime must be a real directory: {cua_root}")

    entries = list(cua_root.rglob("*"))
    if any(_is_linklike(path) for path in entries):
        raise RuntimeError("Computer Use runtime contains a symbolic link or junction")
    files = [path for path in entries if path.is_file()]
    if not files or len(files) > MAX_CUA_FILE_COUNT:
        raise RuntimeError(
            f"Computer Use runtime file count {len(files)} is outside 1..{MAX_CUA_FILE_COUNT}"
        )
    sizes = [path.stat().st_size for path in files]
    if max(sizes) > MAX_CUA_FILE_BYTES or sum(sizes) > MAX_CUA_TREE_BYTES:
        raise RuntimeError("Computer Use runtime exceeds the static qualification size limits")

    manifest = _read_bounded_json(
        cua_root / "manifest.json", MAX_CUA_MANIFEST_BYTES, "Computer Use runtime manifest"
    )
    exact_manifest_values = {
        "platform": "windows",
        "arch": "x64",
        "target": "windows-x64",
        "node_path": "bin/node.exe",
        "node_repl_path": "bin/node_repl.exe",
        "node_modules": "bin/node_modules",
    }
    for field, expected in exact_manifest_values.items():
        if manifest.get(field) != expected:
            raise RuntimeError(
                f"Computer Use manifest field {field!r} is {manifest.get(field)!r}, expected {expected!r}"
            )
    node_version = manifest.get("node_version")
    if not isinstance(node_version, str) or re.fullmatch(r"\d+\.\d+\.\d+", node_version) is None:
        raise RuntimeError("Computer Use manifest has an invalid node_version")
    runtime_version = manifest.get("runtime_archive_version")
    if not isinstance(runtime_version, str) or not runtime_version or len(runtime_version) > 192:
        raise RuntimeError("Computer Use manifest has an invalid runtime_archive_version")
    runtime_archive = manifest.get("runtime_archive_name")
    if (
        not isinstance(runtime_archive, str)
        or len(runtime_archive) > 255
        or Path(runtime_archive).name != runtime_archive
        or not runtime_archive.endswith("-windows-x64.zip")
    ):
        raise RuntimeError("Computer Use manifest has an unsafe runtime_archive_name")

    for field in ("node_path", "node_repl_path"):
        executable = _safe_relative_file(cua_root, manifest[field], f"Computer Use {field}")
        if not is_pe_executable(executable):
            raise RuntimeError(f"Computer Use {field} is not a PE executable: {executable}")

    package_root = cua_root.joinpath(*CUA_PACKAGE_RELATIVE.parts)
    package = _read_bounded_json(
        package_root / "package.json", MAX_CUA_PACKAGE_BYTES, "@oai/cua package manifest"
    )
    if package.get("name") != "@oai/cua" or package.get("type") != "module":
        raise RuntimeError("Computer Use package identity/type changed unexpectedly")
    package_version = package.get("version")
    if not isinstance(package_version, str) or not package_version or len(package_version) > 192:
        raise RuntimeError("Computer Use package has an invalid version")
    _safe_relative_file(package_root, package.get("main"), "@oai/cua main module")

    native_helper = package_root / "bin" / "windows" / "codex-computer-use.exe"
    code_mode_host = resources / "codex-code-mode-host.exe"
    for executable, label in (
        (native_helper, "Computer Use native helper"),
        (code_mode_host, "Codex code-mode host"),
    ):
        _require_regular_file(executable, label)
        if not is_pe_executable(executable):
            raise RuntimeError(f"{label} is not a PE executable: {executable}")

    helper_transport = _read_bounded_text(
        package_root.joinpath(*CUA_HELPER_TRANSPORT_RELATIVE.parts),
        MAX_CUA_CONTRACT_SOURCE_BYTES,
        "Computer Use stdio transport",
    )
    for marker in (
        'stdio:["pipe","pipe","pipe"]',
        "windowsHide:!0",
        ".stdin.write(",
        ".stdout.on(",
        ".stderr.on(",
        "CODEX_HOME",
        "computer-use request timed out",
    ):
        if marker not in helper_transport:
            raise RuntimeError(f"Computer Use stdio transport contract is missing {marker!r}")

    native_pipe = _read_bounded_text(
        package_root.joinpath(*CUA_NATIVE_PIPE_CLIENT_RELATIVE.parts),
        MAX_CUA_CONTRACT_SOURCE_BYTES,
        "Computer Use native-pipe client",
    )
    for marker in (
        "SKY_CUA_NATIVE_PIPE",
        "SKY_CUA_NATIVE_PIPE_DIRECTORY",
        "createConnection",
        "--parent-pid",
        "8388608",
        "67108864",
        "Computer Use native pipe is unavailable",
        "Computer Use native pipe frame is too large",
    ):
        if marker not in native_pipe:
            raise RuntimeError(f"Computer Use native-pipe contract is missing {marker!r}")

    joined_transport = helper_transport + "\n" + native_pipe
    for fallback in ("powershell.exe", "cmd.exe", "osascript", "SendInput", "UIAutomation"):
        if fallback.lower() in joined_transport.lower():
            raise RuntimeError(f"Computer Use transport contains forbidden fallback {fallback!r}")

    hashes = tree_file_hashes(cua_root)
    return {
        "qualification": "static-contract-only",
        "treeSha256": tree_digest(hashes),
        "fileCount": len(files),
        "totalBytes": sum(sizes),
        "nodeVersion": node_version,
        "runtimeVersion": runtime_version,
        "packageVersion": package_version,
        "stdio": "three-pipe-json-lines",
        "nativePipe": "length-prefixed-json-rpc",
        "outboundFrameLimitBytes": 8 * 1024 * 1024,
        "inboundFrameLimitBytes": 64 * 1024 * 1024,
    }


def is_pe_executable(path: Path) -> bool:
    if not path.is_file():
        return False
    try:
        with path.open("rb") as handle:
            return handle.read(2) == b"MZ"
    except OSError:
        return False


def _canonical(path: Path) -> Path:
    return Path(os.path.normcase(str(path.expanduser().resolve(strict=False))))


def _is_relative_to(path: Path, parent: Path) -> bool:
    try:
        path.relative_to(parent)
        return True
    except ValueError:
        return False


def validate_source_destination(source_app: Path, destination: Path) -> None:
    source = _canonical(source_app)
    target = _canonical(destination)
    if source == target or _is_relative_to(target, source):
        raise RuntimeError(
            "destination must be outside the official source tree; the source is never patched in place"
        )
    if _is_relative_to(source, target):
        raise RuntimeError("destination cannot be a parent of the official source tree")
    protected = Path(os.environ.get("ProgramFiles", "C:/Program Files")) / "WindowsApps"
    if _is_relative_to(target, _canonical(protected)):
        raise RuntimeError("destination must not be inside Program Files\\WindowsApps")


def discover_appx_source() -> Path:
    if os.name != "nt":
        raise RuntimeError("automatic Appx discovery is only available on Windows; pass --source")
    script = (
        "$p=Get-AppxPackage -Name OpenAI.Codex | "
        "Sort-Object Version -Descending | Select-Object -First 1; "
        "if($null -eq $p){exit 3}; [Console]::Out.Write($p.InstallLocation)"
    )
    try:
        location = output(["powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script])
    except subprocess.CalledProcessError as error:
        raise RuntimeError("OpenAI.Codex is not installed for the current Windows user") from error
    if not location:
        raise RuntimeError("Appx discovery returned an empty OpenAI.Codex install location")
    return Path(location)


def normalize_source_path(source: Path) -> tuple[Path | None, Path]:
    candidate = source.expanduser().resolve(strict=True)
    if candidate.is_file():
        if candidate.name.lower() not in {"chatgpt.exe", "codex.exe"}:
            raise RuntimeError(f"source executable is not ChatGPT.exe or Codex.exe: {candidate}")
        candidate = candidate.parent

    package_root: Path | None = None
    if (candidate / "AppxManifest.xml").is_file() and (candidate / "app").is_dir():
        package_root = candidate
        app_root = candidate / "app"
    elif (candidate / "resources" / "app.asar").is_file():
        app_root = candidate
        if (candidate.parent / "AppxManifest.xml").is_file():
            package_root = candidate.parent
    else:
        raise RuntimeError(
            f"source does not contain app/resources/app.asar or resources/app.asar: {candidate}"
        )
    return package_root, app_root


def parse_appx_identity(package_root: Path | None) -> tuple[str, str, str]:
    if package_root is None:
        return "OpenAI.Codex", "unknown", "manual-source"
    manifest = package_root / "AppxManifest.xml"
    try:
        root = ET.parse(manifest).getroot()
    except (ET.ParseError, OSError) as error:
        raise RuntimeError(f"could not parse {manifest}") from error
    identity = next((element for element in root.iter() if element.tag.endswith("}Identity")), None)
    if identity is None:
        raise RuntimeError(f"Appx manifest has no Identity: {manifest}")
    name = identity.attrib.get("Name", "")
    version = identity.attrib.get("Version", "")
    if name != "OpenAI.Codex" or not version:
        raise RuntimeError(f"unexpected Appx identity {name!r} version {version!r}")
    return name, version, package_root.name


def authenticode_info(path: Path) -> tuple[str, str]:
    if os.name != "nt":
        raise RuntimeError("Authenticode verification is only available on Windows")
    environment = os.environ.copy()
    environment["CODEX_ROUTER_SIGNATURE_TARGET"] = str(path)
    shell = shutil.which("pwsh") or shutil.which("powershell.exe")
    if shell is None:
        raise RuntimeError("PowerShell is required for Authenticode verification")
    script = (
        '$ErrorActionPreference="Stop";'
        "$s=Get-AuthenticodeSignature -LiteralPath $env:CODEX_ROUTER_SIGNATURE_TARGET;"
        "[pscustomobject]@{status=[int]$s.Status;"
        "subject=$s.SignerCertificate.Subject;thumbprint=$s.SignerCertificate.Thumbprint}"
        "|ConvertTo-Json -Compress"
    )
    try:
        raw = subprocess.check_output(
            [shell, "-NoProfile", "-NonInteractive", "-Command", script],
            env=environment,
            text=True,
            encoding="utf-8",
        )
        result = json.loads(raw)
    except (subprocess.CalledProcessError, json.JSONDecodeError, OSError) as error:
        raise RuntimeError(f"could not verify Authenticode signature: {path}") from error
    subject = str(result.get("subject") or "")
    thumbprint = str(result.get("thumbprint") or "").upper()
    if result.get("status") != 0 or "OpenAI" not in subject or not thumbprint:
        raise RuntimeError(
            f"source executable does not have a valid OpenAI Authenticode signature: {path}"
        )
    return subject, thumbprint


def inspect_source(source: Path) -> SourceInfo:
    package_root, app_root = normalize_source_path(source)
    resources = app_root / "resources"
    required = (
        app_root / "ChatGPT.exe",
        resources / "app.asar",
        resources / "codex.exe",
        resources / "native" / "windows-account.node",
    )
    missing = [str(path) for path in required if not path.is_file()]
    if missing:
        raise RuntimeError("source is missing required files: " + ", ".join(missing))
    if (app_root / "ChatGPT.real.exe").exists() or (resources / "codex.real.exe").exists():
        raise RuntimeError("source already looks patched; use the official OpenAI.Codex installation")
    if not is_pe_executable(app_root / "ChatGPT.exe"):
        raise RuntimeError("source ChatGPT.exe is not a PE executable")
    if not is_pe_executable(resources / "codex.exe"):
        raise RuntimeError("source resources/codex.exe is not a PE executable")

    package_name, package_version, package_full_name = parse_appx_identity(package_root)
    signature_paths = (
        app_root / "ChatGPT.exe",
        app_root / "Codex.exe",
        resources / "codex.exe",
        resources / "native" / "windows-account.node",
    )
    if not signature_paths[1].is_file():
        raise RuntimeError("source is missing the signed Codex.exe desktop helper")
    signatures = [authenticode_info(path) for path in signature_paths]
    signer_subject, signer_thumbprint = signatures[0]
    if any(thumbprint != signer_thumbprint for _, thumbprint in signatures[1:]):
        raise RuntimeError("official source executables are signed by different certificates")
    asar_package = extract_asar_package_json(resources / "app.asar")
    computer_use = inspect_computer_use_contract(resources)
    return SourceInfo(
        package_root=package_root,
        app_root=app_root,
        package_name=package_name,
        package_version=package_version,
        package_full_name=package_full_name,
        asar_version=str(asar_package.get("version", "unknown")),
        asar_build=str(asar_package.get("codexBuildNumber", "unknown")),
        asar_sha256=sha256_file(resources / "app.asar"),
        codex_sha256=sha256_file(resources / "codex.exe"),
        chatgpt_sha256=sha256_file(app_root / "ChatGPT.exe"),
        codex_launcher_sha256=sha256_file(app_root / "Codex.exe"),
        signer_subject=signer_subject,
        signer_thumbprint=signer_thumbprint,
        windows_account_sha256=sha256_file(resources / "native" / "windows-account.node"),
        cua_tree_sha256=str(computer_use["treeSha256"]),
        cua_node_version=str(computer_use["nodeVersion"]),
        cua_runtime_version=str(computer_use["runtimeVersion"]),
        cua_package_version=str(computer_use["packageVersion"]),
    )


def ensure_asar_tool() -> Path:
    executable = "asar.cmd" if os.name == "nt" else "asar"
    asar = PROJECT_ROOT / "node_modules" / ".bin" / executable
    package_manifest = PROJECT_ROOT / "node_modules" / "@electron" / "asar" / "package.json"
    expected = json.loads((PROJECT_ROOT / "package.json").read_text(encoding="utf-8"))[
        "devDependencies"
    ]["@electron/asar"]
    if not asar.is_file() or not package_manifest.is_file():
        raise RuntimeError("run `npm ci --ignore-scripts` before patching")
    actual = json.loads(package_manifest.read_text(encoding="utf-8")).get("version")
    if actual != expected:
        raise RuntimeError(
            f"installed @electron/asar is {actual!r}, expected {expected!r}; run `npm ci --ignore-scripts`"
        )
    return asar


def extract_asar_package_json(asar_path: Path) -> dict[str, object]:
    asar = ensure_asar_tool()
    with tempfile.TemporaryDirectory(prefix="codex-mux-asar-metadata-") as temporary:
        run([str(asar), "extract-file", str(asar_path), "package.json"], cwd=Path(temporary))
        package_path = Path(temporary) / "package.json"
        if not package_path.is_file():
            # @electron/asar historically wrote beside the archive for an
            # absolute path; fall back to a full extraction without guessing.
            extracted = Path(temporary) / "asar"
            run([str(asar), "extract", str(asar_path), str(extracted)])
            package_path = extracted / "package.json"
        try:
            data = json.loads(package_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as error:
            raise RuntimeError("could not read package.json from app.asar") from error
    if not isinstance(data, dict):
        raise RuntimeError("app.asar package.json is not an object")
    return data


def validate_approved_source(source: SourceInfo, allow_untested: bool) -> None:
    expected = TESTED_SOURCE_BUILDS.get(source.package_version)
    mismatches: list[str] = []
    if expected is None:
        mismatches.append(f"package version {source.package_version!r} is not approved")
    else:
        comparisons = {
            "ASAR version": (source.asar_version, expected["asar_version"]),
            "ASAR build": (source.asar_build, expected["asar_build"]),
            "app.asar SHA-256": (source.asar_sha256, expected["asar_sha256"]),
            "codex.exe SHA-256": (source.codex_sha256, expected["codex_sha256"]),
            "ChatGPT.exe SHA-256": (source.chatgpt_sha256, expected["chatgpt_sha256"]),
            "Codex.exe SHA-256": (
                source.codex_launcher_sha256,
                expected["codex_launcher_sha256"],
            ),
            "windows-account.node SHA-256": (
                source.windows_account_sha256,
                expected["windows_account_sha256"],
            ),
            "Computer Use tree SHA-256": (
                source.cua_tree_sha256,
                expected["cua_tree_sha256"],
            ),
            "Computer Use Node version": (
                source.cua_node_version,
                expected["cua_node_version"],
            ),
            "Computer Use runtime version": (
                source.cua_runtime_version,
                expected["cua_runtime_version"],
            ),
            "@oai/cua package version": (
                source.cua_package_version,
                expected["cua_package_version"],
            ),
        }
        for label, (actual, wanted) in comparisons.items():
            if actual.lower() != wanted.lower():
                mismatches.append(f"{label} is {actual!r}, expected {wanted!r}")
    if mismatches and not allow_untested:
        raise RuntimeError(
            "source build is not approved: " + "; ".join(mismatches) + "; "
            "review the update or pass --allow-untested-source (anchors still fail closed)"
        )
    if mismatches:
        print(
            "Warning: continuing with an untested source; every known semantic anchor remains mandatory:\n  "
            + "\n  ".join(mismatches),
            file=sys.stderr,
        )


def resolve_state_root() -> Path:
    configured = (
        os.environ.get("CODEX_ROUTER_DATA_DIR")
        or os.environ.get("CODEX_MUX_HOME")
        or os.environ.get("CODEX_MUX_STATE_ROOT")
    )
    # Path.resolve follows MSIX file-system redirection when this tool is
    # launched from the packaged Codex host. That can silently rewrite an
    # explicit LocalAppData path into OpenAI.Codex's LocalCache. Preserve the
    # installer's validated absolute path lexically instead.
    candidate = (Path(configured) if configured else DEFAULT_STATE_ROOT).expanduser()
    return Path(os.path.abspath(os.path.normpath(str(candidate))))


def prepare_control_token(state_root: Path) -> tuple[str, bool]:
    path = state_root / "control-token"
    if path.exists():
        token = path.read_text(encoding="utf-8").strip()
        if re.fullmatch(r"[0-9a-f]{64}", token) is None:
            raise RuntimeError(f"invalid control token at {path}")
        return token, False
    return secrets.token_hex(32), True


def persist_control_token(state_root: Path, token: str) -> bool:
    state_root.mkdir(parents=True, exist_ok=True)
    token_path = state_root / "control-token"
    try:
        descriptor = os.open(token_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        existing = token_path.read_text(encoding="utf-8").strip()
        if not secrets.compare_digest(existing, token):
            raise RuntimeError(f"control token changed concurrently at {token_path}")
        return False
    with os.fdopen(descriptor, "w", encoding="utf-8", newline="") as handle:
        handle.write(token)
        handle.flush()
        os.fsync(handle.fileno())
    try:
        token_path.chmod(stat.S_IREAD | stat.S_IWRITE)
    except OSError:
        pass
    return True


def _build_go_executable(destination: Path, package: str, *, windows_gui: bool = False) -> None:
    go = require_tool("go")
    command = [go, "build", "-trimpath"]
    flags = "-s -w" + (" -H=windowsgui" if windows_gui else "")
    command.extend(["-ldflags", flags, "-o", str(destination), package])
    run(command, cwd=PROJECT_ROOT)


def prepare_executable(
    supplied: Path | None,
    destination: Path,
    *,
    package: str,
    description: str,
    windows_gui: bool = False,
) -> Path:
    if supplied is None:
        prebuilt_name = "ChatGPT.exe" if windows_gui else "codex-mux.exe"
        prebuilt = PROJECT_ROOT / "build" / "windows" / prebuilt_name
        if prebuilt.is_file():
            shutil.copy2(prebuilt, destination)
            if not is_pe_executable(destination):
                raise RuntimeError(f"prebuilt {description} is not a Windows PE executable: {prebuilt}")
            return destination
        package_path = PROJECT_ROOT / package.removeprefix("./").replace("/", os.sep)
        if not package_path.is_dir():
            raise RuntimeError(f"{description} source directory not found: {package_path}")
        _build_go_executable(destination, package, windows_gui=windows_gui)
    else:
        supplied = supplied.expanduser().resolve(strict=True)
        if not supplied.is_file():
            raise RuntimeError(f"{description} is not a file: {supplied}")
        shutil.copy2(supplied, destination)
    if not is_pe_executable(destination):
        raise RuntimeError(f"{description} is not a Windows PE executable: {destination}")
    return destination


def patch_owl_config(staged_app: Path) -> None:
    path = staged_app / "resources" / "owl-app.ini"
    text = path.read_text(encoding="utf-8")
    app_version_lines = [line for line in text.splitlines() if line.startswith("AppVersion=")]
    if len(app_version_lines) != 1:
        raise RuntimeError("owl-app.ini must contain exactly one AppVersion entry")
    text = replace_unique(
        text,
        "UserDataDirectoryName=Codex",
        f"UserDataDirectoryName={PRODUCT_NAME}",
        "Owl user-data directory",
    )
    if app_version_lines[0] not in text:
        raise RuntimeError("patching owl-app.ini unexpectedly changed AppVersion")
    path.write_text(text, encoding="utf-8")


def replace_unique(text: str, anchor: str, replacement: str, description: str) -> str:
    count = text.count(anchor)
    if count != 1:
        raise RuntimeError(f"expected one {description} anchor, found {count}")
    return text.replace(anchor, replacement, 1)


def replace_identifiers(source: str, mapping: dict[str, str]) -> str:
    for original, replacement in mapping.items():
        pattern = (
            re.compile(rf"(?<![A-Za-z0-9_$]){re.escape(original)}(?![A-Za-z0-9_$])")
            if original.startswith("$")
            else re.compile(rf"(?<![A-Za-z0-9_$]){re.escape(original)}(?![A-Za-z0-9_$])")
        )
        source, count = pattern.subn(replacement, source)
        if count == 0:
            raise RuntimeError(f"injected UI identifier {original!r} was not found")
    return source


def patch_windows_bootstrap(extracted: Path) -> None:
    bootstrap_files = list((extracted / ".vite" / "build").glob("bootstrap-*.js"))
    if len(bootstrap_files) != 1:
        raise RuntimeError(f"expected one desktop bootstrap bundle, found {len(bootstrap_files)}")
    path = bootstrap_files[0]
    text = path.read_text(encoding="utf-8")
    if text.count("CODEX_ELECTRON_USER_DATA_PATH") != 2:
        raise RuntimeError("desktop bootstrap no longer has the expected explicit user-data support")
    user_data_pattern = re.compile(
        r"\.app\.setPath\(`userData`,[A-Za-z_$][\w$]*\(\{appDataPath:"
        r"[A-Za-z_$][\w$]*\.app\.getPath\(`appData`\),buildFlavor:"
    )
    if len(user_data_pattern.findall(text)) != 1:
        raise RuntimeError("could not verify user-data isolation before desktop startup")

    updater_pattern = re.compile(
        r"await (?P<manager>[A-Za-z_$][\w$]*)\.initialize\(\);"
        r"(?=let\{runMainAppStartup:)"
    )
    text, count = updater_pattern.subn("", text, count=1)
    if count != 1:
        raise RuntimeError("could not disable the copied app's automatic updater")

    app_name_pattern = re.compile(
        r"(?P<electron>[A-Za-z_$][\w$]*)\.app\.setName\([^;]+?\),"
        r"(?=(?P=electron)\.app\.setPath\(`userData`)"
    )
    text, count = app_name_pattern.subn(
        lambda match: f"{match.group('electron')}.app.setName(`{PRODUCT_NAME}`),",
        text,
        count=1,
    )
    if count != 1:
        raise RuntimeError("could not give the copied desktop app an independent display name")

    app_id_pattern = re.compile(
        r"process\.platform===`win32`&&(?P<electron>[A-Za-z_$][\w$]*)\.app\."
        r"setAppUserModelId\([^;]+\)"
    )
    text, count = app_id_pattern.subn(
        lambda match: (
            f"process.platform===`win32`&&{match.group('electron')}.app."
            "setAppUserModelId(`com.openai.codex.subscription-router`)"
        ),
        text,
        count=1,
    )
    if count != 1:
        raise RuntimeError("could not isolate the copied app's Windows AppUserModelID")
    path.write_text(text, encoding="utf-8")


def patch_windows_runtime_paths(extracted: Path) -> None:
    build = extracted / ".vite" / "build"
    source_files = list(build.glob("src-*.js"))
    runtime_anchor = (
        "function fF(e){return(0,i.join)(process.env.LOCALAPPDATA??"
        "(0,i.join)((0,r.homedir)(),`AppData`,`Local`),...e)}"
    )
    matches = [path for path in source_files if runtime_anchor in path.read_text(encoding="utf-8")]
    if len(matches) != 1:
        raise RuntimeError(f"expected one bundled-runtime cache root anchor, found {len(matches)}")
    path = matches[0]
    text = path.read_text(encoding="utf-8")
    runtime_replacement = (
        "function fF(e){let t=process.env.CODEX_MUX_HOME??(0,i.join)("
        "process.env.LOCALAPPDATA??(0,i.join)((0,r.homedir)(),`AppData`,`Local`),"
        "`Codex Subscription Router`);if(e[0]===`OpenAI`&&e[1]===`Codex`)"
        "return(0,i.join)(t,`runtime-cache`,...e.slice(2));"
        "return(0,i.join)(process.env.LOCALAPPDATA??"
        "(0,i.join)((0,r.homedir)(),`AppData`,`Local`),...e)}"
    )
    text = replace_unique(text, runtime_anchor, runtime_replacement, "runtime cache isolation")
    path.write_text(text, encoding="utf-8")

    logger_path = next(iter(build.glob("file-based-logger-*.js")), None)
    if logger_path is None:
        raise RuntimeError("desktop file logger bundle was not found")
    logger = logger_path.read_text(encoding="utf-8")
    logger_anchor = (
        "(0,i.join)(n.LOCALAPPDATA??(0,i.join)(a,`AppData`,`Local`),`Codex`,`Logs`)"
    )
    logger_replacement = (
        "(0,i.join)(process.env.CODEX_MUX_HOME??n.LOCALAPPDATA??"
        "(0,i.join)(a,`AppData`,`Local`),"
        "process.env.CODEX_MUX_HOME?`logs`:`Codex Subscription Router/logs`)"
    )
    logger = replace_unique(logger, logger_anchor, logger_replacement, "desktop log isolation")
    logger_path.write_text(logger, encoding="utf-8")

    worker_path = build / "worker.js"
    worker = worker_path.read_text(encoding="utf-8")
    worker_anchor = (
        "(0,E.join)(n.LOCALAPPDATA??(0,E.join)(r,`AppData`,`Local`),`Codex`,`Logs`)"
    )
    worker_replacement = (
        "(0,E.join)(process.env.CODEX_MUX_HOME??n.LOCALAPPDATA??"
        "(0,E.join)(r,`AppData`,`Local`),"
        "process.env.CODEX_MUX_HOME?`logs`:`Codex Subscription Router/logs`)"
    )
    worker = replace_unique(worker, worker_anchor, worker_replacement, "worker log isolation")
    worker_path.write_text(worker, encoding="utf-8")


def patch_windows_native_messaging_isolation(extracted: Path) -> None:
    source_files = list((extracted / ".vite" / "build").glob("src-*.js"))
    registry_delete_anchor = (
        "function oY(e){if(process.platform!==`win32`)return;let t=`${jq}\\\\${e}`;"
        "try{await Oq(`reg`,[`query`,t])}catch{return}await Oq(`reg`,[`delete`,t,`/f`])}"
    )
    matches = [path for path in source_files if registry_delete_anchor in path.read_text(encoding="utf-8")]
    if len(matches) != 1:
        raise RuntimeError(f"expected one native-host registry delete anchor, found {len(matches)}")
    path = matches[0]
    text = path.read_text(encoding="utf-8")
    text = replace_unique(
        text,
        registry_delete_anchor,
        "function oY(e){return}",
        "native-host registry delete",
    )
    registry_add_anchor = (
        "function sY(e){let t=e.manifestPath;process.platform!==`win32`||t==null||"
        "await Oq(`reg`,[`add`,`${jq}\\\\${e.nativeHostName}`,`/ve`,`/t`,`REG_SZ`,"
        "`/d`,t,`/f`])}"
    )
    text = replace_unique(
        text,
        registry_add_anchor,
        "function sY(e){return}",
        "native-host registry add",
    )
    manifest_read_anchor = (
        "case`win32`:return Fy(`windows`).map(t=>(0,i.join)(r.default.homedir(),"
        "t,`${e}.json`));"
    )
    text = replace_unique(
        text,
        manifest_read_anchor,
        "case`win32`:return[];",
        "official Chrome native-host manifest read",
    )
    state_paths_anchor = (
        "function yJ(e){let t=bJ();return[...t==null?[]:[t],(0,i.join)(e.codexHome,Mq)]"
        ".filter((e,t,n)=>n.indexOf(e)===t)}"
    )
    state_paths_replacement = (
        "function yJ(e){if(process.platform===`win32`)return process.env.CODEX_MUX_HOME?"
        "[(0,i.join)(process.env.CODEX_MUX_HOME,Mq)]:[];let t=bJ();return"
        "[...t==null?[]:[t],(0,i.join)(e.codexHome,Mq)].filter((e,t,n)=>n.indexOf(e)===t)}"
    )
    text = replace_unique(
        text,
        state_paths_anchor,
        state_paths_replacement,
        "native-host registry state paths",
    )
    global_state_anchor = (
        "case`win32`:return(0,i.join)(process.env.LOCALAPPDATA??"
        "(0,i.join)(r.default.homedir(),`AppData`,`Local`),`OpenAI`,`Codex`,Mq);"
    )
    global_state_replacement = (
        "case`win32`:return(0,i.join)(process.env.CODEX_MUX_HOME??"
        "(0,i.join)(process.env.LOCALAPPDATA??(0,i.join)(r.default.homedir(),"
        "`AppData`,`Local`),`Codex Subscription Router`),Mq);"
    )
    text = replace_unique(
        text,
        global_state_anchor,
        global_state_replacement,
        "global Chrome native-host state path",
    )
    for old in (
        registry_delete_anchor,
        registry_add_anchor,
        manifest_read_anchor,
        state_paths_anchor,
        global_state_anchor,
    ):
        if old in text:
            raise RuntimeError("an official Chrome native-host mutation path remained after patching")
    path.write_text(text, encoding="utf-8")


def verify_windows_integration_isolation(extracted: Path) -> None:
    # The official Explorer verb is registered by the Appx manifest, which is
    # deliberately outside app_root and is never copied.  Fail if a future app
    # adds a second, self-registering implementation inside ASAR.
    for candidate in extracted.rglob("*"):
        if not candidate.is_file() or candidate.is_symlink():
            continue
        try:
            data = candidate.read_bytes()
        except OSError:
            continue
        if b"OpenProjectInCodex" in data or "OpenProjectInCodex".encode("utf-16le") in data:
            raise RuntimeError(
                f"copied app contains a self-registering Explorer integration anchor: {candidate}"
            )

    protocol_files = list((extracted / ".vite" / "build").glob("window-all-closed-*.js"))
    protocol_anchor = "if(process.platform===`win32`)return;"
    matches = [
        path
        for path in protocol_files
        if protocol_anchor in path.read_text(encoding="utf-8")
        and "setAsDefaultProtocolClient" in path.read_text(encoding="utf-8")
    ]
    if len(matches) != 1:
        raise RuntimeError(
            "could not verify that the copied app skips codex:// protocol registration on Windows"
        )


def patch_windows_appshots_gate(extracted: Path) -> None:
    main_files = list((extracted / ".vite" / "build").glob("main-*.js"))
    if len(main_files) != 1:
        raise RuntimeError(f"expected one desktop main bundle, found {len(main_files)}")
    path = main_files[0]
    text = path.read_text(encoding="utf-8")
    bridge_anchor = "V=y&&a.a.isInternal(i)?Uje(g):null,ne=new PAe"
    bridge_replacement = (
        "V=y&&(a.a.isInternal(i)||process.env.CODEX_ROUTER_ENABLE_APPSHOTS===\"1\")"
        "?Uje(g):null,ne=new PAe"
    )
    text = replace_unique(
        text,
        bridge_anchor,
        bridge_replacement,
        "Windows Appshots native bridge gate",
    )
    feature_anchor = (
        "let s=br(e);I&&H.windowsCaptureNativeBridge==null&&(s.appshotsEnabled=!1),"
        "I&&!a.a.isInternal(c)&&(s.appshotsEnabled=!1),Re.setDesktopFeatureAvailability(s);"
    )
    feature_replacement = (
        "let s=br(e);if(I&&process.env.CODEX_ROUTER_ENABLE_APPSHOTS===\"1\")"
        "s.appshotsEnabled=H.windowsCaptureNativeBridge!=null;else{"
        "I&&H.windowsCaptureNativeBridge==null&&(s.appshotsEnabled=!1),"
        "I&&!a.a.isInternal(c)&&(s.appshotsEnabled=!1)}Re.setDesktopFeatureAvailability(s);"
    )
    text = replace_unique(
        text,
        feature_anchor,
        feature_replacement,
        "Windows Appshots feature gate",
    )
    if bridge_anchor in text or feature_anchor in text:
        raise RuntimeError("an original Windows Appshots gate remained after patching")
    path.write_text(text, encoding="utf-8")


def verify_windows_appshots_contract(extracted: Path) -> dict[str, object]:
    main_files = list((extracted / ".vite" / "build").glob("main-*.js"))
    if len(main_files) != 1:
        raise RuntimeError(f"expected one Appshots desktop main bundle, found {len(main_files)}")
    path = _require_regular_file(main_files[0], "Appshots desktop main bundle")
    text = _read_bounded_text(path, MAX_APPSHOTS_BUNDLE_BYTES, "Appshots desktop main bundle")
    strict_gate = 'process.env.CODEX_ROUTER_ENABLE_APPSHOTS==="1"'
    if text.count(strict_gate) != 2 or text.count("CODEX_ROUTER_ENABLE_APPSHOTS") != 2:
        raise RuntimeError(
            "Appshots must have exactly two strict opt-in gates for CODEX_ROUTER_ENABLE_APPSHOTS=1"
        )
    required = (
        "a.a.isInternal(i)||" + strict_gate,
        "s.appshotsEnabled=H.windowsCaptureNativeBridge!=null",
    )
    for marker in required:
        if marker not in text:
            raise RuntimeError(f"Appshots opt-in contract is missing {marker!r}")
    if "s.appshotsEnabled=!0" in text:
        raise RuntimeError("Appshots was enabled unconditionally")
    return {
        "qualification": "static-contract-only",
        "defaultEnabled": False,
        "optInEnvironment": "CODEX_ROUTER_ENABLE_APPSHOTS",
        "enabledValue": "1",
        "requiresNativeBridge": True,
    }


def _prepare_account_component(token: str, control_port: int) -> str:
    component = (PROJECT_ROOT / "ui" / "account-menu.js").read_text(encoding="utf-8")
    component = component.replace("__CODEX_MUX_CONTROL_PORT__", str(control_port))
    component = component.replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    component = replace_identifiers(
        component,
        {
            "e7": "p8",
            "kXc": "ibl",
            "QLs": "g6s",
            "Lo": "ds",
            "BW": "Tz",
            "_H": "UI",
            "CH": "YI",
            "jLa": "aza",
            "S2": "b1",
        },
    )
    old_methods = (
        '"list-apps",\n  "list-installed-apps",\n  "read-apps",\n'
        '  "list-mcp-server-status",\n  "login-mcp-server",'
    )
    new_methods = (
        '"app/list",\n  "app/installed",\n  "app/read",\n'
        '  "mcpServerStatus/list",\n  "mcpServer/oauth/login",'
    )
    if old_methods not in component:
        raise RuntimeError("account UI plugin-method allowlist changed unexpectedly")
    return component.replace(old_methods, new_methods, 1)


def patch_windows_renderer(extracted: Path, token: str, control_port: int) -> None:
    webview = extracted / "webview"
    index_path = webview / "index.html"
    index = index_path.read_text(encoding="utf-8")
    connect_anchor = "connect-src &#39;self&#39;"
    index = replace_unique(
        index,
        connect_anchor,
        f"{connect_anchor} http://127.0.0.1:{control_port}",
        "renderer CSP connect-src",
    )
    index_path.write_text(index, encoding="utf-8")

    initial_files = list((webview / "assets").glob("app-initial-*.js"))
    if len(initial_files) != 1:
        raise RuntimeError(f"expected one initial renderer bundle, found {len(initial_files)}")
    initial_path = initial_files[0]
    initial = initial_path.read_text(encoding="utf-8")
    if "function CodexMuxAccountMenu(" in initial:
        raise RuntimeError("source app already contains the Codex multiplexer UI")
    component_anchor = "function Qyl(e){let t=(0,rbl.c)(252),{sidebarFooter:n,triggerButton:r}=e"
    initial = replace_unique(
        initial,
        component_anchor,
        _prepare_account_component(token, control_port) + "\n" + component_anchor,
        "profile-menu component",
    )

    request_anchor = (
        "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error("
        "`AppServerRequestClient is missing a message dispatcher`);return e===`config/read`?"
        "this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,e===`plugin/list`&&"
        "n?.timeoutMs==null?{...n,timeoutMs:HLt}:n)}"
    )
    request_replacement = request_anchor.replace(
        "{if(this.dispatchMessage", "{t=codexMuxScopePluginRequest(e,t);if(this.dispatchMessage", 1
    )
    initial = replace_unique(initial, request_anchor, request_replacement, "app-server request bridge")

    initial = replace_unique(
        initial,
        "let e=await Ob.safeGet(`/wham/profiles/me`)",
        "let e=await codexMuxProfileData(globalThis.__codexMuxSelectedProfileAccountId??null)",
        "profile statistics request",
    )
    usage_anchor = (
        "function g6s(e){let t=(0,_6s.c)(20),{defaultResetCreditsOpen:n,"
        "initialAvailableCount:r,isRateLimitReached:i,onClose:a,onResetComplete:o}=e"
    )
    initial = replace_unique(
        initial,
        usage_anchor,
        usage_anchor + ";CodexMuxUseResetAccountState()",
        "usage reset modal",
    )
    reset_query_anchor = (
        "function WAa(){let e=(0,uH.c)(1),t;return e[0]===Symbol.for("
        "`react.memo_cache_sentinel`)?(t={queryKey:[`rate-limit-reset-credits`],"
        "queryFn:GAa,refetchInterval:nm.ONE_MINUTE,staleTime:nm.FIVE_SECONDS},"
        "e[0]=t):t=e[0],Lt(t)}"
    )
    reset_query_replacement = (
        "function WAa(){let e=window.__codexMuxResetAccountId;return Lt({"
        "queryKey:[`rate-limit-reset-credits`,e??`primary`],"
        "queryFn:e?()=>codexMuxRateLimitResets(e):GAa,"
        "refetchInterval:nm.ONE_MINUTE,staleTime:nm.FIVE_SECONDS})}"
    )
    initial = replace_unique(
        initial,
        reset_query_anchor,
        reset_query_replacement,
        "rate-limit reset query",
    )
    reset_mutation_anchor = (
        "function KAa(){let e=(0,uH.c)(3),t=lt(),n=AS(),r;return "
        "e[0]!==n||e[1]!==t?(r={mutationFn:qAa,onSuccess:(e,r)=>{"
        "let{creditId:i}=r,a=e.code;if(a===`reset`||a===`already_redeemed`){"
        "let n=e.code===`reset`?e.credit?.id??i:i;t.setQueryData("
        "[`rate-limit-reset-credits`],e=>gAa(e,a,n))}Promise.all(["
        "n([`rate-limit-status`]),n([`rate-limit-reset-credits`])])}},"
        "e[0]=n,e[1]=t,e[2]=r):r=e[2],$t(r)}"
    )
    reset_mutation_replacement = (
        "function KAa(){let e=lt(),t=AS(),n=window.__codexMuxResetAccountId,"
        "r=[`rate-limit-reset-credits`,n??`primary`];return $t({"
        "mutationFn:n?i=>codexMuxConsumeRateLimitReset(n,i):qAa,"
        "onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;"
        "if(o===`reset`||o===`already_redeemed`){let t=o===`reset`?"
        "n.credit?.id??a:a;e.setQueryData(r,e=>gAa(e,o,t))}"
        "Promise.all([t([`rate-limit-status`]),t(r)])}})}"
    )
    initial = replace_unique(
        initial,
        reset_mutation_anchor,
        reset_mutation_replacement,
        "rate-limit reset mutation",
    )
    initial = replace_unique(
        initial,
        "let y=v;if(g!=null){",
        "let y=window.__codexMuxSelectedUsageWindows??v;if(g!=null){",
        "usage-window selection",
    )
    header_anchor = (
        "let ve;t[46]===ge?ve=t[47]:(ve=(0,d1.jsxs)(bz,{children:[ge,_e]}),"
        "t[46]=ge,t[47]=ve);"
    )
    initial = replace_unique(
        initial,
        header_anchor,
        "let ve=(0,d1.jsxs)(bz,{children:[ge,_e,window.__codexMuxResetAccountSelector??null]});",
        "usage header",
    )
    initial = replace_unique(
        initial,
        "usageItems:wt",
        "usageItems:(0,p8.jsx)(CodexMuxAccountMenu,{})",
        "profile usage slot",
    )
    for anchor in (
        "sideOffset:6,triggerButton:Ot,onOpenChange:l,children:N",
        "open:s,onOpenChange:l,contentWidth:`panel`,triggerButton:Ot,children:zt",
    ):
        initial = replace_unique(
            initial,
            anchor,
            anchor.replace("onOpenChange:l", "onOpenChange:CodexMuxProfileMenuOpenChange(l)"),
            "profile menu open-state",
        )
    for depleted in (
        "defaultMessage:`You’re out of Codex and Work usage`",
        "defaultMessage:`You’ve used all Codex and Work usage`",
        "defaultMessage:`You’ve reached your usage limit`",
    ):
        initial = replace_unique(
            initial,
            depleted,
            "defaultMessage:`All connected subscriptions are depleted`",
            "subscription depletion message",
        )
    initial_path.write_text(initial, encoding="utf-8")

    profile_files = list((webview / "assets").glob("profile-*.js"))
    if len(profile_files) != 1:
        raise RuntimeError(f"expected one profile settings bundle, found {len(profile_files)}")
    profile_path = profile_files[0]
    profile = profile_path.read_text(encoding="utf-8")
    profile_anchor = (
        "let Yt;t[85]!==Kt||t[86]!==Jt?(Yt=(0,$.jsx)(`section`,{\"aria-busy\":Kt,"
        "className:`flex flex-col items-center`,children:Jt}),t[85]=Kt,t[86]=Jt,t[87]=Yt):Yt=t[87];"
    )
    profile_replacement = profile_anchor.replace(
        "children:Jt",
        "children:globalThis.CodexMuxProfileAvatarStack?.({onSelect:()=>j.refetch()})??Jt",
        1,
    )
    profile = replace_unique(profile, profile_anchor, profile_replacement, "profile avatar stack")
    profile_path.write_text(profile, encoding="utf-8")

    plugin_anchor = "action:F,children:w})"
    plugin_files = [
        path
        for path in (webview / "assets").glob("plugins-settings-*.js")
        if plugin_anchor in path.read_text(encoding="utf-8")
    ]
    if len(plugin_files) != 1:
        raise RuntimeError(f"expected one plugin settings content bundle, found {len(plugin_files)}")
    plugin_path = plugin_files[0]
    plugin = plugin_path.read_text(encoding="utf-8")
    plugin = replace_unique(
        plugin,
        plugin_anchor,
        "action:F,children:[globalThis.CodexMuxPluginScope?.()??null,w]})",
        "plugin account scope",
    )
    plugin_path.write_text(plugin, encoding="utf-8")

    thread_anchor = (
        "function mE(e){let t=(0,_E.c)(33),{onForceShow:n,isVisible:r,"
        "registerEnvironmentActionCommands:i,onOpenBackgroundAgent:a,"
        "onOpenPullRequestSidePanel:o,onOpenSubagentsPanel:s}=e"
    )
    thread_files = [
        path
        for path in (webview / "assets").glob("local-conversation-thread-*.js")
        if thread_anchor in path.read_text(encoding="utf-8")
    ]
    if len(thread_files) != 1:
        raise RuntimeError(f"expected one local conversation renderer bundle, found {len(thread_files)}")
    thread_path = thread_files[0]
    thread = thread_path.read_text(encoding="utf-8")
    thread_component = (PROJECT_ROOT / "ui" / "thread-subscription.js").read_text(
        encoding="utf-8"
    )
    thread_component = thread_component.replace("__CODEX_MUX_CONTROL_PORT__", str(control_port))
    thread_component = thread_component.replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    thread_component = replace_identifiers(
        thread_component,
        {"$n": "Rt", "sr": "P", "TE": "iE", "zE": "vE", "K": "W"},
    )
    thread = replace_unique(
        thread,
        thread_anchor,
        thread_component + "\n" + thread_anchor,
        "thread summary component",
    )
    sections_anchor = "children:[l,u,d,f,p,m,h,g,_,v,y,b,x,S,C]"
    thread = replace_unique(
        thread,
        sections_anchor,
        "children:[l,u,d,f,(0,vE.jsx)(CodexMuxThreadSubscription,{}),p,m,h,g,_,v,y,b,x,S,C]",
        "thread summary section list",
    )
    thread_path.write_text(thread, encoding="utf-8")


def patch_extracted_asar(extracted: Path, token: str, control_port: int) -> None:
    verify_windows_integration_isolation(extracted)
    patch_windows_bootstrap(extracted)
    patch_windows_runtime_paths(extracted)
    patch_windows_native_messaging_isolation(extracted)
    patch_windows_appshots_gate(extracted)
    verify_windows_appshots_contract(extracted)
    patch_windows_renderer(extracted, token, control_port)


def repack_asar(
    asar: Path,
    extracted: Path,
    output_path: Path,
    unpacked_files: tuple[str, ...],
) -> Path:
    if not unpacked_files:
        raise RuntimeError("source app.asar.unpacked contains no files")
    # Preserve the official file-level unpack set exactly.  Unpacking whole
    # package directories would silently move hundreds of ordinary JS files
    # out of ASAR and make the result layout diverge from the signed source.
    # asar matches --unpack against absolute crawled filenames, hence the
    # leading **/.  The brace body is the exact source-relative file set.
    unpack_expression = "**/{" + ",".join(
        path.replace("\\", "/") for path in unpacked_files
    ) + "}"
    run(
        [
            str(asar),
            "pack",
            "--unpack",
            unpack_expression,
            str(extracted),
            str(output_path),
        ]
    )
    unpacked = output_path.with_name(output_path.name + ".unpacked")
    if not output_path.is_file() or not unpacked.is_dir():
        raise RuntimeError("ASAR pack did not produce the archive and unpacked native tree")
    listing = output([str(asar), "list", "--is-pack", str(output_path)]).replace("\\", "/")
    for relative in REQUIRED_UNPACKED_FILES:
        marker = f"unpack : /{relative}"
        if marker not in listing:
            raise RuntimeError(f"native ASAR file was not kept unpacked: {relative}")
        if not (unpacked / Path(relative)).is_file():
            raise RuntimeError(f"native ASAR sidecar file is missing: {relative}")
    return unpacked


def install_repacked_asar(staged_app: Path, archive: Path, unpacked: Path) -> None:
    resources = staged_app / "resources"
    shutil.copy2(archive, resources / "app.asar")
    target_unpacked = resources / "app.asar.unpacked"
    if target_unpacked.exists():
        shutil.rmtree(target_unpacked)
    shutil.copytree(unpacked, target_unpacked)


def swap_executables(staged_app: Path, mux: Path, launcher: Path) -> None:
    desktop = staged_app / "ChatGPT.exe"
    real_desktop = staged_app / "ChatGPT.real.exe"
    resources = staged_app / "resources"
    codex = resources / "codex.exe"
    real_codex = resources / "codex.real.exe"
    for unexpected in (real_desktop, real_codex):
        if unexpected.exists():
            raise RuntimeError(f"staged source already contains {unexpected.name}")
    desktop.rename(real_desktop)
    shutil.copy2(launcher, desktop)
    codex.rename(real_codex)
    shutil.copy2(mux, codex)
    for path, label in (
        (desktop, "launcher"),
        (real_desktop, "official desktop executable"),
        (codex, "multiplexer"),
        (real_codex, "official Codex executable"),
    ):
        if not is_pe_executable(path):
            raise RuntimeError(f"{label} is not a PE executable after staging: {path}")


def verify_preserved_windows_resources(source_app: Path, staged_app: Path) -> dict[str, object]:
    source_resources = source_app / "resources"
    staged_resources = staged_app / "resources"
    official_codex = source_resources / "codex.exe"
    if sha256_file(staged_resources / "codex.real.exe") != sha256_file(official_codex):
        raise RuntimeError("official codex.exe changed while being preserved as codex.real.exe")

    helper_names = (
        "codex-code-mode-host.exe",
        "codex-command-runner.exe",
        "codex-windows-sandbox-setup.exe",
    )
    helper_hashes: dict[str, str] = {"codex.real.exe": sha256_file(official_codex)}
    for name in helper_names:
        source = source_resources / name
        staged = staged_resources / name
        if not source.is_file() or not staged.is_file():
            raise RuntimeError(f"required Windows CLI helper is missing: {name}")
        source_hash = sha256_file(source)
        if sha256_file(staged) != source_hash:
            raise RuntimeError(f"Windows CLI helper changed during staging: {name}")
        helper_hashes[name] = source_hash

    preserved_trees: dict[str, str] = {}
    for relative in ("cua_node", "native"):
        source_tree = tree_file_hashes(source_resources / relative)
        staged_tree = tree_file_hashes(staged_resources / relative)
        if source_tree != staged_tree:
            raise RuntimeError(
                f"resources/{relative} was not preserved byte-for-byte: "
                f"{tree_difference(source_tree, staged_tree)}"
            )
        preserved_trees[relative] = tree_digest(source_tree)

    source_unpacked = tree_file_hashes(source_resources / "app.asar.unpacked")
    staged_unpacked = tree_file_hashes(staged_resources / "app.asar.unpacked")
    if source_unpacked != staged_unpacked:
        raise RuntimeError(
            "native app.asar.unpacked payload changed during repack: "
            + tree_difference(source_unpacked, staged_unpacked)
        )
    preserved_trees["app.asar.unpacked"] = tree_digest(source_unpacked)
    computer_use = inspect_computer_use_contract(staged_resources)
    if computer_use["treeSha256"] != preserved_trees["cua_node"]:
        raise RuntimeError("Computer Use contract digest does not match the preserved resource tree")
    return {
        "cliHelpers": helper_hashes,
        "preservedResourceTrees": preserved_trees,
        "computerUse": computer_use,
    }


def write_build_manifest(
    staged_app: Path,
    source: SourceInfo,
    destination: Path,
    state_root: Path,
    mux: Path,
    launcher: Path,
    preservation: dict[str, object],
    backup_path: Path | None = None,
    control_port: int | None = None,
) -> dict[str, object]:
    selected_control_port = resolve_control_port(control_port)
    launcher_config = staged_app / "resources" / "codex-router" / "launcher-config.json"
    launcher_config.parent.mkdir(parents=True, exist_ok=True)
    launcher_config_temporary = launcher_config.with_name(
        f".{launcher_config.name}.{uuid.uuid4().hex}.tmp"
    )
    launcher_config_temporary.write_text(
        json.dumps(
            {
                "schemaVersion": 2,
                "stateRoot": str(state_root),
                "controlPort": selected_control_port,
            },
            indent=2,
            sort_keys=True,
        )
        + "\n",
        encoding="utf-8",
    )
    os.replace(launcher_config_temporary, launcher_config)

    manifest: dict[str, object] = {
        "schemaVersion": 2,
        "projectVersion": PROJECT_VERSION,
        "sourceVersion": source.package_version,
        "sourceAsarVersion": source.asar_version,
        "sourceBuild": source.asar_build,
        "sourcePackage": source.package_full_name,
        "sourcePath": str(source.app_root),
        "sourceAsarSha256": source.asar_sha256,
        "sourceCodexSha256": source.codex_sha256,
        "sourceChatGptSha256": source.chatgpt_sha256,
        "sourceCodexLauncherSha256": source.codex_launcher_sha256,
        "sourceWindowsAccountSha256": source.windows_account_sha256,
        "sourceSignerSubject": source.signer_subject,
        "sourceSignerThumbprint": source.signer_thumbprint,
        "patchedAsarSha256": sha256_file(staged_app / "resources" / "app.asar"),
        "muxSha256": sha256_file(mux),
        "launcherSha256": sha256_file(launcher),
        "createdAtUtc": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "destination": str(destination),
        "profilePath": str(state_root / "Profile"),
        "controlTokenPath": str(state_root / "control-token"),
        "runtimeCachePath": str(state_root / "runtime-cache"),
        "logsPath": str(state_root / "logs"),
        "launcherConfigPath": "resources/codex-router/launcher-config.json",
        "controlPort": selected_control_port,
        # This path is planned before the directory publication.  Therefore
        # the manifest and app payload cross the destination rename boundary
        # together; there is no post-publish window with stale rollback data.
        "backupPath": str(backup_path) if backup_path is not None else None,
        "preservation": preservation,
        "capabilityQualification": {
            "appshots": {
                "qualification": "static-contract-only",
                "defaultEnabled": False,
                "optInEnvironment": "CODEX_ROUTER_ENABLE_APPSHOTS",
                "enabledValue": "1",
                "requiresNativeBridge": True,
            },
            "computerUse": preservation.get(
                "computerUse", {"qualification": "not-evaluated"}
            ),
        },
        "windowsIntegrationIsolation": {
            "appUserModelId": "com.openai.codex.subscription-router",
            "displayName": PRODUCT_NAME,
            "appxManifestCopied": False,
            "officialProtocolRegistrationDisabled": True,
            "officialExplorerVerbRegistrationCopied": False,
        },
    }
    path = staged_app / BUILD_MANIFEST_NAME
    temporary = staged_app / f".{BUILD_MANIFEST_NAME}.{uuid.uuid4().hex}.tmp"
    temporary.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.replace(temporary, path)
    return manifest


def plan_backup_path(destination: Path) -> Path:
    backup_root = destination.parent / ".codex-subscription-router-backups"
    backup_directory = backup_root / (
        time.strftime("%Y%m%d-%H%M%S") + "-" + uuid.uuid4().hex[:8]
    )
    return backup_directory / destination.name


def atomic_install(
    staged_app: Path,
    destination: Path,
    *,
    force: bool,
    planned_backup: Path | None = None,
) -> Path | None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    if destination.exists() and not force:
        raise RuntimeError(f"destination exists: {destination} (pass --force for a recoverable backup)")

    backup: Path | None = None
    if destination.exists():
        backup = planned_backup or plan_backup_path(destination)
        expected_root = (destination.parent / ".codex-subscription-router-backups").resolve(
            strict=False
        )
        if backup.parent.parent.resolve(strict=False) != expected_root:
            raise RuntimeError("planned backup is outside the router backup root")
        backup.parent.mkdir(parents=True, exist_ok=False)
        destination.rename(backup)
    try:
        staged_app.rename(destination)
    except OSError:
        if destination.exists():
            failed = destination.parent / (
                f".{destination.name}.failed-{time.strftime('%Y%m%d-%H%M%S')}-{uuid.uuid4().hex[:8]}"
            )
            destination.rename(failed)
        if backup is not None and backup.exists():
            backup.rename(destination)
        raise
    return backup


def patch_app(
    source_path: Path | None,
    destination: Path,
    mux_path: Path | None,
    launcher_path: Path | None,
    *,
    force: bool,
    dry_run: bool,
    allow_untested_source: bool,
    control_port: int | None = None,
) -> dict[str, object]:
    selected_control_port = resolve_control_port(control_port)
    source_path = source_path or discover_appx_source()
    source = inspect_source(source_path)
    destination = destination.expanduser().resolve(strict=False)
    validate_source_destination(source.app_root, destination)
    validate_approved_source(source, allow_untested_source)
    if destination.exists() and not force:
        raise RuntimeError(f"destination exists: {destination} (pass --force for a recoverable backup)")

    print(
        f"Source: {source.package_full_name}; app {source.asar_version} build {source.asar_build}; "
        f"app.asar {source.asar_sha256}"
    )
    asar = ensure_asar_tool()
    state_root = resolve_state_root()
    token, token_is_new = prepare_control_token(state_root)
    destination.parent.mkdir(parents=True, exist_ok=True)

    token_persisted = False
    state_root_preexisted = state_root.exists()
    with tempfile.TemporaryDirectory(
        # @electron/asar's minimatch defaults do not traverse a leading-dot
        # staging segment for the exact **/{...} unpack pattern.
        prefix="codex-subscription-router-staging-", dir=destination.parent
    ) as temporary:
        temporary_path = Path(temporary)
        staged_app = temporary_path / destination.name
        extracted = temporary_path / "asar"
        repacked = temporary_path / "app.asar"
        mux = prepare_executable(
            mux_path,
            temporary_path / "codex-mux.exe",
            package="./cmd/codex-mux",
            description="Codex multiplexer",
        )
        launcher = prepare_executable(
            launcher_path,
            temporary_path / "ChatGPT-launcher.exe",
            package="./cmd/windows-launcher",
            description="Windows desktop launcher",
            windows_gui=True,
        )

        print("Copying the official app into isolated staging…")
        shutil.copytree(source.app_root, staged_app, symlinks=False)
        if (staged_app / "AppxManifest.xml").exists():
            raise RuntimeError("staging unexpectedly contains the official Appx manifest")
        patch_owl_config(staged_app)
        run([str(asar), "extract", str(staged_app / "resources" / "app.asar"), str(extracted)])
        print("Validating and patching ASAR anchors…")
        patch_extracted_asar(extracted, token, selected_control_port)
        official_unpacked_files = tuple(
            tree_file_hashes(staged_app / "resources" / "app.asar.unpacked").keys()
        )
        unpacked = repack_asar(asar, extracted, repacked, official_unpacked_files)
        install_repacked_asar(staged_app, repacked, unpacked)
        swap_executables(staged_app, mux, launcher)
        preservation = verify_preserved_windows_resources(source.app_root, staged_app)
        planned_backup = (
            plan_backup_path(destination) if destination.exists() and not dry_run else None
        )
        manifest = write_build_manifest(
            staged_app,
            source,
            destination,
            state_root,
            mux,
            launcher,
            preservation,
            planned_backup,
            selected_control_port,
        )

        if dry_run:
            manifest["dryRun"] = True
            print("Dry run completed; destination and state were not changed.")
            return manifest

        try:
            if token_is_new:
                token_persisted = persist_control_token(state_root, token)
            backup = atomic_install(
                staged_app, destination, force=force, planned_backup=planned_backup
            )
        except BaseException:
            if token_persisted:
                try:
                    (state_root / "control-token").unlink()
                    if not state_root_preexisted:
                        state_root.rmdir()
                except OSError:
                    pass
            raise

    print(f"Installed: {destination}")
    if backup is not None:
        print(f"Previous build moved to: {backup}")
    return manifest


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    if os.name != "nt":
        print("patch failed: this patcher must run on Windows", file=sys.stderr)
        return 1
    try:
        patch_app(
            args.source,
            args.destination,
            args.mux,
            args.launcher,
            force=args.force,
            dry_run=args.dry_run,
            allow_untested_source=args.allow_untested_source,
            control_port=args.control_port,
        )
    except (RuntimeError, OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"patch failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
