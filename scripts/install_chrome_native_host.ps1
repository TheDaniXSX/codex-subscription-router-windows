#Requires -Version 5.1

<#
.SYNOPSIS
Builds and registers the router-owned Chrome Native Messaging host per user.

.DESCRIPTION
Registration is deliberately opt-in. ExtensionId is mandatory because a
Chrome Web Store ID cannot be inferred safely from source. The script uses a
router-owned host name and refuses to overwrite a different registry value.
It does not install an extension or open Chrome.
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$ExtensionId,

    [string]$Destination = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router Chrome Connector'),

    [string]$LauncherConfig = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router\resources\codex-router\launcher-config.json'),

    [string]$BuildManifest = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router\codex-mux-build.json'),

    [string]$HostBinary,

    [switch]$DryRun,

    [Parameter(DontShow = $true)]
    [string]$RegistryFixtureRoot
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ChromeNativeHost.Common.ps1')

Assert-ChromeExtensionId -ExtensionId $ExtensionId
$destinationPath = Resolve-ChromeConnectorPath -Path $Destination
$launcherConfigPath = Resolve-ChromeConnectorPath -Path $LauncherConfig
$buildManifestPath = Resolve-ChromeConnectorPath -Path $BuildManifest
Assert-ChromeConnectorLocalPath -Path $destinationPath
if (-not (Test-Path -LiteralPath $launcherConfigPath -PathType Leaf) -or
    -not (Test-Path -LiteralPath $buildManifestPath -PathType Leaf)) {
    throw 'A current router installation with launcher-config.json and codex-mux-build.json is required.'
}
$launcher = Get-Content -LiteralPath $launcherConfigPath -Raw | ConvertFrom-Json
$build = Get-Content -LiteralPath $buildManifestPath -Raw | ConvertFrom-Json
$launcherProperties = @($launcher.PSObject.Properties.Name)
$buildProperties = @($build.PSObject.Properties.Name)
if ($launcherProperties -notcontains 'schemaVersion' -or $launcherProperties -notcontains 'stateRoot' -or
    $launcherProperties -notcontains 'controlPort' -or $buildProperties -notcontains 'schemaVersion' -or
    $buildProperties -notcontains 'controlPort' -or [int]$launcher.schemaVersion -ne 2 -or
    [int]$build.schemaVersion -ne 2) {
    throw 'Chrome connector release requires schemaVersion=2 launcher and build manifests.'
}
$controlPort = [int]$launcher.controlPort
if ($controlPort -lt 49152 -or $controlPort -gt 65535 -or $controlPort -ne [int]$build.controlPort) {
    throw 'Launcher and build manifests must contain the same controlPort in 49152..65535.'
}
$stateRootPath = Resolve-ChromeConnectorPath -Path ([string]$launcher.stateRoot)
Assert-ChromeConnectorLocalPath -Path $stateRootPath
$manifestPath = Join-Path $destinationPath "$script:ChromeNativeHostName.json"
$configPath = Join-Path $destinationPath 'chrome-native-host.config.json'
$executablePath = Join-Path $destinationPath 'codex-router-chrome-host.exe'
$receiptPath = Join-Path $destinationPath 'ownership-receipt.json'

$existingRegistryValue = Get-ChromeNativeHostRegistryValue -RegistryFixtureRoot $RegistryFixtureRoot
if ($null -ne $existingRegistryValue -and
    -not [string]::Equals([string]$existingRegistryValue, $manifestPath, [StringComparison]::OrdinalIgnoreCase)) {
    throw "The router-owned host name is already registered to a different path. Refusing to overwrite: $existingRegistryValue"
}

if ($DryRun) {
    [PSCustomObject]@{
        Ready = $true
        HostName = $script:ChromeNativeHostName
        AllowedOrigin = "chrome-extension://$ExtensionId/"
        Destination = $destinationPath
        ManifestPath = $manifestPath
        StateRoot = $stateRootPath
        ControlPort = $controlPort
        RegistryCollision = $false
        Changed = $false
    } | ConvertTo-Json -Depth 4
    exit 0
}

