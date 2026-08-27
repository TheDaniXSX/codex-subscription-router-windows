#Requires -Version 5.1

<#
.SYNOPSIS
Verifies repository safety or the public-release Chrome connector gate.

.DESCRIPTION
RepositoryOnly verifies the independent source without touching Chrome or the
registry. ReleaseGate additionally requires a published extension ID, an exact
installed manifest/registry value, a valid Authenticode signature, and a clean
VM qualification report. The gate intentionally cannot pass on source alone.
#>

[CmdletBinding(DefaultParameterSetName = 'Repository')]
param(
    [Parameter(ParameterSetName = 'Repository')]
    [switch]$RepositoryOnly,

    [Parameter(Mandatory = $true, ParameterSetName = 'Release')]
    [switch]$ReleaseGate,

    [Parameter(Mandatory = $true, ParameterSetName = 'Release')]
    [string]$ExtensionId,

    [Parameter(Mandatory = $true, ParameterSetName = 'Release')]
    [string]$ManifestPath,

    [Parameter(Mandatory = $true, ParameterSetName = 'Release')]
    [string]$HostBinary,

    [Parameter(Mandatory = $true, ParameterSetName = 'Release')]
    [string]$QualificationReport,

    [Parameter(Mandatory = $true, ParameterSetName = 'Release')]
    [string]$PublishedStoreUrl,

    [Parameter(DontShow = $true)]
    [string]$RegistryFixtureRoot
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ChromeNativeHost.Common.ps1')
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$checks = [Collections.Generic.List[object]]::new()

function Add-ConnectorCheck {
    param([string]$Name, [bool]$Passed, [string]$Detail)
    $script:checks.Add([PSCustomObject]@{ Name = $Name; Passed = $Passed; Detail = $Detail })
}

$extensionManifestPath = Join-Path $projectRoot 'chrome-extension\manifest.json'
$extensionManifest = Get-Content -LiteralPath $extensionManifestPath -Raw | ConvertFrom-Json
Add-ConnectorCheck 'Independent host name' `
    ($script:ChromeNativeHostName -eq 'io.github.thedanixsx.codex_subscription_router') `
    $script:ChromeNativeHostName
Add-ConnectorCheck 'Manifest V3 extension source' `
    ($extensionManifest.manifest_version -eq 3) `
    $extensionManifestPath
$permissions = @($extensionManifest.permissions)
Add-ConnectorCheck 'Least-privilege activeTab permission' `
    ($permissions.Count -eq 3 -and ($permissions -contains 'activeTab') -and
        ($permissions -contains 'nativeMessaging') -and ($permissions -contains 'scripting')) `
    ($permissions -join ', ')
Add-ConnectorCheck 'No global host permissions' `
    ($extensionManifest.PSObject.Properties.Name -notcontains 'host_permissions') `
    'The extension captures only a user-activated tab.'
Add-ConnectorCheck 'No unpublished identity is pinned' `
    ($extensionManifest.PSObject.Properties.Name -notcontains 'key' -and
        $extensionManifest.PSObject.Properties.Name -notcontains 'update_url') `
    'Chrome Web Store must assign the production identity.'

$sourceFiles = @(
    (Join-Path $projectRoot 'chrome-extension\manifest.json'),
    (Join-Path $projectRoot 'chrome-extension\service-worker.js'),
    (Join-Path $projectRoot 'internal\chromenative\protocol.go'),
    (Join-Path $projectRoot 'scripts\install_chrome_native_host.ps1'),
    (Join-Path $projectRoot 'scripts\uninstall_chrome_native_host.ps1')
)
$forbiddenOfficialIdentifier = 'com' + '.openai' + '.codexextension'
$officialReferences = @($sourceFiles | Where-Object {
    (Get-Content -LiteralPath $_ -Raw).Contains($forbiddenOfficialIdentifier)
})
Add-ConnectorCheck 'No official host identifier in connector implementation' `
    ($officialReferences.Count -eq 0) `
    $(if ($officialReferences.Count -eq 0) { 'No collision anchors found.' } else { $officialReferences -join ', ' })

if ($ReleaseGate) {
    Assert-ChromeExtensionId -ExtensionId $ExtensionId
    $manifestFullPath = Resolve-ChromeConnectorPath -Path $ManifestPath
    $hostFullPath = Resolve-ChromeConnectorPath -Path $HostBinary
    $reportFullPath = Resolve-ChromeConnectorPath -Path $QualificationReport
    foreach ($required in @($manifestFullPath, $hostFullPath, $reportFullPath)) {
        if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
            throw "Required release evidence is missing: $required"
        }
    }
    $nativeManifest = Get-Content -LiteralPath $manifestFullPath -Raw | ConvertFrom-Json
    $allowedOrigins = @($nativeManifest.allowed_origins)
    Add-ConnectorCheck 'Release manifest host name' ($nativeManifest.name -eq $script:ChromeNativeHostName) ([string]$nativeManifest.name)
    Add-ConnectorCheck 'Single published allowed origin' `
        ($allowedOrigins.Count -eq 1 -and $allowedOrigins[0] -ceq "chrome-extension://$ExtensionId/") `
        ($allowedOrigins -join ', ')
    Add-ConnectorCheck 'Manifest executable target' `
        ([string]::Equals([string]$nativeManifest.path, $hostFullPath, [StringComparison]::OrdinalIgnoreCase)) `
        ([string]$nativeManifest.path)
    $registryValue = Get-ChromeNativeHostRegistryValue -RegistryFixtureRoot $RegistryFixtureRoot
    Add-ConnectorCheck 'Exact HKCU registration' `
        ([string]::Equals([string]$registryValue, $manifestFullPath, [StringComparison]::Ordinal)) `
        ([string]$registryValue)
    $signature = Get-AuthenticodeSignature -LiteralPath $hostFullPath
    Add-ConnectorCheck 'Signed native host' ($signature.Status -eq 'Valid') ([string]$signature.Status)

    $expectedStoreSuffix = "/$ExtensionId"
    $storeUri = $null
    $storeValid = [Uri]::TryCreate($PublishedStoreUrl, [UriKind]::Absolute, [ref]$storeUri) -and
        $storeUri.Scheme -eq 'https' -and
        $storeUri.Host -eq 'chromewebstore.google.com' -and
        $storeUri.AbsolutePath.EndsWith($expectedStoreSuffix, [StringComparison]::Ordinal)
    Add-ConnectorCheck 'Published Chrome Web Store URL' $storeValid $PublishedStoreUrl

    $report = Get-Content -LiteralPath $reportFullPath -Raw | ConvertFrom-Json
    $requiredScenarios = @('install', 'hello', 'health', 'capture-http', 'reject-non-http', 'desktop-consume', 'restart', 'upgrade', 'compare-and-delete', 'official-invariance')
    $reportedScenarios = @($report.scenarios)
    $scenariosPass = ($requiredScenarios | Where-Object { $reportedScenarios -notcontains $_ }).Count -eq 0
    Add-ConnectorCheck 'Clean Windows VM qualification' `
        ($report.schema -eq 1 -and $report.cleanVm -eq $true -and $report.extensionId -ceq $ExtensionId -and $scenariosPass) `
        ($reportedScenarios -join ', ')
}
elseif ($RepositoryOnly) {
    Add-ConnectorCheck 'Public extension identity configured' $false 'Blocked until Chrome Web Store assigns the production ID.'
    Add-ConnectorCheck 'Clean-VM release qualification' $false 'Blocked until signed-host E2E evidence is supplied.'
}

$checks | Format-Table -AutoSize
$failed = @($checks | Where-Object { -not $_.Passed })
if ($ReleaseGate -and $failed.Count -gt 0) {
    throw "Chrome connector release gate failed: $($failed.Count) check(s) did not pass."
}
if (-not $ReleaseGate) {
    [PSCustomObject]@{
        RepositorySafe = (@($checks | Where-Object { $_.Name -notlike 'Public*' -and $_.Name -notlike 'Clean-VM*' -and -not $_.Passed }).Count -eq 0)
        ReadyForPublicRelease = $false
        Blockers = @('published-extension-id', 'signed-host', 'clean-vm-e2e')
    } | ConvertTo-Json -Depth 4
}
else {
    throw 'Select either -RepositoryOnly or -ReleaseGate.'
}
