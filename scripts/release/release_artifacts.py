#!/usr/bin/env python3
"""Build and verify deterministic, source-only release artifacts.

The public release boundary is the selected Git commit. Files are read from Git
objects instead of the working tree so line-ending conversion, ignored build
output, and local account state cannot leak into an archive.
"""

from __future__ import annotations

import base64
import datetime as dt
import gzip
import hashlib
import json
import os
import platform
import re
import subprocess
import tarfile
import tempfile
import urllib.parse
import uuid
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Any, Iterable, Sequence


PROJECT_NAME = "codex-subscription-router-windows"
PROJECT_REPOSITORY = "https://github.com/TheDaniXSX/codex-subscription-router-windows"
BUILD_TYPE = f"{PROJECT_REPOSITORY}/source-release/v1"
SUPPORTING_E2E_REPORT = "docs/E2E-REPORT-WINDOWS.md"

FORBIDDEN_NAMES = {
    ".env",
    "auth.json",
    "control-token",
    "credentials.json",
    "state.json",
}
FORBIDDEN_SUFFIXES = {
    ".7z",
    ".a",
    ".app",
    ".appinstaller",
    ".appx",
    ".appxbundle",
    ".appxsym",
    ".appxupload",
    ".asar",
    ".cer",
    ".crt",
    ".dmp",
    ".dll",
    ".dmg",
    ".dylib",
    ".exe",
    ".gz",
    ".key",
    ".lib",
    ".mobileprovision",
    ".msi",
    ".msix",
    ".msixbundle",
    ".msixupload",
    ".node",
    ".nupkg",
    ".o",
    ".obj",
    ".p12",
    ".pem",
    ".pfx",
    ".pkg",
    ".provisionprofile",
    ".rar",
    ".so",
    ".tar",
    ".wasm",
    ".zip",
}
FORBIDDEN_PATH_PARTS = {
    "codex subscription router data",
    "openai.codex_2p2nqsd0c76g0",
    "windowsapps",
}
SECRET_PATTERNS = (
    re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
    re.compile(rb"\bghp_[A-Za-z0-9]{20,}\b"),
    re.compile(rb"\bgithub_pat_[A-Za-z0-9_]{20,}\b"),
    re.compile(rb"\bsk-[A-Za-z0-9_-]{20,}\b"),
    re.compile(rb"(?im)^\s*(?:_authToken|password)\s*=\s*[^${\s][^\r\n]*$"),
)
MAX_SOURCE_FILE_SIZE = 10 * 1024 * 1024
HASH_LINE = re.compile(r"^([0-9a-f]{64})  ([0-9]+)  ([^\\]+)$")


class ReleaseError(RuntimeError):
    """A release artifact did not meet the source-only contract."""


@dataclass(frozen=True)
class SourceFile:
    path: str
    data: bytes
    executable: bool


@dataclass(frozen=True)
class BuildContext:
    source_root: Path
    version: str
    commit: str
    source_epoch: int
    archive_root: str


