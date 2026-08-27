[CmdletBinding()]
[Diagnostics.CodeAnalysis.SuppressMessageAttribute(
    'PSAvoidUsingWriteHost',
    '',
    Justification = 'This executable qualification harness emits human-readable progress in CI.'
)]
[Diagnostics.CodeAnalysis.SuppressMessageAttribute(
    'PSUseShouldProcessForStateChangingFunctions',
    '',
    Justification = 'State changes are restricted to a uniquely named fixture tree under TEMP.'
)]
param(
    [string]$RepositoryRoot,
    [string]$ReportPath,
    [ValidateRange(10, 10000)]
    [int]$SoakRequestsPerCycle = 80,
    [ValidateRange(2, 20)]
    [int]$SoakRestartCycles = 2,
    [switch]$KeepArtifacts
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$windowsTests = Split-Path -Parent $PSScriptRoot
. (Join-Path $windowsTests 'Test-Helpers.ps1')
$repo = Resolve-RepositoryRoot $RepositoryRoot
if ([string]::IsNullOrWhiteSpace($ReportPath)) {
    $ReportPath = Join-Path $repo 'artifacts\release-gates\WINDOWS-AUTOMATED-GATES-RESULT.md'
}
$ReportPath = [IO.Path]::GetFullPath($ReportPath)
$work = New-SafeSmokeDirectory -Prefix 'codex-router-smoke-qualification'
$startedAt = [DateTimeOffset]::UtcNow
$results = [Collections.Generic.List[object]]::new()
$effectiveSoakRequestsPerCycle = [int]$SoakRequestsPerCycle
$effectiveSoakRestartCycles = [int]$SoakRestartCycles

function Get-StringSha256 {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Value)

    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($Value)
        return ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace('-', '').ToLowerInvariant()
    }
    finally { $sha.Dispose() }
}

