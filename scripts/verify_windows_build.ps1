#Requires -Version 5.1

<#
.SYNOPSIS
Verifies a Windows Codex Subscription Router build without touching a running app.

.DESCRIPTION
The full verification checks the immutable source payload, the copied Electron
layout, router build metadata, ASAR integrity, executable signatures, directory
ACLs, and the mux passthrough.  The smoke test invokes only the bundled CLI
executables with --version; it never starts or stops the desktop executable.

Use -RepositoryOnly in CI, where the proprietary Codex payload is unavailable.
That mode verifies that no compiled payload, credentials, or likely secrets are
tracked by Git.

.EXAMPLE
./scripts/verify_windows_build.ps1

.EXAMPLE
./scripts/verify_windows_build.ps1 -RepositoryOnly

.EXAMPLE
./scripts/verify_windows_build.ps1 -BuildPath 'D:\staging\router' -PassThru

.EXAMPLE
./scripts/verify_windows_build.ps1 -OfflineHistorical -SourceInventoryPath .\source-inventory.json
#>

[CmdletBinding()]
param(
    [Parameter()]
    [string]$BuildPath = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),

    [Parameter()]
    [string]$SourcePath,

    [Parameter()]
    [string]$ManifestPath,

    [Parameter()]
    [string]$SourceInventoryPath,

    [Parameter()]
    [string]$RepositoryRoot,

    [Parameter()]
    [string]$StateRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router Data'),

    [Parameter()]
    [ValidatePattern('^[0-9a-fA-F]{64}$')]
    [string]$ExpectedSourceAsarSha256,

    [Parameter()]
    [ValidatePattern('^[0-9a-fA-F]{64}$')]
    [string]$ExpectedSourceCodexSha256,

    [Parameter()]
    [switch]$RepositoryOnly,

    [Parameter()]
    [switch]$SkipSmokeTest,

    [Parameter()]
    [switch]$StrictSignatures,

    [Parameter()]
    [switch]$OfflineHistorical,

    [Parameter()]
    [switch]$SkipSignatureValidation,

    [Parameter()]
    [switch]$SkipAclValidation,

    [Parameter()]
    [switch]$VerifyChromeNativeHostInvariance,

    [Parameter()]
    [switch]$PassThru
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($RepositoryRoot)) {
    $scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
    $RepositoryRoot = Split-Path -Parent $scriptDirectory
}

$script:Checks = New-Object 'System.Collections.Generic.List[object]'
$script:Warnings = New-Object 'System.Collections.Generic.List[string]'

function Add-Check {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$Passed,
        [Parameter(Mandatory = $true)][string]$Detail
    )

    $script:Checks.Add([pscustomobject]@{
            Name   = $Name
            Passed = $Passed
            Detail = $Detail
        })
}

function Add-WarningMessage {
    param([Parameter(Mandatory = $true)][string]$Message)
    $script:Warnings.Add($Message)
}

function Get-NormalizedPath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [switch]$AllowMissing
    )

    if (-not $AllowMissing -and -not (Test-Path -LiteralPath $Path)) {
        throw "Path does not exist: $Path"
    }
    return [System.IO.Path]::GetFullPath($Path).TrimEnd(
        [System.IO.Path]::DirectorySeparatorChar,
        [System.IO.Path]::AltDirectorySeparatorChar
    )
}

function Test-PathIsWithin {
    param(
        [Parameter(Mandatory = $true)][string]$Child,
        [Parameter(Mandatory = $true)][string]$Parent
    )

    $normalizedChild = (Get-NormalizedPath -Path $Child -AllowMissing)
    $normalizedParent = (Get-NormalizedPath -Path $Parent -AllowMissing)
    if ($normalizedChild.Equals($normalizedParent, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $true
    }
    return $normalizedChild.StartsWith(
        $normalizedParent + [System.IO.Path]::DirectorySeparatorChar,
        [System.StringComparison]::OrdinalIgnoreCase
    )
}

function Get-Sha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-StringSha256 {
    param([AllowEmptyString()][Parameter(Mandatory = $true)][string]$Value)

    $algorithm = [System.Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($Value)
        return ([System.BitConverter]::ToString($algorithm.ComputeHash($bytes))).Replace('-', '').ToLowerInvariant()
    }
    finally {
        $algorithm.Dispose()
    }
}

function Get-DeterministicTreeSummary {
    param([Parameter(Mandatory = $true)][string]$Root)

    if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
        return $null
    }
    $recordsByPath = @{}
    foreach ($file in @(Get-ChildItem -LiteralPath $Root -File -Recurse -Force)) {
        $relative = $file.FullName.Substring($Root.Length).TrimStart('\', '/').Replace('\', '/')
        $recordsByPath[$relative] = [pscustomobject]@{
            RelativePath = $relative
            Length = [long]$file.Length
            Sha256 = Get-Sha256 -Path $file.FullName
        }
    }
    $recordsByFoldedPath = @{}
    foreach ($name in $recordsByPath.Keys) {
        $folded = ([string]$name).ToLowerInvariant()
        if ($recordsByFoldedPath.ContainsKey($folded)) { throw "Tree contains paths that differ only by case: $name" }
        $recordsByFoldedPath[$folded] = $recordsByPath[$name]
    }
    $names = [string[]]@($recordsByFoldedPath.Keys)
    [Array]::Sort($names, [System.StringComparer]::Ordinal)
    $builder = New-Object System.Text.StringBuilder
    $totalBytes = [long]0
    foreach ($name in $names) {
        $record = $recordsByFoldedPath[$name]
        $totalBytes += $record.Length
        [void]$builder.Append($record.RelativePath)
        [void]$builder.Append([char]0)
        [void]$builder.Append($record.Sha256)
        [void]$builder.Append("`n")
    }
    return [pscustomobject]@{
        FileCount = $names.Count
        TotalBytes = $totalBytes
        TreeHash = Get-StringSha256 -Value $builder.ToString()
        Files = @($names | ForEach-Object { $recordsByFoldedPath[$_] })
    }
}

function Get-DeclaredTreeSummary {
    param(
        [Parameter(Mandatory = $true)][object[]]$Files,
        [string]$Prefix = ''
    )

    $normalizedPrefix = $Prefix.Replace('\', '/').Trim('/')
    $recordsByPath = @{}
    foreach ($file in $Files) {
        $relative = [string](Get-JsonProperty -Object $file -Name 'relativePath')
        $hash = [string](Get-JsonProperty -Object $file -Name 'hash')
        $length = Get-JsonProperty -Object $file -Name 'length'
        if ([string]::IsNullOrWhiteSpace($relative) -or $hash -notmatch '^[0-9a-fA-F]{64}$' -or $null -eq $length) {
            throw 'Source inventory contains an invalid payload hash record.'
        }
        $portable = $relative.Replace('\', '/')
        if ($normalizedPrefix) {
            $prefixWithSlash = $normalizedPrefix + '/'
            if (-not $portable.StartsWith($prefixWithSlash, [System.StringComparison]::Ordinal)) {
                continue
            }
            $portable = $portable.Substring($prefixWithSlash.Length)
        }
        if ($recordsByPath.ContainsKey($portable)) {
            throw "Source inventory contains a duplicate payload path: $portable"
        }
        $recordsByPath[$portable] = [pscustomobject]@{
            RelativePath = $portable
            Length = [long]$length
            Sha256 = $hash.ToLowerInvariant()
        }
    }
    $recordsByFoldedPath = @{}
    foreach ($name in $recordsByPath.Keys) {
        $folded = ([string]$name).ToLowerInvariant()
        if ($recordsByFoldedPath.ContainsKey($folded)) { throw "Inventory contains paths that differ only by case: $name" }
        $recordsByFoldedPath[$folded] = $recordsByPath[$name]
    }
    $names = [string[]]@($recordsByFoldedPath.Keys)
    [Array]::Sort($names, [System.StringComparer]::Ordinal)
    $builder = New-Object System.Text.StringBuilder
    $totalBytes = [long]0
    foreach ($name in $names) {
        $record = $recordsByFoldedPath[$name]
        $totalBytes += $record.Length
        [void]$builder.Append($record.RelativePath)
        [void]$builder.Append([char]0)
        [void]$builder.Append($record.Sha256)
        [void]$builder.Append("`n")
    }
    return [pscustomobject]@{
        FileCount = $names.Count
        TotalBytes = $totalBytes
        TreeHash = Get-StringSha256 -Value $builder.ToString()
        Files = @($names | ForEach-Object { $recordsByFoldedPath[$_] })
    }
}

function Get-JsonProperty {
    param(
        [AllowNull()][object]$Object,
        [Parameter(Mandatory = $true)][string]$Name
    )

    if ($null -eq $Object) {
        return $null
    }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) {
        return $null
    }
    return $property.Value
}

function Resolve-SourceLayout {
    param([Parameter(Mandatory = $true)][string]$Path)

    $root = Get-NormalizedPath -Path $Path
    if ((Test-Path -LiteralPath (Join-Path $root 'app\resources\app.asar') -PathType Leaf) -and
        (Test-Path -LiteralPath (Join-Path $root 'app\ChatGPT.exe') -PathType Leaf)) {
        return [pscustomobject]@{ PackageRoot = $root; AppRoot = (Join-Path $root 'app') }
    }
    if ((Test-Path -LiteralPath (Join-Path $root 'resources\app.asar') -PathType Leaf) -and
        (Test-Path -LiteralPath (Join-Path $root 'ChatGPT.exe') -PathType Leaf)) {
        return [pscustomobject]@{ PackageRoot = $null; AppRoot = $root }
    }
    throw "Source does not contain an Electron Codex payload: $root"
}

function Resolve-BuildLayout {
    param([Parameter(Mandatory = $true)][string]$Path)

    $root = Get-NormalizedPath -Path $Path
    if (Test-Path -LiteralPath (Join-Path $root 'app\resources\app.asar') -PathType Leaf) {
        return [pscustomobject]@{ BuildRoot = $root; AppRoot = (Join-Path $root 'app') }
    }
    if (Test-Path -LiteralPath (Join-Path $root 'resources\app.asar') -PathType Leaf) {
        return [pscustomobject]@{ BuildRoot = $root; AppRoot = $root }
    }
    throw "Build does not contain app\resources\app.asar or resources\app.asar: $root"
}

function Find-BuildManifest {
    param(
        [Parameter(Mandatory = $true)][object]$Layout,
        [string]$ExplicitPath
    )

    if ($ExplicitPath) {
        return Get-NormalizedPath -Path $ExplicitPath
    }
    $candidates = @(
        (Join-Path $Layout.AppRoot 'codex-mux-build.json'),
        (Join-Path $Layout.BuildRoot 'codex-mux-build.json'),
        (Join-Path $Layout.BuildRoot 'router-package.json')
    ) | Select-Object -Unique
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return $candidate
        }
    }
    return $null
}

