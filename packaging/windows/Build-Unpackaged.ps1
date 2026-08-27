#requires -Version 7.2

<#
.SYNOPSIS
Creates an independent, unpackaged Windows payload without modifying the source.

.DESCRIPTION
Accepts either a package-root layout (app\ChatGPT.exe) or the app-root layout
created by patch_windows_app.py (ChatGPT.exe). The output always uses a stable
package-root layout with app\ChatGPT.exe and optional assets. Existing output is
never overwritten unless -Overwrite is explicit; the previous output is retained
as a timestamped backup.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string] $SourceRoot,

    [Parameter(Mandatory)]
    [string] $OutputPath,

    [string] $AssetsPath,

    [string] $Version,

    [switch] $Overwrite,

    [switch] $AllowUnpatchedPayload
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path -Path $PSScriptRoot -ChildPath 'Common.ps1')

if ([string]::IsNullOrWhiteSpace($Version)) {
    $versionFile = Resolve-RouterAbsolutePath -Path (Join-Path $PSScriptRoot '..\..\VERSION') -MustExist
    $Version = (Get-Content -LiteralPath $versionFile -Raw -ErrorAction Stop).Trim()
}
if ($Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$') {
    throw "Version must be a SemVer-compatible project version: $Version"
}

$layout = Get-RouterPayloadLayout -SourceRoot $SourceRoot
Assert-RouterPatchedPayload -AppRoot $layout.AppRoot -AllowUnpatchedPayload:$AllowUnpatchedPayload
$output = Assert-RouterSafeOutputPath -OutputPath $OutputPath -SourcePath $layout.SourceRoot

if ((Test-Path -LiteralPath $output) -and -not $Overwrite) {
    throw "Output already exists. Choose another path or pass -Overwrite: $output"
}

$outputParent = Split-Path -Path $output -Parent
$outputName = Split-Path -Path $output -Leaf
New-Item -ItemType Directory -Path $outputParent -Force | Out-Null
$staging = Join-Path -Path $outputParent -ChildPath ".$outputName.staging.$([guid]::NewGuid().ToString('N'))"
$backup = $null

try {
    New-Item -ItemType Directory -Path $staging | Out-Null
    Assert-RouterTreeWithoutReparsePoint -Path $staging
    Copy-RouterTreeVerified -Source $layout.AppRoot -Destination (Join-Path $staging 'app')

    $resolvedAssets = $null
    if ($AssetsPath) {
        $resolvedAssets = Resolve-RouterAbsolutePath -Path $AssetsPath -MustExist
    }
    elseif ($layout.AssetsRoot -and (Test-Path -LiteralPath $layout.AssetsRoot -PathType Container)) {
        $resolvedAssets = $layout.AssetsRoot
    }
    if ($resolvedAssets) {
        Assert-RouterTreeWithoutReparsePoint -Path $resolvedAssets
        Copy-RouterTreeVerified -Source $resolvedAssets -Destination (Join-Path $staging 'assets')
    }

    $appRoot = Join-Path -Path $staging -ChildPath 'app'
    $metadata = [ordered]@{
        schemaVersion = 2
        kind = 'windows-unpackaged'
        displayName = 'Codex Subscription Router'
        version = $Version
        createdAtUtc = [DateTimeOffset]::UtcNow.ToString('O')
        sourceLayout = $layout.Layout
        launchTarget = 'app\ChatGPT.exe'
        routerExecutable = 'app\resources\codex.exe'
        originalCodexExecutable = 'app\resources\codex.real.exe'
        filesManifest = 'router-package.files.json'
        hashes = [ordered]@{
            launcher = Get-RouterFileHashOrNull -Path (Join-Path $appRoot 'ChatGPT.exe')
            originalDesktop = Get-RouterFileHashOrNull -Path (Join-Path $appRoot 'ChatGPT.real.exe')
            appAsar = Get-RouterFileHashOrNull -Path (Join-Path $appRoot 'resources\app.asar')
            router = Get-RouterFileHashOrNull -Path (Join-Path $appRoot 'resources\codex.exe')
            originalCodex = Get-RouterFileHashOrNull -Path (Join-Path $appRoot 'resources\codex.real.exe')
        }
    }
    $metadataJson = $metadata | ConvertTo-Json -Depth 5
    [IO.File]::WriteAllText(
        (Join-Path $staging 'router-package.json'),
        $metadataJson + [Environment]::NewLine,
        [Text.UTF8Encoding]::new($false)
    )
    [void] (Write-RouterTreeHashManifest `
        -Root $staging `
        -ManifestRelativePath 'router-package.files.json' `
        -Kind 'windows-unpackaged-files')
    Assert-RouterTreeHashManifest -Root $staging -ManifestRelativePath 'router-package.files.json'

    if (Test-Path -LiteralPath $output) {
        Assert-RouterTreeWithoutReparsePoint -Path $output
        if (Test-Path -LiteralPath (Join-Path $output 'router-package.files.json') -PathType Leaf) {
            Assert-RouterTreeHashManifest -Root $output -ManifestRelativePath 'router-package.files.json'
        }
        $backup = "$output.backup.$([DateTimeOffset]::UtcNow.ToString('yyyyMMddTHHmmssfffZ'))"
        if (Test-Path -LiteralPath $backup) {
            throw "Refusing to overwrite an existing packaging backup: $backup"
        }
        Move-Item -LiteralPath $output -Destination $backup
    }

    try {
        Move-Item -LiteralPath $staging -Destination $output
        Assert-RouterTreeHashManifest -Root $output -ManifestRelativePath 'router-package.files.json'
    }
    catch {
        if (Test-Path -LiteralPath $output) {
            Remove-RouterTreeSafely -Path $output
        }
        if ($backup -and (Test-Path -LiteralPath $backup)) {
            Move-Item -LiteralPath $backup -Destination $output
            $backup = $null
        }
        throw
    }
}
finally {
    if (Test-Path -LiteralPath $staging) {
        Remove-RouterTreeSafely -Path $staging
    }
}

[pscustomobject]@{
    Kind = 'windows-unpackaged'
    OutputPath = $output
    LaunchTarget = Join-Path -Path $output -ChildPath 'app\ChatGPT.exe'
    HashManifest = Join-Path -Path $output -ChildPath 'router-package.files.json'
    PreviousOutputBackup = $backup
}
