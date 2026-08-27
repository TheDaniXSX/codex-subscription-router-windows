[CmdletBinding()]
param(
    [string]$RepositoryRoot,
    [switch]$KeepArtifacts
)

$ErrorActionPreference = 'Stop'
$repo = if ([string]::IsNullOrWhiteSpace($RepositoryRoot)) {
    [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
}
else {
    [IO.Path]::GetFullPath($RepositoryRoot)
}
$testRoot = Join-Path $env:LOCALAPPDATA ("Temp\codex-router-chrome-test-" + [Guid]::NewGuid().ToString('N'))
$destination = Join-Path $testRoot 'connector'
$stateRoot = Join-Path $testRoot 'state'
$launcherConfig = Join-Path $testRoot 'launcher-config.json'
$buildManifest = Join-Path $testRoot 'codex-mux-build.json'
$registryFixture = Join-Path $testRoot 'registry'
$extensionId = 'abcdefghijklmnopabcdefghijklmnop'
$hostName = 'io.github.thedanixsx.codex_subscription_router'
$installScript = Join-Path $repo 'scripts\install_chrome_native_host.ps1'
$uninstallScript = Join-Path $repo 'scripts\uninstall_chrome_native_host.ps1'
$fixtureValue = Join-Path $registryFixture "Software\Google\Chrome\NativeMessagingHosts\$hostName\(default).txt"

try {
    $launcherFixture = @{ schemaVersion = 2; stateRoot = $stateRoot; controlPort = 54321 } | ConvertTo-Json
    $buildFixture = @{ schemaVersion = 2; controlPort = 54321 } | ConvertTo-Json
    [IO.Directory]::CreateDirectory($testRoot) | Out-Null
    [IO.File]::WriteAllText($launcherConfig, $launcherFixture)
    [IO.File]::WriteAllText($buildManifest, $buildFixture)

    & $installScript `
        -ExtensionId $extensionId `
        -Destination $destination `
        -LauncherConfig $launcherConfig `
        -BuildManifest $buildManifest `
        -RegistryFixtureRoot $registryFixture `
        -DryRun | Out-Null
    if ((Test-Path -LiteralPath $destination) -or (Test-Path -LiteralPath $fixtureValue)) {
        throw 'Dry-run changed connector files or the registry fixture.'
    }

    [IO.File]::WriteAllText($buildManifest, (@{ schemaVersion = 2; controlPort = 54322 } | ConvertTo-Json))
    & pwsh -NoProfile -NonInteractive -File $installScript `
        -ExtensionId $extensionId `
        -Destination $destination `
        -LauncherConfig $launcherConfig `
        -BuildManifest $buildManifest `
        -RegistryFixtureRoot $registryFixture `
        -DryRun *> $null
    if ($LASTEXITCODE -eq 0) {
        throw 'Mismatched launcher/build ports should fail closed.'
    }
    [IO.File]::WriteAllText($buildManifest, $buildFixture)

    & $installScript `
        -ExtensionId $extensionId `
        -Destination $destination `
        -LauncherConfig $launcherConfig `
        -BuildManifest $buildManifest `
        -RegistryFixtureRoot $registryFixture | Out-Null

    $manifestPath = Join-Path $destination "$hostName.json"
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if ($manifest.name -ne $hostName -or @($manifest.allowed_origins).Count -ne 1 -or
        $manifest.allowed_origins[0] -cne "chrome-extension://$extensionId/") {
        throw 'Generated native-host manifest has an invalid identity or origin.'
    }
    if ([IO.File]::ReadAllText($fixtureValue) -ne $manifestPath) {
        throw 'Fixture registry value does not point to the generated manifest.'
    }

    $receiptPath = Join-Path $destination 'ownership-receipt.json'
    $ownedReceipt = [IO.File]::ReadAllText($receiptPath)
    $tamperedReceipt = $ownedReceipt | ConvertFrom-Json
    $tamperedReceipt.files.config.path = 'C:\Foreign\victim.txt'
    [IO.File]::WriteAllText($receiptPath, ($tamperedReceipt | ConvertTo-Json -Depth 8))
    & pwsh -NoProfile -NonInteractive -File $uninstallScript `
        -Destination $destination `
        -RegistryFixtureRoot $registryFixture *> $null
    if ($LASTEXITCODE -eq 0 -or -not (Test-Path -LiteralPath $manifestPath)) {
        throw 'A tampered receipt should fail without deleting connector artifacts.'
    }
    [IO.File]::WriteAllText($receiptPath, $ownedReceipt)

    # A second exact install is an idempotent owned upgrade.
    & $installScript `
        -ExtensionId $extensionId `
        -Destination $destination `
        -LauncherConfig $launcherConfig `
        -BuildManifest $buildManifest `
        -RegistryFixtureRoot $registryFixture | Out-Null

    # A foreign registry value must make uninstall preserve every artifact.
    [IO.File]::WriteAllText($fixtureValue, 'C:\Foreign\host.json')
    & pwsh -NoProfile -NonInteractive -File $uninstallScript `
        -Destination $destination `
        -RegistryFixtureRoot $registryFixture *> $null
    if ($LASTEXITCODE -ne 2) {
        throw "Modified registry fixture should return 2, got $LASTEXITCODE"
    }
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf) -or
        [IO.File]::ReadAllText($fixtureValue) -ne 'C:\Foreign\host.json') {
        throw 'Compare-and-delete did not preserve foreign state.'
    }

    # Restore the exact owned value and verify complete removal.
    [IO.File]::WriteAllText($fixtureValue, $manifestPath)
    & $uninstallScript -Destination $destination -RegistryFixtureRoot $registryFixture | Out-Null
    if (Test-Path -LiteralPath $destination) {
        throw 'Exact owned connector directory was not removed.'
    }
    if (Test-Path -LiteralPath $fixtureValue) {
        throw 'Exact owned registry fixture was not removed.'
    }

    Write-Output 'Chrome Native Messaging fixture install, idempotency, and compare-and-delete: PASS'
}
finally {
    if (-not $KeepArtifacts -and (Test-Path -LiteralPath $testRoot)) {
        $resolvedTestRoot = [IO.Path]::GetFullPath($testRoot)
        $resolvedLocal = [IO.Path]::GetFullPath($env:LOCALAPPDATA).TrimEnd('\') + '\'
        if (-not ($resolvedTestRoot + '\').StartsWith($resolvedLocal, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing to remove unexpected test path: $resolvedTestRoot"
        }
        Remove-Item -LiteralPath $resolvedTestRoot -Recurse -Force
    }
}