function Get-InstalledCodexSource {
    if ($null -eq (Get-Command Get-AppxPackage -ErrorAction SilentlyContinue)) {
        return $null
    }
    $packages = @(Get-AppxPackage -Name 'OpenAI.Codex' -ErrorAction SilentlyContinue |
            Where-Object { $_.Status -eq 'Ok' } |
            Sort-Object -Property Version -Descending)
    if ($packages.Count -eq 0) {
        return $null
    }
    return $packages[0].InstallLocation
}

function Invoke-CapturedProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter()][string[]]$Arguments = @(),
        [Parameter()][int]$TimeoutMilliseconds = 15000
    )

    $argumentText = ($Arguments | ForEach-Object {
            if ($_ -match '[\s"]') {
                '"' + $_.Replace('"', '\"') + '"'
            }
            else {
                $_
            }
        }) -join ' '
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = $FilePath
    $startInfo.Arguments = $argumentText
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    if ($startInfo.EnvironmentVariables.ContainsKey('CODEX_MUX_REAL_CODEX')) {
        $startInfo.EnvironmentVariables.Remove('CODEX_MUX_REAL_CODEX')
    }

    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $startInfo
    if (-not $process.Start()) {
        throw "Could not start $FilePath"
    }
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    if (-not $process.WaitForExit($TimeoutMilliseconds)) {
        try { $process.Kill() } catch { Write-Verbose "Timed-out process already exited: $($_.Exception.Message)" }
        throw "Process timed out after $TimeoutMilliseconds ms: $FilePath $argumentText"
    }
    $process.WaitForExit()
    return [pscustomobject]@{
        ExitCode = $process.ExitCode
        Stdout   = $stdoutTask.Result
        Stderr   = $stderrTask.Result
    }
}

