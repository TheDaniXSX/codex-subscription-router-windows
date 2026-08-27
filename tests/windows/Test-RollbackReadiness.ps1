[CmdletBinding()]
param(
    [string]$AppRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),
    [string]$BackupRoot,
    [string]$RepositoryRoot
)

. (Join-Path $PSScriptRoot 'Test-Helpers.ps1')

$repo = Resolve-RepositoryRoot $RepositoryRoot
if (-not (Test-Path -LiteralPath $AppRoot -PathType Container)) {
    throw "Installed router directory was not found: $AppRoot"
}
if ([string]::IsNullOrWhiteSpace($BackupRoot)) {
    $BackupRoot = Join-Path (Split-Path -Parent $AppRoot) '.codex-subscription-router-backups'
}

$resolvedApp = (Resolve-Path -LiteralPath $AppRoot).Path
$resolvedBackup = [IO.Path]::GetFullPath($BackupRoot)
if ($resolvedApp.Equals($resolvedBackup, [StringComparison]::OrdinalIgnoreCase) -or
    $resolvedApp.StartsWith($resolvedBackup + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or
    $resolvedBackup.StartsWith($resolvedApp + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Application and backup roots must not contain each other.'
}

$backups = if (Test-Path -LiteralPath $resolvedBackup -PathType Container) {
    @(Get-ChildItem -LiteralPath $resolvedBackup -Directory | Sort-Object LastWriteTimeUtc -Descending)
}
else { @() }

$latest = $null
$payload = @()
$rollbackMode = 'fresh-install removal'
if ($backups.Count -gt 0) {
    $latest = $backups[0]
    $payload = @(Get-ChildItem -LiteralPath $latest.FullName -Recurse -File)
    if ($payload.Count -eq 0) {
        throw "Latest rollback backup is empty: $($latest.FullName)"
    }
    $preservedBinary = @($payload | Where-Object Name -In @('codex.exe', 'codex.real.exe', 'app.asar'))
    if ($preservedBinary.Count -eq 0) {
        throw "Latest rollback backup contains no recognizable application payload: $($latest.FullName)"
    }
    $rollbackMode = 'previous-build restore'
}
elseif (-not (Test-Path -LiteralPath (Join-Path $resolvedApp 'codex-mux-build.json') -PathType Leaf)) {
    throw 'No prior backup exists and the current fresh-install provenance manifest is missing.'
}

$rollbackScripts = @(Get-ChildItem -LiteralPath (Join-Path $repo 'scripts') -Filter '*.ps1' -File | Where-Object {
    (Get-Content -Raw -LiteralPath $_.FullName) -match '(?i)rollback|restore|failed-installations'
})
if ($rollbackScripts.Count -eq 0) {
    throw 'No rollback or restore script exists under scripts/.'
}
foreach ($script in $rollbackScripts) {
    $tokens = $null
    $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($script.FullName, [ref]$tokens, [ref]$errors)
    if ($errors.Count -gt 0) {
        throw "Rollback script has PowerShell parse errors: $($script.FullName)"
    }
    $text = $ast.Extent.Text
    if ($text -notmatch 'SupportsShouldProcess|\$WhatIfPreference|-WhatIf') {
        Write-Warning "Rollback script does not advertise a dry-run/ShouldProcess gate: $($script.Name)"
    }
}

Write-Host "Rollback mode: $rollbackMode"
if ($null -ne $latest) {
    Write-Host "Latest recoverable backup: $($latest.FullName)"
    Write-Host "Backup files: $($payload.Count)"
}
else {
    Write-Host 'INFO  No previous build exists; rollback means moving the fresh independent app aside while preserving state.'
}
Write-Host "Rollback scripts: $($rollbackScripts.Name -join ', ')"
Write-SmokePass 'rollback prerequisites are present; no restore was performed'