function Get-RegistryFingerprint {
    param([Parameter(Mandatory)][string]$SubKey)

    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey(
        [Microsoft.Win32.RegistryHive]::CurrentUser,
        [Microsoft.Win32.RegistryView]::Default
    )
    try {
        $key = $base.OpenSubKey($SubKey, $false)
        if ($null -eq $key) {
            return [pscustomobject]@{ Kind = 'Registry'; Name = "HKCU\$SubKey"; Exists = $false; Sha256 = $null }
        }
        try {
            $lines = [Collections.Generic.List[string]]::new()
            $pending = [Collections.Generic.Queue[object]]::new()
            $pending.Enqueue([pscustomobject]@{ Relative = ''; Key = $key })
            while ($pending.Count -gt 0) {
                $entry = $pending.Dequeue()
                $current = $entry.Key
                $relative = [string]$entry.Relative
                $lines.Add("key:$relative")
                foreach ($valueName in @($current.GetValueNames() | Sort-Object)) {
                    $kind = [string]$current.GetValueKind($valueName)
                    $value = $current.GetValue(
                        $valueName,
                        $null,
                        [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames
                    )
                    $encoded = if ($value -is [byte[]]) {
                        [Convert]::ToBase64String($value)
                    }
                    elseif ($value -is [string[]]) {
                        [string]::Join("`0", $value)
                    }
                    elseif ($null -eq $value) { '' }
                    else { [Convert]::ToString($value, [Globalization.CultureInfo]::InvariantCulture) }
                    $lines.Add("value:$relative/$valueName`:$kind`:$(Get-StringSha256 -Value $encoded)")
                }
                foreach ($childName in @($current.GetSubKeyNames() | Sort-Object)) {
                    $child = $current.OpenSubKey($childName, $false)
                    if ($null -ne $child) {
                        $childRelative = if ($relative) { "$relative\$childName" } else { $childName }
                        $pending.Enqueue([pscustomobject]@{ Relative = $childRelative; Key = $child })
                    }
                }
                if (-not [Object]::ReferenceEquals($current, $key)) { $current.Dispose() }
            }
            return [pscustomobject]@{
                Kind = 'Registry'
                Name = "HKCU\$SubKey"
                Exists = $true
                Sha256 = Get-StringSha256 -Value (($lines | Sort-Object) -join "`n")
            }
        }
        finally { $key.Dispose() }
    }
    finally { $base.Dispose() }
}

function Get-FileFingerprint {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][string]$Name)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return [pscustomobject]@{ Kind = 'File'; Name = $Name; Exists = $false; Sha256 = $null }
    }
    return [pscustomobject]@{
        Kind = 'File'
        Name = $Name
        Exists = $true
        Sha256 = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

function Get-OfficialIntegrationSnapshot {
    $snapshots = [Collections.Generic.List[object]]::new()
    foreach ($subKey in @(
        'Software\Google\Chrome\NativeMessagingHosts\com.openai.codexextension',
        'Software\Classes\codex',
        'Software\Classes\Directory\shell\OpenProjectInCodex',
        'Software\Classes\Directory\Background\shell\OpenProjectInCodex',
        'Software\Classes\codex-router',
        'Software\Classes\Directory\shell\OpenProjectInCodexRouter',
        'Software\Classes\Directory\Background\shell\OpenProjectInCodexRouter'
    )) {
        $snapshots.Add((Get-RegistryFingerprint -SubKey $subKey))
    }
    $fileCandidates = @(
        @{ Path = (Join-Path $env:LOCALAPPDATA 'OpenAI\extension\com.openai.codexextension.json'); Name = 'OpenAI Chrome native-host manifest' },
        @{ Path = (Join-Path $env:LOCALAPPDATA 'OpenAI\Codex\chrome-native-hosts-v2.json'); Name = 'OpenAI Codex native-host catalog' },
        @{ Path = (Join-Path $env:USERPROFILE '.codex\chrome-native-hosts-v2.json'); Name = 'Codex user native-host catalog' }
    )
    foreach ($candidate in $fileCandidates) {
        $snapshots.Add((Get-FileFingerprint -Path $candidate.Path -Name $candidate.Name))
    }
    return @($snapshots)
}

function Assert-SnapshotsEqual {
    param([Parameter(Mandatory)][object[]]$Before, [Parameter(Mandatory)][object[]]$After)

    if ($Before.Count -ne $After.Count) { throw 'Official integration snapshot cardinality changed.' }
    for ($index = 0; $index -lt $Before.Count; $index++) {
        $left = $Before[$index]
        $right = $After[$index]
        if ($left.Kind -cne $right.Kind -or $left.Name -cne $right.Name -or
            $left.Exists -ne $right.Exists -or $left.Sha256 -cne $right.Sha256) {
            throw "Official integration state changed during automated synthetic gates: $($left.Name)"
        }
    }
}

function New-StageEvidence {
    param([Parameter(Mandatory)][string]$Summary, $Data)
    return [pscustomobject]@{ QualificationSummary = $Summary; QualificationData = $Data }
}

function Invoke-QualificationStage {
    param(
        [Parameter(Mandatory)][string]$ID,
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][scriptblock]$Action
    )

    $stageStarted = [DateTimeOffset]::UtcNow
    Write-Host "`n[$ID] $Name" -ForegroundColor Cyan
    try {
        $output = @(& $Action)
        $evidence = if ($output.Count -gt 0) { $output[-1] } else { $null }
        $summary = 'Completed successfully.'
        $data = $null
        if ($null -ne $evidence -and $null -ne $evidence.PSObject.Properties['QualificationSummary']) {
            $summary = [string]$evidence.QualificationSummary
            $data = $evidence.QualificationData
        }
        $results.Add([pscustomobject]@{
            ID = $ID
            Name = $Name
            Status = 'PASS'
            Seconds = [Math]::Round(([DateTimeOffset]::UtcNow - $stageStarted).TotalSeconds, 2)
            Summary = $summary
            Data = $data
        })
        Write-Host "PASS  $Name" -ForegroundColor Green
    }
    catch {
        $results.Add([pscustomobject]@{
            ID = $ID
            Name = $Name
            Status = 'FAIL'
            Seconds = [Math]::Round(([DateTimeOffset]::UtcNow - $stageStarted).TotalSeconds, 2)
            Summary = $_.Exception.Message
            Data = $null
        })
        Write-Host "FAIL  $Name`: $($_.Exception.Message)" -ForegroundColor Red
    }
}