if (Test-Path -LiteralPath $destinationPath) {
    if (-not (Test-Path -LiteralPath $receiptPath -PathType Leaf)) {
        throw "Destination exists without an ownership receipt; refusing to overwrite: $destinationPath"
    }
    $existingReceipt = Get-Content -LiteralPath $receiptPath -Raw | ConvertFrom-Json
    if ($existingReceipt.schema -ne 1 -or
        $existingReceipt.owner -ne 'github.com/TheDaniXSX/codex-subscription-router-windows' -or
        $existingReceipt.hostName -ne $script:ChromeNativeHostName -or
        -not [string]::Equals([string]$existingReceipt.registryValue, $manifestPath, [StringComparison]::Ordinal)) {
        throw 'Existing ownership receipt is invalid; refusing to overwrite the connector.'
    }
    $expectedFiles = [ordered]@{
        executable = $executablePath
        config = $configPath
        manifest = $manifestPath
    }
    if (@($existingReceipt.files.PSObject.Properties.Name).Count -ne $expectedFiles.Count) {
        throw 'Existing ownership receipt has an unexpected file set.'
    }
    foreach ($name in $expectedFiles.Keys) {
        $property = $existingReceipt.files.PSObject.Properties[$name]
        if ($null -eq $property -or
            -not [string]::Equals([string]$property.Value.path, [string]$expectedFiles[$name], [StringComparison]::OrdinalIgnoreCase) -or
            [string]$property.Value.sha256 -cnotmatch '^[0-9a-f]{64}$') {
            throw "Existing ownership receipt has an invalid $name path or hash."
        }
        $entry = $property.Value
        if ((Test-Path -LiteralPath ([string]$entry.path) -PathType Leaf) -and
            (Get-ChromeConnectorSha256 -Path ([string]$entry.path)) -ne ([string]$entry.sha256).ToLowerInvariant()) {
            throw "An installed connector artifact was modified; refusing to overwrite it: $($entry.path)"
        }
    }
}
else {
    New-Item -ItemType Directory -Path $destinationPath | Out-Null
}

$buildRoot = Join-Path ([IO.Path]::GetTempPath()) ("codex-router-chrome-host-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $buildRoot | Out-Null
try {
    $stagedHost = Join-Path $buildRoot 'codex-router-chrome-host.exe'
    if ([string]::IsNullOrWhiteSpace($HostBinary)) {
        $projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
        $goCommand = Get-Command 'go.exe' -ErrorAction SilentlyContinue
        if ($null -eq $goCommand) {
            $goFallback = Join-Path $env:ProgramFiles 'Go\bin\go.exe'
            if (-not (Test-Path -LiteralPath $goFallback -PathType Leaf)) {
                throw 'Go 1.26 or newer is required to build the native host.'
            }
            $goExecutable = $goFallback
        }
        else {
            $goExecutable = $goCommand.Source
        }
        Push-Location $projectRoot
        try {
            & $goExecutable build -trimpath -ldflags '-H=windowsgui' -o $stagedHost '.\cmd\chrome-native-host'
            if ($LASTEXITCODE -ne 0) {
                throw "go build failed with exit code $LASTEXITCODE"
            }
        }
        finally {
            Pop-Location
        }
    }
    else {
        $sourceHost = Resolve-ChromeConnectorPath -Path $HostBinary
        if (-not (Test-Path -LiteralPath $sourceHost -PathType Leaf)) {
            throw "HostBinary does not exist: $sourceHost"
        }
        Copy-Item -LiteralPath $sourceHost -Destination $stagedHost
    }

    $config = [ordered]@{
        schemaVersion = 2
        hostName = $script:ChromeNativeHostName
        extensionId = $ExtensionId
        launcherConfig = $launcherConfigPath
        buildManifest = $buildManifestPath
    }
    $manifest = [ordered]@{
        name = $script:ChromeNativeHostName
        description = 'Codex Subscription Router Chrome bridge'
        path = $executablePath
        type = 'stdio'
        allowed_origins = @("chrome-extension://$ExtensionId/")
    }
    $stagedConfig = Join-Path $buildRoot 'chrome-native-host.config.json'
    $stagedManifest = Join-Path $buildRoot "$script:ChromeNativeHostName.json"
    Write-ChromeConnectorJson -Value $config -Path $stagedConfig
    Write-ChromeConnectorJson -Value $manifest -Path $stagedManifest

    Copy-Item -LiteralPath $stagedHost -Destination $executablePath -Force
    Copy-Item -LiteralPath $stagedConfig -Destination $configPath -Force
    Copy-Item -LiteralPath $stagedManifest -Destination $manifestPath -Force

    $receipt = [ordered]@{
        schema = 1
        owner = 'github.com/TheDaniXSX/codex-subscription-router-windows'
        installedAt = [DateTime]::UtcNow.ToString('o')
        hostName = $script:ChromeNativeHostName
        extensionId = $ExtensionId
        registryValue = $manifestPath
        files = [ordered]@{
            executable = [ordered]@{ path = $executablePath; sha256 = (Get-ChromeConnectorSha256 -Path $executablePath) }
            config = [ordered]@{ path = $configPath; sha256 = (Get-ChromeConnectorSha256 -Path $configPath) }
            manifest = [ordered]@{ path = $manifestPath; sha256 = (Get-ChromeConnectorSha256 -Path $manifestPath) }
        }
    }
    Write-ChromeConnectorJson -Value $receipt -Path $receiptPath
    Set-ChromeNativeHostRegistryValue -Value $manifestPath -RegistryFixtureRoot $RegistryFixtureRoot

    [PSCustomObject]@{
        Installed = $true
        HostName = $script:ChromeNativeHostName
        AllowedOrigin = "chrome-extension://$ExtensionId/"
        ManifestPath = $manifestPath
        ControlPort = $controlPort
        RegistryScope = 'HKCU'
    } | ConvertTo-Json -Depth 4
}
catch {
    throw
}
finally {
    if (Test-Path -LiteralPath $buildRoot) {
        Remove-Item -LiteralPath $buildRoot -Recurse -Force
    }
}
