#!/usr/bin/env python3
"""Statically qualify Appshots and Computer Use in a staged Windows router.

The command is intentionally read-only.  It extracts app.asar into a temporary
directory, validates the strict Appshots opt-in gate, and inspects the preserved
Computer Use runtime without starting Electron, Node, or a native helper.
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import subprocess
import sys
import tempfile
from pathlib import Path


PROJECT_ROOT = Path(__file__).resolve().parent.parent
PATCHER_PATH = PROJECT_ROOT / "scripts" / "patch_windows_app.py"
SPEC = importlib.util.spec_from_file_location("windows_capability_patcher", PATCHER_PATH)
assert SPEC is not None and SPEC.loader is not None
patcher = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = patcher
SPEC.loader.exec_module(patcher)


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--app-root",
        required=True,
        type=Path,
        help="Staged/installed router app root or its ChatGPT.exe launcher.",
    )
    parser.add_argument(
        "--cua-only",
        action="store_true",
        help="Inspect only preserved Computer Use resources; do not extract app.asar.",
    )
    return parser.parse_args(argv)


def normalize_app_root(value: Path) -> Path:
    candidate = value.expanduser().resolve(strict=True)
    if candidate.is_file():
        if candidate.name.lower() != "chatgpt.exe":
            raise RuntimeError(f"candidate executable is not ChatGPT.exe: {candidate}")
        candidate = candidate.parent
    if patcher._is_linklike(candidate) or not candidate.is_dir():
        raise RuntimeError(f"candidate app root must be a real directory: {candidate}")
    resources = candidate / "resources"
    if not resources.is_dir() or not (resources / "app.asar").is_file():
        raise RuntimeError(f"candidate does not contain resources/app.asar: {candidate}")
    return candidate


def validate_approved_computer_use(
    source_version: object, computer_use: dict[str, object]
) -> None:
    expected = (
        patcher.TESTED_SOURCE_BUILDS.get(source_version)
        if isinstance(source_version, str)
        else None
    )
    if expected is None:
        raise RuntimeError(f"router build manifest has no approved sourceVersion: {source_version!r}")
    comparisons = {
        "tree digest": (computer_use["treeSha256"], expected["cua_tree_sha256"]),
        "Node version": (computer_use["nodeVersion"], expected["cua_node_version"]),
        "runtime version": (
            computer_use["runtimeVersion"],
            expected["cua_runtime_version"],
        ),
        "package version": (
            computer_use["packageVersion"],
            expected["cua_package_version"],
        ),
    }
    for label, (actual, wanted) in comparisons.items():
        if actual != wanted:
            raise RuntimeError(
                f"Computer Use {label} is {actual!r}, expected approved value {wanted!r}"
            )


def qualify(app_root: Path, *, cua_only: bool) -> dict[str, object]:
    resources = app_root / "resources"
    build_manifest_path = app_root / patcher.BUILD_MANIFEST_NAME
    build_manifest = patcher._read_bounded_json(
        build_manifest_path, 1024 * 1024, "router build manifest"
    )
    source_version = build_manifest.get("sourceVersion")
    computer_use = patcher.inspect_computer_use_contract(resources)
    validate_approved_computer_use(source_version, computer_use)
    result: dict[str, object] = {
        "schemaVersion": 1,
        "execution": "static-read-only",
        "sourceVersion": source_version,
        "buildManifestSha256": patcher.sha256_file(build_manifest_path),
        "computerUse": computer_use,
    }
    if cua_only:
        result["appshots"] = {"qualification": "not-evaluated"}
        return result

    asar = patcher.ensure_asar_tool()
    with tempfile.TemporaryDirectory(prefix="codex-router-capability-qualification-") as temporary:
        extracted = Path(temporary) / "asar"
        patcher.run([str(asar), "extract", str(resources / "app.asar"), str(extracted)])
        result["appshots"] = patcher.verify_windows_appshots_contract(extracted)
    return result


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        app_root = normalize_app_root(args.app_root)
        result = qualify(app_root, cua_only=args.cua_only)
    except (OSError, RuntimeError, ValueError, subprocess.CalledProcessError) as error:
        print(f"capability qualification failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