function New-SyntheticRouterLayout {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$Version,
        [Parameter(Mandatory)][string]$Destination,
        [Parameter(Mandatory)][string]$StateRoot
    )

    [void](New-Item -ItemType Directory -Path (Join-Path $Path 'resources\cua_node') -Force)
    $contents = [ordered]@{
        'ChatGPT.exe' = "synthetic launcher $Version"
        'ChatGPT.real.exe' = "synthetic desktop $Version"
        'resources\app.asar' = "synthetic patched asar $Version"
        'resources\codex.exe' = "synthetic mux $Version"
        'resources\codex.real.exe' = "synthetic original codex $Version"
        'resources\cua_node\code-mode-host.exe' = "synthetic CUA helper $Version"
        'release-marker.txt' = $Version
    }
    foreach ($relativePath in $contents.Keys) {
        $fullPath = Join-Path $Path $relativePath
        [IO.File]::WriteAllText($fullPath, $contents[$relativePath], [Text.UTF8Encoding]::new($false))
    }
    $manifest = [ordered]@{
        schemaVersion = 1
        syntheticFixture = $true
        routerVersion = $Version
        createdAtUtc = [DateTimeOffset]::UtcNow.ToString('O')
        destination = [IO.Path]::GetFullPath($Destination)
        profilePath = [IO.Path]::GetFullPath((Join-Path $StateRoot 'Profile'))
        patchedAsarSha256 = (Get-FileHash -LiteralPath (Join-Path $Path 'resources\app.asar') -Algorithm SHA256).Hash.ToLowerInvariant()
        muxSha256 = (Get-FileHash -LiteralPath (Join-Path $Path 'resources\codex.exe') -Algorithm SHA256).Hash.ToLowerInvariant()
        launcherSha256 = (Get-FileHash -LiteralPath (Join-Path $Path 'ChatGPT.exe') -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    [IO.File]::WriteAllText(
        (Join-Path $Path 'codex-mux-build.json'),
        (($manifest | ConvertTo-Json -Depth 10) + [Environment]::NewLine),
        [Text.UTF8Encoding]::new($false)
    )
}

function New-TestShortcut {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][string]$TargetPath)

    $shell = New-Object -ComObject WScript.Shell
    try {
        $shortcut = $shell.CreateShortcut($Path)
        try {
            $shortcut.TargetPath = $TargetPath
            $shortcut.Save()
        }
        finally { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut) | Out-Null }
    }
    finally { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell) | Out-Null }
}