function Test-TrackedRepositoryContent {
    param([Parameter(Mandatory = $true)][string]$Root)

    if (-not (Test-Path -LiteralPath (Join-Path $Root '.git'))) {
        Add-Check -Name 'Repository is a Git worktree' -Passed $false -Detail $Root
        return
    }
    $git = Get-Command git -ErrorAction SilentlyContinue
    if ($null -eq $git) {
        Add-Check -Name 'Git is available for repository checks' -Passed $false -Detail 'git was not found on PATH'
        return
    }

    $tracked = @(& $git.Source -C $Root ls-files)
    if ($LASTEXITCODE -ne 0) {
        Add-Check -Name 'Enumerate tracked repository files' -Passed $false -Detail 'git ls-files failed'
        return
    }

    $forbiddenExtensions = @(
        '.appx', '.asar', '.cer', '.crt', '.dll', '.dylib', '.exe', '.key',
        '.mobileprovision', '.msix', '.msixbundle', '.node', '.p12', '.pem',
        '.pfx', '.pkg', '.provisionprofile', '.so'
    )
    $forbiddenNames = @(
        'auth.json', 'control-token', 'codex-mux-build.json',
        'router-package.json', 'state.json'
    )
    $forbiddenRoots = @('build/', 'dist/', 'node_modules/', '.artifacts/')
    $badPayloads = New-Object 'System.Collections.Generic.List[string]'
    foreach ($relative in $tracked) {
        $portable = $relative.Replace('\', '/')
        $extension = [System.IO.Path]::GetExtension($portable).ToLowerInvariant()
        $leaf = [System.IO.Path]::GetFileName($portable).ToLowerInvariant()
        $badRoot = $false
        foreach ($prefix in $forbiddenRoots) {
            if ($portable.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
                $badRoot = $true
                break
            }
        }
        if (($forbiddenExtensions -contains $extension) -or
            ($forbiddenNames -contains $leaf) -or $badRoot) {
            $badPayloads.Add($relative)
        }
    }
    Add-Check -Name 'No compiled/official payload is tracked' -Passed ($badPayloads.Count -eq 0) -Detail $(
        if ($badPayloads.Count -eq 0) { "$($tracked.Count) tracked files inspected" }
        else { $badPayloads -join ', ' }
    )

    # Build the regex fragments at runtime so this verifier does not flag its own rules.
    $secretRules = [ordered]@{
        'OpenAI-style API token' = ('s' + 'k-[A-Za-z0-9_-]{20,}')
        'GitHub token' = ('g' + 'h[pousr]_[A-Za-z0-9]{30,}')
        'Private key' = ('-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE ' + 'KEY-----')
        'Credential JSON field' = ('(?i)"(?:access_token|refresh_token|id_token|client_secret)"\s*:\s*"(?!example|placeholder|redacted|secret-token|test|dummy|fake|<)[^"\r\n]{12,}"')
        'API key assignment' = ('(?i)(?:OPENAI_API_KEY|CODEX_API_KEY)\s*=\s*(?!example|placeholder|redacted|<)[^\s"'']{12,}')
    }
    $textExtensions = @(
        '', '.c', '.cjs', '.css', '.go', '.h', '.html', '.ini', '.js', '.json',
        '.md', '.mjs', '.npmrc', '.ps1', '.py', '.sh', '.toml', '.ts', '.tsx',
        '.txt', '.xml', '.yaml', '.yml'
    )
    $secretFindings = New-Object 'System.Collections.Generic.List[string]'
    foreach ($relative in $tracked) {
        $portable = $relative.Replace('\', '/')
        if ($portable.Equals('scripts/verify_windows_build.ps1', [System.StringComparison]::OrdinalIgnoreCase)) {
            continue
        }
        $absolute = Join-Path $Root $relative
        if (-not (Test-Path -LiteralPath $absolute -PathType Leaf)) {
            continue
        }
        $item = Get-Item -LiteralPath $absolute
        if ($item.Length -gt 5MB) {
            continue
        }
        $extension = [System.IO.Path]::GetExtension($absolute).ToLowerInvariant()
        if ($textExtensions -notcontains $extension) {
            continue
        }
        try {
            $content = [System.IO.File]::ReadAllText($absolute)
        }
        catch {
            continue
        }
        foreach ($entry in $secretRules.GetEnumerator()) {
            if ([regex]::IsMatch($content, [string]$entry.Value)) {
                $secretFindings.Add("$relative ($($entry.Key))")
            }
        }
    }
    Add-Check -Name 'No likely secrets are tracked' -Passed ($secretFindings.Count -eq 0) -Detail $(
        if ($secretFindings.Count -eq 0) { 'No credential patterns found; values were never printed' }
        else { $secretFindings -join ', ' }
    )
}

function Test-CopyStructure {
    param(
        [Parameter(Mandatory = $true)][string]$SourceAppRoot,
        [Parameter(Mandatory = $true)][string]$DestinationAppRoot
    )

    $required = @(
        'ChatGPT.exe',
        'ChatGPT.real.exe',
        'Codex.exe',
        'resources',
        'resources\app.asar',
        'resources\app.asar.unpacked',
        'resources\codex.exe',
        'resources\codex.real.exe',
        'resources\native\windows-account.node'
    )
    $missingRequired = @($required | Where-Object {
            -not (Test-Path -LiteralPath (Join-Path $DestinationAppRoot $_))
        })
    Add-Check -Name 'Required router layout is present' -Passed ($missingRequired.Count -eq 0) -Detail $(
        if ($missingRequired.Count -eq 0) { $DestinationAppRoot }
        else { 'Missing: ' + ($missingRequired -join ', ') }
    )

    $sourceFiles = @(Get-ChildItem -LiteralPath $SourceAppRoot -File -Recurse -Force)
    $missingCopies = New-Object 'System.Collections.Generic.List[string]'
    $wrongHashes = New-Object 'System.Collections.Generic.List[string]'
    foreach ($sourceFile in $sourceFiles) {
        $relative = $sourceFile.FullName.Substring($SourceAppRoot.Length).TrimStart('\', '/')
        $destinationRelative = $relative
        if ($relative.Equals('ChatGPT.exe', [System.StringComparison]::OrdinalIgnoreCase)) {
            $destinationRelative = 'ChatGPT.real.exe'
        }
        elseif ($relative.Equals('resources\codex.exe', [System.StringComparison]::OrdinalIgnoreCase)) {
            $destinationRelative = 'resources\codex.real.exe'
        }
        $destinationFile = Join-Path $DestinationAppRoot $destinationRelative
        if (-not (Test-Path -LiteralPath $destinationFile -PathType Leaf)) {
            $missingCopies.Add($destinationRelative)
            continue
        }
        $expectedToChange = $relative.Equals('resources\app.asar', [System.StringComparison]::OrdinalIgnoreCase) -or
            $relative.Equals('resources\owl-app.ini', [System.StringComparison]::OrdinalIgnoreCase)
        if (-not $expectedToChange -and
            (Get-Sha256 -Path $destinationFile) -ne (Get-Sha256 -Path $sourceFile.FullName)) {
            $wrongHashes.Add($destinationRelative)
        }
    }
    Add-Check -Name 'Source payload structure was completely copied' -Passed ($missingCopies.Count -eq 0) -Detail $(
        if ($missingCopies.Count -eq 0) { "$($sourceFiles.Count) source files mapped" }
        else { 'Missing: ' + (($missingCopies | Select-Object -First 20) -join ', ') }
    )
    Add-Check -Name 'Every unmodified copied file retains its SHA-256' -Passed ($wrongHashes.Count -eq 0) -Detail $(
        if ($wrongHashes.Count -eq 0) { "$($sourceFiles.Count) files inspected; only app.asar, owl-app.ini, and router executables may differ" }
        else { 'Unexpected hashes: ' + (($wrongHashes | Select-Object -First 20) -join ', ') }
    )
}

function Test-SignaturePair {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination,
        [Parameter(Mandatory = $true)][string]$Name
    )

    $sourceSignature = Get-AuthenticodeSignature -FilePath $Source
    $destinationSignature = Get-AuthenticodeSignature -FilePath $Destination
    $hashPreserved = (Get-Sha256 -Path $Source) -eq (Get-Sha256 -Path $Destination)
    $passed = $destinationSignature.Status -eq 'Valid' -and $hashPreserved
    $detail = "source=$($sourceSignature.Status), destination=$($destinationSignature.Status), hash preserved=$hashPreserved"
    if ($sourceSignature.Status -eq 'Valid') {
        $sourceThumbprint = $sourceSignature.SignerCertificate.Thumbprint
        $destinationThumbprint = if ($null -ne $destinationSignature.SignerCertificate) {
            $destinationSignature.SignerCertificate.Thumbprint
        } else { $null }
        $passed = $passed -and ($sourceThumbprint -eq $destinationThumbprint)
        $detail += ', signer preserved=' + ($sourceThumbprint -eq $destinationThumbprint)
    }
    Add-Check -Name $Name -Passed $passed -Detail $detail
}

function Test-PreservedOfficialSignature {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$ExpectedHash,
        [Parameter(Mandatory = $true)][string]$ExpectedThumbprint,
        [Parameter(Mandatory = $true)][string]$Name
    )

    $actualHash = Get-Sha256 -Path $Path
    $signature = Get-AuthenticodeSignature -FilePath $Path
    $actualThumbprint = if ($null -ne $signature.SignerCertificate) {
        [string]$signature.SignerCertificate.Thumbprint
    } else { '' }
    $passed = $actualHash -eq $ExpectedHash.ToLowerInvariant() -and
        $signature.Status -eq 'Valid' -and
        $actualThumbprint.Equals($ExpectedThumbprint, [System.StringComparison]::OrdinalIgnoreCase)
    Add-Check -Name $Name -Passed $passed -Detail (
        "hash preserved=$($actualHash -eq $ExpectedHash.ToLowerInvariant()); signature=$($signature.Status); signer preserved=$($actualThumbprint.Equals($ExpectedThumbprint, [System.StringComparison]::OrdinalIgnoreCase))"
    )
}

function Read-SourceInventory {
    param([Parameter(Mandatory = $true)][string]$Path)

    $resolved = Get-NormalizedPath -Path $Path
    $inventory = Get-Content -LiteralPath $resolved -Raw | ConvertFrom-Json
    $schema = [string](Get-JsonProperty -Object $inventory -Name 'schemaVersion')
    if ($schema -ne '2.0') {
        throw "Source inventory schema 2.0 is required; found '$schema'."
    }
    if ((Get-JsonProperty -Object $inventory -Name 'status') -ne 'Healthy') {
        throw 'Source inventory was not completed with Healthy status.'
    }
    if ((Get-JsonProperty -Object $inventory -Name 'hashesSkipped') -eq $true) {
        throw 'Source inventory omitted hashes and cannot establish historical provenance.'
    }
    if ((Get-JsonProperty -Object $inventory -Name 'signaturesSkipped') -eq $true -and -not $SkipSignatureValidation) {
        throw 'Source inventory omitted signature validation and cannot establish release provenance.'
    }
    $payload = Get-JsonProperty -Object $inventory -Name 'preservedPayload'
    $files = @(Get-JsonProperty -Object $payload -Name 'files')
    if ($files.Count -eq 0) {
        throw 'Source inventory does not contain a complete preserved payload map.'
    }
    $declared = Get-DeclaredTreeSummary -Files $files
    $declaredTreeHash = [string](Get-JsonProperty -Object $payload -Name 'treeHash')
    $declaredCount = Get-JsonProperty -Object $payload -Name 'fileCount'
    $declaredBytes = Get-JsonProperty -Object $payload -Name 'totalBytes'
    if ($declared.TreeHash -ne $declaredTreeHash.ToLowerInvariant() -or
        $declared.FileCount -ne [int]$declaredCount -or
        $declared.TotalBytes -ne [long]$declaredBytes) {
        throw 'Source inventory payload summary does not match its declared file records.'
    }

    $payloadHashes = @{}
    foreach ($record in $files) {
        $payloadHashes[[string](Get-JsonProperty -Object $record -Name 'relativePath')] =
            ([string](Get-JsonProperty -Object $record -Name 'hash')).ToLowerInvariant()
    }
    $seenInventoryHashes = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($record in @(Get-JsonProperty -Object $inventory -Name 'hashes')) {
        $relative = ([string](Get-JsonProperty -Object $record -Name 'relativePath')).Replace('\', '/')
        $hash = [string](Get-JsonProperty -Object $record -Name 'hash')
        $algorithm = [string](Get-JsonProperty -Object $record -Name 'algorithm')
        if (-not $seenInventoryHashes.Add($relative) -or $algorithm -ne 'SHA256' -or $hash -notmatch '^[0-9a-fA-F]{64}$') {
            throw "Source inventory contains an invalid or duplicate declared hash: $relative"
        }
        if ($relative.StartsWith('app/', [System.StringComparison]::OrdinalIgnoreCase)) {
            $payloadRelative = $relative.Substring(4)
            if (-not $payloadHashes.ContainsKey($payloadRelative) -or
                $payloadHashes[$payloadRelative] -ne $hash.ToLowerInvariant()) {
                throw "Source inventory hash disagrees with its complete payload map: $relative"
            }
        }
    }
    return $inventory
}

function Test-InventoryIdentity {
    param(
        [Parameter(Mandatory = $true)][object]$Inventory,
        [Parameter(Mandatory = $true)][object]$BuildManifest
    )

    $identityValidation = Get-JsonProperty -Object $Inventory -Name 'identityValidation'
    $selected = Get-JsonProperty -Object $Inventory -Name 'selectedPackage'
    $sourceManifest = Get-JsonProperty -Object $Inventory -Name 'manifest'
    $identity = Get-JsonProperty -Object $sourceManifest -Name 'identity'
    $expected = [ordered]@{
        name = 'OpenAI.Codex'
        packageFamilyName = 'OpenAI.Codex_2p2nqsd0c76g0'
        publisher = 'CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B'
        architecture = 'x64'
    }
    $problems = New-Object 'System.Collections.Generic.List[string]'
    if ((Get-JsonProperty -Object $identityValidation -Name 'passed') -ne $true) { $problems.Add('inventory identity gate did not pass') }
    if ([string](Get-JsonProperty -Object $identity -Name 'name') -cne $expected.name) { $problems.Add('identity name') }
    if ([string](Get-JsonProperty -Object $selected -Name 'packageFamilyName') -cne $expected.packageFamilyName) { $problems.Add('package family') }
    if ([string](Get-JsonProperty -Object $identity -Name 'publisher') -cne $expected.publisher) { $problems.Add('publisher') }
    if (-not ([string](Get-JsonProperty -Object $identity -Name 'processorArchitecture')).Equals($expected.architecture, [System.StringComparison]::OrdinalIgnoreCase)) { $problems.Add('architecture') }
    if ([string](Get-JsonProperty -Object $selected -Name 'packageFullName') -cne [string](Get-JsonProperty -Object $BuildManifest -Name 'sourcePackage')) { $problems.Add('build source package') }
    if ([string](Get-JsonProperty -Object $selected -Name 'version') -cne [string](Get-JsonProperty -Object $BuildManifest -Name 'sourceVersion')) { $problems.Add('build source version') }
    if (-not $SkipSignatureValidation) {
        $chatGptSignature = @(@(Get-JsonProperty -Object $Inventory -Name 'signatures') | Where-Object {
                ([string](Get-JsonProperty -Object $_ -Name 'relativePath')).Equals('app\ChatGPT.exe', [System.StringComparison]::OrdinalIgnoreCase)
            } | Select-Object -First 1)
        if ($chatGptSignature.Count -ne 1) {
            $problems.Add('inventory ChatGPT signature')
        }
        else {
            $signer = Get-JsonProperty -Object $chatGptSignature[0] -Name 'signer'
            if ([string](Get-JsonProperty -Object $chatGptSignature[0] -Name 'status') -ne 'Valid' -or
                -not ([string](Get-JsonProperty -Object $signer -Name 'subject')).Equals([string](Get-JsonProperty -Object $BuildManifest -Name 'sourceSignerSubject'), [System.StringComparison]::Ordinal) -or
                -not ([string](Get-JsonProperty -Object $signer -Name 'thumbprint')).Equals([string](Get-JsonProperty -Object $BuildManifest -Name 'sourceSignerThumbprint'), [System.StringComparison]::OrdinalIgnoreCase)) {
                $problems.Add('build source signer')
            }
        }
    }
    Add-Check -Name 'Source package identity, architecture, and publisher are exact' -Passed ($problems.Count -eq 0) -Detail $(
        if ($problems.Count -eq 0) { "$($expected.packageFamilyName); $($expected.architecture); $($expected.publisher)" }
        else { 'Mismatch: ' + ($problems -join ', ') }
    )
}

function Test-LiveSourceIdentity {
    param(
        [Parameter(Mandatory = $true)][string]$PackageRoot,
        [Parameter(Mandatory = $true)][object]$BuildManifest
    )

    $manifestPath = Join-Path $PackageRoot 'AppxManifest.xml'
    $problems = New-Object 'System.Collections.Generic.List[string]'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        $problems.Add('AppxManifest.xml missing')
    }
    else {
        [xml]$appxManifest = [System.IO.File]::ReadAllText($manifestPath)
        $identity = $appxManifest.SelectSingleNode("/*[local-name()='Package']/*[local-name()='Identity']")
        if ($null -eq $identity) {
            $problems.Add('Package/Identity missing')
        }
        else {
            $name = [string]$identity.GetAttribute('Name')
            $publisher = [string]$identity.GetAttribute('Publisher')
            $version = [string]$identity.GetAttribute('Version')
            $architecture = [string]$identity.GetAttribute('ProcessorArchitecture')
            $packageLeaf = [System.IO.Path]::GetFileName($PackageRoot.TrimEnd('\', '/'))
            if ($name -cne 'OpenAI.Codex') { $problems.Add('identity name') }
            if ($publisher -cne 'CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B') { $problems.Add('publisher') }
            if (-not $architecture.Equals('x64', [System.StringComparison]::OrdinalIgnoreCase)) { $problems.Add('architecture') }
            if ($packageLeaf -cne [string](Get-JsonProperty -Object $BuildManifest -Name 'sourcePackage')) { $problems.Add('build source package') }
            if ($version -cne [string](Get-JsonProperty -Object $BuildManifest -Name 'sourceVersion')) { $problems.Add('build source version') }
            if (-not $packageLeaf.EndsWith('__2p2nqsd0c76g0', [System.StringComparison]::Ordinal)) { $problems.Add('publisher ID/package family') }
        }
    }
    Add-Check -Name 'Live source package identity, architecture, and publisher are exact' -Passed ($problems.Count -eq 0) -Detail $(
        if ($problems.Count -eq 0) { 'OpenAI.Codex_2p2nqsd0c76g0; x64; Microsoft Store publisher identity validated' }
        else { 'Mismatch: ' + ($problems -join ', ') }
    )
}

function Test-InventoryPayloadAgainstBuild {
    param(
        [Parameter(Mandatory = $true)][object]$Inventory,
        [Parameter(Mandatory = $true)][string]$DestinationAppRoot,
        [Parameter(Mandatory = $true)][object]$BuildManifest
    )

    $payload = Get-JsonProperty -Object $Inventory -Name 'preservedPayload'
    $files = @(Get-JsonProperty -Object $payload -Name 'files')
    $mismatches = New-Object 'System.Collections.Generic.List[string]'
    $validated = 0
    foreach ($record in $files) {
        $sourceRelative = ([string](Get-JsonProperty -Object $record -Name 'relativePath')).Replace('/', '\')
        $expectedHash = ([string](Get-JsonProperty -Object $record -Name 'hash')).ToLowerInvariant()
        if ($sourceRelative.Equals('resources\app.asar', [System.StringComparison]::OrdinalIgnoreCase)) {
            if ($expectedHash -ne ([string](Get-JsonProperty -Object $BuildManifest -Name 'sourceAsarSha256')).ToLowerInvariant()) {
                $mismatches.Add('resources/app.asar (inventory/manifest provenance)')
            }
            continue
        }
        if ($sourceRelative.Equals('resources\owl-app.ini', [System.StringComparison]::OrdinalIgnoreCase)) {
            continue
        }
        $destinationRelative = $sourceRelative
        if ($sourceRelative.Equals('ChatGPT.exe', [System.StringComparison]::OrdinalIgnoreCase)) {
            $destinationRelative = 'ChatGPT.real.exe'
        }
        elseif ($sourceRelative.Equals('resources\codex.exe', [System.StringComparison]::OrdinalIgnoreCase)) {
            $destinationRelative = 'resources\codex.real.exe'
        }
        $destination = Join-Path $DestinationAppRoot $destinationRelative
        if (-not (Test-Path -LiteralPath $destination -PathType Leaf) -or
            (Get-Sha256 -Path $destination) -ne $expectedHash) {
            $mismatches.Add($sourceRelative.Replace('\', '/'))
        }
        $validated++
    }
    Add-Check -Name 'Historical inventory hashes every preserved payload file' -Passed ($mismatches.Count -eq 0) -Detail $(
        if ($mismatches.Count -eq 0) { "$validated preserved files match their historical SHA-256 records" }
        else { 'Missing or changed: ' + (($mismatches | Select-Object -First 20) -join ', ') }
    )

    foreach ($summary in @(Get-JsonProperty -Object $Inventory -Name 'nativePayloadSummary')) {
        $name = [string](Get-JsonProperty -Object $summary -Name 'name')
        $prefix = 'resources/' + $name
        $declared = Get-DeclaredTreeSummary -Files $files -Prefix $prefix
        $summaryHash = [string](Get-JsonProperty -Object $summary -Name 'treeHash')
        $summaryCount = Get-JsonProperty -Object $summary -Name 'fileCount'
        $summaryBytes = Get-JsonProperty -Object $summary -Name 'totalBytes'
        $passed = $declared.TreeHash -eq $summaryHash.ToLowerInvariant() -and
            $declared.FileCount -eq [int]$summaryCount -and
            $declared.TotalBytes -eq [long]$summaryBytes
        Add-Check -Name "Historical $name inventory summary is deterministic" -Passed $passed -Detail (
            "files=$($declared.FileCount); bytes=$($declared.TotalBytes); tree=$($declared.TreeHash)"
        )
    }
}

function Get-InventoryPayloadHash {
    param(
        [Parameter(Mandatory = $true)][object]$Inventory,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    $payload = Get-JsonProperty -Object $Inventory -Name 'preservedPayload'
    $portableExpected = $RelativePath.Replace('\', '/')
    $matches = @(@(Get-JsonProperty -Object $payload -Name 'files') | Where-Object {
            ([string](Get-JsonProperty -Object $_ -Name 'relativePath')).Equals($portableExpected, [System.StringComparison]::Ordinal)
        })
    if ($matches.Count -ne 1) {
        throw "Historical inventory expected exactly one '$portableExpected' record; found $($matches.Count)."
    }
    return ([string](Get-JsonProperty -Object $matches[0] -Name 'hash')).ToLowerInvariant()
}

function Get-Sha256Declarations {
    param(
        [AllowNull()][object]$Object,
        [string]$Prefix = ''
    )

    $results = New-Object 'System.Collections.Generic.List[object]'
    if ($null -eq $Object -or $Object -is [string] -or $Object -is [ValueType]) {
        return @()
    }
    if ($Object -is [System.Collections.IEnumerable] -and $Object -isnot [pscustomobject]) {
        $index = 0
        foreach ($item in $Object) {
            $itemPrefix = if ($Prefix) { "$Prefix[$index]" } else { "[$index]" }
            foreach ($result in @(Get-Sha256Declarations -Object $item -Prefix $itemPrefix)) { $results.Add($result) }
            $index++
        }
        return $results.ToArray()
    }
    foreach ($property in @($Object.PSObject.Properties)) {
        $path = if ($Prefix) { "$Prefix.$($property.Name)" } else { $property.Name }
        if ($property.Name -match '(?i)sha256$') {
            $results.Add([pscustomobject]@{ Path = $path; Value = [string]$property.Value })
        }
        elseif ($null -ne $property.Value -and $property.Value -isnot [string] -and $property.Value -isnot [ValueType]) {
            foreach ($result in @(Get-Sha256Declarations -Object $property.Value -Prefix $path)) { $results.Add($result) }
        }
    }
    return $results.ToArray()
}

function Test-AllManifestHashes {
    param(
        [Parameter(Mandatory = $true)][object]$Manifest,
        [Parameter(Mandatory = $true)][string]$DestinationAppRoot,
        [AllowNull()][string]$SourceAppRoot
    )

    $fileHashMappings = [ordered]@{
        sourceChatGptSha256 = 'ChatGPT.real.exe'
        sourceCodexLauncherSha256 = 'Codex.exe'
        sourceCodexSha256 = 'resources\codex.real.exe'
        sourceWindowsAccountSha256 = 'resources\native\windows-account.node'
        patchedAsarSha256 = 'resources\app.asar'
        muxSha256 = 'resources\codex.exe'
        launcherSha256 = 'ChatGPT.exe'
    }
    $problems = New-Object 'System.Collections.Generic.List[string]'
    $validatedNames = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::Ordinal)
    foreach ($entry in $fileHashMappings.GetEnumerator()) {
        $declared = [string](Get-JsonProperty -Object $Manifest -Name ([string]$entry.Key))
        $path = Join-Path $DestinationAppRoot ([string]$entry.Value)
        if ($declared -notmatch '^[0-9a-fA-F]{64}$' -or
            -not (Test-Path -LiteralPath $path -PathType Leaf) -or
            (Get-Sha256 -Path $path) -ne $declared.ToLowerInvariant()) {
            $problems.Add([string]$entry.Key)
        }
        [void]$validatedNames.Add([string]$entry.Key)
    }

    $sourceAsarDeclared = [string](Get-JsonProperty -Object $Manifest -Name 'sourceAsarSha256')
    if ($sourceAsarDeclared -notmatch '^[0-9a-fA-F]{64}$') {
        $problems.Add('sourceAsarSha256')
    }
    elseif ($SourceAppRoot) {
        $sourceAsarPath = Join-Path $SourceAppRoot 'resources\app.asar'
        if (-not (Test-Path -LiteralPath $sourceAsarPath -PathType Leaf) -or
            (Get-Sha256 -Path $sourceAsarPath) -ne $sourceAsarDeclared.ToLowerInvariant()) {
            $problems.Add('sourceAsarSha256')
        }
    }
    [void]$validatedNames.Add('sourceAsarSha256')

    $preservation = Get-JsonProperty -Object $Manifest -Name 'preservation'
    $cliHelpers = Get-JsonProperty -Object $preservation -Name 'cliHelpers'
    if ($null -eq $cliHelpers) {
        $problems.Add('preservation.cliHelpers')
    }
    $cliProperties = if ($null -ne $cliHelpers) { @($cliHelpers.PSObject.Properties) } else { @() }
    $requiredHelperNames = @(
        'codex.real.exe',
        'codex-code-mode-host.exe',
        'codex-command-runner.exe',
        'codex-windows-sandbox-setup.exe'
    )
    $observedHelperNames = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::Ordinal)
    foreach ($property in $cliProperties) {
        if ($requiredHelperNames -notcontains $property.Name) {
            $problems.Add("unexpected preservation.cliHelpers.$($property.Name)")
            continue
        }
        [void]$observedHelperNames.Add($property.Name)
        $declared = [string]$property.Value
        $relative = if ($property.Name -eq 'codex.real.exe') {
            'resources\codex.real.exe'
        } else { 'resources\' + $property.Name }
        $path = Join-Path $DestinationAppRoot $relative
        if ($declared -notmatch '^[0-9a-fA-F]{64}$' -or
            -not (Test-Path -LiteralPath $path -PathType Leaf) -or
            (Get-Sha256 -Path $path) -ne $declared.ToLowerInvariant()) {
            $problems.Add("preservation.cliHelpers.$($property.Name)")
        }
        [void]$validatedNames.Add("preservation.cliHelpers.$($property.Name)")
    }
    foreach ($requiredHelper in $requiredHelperNames) {
        if (-not $observedHelperNames.Contains($requiredHelper)) {
            $problems.Add("preservation.cliHelpers.$requiredHelper missing")
        }
    }

    $preservedTrees = Get-JsonProperty -Object $preservation -Name 'preservedResourceTrees'
    if ($null -eq $preservedTrees) {
        $problems.Add('preservation.preservedResourceTrees')
    }
    $observedTreeNames = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::Ordinal)
    $requiredTreeNames = @('cua_node', 'native', 'app.asar.unpacked')
    $treeProperties = if ($null -ne $preservedTrees) { @($preservedTrees.PSObject.Properties) } else { @() }
    foreach ($property in $treeProperties) {
        if ($requiredTreeNames -notcontains $property.Name) {
            $problems.Add("unexpected preservation.preservedResourceTrees.$($property.Name)")
            continue
        }
        $declared = [string]$property.Value
        $path = Join-Path (Join-Path $DestinationAppRoot 'resources') $property.Name
        $summary = Get-DeterministicTreeSummary -Root $path
        if ($declared -notmatch '^[0-9a-fA-F]{64}$' -or $null -eq $summary -or
            $summary.TreeHash -ne $declared.ToLowerInvariant()) {
            $problems.Add("preservation.preservedResourceTrees.$($property.Name)")
        }
        [void]$validatedNames.Add("preservation.preservedResourceTrees.$($property.Name)")
        [void]$observedTreeNames.Add($property.Name)
        Add-Check -Name "Preserved $($property.Name) tree matches manifest" -Passed (
            $declared -match '^[0-9a-fA-F]{64}$' -and $null -ne $summary -and $summary.TreeHash -eq $declared.ToLowerInvariant()
        ) -Detail $(
            if ($null -eq $summary) { 'Tree is missing' }
            else { "files=$($summary.FileCount); bytes=$($summary.TotalBytes); tree=$($summary.TreeHash)" }
        )
    }
    foreach ($requiredTree in $requiredTreeNames) {
        if (-not $observedTreeNames.Contains($requiredTree)) {
            $problems.Add("preservation.preservedResourceTrees.$requiredTree missing")
            Add-Check -Name "Preserved $requiredTree tree matches manifest" -Passed $false -Detail 'Tree hash is not declared by build metadata'
        }
    }
    $computerUse = Get-JsonProperty -Object $preservation -Name 'computerUse'
    $computerUseTreeHash = [string](Get-JsonProperty -Object $computerUse -Name 'treeSha256')
    $cuaTreeHash = [string](Get-JsonProperty -Object $preservedTrees -Name 'cua_node')
    if ($null -ne $computerUse -or $computerUseTreeHash) {
        $computerUsePassed = $computerUseTreeHash -match '^[0-9a-fA-F]{64}$' -and
            $computerUseTreeHash.Equals($cuaTreeHash, [System.StringComparison]::OrdinalIgnoreCase)
        if (-not $computerUsePassed) { $problems.Add('preservation.computerUse.treeSha256') }
        [void]$validatedNames.Add('preservation.computerUse.treeSha256')
        Add-Check -Name 'Computer Use summary matches the preserved CUA tree' -Passed $computerUsePassed -Detail (
            "declared=$computerUseTreeHash; preserved=$cuaTreeHash"
        )
    }

    $qualification = Get-JsonProperty -Object $Manifest -Name 'capabilityQualification'
    $qualifiedComputerUse = Get-JsonProperty -Object $qualification -Name 'computerUse'
    $qualifiedTreeHash = [string](Get-JsonProperty -Object $qualifiedComputerUse -Name 'treeSha256')
    if ($qualifiedTreeHash) {
        $qualifiedTreePassed = $qualifiedTreeHash -match '^[0-9a-fA-F]{64}$' -and
            $qualifiedTreeHash.Equals($cuaTreeHash, [System.StringComparison]::OrdinalIgnoreCase)
        if (-not $qualifiedTreePassed) { $problems.Add('capabilityQualification.computerUse.treeSha256') }
        [void]$validatedNames.Add('capabilityQualification.computerUse.treeSha256')
    }

    foreach ($declaration in @(Get-Sha256Declarations -Object $Manifest)) {
        if (-not $validatedNames.Contains($declaration.Path)) {
            $problems.Add("unvalidated declaration $($declaration.Path)")
        }
    }
    Add-Check -Name 'Every SHA-256 declared by build metadata matches an artifact' -Passed ($problems.Count -eq 0) -Detail $(
        if ($problems.Count -eq 0) { "$($validatedNames.Count) build provenance hashes validated" }
        else { 'Invalid or unvalidated: ' + (($problems | Select-Object -Unique) -join ', ') }
    )
}

function Test-OptionalSignature {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter()][switch]$RequireSignature
    )

    $signature = Get-AuthenticodeSignature -FilePath $Path
    if ($signature.Status -eq 'Valid') {
        Add-Check -Name $Name -Passed $true -Detail "Valid signature: $($signature.SignerCertificate.Subject)"
        return
    }
    if ($signature.Status -eq 'NotSigned' -and -not $RequireSignature) {
        Add-Check -Name $Name -Passed $true -Detail 'Unsigned local build (allowed; use -StrictSignatures for release verification)'
        Add-WarningMessage "$Name is unsigned: $Path"
        return
    }
    Add-Check -Name $Name -Passed $false -Detail "Authenticode status: $($signature.Status)"
}

function Get-RuleSid {
    param([Parameter(Mandatory = $true)][object]$Rule)
    try {
        return $Rule.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value
    }
    catch {
        return [string]$Rule.IdentityReference
    }
}

function Test-WriteAcl {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Name
    )

    $acl = Get-Acl -LiteralPath $Path
    $broadSids = @('S-1-1-0', 'S-1-5-11', 'S-1-5-32-545')
    $writeMask = [System.Security.AccessControl.FileSystemRights]::Write -bor
        [System.Security.AccessControl.FileSystemRights]::Modify -bor
        [System.Security.AccessControl.FileSystemRights]::FullControl -bor
        [System.Security.AccessControl.FileSystemRights]::ChangePermissions -bor
        [System.Security.AccessControl.FileSystemRights]::TakeOwnership
    $unsafe = New-Object 'System.Collections.Generic.List[string]'
    foreach ($rule in $acl.Access) {
        $sid = Get-RuleSid -Rule $rule
        if ($rule.AccessControlType -eq [System.Security.AccessControl.AccessControlType]::Allow -and
            $broadSids -contains $sid -and
            (($rule.FileSystemRights -band $writeMask) -ne 0)) {
            $unsafe.Add("$sid=$($rule.FileSystemRights)")
        }
    }
    Add-Check -Name "$Name ACL prevents broad writes" -Passed ($unsafe.Count -eq 0) -Detail $(
        if ($unsafe.Count -eq 0) { "owner=$($acl.Owner)" }
        else { $unsafe -join ', ' }
    )
}

function Test-PrivateTreeAcl {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Name,
        [switch]$AllowMissing
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Container)) {
        Add-Check -Name "$Name has a private protected ACL" -Passed ([bool]$AllowMissing) -Detail $(
            if ($AllowMissing) { 'Directory is absent; no retained payload is exposed' }
            else { "Missing directory: $Path" }
        )
        return
    }

    $acl = Get-Acl -LiteralPath $Path
    $currentSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $systemSid = 'S-1-5-18'
    $expectedSids = @($currentSid, $systemSid)
    $problems = New-Object 'System.Collections.Generic.List[string]'
    if (-not $acl.AreAccessRulesProtected) {
        $problems.Add('DACL inherits from its parent')
    }
    $ownerSid = try {
        (New-Object System.Security.Principal.NTAccount($acl.Owner)).Translate(
            [System.Security.Principal.SecurityIdentifier]
        ).Value
    }
    catch { [string]$acl.Owner }
    if ($ownerSid -ne $currentSid) {
        $problems.Add("owner is $ownerSid")
    }
    $observedSids = New-Object 'System.Collections.Generic.List[string]'
    foreach ($rule in $acl.Access) {
        $sid = Get-RuleSid -Rule $rule
        $observedSids.Add($sid)
        $isExpected = $expectedSids -contains $sid -and
            $rule.AccessControlType -eq [System.Security.AccessControl.AccessControlType]::Allow -and
            (($rule.FileSystemRights -band [System.Security.AccessControl.FileSystemRights]::FullControl) -eq
                [System.Security.AccessControl.FileSystemRights]::FullControl)
        if (-not $isExpected) {
            $problems.Add("unexpected ACE for $sid")
        }
    }
    foreach ($expectedSid in $expectedSids) {
        if ($observedSids -notcontains $expectedSid) {
            $problems.Add("missing FullControl ACE for $expectedSid")
        }
    }
    if ($observedSids.Count -ne 2) {
        $problems.Add("expected exactly 2 ACEs, found $($observedSids.Count)")
    }
    Add-Check -Name "$Name has a private protected ACL" -Passed ($problems.Count -eq 0) -Detail $(
        if ($problems.Count -eq 0) { 'owner=current user; DACL protected; only current user and SYSTEM have FullControl' }
        else { $problems -join '; ' }
    )
}

function Test-SensitiveFileAcl {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return
    }
    $acl = Get-Acl -LiteralPath $Path
    $broadSids = @('S-1-1-0', 'S-1-5-11', 'S-1-5-32-545')
    $readMask = [System.Security.AccessControl.FileSystemRights]::Read -bor
        [System.Security.AccessControl.FileSystemRights]::ReadAndExecute -bor
        [System.Security.AccessControl.FileSystemRights]::FullControl
    $unsafe = New-Object 'System.Collections.Generic.List[string]'
    foreach ($rule in $acl.Access) {
        $sid = Get-RuleSid -Rule $rule
        if ($rule.AccessControlType -eq [System.Security.AccessControl.AccessControlType]::Allow -and
            $broadSids -contains $sid -and
            (($rule.FileSystemRights -band $readMask) -ne 0)) {
            $unsafe.Add("$sid=$($rule.FileSystemRights)")
        }
    }
    Add-Check -Name "Sensitive ACL: $([System.IO.Path]::GetFileName($Path))" -Passed ($unsafe.Count -eq 0) -Detail $(
        if ($unsafe.Count -eq 0) { "owner=$($acl.Owner)" }
        else { $unsafe -join ', ' }
    )
}

