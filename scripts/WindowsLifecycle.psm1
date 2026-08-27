#Requires -Version 5.1

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'

function Resolve-CsrFullPath {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [switch]$MustExist
    )

    $expanded = [Environment]::ExpandEnvironmentVariables($Path)
    $full = [IO.Path]::GetFullPath($expanded)
    if ($MustExist -and -not (Test-Path -LiteralPath $full)) {
        throw "Path does not exist: $full"
    }
    if (Test-Path -LiteralPath $full) {
        # Get-Item expands an existing Windows 8.3 alias such as RUNNER~1 to
        # the filesystem's long spelling without following a reparse target.
        # This keeps process paths, manifests, and lifecycle roots comparable.
        $full = (Get-Item -LiteralPath $full -Force).FullName
    }
    $root = [IO.Path]::GetPathRoot($full)
    if ($full.Equals($root, [StringComparison]::OrdinalIgnoreCase)) {
        return $root
    }
    return $full.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
}

function Test-CsrPathWithin {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$Parent
    )

    $candidateFull = (Resolve-CsrFullPath -Path $Candidate) + [IO.Path]::DirectorySeparatorChar
    $parentFull = (Resolve-CsrFullPath -Path $Parent) + [IO.Path]::DirectorySeparatorChar
    return $candidateFull.StartsWith($parentFull, [StringComparison]::OrdinalIgnoreCase)
}

function Assert-CsrNoReparseAncestor {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$AllowedRoot
    )

    $current = Resolve-CsrFullPath -Path $Path
    $boundary = Resolve-CsrFullPath -Path $AllowedRoot -MustExist
    while ($true) {
        if (Test-Path -LiteralPath $current) {
            $item = Get-Item -LiteralPath $current -Force
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "Managed path traverses a junction or symbolic link: $current"
            }
        }
        if ($current.Equals($boundary, [StringComparison]::OrdinalIgnoreCase)) {
            break
        }
        $parent = Split-Path -Parent $current
        if ([string]::IsNullOrWhiteSpace($parent) -or $parent.Equals($current, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Managed path is outside the allowed root: $Path"
        }
        $current = Resolve-CsrFullPath -Path $parent
    }
}

function Assert-CsrTreeHasNoReparsePoints {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Root)

    $resolved = Resolve-CsrFullPath -Path $Root -MustExist
    $pending = [Collections.Generic.Stack[string]]::new()
    $pending.Push($resolved)
    while ($pending.Count -gt 0) {
        $directory = $pending.Pop()
        foreach ($item in @(Get-ChildItem -LiteralPath $directory -Force -ErrorAction Stop)) {
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "Router tree contains a junction, symbolic link, or other reparse point: $($item.FullName)"
            }
            if ($item.PSIsContainer) { $pending.Push($item.FullName) }
        }
    }
}

function Assert-CsrManagedPaths {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Destination,
        [Parameter(Mandatory = $true)][string]$StateRoot,
        [Parameter(Mandatory = $true)][string]$BackupRoot,
        [Parameter(Mandatory = $true)][string]$AllowedRoot
    )

    $allowed = Resolve-CsrFullPath -Path $AllowedRoot -MustExist
    $paths = @(
        [PSCustomObject]@{ Name = 'Destination'; Path = (Resolve-CsrFullPath -Path $Destination) },
        [PSCustomObject]@{ Name = 'StateRoot'; Path = (Resolve-CsrFullPath -Path $StateRoot) },
        [PSCustomObject]@{ Name = 'BackupRoot'; Path = (Resolve-CsrFullPath -Path $BackupRoot) }
    )
    foreach ($entry in $paths) {
        if ($entry.Path.Equals($allowed, [StringComparison]::OrdinalIgnoreCase) -or
            -not (Test-CsrPathWithin -Candidate $entry.Path -Parent $allowed)) {
            throw "$($entry.Name) must be a strict child of the allowed root '$allowed': $($entry.Path)"
        }
        Assert-CsrNoReparseAncestor -Path $entry.Path -AllowedRoot $allowed
    }

    $destinationPath = $paths[0].Path
    $statePath = $paths[1].Path
    $backupPath = $paths[2].Path
    if ($destinationPath.Equals($statePath, [StringComparison]::OrdinalIgnoreCase) -or
        (Test-CsrPathWithin -Candidate $destinationPath -Parent $statePath) -or
        (Test-CsrPathWithin -Candidate $statePath -Parent $destinationPath)) {
        throw 'Destination and StateRoot must be separate, non-overlapping trees.'
    }
    if ($backupPath.Equals($destinationPath, [StringComparison]::OrdinalIgnoreCase) -or
        (Test-CsrPathWithin -Candidate $backupPath -Parent $destinationPath) -or
        (Test-CsrPathWithin -Candidate $destinationPath -Parent $backupPath)) {
        throw 'BackupRoot and Destination must be separate, non-overlapping trees.'
    }
    if ($backupPath.Equals($statePath, [StringComparison]::OrdinalIgnoreCase) -or
        (Test-CsrPathWithin -Candidate $backupPath -Parent $statePath) -or
        (Test-CsrPathWithin -Candidate $statePath -Parent $backupPath)) {
        throw 'BackupRoot and StateRoot must be separate, non-overlapping trees.'
    }
}

