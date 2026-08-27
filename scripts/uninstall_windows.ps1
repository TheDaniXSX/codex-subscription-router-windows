#Requires -Version 5.1

<#
.SYNOPSIS
Safely uninstalls the independent Windows router.

.DESCRIPTION
Authenticates the app from its manifest and current hashes, refuses to act
while router-owned executables are running, and removes only the exact shortcut
that targets this installation. Account state and backups are preserved unless
RemoveState or RemoveBackups is explicitly requested.
#>

[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
param(
    [string]$Destination = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),
    [string]$StateRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router Data'),
    [string]$BackupRoot,
    [string]$ShortcutPath = (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Codex Subscription Router.lnk'),
    [string]$AllowedRoot = $env:LOCALAPPDATA,
    [string]$ShortcutAllowedRoot = $env:APPDATA,
    [switch]$RemoveState,
    [switch]$RemoveBackups,
    [Parameter(DontShow = $true)][string]$ShellIntegrationScript
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'Continue'
Import-Module (Join-Path $PSScriptRoot 'WindowsLifecycle.psm1') -Force

$transcriptStarted = $false
$mutex = $null
$mutexAcquired = $false
try {
    if ($env:OS -ne 'Windows_NT') { throw 'This uninstall command supports Windows only.' }
    $Destination = Resolve-CsrFullPath -Path $Destination
    $StateRoot = Resolve-CsrFullPath -Path $StateRoot
    $AllowedRoot = Resolve-CsrFullPath -Path $AllowedRoot -MustExist
    $ShortcutAllowedRoot = Resolve-CsrFullPath -Path $ShortcutAllowedRoot -MustExist
    if ([string]::IsNullOrWhiteSpace($BackupRoot)) {
        $BackupRoot = Join-Path (Split-Path -Parent $Destination) '.codex-subscription-router-backups'
    }
    $BackupRoot = Resolve-CsrFullPath -Path $BackupRoot
    Assert-CsrManagedPaths -Destination $Destination -StateRoot $StateRoot -BackupRoot $BackupRoot -AllowedRoot $AllowedRoot
    if (-not [string]::IsNullOrWhiteSpace($ShortcutPath)) {
        $ShortcutPath = Resolve-CsrFullPath -Path $ShortcutPath
        if ($ShortcutPath.Equals($ShortcutAllowedRoot, [StringComparison]::OrdinalIgnoreCase) -or
            -not (Test-CsrPathWithin -Candidate $ShortcutPath -Parent $ShortcutAllowedRoot)) {
            throw "ShortcutPath must be a strict child of ShortcutAllowedRoot '$ShortcutAllowedRoot': $ShortcutPath"
        }
        Assert-CsrNoReparseAncestor -Path $ShortcutPath -AllowedRoot $ShortcutAllowedRoot
    }
    [void](Assert-CsrInstallationIntegrity -LayoutPath $Destination -ExpectedDestination $Destination -ExpectedStateRoot $StateRoot)
    Assert-CsrRouterStopped -Roots @($Destination)

    $authenticatedBackups = @(Get-CsrBackupEntries -BackupRoot $BackupRoot -Destination $Destination -StateRoot $StateRoot -RequireIntegrity)
    if ($RemoveBackups -and (Test-Path -LiteralPath $BackupRoot -PathType Container)) {
        $allContainers = @(Get-ChildItem -LiteralPath $BackupRoot -Directory -Force)
        if ($authenticatedBackups.Count -ne $allContainers.Count) {
            throw 'RemoveBackups was requested, but BackupRoot contains an unauthenticated or unexpected directory. Run cleanup or inspect it manually.'
        }
        foreach ($entry in $authenticatedBackups) {
            $children = @(Get-ChildItem -LiteralPath $entry.Container -Force)
            if ($children.Count -ne 1 -or -not $children[0].FullName.Equals($entry.AppPath, [StringComparison]::OrdinalIgnoreCase)) {
                throw "RemoveBackups was requested, but a backup container has unexpected contents: $($entry.Container)"
            }
        }
    }

    $shortcutOwned = $false
    if (-not [string]::IsNullOrWhiteSpace($ShortcutPath) -and (Test-Path -LiteralPath $ShortcutPath -PathType Leaf)) {
        try {
            $shell = New-Object -ComObject WScript.Shell
            $shortcut = $shell.CreateShortcut($ShortcutPath)
            $target = Resolve-CsrFullPath -Path ([string]$shortcut.TargetPath)
            [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut) | Out-Null
            [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell) | Out-Null
            $launcherCandidates = @(
                (Resolve-CsrFullPath -Path (Join-Path $Destination 'ChatGPT.exe')),
                (Resolve-CsrFullPath -Path (Join-Path $Destination 'Codex.exe'))
            )
            $shortcutOwned = $launcherCandidates -contains $target
            if (-not $shortcutOwned) { Write-Warning "Shortcut was preserved because it does not target this router: $ShortcutPath" }
        }
        catch {
            throw "Could not safely inspect shortcut '$ShortcutPath': $($_.Exception.Message)"
        }
    }

    if ([string]::IsNullOrWhiteSpace($ShellIntegrationScript)) {
        $shellIntegrationScript = Join-Path $PSScriptRoot 'windows\Manage-ShellIntegration.ps1'
    }
    else {
        $temporaryRoot = Resolve-CsrFullPath -Path ([IO.Path]::GetTempPath())
        if (-not (Test-CsrPathWithin -Candidate $AllowedRoot -Parent $temporaryRoot)) {
            throw 'A ShellIntegrationScript override is permitted only for a fixture AllowedRoot under TEMP.'
        }
        $shellIntegrationScript = Resolve-CsrFullPath -Path $ShellIntegrationScript -MustExist
        if (-not (Test-CsrPathWithin -Candidate $shellIntegrationScript -Parent $AllowedRoot)) {
            throw 'The fixture ShellIntegrationScript must be inside AllowedRoot.'
        }
        Assert-CsrNoReparseAncestor -Path $shellIntegrationScript -AllowedRoot $AllowedRoot
    }
    if (-not (Test-Path -LiteralPath $shellIntegrationScript -PathType Leaf)) {
        throw "Shell integration manager is missing; cannot safely compare-and-delete router-owned registrations: $shellIntegrationScript"
    }

    $uninstallDescription = "Uninstall authenticated router; RemoveState=$([bool]$RemoveState); RemoveBackups=$([bool]$RemoveBackups)"
    $operationApproved = $PSCmdlet.ShouldProcess($Destination, $uninstallDescription)
    if (-not $operationApproved) {
        if ($WhatIfPreference) {
            & $shellIntegrationScript `
                -Action Unregister `
                -Feature All `
                -LauncherPath (Join-Path $Destination 'ChatGPT.exe') `
                -WhatIf `
                -Confirm:$false
            Write-Host "WouldRemoveApplication: $Destination"
            if ($RemoveBackups) { Write-Host "WouldRemoveBackups: $BackupRoot" }
            if ($RemoveState) { Write-Host "WouldRemoveState: $StateRoot" }
            else { Write-Host "StatePreserved: $StateRoot" }
        }
        return
    }

    $mutex = New-Object Threading.Mutex($false, (Get-CsrLifecycleMutexName -AllowedRoot $AllowedRoot))
    try { $mutexAcquired = $mutex.WaitOne(0) }
    catch [Threading.AbandonedMutexException] { $mutexAcquired = $true }
    if (-not $mutexAcquired) { throw 'Another router install or lifecycle operation is already running.' }

    $logRoot = if ($RemoveState) { Join-Path $env:TEMP 'Codex Subscription Router\logs' } else { Join-Path $StateRoot 'logs' }
    New-Item -ItemType Directory -Path $logRoot -Force | Out-Null
    $logPath = Join-Path $logRoot ("uninstall-{0}.log" -f [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmssfff'))
    Start-Transcript -LiteralPath $logPath -Force | Out-Null
    $transcriptStarted = $true
    Write-Host "Transcript: $logPath"
    Write-Host 'Unregistering only exact router-owned protocol and Explorer integration, if present.'
    & $shellIntegrationScript `
        -Action Unregister `
        -Feature All `
        -LauncherPath (Join-Path $Destination 'ChatGPT.exe') `
        -WhatIf:$false `
        -Confirm:$false

    if ($shortcutOwned) {
        Remove-Item -LiteralPath $ShortcutPath -Force
        Write-Host "RemovedShortcut: $ShortcutPath"
    }
    Remove-Item -LiteralPath $Destination -Recurse -Force
    Write-Host "RemovedApplication: $Destination"
    if ($RemoveBackups -and (Test-Path -LiteralPath $BackupRoot)) {
        Remove-Item -LiteralPath $BackupRoot -Recurse -Force
        Write-Host "RemovedBackups: $BackupRoot"
    }

    if ($RemoveState) {
        if ($transcriptStarted) {
            Stop-Transcript | Out-Null
            $transcriptStarted = $false
        }
        if (Test-Path -LiteralPath $StateRoot) {
            Remove-Item -LiteralPath $StateRoot -Recurse -Force
            Write-Host "RemovedState: $StateRoot"
        }
    }
    else {
        Write-Host "StatePreserved: $StateRoot"
    }
    Write-Host 'Uninstall completed. The official Codex installation was not modified.' -ForegroundColor Green
}
finally {
    if ($transcriptStarted) { Stop-Transcript | Out-Null }
    if ($null -ne $mutex) {
        if ($mutexAcquired) { try { $mutex.ReleaseMutex() } catch { Write-Warning $_.Exception.Message } }
        $mutex.Dispose()
    }
}