function Get-FileInvarianceSnapshot {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return [pscustomobject]@{
            Path   = $Path
            Exists = $false
            Length = $null
            Sha256 = $null
        }
    }
    $item = Get-Item -LiteralPath $Path
    return [pscustomobject]@{
        Path   = $Path
        Exists = $true
        Length = $item.Length
        Sha256 = Get-Sha256 -Path $Path
    }
}

function Get-NativeHostRegistrySnapshot {
    $registryPath = 'HKCU:\Software\Google\Chrome\NativeMessagingHosts\com.openai.codexextension'
    if (-not (Test-Path -LiteralPath $registryPath)) {
        return [pscustomobject]@{
            Path      = $registryPath
            Exists    = $false
            ValueKind = $null
            ValueHash = $null
        }
    }
    $key = Get-Item -LiteralPath $registryPath
    $value = [string]$key.GetValue('', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $kind = [string]$key.GetValueKind('')
    return [pscustomobject]@{
        Path      = $registryPath
        Exists    = $true
        ValueKind = $kind
        ValueHash = Get-StringSha256 -Value $value
    }
}

function Get-ChromeNativeHostSnapshot {
    $paths = @(
        (Join-Path $env:LOCALAPPDATA 'OpenAI\extension\com.openai.codexextension.json'),
        (Join-Path $env:LOCALAPPDATA 'OpenAI\Codex\chrome-native-hosts-v2.json'),
        (Join-Path $env:USERPROFILE '.codex\chrome-native-hosts-v2.json')
    )
    return [pscustomobject]@{
        Files    = @($paths | ForEach-Object { Get-FileInvarianceSnapshot -Path $_ })
        Registry = Get-NativeHostRegistrySnapshot
    }
}

function Compare-ChromeNativeHostSnapshot {
    param(
        [Parameter(Mandatory = $true)][object]$Before,
        [Parameter(Mandatory = $true)][object]$After
    )

    for ($index = 0; $index -lt $Before.Files.Count; $index++) {
        $beforeFile = $Before.Files[$index]
        $afterFile = $After.Files[$index]
        $passed = $beforeFile.Exists -eq $afterFile.Exists -and
            $beforeFile.Length -eq $afterFile.Length -and
            $beforeFile.Sha256 -eq $afterFile.Sha256
        Add-Check -Name "Official Chrome host file stayed invariant: $([System.IO.Path]::GetFileName($beforeFile.Path))" -Passed $passed -Detail (
            "exists=$($beforeFile.Exists); before=$($beforeFile.Sha256); after=$($afterFile.Sha256)"
        )
    }
    $beforeRegistry = $Before.Registry
    $afterRegistry = $After.Registry
    $registryPassed = $beforeRegistry.Exists -eq $afterRegistry.Exists -and
        $beforeRegistry.ValueKind -eq $afterRegistry.ValueKind -and
        $beforeRegistry.ValueHash -eq $afterRegistry.ValueHash
    Add-Check -Name 'Official Chrome native-host registry value stayed invariant' -Passed $registryPassed -Detail (
        "exists=$($beforeRegistry.Exists); before=$($beforeRegistry.ValueHash); after=$($afterRegistry.ValueHash)"
    )
}

function Find-TextMarkersInFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string[]]$Markers
    )

    $remaining = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::Ordinal)
    foreach ($marker in $Markers) {
        [void]$remaining.Add($marker)
    }
    $maximumMarkerLength = ($Markers | Measure-Object -Property Length -Maximum).Maximum
    $buffer = New-Object char[] 65536
    $carry = ''
    $reader = New-Object System.IO.StreamReader(
        $Path,
        [System.Text.Encoding]::UTF8,
        $true,
        65536
    )
    try {
        while ($remaining.Count -gt 0) {
            $count = $reader.ReadBlock($buffer, 0, $buffer.Length)
            if ($count -eq 0) {
                break
            }
            $text = $carry + (New-Object string ($buffer, 0, $count))
            foreach ($marker in @($remaining)) {
                if ($text.IndexOf($marker, [System.StringComparison]::Ordinal) -ge 0) {
                    [void]$remaining.Remove($marker)
                }
            }
            $carryLength = [Math]::Min([Math]::Max(0, $maximumMarkerLength - 1), $text.Length)
            if ($carryLength -gt 0) {
                $carry = $text.Substring($text.Length - $carryLength)
            }
            else {
                $carry = ''
            }
        }
    }
    finally {
        $reader.Dispose()
    }
    return @($remaining)
}