function Get-CsrFileHash {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Required router file is missing: $Path"
    }
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-CsrTreeDigest {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Root)

    $resolved = Resolve-CsrFullPath -Path $Root -MustExist
    if (-not (Test-Path -LiteralPath $resolved -PathType Container)) { throw "Required router tree is missing: $resolved" }
    # Keep this byte-for-byte compatible with the deterministic tree summary
    # used by the inventory and release verifier. Windows path ordering is not
    # equivalent to sorting the original strings with OrdinalIgnoreCase (for
    # example, '_' and letters can be ordered differently). Fold first, sort
    # the folded keys with Ordinal, but hash the original relative path.
    $recordsByFoldedPath = New-Object 'Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    foreach ($file in @(Get-ChildItem -LiteralPath $resolved -File -Recurse -Force -ErrorAction Stop)) {
        if (($file.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "Router integrity tree contains a symbolic link or reparse file: $($file.FullName)"
        }
        $relative = $file.FullName.Substring($resolved.Length).TrimStart('\', '/').Replace('\', '/')
        $folded = $relative.ToLowerInvariant()
        if ($recordsByFoldedPath.ContainsKey($folded)) {
            throw "Router integrity tree contains paths that differ only by case: $relative"
        }
        $recordsByFoldedPath.Add($folded, [PSCustomObject]@{
            RelativePath = $relative
            Hash = Get-CsrFileHash -Path $file.FullName
        })
    }
    $names = [string[]]@($recordsByFoldedPath.Keys)
    [Array]::Sort($names, [StringComparer]::Ordinal)
    $builder = New-Object Text.StringBuilder
    foreach ($name in $names) {
        $record = $recordsByFoldedPath[$name]
        [void]$builder.Append($record.RelativePath)
        [void]$builder.Append([char]0)
        [void]$builder.Append($record.Hash)
        [void]$builder.Append("`n")
    }
    $algorithm = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($builder.ToString())
        return ([BitConverter]::ToString($algorithm.ComputeHash($bytes))).Replace('-', '').ToLowerInvariant()
    }
    finally { $algorithm.Dispose() }
}

