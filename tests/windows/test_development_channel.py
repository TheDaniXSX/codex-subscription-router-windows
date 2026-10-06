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

    def test_shared_schema_is_validated_by_installer_lifecycle_and_verifier(self) -> None:
        with tempfile.TemporaryDirectory(prefix="csr-shared-schema-") as temporary:
            fixture = Path(temporary)
            script = fixture / "schema.ps1"
            script.write_text(r"""
param([string]$Repository, [string]$Fixture)
Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $Repository 'scripts/WindowsLifecycle.psm1') -Force
$errors = $null; $tokens = $null
$ast = [Management.Automation.Language.Parser]::ParseFile((Join-Path $Repository 'scripts/install_windows.ps1'), [ref]$tokens, [ref]$errors)
foreach ($function in $ast.FindAll({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst]}, $false)) {
    if ($function.Name -in @('Resolve-FullPath', 'Test-InstalledLayout')) { . ([scriptblock]::Create($function.Extent.Text)) }
}
function MustReject([scriptblock]$Action) {
    $rejected = $false
    try { & $Action } catch { $rejected = $true }
    if (-not $rejected) { throw 'Expected shared binding rejection.' }
}
function Get-JsonProperty { param($Object, $Name) if ($null -ne $Object -and $null -ne $Object.PSObject.Properties[$Name]) { return $Object.$Name } return $null }
function Get-NormalizedPath { param($Path, [switch]$AllowMissing) return [IO.Path]::GetFullPath($Path) }
function Add-Check { param($Name, $Passed, $Detail) if (-not $Passed) { throw "$Name : $Detail" } }
$verifier = Get-Content -LiteralPath (Join-Path $Repository 'scripts/verify_windows_build.ps1') -Raw
$start = $verifier.IndexOf('        $manifestChannel = [string](Get-JsonProperty -Object $manifest')
$end = $verifier.IndexOf("`n    }`n    catch", $start)
if ($end -lt 0) { $end = $verifier.IndexOf("`r`n    }`r`n    catch", $start) }
if ($start -lt 0 -or $end -lt $start) { throw 'Verifier channel block not found.' }
$channelCheck = [scriptblock]::Create($verifier.Substring($start, $end - $start))
$StrictSignatures = $false
foreach ($channel in @('production', 'development')) {
    $script:InstallChannelValue = $channel
    $app = Join-Path $Fixture $channel
    $StateRoot = Join-Path $Fixture ($channel + '-runtime')
    $native = Join-Path $Fixture 'native'
    $configuration = Join-Path $app 'resources/codex-router'
    [void](New-Item -ItemType Directory -Path $configuration -Force)
    foreach ($file in @('ChatGPT.exe', 'resources/codex.exe', 'resources/codex.real.exe', 'resources/app.asar')) {
        Set-Content -LiteralPath (Join-Path $app $file) -Value 'fixture'
    }
    $identity = if ($channel -eq 'development') { 'com.openai.codex.subscription-router.dev' } else { 'com.openai.codex.subscription-router' }
    $display = if ($channel -eq 'development') { 'Codex Subscription Router [DEV]' } else { 'Codex Subscription Router' }
    $fields = @{schemaVersion=3;controlPort=55876;installChannel=$channel;sharedStateRoot=(Join-Path $Fixture 'shared');
        sharedPrimaryHome=$native;usageDataRoot=(Join-Path $Fixture 'usage');sharedProtocol=1;activationPairId='test-pair-001';
        primaryCodexHome=$native;primarySqliteHome=$native}
    $manifestData = $fields.Clone()
    $manifestData.destination = $app
    $manifestData.profilePath = Join-Path $StateRoot 'Profile'
    $manifestData.sourceAsarSha256 = 'fixture'
    $manifestData.patchedAsarSha256 = 'fixture'
    $manifestData.muxSha256 = 'fixture'
    $manifestData.launcherSha256 = 'fixture'
    $manifestData.windowsIntegrationIsolation = @{appUserModelId=$identity;displayName=$display}
    $sidecar = $fields.Clone(); $sidecar.stateRoot = $StateRoot
    $manifestPath = Join-Path $app 'codex-mux-build.json'
    $sidecarPath = Join-Path $configuration 'launcher-config.json'
    $manifestData | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $manifestPath
    $sidecar | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $sidecarPath
    [void](Test-InstalledLayout $app $StateRoot)
    [void](Read-CsrManifest $app $app $StateRoot)
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    $launcherConfig = Get-Content -LiteralPath $sidecarPath -Raw | ConvertFrom-Json
    $schemaNumber = 3
    . $channelCheck
    $savedIdentity = $manifest.windowsIntegrationIsolation.appUserModelId
    $manifest.windowsIntegrationIsolation.PSObject.Properties.Remove('appUserModelId')
    MustReject { . $channelCheck }
    $manifest.windowsIntegrationIsolation | Add-Member -NotePropertyName appUserModelId -NotePropertyValue $savedIdentity
    $sidecar.activationPairId = 'different-pair'
    $sidecar | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $sidecarPath
    MustReject { Test-InstalledLayout $app $StateRoot }
    MustReject { Read-CsrManifest $app $app $StateRoot }
    $launcherConfig.activationPairId = 'different-pair'
    MustReject { . $channelCheck }
}
# Only historical, implicit-production, non-release metadata may omit both
# identity fields. Explicit channel, partial identity and strict mode fail closed.
$schemaNumber = 2
$manifest = [pscustomobject]@{windowsIntegrationIsolation=[pscustomobject]@{}}
$launcherConfig = [pscustomobject]@{}
. $channelCheck
$StrictSignatures = $true
MustReject { . $channelCheck }
$StrictSignatures = $false
$manifest | Add-Member -NotePropertyName installChannel -NotePropertyValue 'production'
MustReject { . $channelCheck }
$manifest.PSObject.Properties.Remove('installChannel')
$manifest.windowsIntegrationIsolation | Add-Member -NotePropertyName appUserModelId -NotePropertyValue 'com.openai.codex.subscription-router'
MustReject { . $channelCheck }
Write-Host 'Shared schema checks passed.'
""", encoding="utf-8")
            result = subprocess.run([self.shell, "-NoProfile", "-NonInteractive", "-File", str(script),
                "-Repository", str(ROOT), "-Fixture", str(fixture)], capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("Shared schema checks passed.", result.stdout)

    def test_prepare_only_skips_live_mutations_and_port_reservation(self) -> None:
        source = (ROOT / "scripts/install_windows.ps1").read_text(encoding="utf-8")
        self.assertIn("if (-not $script:EffectiveDryRun -and -not $PrepareOnly) {\n        Initialize-SecureStateRoot", source)
        self.assertIn("$patcherPublished = -not $script:EffectiveDryRun -and -not $PrepareOnly", source)
        self.assertIn("$patchArguments += @('--prepare-only', '--prepared-destination', $PreparedDestination)", source)
        self.assertIn("if ($PrepareOnly) {\n        Write-Info", source)
        self.assertIn("'--shared-state-root', $SharedStateRoot", source)


if __name__ == "__main__":
    unittest.main()
