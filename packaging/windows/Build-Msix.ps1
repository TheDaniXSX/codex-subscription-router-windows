#requires -Version 7.2

<#
.SYNOPSIS
Builds an optional, independently identified MSIX from an unpackaged payload.

.DESCRIPTION
MSIX is an experimental second-stage distribution format. It never copies the
official AppxManifest, signature, block map, package identity, COM registration,
file associations, or codex:// protocol. An unsigned local package requires the
explicit -AllowUnsigned switch. -Release additionally requires a non-placeholder
identity, a current code-signing certificate, an RFC 3161 timestamp URL, and a
successful SignTool verification before the artifact is committed.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string] $PayloadRoot,

    [Parameter(Mandatory)]
    [string] $AssetsPath,

    [Parameter(Mandatory)]
    [string] $OutputPath,

    [ValidatePattern('^[A-Za-z0-9.-]{3,50}$')]
    [string] $IdentityName = 'CodexSubscriptionRouter.Local',

    [ValidatePattern('^CN=.+')]
    [string] $Publisher = 'CN=CodexSubscriptionRouter.Local',

    [string] $Version,

    [ValidateSet('x64', 'arm64', 'x86')]
    [string] $Architecture = 'x64',

    [string] $DisplayName = 'Codex Subscription Router',

    [string] $PublisherDisplayName = 'Local developer',

    [string] $Description = 'Local Codex multi-subscription router',

    [ValidatePattern('^[a-z][a-z0-9+.-]{1,38}$')]
    [string] $ProtocolName = 'codex-router',

    [string] $CertificateThumbprint,

    [string] $TimestampUrl,

    [switch] $Release,

    [switch] $AllowUnsigned,

    [switch] $Overwrite,

    [switch] $ValidateOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path -Path $PSScriptRoot -ChildPath 'Common.ps1')

$versionFile = Resolve-RouterAbsolutePath -Path (Join-Path $PSScriptRoot '..\..\VERSION') -MustExist
$projectVersion = (Get-Content -LiteralPath $versionFile -Raw -ErrorAction Stop).Trim()
if ($projectVersion -notmatch '^(?<major>[0-9]+)\.(?<minor>[0-9]+)\.(?<patch>[0-9]+)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$') {
    throw "The project VERSION is not SemVer-compatible: $projectVersion"
}
$projectMsixVersion = "$($Matches.major).$($Matches.minor).$($Matches.patch).0"
if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = $projectMsixVersion
}
if ($Version -notmatch '^\d+\.\d+\.\d+\.\d+$') {
    throw "MSIX Version must contain four numeric components: $Version"
}

if ($IdentityName -match '(?i)^OpenAI(?:\.|$)') {
    throw 'The router must not reuse or impersonate an OpenAI package identity.'
}
if ($ProtocolName -ieq 'codex') {
    throw 'The router must not claim the official codex:// protocol.'
}
if ($Publisher -match '(?i)\bOpenAI\b' -or $PublisherDisplayName -match '(?i)\bOpenAI\b') {
    throw 'The router must not present OpenAI as its publisher.'
}
if ($CertificateThumbprint -and $AllowUnsigned) {
    throw '-CertificateThumbprint and -AllowUnsigned are mutually exclusive.'
}
if (-not $ValidateOnly -and -not $CertificateThumbprint -and -not $AllowUnsigned) {
    throw 'Refusing to create an implicitly unsigned MSIX. Supply -CertificateThumbprint, or use -AllowUnsigned for a local non-release artifact.'
}
if ($Release) {
    if (
        $IdentityName -match '(?i)(^|\.)Local($|\.)' -or
        $Publisher -match '(?i)CodexSubscriptionRouter\.Local' -or
        $PublisherDisplayName -match '(?i)^Local developer$'
    ) {
        throw 'Release mode rejects placeholder Local identity and publisher values.'
    }
    if (-not $CertificateThumbprint) {
        throw 'Release mode requires -CertificateThumbprint.'
    }
    if ($Version -cne $projectMsixVersion) {
        throw "Release MSIX version '$Version' must match project VERSION '$projectVersion' as '$projectMsixVersion'."
    }
    $timestampUri = $null
    if (
        -not [Uri]::TryCreate($TimestampUrl, [UriKind]::Absolute, [ref] $timestampUri) -or
        $timestampUri.Scheme -notin @('http', 'https')
    ) {
        throw 'Release mode requires an absolute HTTP(S) RFC 3161 -TimestampUrl.'
    }
}