function Read-CsrManifest {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$LayoutPath,
        [Parameter(Mandatory = $true)][string]$ExpectedDestination,
        [string]$ExpectedStateRoot
    )

    $layout = Resolve-CsrFullPath -Path $LayoutPath -MustExist
    $manifestPath = Join-Path $layout 'codex-mux-build.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "Refusing to manage an unauthenticated directory; build manifest is missing: $manifestPath"
    }
    try {
        $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    }
    catch {
        throw "Router build manifest is invalid: $manifestPath ($($_.Exception.Message))"
    }
    foreach ($field in @('schemaVersion', 'destination', 'profilePath', 'patchedAsarSha256', 'muxSha256', 'launcherSha256')) {
        if ($null -eq $manifest.PSObject.Properties[$field] -or [string]::IsNullOrWhiteSpace([string]$manifest.$field)) {
            throw "Router build manifest lacks required field '$field': $manifestPath"
        }
    }
    $schemaVersion = [int]$manifest.schemaVersion
    if ($schemaVersion -notin @(1, 2)) {
        throw "Unsupported router build manifest schema: $($manifest.schemaVersion)"
    }
    if ($schemaVersion -eq 2) {
        if ($null -eq $manifest.PSObject.Properties['controlPort'] -or
            [int]$manifest.controlPort -lt 49152 -or [int]$manifest.controlPort -gt 65535) {
            throw "Router build manifest schema 2 has an invalid controlPort: $manifestPath"
        }
    }
    $recordedDestination = Resolve-CsrFullPath -Path ([string]$manifest.destination)
    $expected = Resolve-CsrFullPath -Path $ExpectedDestination
    if (-not $recordedDestination.Equals($expected, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Manifest destination '$recordedDestination' does not authenticate expected destination '$expected'."
    }
    if (-not [string]::IsNullOrWhiteSpace($ExpectedStateRoot)) {
        $recordedProfile = Resolve-CsrFullPath -Path ([string]$manifest.profilePath)
        $expectedProfile = Resolve-CsrFullPath -Path (Join-Path $ExpectedStateRoot 'Profile')
        if (-not $recordedProfile.Equals($expectedProfile, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Manifest profile '$recordedProfile' does not authenticate StateRoot '$ExpectedStateRoot'."
        }
    }
    return $manifest
}

function Assert-CsrInstallationIntegrity {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$LayoutPath,
        [Parameter(Mandatory = $true)][string]$ExpectedDestination,
        [string]$ExpectedStateRoot
    )

    $layout = Resolve-CsrFullPath -Path $LayoutPath -MustExist
    Assert-CsrTreeHasNoReparsePoints -Root $layout
    $manifest = Read-CsrManifest -LayoutPath $layout -ExpectedDestination $ExpectedDestination -ExpectedStateRoot $ExpectedStateRoot
    $checks = @(
        [PSCustomObject]@{ Name = 'patched app.asar'; Path = (Join-Path $layout 'resources\app.asar'); Hash = [string]$manifest.patchedAsarSha256 },
        [PSCustomObject]@{ Name = 'multiplexer'; Path = (Join-Path $layout 'resources\codex.exe'); Hash = [string]$manifest.muxSha256 },
        [PSCustomObject]@{ Name = 'desktop launcher'; Path = (Join-Path $layout 'ChatGPT.exe'); Hash = [string]$manifest.launcherSha256 }
    )
    if ($null -ne $manifest.PSObject.Properties['sourceCodexSha256'] -and
        -not [string]::IsNullOrWhiteSpace([string]$manifest.sourceCodexSha256) -and
        (Test-Path -LiteralPath (Join-Path $layout 'resources\codex.real.exe') -PathType Leaf)) {
        $checks += [PSCustomObject]@{ Name = 'preserved official Codex CLI'; Path = (Join-Path $layout 'resources\codex.real.exe'); Hash = [string]$manifest.sourceCodexSha256 }
    }
    if ($null -ne $manifest.PSObject.Properties['sourceChatGptSha256'] -and
        -not [string]::IsNullOrWhiteSpace([string]$manifest.sourceChatGptSha256) -and
        (Test-Path -LiteralPath (Join-Path $layout 'ChatGPT.real.exe') -PathType Leaf)) {
        $checks += [PSCustomObject]@{ Name = 'preserved official desktop'; Path = (Join-Path $layout 'ChatGPT.real.exe'); Hash = [string]$manifest.sourceChatGptSha256 }
    }
    if ($null -ne $manifest.PSObject.Properties['preservation'] -and
        $null -ne $manifest.preservation.PSObject.Properties['cliHelpers']) {
        foreach ($property in $manifest.preservation.cliHelpers.PSObject.Properties) {
            $checks += [PSCustomObject]@{
                Name = "preserved helper $($property.Name)"
                Path = (Join-Path $layout (Join-Path 'resources' $property.Name))
                Hash = [string]$property.Value
            }
        }
    }
    foreach ($check in $checks) {
        $actual = Get-CsrFileHash -Path $check.Path
        if (-not $actual.Equals($check.Hash, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing lifecycle operation because $($check.Name) hash does not match its manifest: $($check.Path)"
        }
    }
    if ($null -ne $manifest.PSObject.Properties['preservation'] -and
        $null -ne $manifest.preservation.PSObject.Properties['preservedResourceTrees']) {
        foreach ($property in $manifest.preservation.preservedResourceTrees.PSObject.Properties) {
            $tree = Join-Path $layout (Join-Path 'resources' $property.Name)
            $actualTreeHash = Get-CsrTreeDigest -Root $tree
            if (-not $actualTreeHash.Equals([string]$property.Value, [StringComparison]::OrdinalIgnoreCase)) {
                throw "Refusing lifecycle operation because preserved tree '$($property.Name)' does not match its manifest: $tree"
            }
        }
    }
    return $manifest
}

function New-CsrPrivateAcl {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][bool]$Directory)

    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $systemSid = New-Object Security.Principal.SecurityIdentifier(
        [Security.Principal.WellKnownSidType]::LocalSystemSid, $null
    )
    $acl = if ($Directory) {
        New-Object Security.AccessControl.DirectorySecurity
    }
    else {
        New-Object Security.AccessControl.FileSecurity
    }
    $acl.SetOwner($currentSid)
    $acl.SetAccessRuleProtection($true, $false)
    $inheritance = if ($Directory) {
        [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit
    }
    else { [Security.AccessControl.InheritanceFlags]::None }
    $propagation = [Security.AccessControl.PropagationFlags]::None
    $allow = [Security.AccessControl.AccessControlType]::Allow
    $full = [Security.AccessControl.FileSystemRights]::FullControl
    foreach ($sid in @($currentSid, $systemSid)) {
        $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sid, $full, $inheritance, $propagation, $allow)))
    }
    return $acl
}

