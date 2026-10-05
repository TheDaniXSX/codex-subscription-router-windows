"""Offline guards for the single-app c4eb2ea recovery, not live acceptance."""

from __future__ import annotations

import json
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[2]

# These belong to the later experimental fork, not this recovery baseline.
RETIRED_RUNTIME_PATHS = (
    "internal/usage",
    "internal/broker",
    "cmd/codex-mux/shared.go",
    "scripts/activate_shared_pair.ps1",
    "scripts/windows_shared_state.py",
    "scripts/windows_shared_state_26928.py",
    "ui/turn-usage.js",
    "ui/shared-global-state.cjs",
    "ui/draft-guard.js",
)
RETIRED_ENVIRONMENT_OPTIONS = (
    "CODEX_MUX_USAGE_",
    "CODEX_MUX_SHARED_",
    "CODEX_ROUTER_INSTALL_CHANNEL",
    "CODEX_MUX_INSTALL_CHANNEL",
    "CODEX_ROUTER_USAGE_CALIBRATION",
)
RUNTIME_TEXT_SUFFIXES = {".go", ".js", ".cjs", ".py", ".ps1", ".psm1"}


class BaselineRecoveryTests(unittest.TestCase):
    def test_experimental_runtime_modules_are_not_included(self) -> None:
        for relative in RETIRED_RUNTIME_PATHS:
            with self.subTest(path=relative):
                self.assertFalse(
                    (ROOT / relative).exists(),
                    f"Single-app baseline unexpectedly includes {relative}",
                )

    def test_runtime_does_not_enable_calibration_or_shared_channels(self) -> None:
        for directory in ("cmd", "internal", "scripts", "ui"):
            for path in sorted((ROOT / directory).rglob("*")):
                if path.is_file() and path.suffix in RUNTIME_TEXT_SUFFIXES:
                    contents = path.read_text(encoding="utf-8-sig")
                    for option in RETIRED_ENVIRONMENT_OPTIONS:
                        with self.subTest(path=path.relative_to(ROOT), option=option):
                            self.assertNotIn(option, contents)

    def test_asar_tooling_is_preserved_with_patched_brace_expansion(self) -> None:
        package = json.loads((ROOT / "package.json").read_text(encoding="utf-8"))
        lock = json.loads((ROOT / "package-lock.json").read_text(encoding="utf-8"))
        self.assertEqual(package["devDependencies"]["@electron/asar"], "4.3.0")
        self.assertEqual(lock["packages"]["node_modules/@electron/asar"]["version"], "4.3.0")
        brace_version = lock["packages"]["node_modules/brace-expansion"]["version"]
        parts = tuple(int(part) for part in brace_version.split("."))
        self.assertEqual(parts[0], 5, "Preserve the compatible transitive major")
        self.assertGreaterEqual(parts, (5, 0, 12))


if __name__ == "__main__":
    unittest.main()
