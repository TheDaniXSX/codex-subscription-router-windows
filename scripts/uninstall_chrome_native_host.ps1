#Requires -Version 5.1

<#
.SYNOPSIS
Removes only exact router-owned Chrome Native Messaging artifacts.

.DESCRIPTION
The registry default value and every installed file are compared with the
ownership receipt before deletion. Missing or modified artifacts are preserved
and reported. Router account state and captured contexts are retained.
#>

[CmdletBinding()]
param(
    [string]$Destination = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router Chrome Connector'),

    [switch]$DryRun,

    [Parameter(DontShow = $true)]
    [string]$RegistryFixtureRoot
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ChromeNativeHost.Common.ps1')

$destinationPath = Resolve-ChromeConnectorPath -Path $Destination
Assert-ChromeConnectorLocalPath -Path $destinationPath
$receiptPath = Join-Path $destinationPath 'ownership-receipt.json'
if (-not (Test-Path -LiteralPath $receiptPath -PathType Leaf)) {
    throw "Ownership receipt is missing; nothing can be removed safely: $receiptPath"
}
$receipt = Get-Content -LiteralPath $receiptPath -Raw | ConvertFrom-Json
$expectedManifestPath = Join-Path $destinationPath "$script:ChromeNativeHostName.json"
$expectedExecutablePath = Join-Path $destinationPath 'codex-router-chrome-host.exe'
$expectedConfigPath = Join-Path $destinationPath 'chrome-native-host.config.json'
$receiptProperties = @($receipt.PSObject.Properties.Name)
if ($receiptProperties -notcontains 'schema' -or $receiptProperties -notcontains 'owner' -or
    $receiptProperties -notcontains 'hostName' -or $receiptProperties -notcontains 'extensionId' -or
    $receiptProperties -notcontains 'registryValue' -or $receiptProperties -notcontains 'files' -or
    $receipt.schema -ne 1 -or
    $receipt.owner -ne 'github.com/TheDaniXSX/codex-subscription-router-windows' -or
    $receipt.hostName -ne $script:ChromeNativeHostName -or
    -not [string]::Equals([string]$receipt.registryValue, $expectedManifestPath, [StringComparison]::Ordinal)) {
    throw 'Ownership receipt is invalid or belongs to another native host.'
}
Assert-ChromeExtensionId -ExtensionId ([string]$receipt.extensionId)
$expectedFiles = [ordered]@{
    executable = $expectedExecutablePath
    config = $expectedConfigPath
    manifest = $expectedManifestPath
}
if (@($receipt.files.PSObject.Properties.Name).Count -ne $expectedFiles.Count) {
    throw 'Ownership receipt contains an unexpected file set.'
}
foreach ($name in $expectedFiles.Keys) {
    $property = $receipt.files.PSObject.Properties[$name]
    if ($null -eq $property) {
        throw "Ownership receipt is missing the $name file."
    }
    $entry = $property.Value
    if (-not [string]::Equals([string]$entry.path, [string]$expectedFiles[$name], [StringComparison]::OrdinalIgnoreCase) -or
        [string]$entry.sha256 -cnotmatch '^[0-9a-f]{64}$') {
        throw "Ownership receipt has an invalid $name path or hash."
    }
}

$actualRegistryValue = Get-ChromeNativeHostRegistryValue -RegistryFixtureRoot $RegistryFixtureRoot
$registryOwned = $null -eq $actualRegistryValue -or
    [string]::Equals([string]$actualRegistryValue, [string]$receipt.registryValue, [StringComparison]::Ordinal)
$plannedFiles = @()
foreach ($entry in @($receipt.files.executable, $receipt.files.config, $receipt.files.manifest)) {
    $entryPath = [string]$entry.path
    $exists = Test-Path -LiteralPath $entryPath -PathType Leaf
    $owned = -not $exists -or
        (Get-ChromeConnectorSha256 -Path $entryPath) -eq ([string]$entry.sha256).ToLowerInvariant()
    $plannedFiles += [PSCustomObject]@{ Entry = $entry; Exists = $exists; Owned = $owned }
}
$hasMismatch = (-not $registryOwned) -or ($plannedFiles.Where({ -not $_.Owned }).Count -gt 0)

$registryResult = if ($hasMismatch) {
    [PSCustomObject]@{ Removed = $false; Reason = $(if ($registryOwned) { 'preserved-with-modified-artifact' } else { 'value-mismatch' }) }
}
else {
    Remove-ChromeNativeHostRegistryValueIfOwned `
        -ExpectedValue ([string]$receipt.registryValue) `
        -RegistryFixtureRoot $RegistryFixtureRoot `
        -DryRun:$DryRun
}
$fileResults = @()
foreach ($planned in $plannedFiles) {
    if ($hasMismatch) {
        $fileResults += [PSCustomObject]@{
            Removed = $false
            Reason = $(if ($planned.Owned) { 'preserved-with-modified-artifact' } else { 'hash-mismatch' })
            Path = [string]$planned.Entry.path
        }
    }
    else {
        $fileResults += Remove-ChromeConnectorFileIfOwned `
            -Path ([string]$planned.Entry.path) `
            -ExpectedSha256 ([string]$planned.Entry.sha256) `
            -DryRun:$DryRun
    }
}
if (-not $DryRun -and -not $hasMismatch) {
    Remove-Item -LiteralPath $receiptPath -Force
    if ((Get-ChildItem -LiteralPath $destinationPath -Force | Measure-Object).Count -eq 0) {
        Remove-Item -LiteralPath $destinationPath -Force
    }
}

[PSCustomObject]@{
    Removed = (-not $DryRun -and -not $hasMismatch)
    Registry = $registryResult
    Files = $fileResults
    ModifiedArtifactsPreserved = $hasMismatch
    StatePreserved = $true
} | ConvertTo-Json -Depth 6

if ($hasMismatch) {
    exit 2
}