function Set-CsrOwnerAndDacl {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][IO.FileSystemInfo]$Item,
        [Parameter(Mandatory = $true)]$Acl
    )

    # Set-Acl asks the PowerShell filesystem provider to persist security
    # descriptor sections that were not requested here. On Store-derived trees
    # that can include SACL state and fail for an ordinary desktop user with a
    # SeSecurityPrivilege error. Persist the descriptor through the .NET access
    # control APIs instead: the object marks only Owner and Access/DACL as
    # modified, so no SACL is read or written and elevation is not required.
    if ($Item.PSIsContainer) {
        $directory = New-Object IO.DirectoryInfo($Item.FullName)
        if ($PSVersionTable.PSEdition -eq 'Desktop') {
            [IO.Directory]::SetAccessControl($Item.FullName, [Security.AccessControl.DirectorySecurity]$Acl)
        }
        else {
            [IO.FileSystemAclExtensions]::SetAccessControl($directory, [Security.AccessControl.DirectorySecurity]$Acl)
        }
    }
    else {
        $file = New-Object IO.FileInfo($Item.FullName)
        if ($PSVersionTable.PSEdition -eq 'Desktop') {
            [IO.File]::SetAccessControl($Item.FullName, [Security.AccessControl.FileSecurity]$Acl)
        }
        else {
            [IO.FileSystemAclExtensions]::SetAccessControl($file, [Security.AccessControl.FileSecurity]$Acl)
        }
    }
}

function Set-CsrPrivateDirectoryAcl {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)

    $root = Resolve-CsrFullPath -Path $Path -MustExist
    if (-not (Test-Path -LiteralPath $root -PathType Container)) { throw "Private ACL target is not a directory: $root" }
    Assert-CsrTreeHasNoReparsePoints -Root $root
    $items = @(Get-ChildItem -LiteralPath $root -Recurse -Force -ErrorAction Stop)
    $total = $items.Count + 1
    $index = 0
    foreach ($item in @((Get-Item -LiteralPath $root -Force)) + $items) {
        $index++
        if (($index % 250) -eq 0 -or $index -eq $total) {
            Write-Progress -Activity 'Hardening router files for the current user and SYSTEM' -Status "$index of $total" -PercentComplete (($index * 100) / $total)
        }
        $acl = New-CsrPrivateAcl -Directory ([bool]$item.PSIsContainer)
        Set-CsrOwnerAndDacl -Item $item -Acl $acl
    }
    Write-Progress -Activity 'Hardening router files for the current user and SYSTEM' -Completed
    Assert-CsrPrivateDirectoryAcl -Path $root
}