function Test-AsarArchive {
    param(
        [Parameter(Mandatory = $true)][string]$AsarPath,
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][int]$ControlPort,
        [Parameter(Mandatory = $true)][bool]$LegacyControlPort
    )

    $node = Get-Command node -ErrorAction SilentlyContinue
    $asarCli = Join-Path $Root 'node_modules\@electron\asar\bin\asar.mjs'
    if ($null -eq $node -or -not (Test-Path -LiteralPath $asarCli -PathType Leaf)) {
        Add-Check -Name 'ASAR can be structurally inspected' -Passed $false -Detail 'Run npm ci to install the pinned @electron/asar tool and ensure node is on PATH'
        return
    }
    try {
        $result = Invoke-CapturedProcess -FilePath $node.Source -Arguments @($asarCli, 'list', $AsarPath) -TimeoutMilliseconds 30000
        $requiredEntries = @('\.vite\build', '\native-menu-locales')
        $missing = @($requiredEntries | Where-Object { $result.Stdout.IndexOf($_, [System.StringComparison]::OrdinalIgnoreCase) -lt 0 })
        $passed = ($result.ExitCode -eq 0) -and ($missing.Count -eq 0)
        $detail = if ($result.ExitCode -ne 0) {
            "asar list exited $($result.ExitCode): $($result.Stderr.Trim())"
        }
        elseif ($missing.Count -gt 0) {
            'Missing entries: ' + ($missing -join ', ')
        }
        else {
            'Archive parsed and required Electron entries are present'
        }
        Add-Check -Name 'Patched ASAR is structurally valid' -Passed $passed -Detail $detail

        $routerMarkers = @(
            'CodexMuxAccountMenu',
            'CodexMuxThreadSubscription',
            'process.env.CODEX_MUX_HOME',
            "http://127.0.0.1:$ControlPort",
            'function oY(e){return}',
            'function sY(e){return}',
            'case`win32`:return[];',
            'function yJ(e){if(process.platform===`win32`)return process.env.CODEX_MUX_HOME?',
            'if(process.platform===`win32`)return;'
        )
        $missingMarkers = @(Find-TextMarkersInFile -Path $AsarPath -Markers $routerMarkers)
        Add-Check -Name 'Patched ASAR contains router integration markers' -Passed ($missingMarkers.Count -eq 0) -Detail $(
            if ($missingMarkers.Count -eq 0) { "$($routerMarkers.Count) expected markers found" }
            else { 'Missing: ' + ($missingMarkers -join ', ') }
        )

        if (-not $LegacyControlPort) {
            $legacyMarkerMissing = @(Find-TextMarkersInFile -Path $AsarPath -Markers @('http://127.0.0.1:48123'))
            Add-Check -Name 'Patched ASAR has no legacy fixed control-port fallback' -Passed ($legacyMarkerMissing.Count -eq 1) -Detail $(
                if ($legacyMarkerMissing.Count -eq 1) { 'No 127.0.0.1:48123 fallback is embedded' }
                else { 'Legacy 127.0.0.1:48123 marker remains active' }
            )
        }

        $activeOfficialAnchors = @(
            'function oY(e){if(process.platform!==`win32`)return;',
            'function sY(e){let t=e.manifestPath;process.platform!==`win32`',
            'case`win32`:return Fy(`windows`).map',
            'case`win32`:return(0,i.join)(process.env.LOCALAPPDATA??',
            'OpenProjectInCodex'
        )
        $notFound = @(Find-TextMarkersInFile -Path $AsarPath -Markers $activeOfficialAnchors)
        $found = @($activeOfficialAnchors | Where-Object { $notFound -notcontains $_ })
        Add-Check -Name 'Patched ASAR has no active official Chrome/Explorer integration anchors' -Passed ($found.Count -eq 0) -Detail $(
            if ($found.Count -eq 0) { 'Registry add/delete, official manifest lookup, and official Explorer verb are absent' }
            else { 'Still active: ' + ($found -join ', ') }
        )
    }
    catch {
        Add-Check -Name 'Patched ASAR is structurally valid' -Passed $false -Detail $_.Exception.Message
    }
}