$parsedVersion = [version] $Version
foreach ($component in @($parsedVersion.Major, $parsedVersion.Minor, $parsedVersion.Build, $parsedVersion.Revision)) {
    if ($component -lt 0 -or $component -gt 65535) {
        throw "Every MSIX version component must be between 0 and 65535: $Version"
    }
}

$certificate = $null
$normalizedThumbprint = $null
$signTool = $null
if ($CertificateThumbprint) {
    $normalizedThumbprint = $CertificateThumbprint.Replace(' ', '').ToUpperInvariant()
    if ($normalizedThumbprint -notmatch '^[0-9A-F]{40}$') {
        throw 'CertificateThumbprint must be the 40-character SHA-1 certificate thumbprint used by SignTool /sha1.'
    }
    $certificate = Get-Item -LiteralPath "Cert:\CurrentUser\My\$normalizedThumbprint" -ErrorAction Stop
    if ($certificate.Subject -cne $Publisher) {
        throw "Certificate subject '$($certificate.Subject)' must exactly match manifest Publisher '$Publisher'."
    }
    if (-not $certificate.HasPrivateKey) {
        throw "Certificate $normalizedThumbprint does not have an accessible private key."
    }
    $now = [DateTime]::UtcNow
    if ($certificate.NotBefore.ToUniversalTime() -gt $now -or $certificate.NotAfter.ToUniversalTime() -le $now) {
        throw "Certificate $normalizedThumbprint is not currently valid."
    }
    $codeSigningEku = @(
        $certificate.Extensions |
            Where-Object { $_ -is [Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension] } |
            ForEach-Object { $_.EnhancedKeyUsages } |
            Where-Object { $_.Value -eq '1.3.6.1.5.5.7.3.3' }
    )
    if ($codeSigningEku.Count -eq 0) {
        throw "Certificate $normalizedThumbprint is not valid for Code Signing (EKU 1.3.6.1.5.5.7.3.3)."
    }

    $signTool = Find-RouterWindowsSdkTool -Name 'signtool.exe'
    if (-not $signTool) {
        throw 'signtool.exe was not found; a signed package cannot be produced or verified.'
    }
}

$payload = Resolve-RouterAbsolutePath -Path $PayloadRoot -MustExist
Assert-RouterTreeWithoutReparsePoint -Path $payload
Assert-RouterTreeHashManifest -Root $payload -ManifestRelativePath 'router-package.files.json'
$appRoot = Join-Path -Path $payload -ChildPath 'app'
Assert-RouterPatchedPayload -AppRoot $appRoot
$assets = Resolve-RouterAbsolutePath -Path $AssetsPath -MustExist
Assert-RouterTreeWithoutReparsePoint -Path $assets
foreach ($assetName in @('icon.png', 'Square44x44Logo.png', 'Square150x150Logo.png')) {
    $asset = Join-Path -Path $assets -ChildPath $assetName
    if (-not (Test-Path -LiteralPath $asset -PathType Leaf)) {
        throw "Required independent MSIX asset is missing: $asset"
    }
}

$output = Assert-RouterSafeOutputPath -OutputPath $OutputPath -SourcePath $payload
if ([IO.Path]::GetExtension($output) -ine '.msix') {
    throw "OutputPath must end in .msix: $output"
}
if ([IO.Path]::GetFileName($output) -match '[\r\n*]') {
    throw "OutputPath contains characters that cannot be represented safely in a checksum file: $output"
}
$outputChecksum = "$output.sha256"
if (((Test-Path -LiteralPath $output) -or (Test-Path -LiteralPath $outputChecksum)) -and -not $Overwrite) {
    throw "Output or checksum already exists. Choose another path or pass -Overwrite: $output"
}
New-Item -ItemType Directory -Path (Split-Path -Path $output -Parent) -Force | Out-Null

$manifestValues = @{
    IDENTITY_NAME = $IdentityName
    PUBLISHER = $Publisher
    VERSION = $Version
    ARCHITECTURE = $Architecture
    DISPLAY_NAME = $DisplayName
    PUBLISHER_DISPLAY_NAME = $PublisherDisplayName
    DESCRIPTION = $Description
    PROTOCOL_NAME = $ProtocolName
}

