#requires -Version 7.2

<#
.SYNOPSIS
Runs hermetic packaging contract tests without official OpenAI binaries.
#>
[CmdletBinding()]
param(
    [string] $SdkValidationAssetsPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path -Path $PSScriptRoot -ChildPath 'Common.ps1')

$projectVersion = (Get-Content -LiteralPath (Join-Path $PSScriptRoot '..\..\VERSION') -Raw).Trim()
if ($projectVersion -notmatch '^(?<major>[0-9]+)\.(?<minor>[0-9]+)\.(?<patch>[0-9]+)') {
    throw "Project VERSION is not SemVer-compatible: $projectVersion"
}
$expectedMsixVersion = "$($Matches.major).$($Matches.minor).$($Matches.patch).0"

$forbiddenRepositoryPayload = @(
    Get-ChildItem -LiteralPath $PSScriptRoot -File -Recurse -Force |
        Where-Object {
            $_.Extension -in @('.msix', '.msixbundle', '.appx', '.appxbundle', '.pfx', '.p12', '.cer', '.asar', '.exe')
        }
)
if ($forbiddenRepositoryPayload.Count -gt 0) {
    throw "Official/generated payload or signing material must not be stored under packaging/windows: $($forbiddenRepositoryPayload.FullName -join ', ')"
}

$scripts = @(
    'Common.ps1',
    'Build-Unpackaged.ps1',
    'Build-Msix.ps1'
)
foreach ($scriptName in $scripts) {
    $scriptPath = Join-Path -Path $PSScriptRoot -ChildPath $scriptName
    $parseErrors = $null
    [void] [Management.Automation.Language.Parser]::ParseFile($scriptPath, [ref] $null, [ref] $parseErrors)
    if ($parseErrors.Count -gt 0) {
        throw "$scriptName has PowerShell parse errors: $($parseErrors.Message -join '; ')"
    }
}