function Assert-CsrPrivateDirectoryAcl {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)

    $root = Resolve-CsrFullPath -Path $Path -MustExist
    $allowed = @(
        [Security.Principal.WindowsIdentity]::GetCurrent().User.Value,
        (New-Object Security.Principal.SecurityIdentifier([Security.Principal.WellKnownSidType]::LocalSystemSid, $null)).Value
    )
    foreach ($item in @((Get-Item -LiteralPath $root -Force)) + @(Get-ChildItem -LiteralPath $root -Recurse -Force -ErrorAction Stop)) {
        $acl = Get-Acl -LiteralPath $item.FullName
        if (-not $acl.AreAccessRulesProtected) { throw "Router ACL still inherits permissions: $($item.FullName)" }
        try { $ownerSid = (New-Object Security.Principal.NTAccount($acl.Owner)).Translate([Security.Principal.SecurityIdentifier]).Value }
        catch { throw "Router ACL owner cannot be resolved: $($item.FullName)" }
        if ($ownerSid -ne $allowed[0]) { throw "Router ACL owner is not the current user: $($item.FullName)" }
        $seen = [Collections.Generic.List[string]]::new()
        foreach ($rule in $acl.Access) {
            if ($rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow) { continue }
            try { $sid = $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value }
            catch { throw "Router ACL contains an unresolvable identity: $($item.FullName)" }
            if ($allowed -notcontains $sid) { throw "Router ACL grants access to unexpected SID ${sid}: $($item.FullName)" }
            if (($rule.FileSystemRights -band [Security.AccessControl.FileSystemRights]::FullControl) -ne [Security.AccessControl.FileSystemRights]::FullControl) {
                throw "Router ACL does not grant FullControl to required SID ${sid}: $($item.FullName)"
            }
            $seen.Add($sid)
        }
        foreach ($sid in $allowed) {
            if ($seen -notcontains $sid) { throw "Router ACL is missing required SID ${sid}: $($item.FullName)" }
        }
    }
    return $true
}

function Get-CsrLifecycleMutexName {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$AllowedRoot)

    $root = Resolve-CsrFullPath -Path $AllowedRoot
    $temporary = Resolve-CsrFullPath -Path ([IO.Path]::GetTempPath())
    if ((Test-CsrPathWithin -Candidate $root -Parent $temporary)) {
        $algorithm = [Security.Cryptography.SHA256]::Create()
        try {
            $bytes = [Text.Encoding]::UTF8.GetBytes($root.ToLowerInvariant())
            $suffix = ([BitConverter]::ToString($algorithm.ComputeHash($bytes))).Replace('-', '').Substring(0, 16)
            return "Local\CodexSubscriptionRouterInstaller-Test-$suffix"
        }
        finally { $algorithm.Dispose() }
    }
    return 'Local\CodexSubscriptionRouterInstaller'
}

function Get-CsrRouterProcesses {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string[]]$Roots)

    $prefixes = @($Roots | ForEach-Object { (Resolve-CsrFullPath -Path $_) + [IO.Path]::DirectorySeparatorChar })
    try {
        return @(Get-CimInstance -ClassName Win32_Process -ErrorAction Stop | Where-Object {
            $executablePath = [string]$_.ExecutablePath
            if ([string]::IsNullOrWhiteSpace($executablePath)) { return $false }
            $executablePath = Resolve-CsrFullPath -Path $executablePath
            foreach ($prefix in $prefixes) {
                if ($executablePath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) { return $true }
            }
            return $false
        })
    }
    catch {
        throw "Could not safely inspect router process executable paths: $($_.Exception.Message)"
    }
}

function Assert-CsrRouterStopped {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string[]]$Roots)

    $running = @(Get-CsrRouterProcesses -Roots $Roots)
    if ($running.Count -gt 0) {
        $summary = ($running | ForEach-Object { "$($_.Name) (PID $($_.ProcessId), $($_.ExecutablePath))" }) -join '; '
        throw "Close Codex Subscription Router before continuing. Only router-owned processes were detected: $summary"
    }
}

