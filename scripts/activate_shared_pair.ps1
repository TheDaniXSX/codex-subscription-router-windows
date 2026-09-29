#Requires -Version 7.0
<#
Publishes two already-built, verified installations after their windows close.
Never kills an application or changes account/chat/calibration files. Both old
application trees remain available as rollback backups. Run independently of
either desktop process tree (for example from a normal PowerShell window).
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ProductionCandidate,
    [Parameter(Mandatory = $true)][string]$DevelopmentCandidate,
    [switch]$WaitForExit,
    [ValidateRange(1, 1440)][int]$TimeoutMinutes = 60,
    [switch]$LaunchDevelopment,
    [switch]$ValidateOnly,
    [string]$AllowedRoot = $env:LOCALAPPDATA
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'WindowsLifecycle.psm1') -Force

function Read-CsrCandidate([string]$Path, [string]$Channel) {
    $candidate = Resolve-CsrFullPath -Path $Path -MustExist
    Assert-CsrNoReparseAncestor -Path $candidate -AllowedRoot $AllowedRoot
    $receipt = Get-Content -LiteralPath (Join-Path $candidate 'codex-mux-prepared.json') -Raw | ConvertFrom-Json
    if ([int]$receipt.schemaVersion -ne 1 -or [bool]$receipt.controlTokenIsNew) {
        throw 'Pair activation requires two existing installations with their existing control tokens.'
    }
    $destination = Resolve-CsrFullPath -Path ([string]$receipt.destination) -MustExist
    $stateRoot = Resolve-CsrFullPath -Path ([string]$receipt.stateRoot) -MustExist
    foreach ($path in @($destination, $stateRoot)) {
        Assert-CsrNoReparseAncestor -Path $path -AllowedRoot $AllowedRoot
        if ($path -eq (Resolve-CsrFullPath $AllowedRoot)) { throw 'A managed path cannot be the allowed root.' }
    }
    if ((Test-CsrPathWithin $candidate $destination) -or (Test-CsrPathWithin $destination $candidate) -or
        (Test-CsrPathWithin $candidate $stateRoot) -or (Test-CsrPathWithin $stateRoot $candidate)) {
        throw 'Candidate, installation and data must occupy separate directory trees.'
    }
    if ([IO.Path]::GetPathRoot($candidate) -ne [IO.Path]::GetPathRoot($destination)) {
        throw 'Candidate and final installation must be on the same volume for atomic rename.'
    }
    if ((Get-CsrFileHash (Join-Path $candidate 'codex-mux-build.json')) -ne [string]$receipt.preparedManifestSha256) {
        throw 'Prepared manifest changed after preparation.'
    }
    if ((Get-CsrFileHash (Join-Path $destination 'codex-mux-build.json')) -ne [string]$receipt.expectedInstalledManifestSha256) {
        throw 'Installed app changed after preparation; build a new pair.'
    }
    # Token files can contain a trailing newline; the receipt hashes the value.
    $token = (Get-Content -LiteralPath (Join-Path $stateRoot 'control-token') -Raw).Trim()
    $tokenHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($token))).ToLowerInvariant()
    $token = $null
    if ($tokenHash -ne [string]$receipt.controlTokenSha256) { throw 'Control token changed after preparation.' }
    $manifest = Assert-CsrInstallationIntegrity -LayoutPath $candidate -ExpectedDestination $destination -ExpectedStateRoot $stateRoot
    if ([int]$manifest.schemaVersion -ne 3 -or [string]$manifest.installChannel -cne $Channel -or
        [string]$manifest.activationPairId -cne [string]$receipt.activationPairId) {
        throw 'Prepared installation has an invalid channel or shared binding.'
    }
    return [PSCustomObject]@{ Candidate=$candidate; Destination=$destination; StateRoot=$stateRoot; Manifest=$manifest; Receipt=$receipt; Backup=''; Published=$false; PreviousMoved=$false }
}

