#Requires -Version 5.1

[CmdletBinding()]
param([string]$RepositoryRoot)

. (Join-Path $PSScriptRoot 'Test-Helpers.ps1')
$repo = Resolve-RepositoryRoot $RepositoryRoot
$fixtureRoot = Join-Path $PSScriptRoot 'fixtures\diagnostics'
$doctor = Join-Path $repo 'scripts\doctor_windows.ps1'
$soak = Join-Path $repo 'scripts\measure_windows_router.ps1'

function Assert-Condition {
    param(
        [Parameter(Mandatory = $true)][bool]$Condition,
        [Parameter(Mandatory = $true)][string]$Message
    )
    if (-not $Condition) { throw $Message }
}

function Get-FixtureHashMap {
    $hashes = [ordered]@{}
    foreach ($file in @(Get-ChildItem -LiteralPath $fixtureRoot -File -Recurse -Force | Sort-Object FullName)) {
        $hashes[$file.FullName] = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
    }
    return $hashes
}

if (-not (Test-Path -LiteralPath $doctor -PathType Leaf)) { throw "Doctor script is missing: $doctor" }
if (-not (Test-Path -LiteralPath $soak -PathType Leaf)) { throw "Soak script is missing: $soak" }

$before = Get-FixtureHashMap
$appRoot = Join-Path $fixtureRoot 'app'
$stateRoot = Join-Path $fixtureRoot 'state'
$backupRoot = Join-Path $fixtureRoot 'backups'
$failedRoot = Join-Path $fixtureRoot 'failed'
$tempRoot = Join-Path $fixtureRoot 'temp'
$manifestPath = Join-Path $appRoot 'build-manifest.fixture.json'
$launcherConfigPath = Join-Path $fixtureRoot 'launcher-sidecar.fixture.json'

$doctorText = (& $doctor `
        -AppRoot $appRoot `
        -StateRoot $stateRoot `
        -BackupRoot $backupRoot `
        -FailedRoots @($failedRoot) `
        -TempRoots @($tempRoot) `
        -StateFilePath (Join-Path $stateRoot 'router-state.fixture.json') `
        -ProcessSnapshotPath (Join-Path $fixtureRoot 'processes.json') `
        -AccountSnapshotPath (Join-Path $fixtureRoot 'accounts.json') `
        -ManifestPath $manifestPath `
        -LauncherConfigPath $launcherConfigPath `
        -SkipLiveControl `
        -OutputFormat Json | Out-String)
$doctorReport = $doctorText | ConvertFrom-Json

Assert-Condition ([bool]$doctorReport.ReadOnly) 'Doctor did not mark the report read-only.'
Assert-Condition ([bool]$doctorReport.ShareSafeByDefault) 'Doctor report is not share-safe by default.'
Assert-Equal -Actual $doctorReport.Processes.InventoryStatus -Expected 'ok' -Message 'Fixture process inventory did not remain deterministic'
Assert-Equal -Actual @($doctorReport.Processes.Router).Count -Expected 2 -Message 'Doctor did not filter router processes by AppRoot'
Assert-Equal -Actual @($doctorReport.Processes.OfficialCodex).Count -Expected 1 -Message 'Doctor did not identify the official Codex process separately'
Assert-Condition ([bool]$doctorReport.Processes.SimultaneousDesktopApps) 'Doctor missed simultaneous official and router processes.'
Assert-Condition ([bool]$doctorReport.ControlPort.OwnedByRouter) 'Doctor did not bind the synthetic listener owner to AppRoot.'
Assert-Equal -Actual $doctorReport.ControlPort.Port -Expected 49210 -Message 'Doctor did not discover the randomized control port'
Assert-Equal -Actual $doctorReport.ControlPort.Source -Expected 'launcher-config+manifest' -Message 'Doctor did not cross-check launcher and manifest control ports'
Assert-Equal -Actual $doctorReport.Accounts.Source -Expected 'snapshot' -Message 'Doctor did not use the supplied account snapshot'
Assert-Equal -Actual $doctorReport.Accounts.Count -Expected 2 -Message 'Doctor returned the wrong safe account count'
Assert-Equal -Actual $doctorReport.Accounts.Items[0].Account -Expected 'Controller' -Message 'Doctor did not anonymize the controller account'
Assert-Equal -Actual $doctorReport.Accounts.Items[1].Status -Expected 'disconnected' -Message 'Doctor lost account connection status'
Assert-Condition ([bool]$doctorReport.Manifest.Present) 'Doctor did not find the fixture manifest.'
Assert-Equal -Actual $doctorReport.Manifest.Fields.projectVersion -Expected '9.9.9-test' -Message 'Doctor did not expose safe version metadata'
Assert-Condition (@($doctorReport.Storage | Where-Object { $_.Name -eq 'ActiveApp' -and $_.Bytes -gt 0 }).Count -eq 1) 'Doctor did not measure the app fixture.'
Assert-Condition (@($doctorReport.RedactedLogs | Where-Object { @($_.Lines | Where-Object { $_ -match '<REDACTED' }).Count -gt 0 }).Count -eq 1) 'Doctor did not return the expected redacted log excerpt.'
$redactedValues = @($doctorReport.RedactedLogs | ForEach-Object { $_.Lines }) + @($doctorReport.Accounts.Items | ForEach-Object { $_.Error })
Assert-Condition ((@($redactedValues) -join "`n") -match '<REDACTED') 'Doctor did not show redaction markers.'
foreach ($privateValue in @(
        'alice@example.invalid', 'bob@example.invalid', 'TEST-SECRET-BEARER',
        'PRIVATE-ACCESS-TOKEN', 'test-access-token', 'test-password',
        'private-primary-id', 'private-secondary-id', 'Private controller label',
        'Private company label', 'DO-NOT-EMIT-THIS-FIELD', 'C:\Users\example'
    )) {
    Assert-Condition (-not $doctorText.Contains($privateValue)) "Doctor leaked fixture-private value: $privateValue"
}
Assert-Condition (@($doctorReport.Recommendations | Where-Object { $_ -match 'run the router instead' }).Count -eq 1) 'Doctor omitted the simultaneous-app usage recommendation.'

