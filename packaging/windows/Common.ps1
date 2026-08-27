#requires -Version 7.2

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-RouterAbsolutePath {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Path,

        [switch] $MustExist
    )

    $expanded = [Environment]::ExpandEnvironmentVariables($Path)
    if (-not [IO.Path]::IsPathRooted($expanded)) {
        $expanded = Join-Path -Path (Get-Location).Path -ChildPath $expanded
    }

    $fullPath = [IO.Path]::GetFullPath($expanded).TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    )
    if ($MustExist -and -not (Test-Path -LiteralPath $fullPath)) {
        throw "Path does not exist: $fullPath"
    }

    return $fullPath
}

function Test-RouterPathContainedBy {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Candidate,

        [Parameter(Mandatory)]
        [string] $Parent
    )

    $candidatePath = (Resolve-RouterAbsolutePath -Path $Candidate) + [IO.Path]::DirectorySeparatorChar
    $parentPath = (Resolve-RouterAbsolutePath -Path $Parent) + [IO.Path]::DirectorySeparatorChar
    return $candidatePath.StartsWith($parentPath, [StringComparison]::OrdinalIgnoreCase)
}

function Test-RouterItemIsReparsePoint {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [IO.FileSystemInfo] $Item
    )

    return ($Item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0
}

function Assert-RouterPathWithoutReparsePoint {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Path
    )

    $candidate = Resolve-RouterAbsolutePath -Path $Path
    while (-not (Test-Path -LiteralPath $candidate)) {
        $parent = Split-Path -Path $candidate -Parent
        if (-not $parent -or $parent -eq $candidate) {
            throw "Could not find an existing ancestor for path: $Path"
        }
        $candidate = $parent
    }

    while ($candidate) {
        $item = Get-Item -LiteralPath $candidate -Force -ErrorAction Stop
        if (Test-RouterItemIsReparsePoint -Item $item) {
            throw "Packaging paths must not traverse a reparse point: $($item.FullName)"
        }

        $parent = Split-Path -Path $candidate -Parent
        if (-not $parent -or $parent -eq $candidate) {
            break
        }
        $candidate = $parent
    }
}

function Assert-RouterTreeWithoutReparsePoint {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Path
    )

    $root = Resolve-RouterAbsolutePath -Path $Path -MustExist
    Assert-RouterPathWithoutReparsePoint -Path $root

    $pending = [Collections.Generic.Queue[string]]::new()
    $pending.Enqueue($root)
    while ($pending.Count -gt 0) {
        $currentPath = $pending.Dequeue()
        $current = Get-Item -LiteralPath $currentPath -Force -ErrorAction Stop
        if (Test-RouterItemIsReparsePoint -Item $current) {
            throw "Packaging trees must not contain a reparse point: $($current.FullName)"
        }
        if (-not $current.PSIsContainer) {
            continue
        }

        foreach ($child in @(Get-ChildItem -LiteralPath $current.FullName -Force -ErrorAction Stop)) {
            if (Test-RouterItemIsReparsePoint -Item $child) {
                throw "Packaging trees must not contain a reparse point: $($child.FullName)"
            }
            if ($child.PSIsContainer) {
                $pending.Enqueue($child.FullName)
            }
        }
    }
}

function Assert-RouterSafeOutputPath {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $OutputPath,

        [string] $SourcePath
    )

    $target = Resolve-RouterAbsolutePath -Path $OutputPath
    Assert-RouterPathWithoutReparsePoint -Path $target
    $driveRoot = [IO.Path]::GetPathRoot($target).TrimEnd('\', '/')
    if ($target.TrimEnd('\', '/') -eq $driveRoot) {
        throw "A drive root is not a valid packaging output: $target"
    }

    if ($env:ProgramFiles) {
        $windowsApps = Join-Path -Path $env:ProgramFiles -ChildPath 'WindowsApps'
        if (Test-RouterPathContainedBy -Candidate $target -Parent $windowsApps) {
            throw "Packaging output must never be inside WindowsApps: $target"
        }
    }

    if ($SourcePath) {
        $source = Resolve-RouterAbsolutePath -Path $SourcePath -MustExist
        Assert-RouterTreeWithoutReparsePoint -Path $source
        if (
            $target.Equals($source, [StringComparison]::OrdinalIgnoreCase) -or
            (Test-RouterPathContainedBy -Candidate $target -Parent $source) -or
            (Test-RouterPathContainedBy -Candidate $source -Parent $target)
        ) {
            throw "Source and output must be separate, non-nested directories. Source: $source; output: $target"
        }
    }

    return $target
}

function Get-RouterPayloadLayout {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $SourceRoot
    )

    $source = Resolve-RouterAbsolutePath -Path $SourceRoot -MustExist
    Assert-RouterTreeWithoutReparsePoint -Path $source
    $packageApp = Join-Path -Path $source -ChildPath 'app\ChatGPT.exe'
    $flatApp = Join-Path -Path $source -ChildPath 'ChatGPT.exe'

    if (Test-Path -LiteralPath $packageApp -PathType Leaf) {
        return [pscustomobject]@{
            SourceRoot = $source
            AppRoot    = Join-Path -Path $source -ChildPath 'app'
            AssetsRoot = Join-Path -Path $source -ChildPath 'assets'
            Layout     = 'package-root'
        }
    }

    if (Test-Path -LiteralPath $flatApp -PathType Leaf) {
        return [pscustomobject]@{
            SourceRoot = $source
            AppRoot    = $source
            AssetsRoot = $null
            Layout     = 'app-root'
        }
    }

    throw "SourceRoot must contain either app\ChatGPT.exe or ChatGPT.exe: $source"
}

