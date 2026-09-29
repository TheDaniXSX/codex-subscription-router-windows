from __future__ import annotations

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class DevelopmentInstallerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.shell = shutil.which("pwsh")
        if cls.shell is None:
            raise unittest.SkipTest("PowerShell 7 is unavailable")

    def test_installer_guards_are_read_only_and_channel_bound(self) -> None:
        # Parse the real installer, then load only its pure validation helpers.
        # This never executes installer top-level code or resolves the live app.
        with tempfile.TemporaryDirectory(prefix="csr-dev-channel-") as temporary:
            fixture = Path(temporary)
            script = fixture / "guards.ps1"
            script.write_text(
                r"""
param([string]$Repository, [string]$Fixture)
Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
$parseErrors = $null
$tokens = $null
$installer = Join-Path $Repository 'scripts/install_windows.ps1'
$ast = [Management.Automation.Language.Parser]::ParseFile($installer, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) { throw ($parseErrors | Out-String) }
$names = @('Resolve-FullPath', 'Test-PathIsWithin', 'Assert-InstallChannelPaths',
    'Get-RecordedInstallChannel', 'Assert-ExistingInstallationBinding')
foreach ($function in $ast.FindAll({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst]}, $false)) {
    if ($names -contains $function.Name) { . ([scriptblock]::Create($function.Extent.Text)) }
}
function MustReject([scriptblock]$Action) {
    $rejected = $false
    try { & $Action } catch { $rejected = $true }
    if (-not $rejected) { throw 'Expected channel guard rejection.' }
}
$local = Join-Path $Fixture 'local'
$app = Join-Path $local 'CSR-Dev/app'
$state = Join-Path $local 'CSR-Dev/data'
$prod = Join-Path $local 'Programs/Codex Subscription Router'
$prodState = Join-Path $local 'Programs/Codex Subscription Router Data'
Assert-InstallChannelPaths $app $state $local 'development'
Assert-InstallChannelPaths $prod $prodState $local 'production'
MustReject { Assert-InstallChannelPaths $prod $state $local 'development' }
MustReject { Assert-InstallChannelPaths $app $prodState $local 'development' }
MustReject { Assert-InstallChannelPaths $app $state $local 'production' }
if (Test-Path -LiteralPath $local) { throw 'Path preflight created directories.' }
$configDirectory = Join-Path $app 'resources/codex-router'
[void](New-Item -ItemType Directory -Path $configDirectory -Force)
$manifest = Join-Path $app 'codex-mux-build.json'
$sidecar = Join-Path $configDirectory 'launcher-config.json'
@{installChannel='development'} | ConvertTo-Json | Set-Content -LiteralPath $manifest
@{installChannel='development';stateRoot=$state} | ConvertTo-Json | Set-Content -LiteralPath $sidecar
Assert-ExistingInstallationBinding $app $state 'development'
MustReject { Assert-ExistingInstallationBinding $app $state 'production' }
MustReject { Assert-ExistingInstallationBinding $app (Join-Path $local 'CSR-Dev/other') 'development' }
@{installChannel='production'} | ConvertTo-Json | Set-Content -LiteralPath $manifest
MustReject { Assert-ExistingInstallationBinding $app $state 'development' }
# Both production records may omit the channel for old installations.
@{} | ConvertTo-Json | Set-Content -LiteralPath $manifest
@{stateRoot=$state} | ConvertTo-Json | Set-Content -LiteralPath $sidecar
Assert-ExistingInstallationBinding $app $state 'production'
foreach ($file in @('scripts/verify_windows_build.ps1', 'scripts/WindowsLifecycle.psm1')) {
    $parseErrors = $null
    [void][Management.Automation.Language.Parser]::ParseFile((Join-Path $Repository $file), [ref]$tokens, [ref]$parseErrors)
    if ($parseErrors.Count -ne 0) { throw ($parseErrors | Out-String) }
}
Write-Host 'Development channel guards passed.'
""",
                encoding="utf-8",
            )
            result = subprocess.run(
                [self.shell, "-NoProfile", "-NonInteractive", "-File", str(script),
                 "-Repository", str(ROOT), "-Fixture", str(fixture)],
                capture_output=True, text=True, timeout=30,
                env={**os.environ, "POWERSHELL_TELEMETRY_OPTOUT": "1"},
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("Development channel guards passed.", result.stdout)

    def test_installer_wires_guards_before_mutation_and_distinct_shortcut(self) -> None:
        source = (ROOT / "scripts" / "install_windows.ps1").read_text(encoding="utf-8")
        calls = source[source.index("\ntry {\n    if ($env:OS"):]
        self.assertLess(calls.index("Assert-InstallChannelPaths -InstallDestination"), calls.index("Initialize-SecureStateRoot -RouterStateRoot"))
        self.assertLess(calls.index("Assert-ExistingInstallationBinding -InstallDestination"), calls.index("Initialize-SecureStateRoot -RouterStateRoot"))
        self.assertIn("-ShortcutName $script:StartMenuShortcutName", calls)
        self.assertIn("'--state-root', $StateRoot", calls)
        self.assertIn("'CSR-Dev\\app'", calls)
        self.assertIn("'CSR-Dev\\data'", calls)


if __name__ == "__main__":
    unittest.main()