function Assert-CsrPair($Production, $Development) {
    foreach ($key in @('sharedStateRoot','sharedPrimaryHome','usageDataRoot','sharedProtocol','activationPairId','muxSha256','launcherSha256','sourceCodexSha256')) {
        if ([string]$Production.Manifest.$key -cne [string]$Development.Manifest.$key) { throw "Prepared pair disagrees on $key." }
    }
    if ($Production.Destination -eq $Development.Destination -or $Production.StateRoot -eq $Development.StateRoot -or
        [int]$Production.Manifest.controlPort -eq [int]$Development.Manifest.controlPort) {
        throw 'Each desktop requires a distinct installation, runtime root and control port.'
    }
    if ([string]$Production.Manifest.sharedStateRoot -ne $Production.StateRoot -or
        [string]$Development.Manifest.usageDataRoot -ne $Development.StateRoot) {
        throw 'Shared registry must remain in PROD data; calibration must remain in DEV data.'
    }
    $primary = Resolve-CsrFullPath ([string]$Production.Manifest.sharedPrimaryHome) -MustExist
    if (-not (Test-Path -LiteralPath $primary -PathType Container)) { throw 'Shared primary home must be an existing directory.' }
    # The native home may live outside LOCALAPPDATA. It is protected data,
    # never a move source or destination; reject aliases hiding an overlap.
    Assert-CsrNoReparseAncestor -Path $primary -AllowedRoot ([IO.Path]::GetPathRoot($primary))
    $protected = @($Production.StateRoot, $Development.StateRoot, $primary)
    $moved = @($Production.Candidate, $Development.Candidate, $Production.Destination, $Development.Destination)
    Assert-CsrSeparateTrees -Paths $protected -Description 'Account, calibration and native data'
    Assert-CsrSeparateTrees -Paths $moved -Description 'Prepared and installed applications'
    foreach ($path in $moved) {
        foreach ($dataRoot in $protected) {
            Assert-CsrSeparateTrees -Paths @($path,$dataRoot) -Description 'Applications and protected data'
        }
    }
    $operation = Resolve-CsrFullPath (Split-Path -Parent $Production.Candidate) -MustExist
    if ($operation -eq (Resolve-CsrFullPath $AllowedRoot)) { throw 'Activation staging must use its own directory below AllowedRoot.' }
    foreach ($dataRoot in $protected) {
        Assert-CsrSeparateTrees -Paths @($operation,$dataRoot) -Description 'Activation staging and protected data'
    }
    $backups = @((Join-Path $operation 'previous-production'), (Join-Path $operation 'previous-development'))
    Assert-CsrSeparateTrees -Paths @($moved + $backups) -Description 'Application sources, destinations and rollback backups'
}

function Assert-CsrSeparateTrees([string[]]$Paths, [string]$Description) {
    for ($i=0; $i -lt $Paths.Count; $i++) {
        for ($j=$i+1; $j -lt $Paths.Count; $j++) {
            if ((Test-CsrPathWithin $Paths[$i] $Paths[$j]) -or (Test-CsrPathWithin $Paths[$j] $Paths[$i])) {
                throw "$Description must be separate, non-overlapping trees: '$($Paths[$i])' and '$($Paths[$j])'."
            }
        }
    }
}

