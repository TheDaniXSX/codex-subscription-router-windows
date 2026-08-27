#Requires -Version 5.1

<#
.SYNOPSIS
Atomically restores an authenticated previous Windows router build.

.DESCRIPTION
The selected backup and current app are fully authenticated from their
manifests and hashes before the current app is moved. If publication of the
backup fails, the current app is restored automatically. Persistent account
state is never changed.
#>

[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
param(
    [string]$Destination = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),
    [string]$StateRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router Data'),
    [string]$BackupRoot,
    [string]$BackupPath,
    [string]$ShortcutPath,
    [string]$AllowedRoot = $env:LOCALAPPDATA,
    [Parameter(DontShow = $true)][switch]$TestFailAfterMoveCurrent
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'Continue'
Import-Module (Join-Path $PSScriptRoot 'WindowsLifecycle.psm1') -Force

$transcriptStarted = $false
$mutex = $null
$mutexAcquired = $false
try {
    if ($env:OS -ne 'Windows_NT') { throw 'This rollback command supports Windows only.' }
    $Destination = Resolve-CsrFullPath -Path $Destination
    $StateRoot = Resolve-CsrFullPath -Path $StateRoot
    $AllowedRoot = Resolve-CsrFullPath -Path $AllowedRoot -MustExist
    if ([string]::IsNullOrWhiteSpace($BackupRoot)) {
        $BackupRoot = Join-Path (Split-Path -Parent $Destination) '.codex-subscription-router-backups'
    }
    $BackupRoot = Resolve-CsrFullPath -Path $BackupRoot
    Assert-CsrManagedPaths -Destination $Destination -StateRoot $StateRoot -BackupRoot $BackupRoot -AllowedRoot $AllowedRoot
    [void](Assert-CsrInstallationIntegrity -LayoutPath $Destination -ExpectedDestination $Destination -ExpectedStateRoot $StateRoot)

    $backups = @(Get-CsrBackupEntries -BackupRoot $BackupRoot -Destination $Destination -StateRoot $StateRoot -RequireIntegrity)
    if ([string]::IsNullOrWhiteSpace($BackupPath)) {
        if ($backups.Count -eq 0) { throw "No authenticated rollback backup exists under: $BackupRoot" }
        $selected = $backups[0]
    }
    else {
        $requested = Resolve-CsrFullPath -Path $BackupPath -MustExist
        $selected = $backups | Where-Object {
            $_.Container.Equals($requested, [StringComparison]::OrdinalIgnoreCase) -or
            $_.AppPath.Equals($requested, [StringComparison]::OrdinalIgnoreCase)
        } | Select-Object -First 1
        if ($null -eq $selected) { throw "BackupPath is not an authenticated backup under BackupRoot: $requested" }
    }
    Write-Host "BackupPath: $($selected.AppPath)"
    Assert-CsrRouterStopped -Roots @($Destination, $selected.AppPath)

    if ($TestFailAfterMoveCurrent) {
        $temporaryRoot = Resolve-CsrFullPath -Path ([IO.Path]::GetTempPath())
        if (-not (Test-CsrPathWithin -Candidate $AllowedRoot -Parent $temporaryRoot)) {
            throw 'The hidden failure-injection switch is permitted only for a fixture AllowedRoot under TEMP.'
        }
    }

    $operationApproved = $PSCmdlet.ShouldProcess($Destination, "Replace active router with authenticated backup '$($selected.AppPath)'")
    if (-not $operationApproved) {
        Write-Host "WouldRollbackFrom: $($selected.AppPath)"
        Write-Host "StatePreserved: $StateRoot"
        return
    }

    $mutex = New-Object Threading.Mutex($false, (Get-CsrLifecycleMutexName -AllowedRoot $AllowedRoot))
    try { $mutexAcquired = $mutex.WaitOne(0) }
    catch [Threading.AbandonedMutexException] { $mutexAcquired = $true }
    if (-not $mutexAcquired) { throw 'Another router install or lifecycle operation is already running.' }

    $logRoot = Join-Path $StateRoot 'logs'
    New-Item -ItemType Directory -Path $logRoot -Force | Out-Null
    $logPath = Join-Path $logRoot ("rollback-{0}.log" -f [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmssfff'))
    Start-Transcript -LiteralPath $logPath -Force | Out-Null
    $transcriptStarted = $true
    Write-Host "Transcript: $logPath"

    if ($operationApproved) {
        $transaction = Join-Path $BackupRoot ("{0}-rollback-{1}" -f [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmssfff'), [Guid]::NewGuid().ToString('N').Substring(0, 8))
        $displaced = Join-Path $transaction (Split-Path -Leaf $Destination)
        New-Item -ItemType Directory -Path $transaction | Out-Null
        $currentMoved = $false
        try {
            Move-Item -LiteralPath $Destination -Destination $displaced
            $currentMoved = $true
            if ($TestFailAfterMoveCurrent) { throw 'Injected rollback publication failure for hermetic testing.' }
            Move-Item -LiteralPath $selected.AppPath -Destination $Destination
            [void](Assert-CsrInstallationIntegrity -LayoutPath $Destination -ExpectedDestination $Destination -ExpectedStateRoot $StateRoot)

            $restoredManifestPath = Join-Path $Destination 'codex-mux-build.json'
            $restoredManifest = Read-CsrManifest -LayoutPath $Destination -ExpectedDestination $Destination -ExpectedStateRoot $StateRoot
            if ($null -eq $restoredManifest.PSObject.Properties['backupPath']) {
                $restoredManifest | Add-Member -NotePropertyName backupPath -NotePropertyValue $displaced
            }
            else { $restoredManifest.backupPath = $displaced }
            Write-CsrJsonAtomic -Value $restoredManifest -Path $restoredManifestPath

            if ((Test-Path -LiteralPath $selected.Container -PathType Container) -and
                @(Get-ChildItem -LiteralPath $selected.Container -Force).Count -eq 0) {
                Remove-Item -LiteralPath $selected.Container -Force
            }
            Write-Host "Rollback completed. BackupPath: $displaced" -ForegroundColor Green
        }
        catch {
            $failure = $_.Exception.Message
            if ($currentMoved) {
                if (Test-Path -LiteralPath $Destination) {
                    $failedPublish = Join-Path $transaction ("failed-publish-{0}" -f [Guid]::NewGuid().ToString('N'))
                    Move-Item -LiteralPath $Destination -Destination $failedPublish
                }
                if (Test-Path -LiteralPath $displaced -PathType Container) {
                    Move-Item -LiteralPath $displaced -Destination $Destination
                }
                if ((Test-Path -LiteralPath $transaction -PathType Container) -and
                    @(Get-ChildItem -LiteralPath $transaction -Force).Count -eq 0) {
                    Remove-Item -LiteralPath $transaction -Force
                }
            }
            throw "$failure The pre-rollback installation was restored automatically."
        }
    }
    if (-not [string]::IsNullOrWhiteSpace($ShortcutPath)) {
        Write-Host "ShortcutPreserved: $(Resolve-CsrFullPath -Path $ShortcutPath)"
    }
    Write-Host "StatePreserved: $StateRoot"
}
finally {
    if ($transcriptStarted) { Stop-Transcript | Out-Null }
    if ($null -ne $mutex) {
        if ($mutexAcquired) { try { $mutex.ReleaseMutex() } catch { Write-Warning $_.Exception.Message } }
        $mutex.Dispose()
    }
}
