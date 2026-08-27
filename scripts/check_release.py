#!/usr/bin/env python3
"""Perform deterministic, non-building Windows release checks."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
REPOSITORY = "https://github.com/TheDaniXSX/codex-subscription-router-windows"
PATCH_PROFILE = "windows-26.820.9563.0-x64-r1"
COMPATIBILITY_MARKERS = (
    "OpenAI.Codex_26.820.9563.0_x64__2p2nqsd0c76g0",
    "26.820.71523",
    "7226",
    "0.150.0-alpha.8",
    "e353c580ef4939d36f4ae32a35c896d089205c1d06b9f711cf78ffa4a3578a8a",
    "799ff77125c47b0736ceb36e9b33975bb93d4162bca663730f3a4c90faf2add9",
    "4ec11307b67796338d666f40c431b2804e41669576d3bc350dece8703bf4a114",
)
REQUIRED_FILES = (
    ".github/workflows/release.yml",
    ".github/workflows/security.yml",
    ".github/workflows/windows.yml",
    "CHANGELOG.md",
    "CONTRIBUTING.md",
    "LICENSE",
    "NOTICE.md",
    "README.md",
    "SECURITY.md",
    "VERSION",
    "docs/COMPATIBILITY.md",
    "docs/CHROME-CONNECTOR-RELEASE.md",
    "docs/E2E-REPORT-WINDOWS.md",
    "docs/INSTALL-WINDOWS.md",
    "docs/RELEASING.md",
    "docs/SOURCE-RELEASE-ARTIFACTS.md",
    "docs/WINDOWS-ARCHITECTURE.md",
    "docs/WINDOWS-CAPABILITY-QUALIFICATION.md",
    "docs/WINDOWS-DIAGNOSTICS.md",
    "docs/WINDOWS-IMPLEMENTATION-PLAN.md",
    "docs/WINDOWS-PARITY.md",
    "docs/WINDOWS-AUTOMATED-GATES-RESULT.md",
    "docs/WINDOWS-RELEASE.md",
    "docs/WINDOWS-SECURITY.md",
    "docs/WINDOWS-SHELL-INTEGRATION.md",
    "package-lock.json",
    "package.json",
    "packaging/windows/Test-Packaging.ps1",
    "scripts/cleanup_windows.ps1",
    "scripts/doctor_windows.ps1",
    "scripts/install_windows.ps1",
    "scripts/inventory_windows_source.ps1",
    "scripts/measure_windows_router.ps1",
    "scripts/patch_windows_app.py",
    "scripts/release/build_source_release.py",
    "scripts/release/create_source_gate_evidence.py",
    "scripts/release/verify_source_release.py",
    "scripts/rollback_windows.ps1",
    "scripts/uninstall_windows.ps1",
    "scripts/verify_windows_build.ps1",
    "scripts/windows/Manage-ShellIntegration.ps1",
    "tests/release/test_source_release.py",
    "tests/windows/Invoke-WindowsSmokeTests.ps1",
    "tests/windows/SMOKE-TEST.md",
    "tests/windows/Test-DoctorAndSoak.ps1",
    "tests/windows/release-gates/Invoke-WindowsAutomatedReleaseGates.ps1",
)
FORBIDDEN_TRACKED_SUFFIXES = {
    ".appx",
    ".appxbundle",
    ".asar",
    ".cer",
    ".dll",
    ".dmg",
    ".dmp",
    ".exe",
    ".key",
    ".mobileprovision",
    ".msi",
    ".msix",
    ".msixbundle",
    ".node",
    ".p12",
    ".pem",
    ".pfx",
    ".pkg",
    ".provisionprofile",
    ".zip",
}
FORBIDDEN_TRACKED_NAMES = {
    ".env",
    "auth.json",
    "codex-mux-build.json",
    "control-token",
    "credentials.json",
    "launcher-config.json",
    "state.json",
}
TEXT_SUFFIXES = {
    "",
    ".c",
    ".cjs",
    ".go",
    ".js",
    ".json",
    ".md",
    ".ps1",
    ".psd1",
    ".psm1",
    ".py",
    ".toml",
    ".xml",
    ".yml",
    ".yaml",
}
MACHINE_PATH_PATTERNS = (
    re.compile("/" + r"Users/(?!example/|test/)[^/\s`]+/", re.IGNORECASE),
    re.compile(
        r"[A-Z]:\\Users\\(?!example\\|test\\|username\\|<)[^\\\s`]+\\",
        re.IGNORECASE,
    ),
    re.compile("D:" + r"\\DevFiles\\Personal\\", re.IGNORECASE),
)
MARKER_PATTERN = r"^<!--\s*{name}:\s*([^>]+?)\s*-->$"


def fail(message: str) -> None:
    print(f"release check: {message}", file=sys.stderr)
    raise SystemExit(1)


def release_marker(report: str, name: str) -> str:
    """Return one exact machine-readable marker from the E2E report."""

    matches = re.findall(
        MARKER_PATTERN.format(name=re.escape(name)), report, re.MULTILINE
    )
    if len(matches) != 1:
        fail(f"E2E report must contain exactly one {name!r} marker")
    return matches[0].strip()


def validate_qualification(report: str, version: str) -> str:
    """Validate the versioned E2E template or historical report."""

    report_version = release_marker(report, "release-version")
    status = release_marker(report, "release-qualification")
    commit = release_marker(report, "release-commit")
    date = release_marker(report, "release-date")

    if report_version != version:
        fail("E2E report version does not match VERSION")
    if status not in {"QUALIFIED", "NOT QUALIFIED"}:
        fail("E2E qualification must be QUALIFIED or NOT QUALIFIED")

    if status == "QUALIFIED":
        if re.fullmatch(r"[0-9a-f]{40}", commit) is None:
            fail("qualified E2E report must record a full lowercase commit SHA")
        try:
            dt.date.fromisoformat(date)
        except ValueError:
            fail("qualified E2E report must record an ISO qualification date")
        if re.search(r"^- \[ \]", report, re.MULTILINE):
            fail("qualified E2E report still contains unchecked release gates")

    return status


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--release-gate",
        action="store_true",
        help="validate metadata before commit-bound CI qualification runs",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)

    for relative in REQUIRED_FILES:
        if not (ROOT / relative).is_file():
            fail(f"missing required Windows release file: {relative}")

    version = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
    if re.fullmatch(r"\d+\.\d+\.\d+", version) is None:
        fail(f"VERSION is not semantic: {version!r}")

    package = json.loads((ROOT / "package.json").read_text(encoding="utf-8"))
    lock = json.loads((ROOT / "package-lock.json").read_text(encoding="utf-8"))
    if package.get("version") != version:
        fail("package.json version does not match VERSION")
    lock_root_version = lock.get("packages", {}).get("", {}).get("version")
    if lock.get("version") != version or lock_root_version != version:
        fail("package-lock.json version does not match VERSION")
    if package.get("repository", {}).get("url") != f"git+{REPOSITORY}.git":
        fail("package.json repository does not identify the Windows fork")
    if package.get("homepage") != f"{REPOSITORY}#readme":
        fail("package.json homepage does not identify the Windows fork")
    if package.get("bugs", {}).get("url") != f"{REPOSITORY}/issues":
        fail("package.json bugs URL does not identify the Windows fork")
    if package.get("scripts", {}).get("release:gate") != (
        "python scripts/check_release.py --release-gate"
    ):
        fail("package.json does not expose the release:gate command")
    if package.get("scripts", {}).get("release:automated-evidence") != (
        "python scripts/release/create_source_gate_evidence.py "
        "--output artifacts/source-gates.json"
    ):
        fail("package.json does not expose commit-bound automated evidence")

    asar_version = package.get("devDependencies", {}).get("@electron/asar")
    locked_asar = lock.get("packages", {}).get("node_modules/@electron/asar", {})
    if not isinstance(asar_version, str) or re.fullmatch(
        r"\d+\.\d+\.\d+", asar_version
    ) is None:
        fail("@electron/asar must use an exact version")
    if locked_asar.get("version") != asar_version:
        fail("package-lock.json does not match the declared @electron/asar version")
    if package.get("license") != "MIT":
        fail("package.json license does not match LICENSE")

    changelog = (ROOT / "CHANGELOG.md").read_text(encoding="utf-8")
    dated_heading = rf"^## \[{re.escape(version)}\] - \d{{4}}-\d{{2}}-\d{{2}}$"
    if re.search(dated_heading, changelog, re.MULTILINE) is None:
        fail(f"CHANGELOG.md has no dated entry for {version}")
    expected_release_link = f"{REPOSITORY}/releases/tag/v{version}"
    expected_compare_link = f"{REPOSITORY}/compare/v{version}...HEAD"
    if expected_release_link not in changelog:
        fail(f"CHANGELOG.md has no Windows-fork release link for {version}")
    if expected_compare_link not in changelog:
        fail("CHANGELOG.md has no Windows-fork Unreleased comparison link")

    compatibility = (ROOT / "docs/COMPATIBILITY.md").read_text(encoding="utf-8")
    if f"## Release {version}" not in compatibility:
        fail(f"docs/COMPATIBILITY.md has no entry for {version}")
    if PATCH_PROFILE not in compatibility:
        fail("docs/COMPATIBILITY.md does not name the locked Windows profile")
    for marker in COMPATIBILITY_MARKERS:
        if marker not in compatibility:
            fail(f"docs/COMPATIBILITY.md is missing locked value: {marker}")

    report = (ROOT / "docs/E2E-REPORT-WINDOWS.md").read_text(encoding="utf-8")
    status = validate_qualification(report, version)
    if PATCH_PROFILE not in report:
        fail("Windows E2E report does not name the locked patch profile")
    for marker in COMPATIBILITY_MARKERS:
        if marker not in report:
            fail(f"Windows E2E report is missing locked value: {marker}")

    tracked_output = subprocess.check_output(["git", "ls-files", "-z"], cwd=ROOT)
    tracked = [
        Path(value.decode("utf-8"))
        for value in tracked_output.split(b"\0")
        if value
    ]
    for relative in tracked:
        path = ROOT / relative
        lower_name = relative.name.lower()
        contains_app_bundle = any(
            part.lower().endswith(".app") for part in relative.parts
        )
        if contains_app_bundle or relative.suffix.lower() in FORBIDDEN_TRACKED_SUFFIXES:
            fail(f"forbidden release artifact is tracked: {relative}")
        if lower_name in FORBIDDEN_TRACKED_NAMES or lower_name.startswith(".env."):
            fail(f"credential or local-state file is tracked: {relative}")
        if path.is_file() and path.stat().st_size > 10 * 1024 * 1024:
            fail(f"unexpected tracked file larger than 10 MiB: {relative}")
        if path.is_file() and relative.suffix.lower() in TEXT_SUFFIXES:
            text = path.read_text(encoding="utf-8", errors="replace")
            for pattern in MACHINE_PATH_PATTERNS:
                if pattern.search(text):
                    fail(f"machine-specific user/workspace path is tracked: {relative}")

    report_state = "historical qualification" if status == "QUALIFIED" else "template"
    suffix = "; ready for commit-bound CI gates" if args.release_gate else ""
    print(
        f"release check: v{version} Windows metadata is consistent "
        f"({report_state}{suffix})"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