function Get-CsrBackupEntries {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$BackupRoot,
        [Parameter(Mandatory = $true)][string]$Destination,
        [string]$StateRoot,
        [switch]$RequireIntegrity
    )

    if (-not (Test-Path -LiteralPath $BackupRoot -PathType Container)) {
        return @()
    }
    $leaf = Split-Path -Leaf (Resolve-CsrFullPath -Path $Destination)
    $result = [Collections.Generic.List[object]]::new()
    foreach ($container in @(Get-ChildItem -LiteralPath $BackupRoot -Directory -Force | Sort-Object LastWriteTimeUtc -Descending)) {
        $app = Join-Path $container.FullName $leaf
        if (-not (Test-Path -LiteralPath $app -PathType Container)) { continue }
        try {
            if ($RequireIntegrity) {
                $manifest = Assert-CsrInstallationIntegrity -LayoutPath $app -ExpectedDestination $Destination -ExpectedStateRoot $StateRoot
            }
            else {
                $manifest = Read-CsrManifest -LayoutPath $app -ExpectedDestination $Destination -ExpectedStateRoot $StateRoot
            }
            $result.Add([PSCustomObject]@{
                Container = $container.FullName
                AppPath = $app
                Manifest = $manifest
                LastWriteTimeUtc = $container.LastWriteTimeUtc
            })
        }
        catch {
            Write-Warning "Ignoring unauthenticated backup '$($container.FullName)': $($_.Exception.Message)"
        }
    }
    return @($result)
}

function Write-CsrJsonAtomic {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]$Value,
        [Parameter(Mandatory = $true)][string]$Path
    )

    $temporary = Join-Path (Split-Path -Parent $Path) ('.{0}.{1}.tmp' -f (Split-Path -Leaf $Path), [Guid]::NewGuid().ToString('N'))
    $replacementBackup = Join-Path (Split-Path -Parent $Path) ('.{0}.{1}.replace-backup' -f (Split-Path -Leaf $Path), [Guid]::NewGuid().ToString('N'))
    try {
        $json = ($Value | ConvertTo-Json -Depth 20) + "`n"
        [IO.File]::WriteAllText($temporary, $json, (New-Object Text.UTF8Encoding($false)))
        if (Test-Path -LiteralPath $Path -PathType Leaf) {
            [IO.File]::Replace($temporary, $Path, $replacementBackup, $true)
            try { Remove-Item -LiteralPath $replacementBackup -Force }
            catch { Write-Warning "Manifest was replaced atomically, but its temporary recovery copy remains: $replacementBackup" }
        }
        else {
            [IO.File]::Move($temporary, $Path)
        }
    }
    finally {
        if (Test-Path -LiteralPath $temporary -PathType Leaf) {
            Remove-Item -LiteralPath $temporary -Force
        }
    }
}

function Get-CsrTreeSize {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path)) { return [Int64]0 }
    if (Test-Path -LiteralPath $Path -PathType Leaf) {
        return [Int64](Get-Item -LiteralPath $Path -Force).Length
    }
    [Int64]$total = 0
    Get-ChildItem -LiteralPath $Path -File -Recurse -Force -ErrorAction Stop | ForEach-Object { $total += [Int64]$_.Length }
    return $total
}

function Get-CsrFreeSpace {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)

    $full = Resolve-CsrFullPath -Path $Path
    $root = [IO.Path]::GetPathRoot($full)
    return [Int64]([IO.DriveInfo]::new($root).AvailableFreeSpace)
}

Export-ModuleMember -Function @(
    'Resolve-CsrFullPath', 'Test-CsrPathWithin', 'Assert-CsrNoReparseAncestor', 'Assert-CsrTreeHasNoReparsePoints',
    'Assert-CsrManagedPaths', 'Get-CsrFileHash', 'Get-CsrTreeDigest', 'Read-CsrManifest',
    'Assert-CsrInstallationIntegrity', 'Get-CsrRouterProcesses', 'Assert-CsrRouterStopped',
    'Get-CsrBackupEntries', 'Write-CsrJsonAtomic', 'Get-CsrTreeSize', 'Get-CsrFreeSpace',
    'Set-CsrPrivateDirectoryAcl', 'Assert-CsrPrivateDirectoryAcl', 'Get-CsrLifecycleMutexName'
)