$offlineText = (& $doctor `
        -AppRoot $appRoot `
        -StateRoot $stateRoot `
        -BackupRoot $backupRoot `
        -FailedRoots @($failedRoot) `
        -TempRoots @($tempRoot) `
        -StateFilePath (Join-Path $stateRoot 'router-state.fixture.json') `
        -ProcessSnapshotPath (Join-Path $fixtureRoot 'processes.json') `
        -ManifestPath $manifestPath `
        -LauncherConfigPath $launcherConfigPath `
        -SkipLiveControl `
        -SkipLogExcerpts `
        -OutputFormat Json | Out-String)
$offlineReport = $offlineText | ConvertFrom-Json
Assert-Equal -Actual $offlineReport.Accounts.Source -Expected 'offline-state' -Message 'Doctor did not fall back to offline routing state'
Assert-Equal -Actual $offlineReport.Accounts.Items[0].ThreadCount -Expected 2 -Message 'Doctor did not count offline thread ownership'
foreach ($privateValue in @('private-secondary-id', 'Private company label', 'C:\Users\example')) {
    Assert-Condition (-not $offlineText.Contains($privateValue)) "Offline doctor leaked fixture-private value: $privateValue"
}

$mismatchText = (& $doctor `
        -AppRoot $appRoot `
        -StateRoot $stateRoot `
        -BackupRoot $backupRoot `
        -FailedRoots @($failedRoot) `
        -TempRoots @($tempRoot) `
        -StateFilePath (Join-Path $stateRoot 'router-state.fixture.json') `
        -ProcessSnapshotPath (Join-Path $fixtureRoot 'processes.json') `
        -ManifestPath (Join-Path $fixtureRoot 'build-manifest-mismatch.fixture.json') `
        -LauncherConfigPath $launcherConfigPath `
        -SkipLiveControl `
        -SkipLogExcerpts `
        -OutputFormat Json | Out-String)
$mismatchReport = $mismatchText | ConvertFrom-Json
Assert-Condition (-not [bool]$mismatchReport.ControlPort.Discovered) 'Doctor trusted mismatched persisted control ports.'
Assert-Equal -Actual $mismatchReport.ControlPort.Source -Expected 'configuration-mismatch' -Message 'Doctor did not report the port configuration mismatch'

$passText = (& $soak `
        -AppRoot 'D:\Synthetic\Router' `
        -FixturePath (Join-Path $fixtureRoot 'soak-pass.json') `
        -OutputFormat Json | Out-String)
$passReport = $passText | ConvertFrom-Json
Assert-Equal -Actual $passReport.Status -Expected 'Pass' -Message 'Stable synthetic soak did not pass'
Assert-Equal -Actual $passReport.SampleCount -Expected 3 -Message 'Synthetic soak sample count is wrong'
Assert-Equal -Actual $passReport.Summary.BaselineProcessCount -Expected 2 -Message 'Soak included a process outside AppRoot'
Assert-Equal -Actual $passReport.Summary.WorkingSetGrowthMB -Expected 15 -Message 'Working-set growth calculation is wrong'
Assert-Equal -Actual $passReport.Summary.PrivateMemoryGrowthMB -Expected 15 -Message 'Private-memory growth calculation is wrong'
Assert-Equal -Actual $passReport.Summary.HandleGrowth -Expected 40 -Message 'Handle growth calculation is wrong'
Assert-Condition ([bool]$passReport.ReadOnly) 'Soak report did not mark itself read-only.'
Assert-Equal -Actual $passReport.AppRoot -Expected '<APP>' -Message 'Soak exposed AppRoot by default'

$thresholdText = (& $soak `
        -AppRoot 'D:\Synthetic\Router' `
        -FixturePath (Join-Path $fixtureRoot 'soak-threshold.json') `
        -OutputFormat Json | Out-String)
$thresholdReport = $thresholdText | ConvertFrom-Json
Assert-Equal -Actual $thresholdReport.Status -Expected 'ThresholdExceeded' -Message 'Leaking synthetic soak did not exceed thresholds'
Assert-Condition (@($thresholdReport.Violations).Count -eq 3) 'Synthetic soak returned the wrong threshold violation count.'

$after = Get-FixtureHashMap
Assert-Equal -Actual $after.Count -Expected $before.Count -Message 'Diagnostics changed fixture file count'
foreach ($path in $before.Keys) {
    Assert-Equal -Actual $after[$path] -Expected $before[$path] -Message "Diagnostics modified fixture: $path"
}

Write-SmokePass 'Doctor output is read-only, share-safe, and redacted'
Write-SmokePass 'Resource soak filters by AppRoot and enforces deterministic growth thresholds'
