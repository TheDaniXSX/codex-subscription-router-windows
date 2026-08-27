#requires -Version 5.1

<#
.SYNOPSIS
Creates a read-only inventory of the installed OpenAI Codex Appx/MSIX package.

.DESCRIPTION
Detects the current user's OpenAI.Codex package (or every user when -AllUsers is
specified), selects the newest installed version, and records package metadata,
the relevant on-disk structure, SHA-256 hashes, and Authenticode signatures.

The script never reads Codex profile data or credentials. It only reads package
metadata and files below the selected package's InstallLocation. It never writes
to WindowsApps. The only optional write is the explicitly requested -JsonPath.

.PARAMETER PackageName
Appx/MSIX package name to find. Defaults to OpenAI.Codex.

.PARAMETER AllUsers
Search packages registered for all users. This can require elevation. The
default is to inspect only the current user's registration.

.PARAMETER PackageRoot
Inventory an archived package directory instead of querying Appx registration.
The directory must contain AppxManifest.xml and app. This makes compatibility
records reproducible after Windows has removed an older WindowsApps version.

.PARAMETER OutputFormat
Human writes a readable report to the host, Json emits one JSON document to the
success stream, and Both does both. Defaults to Human.

.PARAMETER JsonPath
Optionally save the JSON report to this path. The destination must already have
an existing parent directory and cannot be inside WindowsApps or the selected
package directory.

.PARAMETER Force
Allow -JsonPath to replace an existing regular file.

.PARAMETER HashAlgorithm
Hash algorithm used for relevant package artifacts. Defaults to SHA256.

.PARAMETER SkipHashes
Skip file hashing while retaining package structure and signature checks. Useful
for a fast diagnostic pass; normal compatibility baselines should include hashes.

.PARAMETER SkipSignatures
Skip Authenticode checks. This is intended only for synthetic test fixtures;
release compatibility inventories must not use it.

.OUTPUTS
With -OutputFormat Json or Both, emits a JSON document. Human output is written
with Write-Host so it does not contaminate the JSON success stream.

.NOTES
Exit codes:
  0  Inventory completed; required structure exists and checked signatures are valid.
  2  No matching Appx/MSIX package is registered.
  3  Package metadata or package files could not be read or hashed.
  4  One or more required package artifacts are missing.
  5  One or more checked Authenticode signatures are not valid.
  6  The requested JSON output file could not be written safely.
 64  An internally validated argument or output-path constraint is invalid.

.EXAMPLE
./scripts/inventory_windows_source.ps1

.EXAMPLE
./scripts/inventory_windows_source.ps1 -OutputFormat Both -JsonPath ./.artifacts/source-inventory.json

.EXAMPLE
./scripts/inventory_windows_source.ps1 -OutputFormat Json | Set-Content ./source-inventory.json
#>