function Assert-RouterPatchedPayload {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $AppRoot,

        [switch] $AllowUnpatchedPayload
    )

    $required = @(
        'ChatGPT.exe',
        'resources\app.asar',
        'resources\codex.exe'
    )
    foreach ($relativePath in $required) {
        $candidate = Join-Path -Path $AppRoot -ChildPath $relativePath
        if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            throw "Required payload file is missing: $candidate"
        }
    }

    if (-not $AllowUnpatchedPayload) {
        $patchOutputs = @(
            'ChatGPT.real.exe',
            'resources\codex.real.exe'
        )
        foreach ($relativePath in $patchOutputs) {
            $candidate = Join-Path -Path $AppRoot -ChildPath $relativePath
            if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
                throw "Payload does not look safely patched; expected $candidate. Use -AllowUnpatchedPayload only for packaging tests."
            }
        }
    }
}

function Get-RouterFileHashOrNull {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Path
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return $null
    }

    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-RouterTreeHashEntry {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Root,

        [string[]] $ExcludeRelativePath = @()
    )

    $resolvedRoot = Resolve-RouterAbsolutePath -Path $Root -MustExist
    $rootItem = Get-Item -LiteralPath $resolvedRoot -Force -ErrorAction Stop
    if (-not $rootItem.PSIsContainer) {
        throw "Hash manifest root must be a directory: $resolvedRoot"
    }
    Assert-RouterTreeWithoutReparsePoint -Path $resolvedRoot

    $excluded = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($relativePath in $ExcludeRelativePath) {
        [void] $excluded.Add($relativePath.Replace('\', '/'))
    }

    $entries = [Collections.Generic.List[object]]::new()
    $pending = [Collections.Generic.Queue[string]]::new()
    $pending.Enqueue($resolvedRoot)
    while ($pending.Count -gt 0) {
        $current = $pending.Dequeue()
        $currentItem = Get-Item -LiteralPath $current -Force -ErrorAction Stop
        if ((Test-RouterItemIsReparsePoint -Item $currentItem) -or -not $currentItem.PSIsContainer) {
            throw "Hash manifest traversal encountered a replaced or reparse directory: $current"
        }
        foreach ($item in @(Get-ChildItem -LiteralPath $current -Force -ErrorAction Stop)) {
            if (Test-RouterItemIsReparsePoint -Item $item) {
                throw "Hash manifests must not follow a reparse point: $($item.FullName)"
            }
            if ($item.PSIsContainer) {
                $pending.Enqueue($item.FullName)
                continue
            }

            $relativePath = [IO.Path]::GetRelativePath($resolvedRoot, $item.FullName).Replace('\', '/')
            if ($excluded.Contains($relativePath)) {
                continue
            }
            $entries.Add([pscustomobject][ordered]@{
                path = $relativePath
                size = [int64] $item.Length
                sha256 = (Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
            })
        }
    }

    return @($entries | Sort-Object -Property @{ Expression = 'path'; Ascending = $true })
}

function Assert-RouterHashEntriesEqual {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [AllowEmptyCollection()]
        [object[]] $Expected,

        [Parameter(Mandatory)]
        [AllowEmptyCollection()]
        [object[]] $Actual,

        [string] $Context = 'tree verification'
    )

    if ($Expected.Count -ne $Actual.Count) {
        throw "$Context failed: expected $($Expected.Count) files, found $($Actual.Count)."
    }
    for ($index = 0; $index -lt $Expected.Count; $index++) {
        $expectedEntry = $Expected[$index]
        $actualEntry = $Actual[$index]
        if (
            ([string] $expectedEntry.path) -cne ([string] $actualEntry.path) -or
            ([int64] $expectedEntry.size) -ne ([int64] $actualEntry.size) -or
            ([string] $expectedEntry.sha256) -cne ([string] $actualEntry.sha256)
        ) {
            throw "$Context failed at '$($expectedEntry.path)': expected size/hash $($expectedEntry.size)/$($expectedEntry.sha256), found '$($actualEntry.path)' $($actualEntry.size)/$($actualEntry.sha256)."
        }
    }
}

function Copy-RouterTreeVerified {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Source,

        [Parameter(Mandatory)]
        [string] $Destination
    )

    $sourceRoot = Resolve-RouterAbsolutePath -Path $Source -MustExist
    $destinationRoot = Resolve-RouterAbsolutePath -Path $Destination
    Assert-RouterTreeWithoutReparsePoint -Path $sourceRoot
    Assert-RouterPathWithoutReparsePoint -Path $destinationRoot
    if (Test-Path -LiteralPath $destinationRoot) {
        throw "Verified copy destination must not already exist: $destinationRoot"
    }

    $before = @(Get-RouterTreeHashEntry -Root $sourceRoot)
    Copy-Item -LiteralPath $sourceRoot -Destination $destinationRoot -Recurse -Force -ErrorAction Stop
    Assert-RouterTreeWithoutReparsePoint -Path $destinationRoot
    $copied = @(Get-RouterTreeHashEntry -Root $destinationRoot)
    $after = @(Get-RouterTreeHashEntry -Root $sourceRoot)
    Assert-RouterHashEntriesEqual -Expected $before -Actual $after -Context 'source stability verification'
    Assert-RouterHashEntriesEqual -Expected $before -Actual $copied -Context 'verified copy'
}

function Copy-RouterFileVerified {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Source,

        [Parameter(Mandatory)]
        [string] $Destination
    )

    $sourcePath = Resolve-RouterAbsolutePath -Path $Source -MustExist
    $sourceItem = Get-Item -LiteralPath $sourcePath -Force -ErrorAction Stop
    if ($sourceItem.PSIsContainer -or (Test-RouterItemIsReparsePoint -Item $sourceItem)) {
        throw "Verified file copy requires a regular, non-reparse source file: $sourcePath"
    }
    Assert-RouterPathWithoutReparsePoint -Path $sourcePath
    $destinationPath = Resolve-RouterAbsolutePath -Path $Destination
    Assert-RouterPathWithoutReparsePoint -Path $destinationPath
    if (Test-Path -LiteralPath $destinationPath) {
        throw "Verified file copy destination must not already exist: $destinationPath"
    }

    $before = Get-RouterFileHashOrNull -Path $sourcePath
    $beforeLength = $sourceItem.Length
    Copy-Item -LiteralPath $sourcePath -Destination $destinationPath -Force -ErrorAction Stop
    $destinationItem = Get-Item -LiteralPath $destinationPath -Force -ErrorAction Stop
    if (Test-RouterItemIsReparsePoint -Item $destinationItem) {
        throw "Verified file copy produced a reparse point: $destinationPath"
    }
    $after = Get-RouterFileHashOrNull -Path $sourcePath
    $copied = Get-RouterFileHashOrNull -Path $destinationPath
    if ($before -cne $after -or $before -cne $copied -or $beforeLength -ne $destinationItem.Length) {
        throw "Verified file copy failed or the source changed while copying: $sourcePath"
    }
}

