from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
MODULE_PATH = REPOSITORY_ROOT / "scripts" / "ShortcutIdentity.psm1"
APP_USER_MODEL_ID = "com.openai.codex.subscription-router"


@unittest.skipUnless(os.name == "nt", "Windows shortcut properties require Windows")
class ShortcutIdentityTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.shell = shutil.which("pwsh") or shutil.which("powershell")
        if cls.shell is None:
            raise unittest.SkipTest("PowerShell is unavailable")

    def test_round_trip_preserves_shortcut_target(self) -> None:
        with tempfile.TemporaryDirectory(prefix="codex-router-shortcut-") as root:
            shortcut = Path(root) / "Codex Subscription Router.lnk"
            script = Path(root) / "round-trip.ps1"
            script.write_text(
                "param([string]$Module,[string]$Shortcut,[string]$Target,[string]$AppId)\n"
                "$ErrorActionPreference='Stop'\n"
                "Import-Module $Module -Force\n"
                "$shell=New-Object -ComObject WScript.Shell\n"
                "try {\n"
                "  $link=$shell.CreateShortcut($Shortcut)\n"
                "  try { $link.TargetPath=$Target; $link.Save() }\n"
                "  finally { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($link)|Out-Null }\n"
                "}\n"
                "finally { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell)|Out-Null }\n"
                "Set-CsrShortcutAppUserModelId -Path $Shortcut -AppUserModelId $AppId\n"
                "$actual=Get-CsrShortcutAppUserModelId -Path $Shortcut\n"
                "$verifyShell=New-Object -ComObject WScript.Shell\n"
                "try {\n"
                "  $verify=$verifyShell.CreateShortcut($Shortcut)\n"
                "  try { $targetAfter=$verify.TargetPath }\n"
                "  finally { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($verify)|Out-Null }\n"
                "}\n"
                "finally { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($verifyShell)|Out-Null }\n"
                "Write-Output ('app_id=' + $actual)\n"
                "Write-Output ('target=' + $targetAfter)\n",
                encoding="utf-8",
            )

            result = subprocess.run(
                [
                    self.shell,
                    "-NoProfile",
                    "-NonInteractive",
                    "-File",
                    str(script),
                    "-Module",
                    str(MODULE_PATH),
                    "-Shortcut",
                    str(shortcut),
                    "-Target",
                    sys.executable,
                    "-AppId",
                    APP_USER_MODEL_ID,
                ],
                cwd=REPOSITORY_ROOT,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=30,
                check=False,
            )

            self.assertEqual(result.returncode, 0, result.stdout)
            self.assertIn(f"app_id={APP_USER_MODEL_ID}", result.stdout)
            target_line = next(
                (line for line in result.stdout.splitlines() if line.startswith("target=")),
                None,
            )
            self.assertIsNotNone(target_line, result.stdout)
            self.assertEqual(
                os.path.normcase(os.path.abspath(target_line.removeprefix("target="))),
                os.path.normcase(os.path.abspath(sys.executable)),
            )


if __name__ == "__main__":
    unittest.main()
