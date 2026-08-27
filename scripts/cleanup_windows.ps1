#Requires -Version 5.1

<#
.SYNOPSIS
Safely removes obsolete, authenticated Windows router backups.

.DESCRIPTION
Keeps the active installation and the newest authenticated backup by default.
Every removal candidate must be inside AllowedRoot, must not cross a reparse
point, and must contain a router manifest whose destination, state path, and
current file hashes match. The official Codex package is never inspected or
modified.
#>

[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
param(
    [string]$Destination = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),
    [string]$StateRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router Data'),
    [string]$BackupRoot,
    [string]$AllowedRoot = $env:LOCALAPPDATA,
    [ValidateRange(0, 100)][int]$KeepBackups = 1,
    [ValidateRange(0, 100)][int]$KeepFailedInstallations = 0,
    [switch]$IncludeFailedInstallations
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'Continue'
Import-Module (Join-Path $PSScriptRoot 'WindowsLifecycle.psm1') -Force

$transcriptStarted = $false
$mutex = $null
$mutexAcquired = $false
try {
    if ($env:OS -ne 'Windows_NT') { throw 'This cleanup command supports Windows only.' }
    $Destination = Resolve-CsrFullPath -Path $Destination
    $StateRoot = Resolve-CsrFullPath -Path $StateRoot
    $AllowedRoot = Resolve-CsrFullPath -Path $AllowedRoot -MustExist
    if ([string]::IsNullOrWhiteSpace($BackupRoot)) {
        $BackupRoot = Join-Path (Split-Path -Parent $Destination) '.codex-subscription-router-backups'
    }
    $BackupRoot = Resolve-CsrFullPath -Path $BackupRoot
    Assert-CsrManagedPaths -Destination $Destination -StateRoot $StateRoot -BackupRoot $BackupRoot -AllowedRoot $AllowedRoot

    $activeManifest = Assert-CsrInstallationIntegrity -LayoutPath $Destination -ExpectedDestination $Destination -ExpectedStateRoot $StateRoot
    Assert-CsrRouterStopped -Roots @($Destination)

    if (-not $WhatIfPreference) {
        $mutex = New-Object Threading.Mutex($false, (Get-CsrLifecycleMutexName -AllowedRoot $AllowedRoot))
        try { $mutexAcquired = $mutex.WaitOne(0) }
        catch [Threading.AbandonedMutexException] { $mutexAcquired = $true }
        if (-not $mutexAcquired) { throw 'Another router install or lifecycle operation is already running.' }
    }

    if (-not $WhatIfPreference) {
        $logRoot = Join-Path $StateRoot 'logs'
        New-Item -ItemType Directory -Path $logRoot -Force | Out-Null
        $logPath = Join-Path $logRoot ("cleanup-{0}.log" -f [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmssfff'))
        Start-Transcript -LiteralPath $logPath -Force | Out-Null
        $transcriptStarted = $true
        Write-Host "Transcript: $logPath"
    }

    $backups = @(Get-CsrBackupEntries -BackupRoot $BackupRoot -Destination $Destination -StateRoot $StateRoot -RequireIntegrity)
    Write-Host "Authenticated backups: $($backups.Count); retaining newest: $KeepBackups"
    $toRemove = @($backups | Select-Object -Skip $KeepBackups)
    foreach ($entry in $toRemove) {
        $children = @(Get-ChildItem -LiteralPath $entry.Container -Force)
        if ($children.Count -ne 1 -or -not $children[0].FullName.Equals($entry.AppPath, [StringComparison]::OrdinalIgnoreCase)) {
            Write-Warning "Skipping backup container with unexpected contents: $($entry.Container)"
            continue
        }
        if ($PSCmdlet.ShouldProcess($entry.Container, 'Remove obsolete authenticated router backup')) {
            Remove-Item -LiteralPath $entry.Container -Recurse -Force
            Write-Host "RemovedBackup: $($entry.Container)"
        }
    }

    $remaining = @($backups | Select-Object -First $KeepBackups)
    $newBackupPath = if ($remaining.Count -gt 0) { [string]$remaining[0].AppPath } else { $null }
    $currentBackupPath = if ($null -ne $activeManifest.PSObject.Properties['backupPath']) { [string]$activeManifest.backupPath } else { '' }
    if ($currentBackupPath -ne [string]$newBackupPath -and
        $PSCmdlet.ShouldProcess((Join-Path $Destination 'codex-mux-build.json'), 'Update retained rollback path atomically')) {
        if ($null -eq $activeManifest.PSObject.Properties['backupPath']) {
            $activeManifest | Add-Member -NotePropertyName backupPath -NotePropertyValue $newBackupPath
        }
        else {
            $activeManifest.backupPath = $newBackupPath
        }
        Write-CsrJsonAtomic -Value $activeManifest -Path (Join-Path $Destination 'codex-mux-build.json')
        Write-Host "BackupPath: $newBackupPath"
    }

    if ($IncludeFailedInstallations) {
        $failedRoot = Join-Path $StateRoot 'failed-installations'
        $authenticatedFailed = [Collections.Generic.List[object]]::new()
        if (Test-Path -LiteralPath $failedRoot -PathType Container) {
            foreach ($candidate in @(Get-ChildItem -LiteralPath $failedRoot -Directory -Force | Sort-Object LastWriteTimeUtc -Descending)) {
                try {
                    [void](Assert-CsrInstallationIntegrity -LayoutPath $candidate.FullName -ExpectedDestination $Destination -ExpectedStateRoot $StateRoot)
                    $authenticatedFailed.Add($candidate)
                }
                catch {
                    Write-Warning "Ignoring unauthenticated failed build '$($candidate.FullName)': $($_.Exception.Message)"
                }
            }
        }
        foreach ($candidate in @($authenticatedFailed | Select-Object -Skip $KeepFailedInstallations)) {
            if ($PSCmdlet.ShouldProcess($candidate.FullName, 'Remove authenticated failed installation')) {
                Remove-Item -LiteralPath $candidate.FullName -Recurse -Force
                Write-Host "RemovedFailedInstallation: $($candidate.FullName)"
            }
        }
    }

    Write-Host 'Cleanup completed. The active router and its persistent account state were preserved.' -ForegroundColor Green
}
finally {
    if ($transcriptStarted) { Stop-Transcript | Out-Null }
    if ($null -ne $mutex) {
        if ($mutexAcquired) { try { $mutex.ReleaseMutex() } catch { Write-Warning $_.Exception.Message } }
        $mutex.Dispose()
    }
}