function Write-RouterTreeHashManifest {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Root,

        [string] $ManifestRelativePath = 'router-package.files.json',

        [string] $Kind = 'windows-package-files'
    )

    if ([IO.Path]::IsPathRooted($ManifestRelativePath) -or $ManifestRelativePath -match '(^|[\\/])\.\.([\\/]|$)') {
        throw "ManifestRelativePath must be a safe relative path: $ManifestRelativePath"
    }
    $rootPath = Resolve-RouterAbsolutePath -Path $Root -MustExist
    $manifestPath = Join-Path -Path $rootPath -ChildPath $ManifestRelativePath
    if (Test-Path -LiteralPath $manifestPath) {
        throw "Hash manifest already exists: $manifestPath"
    }

    $manifest = [ordered]@{
        schemaVersion = 1
        kind = $Kind
        algorithm = 'SHA256'
        manifestExcludesSelf = $true
        files = @(Get-RouterTreeHashEntry -Root $rootPath -ExcludeRelativePath $ManifestRelativePath)
    }
    $manifestJson = $manifest | ConvertTo-Json -Depth 8
    $manifestDirectory = Split-Path -Path $manifestPath -Parent
    New-Item -ItemType Directory -Path $manifestDirectory -Force | Out-Null
    [IO.File]::WriteAllText($manifestPath, $manifestJson + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
    return $manifestPath
}