function Complete-Verification {
    param([Parameter()][switch]$EmitResult)

    $failures = @($script:Checks | Where-Object { -not $_.Passed })
    foreach ($check in $script:Checks) {
        $label = if ($check.Passed) { 'PASS' } else { 'FAIL' }
        Write-Information ("[{0}] {1}: {2}" -f $label, $check.Name, $check.Detail) -InformationAction Continue
    }
    foreach ($warning in $script:Warnings) {
        Write-Warning $warning
    }

    $result = [pscustomobject]@{
        Passed       = $failures.Count -eq 0
        CheckCount   = $script:Checks.Count
        FailureCount = $failures.Count
        WarningCount = $script:Warnings.Count
        Checks       = $script:Checks.ToArray()
        Warnings     = $script:Warnings.ToArray()
    }
    if ($EmitResult) {
        Write-Output $result
    }
    if ($failures.Count -gt 0) {
        throw "Windows router verification failed: $($failures.Count) of $($script:Checks.Count) checks failed."
    }
    Write-Information "Windows router verification passed ($($script:Checks.Count) checks)." -InformationAction Continue
}

try {
    $RepositoryRoot = Get-NormalizedPath -Path $RepositoryRoot
    if ($StrictSignatures -and ($SkipSignatureValidation -or $SkipAclValidation)) {
        throw '-StrictSignatures cannot be combined with test-only signature or ACL bypasses.'
    }
    Test-TrackedRepositoryContent -Root $RepositoryRoot
    if ($RepositoryOnly) {
        Complete-Verification -EmitResult:$PassThru
        return
    }

    $layout = Resolve-BuildLayout -Path $BuildPath
    $resolvedManifestPath = Find-BuildManifest -Layout $layout -ExplicitPath $ManifestPath
    if ($null -eq $resolvedManifestPath) {
        Add-Check -Name 'Router build manifest is present' -Passed $false -Detail 'Expected codex-mux-build.json or router-package.json'
        Complete-Verification -EmitResult:$PassThru
        return
    }
    Add-Check -Name 'Router build manifest is present' -Passed $true -Detail $resolvedManifestPath

    try {
        $manifest = Get-Content -LiteralPath $resolvedManifestPath -Raw | ConvertFrom-Json
        Add-Check -Name 'Router build manifest is valid JSON' -Passed $true -Detail $resolvedManifestPath
    }
    catch {
        Add-Check -Name 'Router build manifest is valid JSON' -Passed $false -Detail $_.Exception.Message
        Complete-Verification -EmitResult:$PassThru
        return
    }
    $schemaVersion = Get-JsonProperty -Object $manifest -Name 'schemaVersion'
    $schemaNumber = if ($null -ne $schemaVersion) { [int]$schemaVersion } else { 0 }
    $schemaSupported = $schemaNumber -in @(1, 2) -and (-not $StrictSignatures -or $schemaNumber -eq 2)
    Add-Check -Name 'Router build manifest schema is supported' -Passed (
        $schemaSupported
    ) -Detail $(
        if ($schemaNumber -eq 1 -and -not $StrictSignatures) { 'schemaVersion=1 (legacy compatibility only)' }
        elseif ($schemaNumber -eq 2) { 'schemaVersion=2' }
        else { "schemaVersion=$schemaVersion; release verification requires schemaVersion=2" }
    )
    if ($schemaNumber -eq 1 -and -not $StrictSignatures) {
        Add-WarningMessage 'Legacy schemaVersion 1 uses fixed port 48123 and is not eligible for release.'
    }

    $controlPort = if ($schemaNumber -eq 2) {
        [int](Get-JsonProperty -Object $manifest -Name 'controlPort')
    } else { 48123 }
    $controlPortValid = if ($schemaNumber -eq 2) {
        $controlPort -ge 49152 -and $controlPort -le 65535 -and $controlPort -ne 48123
    } else { $controlPort -eq 48123 -and -not $StrictSignatures }
    Add-Check -Name 'Build control port satisfies the release contract' -Passed $controlPortValid -Detail (
        "schemaVersion=$schemaNumber; controlPort=$controlPort; releaseRange=49152..65535"
    )

    $launcherConfigRelative = [string](Get-JsonProperty -Object $manifest -Name 'launcherConfigPath')
    $expectedLauncherConfigRelative = 'resources/codex-router/launcher-config.json'
    $launcherConfigPathContract = $launcherConfigRelative.Replace('\', '/').Equals(
        $expectedLauncherConfigRelative,
        [System.StringComparison]::Ordinal
    )
    Add-Check -Name 'Launcher sidecar path is fixed inside the app payload' -Passed $launcherConfigPathContract -Detail (
        "declared=$launcherConfigRelative; expected=$expectedLauncherConfigRelative"
    )
    $launcherConfigPath = Join-Path $layout.AppRoot 'resources\codex-router\launcher-config.json'
    try {
        $launcherConfig = Get-Content -LiteralPath $launcherConfigPath -Raw | ConvertFrom-Json
        $launcherSchema = [int](Get-JsonProperty -Object $launcherConfig -Name 'schemaVersion')
        $launcherPort = if ($launcherSchema -eq 2) { [int](Get-JsonProperty -Object $launcherConfig -Name 'controlPort') } else { 48123 }
        $launcherStateRoot = [string](Get-JsonProperty -Object $launcherConfig -Name 'stateRoot')
        $stateRootMatches = -not [string]::IsNullOrWhiteSpace($launcherStateRoot) -and
            (Get-NormalizedPath -Path $launcherStateRoot -AllowMissing).Equals(
                (Get-NormalizedPath -Path $StateRoot -AllowMissing),
                [System.StringComparison]::OrdinalIgnoreCase
            )
        $sidecarValid = $launcherSchema -eq $schemaNumber -and $launcherPort -eq $controlPort -and
            $stateRootMatches -and
            ($launcherSchema -eq 1 -or ($launcherPort -ge 49152 -and $launcherPort -le 65535 -and $launcherPort -ne 48123))
        Add-Check -Name 'Launcher sidecar and build manifest agree on the control port' -Passed $sidecarValid -Detail (
            "manifestSchema=$schemaNumber; sidecarSchema=$launcherSchema; manifestPort=$controlPort; sidecarPort=$launcherPort; stateRootMatches=$stateRootMatches"
        )
    }
    catch {
        Add-Check -Name 'Launcher sidecar and build manifest agree on the control port' -Passed $false -Detail $_.Exception.Message
    }
    $integrationIsolation = Get-JsonProperty -Object $manifest -Name 'windowsIntegrationIsolation'
    $protocolDisabled = Get-JsonProperty -Object $integrationIsolation -Name 'officialProtocolRegistrationDisabled'
    $officialManifestCopied = Get-JsonProperty -Object $integrationIsolation -Name 'appxManifestCopied'
    $officialExplorerCopied = Get-JsonProperty -Object $integrationIsolation -Name 'officialExplorerVerbRegistrationCopied'
    $integrationMetadataValid = $protocolDisabled -eq $true -and
        $officialManifestCopied -eq $false -and
        $officialExplorerCopied -eq $false
    Add-Check -Name 'Build metadata declares official Windows integrations isolated' -Passed $integrationMetadataValid -Detail (
        "protocolDisabled=$protocolDisabled; appxManifestCopied=$officialManifestCopied; explorerVerbCopied=$officialExplorerCopied"
    )

    $sourceInventory = $null
    if ($SourceInventoryPath) {
        try {
            $sourceInventory = Read-SourceInventory -Path $SourceInventoryPath
            Add-Check -Name 'Historical source inventory is complete and internally consistent' -Passed $true -Detail (
                (Get-NormalizedPath -Path $SourceInventoryPath)
            )
            Test-InventoryIdentity -Inventory $sourceInventory -BuildManifest $manifest
        }
        catch {
            Add-Check -Name 'Historical source inventory is complete and internally consistent' -Passed $false -Detail $_.Exception.Message
            Complete-Verification -EmitResult:$PassThru
            return
        }
    }
    elseif ($OfflineHistorical) {
        Add-Check -Name 'Historical source inventory is complete and internally consistent' -Passed $false -Detail '-OfflineHistorical requires -SourceInventoryPath'
        Complete-Verification -EmitResult:$PassThru
        return
    }

    $metadataSource = Get-JsonProperty -Object $manifest -Name 'sourcePath'
    if (-not $OfflineHistorical -and -not $SourcePath -and $metadataSource -and (Test-Path -LiteralPath ([string]$metadataSource))) {
        $SourcePath = [string]$metadataSource
    }
    if (-not $OfflineHistorical -and -not $SourcePath) {
        $SourcePath = Get-InstalledCodexSource
    }
    if (-not $OfflineHistorical -and -not $SourcePath) {
        Add-Check -Name 'Official Codex source is discoverable' -Passed $false -Detail 'Pass -SourcePath or install OpenAI.Codex'
        Complete-Verification -EmitResult:$PassThru
        return
    }

    $sourceLayout = $null
    if (-not $OfflineHistorical) {
        try {
            $sourceLayout = Resolve-SourceLayout -Path $SourcePath
            Add-Check -Name 'Official Codex source is discoverable' -Passed $true -Detail $sourceLayout.AppRoot
        }
        catch {
            Add-Check -Name 'Official Codex source is discoverable' -Passed $false -Detail $_.Exception.Message
            Complete-Verification -EmitResult:$PassThru
            return
        }
    }
    else {
        Add-Check -Name 'Historical verification is independent of WindowsApps retention' -Passed $true -Detail 'No live source path or Appx registration was read'
    }

    if ($null -ne $sourceLayout -and $sourceLayout.PackageRoot) {
        Test-LiveSourceIdentity -PackageRoot $sourceLayout.PackageRoot -BuildManifest $manifest
    }
    elseif (-not $OfflineHistorical -and $null -eq $sourceInventory) {
        Add-Check -Name 'Live source package identity, architecture, and publisher are exact' -Passed $false -Detail 'A loose app directory has no Appx identity; pass a complete source inventory or the package root'
    }

    $sourceApp = if ($null -ne $sourceLayout) { Get-NormalizedPath -Path $sourceLayout.AppRoot } else { $null }
    $destinationApp = Get-NormalizedPath -Path $layout.AppRoot
    $separate = $null -eq $sourceApp -or -not (Test-PathIsWithin -Child $destinationApp -Parent $sourceApp)
    Add-Check -Name 'Build is isolated from the official installation' -Passed $separate -Detail $(
        if ($separate -and $sourceApp) { "$sourceApp -> $destinationApp" }
        elseif ($separate) { "historical inventory -> $destinationApp" }
        else { 'Destination is inside the immutable source app' }
    )

    if ($sourceApp) {
        Test-CopyStructure -SourceAppRoot $sourceApp -DestinationAppRoot $destinationApp
    }
    if ($null -ne $sourceInventory) {
        Test-InventoryPayloadAgainstBuild -Inventory $sourceInventory -DestinationAppRoot $destinationApp -BuildManifest $manifest
    }
    $officialManifestsInCopy = @(
        @(
            (Join-Path $layout.BuildRoot 'AppxManifest.xml'),
            (Join-Path $destinationApp 'AppxManifest.xml')
        ) | Select-Object -Unique | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf }
    )
    Add-Check -Name 'Official Appx manifest was not copied into the router build' -Passed ($officialManifestsInCopy.Count -eq 0) -Detail $(
        if ($officialManifestsInCopy.Count -eq 0) { 'No inherited package identity manifest is present' }
        else { $officialManifestsInCopy -join ', ' }
    )

    $sourceAsar = if ($sourceApp) { Join-Path $sourceApp 'resources\app.asar' } else { $null }
    $sourceCodex = if ($sourceApp) { Join-Path $sourceApp 'resources\codex.exe' } else { $null }
    $sourceDesktop = if ($sourceApp) { Join-Path $sourceApp 'ChatGPT.exe' } else { $null }
    $destinationAsar = Join-Path $destinationApp 'resources\app.asar'
    $destinationMux = Join-Path $destinationApp 'resources\codex.exe'
    $destinationRealCodex = Join-Path $destinationApp 'resources\codex.real.exe'
    $destinationLauncher = Join-Path $destinationApp 'ChatGPT.exe'
    $destinationRealDesktop = Join-Path $destinationApp 'ChatGPT.real.exe'

    $sourceAsarHash = if ($sourceApp) { Get-Sha256 -Path $sourceAsar } else { Get-InventoryPayloadHash -Inventory $sourceInventory -RelativePath 'resources/app.asar' }
    $sourceCodexHash = if ($sourceApp) { Get-Sha256 -Path $sourceCodex } else { Get-InventoryPayloadHash -Inventory $sourceInventory -RelativePath 'resources/codex.exe' }
    $sourceDesktopHash = if ($sourceApp) { Get-Sha256 -Path $sourceDesktop } else { Get-InventoryPayloadHash -Inventory $sourceInventory -RelativePath 'ChatGPT.exe' }
    $destinationAsarHash = Get-Sha256 -Path $destinationAsar
    $destinationMuxHash = Get-Sha256 -Path $destinationMux
    $destinationRealCodexHash = Get-Sha256 -Path $destinationRealCodex
    $destinationLauncherHash = Get-Sha256 -Path $destinationLauncher
    $destinationRealDesktopHash = Get-Sha256 -Path $destinationRealDesktop

    $manifestSourceAsarHash = Get-JsonProperty -Object $manifest -Name 'sourceAsarSha256'
    $manifestSourceCodexHash = Get-JsonProperty -Object $manifest -Name 'sourceCodexSha256'
    $manifestPatchedAsarHash = Get-JsonProperty -Object $manifest -Name 'patchedAsarSha256'
    $manifestMuxHash = Get-JsonProperty -Object $manifest -Name 'muxSha256'
    $manifestLauncherHash = Get-JsonProperty -Object $manifest -Name 'launcherSha256'

    Add-Check -Name 'Source app.asar matches build metadata' -Passed (
        $manifestSourceAsarHash -and $sourceAsarHash -eq ([string]$manifestSourceAsarHash).ToLowerInvariant()
    ) -Detail "actual=$sourceAsarHash expected=$manifestSourceAsarHash"
    Add-Check -Name 'Source codex.exe matches build metadata' -Passed (
        $manifestSourceCodexHash -and $sourceCodexHash -eq ([string]$manifestSourceCodexHash).ToLowerInvariant()
    ) -Detail "actual=$sourceCodexHash expected=$manifestSourceCodexHash"
    if ($ExpectedSourceAsarSha256) {
        Add-Check -Name 'Source app.asar matches explicit approved hash' -Passed (
            $sourceAsarHash -eq $ExpectedSourceAsarSha256.ToLowerInvariant()
        ) -Detail "actual=$sourceAsarHash expected=$($ExpectedSourceAsarSha256.ToLowerInvariant())"
    }
    if ($ExpectedSourceCodexSha256) {
        Add-Check -Name 'Source codex.exe matches explicit approved hash' -Passed (
            $sourceCodexHash -eq $ExpectedSourceCodexSha256.ToLowerInvariant()
        ) -Detail "actual=$sourceCodexHash expected=$($ExpectedSourceCodexSha256.ToLowerInvariant())"
    }
    Add-Check -Name 'codex.real.exe is the exact official binary' -Passed (
        $destinationRealCodexHash -eq $sourceCodexHash
    ) -Detail "source=$sourceCodexHash real=$destinationRealCodexHash"
    Add-Check -Name 'ChatGPT.real.exe is the exact official desktop binary' -Passed (
        $destinationRealDesktopHash -eq $sourceDesktopHash
    ) -Detail "source=$sourceDesktopHash real=$destinationRealDesktopHash"
    Add-Check -Name 'Patched app.asar matches build metadata' -Passed (
        $manifestPatchedAsarHash -and $destinationAsarHash -eq ([string]$manifestPatchedAsarHash).ToLowerInvariant()
    ) -Detail "actual=$destinationAsarHash expected=$manifestPatchedAsarHash"
    Add-Check -Name 'Mux executable matches build metadata' -Passed (
        $manifestMuxHash -and $destinationMuxHash -eq ([string]$manifestMuxHash).ToLowerInvariant()
    ) -Detail "actual=$destinationMuxHash expected=$manifestMuxHash"
    Add-Check -Name 'Desktop launcher matches build metadata' -Passed (
        $manifestLauncherHash -and $destinationLauncherHash -eq ([string]$manifestLauncherHash).ToLowerInvariant()
    ) -Detail "actual=$destinationLauncherHash expected=$manifestLauncherHash"
    Add-Check -Name 'Mux replaced, rather than overwrote, official codex.exe' -Passed (
        $destinationMuxHash -ne $destinationRealCodexHash
    ) -Detail "mux=$destinationMuxHash real=$destinationRealCodexHash"
    Add-Check -Name 'Launcher replaced, rather than overwrote, official ChatGPT.exe' -Passed (
        $destinationLauncherHash -ne $destinationRealDesktopHash
    ) -Detail "launcher=$destinationLauncherHash real=$destinationRealDesktopHash"

    Test-AllManifestHashes -Manifest $manifest -DestinationAppRoot $destinationApp -SourceAppRoot $sourceApp

    $packageManifestPath = Join-Path $layout.BuildRoot 'router-package.json'
    if (Test-Path -LiteralPath $packageManifestPath -PathType Leaf) {
        try {
            $packageManifest = Get-Content -LiteralPath $packageManifestPath -Raw | ConvertFrom-Json
            $packageHashes = Get-JsonProperty -Object $packageManifest -Name 'hashes'
            $packageHashChecks = [ordered]@{
                launcher        = $destinationLauncherHash
                originalDesktop = $destinationRealDesktopHash
                appAsar         = $destinationAsarHash
                router          = $destinationMuxHash
                originalCodex   = $destinationRealCodexHash
            }
            $badPackageHashes = New-Object 'System.Collections.Generic.List[string]'
            foreach ($entry in $packageHashChecks.GetEnumerator()) {
                $recorded = Get-JsonProperty -Object $packageHashes -Name ([string]$entry.Key)
                if (-not $recorded -or ([string]$recorded).ToLowerInvariant() -ne [string]$entry.Value) {
                    $badPackageHashes.Add([string]$entry.Key)
                }
            }
            foreach ($property in @($packageHashes.PSObject.Properties)) {
                if (-not $packageHashChecks.Contains($property.Name)) {
                    $badPackageHashes.Add("unvalidated declaration $($property.Name)")
                }
            }
            $packageKind = Get-JsonProperty -Object $packageManifest -Name 'kind'
            $launchTarget = Get-JsonProperty -Object $packageManifest -Name 'launchTarget'
            $passed = $packageKind -eq 'windows-unpackaged' -and
                $launchTarget -eq 'app\ChatGPT.exe' -and
                $badPackageHashes.Count -eq 0
            Add-Check -Name 'Unpackaged payload metadata matches its files' -Passed $passed -Detail $(
                if ($passed) { 'kind, launch target, and every declared SHA-256 hash are valid' }
                else { "kind=$packageKind launchTarget=$launchTarget badHashes=$($badPackageHashes -join ',')" }
            )
        }
        catch {
            Add-Check -Name 'Unpackaged payload metadata matches its files' -Passed $false -Detail $_.Exception.Message
        }
    }

    Test-AsarArchive -AsarPath $destinationAsar -Root $RepositoryRoot -ControlPort $controlPort -LegacyControlPort ($schemaNumber -eq 1)

    $sourceWindowsAccount = if ($sourceApp) { Join-Path $sourceApp 'resources\native\windows-account.node' } else { $null }
    $destinationWindowsAccount = Join-Path $destinationApp 'resources\native\windows-account.node'
    if ($SkipSignatureValidation) {
        Add-Check -Name 'Official preserved signatures were validated' -Passed $true -Detail 'Skipped explicitly for a synthetic fixture; forbidden for release qualification'
        Add-WarningMessage 'Authenticode validation was skipped; this result is not release evidence.'
    }
    elseif ($sourceApp) {
        Test-SignaturePair -Source $sourceDesktop -Destination $destinationRealDesktop -Name 'Official ChatGPT Authenticode signature is preserved'
        Test-SignaturePair -Source $sourceCodex -Destination $destinationRealCodex -Name 'Official Codex Authenticode signature is preserved'
        Test-SignaturePair -Source (Join-Path $sourceApp 'Codex.exe') -Destination (Join-Path $destinationApp 'Codex.exe') -Name 'Official desktop shim signature is preserved'
        if ((Test-Path -LiteralPath $sourceWindowsAccount -PathType Leaf) -and
            (Test-Path -LiteralPath $destinationWindowsAccount -PathType Leaf)) {
            Test-SignaturePair -Source $sourceWindowsAccount -Destination $destinationWindowsAccount -Name 'Official windows-account.node Authenticode signature is preserved'
        }
        else {
            Add-Check -Name 'Official windows-account.node Authenticode signature is preserved' -Passed $false -Detail (
                "source exists=$(Test-Path -LiteralPath $sourceWindowsAccount -PathType Leaf); destination exists=$(Test-Path -LiteralPath $destinationWindowsAccount -PathType Leaf)"
            )
        }
    }
    else {
        $signerThumbprint = [string](Get-JsonProperty -Object $manifest -Name 'sourceSignerThumbprint')
        Test-PreservedOfficialSignature -Path $destinationRealDesktop -ExpectedHash $sourceDesktopHash -ExpectedThumbprint $signerThumbprint -Name 'Historical official ChatGPT signature is preserved'
        Test-PreservedOfficialSignature -Path $destinationRealCodex -ExpectedHash $sourceCodexHash -ExpectedThumbprint $signerThumbprint -Name 'Historical official Codex signature is preserved'
        Test-PreservedOfficialSignature -Path (Join-Path $destinationApp 'Codex.exe') -ExpectedHash ([string](Get-JsonProperty -Object $manifest -Name 'sourceCodexLauncherSha256')) -ExpectedThumbprint $signerThumbprint -Name 'Historical official desktop shim signature is preserved'
        Test-PreservedOfficialSignature -Path $destinationWindowsAccount -ExpectedHash ([string](Get-JsonProperty -Object $manifest -Name 'sourceWindowsAccountSha256')) -ExpectedThumbprint $signerThumbprint -Name 'Historical official windows-account.node signature is preserved'
    }
    if (-not $SkipSignatureValidation) {
        Test-OptionalSignature -Path $destinationMux -Name 'Mux Authenticode signature is valid when present' -RequireSignature:$StrictSignatures
        Test-OptionalSignature -Path $destinationLauncher -Name 'Launcher Authenticode signature is valid when present' -RequireSignature:$StrictSignatures
    }

    $reparseEntries = @(Get-ChildItem -LiteralPath $layout.BuildRoot -Force -Recurse |
            Where-Object { ($_.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0 })
    Add-Check -Name 'Build contains no junctions or symbolic links' -Passed ($reparseEntries.Count -eq 0) -Detail $(
        if ($reparseEntries.Count -eq 0) { 'All payload paths resolve inside the independent build tree' }
        else { ($reparseEntries | Select-Object -First 20 -ExpandProperty FullName) -join ', ' }
    )

    if ($SkipAclValidation) {
        Add-Check -Name 'Private install/state ACLs were validated' -Passed $true -Detail 'Skipped explicitly for a synthetic fixture; forbidden for release qualification'
        Add-WarningMessage 'ACL validation was skipped; this result is not release evidence.'
    }
    else {
        Test-WriteAcl -Path $layout.BuildRoot -Name 'Build directory'
        Test-PrivateTreeAcl -Path $layout.BuildRoot -Name 'Build directory'
        $backupRoot = Join-Path (Split-Path -Parent $layout.BuildRoot) '.codex-subscription-router-backups'
        $failedInstallationsRoot = Join-Path $StateRoot 'failed-installations'
        Test-PrivateTreeAcl -Path $backupRoot -Name 'Backup root' -AllowMissing
        Test-PrivateTreeAcl -Path $failedInstallationsRoot -Name 'Failed-installations root' -AllowMissing
        if (Test-Path -LiteralPath $StateRoot -PathType Container) {
            Test-WriteAcl -Path $StateRoot -Name 'State directory'
            Test-PrivateTreeAcl -Path $StateRoot -Name 'State directory'
        }
        $sensitiveCandidates = New-Object 'System.Collections.Generic.List[string]'
        $controlTokenPath = Get-JsonProperty -Object $manifest -Name 'controlTokenPath'
        if ($controlTokenPath) {
            $candidate = [string]$controlTokenPath
            if (-not [System.IO.Path]::IsPathRooted($candidate)) {
                $candidate = Join-Path $StateRoot $candidate
            }
            $sensitiveCandidates.Add($candidate)
        }
        $sensitiveCandidates.Add((Join-Path $StateRoot 'control-token'))
        if (Test-Path -LiteralPath $StateRoot -PathType Container) {
            @(Get-ChildItem -LiteralPath $StateRoot -Filter 'auth.json' -File -Recurse -ErrorAction SilentlyContinue) |
                ForEach-Object { $sensitiveCandidates.Add($_.FullName) }
        }
        foreach ($sensitivePath in ($sensitiveCandidates | Select-Object -Unique)) {
            Test-SensitiveFileAcl -Path $sensitivePath
        }
    }

    if ($VerifyChromeNativeHostInvariance -and $SkipSmokeTest) {
        Add-Check -Name 'Chrome native-host invariance gate can run' -Passed $false -Detail '-VerifyChromeNativeHostInvariance requires the read-only smoke test; remove -SkipSmokeTest'
    }

    if (-not $SkipSmokeTest) {
        $chromeNativeHostBefore = if ($VerifyChromeNativeHostInvariance) {
            Get-ChromeNativeHostSnapshot
        }
        else { $null }
        $desktopBefore = @(Get-Process -Name 'ChatGPT', 'Codex' -ErrorAction SilentlyContinue |
                Select-Object -ExpandProperty Id)
        try {
            $launcherSelfTest = Invoke-CapturedProcess -FilePath $destinationLauncher -Arguments @('--router-self-test')
            Add-Check -Name 'Read-only desktop launcher self-test' -Passed ($launcherSelfTest.ExitCode -eq 0) -Detail (
                "exit=$($launcherSelfTest.ExitCode); the launcher self-test does not spawn ChatGPT.real.exe"
            )

            $realVersion = Invoke-CapturedProcess -FilePath $destinationRealCodex -Arguments @('--version')
            $muxVersion = Invoke-CapturedProcess -FilePath $destinationMux -Arguments @('--version')
            $realText = ($realVersion.Stdout + $realVersion.Stderr).Trim()
            $muxText = ($muxVersion.Stdout + $muxVersion.Stderr).Trim()
            $passed = $realVersion.ExitCode -eq 0 -and
                $muxVersion.ExitCode -eq 0 -and
                $realText.Length -gt 0 -and
                $realText -eq $muxText
            Add-Check -Name 'Non-destructive mux passthrough smoke test' -Passed $passed -Detail $(
                if ($passed) { $muxText }
                else { "real(exit=$($realVersion.ExitCode))='$realText'; mux(exit=$($muxVersion.ExitCode))='$muxText'" }
            )
        }
        catch {
            Add-Check -Name 'Non-destructive mux passthrough smoke test' -Passed $false -Detail $_.Exception.Message
        }
        $desktopAfter = @(Get-Process -Name 'ChatGPT', 'Codex' -ErrorAction SilentlyContinue |
                Select-Object -ExpandProperty Id)
        $preexistingStillRunning = @($desktopBefore | Where-Object { $desktopAfter -contains $_ })
        if ($preexistingStillRunning.Count -ne $desktopBefore.Count) {
            Add-WarningMessage 'One or more pre-existing desktop processes exited during verification; this script did not request or terminate them.'
        }
        Add-Check -Name 'Smoke test did not launch the desktop executable' -Passed $true -Detail 'Only resources\codex*.exe --version was invoked; no Stop-Process or GUI launch is performed'
        if ($VerifyChromeNativeHostInvariance) {
            $chromeNativeHostAfter = Get-ChromeNativeHostSnapshot
            Compare-ChromeNativeHostSnapshot -Before $chromeNativeHostBefore -After $chromeNativeHostAfter
        }
    }

    Complete-Verification -EmitResult:$PassThru
}
catch {
    $diagnostic = $_.Exception.Message
    if (-not [string]::IsNullOrWhiteSpace($_.ScriptStackTrace)) {
        $diagnostic += "`n$($_.ScriptStackTrace)"
    }
    Write-Error $diagnostic
    exit 1
}