function Save-CsrPreActivationData($Production, $Development, [string]$OperationRoot) {
    # Only metadata and the calibration ledger are included. Native chat DBs,
    # account homes, auth.json and control tokens are never traversed or copied.
    $snapshotRoot = Join-Path $OperationRoot 'pre-shared-data'
    Assert-CsrNoReparseAncestor -Path $snapshotRoot -AllowedRoot $AllowedRoot
    if (Test-Path -LiteralPath $snapshotRoot) { throw 'A pre-activation data snapshot already exists; inspect it instead of overwriting it.' }
    $shared = [string]$Production.Manifest.sharedStateRoot
    $primary = [string]$Production.Manifest.sharedPrimaryHome
    $usage = [string]$Development.Manifest.usageDataRoot
    $sources = [Collections.Generic.List[object]]::new()
    foreach ($name in @('state.json','routing-mode.json','thread-spending.json')) {
        $sources.Add(@{root=$shared;relative=$name;target=('shared/' + $name)})
    }
    $sources.Add(@{root=$primary;relative='.codex-global-state.json';target='primary/.codex-global-state.json'})
    foreach ($name in @('usage-ledger.sqlite','usage-ledger.sqlite-journal','usage-ledger.sqlite-wal','usage-ledger.sqlite-shm','usage-relations.json')) {
        $sources.Add(@{root=$usage;relative=$name;target=('usage/' + $name)})
    }
    $spendingRoot = Join-Path $shared 'thread-spending'
    Assert-CsrNoReparseAncestor -Path $spendingRoot -AllowedRoot $shared
    $spendingNames = @()
    if (Test-Path -LiteralPath $spendingRoot) {
        if (-not (Test-Path -LiteralPath $spendingRoot -PathType Container)) { throw 'Thread spending metadata must be a directory.' }
        $spendingNames = @(Get-ChildItem -LiteralPath $spendingRoot -Force | Where-Object { $_.Name -match '^[a-f0-9]{64}\.json$' } | Sort-Object Name | ForEach-Object { $_.Name })
        foreach ($name in $spendingNames) { $sources.Add(@{root=$shared;relative=('thread-spending/' + $name);target=('shared/thread-spending/' + $name)}) }
    }
    # This is a cold file-set copy after both desktops and the broker stop.
    # Keep any crash-recovery SQLite sidecars alongside their main database;
    # never open either live data or its copy with a SQLite writer here.
    New-Item -ItemType Directory -Path $snapshotRoot | Out-Null
    [void](Set-CsrPrivateDirectoryAcl -Path $snapshotRoot)
    $files = [Collections.Generic.List[object]]::new()
    $missing = [Collections.Generic.List[object]]::new()
    foreach ($source in $sources) {
        $path = Join-Path $source.root $source.relative
        Assert-CsrNoReparseAncestor -Path $path -AllowedRoot $source.root
        if (-not (Test-Path -LiteralPath $path)) { $missing.Add(@{source=$path;relative=$source.target}); continue }
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Snapshot source must be a regular file: $path" }
        $target = Join-Path $snapshotRoot $source.target
        [void][IO.Directory]::CreateDirectory((Split-Path -Parent $target))
        $before = Get-CsrFileHash $path
        $inputStream = [IO.File]::Open($path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
        try {
            $outputStream = [IO.File]::Open($target, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
            try { $inputStream.CopyTo($outputStream); $outputStream.Flush($true) }
            finally { $outputStream.Dispose() }
        } finally { $inputStream.Dispose() }
        if ((Get-CsrFileHash $target) -ne $before) { throw "Snapshot source changed while copying: $path" }
        $files.Add(@{source=$path;relative=$source.target;sha256=$before;bytes=(Get-Item -LiteralPath $target -Force).Length})
    }
    foreach ($file in $files) {
        if ((Get-CsrFileHash $file.source) -ne $file.sha256) { throw "Snapshot source changed during the baseline: $($file.source)" }
    }
    foreach ($file in $missing) {
        if (Test-Path -LiteralPath $file.source) { throw "Snapshot source appeared during the baseline: $($file.source)" }
    }
    $currentSpendingNames = @()
    if (Test-Path -LiteralPath $spendingRoot) {
        $currentSpendingNames = @(Get-ChildItem -LiteralPath $spendingRoot -Force | Where-Object { $_.Name -match '^[a-f0-9]{64}\.json$' } | Sort-Object Name | ForEach-Object { $_.Name })
    }
    if (($currentSpendingNames -join ',') -cne ($spendingNames -join ',')) { throw 'Thread spending metadata changed during the baseline.' }
    Write-CsrJsonAtomic -Path (Join-Path $snapshotRoot 'snapshot.json') -Value @{
        schemaVersion=1;state='complete';format='cold-file-set';pairId=$Production.Manifest.activationPairId;createdAt=[DateTime]::UtcNow.ToString('o');files=@($files);missing=@($missing)
    }
    # New files inherit the private root ACL while copied, then receive their
    # own protected ACL so later moves cannot make the snapshot public.
    [void](Set-CsrPrivateDirectoryAcl -Path $snapshotRoot)
    return $snapshotRoot
}

$maintenance = $null
$maintenanceHeld = $false
$registryGuard = $null
$transcriptStarted = $false
$journalStarted = $false
$committed = $false
$previousJournal = $null
$journalPath = $null
$entries = @()
try {
    $AllowedRoot = Resolve-CsrFullPath $AllowedRoot -MustExist
    $production = Read-CsrCandidate $ProductionCandidate 'production'
    $development = Read-CsrCandidate $DevelopmentCandidate 'development'
    Assert-CsrPair $production $development
    $entries = @($production,$development)
    $initialReceipts = @($entries | ForEach-Object { $_.Receipt | ConvertTo-Json -Depth 20 -Compress })
    $pairId = [string]$production.Manifest.activationPairId
    if ($pairId -notmatch '^[A-Za-z0-9_-]{8,128}$') { throw 'Invalid activation pair ID.' }
    $operationRoot = Split-Path -Parent $production.Candidate
    $statusPath = Join-Path $operationRoot 'activation-status.json'
    if ($ValidateOnly) { Write-Host 'Both candidates, existing installations, control tokens and shared bindings verified. No changes made.'; return }
    Start-Transcript -LiteralPath (Join-Path $operationRoot 'activation.log') -Append | Out-Null
    $transcriptStarted = $true
    $roots = @($entries | ForEach-Object { $_.Destination })
    $deadline = [DateTime]::UtcNow.AddMinutes($TimeoutMinutes)
    Write-CsrJsonAtomic -Path $statusPath -Value @{pairId=$pairId;state='waiting';updatedAt=[DateTime]::UtcNow.ToString('o')}
    while (@(Get-CsrRouterProcesses -Roots $roots).Count -gt 0) {
        if (-not $WaitForExit) { throw 'Close PROD and DEV first, or use -WaitForExit.' }
        if ([DateTime]::UtcNow -ge $deadline) { throw 'Timed out waiting for the user to close both desktops. No installations changed.' }
        Start-Sleep -Seconds 3
    }
    $maintenance = [Threading.Mutex]::new($false, (Get-CsrLifecycleMutexName -AllowedRoot $AllowedRoot))
    try { $maintenanceHeld = $maintenance.WaitOne(0) } catch [Threading.AbandonedMutexException] { $maintenanceHeld = $true }
    if (-not $maintenanceHeld) { throw 'Another installation operation is running.' }
    Assert-CsrRouterStopped -Roots $roots
    # The same name is held by the old router and the new shared service.
    $sharedRoot = Resolve-CsrFullPath ([string]$production.Manifest.sharedStateRoot) -MustExist
    $digest = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($sharedRoot.ToLowerInvariant()))).ToLowerInvariant().Substring(0,32)
    $created = $false
    $registryGuard = [Threading.Mutex]::new($false, "Local\CodexSubscriptionRouter-$digest", [ref]$created)
    if (-not $created) { throw 'A router still owns the shared data. Wait for its normal shutdown.' }
    # Recheck mutable preconditions after the wait; user may have updated an app.
    $production = Read-CsrCandidate $ProductionCandidate 'production'
    $development = Read-CsrCandidate $DevelopmentCandidate 'development'
    Assert-CsrPair $production $development
    $entries = @($production,$development)
    for ($i=0; $i -lt $entries.Count; $i++) {
        if (($entries[$i].Receipt | ConvertTo-Json -Depth 20 -Compress) -cne $initialReceipts[$i]) {
            throw 'A prepared receipt changed while waiting; validate a fresh activation before moving applications.'
        }
    }
    foreach ($entry in $entries) {
        $entry.Backup = Join-Path $operationRoot ('previous-' + [string]$entry.Manifest.installChannel)
        Assert-CsrNoReparseAncestor -Path $entry.Backup -AllowedRoot $AllowedRoot
        if (Test-Path -LiteralPath $entry.Backup) { throw 'A rollback backup already exists. Inspect the prior activation instead of overwriting it.' }
        if ([IO.Path]::GetPathRoot($entry.Backup) -ne [IO.Path]::GetPathRoot($entry.Destination)) { throw 'Backup must use same volume.' }
    }
    $journalPath = Join-Path $sharedRoot 'shared-activation.json'
    if (Test-Path -LiteralPath $journalPath) { $previousJournal = [IO.File]::ReadAllText($journalPath) }
    $dataSnapshot = Save-CsrPreActivationData $production $development $operationRoot
    Write-CsrJsonAtomic -Path (Join-Path $operationRoot 'activation-plan.json') -Value @{
        pairId=$pairId; previousActivation=$previousJournal; sharedRoot=$sharedRoot; dataSnapshot=$dataSnapshot;
        entries=@($entries | ForEach-Object { @{channel=$_.Manifest.installChannel;candidate=$_.Candidate;destination=$_.Destination;stateRoot=$_.StateRoot;backup=$_.Backup;oldManifest=$_.Receipt.expectedInstalledManifestSha256;newManifest=$_.Receipt.preparedManifestSha256} })
    }
    Write-CsrJsonAtomic -Path $journalPath -Value @{pairId=$pairId;state='publishing';updatedAt=[DateTime]::UtcNow.ToString('o')}
    $journalStarted = $true
    foreach ($entry in $entries) {
        Assert-CsrRouterStopped -Roots $roots
        # All absolute paths were authenticated above, within AllowedRoot, and
        # checked for reparse points. Moves preserve the complete prior trees.
        Move-Item -LiteralPath $entry.Destination -Destination $entry.Backup
        $entry.PreviousMoved = $true
        Move-Item -LiteralPath $entry.Candidate -Destination $entry.Destination
        $entry.Published = $true
        Write-CsrJsonAtomic -Path $statusPath -Value @{pairId=$pairId;state='publishing';published=@($entries | Where-Object Published | ForEach-Object {$_.Manifest.installChannel});updatedAt=[DateTime]::UtcNow.ToString('o')}
        [void](Assert-CsrInstallationIntegrity -LayoutPath $entry.Destination -ExpectedDestination $entry.Destination -ExpectedStateRoot $entry.StateRoot)
    }
    Assert-CsrRouterStopped -Roots @($roots + @($entries | ForEach-Object {$_.Backup}))
    Write-CsrJsonAtomic -Path $journalPath -Value @{pairId=$pairId;state='committed';updatedAt=[DateTime]::UtcNow.ToString('o')}
    # This write opens the launch barrier. Never roll back after it: a desktop
    # may already be starting while optional status/log/launch operations run.
    $journalStarted = $false
    $committed = $true
    Write-CsrJsonAtomic -Path $statusPath -Value @{pairId=$pairId;state='committed';backups=@($entries | ForEach-Object {$_.Backup});updatedAt=[DateTime]::UtcNow.ToString('o')}
    $registryGuard.Dispose(); $registryGuard = $null
    Write-Host 'Shared pair activated. Existing accounts, chats, projects and DEV calibration retained in place.'
    if ($LaunchDevelopment) { Start-Process -FilePath (Join-Path $development.Destination 'ChatGPT.exe') -WindowStyle Hidden | Out-Null }
}
catch {
    $failure = $_
    if ($journalStarted) {
        # Attempt both restorations even if one path is locked or inaccessible.
        # Never move into an existing directory (Move-Item could nest a tree).
        $rollbackErrors = [Collections.Generic.List[string]]::new()
        foreach ($entry in @($entries | Sort-Object { $_.Manifest.installChannel })) {
            if ($entry.Published -and (Test-Path -LiteralPath $entry.Destination)) {
                try {
                    if (Test-Path -LiteralPath $entry.Candidate) { throw "Candidate path is occupied: $($entry.Candidate)" }
                    Move-Item -LiteralPath $entry.Destination -Destination $entry.Candidate
                } catch { $rollbackErrors.Add("Preserve $($entry.Manifest.installChannel) candidate: $($_.Exception.Message)") }
            }
            if ($entry.PreviousMoved) {
                try {
                    if (-not (Test-Path -LiteralPath $entry.Backup)) { throw "Previous application backup is missing: $($entry.Backup)" }
                    if (Test-Path -LiteralPath $entry.Destination) { throw "Application destination is occupied: $($entry.Destination)" }
                    Move-Item -LiteralPath $entry.Backup -Destination $entry.Destination
                } catch { $rollbackErrors.Add("Restore $($entry.Manifest.installChannel): $($_.Exception.Message)") }
            }
            try {
                if ((Get-CsrFileHash (Join-Path $entry.Destination 'codex-mux-build.json')) -ne [string]$entry.Receipt.expectedInstalledManifestSha256) {
                    throw 'Restored manifest does not match the prior installation.'
                }
            } catch { $rollbackErrors.Add("Verify $($entry.Manifest.installChannel) rollback: $($_.Exception.Message)") }
        }
        if ($rollbackErrors.Count -eq 0) {
            try {
                if ($null -ne $previousJournal) {
                    Write-CsrJsonAtomic -Path $journalPath -Value ($previousJournal | ConvertFrom-Json)
                } else {
                    Write-CsrJsonAtomic -Path $journalPath -Value @{pairId=$pairId;state='failed';updatedAt=[DateTime]::UtcNow.ToString('o')}
                }
            } catch { $rollbackErrors.Add("Restore activation barrier: $($_.Exception.Message)") }
        }
        if ($rollbackErrors.Count -gt 0) {
            # An abrupt process/OS failure likewise leaves 'publishing' and the
            # durable activation-plan.json. Recovery must verify that plan;
            # this helper never guesses which partial tree to overwrite.
            $recovery = @{pairId=$pairId;state='recovery-required';error=$failure.Exception.Message;rollbackErrors=@($rollbackErrors);plan=(Join-Path $operationRoot 'activation-plan.json');updatedAt=[DateTime]::UtcNow.ToString('o')}
            try { Write-CsrJsonAtomic -Path $journalPath -Value $recovery } catch { Write-Warning "Could not update launch barrier; retain its existing publishing state: $($_.Exception.Message)" }
            try { Write-CsrJsonAtomic -Path $statusPath -Value $recovery } catch { Write-Warning "Could not save recovery status: $($_.Exception.Message)" }
            Write-Warning ('Activation needs manual recovery. Original error: ' + $failure.Exception.Message + '; rollback errors: ' + ($rollbackErrors -join '; '))
        } else {
            try { Write-CsrJsonAtomic -Path $statusPath -Value @{pairId=$pairId;state='rolled-back';error=$failure.Exception.Message;updatedAt=[DateTime]::UtcNow.ToString('o')} }
            catch { Write-Warning "Applications restored; rollback status could not be saved: $($_.Exception.Message)" }
        }
    } elseif ($committed) {
        Write-Warning "The pair is committed and remains installed. A post-commit operation failed: $($failure.Exception.Message)"
        try { Write-CsrJsonAtomic -Path $statusPath -Value @{pairId=$pairId;state='committed';postCommitError=$failure.Exception.Message;backups=@($entries | ForEach-Object {$_.Backup});updatedAt=[DateTime]::UtcNow.ToString('o')} }
        catch { Write-Warning "The committed launch barrier remains authoritative; status update failed: $($_.Exception.Message)" }
    }
    throw $failure
}
finally {
    if ($null -ne $registryGuard) { $registryGuard.Dispose() }
    if ($maintenanceHeld) { $maintenance.ReleaseMutex() }
    if ($null -ne $maintenance) { $maintenance.Dispose() }
    if ($transcriptStarted) { Stop-Transcript | Out-Null }
}