$testRoot = Join-Path -Path ([IO.Path]::GetTempPath()) -ChildPath "codex-router-packaging-test-$([guid]::NewGuid().ToString('N'))"
try {
    $fixture = Join-Path $testRoot 'fixture-app'
    $fixtureResources = Join-Path $fixture 'resources'
    $fixtureAssets = Join-Path $testRoot 'independent-assets'
    New-Item -ItemType Directory -Path $fixtureResources -Force | Out-Null
    New-Item -ItemType Directory -Path $fixtureAssets -Force | Out-Null

    foreach ($relativeFile in @(
        'ChatGPT.exe',
        'ChatGPT.real.exe',
        'Codex.exe',
        'resources\app.asar',
        'resources\codex.exe',
        'resources\codex.real.exe'
    )) {
        $path = Join-Path $fixture $relativeFile
        [IO.File]::WriteAllText($path, "test fixture: $relativeFile", [Text.UTF8Encoding]::new($false))
    }
    foreach ($assetName in @('icon.png', 'Square44x44Logo.png', 'Square150x150Logo.png')) {
        [IO.File]::WriteAllBytes((Join-Path $fixtureAssets $assetName), [byte[]] @(137, 80, 78, 71))
    }

    $unpackaged = Join-Path $testRoot 'unpackaged'
    $result = & (Join-Path $PSScriptRoot 'Build-Unpackaged.ps1') `
        -SourceRoot $fixture `
        -AssetsPath $fixtureAssets `
        -OutputPath $unpackaged `
        -Version '0.1.0-test'
    if (-not (Test-Path -LiteralPath (Join-Path $unpackaged 'app\ChatGPT.exe') -PathType Leaf)) {
        throw 'Build-Unpackaged did not normalize the app-root fixture into app\ChatGPT.exe.'
    }
    if ($result.HashManifest -ne (Join-Path $unpackaged 'router-package.files.json')) {
        throw 'Build-Unpackaged did not report the complete hash manifest path.'
    }
    if (Test-Path -LiteralPath (Join-Path $unpackaged 'AppxManifest.xml')) {
        throw 'Unpackaged output must not contain an AppxManifest.xml.'
    }

    $metadata = Get-Content -LiteralPath (Join-Path $unpackaged 'router-package.json') -Raw | ConvertFrom-Json
    if (
        $metadata.schemaVersion -ne 2 -or
        $metadata.kind -ne 'windows-unpackaged' -or
        $metadata.launchTarget -ne 'app\ChatGPT.exe' -or
        $metadata.filesManifest -ne 'router-package.files.json'
    ) {
        throw 'router-package.json does not satisfy the unpackaged contract.'
    }
    Assert-RouterTreeHashManifest -Root $unpackaged -ManifestRelativePath 'router-package.files.json'
    $fileManifest = Get-Content -LiteralPath (Join-Path $unpackaged 'router-package.files.json') -Raw | ConvertFrom-Json
    $actualManifestableFiles = @(Get-ChildItem -LiteralPath $unpackaged -File -Recurse -Force | Where-Object { $_.Name -ne 'router-package.files.json' })
    if ($fileManifest.files.Count -ne $actualManifestableFiles.Count) {
        throw 'The unpackaged hash manifest does not enumerate every packaged file.'
    }

    $tampered = Join-Path $testRoot 'tampered-package'
    Copy-RouterTreeVerified -Source $unpackaged -Destination $tampered
    Add-Content -LiteralPath (Join-Path $tampered 'app\resources\app.asar') -Value 'tamper'
    $tamperRejected = $false
    try {
        Assert-RouterTreeHashManifest -Root $tampered -ManifestRelativePath 'router-package.files.json'
    }
    catch {
        $tamperRejected = $true
    }
    if (-not $tamperRejected) {
        throw 'Hash manifest verification accepted a modified payload.'
    }
    $msixTamperRejected = $false
    try {
        & (Join-Path $PSScriptRoot 'Build-Msix.ps1') `
            -PayloadRoot $tampered `
            -AssetsPath $fixtureAssets `
            -OutputPath (Join-Path $testRoot 'tampered.msix') `
            -ValidateOnly
    }
    catch {
        $msixTamperRejected = $true
    }
    if (-not $msixTamperRejected) {
        throw 'Build-Msix accepted a payload that no longer matches its complete hash manifest.'
    }

    $originalAsarHash = $metadata.hashes.appAsar
    [IO.File]::WriteAllText(
        (Join-Path $fixture 'resources\app.asar'),
        'test fixture: resources\app.asar updated',
        [Text.UTF8Encoding]::new($false)
    )
    $updateResult = & (Join-Path $PSScriptRoot 'Build-Unpackaged.ps1') `
        -SourceRoot $fixture `
        -AssetsPath $fixtureAssets `
        -OutputPath $unpackaged `
        -Version '0.1.1-test' `
        -Overwrite
    if (-not $updateResult.PreviousOutputBackup -or -not (Test-Path -LiteralPath $updateResult.PreviousOutputBackup -PathType Container)) {
        throw 'Synthetic packaging update did not retain the previous output as a backup.'
    }
    Assert-RouterTreeHashManifest -Root $unpackaged -ManifestRelativePath 'router-package.files.json'
    Assert-RouterTreeHashManifest -Root $updateResult.PreviousOutputBackup -ManifestRelativePath 'router-package.files.json'
    $updatedMetadata = Get-Content -LiteralPath (Join-Path $unpackaged 'router-package.json') -Raw | ConvertFrom-Json
    if ($updatedMetadata.version -ne '0.1.1-test' -or $updatedMetadata.hashes.appAsar -eq $originalAsarHash) {
        throw 'Synthetic packaging update did not activate the new, independently hashed payload.'
    }

    $outside = Join-Path $testRoot 'junction-target'
    New-Item -ItemType Directory -Path $outside -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $outside 'escape.txt'), 'must not be copied', [Text.UTF8Encoding]::new($false))
    $junction = Join-Path $fixture 'resources\escape-link'
    New-Item -ItemType Junction -Path $junction -Target $outside | Out-Null
    $reparseSourceRejected = $false
    try {
        & (Join-Path $PSScriptRoot 'Build-Unpackaged.ps1') `
            -SourceRoot $fixture `
            -AssetsPath $fixtureAssets `
            -OutputPath (Join-Path $testRoot 'reparse-source-output') `
            -Version '0.0.0-forbidden'
    }
    catch {
        $reparseSourceRejected = $true
    }
    Remove-Item -LiteralPath $junction -Force
    if (-not $reparseSourceRejected) {
        throw 'Build-Unpackaged accepted a source tree containing a junction.'
    }

    $outputJunction = Join-Path $testRoot 'output-junction'
    New-Item -ItemType Junction -Path $outputJunction -Target $outside | Out-Null
    $reparseOutputRejected = $false
    try {
        & (Join-Path $PSScriptRoot 'Build-Unpackaged.ps1') `
            -SourceRoot $fixture `
            -AssetsPath $fixtureAssets `
            -OutputPath (Join-Path $outputJunction 'forbidden-output') `
            -Version '0.0.0-forbidden'
    }
    catch {
        $reparseOutputRejected = $true
    }
    Remove-Item -LiteralPath $outputJunction -Force
    if (-not $reparseOutputRejected) {
        throw 'Build-Unpackaged accepted an output path traversing a junction.'
    }

    $msixResult = & (Join-Path $PSScriptRoot 'Build-Msix.ps1') `
        -PayloadRoot $unpackaged `
        -AssetsPath $fixtureAssets `
        -OutputPath (Join-Path $testRoot 'router.msix') `
        -ValidateOnly
    if (
        $msixResult.IdentityName -eq 'OpenAI.Codex' -or
        $msixResult.ProtocolName -eq 'codex' -or
        $msixResult.Version -ne $expectedMsixVersion -or
        -not $msixResult.ManifestValidated
    ) {
        throw 'The optional MSIX identity isolation checks failed.'
    }
    if (-not $msixResult.PayloadHashManifestValidated) {
        throw 'MSIX staging did not validate its complete payload hash manifest.'
    }

    $officialIdentityWasRejected = $false
    try {
        & (Join-Path $PSScriptRoot 'Build-Msix.ps1') `
            -PayloadRoot $unpackaged `
            -AssetsPath $fixtureAssets `
            -OutputPath (Join-Path $testRoot 'forbidden.msix') `
            -IdentityName 'OpenAI.Codex' `
            -ValidateOnly
    }
    catch {
        $officialIdentityWasRejected = $true
    }
    if (-not $officialIdentityWasRejected) {
        throw 'Build-Msix accepted the official OpenAI.Codex identity.'
    }

    $officialPublisherWasRejected = $false
    try {
        & (Join-Path $PSScriptRoot 'Build-Msix.ps1') `
            -PayloadRoot $unpackaged `
            -AssetsPath $fixtureAssets `
            -OutputPath (Join-Path $testRoot 'forbidden-publisher.msix') `
            -Publisher 'CN=OpenAI' `
            -ValidateOnly
    }
    catch {
        $officialPublisherWasRejected = $true
    }
    if (-not $officialPublisherWasRejected) {
        throw 'Build-Msix accepted an OpenAI publisher.'
    }

    $implicitUnsignedWasRejected = $false
    try {
        & (Join-Path $PSScriptRoot 'Build-Msix.ps1') `
            -PayloadRoot $unpackaged `
            -AssetsPath $fixtureAssets `
            -OutputPath (Join-Path $testRoot 'implicitly-unsigned.msix')
    }
    catch {
        $implicitUnsignedWasRejected = $true
    }
    if (-not $implicitUnsignedWasRejected) {
        throw 'Build-Msix produced an unsigned package without explicit -AllowUnsigned.'
    }

    $releaseWithoutCertificateWasRejected = $false
    try {
        & (Join-Path $PSScriptRoot 'Build-Msix.ps1') `
            -PayloadRoot $unpackaged `
            -AssetsPath $fixtureAssets `
            -OutputPath (Join-Path $testRoot 'invalid-release.msix') `
            -IdentityName 'TheDaniXSX.CodexSubscriptionRouter' `
            -Publisher 'CN=TheDaniXSX' `
            -PublisherDisplayName 'TheDaniXSX' `
            -TimestampUrl 'https://timestamp.example.invalid' `
            -Release `
            -ValidateOnly
    }
    catch {
        $releaseWithoutCertificateWasRejected = $true
    }
    if (-not $releaseWithoutCertificateWasRejected) {
        throw 'Release mode accepted a package without a signing certificate.'
    }

    $sdkPackageValidated = $false
    if ($SdkValidationAssetsPath) {
        $sdkAssets = (Resolve-Path -LiteralPath $SdkValidationAssetsPath -ErrorAction Stop).Path
        $sdkResult = & (Join-Path $PSScriptRoot 'Build-Msix.ps1') `
            -PayloadRoot $unpackaged `
            -AssetsPath $sdkAssets `
            -OutputPath (Join-Path $testRoot 'sdk-validated.msix') `
            -AllowUnsigned
        if (-not (Test-Path -LiteralPath $sdkResult.OutputPath -PathType Leaf)) {
            throw 'Windows SDK packaging did not produce the expected MSIX.'
        }
        $sdkPackageValidated = $true
    }

    [xml] $template = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'AppxManifest.template.xml') -Raw
    if (-not $template.Package) {
        throw 'AppxManifest.template.xml is not parseable XML.'
    }

    Remove-RouterTreeSafely -Path $unpackaged
    if (Test-Path -LiteralPath $unpackaged) {
        throw 'Synthetic unpackaged uninstall did not remove the active layout.'
    }
    if (-not (Test-Path -LiteralPath $updateResult.PreviousOutputBackup -PathType Container)) {
        throw 'Synthetic uninstall unexpectedly removed the retained rollback layout.'
    }

    [pscustomobject]@{
        Passed = $true
        ScriptsParsed = $scripts.Count
        SyntheticInstallUpdateUninstall = $true
        CompleteHashManifest = $true
        TamperRejected = $tamperRejected -and $msixTamperRejected
        ReparsePointsRejected = $reparseSourceRejected -and $reparseOutputRejected
        ReleaseGatesRejectedUnsafeInputs = $officialIdentityWasRejected -and $officialPublisherWasRejected -and $implicitUnsignedWasRejected -and $releaseWithoutCertificateWasRejected
        MsixIdentity = $msixResult.IdentityName
        MsixProtocol = $msixResult.ProtocolName
        SdkPackageValidated = $sdkPackageValidated
    }
}
finally {
    if (Test-Path -LiteralPath $testRoot) {
        Remove-RouterTreeSafely -Path $testRoot
    }
}