def _run(
    args: Sequence[str],
    *,
    cwd: Path,
    text: bool = True,
) -> str | bytes:
    try:
        return subprocess.check_output(
            list(args), cwd=cwd, stderr=subprocess.PIPE, text=text
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        detail = ""
        if isinstance(exc, subprocess.CalledProcessError) and exc.stderr:
            detail_value = exc.stderr
            if isinstance(detail_value, bytes):
                detail_value = detail_value.decode("utf-8", errors="replace")
            detail = f": {detail_value.strip()}"
        raise ReleaseError(f"command failed: {' '.join(args)}{detail}") from exc


def _json_bytes(value: Any) -> bytes:
    return (
        json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
    ).encode("utf-8")


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _rfc3339(epoch: int) -> str:
    return (
        dt.datetime.fromtimestamp(epoch, tz=dt.timezone.utc)
        .isoformat()
        .replace("+00:00", "Z")
    )


def _normalized_path(value: str) -> str:
    if "\\" in value:
        raise ReleaseError(f"non-portable backslash in release path: {value}")
    path = PurePosixPath(value)
    if not value or path.is_absolute() or ".." in path.parts or "." in path.parts:
        raise ReleaseError(f"unsafe release path: {value!r}")
    normalized = path.as_posix()
    if normalized != value:
        raise ReleaseError(f"non-canonical release path: {value!r}")
    return normalized


def scan_source_file(relative_path: str, data: bytes) -> None:
    """Reject official/composite payloads, local state, and obvious secrets."""

    normalized = _normalized_path(relative_path)
    path = PurePosixPath(normalized)
    name = path.name.lower()
    suffix = path.suffix.lower()
    parts = {part.lower() for part in path.parts}

    if name in FORBIDDEN_NAMES or name.startswith(".env."):
        raise ReleaseError(f"local state or credential file is forbidden: {normalized}")
    if (
        (name.startswith("credentials") or name.startswith("secrets"))
        and suffix == ".json"
    ) or suffix == ".token":
        raise ReleaseError(f"local state or credential file is forbidden: {normalized}")
    if suffix in FORBIDDEN_SUFFIXES:
        raise ReleaseError(
            f"binary, credential, or composite payload is forbidden: {normalized}"
        )
    if parts & FORBIDDEN_PATH_PARTS or any(
        part.startswith("openai.codex_") for part in parts
    ):
        raise ReleaseError(
            f"installed application/data path is forbidden: {normalized}"
        )
    if len(data) > MAX_SOURCE_FILE_SIZE:
        raise ReleaseError(
            f"source file exceeds {MAX_SOURCE_FILE_SIZE} bytes: {normalized}"
        )
    for pattern in SECRET_PATTERNS:
        if pattern.search(data):
            raise ReleaseError(
                f"probable secret or private key in source: {normalized}"
            )
    executable_magic = (
        b"MZ",
        b"\x7fELF",
        b"\xca\xfe\xba\xbe",
        b"\xce\xfa\xed\xfe",
        b"\xcf\xfa\xed\xfe",
        b"\xfe\xed\xfa\xce",
        b"\xfe\xed\xfa\xcf",
    )
    if data.startswith(executable_magic):
        raise ReleaseError(f"executable payload magic is forbidden: {normalized}")
    if data.startswith(b"PK\x03\x04"):
        raise ReleaseError(f"embedded ZIP payload is forbidden: {normalized}")


def _git_source_files(source_root: Path, revision: str) -> list[SourceFile]:
    raw = _run(["git", "ls-tree", "-r", "-z", revision], cwd=source_root, text=False)
    assert isinstance(raw, bytes)
    files: list[SourceFile] = []
    for entry in raw.split(b"\0"):
        if not entry:
            continue
        metadata, raw_path = entry.split(b"\t", 1)
        mode, object_type, object_id = metadata.decode("ascii").split(" ")
        path = raw_path.decode("utf-8")
        if object_type != "blob" or mode not in {"100644", "100755"}:
            raise ReleaseError(f"unsupported Git entry {mode} {object_type}: {path}")
        data = _run(["git", "cat-file", "blob", object_id], cwd=source_root, text=False)
        assert isinstance(data, bytes)
        scan_source_file(path, data)
        files.append(SourceFile(path=path, data=data, executable=mode == "100755"))
    files.sort(key=lambda item: item.path.encode("utf-8"))
    if not files:
        raise ReleaseError("selected Git revision has no source files")
    folded_paths = [item.path.casefold() for item in files]
    if len(folded_paths) != len(set(folded_paths)):
        raise ReleaseError("selected Git revision contains case-colliding paths")
    return files


def _git_blob(source_root: Path, revision: str, path: str) -> bytes:
    _normalized_path(path)
    value = _run(["git", "show", f"{revision}:{path}"], cwd=source_root, text=False)
    assert isinstance(value, bytes)
    return value


def _context(source_root: Path, *, allow_dirty: bool) -> BuildContext:
    source_root = source_root.resolve()
    commit_value = _run(["git", "rev-parse", "HEAD"], cwd=source_root)
    assert isinstance(commit_value, str)
    commit = commit_value.strip()
    if re.fullmatch(r"[0-9a-f]{40}", commit) is None:
        raise ReleaseError(f"invalid Git commit: {commit!r}")

    status = _run(
        ["git", "status", "--porcelain=v1", "--untracked-files=all"],
        cwd=source_root,
    )
    assert isinstance(status, str)
    if status.strip() and not allow_dirty:
        raise ReleaseError("working tree is dirty; commit or remove all changes first")

    try:
        version = (source_root / "VERSION").read_text(encoding="utf-8").strip()
    except OSError as exc:
        raise ReleaseError("VERSION is missing from the working tree") from exc
    if re.fullmatch(r"\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", version) is None:
        raise ReleaseError(f"VERSION is not semantic: {version!r}")
    committed_version = _run(["git", "show", f"{commit}:VERSION"], cwd=source_root)
    assert isinstance(committed_version, str)
    if committed_version.strip() != version:
        raise ReleaseError("working-tree VERSION does not match the selected commit")

    timestamp_value = _run(
        ["git", "show", "-s", "--format=%ct", commit], cwd=source_root
    )
    assert isinstance(timestamp_value, str)
    commit_epoch = int(timestamp_value.strip())
    epoch_text = os.environ.get("SOURCE_DATE_EPOCH")
    if epoch_text is not None and int(epoch_text) != commit_epoch:
        raise ReleaseError("SOURCE_DATE_EPOCH must equal the selected commit timestamp")
    source_epoch = commit_epoch
    if not 0 <= source_epoch <= 0xFFFFFFFF:
        raise ReleaseError("commit timestamp is outside the reproducible gzip range")

    return BuildContext(
        source_root=source_root,
        version=version,
        commit=commit,
        source_epoch=source_epoch,
        archive_root=f"{PROJECT_NAME}-v{version}",
    )


def source_gate_document(context: BuildContext) -> dict[str, Any]:
    """Create deterministic automated-gate evidence for the selected CI commit.

    The tracked E2E report is supporting context, not the authority for this
    status. CI must call this only after all required jobs for the exact commit
    succeeded; the GitHub attestation records that trusted execution context.
    """

    report_data = _git_blob(context.source_root, context.commit, SUPPORTING_E2E_REPORT)
    try:
        report_data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise ReleaseError("supporting E2E report is not UTF-8") from exc
    return {
        "evidence": {
            "path": SUPPORTING_E2E_REPORT,
            "sha256": _sha256(report_data),
        },
        "project": PROJECT_NAME,
        "schemaVersion": 1,
        "scope": "source-gates",
        "sourceCommit": context.commit,
        "status": "AUTOMATED_GATES_PASSED",
        "version": context.version,
    }


def validate_source_gates(path: Path, context: BuildContext) -> bytes:
    try:
        value = json.loads(path.read_bytes())
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ReleaseError(f"cannot read source-gate JSON: {exc}") from exc
    expected = source_gate_document(context)
    if _json_bytes(value) != _json_bytes(expected):
        raise ReleaseError(
            "source-gate JSON does not exactly match the gated commit/report"
        )
    return _json_bytes(expected)


def create_source_gate_evidence(
    source_root: Path, output_path: Path, *, allow_dirty: bool = False
) -> Path:
    context = _context(source_root, allow_dirty=allow_dirty)
    output_path = output_path.resolve()
    if output_path.exists():
        raise ReleaseError(f"source-gate output already exists: {output_path}")
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with output_path.open("xb") as output:
        output.write(_json_bytes(source_gate_document(context)))
    return output_path


def _write_tar_gz(
    path: Path, files: Iterable[SourceFile], context: BuildContext
) -> None:
    with path.open("wb") as raw_output:
        with gzip.GzipFile(
            filename="",
            mode="wb",
            compresslevel=9,
            fileobj=raw_output,
            mtime=context.source_epoch,
        ) as gzip_output:
            with tarfile.open(
                fileobj=gzip_output, mode="w", format=tarfile.PAX_FORMAT
            ) as archive:
                for source in files:
                    info = tarfile.TarInfo(f"{context.archive_root}/{source.path}")
                    info.size = len(source.data)
                    info.mtime = context.source_epoch
                    info.mode = 0o755 if source.executable else 0o644
                    info.uid = 0
                    info.gid = 0
                    info.uname = "root"
                    info.gname = "root"
                    info.pax_headers = {}
                    with tempfile.SpooledTemporaryFile() as stream:
                        stream.write(source.data)
                        stream.seek(0)
                        archive.addfile(info, stream)


def _npm_components(source_root: Path) -> list[dict[str, Any]]:
    lock = json.loads((source_root / "package-lock.json").read_text(encoding="utf-8"))
    components: list[dict[str, Any]] = []
    for lock_path, package in sorted(lock.get("packages", {}).items()):
        if not lock_path or "node_modules/" not in lock_path:
            continue
        name = lock_path.rsplit("node_modules/", 1)[-1]
        version = package.get("version")
        if not isinstance(version, str):
            raise ReleaseError(f"npm lock entry has no version: {lock_path}")
        purl_name = urllib.parse.quote(name, safe="/")
        purl = f"pkg:npm/{purl_name}@{urllib.parse.quote(version, safe='')}"
        component: dict[str, Any] = {
            "bom-ref": f"{purl}?path={urllib.parse.quote(lock_path, safe='')}",
            "name": name,
            "purl": purl,
            "scope": "optional" if package.get("optional") else "required",
            "type": "library",
            "version": version,
        }
        license_value = package.get("license")
        if isinstance(license_value, str) and license_value:
            component["licenses"] = [{"license": {"id": license_value}}]
        integrity = package.get("integrity")
        if isinstance(integrity, str) and integrity.startswith("sha512-"):
            try:
                raw_sha512 = base64.b64decode(integrity[7:], validate=True)
                if len(raw_sha512) != 64:
                    raise ValueError("SHA-512 integrity has the wrong byte length")
                sha512 = raw_sha512.hex()
                component["hashes"] = [{"alg": "SHA-512", "content": sha512}]
            except ValueError as exc:
                raise ReleaseError(f"invalid npm integrity for {lock_path}") from exc
        components.append(component)
    return components


def _go_components(source_root: Path) -> list[dict[str, Any]]:
    go_mod = (source_root / "go.mod").read_text(encoding="utf-8")
    requirements: list[tuple[str, str]] = []
    in_require = False
    for raw_line in go_mod.splitlines():
        line = raw_line.split("//", 1)[0].strip()
        if line == "require (":
            in_require = True
            continue
        if in_require and line == ")":
            in_require = False
            continue
        if line.startswith("require "):
            fields = line.removeprefix("require ").split()
        elif in_require:
            fields = line.split()
        else:
            continue
        if len(fields) >= 2:
            requirements.append((fields[0], fields[1]))

    sums: dict[tuple[str, str], str] = {}
    go_sum_path = source_root / "go.sum"
    if go_sum_path.exists():
        for line in go_sum_path.read_text(encoding="utf-8").splitlines():
            fields = line.split()
            if len(fields) == 3 and not fields[1].endswith("/go.mod"):
                sums[(fields[0], fields[1])] = fields[2]

    components = []
    for name, version in sorted(set(requirements)):
        purl = f"pkg:golang/{urllib.parse.quote(name, safe='/')}@{urllib.parse.quote(version, safe='')}"
        component: dict[str, Any] = {
            "bom-ref": purl,
            "name": name,
            "purl": purl,
            "scope": "required",
            "type": "library",
            "version": version,
        }
        if (name, version) in sums:
            component["properties"] = [
                {"name": "go:moduleChecksum", "value": sums[(name, version)]}
            ]
        components.append(component)
    return components


def _cyclonedx(context: BuildContext) -> dict[str, Any]:
    root_ref = f"pkg:github/TheDaniXSX/{PROJECT_NAME}@{context.version}"
    components = _npm_components(context.source_root) + _go_components(
        context.source_root
    )
    components.sort(key=lambda item: item["bom-ref"])
    serial = uuid.uuid5(
        uuid.NAMESPACE_URL, f"{PROJECT_REPOSITORY}@{context.commit}:cyclonedx"
    )
    return {
        "$schema": "https://cyclonedx.org/schema/bom-1.5.schema.json",
        "bomFormat": "CycloneDX",
        "components": components,
        "metadata": {
            "component": {
                "bom-ref": root_ref,
                "licenses": [{"license": {"id": "MIT"}}],
                "name": PROJECT_NAME,
                "purl": root_ref,
                "type": "application",
                "version": context.version,
            },
            "properties": [
                {"name": "source:commit", "value": context.commit},
                {"name": "source:repository", "value": PROJECT_REPOSITORY},
            ],
            "timestamp": _rfc3339(context.source_epoch),
            "tools": {
                "components": [
                    {
                        "name": "release_artifacts.py",
                        "type": "application",
                        "version": "1",
                    },
                    {
                        "name": "Python",
                        "type": "application",
                        "version": platform.python_version(),
                    },
                    {
                        "name": "Go",
                        "type": "application",
                        "version": _tool_version(
                            ["go", "version"], context.source_root
                        ),
                    },
                    {
                        "name": "Node.js",
                        "type": "application",
                        "version": _tool_version(
                            ["node", "--version"], context.source_root
                        ),
                    },
                ]
            },
        },
        "serialNumber": f"urn:uuid:{serial}",
        "specVersion": "1.5",
        "version": 1,
    }


def _spdx_id(value: str) -> str:
    cleaned = re.sub(r"[^A-Za-z0-9.-]+", "-", value).strip("-")
    return f"SPDXRef-{cleaned}"


def _spdx(context: BuildContext, cyclonedx: dict[str, Any]) -> dict[str, Any]:
    document_id = "SPDXRef-DOCUMENT"
    root_id = "SPDXRef-Package-Root"
    packages: list[dict[str, Any]] = [
        {
            "SPDXID": root_id,
            "copyrightText": "NOASSERTION",
            "downloadLocation": f"git+{PROJECT_REPOSITORY}.git@{context.commit}",
            "filesAnalyzed": False,
            "licenseConcluded": "MIT",
            "licenseDeclared": "MIT",
            "name": PROJECT_NAME,
            "supplier": "Organization: TheDaniXSX",
            "versionInfo": context.version,
        }
    ]
    relationships: list[dict[str, str]] = [
        {
            "relatedSpdxElement": root_id,
            "relationshipType": "DESCRIBES",
            "spdxElementId": document_id,
        }
    ]
    used_ids = {document_id, root_id}
    for index, component in enumerate(cyclonedx["components"], start=1):
        package_id = _spdx_id(
            f"Dependency-{index}-{component['name']}-{component['version']}"
        )
        if package_id in used_ids:
            raise ReleaseError(f"duplicate SPDX identifier: {package_id}")
        used_ids.add(package_id)
        license_entries = component.get("licenses", [])
        license_id = "NOASSERTION"
        if license_entries:
            license_id = license_entries[0].get("license", {}).get("id", "NOASSERTION")
        package: dict[str, Any] = {
            "SPDXID": package_id,
            "copyrightText": "NOASSERTION",
            "downloadLocation": "NOASSERTION",
            "externalRefs": [
                {
                    "referenceCategory": "PACKAGE-MANAGER",
                    "referenceLocator": component["purl"],
                    "referenceType": "purl",
                }
            ],
            "filesAnalyzed": False,
            "licenseConcluded": "NOASSERTION",
            "licenseDeclared": license_id,
            "name": component["name"],
            "versionInfo": component["version"],
        }
        hashes = component.get("hashes", [])
        if hashes:
            package["checksums"] = [
                {
                    "algorithm": item["alg"].replace("-", ""),
                    "checksumValue": item["content"],
                }
                for item in hashes
            ]
        packages.append(package)
        relationships.append(
            {
                "relatedSpdxElement": root_id,
                "relationshipType": "BUILD_DEPENDENCY_OF",
                "spdxElementId": package_id,
            }
        )
    return {
        "SPDXID": document_id,
        "creationInfo": {
            "created": _rfc3339(context.source_epoch),
            "creators": ["Tool: release_artifacts.py-1"],
        },
        "dataLicense": "CC0-1.0",
        "documentDescribes": [root_id],
        "documentNamespace": (
            f"{PROJECT_REPOSITORY}/releases/spdx/{context.version}/{context.commit}"
        ),
        "name": f"{PROJECT_NAME}-{context.version}",
        "packages": packages,
        "relationships": relationships,
        "spdxVersion": "SPDX-2.3",
    }


def _tool_version(args: Sequence[str], cwd: Path) -> str:
    try:
        value = _run(args, cwd=cwd)
        assert isinstance(value, str)
        return value.strip().splitlines()[0]
    except ReleaseError:
        return "unavailable"


def _provenance(
    context: BuildContext, subjects: Sequence[tuple[str, bytes]]
) -> dict[str, Any]:
    timestamp = _rfc3339(context.source_epoch)
    return {
        "_type": "https://in-toto.io/Statement/v1",
        "predicate": {
            "buildDefinition": {
                "buildType": BUILD_TYPE,
                "externalParameters": {
                    "automatedGateStatus": "AUTOMATED_GATES_PASSED",
                    "evidenceScope": "source-gates",
                    "sourceArchiveRoot": context.archive_root,
                    "sourceDateEpoch": context.source_epoch,
                    "version": context.version,
                },
                "internalParameters": {
                    "go": _tool_version(["go", "version"], context.source_root),
                    "node": _tool_version(["node", "--version"], context.source_root),
                    "python": platform.python_version(),
                },
                "resolvedDependencies": [
                    {
                        "digest": {"gitCommit": context.commit},
                        "uri": f"git+{PROJECT_REPOSITORY}.git",
                    }
                ],
            },
            "runDetails": {
                "builder": {
                    "id": (
                        f"{PROJECT_REPOSITORY}/blob/{context.commit}/"
                        "scripts/release/build_source_release.py"
                    ),
                    "version": {"script": "1"},
                },
                "metadata": {"finishedOn": timestamp, "startedOn": timestamp},
            },
        },
        "predicateType": "https://slsa.dev/provenance/v1",
        "subject": [
            {"digest": {"sha256": _sha256(data)}, "name": name}
            for name, data in sorted(subjects)
        ],
    }


def _manifest_bytes(artifacts: Sequence[tuple[str, bytes]]) -> bytes:
    lines = [
        f"{_sha256(data)}  {len(data)}  {_normalized_path(name)}"
        for name, data in sorted(artifacts, key=lambda item: item[0].encode("utf-8"))
    ]
    return ("\n".join(lines) + "\n").encode("utf-8")


def build_release(
    source_root: Path,
    output_dir: Path,
    *,
    source_gates_path: Path,
    allow_dirty: bool = False,
) -> list[Path]:
    context = _context(source_root, allow_dirty=allow_dirty)
    files = _git_source_files(context.source_root, context.commit)
    source_gates_data = validate_source_gates(source_gates_path, context)
    output_dir = output_dir.resolve()
    output_dir.mkdir(parents=True, exist_ok=True)
    base = f"{PROJECT_NAME}-v{context.version}"
    expected_names = {
        f"{base}.tar.gz",
        f"{base}.cdx.json",
        f"{base}.spdx.json",
        f"{base}.provenance.json",
        f"{base}.source-gates.json",
        "SHA256SUMS",
    }
    existing = list(output_dir.iterdir())
    if existing:
        raise ReleaseError("output directory must be empty")

    archive_path = output_dir / f"{base}.tar.gz"
    _write_tar_gz(archive_path, files, context)
    archive_data = archive_path.read_bytes()
    cyclonedx_data = _json_bytes(_cyclonedx(context))
    spdx_data = _json_bytes(_spdx(context, json.loads(cyclonedx_data)))
    cdx_name = f"{base}.cdx.json"
    spdx_name = f"{base}.spdx.json"
    source_gates_name = f"{base}.source-gates.json"
    initial_artifacts = [
        (archive_path.name, archive_data),
        (cdx_name, cyclonedx_data),
        (spdx_name, spdx_data),
        (source_gates_name, source_gates_data),
    ]
    provenance_name = f"{base}.provenance.json"
    provenance_data = _json_bytes(_provenance(context, initial_artifacts))
    manifest_data = _manifest_bytes(
        [*initial_artifacts, (provenance_name, provenance_data)]
    )

    values = {
        cdx_name: cyclonedx_data,
        spdx_name: spdx_data,
        source_gates_name: source_gates_data,
        provenance_name: provenance_data,
        "SHA256SUMS": manifest_data,
    }
    for name, data in values.items():
        (output_dir / name).write_bytes(data)
    return [output_dir / name for name in sorted(expected_names)]


def _parse_manifest(data: bytes) -> dict[str, tuple[str, int]]:
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise ReleaseError("SHA256SUMS is not UTF-8") from exc
    entries: dict[str, tuple[str, int]] = {}
    paths: list[str] = []
    for line in text.splitlines():
        match = HASH_LINE.fullmatch(line)
        if match is None:
            raise ReleaseError(f"invalid SHA256SUMS line: {line!r}")
        digest, size_text, name = match.groups()
        _normalized_path(name)
        if name in entries:
            raise ReleaseError(f"duplicate SHA256SUMS entry: {name}")
        entries[name] = (digest, int(size_text))
        paths.append(name)
    if paths != sorted(paths, key=lambda value: value.encode("utf-8")):
        raise ReleaseError("SHA256SUMS entries are not sorted by normalized path")
    if not entries:
        raise ReleaseError("SHA256SUMS is empty")
    return entries


def _verify_archive(path: Path, context: BuildContext) -> None:
    header = path.read_bytes()[:10]
    if len(header) != 10 or header[:2] != b"\x1f\x8b" or header[2] != 8:
        raise ReleaseError("source archive is not gzip data")
    if header[3] != 0:
        raise ReleaseError("gzip header contains optional non-deterministic fields")
    if int.from_bytes(header[4:8], "little") != context.source_epoch:
        raise ReleaseError("gzip timestamp does not match SOURCE_DATE_EPOCH")
    if header[8] != 2 or header[9] != 255:
        raise ReleaseError("gzip compression/platform metadata is not canonical")

    expected_sources = {
        source.path: source
        for source in _git_source_files(context.source_root, context.commit)
    }
    seen_sources: set[str] = set()
    with tarfile.open(path, mode="r:gz") as archive:
        members = archive.getmembers()
        names = [member.name for member in members]
        if names != sorted(names, key=lambda value: value.encode("utf-8")):
            raise ReleaseError("source archive members are not sorted")
        if not names:
            raise ReleaseError("source archive is empty")
        for member in members:
            _normalized_path(member.name)
            prefix = f"{context.archive_root}/"
            if not member.name.startswith(prefix):
                raise ReleaseError(
                    f"archive member is outside expected root: {member.name}"
                )
            relative = member.name[len(prefix) :]
            if not member.isfile():
                raise ReleaseError(
                    f"non-regular archive member is forbidden: {member.name}"
                )
            if (
                member.uid != 0
                or member.gid != 0
                or member.uname != "root"
                or member.gname != "root"
            ):
                raise ReleaseError(f"non-deterministic owner metadata: {member.name}")
            if member.mtime != context.source_epoch:
                raise ReleaseError(f"non-deterministic timestamp: {member.name}")
            if member.mode not in {0o644, 0o755}:
                raise ReleaseError(f"unexpected archive mode: {member.name}")
            stream = archive.extractfile(member)
            if stream is None:
                raise ReleaseError(f"cannot read archive member: {member.name}")
            data = stream.read()
            scan_source_file(relative, data)
            expected = expected_sources.get(relative)
            if expected is None:
                raise ReleaseError(
                    f"archive contains a file outside the commit: {relative}"
                )
            if data != expected.data:
                raise ReleaseError(
                    f"archive content differs from the commit: {relative}"
                )
            expected_mode = 0o755 if expected.executable else 0o644
            if member.mode != expected_mode:
                raise ReleaseError(f"archive mode differs from the commit: {relative}")
            seen_sources.add(relative)
    missing_sources = set(expected_sources) - seen_sources
    if missing_sources:
        raise ReleaseError(
            "archive omits committed source: " + ", ".join(sorted(missing_sources))
        )


def _verify_sboms(
    context: BuildContext, cyclonedx: dict[str, Any], spdx: dict[str, Any]
) -> None:
    if (
        cyclonedx.get("bomFormat") != "CycloneDX"
        or cyclonedx.get("specVersion") != "1.5"
    ):
        raise ReleaseError("CycloneDX document is not version 1.5")
    component = cyclonedx.get("metadata", {}).get("component", {})
    if (
        component.get("name") != PROJECT_NAME
        or component.get("version") != context.version
    ):
        raise ReleaseError("CycloneDX root component does not match the release")
    refs = [item.get("bom-ref") for item in cyclonedx.get("components", [])]
    if not all(isinstance(value, str) and value for value in refs) or len(refs) != len(
        set(refs)
    ):
        raise ReleaseError("CycloneDX component references are missing or duplicated")
    if refs != sorted(refs):
        raise ReleaseError("CycloneDX components are not sorted")

    if spdx.get("spdxVersion") != "SPDX-2.3" or spdx.get("dataLicense") != "CC0-1.0":
        raise ReleaseError("SPDX document is not SPDX 2.3 JSON")
    packages = spdx.get("packages", [])
    if not packages or packages[0].get("name") != PROJECT_NAME:
        raise ReleaseError("SPDX root package does not match the release")
    ids = [item.get("SPDXID") for item in packages]
    if not all(
        isinstance(value, str) and value.startswith("SPDXRef-") for value in ids
    ):
        raise ReleaseError("SPDX package identifiers are invalid")
    if len(ids) != len(set(ids)):
        raise ReleaseError("SPDX package identifiers are duplicated")


def verify_release(source_root: Path, input_dir: Path) -> list[Path]:
    context = _context(source_root, allow_dirty=True)
    input_dir = input_dir.resolve()
    manifest_path = input_dir / "SHA256SUMS"
    if not manifest_path.is_file():
        raise ReleaseError("SHA256SUMS is missing")
    entries = _parse_manifest(manifest_path.read_bytes())
    expected_base = f"{PROJECT_NAME}-v{context.version}"
    expected_names = {
        f"{expected_base}.tar.gz",
        f"{expected_base}.cdx.json",
        f"{expected_base}.spdx.json",
        f"{expected_base}.provenance.json",
        f"{expected_base}.source-gates.json",
    }
    if set(entries) != expected_names:
        missing = sorted(expected_names - set(entries))
        extra = sorted(set(entries) - expected_names)
        raise ReleaseError(f"unexpected artifact set; missing={missing}, extra={extra}")
    children = list(input_dir.iterdir())
    if any(not item.is_file() for item in children):
        raise ReleaseError("release directory contains a non-file entry")
    actual_files = {item.name for item in children}
    if actual_files != expected_names | {"SHA256SUMS"}:
        raise ReleaseError("release directory contains unmanifested or missing files")

    data_by_name: dict[str, bytes] = {}
    for name, (expected_digest, expected_size) in entries.items():
        data = (input_dir / name).read_bytes()
        if len(data) != expected_size or _sha256(data) != expected_digest:
            raise ReleaseError(f"artifact hash/size mismatch: {name}")
        data_by_name[name] = data
    canonical_manifest = _manifest_bytes(
        [(name, data) for name, data in data_by_name.items()]
    )
    if manifest_path.read_bytes() != canonical_manifest:
        raise ReleaseError("SHA256SUMS is not in canonical deterministic form")

    archive_name = f"{expected_base}.tar.gz"
    cdx_name = f"{expected_base}.cdx.json"
    spdx_name = f"{expected_base}.spdx.json"
    provenance_name = f"{expected_base}.provenance.json"
    source_gates_name = f"{expected_base}.source-gates.json"
    _verify_archive(input_dir / archive_name, context)
    try:
        cyclonedx = json.loads(data_by_name[cdx_name])
        spdx = json.loads(data_by_name[spdx_name])
        provenance = json.loads(data_by_name[provenance_name])
        source_gates = json.loads(data_by_name[source_gates_name])
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ReleaseError(f"invalid JSON release metadata: {exc}") from exc
    _verify_sboms(context, cyclonedx, spdx)
    if _json_bytes(source_gates) != _json_bytes(source_gate_document(context)):
        raise ReleaseError("source-gate artifact does not match the commit/report")

    if provenance.get("_type") != "https://in-toto.io/Statement/v1":
        raise ReleaseError("provenance is not an in-toto Statement v1")
    if provenance.get("predicateType") != "https://slsa.dev/provenance/v1":
        raise ReleaseError("provenance predicate is not SLSA v1")
    dependencies = (
        provenance.get("predicate", {})
        .get("buildDefinition", {})
        .get("resolvedDependencies", [])
    )
    if (
        not dependencies
        or dependencies[0].get("digest", {}).get("gitCommit") != context.commit
    ):
        raise ReleaseError("provenance does not identify the selected Git commit")
    external_parameters = (
        provenance.get("predicate", {})
        .get("buildDefinition", {})
        .get("externalParameters", {})
    )
    if external_parameters.get("automatedGateStatus") != "AUTOMATED_GATES_PASSED":
        raise ReleaseError("provenance overclaims or omits automated gate status")
    if external_parameters.get("evidenceScope") != "source-gates":
        raise ReleaseError("provenance does not limit evidence to source gates")
    actual_subjects = {
        item.get("name"): item.get("digest", {}).get("sha256")
        for item in provenance.get("subject", [])
    }
    expected_subjects = {
        name: _sha256(data_by_name[name])
        for name in (archive_name, cdx_name, spdx_name, source_gates_name)
    }
    if actual_subjects != expected_subjects:
        raise ReleaseError("provenance subjects do not match the release artifacts")
    return [input_dir / name for name in sorted(actual_files)]