[CmdletBinding()]
param(
    [Parameter()]
    [ValidateNotNullOrEmpty()]
    [string] $PackageName = 'OpenAI.Codex',

    [Parameter()]
    [switch] $AllUsers,

    [Parameter()]
    [ValidateNotNullOrEmpty()]
    [string] $PackageRoot,

    [Parameter()]
    [ValidateSet('Human', 'Json', 'Both')]
    [string] $OutputFormat = 'Human',

    [Parameter()]
    [ValidateNotNullOrEmpty()]
    [string] $JsonPath,

    [Parameter()]
    [switch] $Force,

    [Parameter()]
    [ValidateSet('SHA256', 'SHA384', 'SHA512')]
    [string] $HashAlgorithm = 'SHA256',

    [Parameter()]
    [switch] $SkipHashes,

    [Parameter()]
    [switch] $SkipSignatures,

    [Parameter()]
    [ValidateNotNullOrEmpty()]
    [string] $ExpectedPackageFamilyName = 'OpenAI.Codex_2p2nqsd0c76g0',

    [Parameter()]
    [ValidateNotNullOrEmpty()]
    [string] $ExpectedPublisher = 'CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B',

    [Parameter()]
    [ValidateSet('x64')]
    [string] $ExpectedArchitecture = 'x64'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:ExitSuccess = 0
$script:ExitPackageNotFound = 2
$script:ExitReadFailure = 3
$script:ExitMissingRequiredArtifact = 4
$script:ExitInvalidSignature = 5
$script:ExitJsonWriteFailure = 6
$script:ExitIdentityMismatch = 7
$script:ExitInvalidArgument = 64
$script:HashCache = @{}

function ConvertTo-IsoUtc {
    param([Parameter(Mandatory = $true)][datetime] $Value)

    return $Value.ToUniversalTime().ToString('o')
}

function Get-CertificateSummary {
    param([Parameter()][AllowNull()][System.Security.Cryptography.X509Certificates.X509Certificate2] $Certificate)

    if ($null -eq $Certificate) {
        return $null
    }

    return [ordered]@{
        subject = $Certificate.Subject
        issuer = $Certificate.Issuer
        thumbprint = $Certificate.Thumbprint
        serialNumber = $Certificate.SerialNumber
        notBeforeUtc = ConvertTo-IsoUtc -Value $Certificate.NotBefore
        notAfterUtc = ConvertTo-IsoUtc -Value $Certificate.NotAfter
    }
}

function Get-AuthenticodeSummary {
    param(
        [Parameter(Mandatory = $true)][string] $RelativePath,
        [Parameter(Mandatory = $true)][string] $FullPath
    )

    $signature = Get-AuthenticodeSignature -LiteralPath $FullPath -ErrorAction Stop
    return [ordered]@{
        relativePath = $RelativePath
        fullPath = $FullPath
        status = [string] $signature.Status
        statusMessage = $signature.StatusMessage
        signatureType = [string] $signature.SignatureType
        signer = Get-CertificateSummary -Certificate $signature.SignerCertificate
        timeStamper = Get-CertificateSummary -Certificate $signature.TimeStamperCertificate
    }
}

function Get-CachedFileHash {
    param(
        [Parameter(Mandatory = $true)][string] $FullPath,
        [Parameter(Mandatory = $true)][string] $Algorithm
    )

    $key = ([System.IO.Path]::GetFullPath($FullPath) + '|' + $Algorithm).ToUpperInvariant()
    if (-not $script:HashCache.ContainsKey($key)) {
        $script:HashCache[$key] = (Get-FileHash -LiteralPath $FullPath -Algorithm $Algorithm -ErrorAction Stop).Hash.ToLowerInvariant()
    }
    return [string] $script:HashCache[$key]
}

function Get-DeterministicTreeInventory {
    param(
        [Parameter(Mandatory = $true)][string] $TreeRoot,
        [Parameter(Mandatory = $true)][string] $Algorithm,
        [Parameter(Mandatory = $true)][bool] $HashesSkipped
    )

    if (-not (Test-Path -LiteralPath $TreeRoot -PathType Container)) {
        return $null
    }

    $recordsByPath = @{}
    foreach ($file in @(Get-ChildItem -LiteralPath $TreeRoot -File -Recurse -Force -ErrorAction Stop)) {
        $relative = $file.FullName.Substring($TreeRoot.Length).TrimStart('\', '/').Replace('\', '/')
        $recordsByPath[$relative] = [ordered]@{
            relativePath = $relative
            length = [long] $file.Length
            hash = if ($HashesSkipped) { $null } else { Get-CachedFileHash -FullPath $file.FullName -Algorithm $Algorithm }
        }
    }

    $recordsByFoldedPath = @{}
    foreach ($name in $recordsByPath.Keys) {
        $folded = ([string] $name).ToLowerInvariant()
        if ($recordsByFoldedPath.ContainsKey($folded)) {
            throw "Tree contains paths that differ only by case: $name"
        }
        $recordsByFoldedPath[$folded] = $recordsByPath[$name]
    }
    $foldedNames = [string[]] @($recordsByFoldedPath.Keys)
    # Match Python's sorted(paths, key=lambda path: path.as_posix().lower()).
    [Array]::Sort($foldedNames, [System.StringComparer]::Ordinal)
    $records = @($foldedNames | ForEach-Object { $recordsByFoldedPath[$_] })
    $treeHash = $null
    if (-not $HashesSkipped) {
        $builder = New-Object System.Text.StringBuilder
        foreach ($record in $records) {
            [void] $builder.Append([string] $record.relativePath)
            [void] $builder.Append([char] 0)
            [void] $builder.Append([string] $record.hash)
            [void] $builder.Append("`n")
        }
        $algorithmInstance = [System.Security.Cryptography.HashAlgorithm]::Create($Algorithm)
        if ($null -eq $algorithmInstance) {
            throw "Hash algorithm is unavailable: $Algorithm"
        }
        try {
            $bytes = [System.Text.Encoding]::UTF8.GetBytes($builder.ToString())
            $treeHash = ([System.BitConverter]::ToString($algorithmInstance.ComputeHash($bytes))).Replace('-', '').ToLowerInvariant()
        }
        finally {
            $algorithmInstance.Dispose()
        }
    }

    $totalBytes = [long] 0
    foreach ($record in $records) { $totalBytes += [long] $record.length }
    return [ordered]@{
        fileCount = $records.Count
        totalBytes = $totalBytes
        treeHashAlgorithm = if ($HashesSkipped) { $null } else { "$Algorithm-path-hash-v1" }
        treeHash = $treeHash
        files = $records
    }
}

function Test-PathIsWithin {
    param(
        [Parameter(Mandatory = $true)][string] $CandidatePath,
        [Parameter(Mandatory = $true)][string] $ParentPath
    )

    $candidate = [System.IO.Path]::GetFullPath($CandidatePath).TrimEnd('\')
    $parent = [System.IO.Path]::GetFullPath($ParentPath).TrimEnd('\')
    if ([string]::Equals($candidate, $parent, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $true
    }

    return $candidate.StartsWith(
        $parent + [System.IO.Path]::DirectorySeparatorChar,
        [System.StringComparison]::OrdinalIgnoreCase
    )
}

function Get-ManifestSummary {
    param([Parameter(Mandatory = $true)][string] $ManifestPath)

    [xml] $manifest = [System.IO.File]::ReadAllText($ManifestPath)
    $identity = $manifest.SelectSingleNode("/*[local-name()='Package']/*[local-name()='Identity']")
    $properties = $manifest.SelectSingleNode("/*[local-name()='Package']/*[local-name()='Properties']")
    $applications = @()

    foreach ($application in @($manifest.SelectNodes("/*[local-name()='Package']/*[local-name()='Applications']/*[local-name()='Application']"))) {
        if ($null -ne $application) {
            $applications += [ordered]@{
                id = [string] $application.GetAttribute('Id')
                executable = [string] $application.GetAttribute('Executable')
                entryPoint = [string] $application.GetAttribute('EntryPoint')
            }
        }
    }

    $displayNameNode = $null
    $publisherDisplayNameNode = $null
    $descriptionNode = $null
    if ($null -ne $properties) {
        $displayNameNode = $properties.SelectSingleNode("./*[local-name()='DisplayName']")
        $publisherDisplayNameNode = $properties.SelectSingleNode("./*[local-name()='PublisherDisplayName']")
        $descriptionNode = $properties.SelectSingleNode("./*[local-name()='Description']")
    }

    $displayName = $null
    $publisherDisplayName = $null
    $description = $null
    if ($null -ne $displayNameNode) { $displayName = [string] $displayNameNode.InnerText }
    if ($null -ne $publisherDisplayNameNode) { $publisherDisplayName = [string] $publisherDisplayNameNode.InnerText }
    if ($null -ne $descriptionNode) { $description = [string] $descriptionNode.InnerText }

    return [ordered]@{
        identity = [ordered]@{
            name = [string] $identity.GetAttribute('Name')
            publisher = [string] $identity.GetAttribute('Publisher')
            version = [string] $identity.GetAttribute('Version')
            processorArchitecture = [string] $identity.GetAttribute('ProcessorArchitecture')
        }
        properties = [ordered]@{
            displayName = $displayName
            publisherDisplayName = $publisherDisplayName
            description = $description
        }
        applications = $applications
    }
}

function Get-DirectChildren {
    param([Parameter(Mandatory = $true)][string] $DirectoryPath)

    $children = @()
    foreach ($item in @(Get-ChildItem -LiteralPath $DirectoryPath -Force -ErrorAction Stop | Sort-Object -Property Name)) {
        $kind = 'File'
        if ($item.PSIsContainer) {
            $kind = 'Directory'
        }

        $length = $null
        if (-not $item.PSIsContainer) {
            $length = [long] $item.Length
        }

        $children += [ordered]@{
            name = $item.Name
            kind = $kind
            length = $length
        }
    }

    return $children
}

function Write-HumanReport {
    param([Parameter(Mandatory = $true)] $Report)

    Write-Host 'OpenAI Codex Windows source inventory'
    Write-Host ('Status             : {0}' -f $Report.status)
    Write-Host ('Exit code          : {0}' -f $Report.exitCode)
    Write-Host ('Generated (UTC)    : {0}' -f $Report.generatedAtUtc)

    if ($null -ne $Report.selectedPackage) {
        Write-Host ('Package            : {0}' -f $Report.selectedPackage.packageFullName)
        Write-Host ('Version            : {0}' -f $Report.selectedPackage.version)
        Write-Host ('Architecture       : {0}' -f $Report.selectedPackage.architecture)
        Write-Host ('Signature kind     : {0}' -f $Report.selectedPackage.signatureKind)
        Write-Host ('Package status     : {0}' -f $Report.selectedPackage.packageStatus)
        Write-Host ('Install location   : {0}' -f $Report.selectedPackage.installLocation)
    }

    if ($null -ne $Report.identityValidation) {
        Write-Host ('Identity validated : {0}' -f $Report.identityValidation.passed)
    }

    if ($null -ne $Report.preservedPayload) {
        Write-Host ('Preserved payload  : {0} files, {1} bytes, tree {2}' -f
            $Report.preservedPayload.fileCount,
            $Report.preservedPayload.totalBytes,
            $Report.preservedPayload.treeHash)
    }

    if ($Report.nativePayloadSummary.Count -gt 0) {
        Write-Host ''
        Write-Host 'Native payload summary:'
        foreach ($summary in $Report.nativePayloadSummary) {
            Write-Host ('  {0,-20} files={1,-5} bytes={2,-12} tree={3}' -f
                $summary.name, $summary.fileCount, $summary.totalBytes, $summary.treeHash)
        }
    }

    if ($Report.structure.Count -gt 0) {
        Write-Host ''
        Write-Host 'Relevant structure:'
        foreach ($node in $Report.structure) {
            $state = 'missing'
            if ($node.exists) {
                $state = $node.kind.ToLowerInvariant()
            }
            $requiredMarker = ''
            if ($node.required) {
                $requiredMarker = ' [required]'
            }
            Write-Host ('  {0,-10} {1}{2}' -f $state, $node.relativePath, $requiredMarker)
        }
    }

    if ($Report.hashes.Count -gt 0) {
        Write-Host ''
        Write-Host ('Hashes ({0}):' -f $Report.hashAlgorithm)
        foreach ($hash in $Report.hashes) {
            Write-Host ('  {0}  {1}' -f $hash.hash, $hash.relativePath)
        }
    }
    elseif ($Report.hashesSkipped) {
        Write-Host ''
        Write-Host 'Hashes             : skipped by request'
    }

    if ($Report.signatures.Count -gt 0) {
        Write-Host ''
        Write-Host 'Authenticode signatures:'
        foreach ($signature in $Report.signatures) {
            $subject = '<no signer certificate>'
            if ($null -ne $signature.signer) {
                $subject = $signature.signer.subject
            }
            Write-Host ('  {0,-12} {1}' -f $signature.status, $signature.relativePath)
            Write-Host ('               {0}' -f $subject)
        }
    }

    if ($Report.warnings.Count -gt 0) {
        Write-Host ''
        Write-Host 'Warnings:' -ForegroundColor Yellow
        foreach ($warning in $Report.warnings) {
            Write-Host ('  - {0}' -f $warning) -ForegroundColor Yellow
        }
    }

    if ($Report.errors.Count -gt 0) {
        Write-Host ''
        Write-Host 'Errors:' -ForegroundColor Red
        foreach ($inventoryError in $Report.errors) {
            Write-Host ('  - {0}' -f $inventoryError) -ForegroundColor Red
        }
    }
}

function Write-RequestedOutput {
    param(
        [Parameter(Mandatory = $true)] $Report,
        [Parameter(Mandatory = $true)][string] $Format,
        [Parameter()][AllowNull()][string] $DestinationPath,
        [Parameter()][switch] $AllowOverwrite,
        [Parameter()][AllowNull()][string] $SelectedInstallLocation
    )

    $json = $Report | ConvertTo-Json -Depth 12

    if ($Format -eq 'Human' -or $Format -eq 'Both') {
        Write-HumanReport -Report $Report
    }

    if ($Format -eq 'Json' -or $Format -eq 'Both') {
        Write-Output $json
    }

    if ([string]::IsNullOrWhiteSpace($DestinationPath)) {
        return
    }

    $resolvedDestination = [System.IO.Path]::GetFullPath($DestinationPath)
    $programFilesPath = [Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFiles)
    $windowsAppsPath = Join-Path -Path $programFilesPath -ChildPath 'WindowsApps'

    $containsWindowsAppsSegment = [System.Text.RegularExpressions.Regex]::IsMatch(
        $resolvedDestination,
        '(^|[\\/])WindowsApps([\\/]|$)',
        [System.Text.RegularExpressions.RegexOptions]::IgnoreCase
    )
    if ($containsWindowsAppsSegment -or (Test-PathIsWithin -CandidatePath $resolvedDestination -ParentPath $windowsAppsPath)) {
        throw "Refusing to write JSON inside WindowsApps: $resolvedDestination"
    }
    if (-not [string]::IsNullOrWhiteSpace($SelectedInstallLocation)) {
        if (Test-PathIsWithin -CandidatePath $resolvedDestination -ParentPath $SelectedInstallLocation) {
            throw "Refusing to write JSON inside the selected package: $resolvedDestination"
        }
    }

    $parentDirectory = Split-Path -Path $resolvedDestination -Parent
    if ([string]::IsNullOrWhiteSpace($parentDirectory) -or -not (Test-Path -LiteralPath $parentDirectory -PathType Container)) {
        throw "The JSON destination's parent directory does not exist: $parentDirectory"
    }
    if (Test-Path -LiteralPath $resolvedDestination) {
        if ((Get-Item -LiteralPath $resolvedDestination -Force).PSIsContainer) {
            throw "The JSON destination is a directory: $resolvedDestination"
        }
        if (-not $AllowOverwrite) {
            throw "The JSON destination already exists. Pass -Force to replace it: $resolvedDestination"
        }
    }

    Set-Content -LiteralPath $resolvedDestination -Value $json -Encoding UTF8 -NoNewline -Force:$AllowOverwrite
}

$report = [ordered]@{
    schemaVersion = '2.0'
    generatedAtUtc = [datetime]::UtcNow.ToString('o')
    operation = 'windows-codex-source-inventory'
    readScope = 'Package registration/manifest metadata and files below the selected package root only; no profile data'
    writesRequested = -not [string]::IsNullOrWhiteSpace($JsonPath)
    status = 'Initializing'
    exitCode = $script:ExitSuccess
    query = [ordered]@{
        packageName = $PackageName
        allUsers = [bool] $AllUsers
        packageRoot = $PackageRoot
        expectedPackageFamilyName = $ExpectedPackageFamilyName
        expectedPublisher = $ExpectedPublisher
        expectedArchitecture = $ExpectedArchitecture
    }
    discoveredPackages = @()
    selectedPackage = $null
    manifest = $null
    identityValidation = $null
    paths = $null
    structure = @()
    observedDirectChildren = [ordered]@{
        packageRoot = @()
        app = @()
        resources = @()
    }
    hashAlgorithm = $HashAlgorithm
    hashesSkipped = [bool] $SkipHashes
    signaturesSkipped = [bool] $SkipSignatures
    hashes = @()
    signatures = @()
    preservedPayload = $null
    nativePayloadSummary = @()
    warnings = @()
    errors = @()
}

$finalExitCode = $script:ExitSuccess
$installLocationForOutputGuard = $null

try {
    if ($PackageRoot -and $AllUsers) {
        throw '-PackageRoot and -AllUsers are mutually exclusive.'
    }

    $packages = @()
    if ($PackageRoot) {
        $resolvedPackageRoot = [System.IO.Path]::GetFullPath($PackageRoot)
        $archivedManifestPath = Join-Path $resolvedPackageRoot 'AppxManifest.xml'
        if (-not (Test-Path -LiteralPath $archivedManifestPath -PathType Leaf)) {
            throw "Archived package root has no AppxManifest.xml: $resolvedPackageRoot"
        }
        [xml] $archivedManifestXml = [System.IO.File]::ReadAllText($archivedManifestPath)
        $archivedIdentity = $archivedManifestXml.SelectSingleNode("/*[local-name()='Package']/*[local-name()='Identity']")
        if ($null -eq $archivedIdentity) {
            throw "Archived AppxManifest.xml has no Package/Identity: $archivedManifestPath"
        }
        $leaf = [System.IO.Path]::GetFileName($resolvedPackageRoot.TrimEnd('\', '/'))
        $publisherId = if ($leaf -match '__(?<publisherId>[A-Za-z0-9]+)$') { $Matches.publisherId } else { $null }
        $family = if ($publisherId) { ([string] $archivedIdentity.GetAttribute('Name')) + '_' + $publisherId } else { $null }
        $packages = @([pscustomobject]@{
                Name = [string] $archivedIdentity.GetAttribute('Name')
                PackageFullName = $leaf
                PackageFamilyName = $family
                Version = [string] $archivedIdentity.GetAttribute('Version')
                Architecture = [string] $archivedIdentity.GetAttribute('ProcessorArchitecture')
                Publisher = [string] $archivedIdentity.GetAttribute('Publisher')
                InstallLocation = $resolvedPackageRoot
                SignatureKind = 'Archived'
                Status = 'Offline'
            })
    }
    else {
        $getPackageArguments = @{
            Name = $PackageName
            ErrorAction = 'Stop'
        }
        if ($AllUsers) {
            $getPackageArguments['AllUsers'] = $true
        }
        $packages = @(Get-AppxPackage @getPackageArguments | Sort-Object -Property Version -Descending)
    }
    foreach ($package in $packages) {
        $report.discoveredPackages += [ordered]@{
            name = [string] $package.Name
            packageFullName = [string] $package.PackageFullName
            packageFamilyName = [string] $package.PackageFamilyName
            version = [string] $package.Version
            architecture = [string] $package.Architecture
            publisher = [string] $package.Publisher
            installLocation = [string] $package.InstallLocation
            signatureKind = [string] $package.SignatureKind
            packageStatus = [string] $package.Status
        }
    }

    if ($packages.Count -eq 0) {
        $report.status = 'PackageNotFound'
        $report.exitCode = $script:ExitPackageNotFound
        $report.errors += "No Appx/MSIX package named '$PackageName' is registered in the requested scope."
        $finalExitCode = $script:ExitPackageNotFound
    }
    else {
        $selectedPackage = $packages[0]
        $installLocation = [string] $selectedPackage.InstallLocation
        $installLocationForOutputGuard = $installLocation

        $report.selectedPackage = $report.discoveredPackages[0]

        if ([string]::IsNullOrWhiteSpace($installLocation)) {
            throw "The selected package has no InstallLocation: $($selectedPackage.PackageFullName)"
        }
        if (-not (Test-Path -LiteralPath $installLocation -PathType Container)) {
            throw "The selected package InstallLocation is not readable: $installLocation"
        }

        $appPath = Join-Path -Path $installLocation -ChildPath 'app'
        $resourcesPath = Join-Path -Path $appPath -ChildPath 'resources'
        $manifestPath = Join-Path -Path $installLocation -ChildPath 'AppxManifest.xml'
        $packageSignaturePath = Join-Path -Path $installLocation -ChildPath 'AppxSignature.p7x'
        $asarPath = Join-Path -Path $resourcesPath -ChildPath 'app.asar'
        $appExecutablePath = Join-Path -Path $appPath -ChildPath 'Codex.exe'
        $embeddedCodexPath = Join-Path -Path $resourcesPath -ChildPath 'codex.exe'

        $report.paths = [ordered]@{
            packageRoot = $installLocation
            app = $appPath
            resources = $resourcesPath
            manifest = $manifestPath
            packageSignature = $packageSignaturePath
            appAsar = $asarPath
            appExecutable = $appExecutablePath
            embeddedCodexExecutable = $embeddedCodexPath
        }

        $nodeDefinitions = @(
            [ordered]@{ relativePath = 'AppxManifest.xml'; kind = 'File'; required = $true; hash = $true; signature = $false },
            [ordered]@{ relativePath = 'AppxSignature.p7x'; kind = 'File'; required = $true; hash = $true; signature = $true },
            [ordered]@{ relativePath = 'app'; kind = 'Directory'; required = $true; hash = $false; signature = $false },
            [ordered]@{ relativePath = 'app\Codex.exe'; kind = 'File'; required = $true; hash = $true; signature = $true },
            [ordered]@{ relativePath = 'app\ChatGPT.exe'; kind = 'File'; required = $true; hash = $true; signature = $true },
            [ordered]@{ relativePath = 'app\resources'; kind = 'Directory'; required = $true; hash = $false; signature = $false },
            [ordered]@{ relativePath = 'app\resources\app.asar'; kind = 'File'; required = $true; hash = $true; signature = $false },
            [ordered]@{ relativePath = 'app\resources\app.asar.unpacked'; kind = 'Directory'; required = $true; hash = $false; signature = $false },
            [ordered]@{ relativePath = 'app\resources\codex.exe'; kind = 'File'; required = $true; hash = $true; signature = $true },
            [ordered]@{ relativePath = 'app\resources\codex'; kind = 'File'; required = $false; hash = $false; signature = $false },
            [ordered]@{ relativePath = 'app\resources\codex-code-mode-host.exe'; kind = 'File'; required = $true; hash = $true; signature = $true },
            [ordered]@{ relativePath = 'app\resources\codex-command-runner.exe'; kind = 'File'; required = $true; hash = $true; signature = $true },
            [ordered]@{ relativePath = 'app\resources\codex-windows-sandbox-setup.exe'; kind = 'File'; required = $true; hash = $true; signature = $true },
            [ordered]@{ relativePath = 'app\resources\cua_node'; kind = 'Directory'; required = $true; hash = $false; signature = $false },
            [ordered]@{ relativePath = 'app\resources\native'; kind = 'Directory'; required = $true; hash = $false; signature = $false },
            [ordered]@{ relativePath = 'app\resources\native\windows-account.node'; kind = 'File'; required = $true; hash = $true; signature = $true },
            [ordered]@{ relativePath = 'app\resources\plugins'; kind = 'Directory'; required = $false; hash = $false; signature = $false },
            [ordered]@{ relativePath = 'app\resources\skills'; kind = 'Directory'; required = $false; hash = $false; signature = $false }
        )

        $hashCandidates = @()
        $signatureCandidates = @()
        foreach ($definition in $nodeDefinitions) {
            $fullPath = Join-Path -Path $installLocation -ChildPath $definition.relativePath
            $exists = $false
            if ($definition.kind -eq 'Directory') {
                $exists = Test-Path -LiteralPath $fullPath -PathType Container
            }
            else {
                $exists = Test-Path -LiteralPath $fullPath -PathType Leaf
            }

            $length = $null
            $lastWriteTimeUtc = $null
            if ($exists) {
                $item = Get-Item -LiteralPath $fullPath -Force -ErrorAction Stop
                if (-not $item.PSIsContainer) {
                    $length = [long] $item.Length
                }
                $lastWriteTimeUtc = ConvertTo-IsoUtc -Value $item.LastWriteTimeUtc
            }

            $node = [ordered]@{
                relativePath = $definition.relativePath
                fullPath = $fullPath
                kind = $definition.kind
                required = [bool] $definition.required
                exists = [bool] $exists
                length = $length
                lastWriteTimeUtc = $lastWriteTimeUtc
            }
            $report.structure += $node

            if (-not $exists -and $definition.required) {
                $report.errors += "Required $($definition.kind.ToLowerInvariant()) is missing: $($definition.relativePath)"
            }
            elseif (-not $exists) {
                $report.warnings += "Optional $($definition.kind.ToLowerInvariant()) is absent: $($definition.relativePath)"
            }

            if ($exists -and $definition.kind -eq 'File' -and $definition.hash) {
                $hashCandidates += [ordered]@{ relativePath = $definition.relativePath; fullPath = $fullPath }
            }
            if ($exists -and $definition.kind -eq 'File' -and $definition.signature) {
                $signatureCandidates += [ordered]@{ relativePath = $definition.relativePath; fullPath = $fullPath }
            }
        }

        $report.observedDirectChildren.packageRoot = Get-DirectChildren -DirectoryPath $installLocation
        if (Test-Path -LiteralPath $appPath -PathType Container) {
            $report.observedDirectChildren.app = Get-DirectChildren -DirectoryPath $appPath
        }
        if (Test-Path -LiteralPath $resourcesPath -PathType Container) {
            $report.observedDirectChildren.resources = Get-DirectChildren -DirectoryPath $resourcesPath
        }

        if (Test-Path -LiteralPath $manifestPath -PathType Leaf) {
            $report.manifest = Get-ManifestSummary -ManifestPath $manifestPath
            $identity = $report.manifest.identity
            $selected = $report.selectedPackage
            $identityChecks = [ordered]@{
                registeredNameMatchesManifest = ([string] $selected.name).Equals([string] $identity.name, [System.StringComparison]::Ordinal)
                registeredVersionMatchesManifest = ([string] $selected.version).Equals([string] $identity.version, [System.StringComparison]::OrdinalIgnoreCase)
                registeredArchitectureMatchesManifest = ([string] $selected.architecture).Equals([string] $identity.processorArchitecture, [System.StringComparison]::OrdinalIgnoreCase)
                registeredPublisherMatchesManifest = ([string] $selected.publisher).Equals([string] $identity.publisher, [System.StringComparison]::Ordinal)
                expectedNameMatches = ([string] $identity.name).Equals($PackageName, [System.StringComparison]::Ordinal)
                expectedFamilyMatches = ([string] $selected.packageFamilyName).Equals($ExpectedPackageFamilyName, [System.StringComparison]::Ordinal)
                expectedPublisherMatches = ([string] $identity.publisher).Equals($ExpectedPublisher, [System.StringComparison]::Ordinal)
                expectedArchitectureMatches = ([string] $identity.processorArchitecture).Equals($ExpectedArchitecture, [System.StringComparison]::OrdinalIgnoreCase)
            }
            $identityFailures = @($identityChecks.GetEnumerator() | Where-Object { -not [bool] $_.Value } | ForEach-Object { [string] $_.Key })
            $report.identityValidation = [ordered]@{
                passed = $identityFailures.Count -eq 0
                checks = $identityChecks
                failures = $identityFailures
            }
            if ($identityFailures.Count -gt 0) {
                $report.errors += 'Package identity validation failed: ' + ($identityFailures -join ', ')
            }
        }

        if (-not $SkipHashes) {
            foreach ($candidate in $hashCandidates) {
                $report.hashes += [ordered]@{
                    relativePath = $candidate.relativePath
                    fullPath = $candidate.fullPath
                    algorithm = $HashAlgorithm
                    hash = Get-CachedFileHash -FullPath $candidate.fullPath -Algorithm $HashAlgorithm
                }
            }
        }

        if (-not $SkipSignatures) {
            foreach ($candidate in $signatureCandidates) {
                $report.signatures += Get-AuthenticodeSummary -RelativePath $candidate.relativePath -FullPath $candidate.fullPath
            }
        }

        $report.preservedPayload = Get-DeterministicTreeInventory -TreeRoot $appPath -Algorithm $HashAlgorithm -HashesSkipped ([bool] $SkipHashes)
        foreach ($nativeDefinition in @(
                [ordered]@{ name = 'cua_node'; path = (Join-Path $resourcesPath 'cua_node') },
                [ordered]@{ name = 'native'; path = (Join-Path $resourcesPath 'native') },
                [ordered]@{ name = 'app.asar.unpacked'; path = (Join-Path $resourcesPath 'app.asar.unpacked') }
            )) {
            $tree = Get-DeterministicTreeInventory -TreeRoot $nativeDefinition.path -Algorithm $HashAlgorithm -HashesSkipped ([bool] $SkipHashes)
            if ($null -ne $tree) {
                $report.nativePayloadSummary += [ordered]@{
                    name = $nativeDefinition.name
                    relativePath = 'app/resources/' + $nativeDefinition.name
                    fileCount = $tree.fileCount
                    totalBytes = $tree.totalBytes
                    treeHashAlgorithm = $tree.treeHashAlgorithm
                    treeHash = $tree.treeHash
                }
            }
        }

        $missingRequired = @($report.structure | Where-Object { $_.required -and -not $_.exists })
        $invalidSignatures = @($report.signatures | Where-Object { $_.status -ne 'Valid' })

        if ($missingRequired.Count -gt 0) {
            $report.status = 'MissingRequiredArtifact'
            $report.exitCode = $script:ExitMissingRequiredArtifact
            $finalExitCode = $script:ExitMissingRequiredArtifact
        }
        elseif ($null -ne $report.identityValidation -and -not $report.identityValidation.passed) {
            $report.status = 'IdentityMismatch'
            $report.exitCode = $script:ExitIdentityMismatch
            $finalExitCode = $script:ExitIdentityMismatch
        }
        elseif ($invalidSignatures.Count -gt 0) {
            foreach ($invalidSignature in $invalidSignatures) {
                $report.errors += "Authenticode signature is $($invalidSignature.status): $($invalidSignature.relativePath)"
            }
            $report.status = 'InvalidSignature'
            $report.exitCode = $script:ExitInvalidSignature
            $finalExitCode = $script:ExitInvalidSignature
        }
        else {
            $report.status = 'Healthy'
            $report.exitCode = $script:ExitSuccess
            $finalExitCode = $script:ExitSuccess
        }
    }
}
catch {
    $report.status = 'ReadFailure'
    $report.exitCode = $script:ExitReadFailure
    $report.errors += $_.Exception.Message
    $finalExitCode = $script:ExitReadFailure
}

try {
    Write-RequestedOutput `
        -Report $report `
        -Format $OutputFormat `
        -DestinationPath $JsonPath `
        -AllowOverwrite:$Force `
        -SelectedInstallLocation $installLocationForOutputGuard
}
catch {
    if ($OutputFormat -eq 'Human' -or $OutputFormat -eq 'Both') {
        Write-Host ('JSON output error: {0}' -f $_.Exception.Message) -ForegroundColor Red
    }
    else {
        Write-Error -Message ('JSON output error: {0}' -f $_.Exception.Message) -ErrorAction Continue
    }

    if ($_.Exception.Message -like 'Refusing to write JSON*') {
        exit $script:ExitInvalidArgument
    }
    exit $script:ExitJsonWriteFailure
}

exit $finalExitCode