function Invoke-SyntheticLifecycle {
    $lifecycleRoot = Join-Path $work 'lifecycle'
    $destination = Join-Path $lifecycleRoot 'Programs\Codex Subscription Router'
    $stateRoot = Join-Path $lifecycleRoot 'Programs\Codex Subscription Router Data'
    $backupRoot = Join-Path $lifecycleRoot 'Programs\.codex-subscription-router-backups'
    $shortcutRoot = Join-Path $lifecycleRoot 'Start Menu'
    $shortcutPath = Join-Path $shortcutRoot 'Codex Subscription Router.lnk'
    $payloadV1 = Join-Path $lifecycleRoot 'payload-v1'
    $payloadV2 = Join-Path $lifecycleRoot 'payload-v2'
    $shellFixture = Join-Path $lifecycleRoot 'shell-integration-fixture.ps1'
    $shellFixtureEvidence = Join-Path $lifecycleRoot 'shell-integration-fixture.json'
    [void](New-Item -ItemType Directory -Path $stateRoot -Force)
    [void](New-Item -ItemType Directory -Path $backupRoot -Force)
    [void](New-Item -ItemType Directory -Path $shortcutRoot -Force)
    [IO.File]::WriteAllText((Join-Path $stateRoot 'state-canary.txt'), 'preserve-me', [Text.UTF8Encoding]::new($false))
    $shellFixtureSource = @'
[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [string]$Action,
    [string]$Feature,
    [string]$LauncherPath
)
if ($Action -ne 'Unregister' -or $Feature -ne 'All' -or -not [IO.Path]::IsPathRooted($LauncherPath)) {
    throw 'Invalid synthetic shell-integration contract.'
}
if (-not $WhatIfPreference) {
    @{ Action = $Action; Feature = $Feature; LauncherLeaf = (Split-Path -Leaf $LauncherPath) } |
        ConvertTo-Json | Set-Content -LiteralPath '__EVIDENCE__' -Encoding utf8NoBOM
}
'@.Replace('__EVIDENCE__', $shellFixtureEvidence.Replace("'", "''"))
    [IO.File]::WriteAllText($shellFixture, $shellFixtureSource, [Text.UTF8Encoding]::new($false))
    New-SyntheticRouterLayout -Path $payloadV1 -Version '1.0.0-fixture' -Destination $destination -StateRoot $stateRoot
    New-SyntheticRouterLayout -Path $payloadV2 -Version '2.0.0-fixture' -Destination $destination -StateRoot $stateRoot

    $modulePath = Join-Path $repo 'scripts\WindowsLifecycle.psm1'
    $rollbackPath = Join-Path $repo 'scripts\rollback_windows.ps1'
    $uninstallPath = Join-Path $repo 'scripts\uninstall_windows.ps1'
    foreach ($requiredPath in @($modulePath, $rollbackPath, $uninstallPath)) {
        if (-not (Test-Path -LiteralPath $requiredPath -PathType Leaf)) { throw "Lifecycle release command is missing: $requiredPath" }
    }
    Import-Module $modulePath -Force

    $nestedRejected = $false
    try {
        Assert-CsrManagedPaths `
            -Destination (Join-Path $backupRoot 'nested-destination') `
            -StateRoot $stateRoot `
            -BackupRoot $backupRoot `
            -AllowedRoot $lifecycleRoot
    }
    catch { $nestedRejected = $true }
    if (-not $nestedRejected) { throw 'Lifecycle path validation accepted Destination inside BackupRoot.' }

    Copy-Item -LiteralPath $payloadV1 -Destination $destination -Recurse -Force
    [void](Assert-CsrInstallationIntegrity -LayoutPath $destination -ExpectedDestination $destination -ExpectedStateRoot $stateRoot)
    New-TestShortcut -Path $shortcutPath -TargetPath (Join-Path $destination 'ChatGPT.exe')
    & (Join-Path $windowsTests 'Test-InstalledLayout.ps1') -AppRoot $destination -RequireComputerUse

    $backupContainer = Join-Path $backupRoot 'fixture-v1'
    [void](New-Item -ItemType Directory -Path $backupContainer)
    Move-Item -LiteralPath $destination -Destination (Join-Path $backupContainer (Split-Path -Leaf $destination))
    Copy-Item -LiteralPath $payloadV2 -Destination $destination -Recurse -Force
    [void](Assert-CsrInstallationIntegrity -LayoutPath $destination -ExpectedDestination $destination -ExpectedStateRoot $stateRoot)
    Assert-Equal -Actual (Get-Content -LiteralPath (Join-Path $destination 'release-marker.txt') -Raw) -Expected '2.0.0-fixture' -Message 'Synthetic update did not publish v2'

    & $rollbackPath `
        -Destination $destination -StateRoot $stateRoot -BackupRoot $backupRoot `
        -BackupPath $backupContainer -ShortcutPath $shortcutPath -AllowedRoot $lifecycleRoot -WhatIf
    Assert-Equal -Actual (Get-Content -LiteralPath (Join-Path $destination 'release-marker.txt') -Raw) -Expected '2.0.0-fixture' -Message 'Rollback -WhatIf mutated the active version'

    $injectedFailureObserved = $false
    try {
        & $rollbackPath `
            -Destination $destination -StateRoot $stateRoot -BackupRoot $backupRoot `
            -BackupPath $backupContainer -ShortcutPath $shortcutPath -AllowedRoot $lifecycleRoot `
            -TestFailAfterMoveCurrent -Confirm:$false
    }
    catch { $injectedFailureObserved = $_.Exception.Message -match 'restored automatically' }
    if (-not $injectedFailureObserved) { throw 'Rollback failure injection did not fail closed and report automatic restoration.' }
    Assert-Equal -Actual (Get-Content -LiteralPath (Join-Path $destination 'release-marker.txt') -Raw) -Expected '2.0.0-fixture' -Message 'Injected rollback failure did not restore v2'

    & $rollbackPath `
        -Destination $destination -StateRoot $stateRoot -BackupRoot $backupRoot `
        -BackupPath $backupContainer -ShortcutPath $shortcutPath -AllowedRoot $lifecycleRoot -Confirm:$false
    Assert-Equal -Actual (Get-Content -LiteralPath (Join-Path $destination 'release-marker.txt') -Raw) -Expected '1.0.0-fixture' -Message 'Rollback did not restore v1'
    Assert-Equal -Actual (Get-Content -LiteralPath (Join-Path $stateRoot 'state-canary.txt') -Raw) -Expected 'preserve-me' -Message 'Rollback changed persistent state'

    & $uninstallPath `
        -Destination $destination -StateRoot $stateRoot -BackupRoot $backupRoot `
        -ShortcutPath $shortcutPath -ShortcutAllowedRoot $shortcutRoot -AllowedRoot $lifecycleRoot `
        -ShellIntegrationScript $shellFixture -WhatIf
    if (-not (Test-Path -LiteralPath $destination -PathType Container)) { throw 'Uninstall -WhatIf removed the application.' }
    if (-not (Test-Path -LiteralPath $shortcutPath -PathType Leaf)) { throw 'Uninstall -WhatIf removed the shortcut.' }

    & $uninstallPath `
        -Destination $destination -StateRoot $stateRoot -BackupRoot $backupRoot `
        -ShortcutPath $shortcutPath -ShortcutAllowedRoot $shortcutRoot -AllowedRoot $lifecycleRoot `
        -ShellIntegrationScript $shellFixture -Confirm:$false
    if (Test-Path -LiteralPath $destination) { throw 'Uninstall left the authenticated application behind.' }
    if (Test-Path -LiteralPath $shortcutPath) { throw 'Uninstall left its exact owned shortcut behind.' }
    Assert-Equal -Actual (Get-Content -LiteralPath (Join-Path $stateRoot 'state-canary.txt') -Raw) -Expected 'preserve-me' -Message 'Default uninstall did not preserve state'
    $shellEvidence = Get-Content -LiteralPath $shellFixtureEvidence -Raw | ConvertFrom-Json
    Assert-Equal -Actual $shellEvidence.Action -Expected 'Unregister' -Message 'Uninstall did not invoke the shell integration fixture with Unregister'
    Assert-Equal -Actual $shellEvidence.Feature -Expected 'All' -Message 'Uninstall did not invoke the shell integration fixture for all router-owned integrations'
    Assert-Equal -Actual $shellEvidence.LauncherLeaf -Expected 'ChatGPT.exe' -Message 'Uninstall passed the wrong launcher to its shell-integration contract'

    return New-StageEvidence -Summary 'Fresh synthetic install, A->B update, failure rollback, exact rollback, -WhatIf, and uninstall-preserve-state passed.' -Data ([pscustomobject]@{
        FreshInstall = 'PASS'
        Update = 'PASS'
        FailureRollback = 'PASS'
        Rollback = 'PASS'
        UninstallWhatIf = 'PASS'
        UninstallPreservedState = 'PASS'
        UnsafeNestedDestinationRejected = 'PASS'
    })
}

function Convert-ToMarkdownSafe {
    param([AllowEmptyString()][string]$Value)
    return $Value.Replace('|', '\|').Replace("`r", ' ').Replace("`n", ' ')
}

function Write-QualificationReport {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][object[]]$StageResults,
        [Parameter(Mandatory)][DateTimeOffset]$Started,
        [Parameter(Mandatory)][DateTimeOffset]$Finished
    )

    $failed = @($StageResults | Where-Object Status -EQ 'FAIL')
    $automatedStatus = if ($failed.Count -eq 0) { 'PASS' } else { 'FAIL' }
    $gitCommit = (& git -C $repo rev-parse HEAD 2>$null | Select-Object -First 1)
    if ([string]::IsNullOrWhiteSpace($gitCommit)) { $gitCommit = 'unavailable' }
    $gitDirty = -not [string]::IsNullOrWhiteSpace((& git -C $repo status --porcelain 2>$null) -join '')
    $os = Get-CimInstance Win32_OperatingSystem -ErrorAction SilentlyContinue
    $osLabel = if ($null -ne $os) { "$($os.Caption) $($os.Version) build $($os.BuildNumber)" } else { [Environment]::OSVersion.VersionString }
    $lines = [Collections.Generic.List[string]]::new()
    $lines.Add('# Windows automated release gates result')
    $lines.Add('')
    $lines.Add("- Generated (UTC): $($Finished.ToString('O'))")
    $lines.Add("- Candidate commit: $gitCommit")
    $lines.Add("- Working tree dirty while tested: $($gitDirty.ToString().ToLowerInvariant())")
    $lines.Add("- Host: $osLabel; PowerShell $($PSVersionTable.PSVersion)")
    $automatedLabel = if ($automatedStatus -eq 'PASS') { 'AUTOMATED_GATES_PASSED' } else { 'AUTOMATED_GATES_FAILED' }
    $lines.Add("- Automated source/synthetic gates: **$automatedLabel**")
    $lines.Add('- Public E2E qualification: **NOT QUALIFIED**')
    $lines.Add("- Elapsed: $([Math]::Round(($Finished - $Started).TotalSeconds, 2)) seconds")
    $lines.Add('')
    $lines.Add('This report contains only evidence produced by the commands executed in this run. The harness used temporary synthetic binaries and `example.invalid` fake accounts. It did not launch, stop, patch, authenticate, or read state from the installed Codex application.')
    $lines.Add('')
    $lines.Add('## Executed automated gates')
    $lines.Add('')
    $lines.Add('| Gate | Result | Seconds | Executed evidence |')
    $lines.Add('|---|---:|---:|---|')
    foreach ($result in $StageResults) {
        $lines.Add("| $($result.ID) - $(Convert-ToMarkdownSafe $result.Name) | $($result.Status) | $($result.Seconds) | $(Convert-ToMarkdownSafe $result.Summary) |")
    }
    $soak = $StageResults | Where-Object ID -EQ 'SYN-05' | Select-Object -First 1
    if ($null -ne $soak -and $null -ne $soak.Data) {
        $lines.Add('')
        $lines.Add('## Synthetic soak measurements')
        $lines.Add('')
        $lines.Add("- Restart cycles: $($soak.Data.RestartCycles)")
        $lines.Add("- Account-scoped requests: $($soak.Data.Requests)")
        $lines.Add("- Peak synthetic process count: $($soak.Data.PeakProcessCount)")
        $lines.Add("- Peak working set: $($soak.Data.PeakWorkingSetMB) MiB")
        $lines.Add("- Peak private memory: $($soak.Data.PeakPrivateMemoryMB) MiB")
        $lines.Add("- Peak handles: $($soak.Data.PeakHandles)")
        $lines.Add("- Maximum within-cycle working-set growth: $($soak.Data.MaxCycleWorkingSetGrowthMB) MiB")
        $lines.Add("- Maximum within-cycle handle growth: $($soak.Data.MaxCycleHandleGrowth)")
        $lines.Add("- Remaining synthetic processes: $($soak.Data.RemainingSyntheticProcesses)")
    }
    $lines.Add('')
    $lines.Add('## Gates requiring controlled Windows E2E qualification')
    $lines.Add('')
    $manualGates = @(
        '**Signed release artifacts:** sign the project-owned launcher and mux with the public release certificate and timestamp; verify with `signtool verify /pa /all`.',
        '**Clean disposable VM lifecycle:** install, launch, update from the previous release, inject a failed update, rollback, reboot, and uninstall as a standard user. Prove official Codex package, profile, protocol, Explorer and Chrome state are invariant.',
        '**Two dedicated test subscriptions:** validate login, rename/disable/enable/logout, sticky ownership, history deduplication, plugins/MCP and controlled quota failover. Do not consume a real reset credit or force quota exhaustion.',
        '**Desktop UI:** validate single-instance activation, notifications, `codex-router://` URI security, Explorer directory/background verbs, Unicode/long paths, high contrast, keyboard navigation and screen reader labels.',
        '**Computer Use and Appshots:** validate helper provenance, consent, cancellation, insertion, multi-monitor layouts and supported DPI values without personal content.',
        '**Long soak:** run at least one stable-host extended session with repeated child crash/recovery and collect private memory, working set, handles, process count and orphan-process evidence.',
        '**Distribution/legal boundary:** publish source and project-owned build artifacts only; confirm that no OpenAI executable, ASAR, credential, account state, certificate or private screenshot is redistributed.',
        '**Optional MSIX only if shipped:** build with the Windows SDK, sign, install, update and uninstall it in the VM; otherwise explicitly mark MSIX unsupported for this release.'
    )
    foreach ($gate in $manualGates) { $lines.Add("- $gate") }
    $lines.Add('')
    $lines.Add('The candidate must remain `NOT QUALIFIED` for a stable public release until every applicable controlled gate has recorded evidence and the canonical Windows E2E report is changed to `QUALIFIED`. Automated evidence permits only an explicitly labelled source preview/prerelease.')

    $directory = Split-Path -Parent $Path
    [void](New-Item -ItemType Directory -Path $directory -Force)
    [IO.File]::WriteAllLines($Path, $lines, [Text.UTF8Encoding]::new($false))
}