$staging = Join-Path -Path ([IO.Path]::GetTempPath()) -ChildPath "codex-router-msix-$([guid]::NewGuid().ToString('N'))"
$artifactStaging = Join-Path -Path (Split-Path -Path $output -Parent) -ChildPath ".$([IO.Path]::GetFileName($output)).staging.$([guid]::NewGuid().ToString('N')).msix"
$checksumStaging = "$artifactStaging.sha256"
$backup = $null
$checksumBackup = $null
$commitStarted = $false
try {
    New-Item -ItemType Directory -Path $staging | Out-Null
    Assert-RouterTreeWithoutReparsePoint -Path $staging
    Copy-RouterTreeVerified -Source $appRoot -Destination (Join-Path $staging 'app')
    Copy-RouterTreeVerified -Source $assets -Destination (Join-Path $staging 'assets')

    $sourceMetadataPath = Join-Path $payload 'router-package.json'
    $sourceManifestPath = Join-Path $payload 'router-package.files.json'
    if (-not (Test-Path -LiteralPath $sourceMetadataPath -PathType Leaf)) {
        throw "Payload metadata is missing: $sourceMetadataPath"
    }

    [void] (Expand-RouterAppxManifest `
        -TemplatePath (Join-Path $PSScriptRoot 'AppxManifest.template.xml') `
        -Values $manifestValues `
        -DestinationPath (Join-Path $staging 'AppxManifest.xml'))

    $packageMetadata = [ordered]@{
        schemaVersion = 1
        kind = 'windows-msix-payload'
        identityName = $IdentityName
        publisher = $Publisher
        version = $Version
        architecture = $Architecture
        protocolName = $ProtocolName
        filesManifest = 'router-msix.files.json'
        sourcePackageMetadataSha256 = Get-RouterFileHashOrNull -Path $sourceMetadataPath
        sourcePackageFilesManifestSha256 = Get-RouterFileHashOrNull -Path $sourceManifestPath
    }
    [IO.File]::WriteAllText(
        (Join-Path $staging 'router-package.json'),
        (($packageMetadata | ConvertTo-Json -Depth 6) + [Environment]::NewLine),
        [Text.UTF8Encoding]::new($false)
    )
    [void] (Write-RouterTreeHashManifest `
        -Root $staging `
        -ManifestRelativePath 'router-msix.files.json' `
        -Kind 'windows-msix-payload-files')
    Assert-RouterTreeHashManifest -Root $staging -ManifestRelativePath 'router-msix.files.json'

    if ($ValidateOnly) {
        [pscustomobject]@{
            Kind = 'windows-msix-validation'
            IdentityName = $IdentityName
            Publisher = $Publisher
            Version = $Version
            ProtocolName = $ProtocolName
            ManifestValidated = $true
            PayloadHashManifestValidated = $true
            ReleasePreflightValidated = [bool] $Release
            SignatureVerified = $false
        }
        return
    }

    $makeAppx = Find-RouterWindowsSdkTool -Name 'makeappx.exe'
    if (-not $makeAppx) {
        throw 'makeappx.exe was not found. Install the Windows SDK or use -ValidateOnly.'
    }

    & $makeAppx pack /d $staging /p $artifactStaging /o | Out-Host
    if ($LASTEXITCODE -ne 0) {
        throw "makeappx.exe failed with exit code $LASTEXITCODE"
    }

    $signed = $false
    $signatureVerified = $false
    if ($certificate) {
        $signArguments = @('sign', '/fd', 'SHA256', '/s', 'My', '/sha1', $normalizedThumbprint)
        if ($TimestampUrl) {
            $signArguments += @('/tr', $TimestampUrl, '/td', 'SHA256')
        }
        $signArguments += $artifactStaging
        & $signTool @signArguments | Out-Host
        if ($LASTEXITCODE -ne 0) {
            throw "signtool.exe failed with exit code $LASTEXITCODE"
        }
        $signed = $true

        & $signTool verify /pa /all /v $artifactStaging | Out-Host
        if ($LASTEXITCODE -ne 0) {
            throw "signtool.exe verification failed with exit code $LASTEXITCODE"
        }
        $signatureVerified = $true
    }
    if ($Release -and (-not $signed -or -not $signatureVerified)) {
        throw 'Release mode requires a successfully verified MSIX signature.'
    }

    $artifactHash = Get-RouterFileHashOrNull -Path $artifactStaging
    if (-not $artifactHash) {
        throw 'The completed MSIX could not be hashed.'
    }
    [IO.File]::WriteAllText(
        $checksumStaging,
        "$artifactHash *$([IO.Path]::GetFileName($output))$([Environment]::NewLine)",
        [Text.UTF8Encoding]::new($false)
    )

    $backupTimestamp = [DateTimeOffset]::UtcNow.ToString('yyyyMMddTHHmmssfffZ')
    if (Test-Path -LiteralPath $output) {
        Assert-RouterPathWithoutReparsePoint -Path $output
        $backup = "$output.backup.$backupTimestamp"
        if (Test-Path -LiteralPath $backup) {
            throw "Refusing to overwrite an existing MSIX backup: $backup"
        }
        Move-Item -LiteralPath $output -Destination $backup
    }
    if (Test-Path -LiteralPath $outputChecksum) {
        Assert-RouterPathWithoutReparsePoint -Path $outputChecksum
        $checksumBackup = "$outputChecksum.backup.$backupTimestamp"
        if (Test-Path -LiteralPath $checksumBackup) {
            throw "Refusing to overwrite an existing checksum backup: $checksumBackup"
        }
        Move-Item -LiteralPath $outputChecksum -Destination $checksumBackup
    }
    try {
        $commitStarted = $true
        Move-Item -LiteralPath $artifactStaging -Destination $output
        Move-Item -LiteralPath $checksumStaging -Destination $outputChecksum
        if ((Get-RouterFileHashOrNull -Path $output) -cne $artifactHash) {
            throw 'The committed MSIX hash does not match the signed staging artifact.'
        }
    }
    catch {
        if (Test-Path -LiteralPath $output) {
            Remove-RouterTreeSafely -Path $output
        }
        if (Test-Path -LiteralPath $outputChecksum) {
            Remove-RouterTreeSafely -Path $outputChecksum
        }
        if ($backup -and (Test-Path -LiteralPath $backup)) {
            Move-Item -LiteralPath $backup -Destination $output
            $backup = $null
        }
        if ($checksumBackup -and (Test-Path -LiteralPath $checksumBackup)) {
            Move-Item -LiteralPath $checksumBackup -Destination $outputChecksum
            $checksumBackup = $null
        }
        $commitStarted = $false
        throw
    }

    [pscustomobject]@{
        Kind = 'windows-msix'
        OutputPath = $output
        IdentityName = $IdentityName
        Publisher = $Publisher
        Version = $Version
        ProtocolName = $ProtocolName
        Signed = $signed
        SignatureVerified = $signatureVerified
        Sha256 = $artifactHash
        ChecksumPath = $outputChecksum
        Release = [bool] $Release
        PreviousOutputBackup = $backup
        PreviousChecksumBackup = $checksumBackup
    }
}
catch {
    $originalError = $_
    try {
        if (($backup -or $commitStarted) -and (Test-Path -LiteralPath $output)) {
            Remove-RouterTreeSafely -Path $output
        }
        if ($backup -and (Test-Path -LiteralPath $backup)) {
            Move-Item -LiteralPath $backup -Destination $output
            $backup = $null
        }
        if (($checksumBackup -or $commitStarted) -and (Test-Path -LiteralPath $outputChecksum)) {
            Remove-RouterTreeSafely -Path $outputChecksum
        }
        if ($checksumBackup -and (Test-Path -LiteralPath $checksumBackup)) {
            Move-Item -LiteralPath $checksumBackup -Destination $outputChecksum
            $checksumBackup = $null
        }
    }
    catch {
        throw "MSIX build failed ('$($originalError.Exception.Message)') and rollback also failed ('$($_.Exception.Message)'). Backups, if any, were left in place for manual recovery."
    }
    throw $originalError
}
finally {
    if (Test-Path -LiteralPath $staging) {
        Remove-RouterTreeSafely -Path $staging
    }
    if (Test-Path -LiteralPath $artifactStaging) {
        Remove-RouterTreeSafely -Path $artifactStaging
    }
    if (Test-Path -LiteralPath $checksumStaging) {
        Remove-RouterTreeSafely -Path $checksumStaging
    }
}