function Assert-RouterTreeHashManifest {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Root,

        [string] $ManifestRelativePath = 'router-package.files.json'
    )

    $rootPath = Resolve-RouterAbsolutePath -Path $Root -MustExist
    $manifestPath = Join-Path -Path $rootPath -ChildPath $ManifestRelativePath
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "Hash manifest is missing: $manifestPath"
    }
    $manifest = Get-Content -LiteralPath $manifestPath -Raw -ErrorAction Stop | ConvertFrom-Json -ErrorAction Stop
    if (
        $manifest.schemaVersion -ne 1 -or
        $manifest.algorithm -cne 'SHA256' -or
        $manifest.manifestExcludesSelf -ne $true
    ) {
        throw "Unsupported or malformed hash manifest: $manifestPath"
    }

    $expected = @($manifest.files)
    foreach ($entry in $expected) {
        $relativePath = [string] $entry.path
        if (
            [string]::IsNullOrWhiteSpace($relativePath) -or
            [IO.Path]::IsPathRooted($relativePath) -or
            $relativePath -match '(^|[\\/])\.\.([\\/]|$)' -or
            $relativePath.Equals($ManifestRelativePath.Replace('\', '/'), [StringComparison]::OrdinalIgnoreCase) -or
            ([string] $entry.sha256) -notmatch '^[0-9a-f]{64}$' -or
            ([int64] $entry.size) -lt 0
        ) {
            throw "Unsafe or malformed hash manifest entry: '$relativePath'"
        }
    }

    $actual = @(Get-RouterTreeHashEntry -Root $rootPath -ExcludeRelativePath $ManifestRelativePath)
    Assert-RouterHashEntriesEqual -Expected $expected -Actual $actual -Context 'hash manifest verification'
}

function Remove-RouterTreeSafely {
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Low')]
    param(
        [Parameter(Mandatory)]
        [string] $Path
    )

    if (-not (Test-Path -LiteralPath $Path)) {
        return
    }
    Assert-RouterTreeWithoutReparsePoint -Path $Path
    if ($PSCmdlet.ShouldProcess($Path, 'Remove verified packaging tree or file')) {
        Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction Stop
    }
}

function Find-RouterWindowsSdkTool {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [ValidateSet('makeappx.exe', 'signtool.exe')]
        [string] $Name
    )

    $command = Get-Command -Name $Name -CommandType Application -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }

    $programFilesX86 = [Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFilesX86)
    if (-not $programFilesX86) {
        return $null
    }

    $binRoot = Join-Path -Path $programFilesX86 -ChildPath 'Windows Kits\10\bin'
    if (-not (Test-Path -LiteralPath $binRoot -PathType Container)) {
        return $null
    }

    $candidates = Get-ChildItem -LiteralPath $binRoot -Directory -ErrorAction SilentlyContinue |
        Sort-Object -Property Name -Descending |
        ForEach-Object { Join-Path -Path $_.FullName -ChildPath "x64\$Name" } |
        Where-Object { Test-Path -LiteralPath $_ -PathType Leaf }
    return $candidates | Select-Object -First 1
}

function ConvertTo-RouterXmlEscapedText {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string] $Value
    )

    return [Security.SecurityElement]::Escape($Value)
}

function Expand-RouterAppxManifest {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $TemplatePath,

        [Parameter(Mandatory)]
        [hashtable] $Values,

        [Parameter(Mandatory)]
        [string] $DestinationPath
    )

    $template = Get-Content -LiteralPath $TemplatePath -Raw
    foreach ($key in $Values.Keys) {
        $token = '{{' + $key + '}}'
        $template = $template.Replace($token, (ConvertTo-RouterXmlEscapedText -Value ([string] $Values[$key])))
    }

    $unresolved = [regex]::Matches($template, '{{[A-Z0-9_]+}}') | ForEach-Object Value | Sort-Object -Unique
    if ($unresolved) {
        throw "Manifest template contains unresolved tokens: $($unresolved -join ', ')"
    }

    try {
        [xml] $null = $template
    }
    catch {
        throw "Rendered AppxManifest.xml is not valid XML: $($_.Exception.Message)"
    }

    $destination = Resolve-RouterAbsolutePath -Path $DestinationPath
    $destinationDirectory = Split-Path -Path $destination -Parent
    New-Item -ItemType Directory -Path $destinationDirectory -Force | Out-Null
    [IO.File]::WriteAllText($destination, $template, [Text.UTF8Encoding]::new($false))
    return $destination
}