$officialBefore = Get-OfficialIntegrationSnapshot
try {
    Invoke-QualificationStage -ID 'SYN-01' -Name 'Offline Windows smoke suite' -Action {
        & (Join-Path $windowsTests 'Invoke-WindowsSmokeTests.ps1') -Mode Offline -RepositoryRoot $repo
        New-StageEvidence -Summary 'Proxy, two-account routing, control security, contracts and patcher fixtures passed.' -Data $null
    }
    Invoke-QualificationStage -ID 'SYN-02' -Name 'Synthetic packaging lifecycle' -Action {
        $packagingTest = Join-Path $repo 'packaging\windows\Test-Packaging.ps1'
        if (-not (Test-Path -LiteralPath $packagingTest -PathType Leaf)) { throw 'Synthetic packaging test is missing.' }
        $packaging = @(& $packagingTest)
        $result = $packaging[-1]
        if ($result.Passed -ne $true) { throw 'Synthetic packaging contract did not report success.' }
        New-StageEvidence -Summary 'Initial package, overwrite backup, hash manifest, and removal fixture passed.' -Data $result
    }
    Invoke-QualificationStage -ID 'SYN-03' -Name 'Synthetic install, update, rollback, and uninstall' -Action {
        Invoke-SyntheticLifecycle
    }
    Invoke-QualificationStage -ID 'SYN-04' -Name 'Protocol and Explorer isolation fixture' -Action {
        $shellTest = Join-Path $windowsTests 'Test-ShellIntegration.ps1'
        if (-not (Test-Path -LiteralPath $shellTest -PathType Leaf)) { throw 'Shell-integration fixture is missing.' }
        & $shellTest -RepositoryRoot $repo
        New-StageEvidence -Summary 'In-memory registry tests passed: independent protocol/verbs, quoting, idempotence, -WhatIf, and compare-and-delete.' -Data $null
    }
    Invoke-QualificationStage -ID 'SYN-05' -Name 'Two-account restart and short resource soak' -Action {
        $soakOutput = @(& (Join-Path $windowsTests 'Test-RouterSoak.ps1') `
            -RepositoryRoot $repo -RequestsPerCycle $effectiveSoakRequestsPerCycle -RestartCycles $effectiveSoakRestartCycles)
        $soak = $soakOutput[-1]
        if ($soak.Passed -ne $true -or $soak.SyntheticOnly -ne $true) { throw 'Synthetic soak did not report success.' }
        New-StageEvidence -Summary "$($soak.RestartCycles) restarts and $($soak.Requests) requests passed with zero orphan synthetic processes." -Data $soak
    }
}
finally {
    Invoke-QualificationStage -ID 'SYN-06' -Name 'Official Chrome/protocol/Explorer invariance' -Action {
        $officialAfter = Get-OfficialIntegrationSnapshot
        Assert-SnapshotsEqual -Before $officialBefore -After $officialAfter
        New-StageEvidence -Summary 'All redacted host-integration fingerprints were identical before and after synthetic operations.' -Data ([pscustomobject]@{ Fingerprints = $officialAfter.Count })
    }
    $finishedAt = [DateTimeOffset]::UtcNow
    Write-QualificationReport -Path $ReportPath -StageResults @($results) -Started $startedAt -Finished $finishedAt
    Write-Host "`nQualification report: $ReportPath"
    if ($KeepArtifacts) {
        Write-Host "Qualification artifacts: $work"
    }
    else {
        Remove-SafeSmokeDirectory -Path $work
    }
}

$failures = @($results | Where-Object Status -EQ 'FAIL')
if ($failures.Count -gt 0) {
    $failedIDs = $failures.ID -join ', '
    throw "Windows automated release gates failed: $failedIDs. See $ReportPath"
}
Write-SmokePass 'Windows automated release gates passed; public E2E qualification remains open'
