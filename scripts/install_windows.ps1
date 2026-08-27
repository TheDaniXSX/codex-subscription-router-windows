#Requires -Version 5.1

<#
.SYNOPSIS
Installs an independent Windows copy of Codex Subscription Router.

.DESCRIPTION
The official OpenAI.Codex package is treated as read-only build input. This
script resolves and fingerprints that package, installs locked build
dependencies, and invokes patch_windows_app.py to create an unpackaged copy in
the current user's LocalAppData directory.

The patcher performs the destination replacement atomically and moves an
existing installation to a recoverable backup before committing the new one.
This script never stops, updates, unregisters, or writes into the official
OpenAI.Codex package.

.PARAMETER Source
Path to an official OpenAI.Codex package or an explicitly supplied unpackaged
source tree. When omitted, the newest OpenAI.Codex Appx package registered for
the current user is selected.

.PARAMETER Destination
Installation directory for the independent router application.

.PARAMETER StateRoot
Directory for router state, account homes, logs, and control token.

.PARAMETER MuxPath
Optional prebuilt codex-mux.exe. When omitted, the patcher builds it from this
checkout using the installed Go toolchain.

.PARAMETER LauncherPath
Optional prebuilt Windows desktop launcher. When omitted, the patcher builds it
from this checkout using the installed Go toolchain.

.PARAMETER Force
Permit replacement of an existing router installation. The patcher creates a
timestamped, recoverable backup before the atomic replacement.

.PARAMETER DryRun
Resolve paths, validate prerequisites, fingerprint the source, and invoke the
patcher's read-only validation mode without installing or launching the app.
A transcript is still written for auditability.

.PARAMETER AllowUntestedSource
Permit an official Codex build whose exact version and hashes have not yet been
approved. Structural and patch-anchor checks still have to pass.

.PARAMETER SkipDependencyInstall
Skip npm ci. Use only when the locked node_modules tree is already present.

.PARAMETER NoShortcut
Do not create or update the current user's Start Menu shortcut.

.PARAMETER NoLaunch
Do not launch the independent router after a successful installation.

.PARAMETER BackupRetention
Number of authenticated previous builds to retain after a successful install.
The default keeps one rollback build in addition to the active app.

.PARAMETER MinimumFreeBytes
Optional stricter free-space floor. The installer always calculates and
enforces the staging requirement even when this value is zero.

.PARAMETER ControlPort
Optional high loopback port reserved for this installation. Zero selects and
temporarily reserves a cryptographically random free port in 49152..65535.

.EXAMPLE
pwsh -File .\scripts\install_windows.ps1 -DryRun

.EXAMPLE
pwsh -File .\scripts\install_windows.ps1

.EXAMPLE
pwsh -File .\scripts\install_windows.ps1 -Force -NoLaunch
#>

[CmdletBinding()]
param(
    [Alias('SourceApp')]
    [string]$Source,

    [string]$Destination = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),

    [string]$StateRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router Data'),

    [string]$MuxPath,

    [string]$LauncherPath,

    [switch]$Force,

    [switch]$DryRun,

    [switch]$AllowUntestedSource,

    [switch]$SkipDependencyInstall,

    [switch]$NoShortcut,

    [switch]$NoLaunch,

    [ValidateRange(0, 100)]
    [int]$BackupRetention = 1,

    [ValidateRange(0, [Int64]::MaxValue)]
    [Int64]$MinimumFreeBytes = 0,

    [ValidateRange(0, 65535)]
    [int]$ControlPort = 0
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'Continue'

$script:TranscriptStarted = $false
$script:ExitCode = 0
$script:EffectiveDryRun = [bool]$DryRun
$script:ProjectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$script:PatcherPath = Join-Path $script:ProjectRoot 'scripts\patch_windows_app.py'
$script:InstallStartedAt = [DateTime]::UtcNow
$script:InstallMutex = $null
$script:InstallMutexAcquired = $false
$script:ControlPortReservation = $null
Import-Module (Join-Path $PSScriptRoot 'WindowsLifecycle.psm1') -Force
Import-Module (Join-Path $PSScriptRoot 'ShortcutIdentity.psm1') -Force
$script:RouterAppUserModelId = 'com.openai.codex.subscription-router'

function Write-Step {
    param([Parameter(Mandatory = $true)][string]$Message)
    $stamp = [DateTime]::Now.ToString('HH:mm:ss')
    Write-Host "`n[$stamp] ==> $Message" -ForegroundColor Cyan
}

function Write-Info {
    param([Parameter(Mandatory = $true)][string]$Message)
    Write-Host "  $Message"
}

function Resolve-FullPath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [switch]$MustExist
    )

    $expanded = [Environment]::ExpandEnvironmentVariables($Path)
    $fullPath = [IO.Path]::GetFullPath($expanded)
    if ($MustExist -and -not (Test-Path -LiteralPath $fullPath)) {
        throw "Path does not exist: $fullPath"
    }
    $pathRoot = [IO.Path]::GetPathRoot($fullPath)
    if ($fullPath.Equals($pathRoot, [StringComparison]::OrdinalIgnoreCase)) {
        return $pathRoot
    }
    return $fullPath.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
}

function Test-PathIsWithin {
    param(
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$Parent
    )

    $candidateFull = (Resolve-FullPath -Path $Candidate) + [IO.Path]::DirectorySeparatorChar
    $parentFull = (Resolve-FullPath -Path $Parent) + [IO.Path]::DirectorySeparatorChar
    return $candidateFull.StartsWith($parentFull, [StringComparison]::OrdinalIgnoreCase)
}

function Assert-SeparateTrees {
    param(
        [Parameter(Mandatory = $true)][string]$ReadOnlySource,
        [Parameter(Mandatory = $true)][string]$InstallDestination,
        [Parameter(Mandatory = $true)][string]$RouterStateRoot
    )

    if ((Test-PathIsWithin -Candidate $InstallDestination -Parent $ReadOnlySource) -or
        (Test-PathIsWithin -Candidate $ReadOnlySource -Parent $InstallDestination)) {
        throw 'Source and destination overlap. The official package must remain a separate, read-only input.'
    }
    if ((Test-PathIsWithin -Candidate $RouterStateRoot -Parent $ReadOnlySource) -or
        (Test-PathIsWithin -Candidate $ReadOnlySource -Parent $RouterStateRoot)) {
        throw 'Source and state paths overlap. Router state may not be stored inside the official package.'
    }
    if ((Resolve-FullPath -Path $InstallDestination) -eq (Resolve-FullPath -Path $RouterStateRoot)) {
        throw 'Destination and StateRoot must be different directories.'
    }
    if ((Test-PathIsWithin -Candidate $InstallDestination -Parent $RouterStateRoot) -or
        (Test-PathIsWithin -Candidate $RouterStateRoot -Parent $InstallDestination)) {
        throw 'Destination and StateRoot may not contain one another. App replacement and persistent account state must remain separate.'
    }
}

function Assert-NoReparseAncestor {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$StopAt
    )

    $current = Resolve-FullPath -Path $Path
    $boundary = Resolve-FullPath -Path $StopAt -MustExist
    while ($true) {
        if (Test-Path -LiteralPath $current) {
            $item = Get-Item -LiteralPath $current -Force
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "Writable path traverses a junction or symbolic link, which is not supported: $current"
            }
        }
        if ($current.Equals($boundary, [StringComparison]::OrdinalIgnoreCase)) {
            break
        }
        $parent = Split-Path -Parent $current
        if ([string]::IsNullOrWhiteSpace($parent) -or $parent.Equals($current, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Writable path is not contained by the expected boundary: $Path"
        }
        $current = Resolve-FullPath -Path $parent
    }
}

function Assert-SafeWritablePaths {
    param(
        [Parameter(Mandatory = $true)][string]$InstallDestination,
        [Parameter(Mandatory = $true)][string]$RouterStateRoot,
        [Parameter(Mandatory = $true)][string]$LocalAppDataRoot
    )

    foreach ($entry in @(
        [PSCustomObject]@{ Name = 'Destination'; Path = $InstallDestination },
        [PSCustomObject]@{ Name = 'StateRoot'; Path = $RouterStateRoot }
    )) {
        if (-not (Test-PathIsWithin -Candidate $entry.Path -Parent $LocalAppDataRoot)) {
            throw "$($entry.Name) must be a child of the current user's LOCALAPPDATA directory: $($entry.Path)"
        }
        if ((Resolve-FullPath -Path $entry.Path) -eq (Resolve-FullPath -Path $LocalAppDataRoot)) {
            throw "$($entry.Name) may not be the LOCALAPPDATA root itself."
        }
        if (Test-Path -LiteralPath $entry.Path) {
            $item = Get-Item -LiteralPath $entry.Path -Force
            if (-not $item.PSIsContainer) {
                throw "$($entry.Name) exists but is not a directory: $($entry.Path)"
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "$($entry.Name) may not be a junction or symbolic link: $($entry.Path)"
            }
        }
    }

    $programsRoot = Join-Path $LocalAppDataRoot 'Programs'
    if ((Resolve-FullPath -Path $InstallDestination) -eq (Resolve-FullPath -Path $programsRoot)) {
        throw 'Destination may not be the LOCALAPPDATA Programs root itself.'
    }
    Assert-NoReparseAncestor -Path $InstallDestination -StopAt $LocalAppDataRoot
    Assert-NoReparseAncestor -Path $RouterStateRoot -StopAt $LocalAppDataRoot
}

function Initialize-SecureStateRoot {
    param([Parameter(Mandatory = $true)][string]$RouterStateRoot)

    New-Item -ItemType Directory -Path $RouterStateRoot -Force | Out-Null
    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $systemSid = New-Object Security.Principal.SecurityIdentifier(
        [Security.Principal.WellKnownSidType]::LocalSystemSid,
        $null
    )
    $inheritance = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor
        [Security.AccessControl.InheritanceFlags]::ObjectInherit
    $propagation = [Security.AccessControl.PropagationFlags]::None
    $allow = [Security.AccessControl.AccessControlType]::Allow
    $fullControl = [Security.AccessControl.FileSystemRights]::FullControl

    # Reapplying an identical protected descriptor through Set-Acl can ask for
    # SeSecurityPrivilege on Windows PowerShell 5.1. Treat the exact secure
    # descriptor as idempotent and avoid rewriting it on updates/retries.
    $existingAcl = Get-Acl -LiteralPath $RouterStateRoot
    try {
        $existingOwnerSid = (New-Object Security.Principal.NTAccount($existingAcl.Owner)).Translate(
            [Security.Principal.SecurityIdentifier]
        ).Value
    }
    catch {
        $existingOwnerSid = ''
    }
    $expectedSids = @($currentSid.Value, $systemSid.Value)
    $existingSids = @()
    $existingRulesAreExact = $existingAcl.AreAccessRulesProtected
    foreach ($rule in $existingAcl.Access) {
        try {
            $ruleSid = $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
        }
        catch {
            $existingRulesAreExact = $false
            continue
        }
        if ($rule.AccessControlType -ne $allow -or
            $expectedSids -notcontains $ruleSid -or
            ($rule.FileSystemRights -band $fullControl) -ne $fullControl) {
            $existingRulesAreExact = $false
        }
        $existingSids += $ruleSid
    }
    if ($existingRulesAreExact -and
        $existingOwnerSid -eq $currentSid.Value -and
        $existingSids.Count -eq 2 -and
        $existingSids -contains $currentSid.Value -and
        $existingSids -contains $systemSid.Value) {
        Write-Info "StateRoot ACL is already hardened for the current user and SYSTEM: $RouterStateRoot"
        return
    }

    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetOwner($currentSid)
    $acl.SetAccessRuleProtection($true, $false)
    $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule(
        $currentSid, $fullControl, $inheritance, $propagation, $allow
    )))
    $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule(
        $systemSid, $fullControl, $inheritance, $propagation, $allow
    )))
    Set-Acl -LiteralPath $RouterStateRoot -AclObject $acl

    $verified = Get-Acl -LiteralPath $RouterStateRoot
    if (-not $verified.AreAccessRulesProtected) {
        throw "StateRoot ACL still inherits permissions: $RouterStateRoot"
    }
    try {
        $ownerSid = (New-Object Security.Principal.NTAccount($verified.Owner)).Translate(
            [Security.Principal.SecurityIdentifier]
        ).Value
    }
    catch {
        throw "StateRoot owner cannot be resolved securely: $($verified.Owner)"
    }
    if ($ownerSid -ne $currentSid.Value) {
        throw "StateRoot owner is not the current user: $ownerSid"
    }
    $allowedSids = @($currentSid.Value, $systemSid.Value)
    $actualAllowSids = @()
    foreach ($rule in $verified.Access) {
        if ($rule.AccessControlType -ne $allow) { continue }
        try {
            $ruleSid = $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
        }
        catch {
            throw "StateRoot ACL contains an unresolvable identity: $($rule.IdentityReference)"
        }
        if ($allowedSids -notcontains $ruleSid) {
            throw "StateRoot ACL grants access to an unexpected identity: $ruleSid"
        }
        if (($rule.FileSystemRights -band $fullControl) -ne $fullControl) {
            throw "StateRoot ACL does not grant FullControl to required SID $ruleSid"
        }
        $actualAllowSids += $ruleSid
    }
    foreach ($requiredSid in $allowedSids) {
        if ($actualAllowSids -notcontains $requiredSid) {
            throw "StateRoot ACL is missing required full control for SID $requiredSid"
        }
    }
    Write-Info "Hardened StateRoot ACL for the current user and SYSTEM: $RouterStateRoot"
}

function Resolve-Executable {
    param(
        [Parameter(Mandatory = $true)][string[]]$Names,
        [Parameter(Mandatory = $true)][string]$InstallHint
    )

    foreach ($name in $Names) {
        $command = Get-Command $name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($null -ne $command) {
            return $command.Source
        }
    }
    throw "Missing prerequisite: $($Names[0]). $InstallHint"
}

function Get-NumericVersion {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][string]$ToolName
    )

    $match = [regex]::Match($Text, '(?<version>\d+\.\d+(?:\.\d+)?)')
    if (-not $match.Success) {
        throw "Could not parse $ToolName version from: $Text"
    }
    return [Version]$match.Groups['version'].Value
}

function Invoke-NativeCommand {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$ArgumentList = @(),
        [string]$FailureMessage = 'External command failed.'
    )

    Write-Info ("Running: {0} {1}" -f $FilePath, ($ArgumentList -join ' '))
    & $FilePath @ArgumentList
    $nativeExitCode = $LASTEXITCODE
    if ($nativeExitCode -ne 0) {
        throw "$FailureMessage Exit code: $nativeExitCode"
    }
}

function New-ControlPortReservation {
    param([Parameter(Mandatory = $true)][int]$RequestedPort)

    if ($RequestedPort -ne 0 -and ($RequestedPort -lt 49152 -or $RequestedPort -gt 65535)) {
        throw 'ControlPort must be zero or a high dynamic port in 49152..65535.'
    }
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        for ($attempt = 0; $attempt -lt 128; $attempt++) {
            $port = $RequestedPort
            if ($port -eq 0) {
                $bytes = New-Object byte[] 4
                $generator.GetBytes($bytes)
                $value = [BitConverter]::ToUInt32($bytes, 0)
                $port = 49152 + [int]($value % (65535 - 49152 + 1))
            }
            $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, $port)
            $listener.Server.ExclusiveAddressUse = $true
            try {
                $listener.Start()
                return [PSCustomObject]@{ Port = $port; Listener = $listener }
            }
            catch [Net.Sockets.SocketException] {
                $listener.Stop()
                if ($RequestedPort -ne 0) { throw "Requested ControlPort $RequestedPort is not available on 127.0.0.1." }
            }
        }
    }
    finally { $generator.Dispose() }
    throw 'Could not reserve a free random control port in 49152..65535 after 128 attempts.'
}

function Invoke-LauncherSelfTest {
    param(
        [Parameter(Mandatory = $true)][string]$LauncherPath,
        [Parameter(Mandatory = $true)][string]$ExpectedStateRoot,
        [Parameter(Mandatory = $true)][int]$ExpectedControlPort
    )

    $variableNames = @('CODEX_ROUTER_DATA_DIR', 'CODEX_MUX_HOME', 'CODEX_MUX_STATE_ROOT')
    $priorValues = @{}
    foreach ($name in $variableNames) {
        $priorValues[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
        [Environment]::SetEnvironmentVariable($name, $null, 'Process')
    }
    try {
        $outputLines = @(& $LauncherPath '--router-self-test' 2>&1 | ForEach-Object { [string]$_ })
        $selfTestExitCode = $LASTEXITCODE
    }
    finally {
        foreach ($name in $variableNames) {
            [Environment]::SetEnvironmentVariable($name, $priorValues[$name], 'Process')
        }
    }
    foreach ($line in $outputLines) {
        Write-Info "launcher self-test: $line"
    }
    if ($selfTestExitCode -ne 0) {
        throw "Launcher self-test failed with exit code $selfTestExitCode."
    }
    $values = @{}
    foreach ($line in $outputLines) {
        $separator = $line.IndexOf('=')
        if ($separator -gt 0) {
            $values[$line.Substring(0, $separator)] = $line.Substring($separator + 1)
        }
    }
    $expectedRootSource = 'sidecar:resources\codex-router\launcher-config.json'
    if ($values['status'] -ne 'ok' -or
        -not ([string]$values['root_source']).Equals($expectedRootSource, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Launcher self-test did not resolve its persisted sidecar configuration.'
    }
    if (-not (Resolve-FullPath -Path $values['state_root']).Equals(
        (Resolve-FullPath -Path $ExpectedStateRoot),
        [StringComparison]::OrdinalIgnoreCase
    )) {
        throw "Launcher self-test selected the wrong StateRoot: $($values['state_root'])"
    }
    if ([int]$values['launcher_config_schema'] -ne 2 -or [int]$values['control_port'] -ne $ExpectedControlPort) {
        throw "Launcher self-test selected an inconsistent schema/control port: schema=$($values['launcher_config_schema']), port=$($values['control_port'])"
    }
}

function Resolve-Python {
    $python = Get-Command 'python.exe' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $python) {
        $probe = & $python.Source -c 'import sys; print(sys.version_info.major); print(sys.executable)'
        if ($LASTEXITCODE -eq 0 -and $probe[0] -eq '3') {
            return [PSCustomObject]@{ FilePath = $python.Source; Prefix = @() }
        }
    }

    $launcher = Get-Command 'py.exe' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $launcher) {
        $probe = & $launcher.Source -3 -c 'import sys; print(sys.version_info.major); print(sys.executable)'
        if ($LASTEXITCODE -eq 0 -and $probe[0] -eq '3') {
            return [PSCustomObject]@{ FilePath = $launcher.Source; Prefix = @('-3') }
        }
    }

    throw 'Missing prerequisite: Python 3. Install Python 3 and ensure python.exe or py.exe is on PATH.'
}

function Resolve-OfficialSource {
    param([string]$ExplicitSource)

    if (-not [string]::IsNullOrWhiteSpace($ExplicitSource)) {
        return Resolve-FullPath -Path $ExplicitSource -MustExist
    }

    if ($null -eq (Get-Command 'Get-AppxPackage' -ErrorAction SilentlyContinue)) {
        throw 'Get-AppxPackage is unavailable. Pass -Source with the official OpenAI.Codex package path.'
    }

    $packages = @(Get-AppxPackage -Name 'OpenAI.Codex' -ErrorAction SilentlyContinue |
        Where-Object { -not [string]::IsNullOrWhiteSpace($_.InstallLocation) } |
        Sort-Object -Property Version -Descending)
    if ($packages.Count -eq 0) {
        throw 'The official OpenAI.Codex package is not registered for this user. Install/update Codex first or pass -Source.'
    }

    $selected = $packages[0]
    Write-Info "Detected package: $($selected.PackageFullName)"
    return Resolve-FullPath -Path $selected.InstallLocation -MustExist
}

function Find-SourceArtifact {
    param(
        [Parameter(Mandatory = $true)][string]$SourceRoot,
        [Parameter(Mandatory = $true)][string[]]$RelativeCandidates,
        [Parameter(Mandatory = $true)][string]$Description
    )

    foreach ($relativePath in $RelativeCandidates) {
        $candidate = Join-Path $SourceRoot $relativePath
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return Resolve-FullPath -Path $candidate -MustExist
        }
    }
    throw "The selected source does not contain $Description in a recognized package layout: $SourceRoot"
}

function Get-SourceFingerprint {
    param([Parameter(Mandatory = $true)][string]$SourceRoot)

    $asarPath = Find-SourceArtifact -SourceRoot $SourceRoot -Description 'resources\app.asar' -RelativeCandidates @(
        'app\resources\app.asar',
        'resources\app.asar'
    )
    $codexPath = Find-SourceArtifact -SourceRoot $SourceRoot -Description 'resources\codex.exe' -RelativeCandidates @(
        'app\resources\codex.exe',
        'resources\codex.exe'
    )
    $desktopPath = Find-SourceArtifact -SourceRoot $SourceRoot -Description 'ChatGPT.exe' -RelativeCandidates @(
        'app\ChatGPT.exe',
        'ChatGPT.exe'
    )
    $desktopShimPath = Find-SourceArtifact -SourceRoot $SourceRoot -Description 'Codex.exe' -RelativeCandidates @(
        'app\Codex.exe',
        'Codex.exe'
    )

    return [PSCustomObject]@{
        AsarPath = $asarPath
        AsarSha256 = (Get-FileHash -LiteralPath $asarPath -Algorithm SHA256).Hash.ToLowerInvariant()
        CodexPath = $codexPath
        CodexSha256 = (Get-FileHash -LiteralPath $codexPath -Algorithm SHA256).Hash.ToLowerInvariant()
        DesktopPath = $desktopPath
        DesktopSha256 = (Get-FileHash -LiteralPath $desktopPath -Algorithm SHA256).Hash.ToLowerInvariant()
        DesktopShimPath = $desktopShimPath
        DesktopShimSha256 = (Get-FileHash -LiteralPath $desktopShimPath -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

function Assert-SourceUnchanged {
    param(
        [Parameter(Mandatory = $true)]$Before,
        [Parameter(Mandatory = $true)]$After
    )

    if ($Before.AsarSha256 -ne $After.AsarSha256 -or
        $Before.CodexSha256 -ne $After.CodexSha256 -or
        $Before.DesktopSha256 -ne $After.DesktopSha256 -or
        $Before.DesktopShimSha256 -ne $After.DesktopShimSha256) {
        throw 'Safety invariant violated: an official source artifact changed while the patcher was running.'
    }
    Write-Info 'Verified after patching: official app.asar and all three executable hashes are unchanged.'
}

function Get-RouterProcesses {
    param([Parameter(Mandatory = $true)][string]$InstallDestination)

    $destinationPrefix = (Resolve-FullPath -Path $InstallDestination) + [IO.Path]::DirectorySeparatorChar
    try {
        return @(Get-CimInstance -ClassName Win32_Process -ErrorAction Stop | Where-Object {
            $executablePath = $_.ExecutablePath
            -not [string]::IsNullOrWhiteSpace($executablePath) -and
            $executablePath.StartsWith($destinationPrefix, [StringComparison]::OrdinalIgnoreCase)
        })
    }
    catch {
        throw "Could not safely inspect running router process paths: $($_.Exception.Message)"
    }
}

function Get-BackupDirectories {
    param([Parameter(Mandatory = $true)][string]$BackupRoot)

    if (-not (Test-Path -LiteralPath $BackupRoot -PathType Container)) {
        return @()
    }
    return @(Get-ChildItem -LiteralPath $BackupRoot -Directory -ErrorAction Stop |
        Sort-Object -Property LastWriteTimeUtc)
}

function Find-NewBackupDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$BackupRoot,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$ExistingPaths,
        [Parameter(Mandatory = $true)][string]$DestinationLeaf,
        $PreviousMarker
    )

    $candidates = @(Get-BackupDirectories -BackupRoot $BackupRoot |
        Where-Object { $ExistingPaths -notcontains $_.FullName } |
        Sort-Object -Property LastWriteTimeUtc -Descending)
    if ($null -eq $PreviousMarker) {
        return $null
    }
    foreach ($candidate in $candidates) {
        $markerPath = Join-Path (Join-Path $candidate.FullName $DestinationLeaf) $PreviousMarker.RelativePath
        if ((Test-Path -LiteralPath $markerPath -PathType Leaf) -and
            (Get-FileHash -LiteralPath $markerPath -Algorithm SHA256).Hash.ToLowerInvariant() -eq $PreviousMarker.Sha256) {
            return $candidate
        }
    }
    return $null
}

function Get-InstallationMarker {
    param([Parameter(Mandatory = $true)][string]$InstallDestination)

    foreach ($relativePath in @('codex-mux-build.json', 'ChatGPT.exe', 'Codex.exe', 'resources\app.asar')) {
        $path = Join-Path $InstallDestination $relativePath
        if (Test-Path -LiteralPath $path -PathType Leaf) {
            return [PSCustomObject]@{
                RelativePath = $relativePath
                Sha256 = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
            }
        }
    }
    throw "Existing destination has no stable file with which to authenticate its backup: $InstallDestination"
}

function Test-DestinationPublishedThisRun {
    param(
        [Parameter(Mandatory = $true)][string]$InstallDestination,
        [Parameter(Mandatory = $true)][DateTime]$StartedAtUtc,
        [Parameter(Mandatory = $true)][string]$ExpectedSourceAsarSha256
    )

    $manifestPath = Join-Path $InstallDestination 'codex-mux-build.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        return $false
    }
    try {
        $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
        $created = [DateTimeOffset]::Parse(
            [string]$manifest.createdAtUtc,
            [Globalization.CultureInfo]::InvariantCulture,
            [Globalization.DateTimeStyles]::RoundtripKind
        ).UtcDateTime
        $recordedDestination = Resolve-FullPath -Path ([string]$manifest.destination)
        return $created -ge $StartedAtUtc.AddMinutes(-1) -and
            $recordedDestination.Equals((Resolve-FullPath -Path $InstallDestination), [StringComparison]::OrdinalIgnoreCase) -and
            ([string]$manifest.sourceAsarSha256).Equals($ExpectedSourceAsarSha256, [StringComparison]::OrdinalIgnoreCase)
    }
    catch {
        return $false
    }
}

function Restore-PreviousInstallation {
    param(
        [Parameter(Mandatory = $true)][string]$InstallDestination,
        [Parameter(Mandatory = $true)][string]$RouterStateRoot,
        [Parameter(Mandatory = $true)][bool]$HadPreviousInstallation,
        [System.IO.DirectoryInfo]$BackupDirectory,
        [Parameter(Mandatory = $true)][string]$Reason
    )

    Write-Host "`nPost-commit verification failed; rolling back without deleting the failed build." -ForegroundColor Yellow
    Write-Info "Reason: $Reason"

    $backupApp = $null
    if ($null -ne $BackupDirectory) {
        $candidate = Join-Path $BackupDirectory.FullName (Split-Path -Leaf $InstallDestination)
        if (Test-Path -LiteralPath $candidate -PathType Container) {
            $backupApp = $candidate
        }
    }
    if ($HadPreviousInstallation -and [string]::IsNullOrWhiteSpace($backupApp)) {
        throw 'Automatic rollback cannot locate the patcher backup. The new build remains closed; inspect the transcript and backup root before changing files.'
    }

    $running = @(Get-RouterProcesses -InstallDestination $InstallDestination)
    if ($running.Count -gt 0) {
        throw 'Automatic rollback is unsafe because a process from the new destination is running. The installer did not terminate it.'
    }

    $failedRoot = Join-Path $RouterStateRoot 'failed-installations'
    New-Item -ItemType Directory -Path $failedRoot -Force | Out-Null
    $failedDestination = Join-Path $failedRoot ([DateTime]::UtcNow.ToString('yyyyMMdd-HHmmssfff'))
    if (Test-Path -LiteralPath $InstallDestination) {
        Move-Item -LiteralPath $InstallDestination -Destination $failedDestination
        Write-Info "Failed build preserved at: $failedDestination"
    }
    if (-not [string]::IsNullOrWhiteSpace($backupApp)) {
        Move-Item -LiteralPath $backupApp -Destination $InstallDestination
        Write-Info "Previous build restored to: $InstallDestination"
    }
    else {
        Write-Info 'Fresh installation removed from service; there was no previous build to restore.'
    }
}

function Install-StartMenuShortcut {
    param(
        [Parameter(Mandatory = $true)][string]$LauncherPath,
        [Parameter(Mandatory = $true)][string]$InstallDestination,
        [Parameter(Mandatory = $true)][string]$RouterStateRoot
    )

    if ([string]::IsNullOrWhiteSpace($env:APPDATA)) {
        throw 'APPDATA is unavailable; the per-user Start Menu path cannot be resolved.'
    }
    $shortcutDirectory = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
    $shortcutPath = Join-Path $shortcutDirectory 'Codex Subscription Router.lnk'
    $temporaryShortcut = Join-Path $shortcutDirectory ('.codex-subscription-router-{0}.lnk' -f [Guid]::NewGuid().ToString('N'))
    $localBackup = Join-Path $shortcutDirectory ('.codex-subscription-router-backup-{0}.lnk' -f [Guid]::NewGuid().ToString('N'))
    $shortcutBackup = $null
    New-Item -ItemType Directory -Path $shortcutDirectory -Force | Out-Null

    try {
        $shell = New-Object -ComObject WScript.Shell
        $shortcut = $shell.CreateShortcut($temporaryShortcut)
        $shortcut.TargetPath = $LauncherPath
        $shortcut.WorkingDirectory = $InstallDestination
        $shortcut.Description = 'Codex Subscription Router (unofficial multi-subscription client)'
        $shortcut.IconLocation = "$LauncherPath,0"
        $shortcut.Save()
        [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut) | Out-Null
        [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell) | Out-Null

        Set-CsrShortcutAppUserModelId -Path $temporaryShortcut -AppUserModelId $script:RouterAppUserModelId

        if (-not (Test-Path -LiteralPath $temporaryShortcut -PathType Leaf)) {
            throw 'WScript.Shell did not create the temporary shortcut.'
        }
        $validationShell = New-Object -ComObject WScript.Shell
        try {
            $validationShortcut = $validationShell.CreateShortcut($temporaryShortcut)
            try {
                if (-not [IO.Path]::GetFullPath($validationShortcut.TargetPath).Equals(
                    [IO.Path]::GetFullPath($LauncherPath),
                    [StringComparison]::OrdinalIgnoreCase
                )) {
                    throw 'The temporary shortcut target does not match the verified router launcher.'
                }
                $shortcutAppUserModelId = Get-CsrShortcutAppUserModelId -Path $temporaryShortcut
                if (-not $shortcutAppUserModelId.Equals($script:RouterAppUserModelId, [StringComparison]::Ordinal)) {
                    throw 'The temporary shortcut AppUserModelID does not match the router window identity.'
                }
            }
            finally {
                [Runtime.InteropServices.Marshal]::FinalReleaseComObject($validationShortcut) | Out-Null
            }
        }
        finally {
            [Runtime.InteropServices.Marshal]::FinalReleaseComObject($validationShell) | Out-Null
        }

        if (Test-Path -LiteralPath $shortcutPath -PathType Leaf) {
            # File.Replace publishes the new shortcut and creates its rollback
            # copy as one same-directory filesystem operation.
            [IO.File]::Replace($temporaryShortcut, $shortcutPath, $localBackup, $true)
            try {
                $backupDirectory = Join-Path $RouterStateRoot ("shortcut-backups\{0}" -f [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmssfff'))
                New-Item -ItemType Directory -Path $backupDirectory -Force | Out-Null
                $archivalBackup = Join-Path $backupDirectory 'Codex Subscription Router.lnk'
                Copy-Item -LiteralPath $localBackup -Destination $archivalBackup
                Remove-Item -LiteralPath $localBackup -Force
                $shortcutBackup = $archivalBackup
            }
            catch {
                # The new shortcut is already valid and published. Retain the
                # same-directory backup rather than undoing a successful app
                # installation because archival copying failed.
                $shortcutBackup = $localBackup
                Write-Warning "Shortcut installed, but its prior version remains beside it because archival backup failed: $($_.Exception.Message)"
            }
        }
        else {
            [IO.File]::Move($temporaryShortcut, $shortcutPath)
        }
        Write-Info "Start Menu shortcut: $shortcutPath"
        return [PSCustomObject]@{
            Path = $shortcutPath
            Target = $LauncherPath
            BackupPath = $shortcutBackup
        }
    }
    catch {
        if (Test-Path -LiteralPath $temporaryShortcut -PathType Leaf) {
            Remove-Item -LiteralPath $temporaryShortcut -Force
        }
        throw
    }
}

function Quote-PowerShellLiteral {
    param([Parameter(Mandatory = $true)][string]$Value)
    return "'" + $Value.Replace("'", "''") + "'"
}

function Show-RollbackInstructions {
    param(
        [Parameter(Mandatory = $true)][string]$InstallDestination,
        [Parameter(Mandatory = $true)][string]$RouterStateRoot,
        [System.IO.DirectoryInfo]$BackupDirectory,
        $ShortcutRecord
    )

    Write-Host "`nRollback / uninstall instructions" -ForegroundColor Yellow
    Write-Info 'First close only Codex Subscription Router. Do not close or unregister the official Codex app.'

    $quotedDestination = Quote-PowerShellLiteral -Value $InstallDestination
    if ($null -ne $BackupDirectory) {
        $backupApp = Join-Path $BackupDirectory.FullName (Split-Path -Leaf $InstallDestination)
        $failedRoot = Join-Path $RouterStateRoot 'failed-installations'
        $failedDestination = Join-Path $failedRoot ([DateTime]::UtcNow.ToString('yyyyMMdd-HHmmssfff'))
        Write-Info "Recoverable previous build: $backupApp"
        Write-Host "  New-Item -ItemType Directory -Force -Path $(Quote-PowerShellLiteral $failedRoot)" -ForegroundColor DarkGray
        Write-Host "  Move-Item -LiteralPath $quotedDestination -Destination $(Quote-PowerShellLiteral $failedDestination)" -ForegroundColor DarkGray
        Write-Host "  Move-Item -LiteralPath $(Quote-PowerShellLiteral $backupApp) -Destination $quotedDestination" -ForegroundColor DarkGray
    }
    else {
        $removedRoot = Join-Path $RouterStateRoot 'removed-apps'
        $removedDestination = Join-Path $removedRoot ([DateTime]::UtcNow.ToString('yyyyMMdd-HHmmssfff'))
        Write-Info 'This was a fresh installation; there is no previous app build to restore.'
        Write-Host "  New-Item -ItemType Directory -Force -Path $(Quote-PowerShellLiteral $removedRoot)" -ForegroundColor DarkGray
        Write-Host "  Move-Item -LiteralPath $quotedDestination -Destination $(Quote-PowerShellLiteral $removedDestination)" -ForegroundColor DarkGray
    }
    Write-Info "Router account state is separate at $RouterStateRoot and is not deleted by these commands."

    if ($null -ne $ShortcutRecord) {
        $quotedShortcut = Quote-PowerShellLiteral -Value $ShortcutRecord.Path
        $quotedTarget = Quote-PowerShellLiteral -Value $ShortcutRecord.Target
        Write-Info 'Remove the Start Menu shortcut only if it still points to this router launcher:'
        if (-not [string]::IsNullOrWhiteSpace($ShortcutRecord.BackupPath)) {
            $quotedShortcutBackup = Quote-PowerShellLiteral -Value $ShortcutRecord.BackupPath
            Write-Host "  `$s=(New-Object -ComObject WScript.Shell).CreateShortcut($quotedShortcut); if ([IO.Path]::GetFullPath(`$s.TargetPath) -eq [IO.Path]::GetFullPath($quotedTarget)) { Remove-Item -LiteralPath $quotedShortcut; Move-Item -LiteralPath $quotedShortcutBackup -Destination $quotedShortcut }" -ForegroundColor DarkGray
            Write-Info "Previous shortcut backup: $($ShortcutRecord.BackupPath)"
        }
        else {
            Write-Host "  `$s=(New-Object -ComObject WScript.Shell).CreateShortcut($quotedShortcut); if ([IO.Path]::GetFullPath(`$s.TargetPath) -eq [IO.Path]::GetFullPath($quotedTarget)) { Remove-Item -LiteralPath $quotedShortcut }" -ForegroundColor DarkGray
        }
    }
}

function Invoke-AuthenticatedBackupRetention {
    param(
        [Parameter(Mandatory = $true)][string]$InstallDestination,
        [Parameter(Mandatory = $true)][string]$RouterStateRoot,
        [Parameter(Mandatory = $true)][string]$BackupRoot,
        [Parameter(Mandatory = $true)][int]$Keep
    )

    $entries = @(Get-CsrBackupEntries -BackupRoot $BackupRoot -Destination $InstallDestination -StateRoot $RouterStateRoot -RequireIntegrity)
    foreach ($entry in @($entries | Select-Object -Skip $Keep)) {
        $children = @(Get-ChildItem -LiteralPath $entry.Container -Force)
        if ($children.Count -ne 1 -or -not $children[0].FullName.Equals($entry.AppPath, [StringComparison]::OrdinalIgnoreCase)) {
            Write-Warning "Retention skipped a backup container with unexpected contents: $($entry.Container)"
            continue
        }
        Remove-Item -LiteralPath $entry.Container -Recurse -Force
        Write-Info "Removed obsolete authenticated backup: $($entry.Container)"
    }

    $remaining = @($entries | Select-Object -First $Keep)
    $retainedPath = if ($remaining.Count -gt 0) { [string]$remaining[0].AppPath } else { $null }
    $manifestPath = Join-Path $InstallDestination 'codex-mux-build.json'
    $manifest = Read-CsrManifest -LayoutPath $InstallDestination -ExpectedDestination $InstallDestination -ExpectedStateRoot $RouterStateRoot
    if ($null -eq $manifest.PSObject.Properties['backupPath']) {
        $manifest | Add-Member -NotePropertyName backupPath -NotePropertyValue $retainedPath
    }
    else { $manifest.backupPath = $retainedPath }
    Write-CsrJsonAtomic -Value $manifest -Path $manifestPath
    Write-Info "Backup retention: active build plus $($remaining.Count) authenticated rollback build(s)."
    return $(if ($remaining.Count -gt 0) { Get-Item -LiteralPath $remaining[0].Container } else { $null })
}

function Test-InstalledLayout {
    param(
        [Parameter(Mandatory = $true)][string]$InstallDestination,
        [Parameter(Mandatory = $true)][string]$RouterStateRoot
    )

    $requiredFiles = @(
        (Join-Path $InstallDestination 'resources\app.asar'),
        (Join-Path $InstallDestination 'resources\codex.exe'),
        (Join-Path $InstallDestination 'resources\codex.real.exe'),
        (Join-Path $InstallDestination 'resources\codex-router\launcher-config.json'),
        (Join-Path $InstallDestination 'codex-mux-build.json')
    )
    foreach ($requiredFile in $requiredFiles) {
        if (-not (Test-Path -LiteralPath $requiredFile -PathType Leaf)) {
            throw "Post-install verification failed; missing file: $requiredFile"
        }
    }

    $launcherCandidates = @(
        (Join-Path $InstallDestination 'ChatGPT.exe'),
        (Join-Path $InstallDestination 'Codex.exe')
    )
    $launcher = $launcherCandidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
    if ([string]::IsNullOrWhiteSpace($launcher)) {
        throw "Post-install verification failed; neither ChatGPT.exe nor Codex.exe exists in $InstallDestination"
    }

    try {
        $metadata = Get-Content -LiteralPath (Join-Path $InstallDestination 'codex-mux-build.json') -Raw | ConvertFrom-Json
    }
    catch {
        throw "Post-install verification failed; codex-mux-build.json is invalid: $($_.Exception.Message)"
    }
    if ($null -eq $metadata.schemaVersion -or [int]$metadata.schemaVersion -ne 2 -or
        $null -eq $metadata.sourceAsarSha256 -or $null -eq $metadata.muxSha256 -or
        $null -eq $metadata.controlPort -or [int]$metadata.controlPort -lt 49152 -or [int]$metadata.controlPort -gt 65535) {
        throw 'Post-install verification failed; codex-mux-build.json lacks required provenance fields.'
    }

    $launcherConfigPath = Join-Path $InstallDestination 'resources\codex-router\launcher-config.json'
    try {
        $launcherConfig = Get-Content -LiteralPath $launcherConfigPath -Raw | ConvertFrom-Json
    }
    catch {
        throw "Post-install verification failed; launcher-config.json is invalid: $($_.Exception.Message)"
    }
    $configProperties = @($launcherConfig.PSObject.Properties | ForEach-Object { $_.Name })
    if ($configProperties.Count -ne 3 -or
        $configProperties -notcontains 'schemaVersion' -or
        $configProperties -notcontains 'stateRoot' -or
        $configProperties -notcontains 'controlPort' -or
        [int]$launcherConfig.schemaVersion -ne 2 -or
        [int]$launcherConfig.controlPort -lt 49152 -or [int]$launcherConfig.controlPort -gt 65535 -or
        [string]::IsNullOrWhiteSpace($launcherConfig.stateRoot)) {
        throw 'Post-install verification failed; launcher-config.json must contain exactly schemaVersion=2, stateRoot, and a high controlPort.'
    }
    $persistedStateRoot = Resolve-FullPath -Path ([string]$launcherConfig.stateRoot)
    if (-not $persistedStateRoot.Equals((Resolve-FullPath -Path $RouterStateRoot), [StringComparison]::OrdinalIgnoreCase)) {
        throw "Post-install verification failed; launcher stateRoot '$persistedStateRoot' does not match '$RouterStateRoot'."
    }
    if ([int]$launcherConfig.controlPort -ne [int]$metadata.controlPort) {
        throw 'Post-install verification failed; manifest and launcher sidecar controlPort values differ.'
    }

    return Resolve-FullPath -Path $launcher -MustExist
}

try {
    if ($env:OS -ne 'Windows_NT') {
        throw 'This installer supports Windows only.'
    }
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        throw 'LOCALAPPDATA is unavailable; a per-user installation path cannot be determined.'
    }

    $Destination = Resolve-FullPath -Path $Destination
    $StateRoot = Resolve-FullPath -Path $StateRoot
    if (-not [string]::IsNullOrWhiteSpace($MuxPath)) {
        $MuxPath = Resolve-FullPath -Path $MuxPath -MustExist
        if (-not (Test-Path -LiteralPath $MuxPath -PathType Leaf) -or
            [IO.Path]::GetExtension($MuxPath) -ne '.exe') {
            throw "MuxPath must name a prebuilt .exe file: $MuxPath"
        }
    }
    if (-not [string]::IsNullOrWhiteSpace($LauncherPath)) {
        $LauncherPath = Resolve-FullPath -Path $LauncherPath -MustExist
        if (-not (Test-Path -LiteralPath $LauncherPath -PathType Leaf) -or
            [IO.Path]::GetExtension($LauncherPath) -ne '.exe') {
            throw "LauncherPath must name a prebuilt .exe file: $LauncherPath"
        }
    }

    # Resolve and validate all read/write boundaries before creating even the
    # audit log. This prevents an accidentally supplied StateRoot from writing
    # into the official package.
    Write-Step 'Preflighting read-only source and independent paths'
    $Source = Resolve-OfficialSource -ExplicitSource $Source
    $localAppDataRoot = Resolve-FullPath -Path $env:LOCALAPPDATA -MustExist
    Assert-SeparateTrees -ReadOnlySource $Source -InstallDestination $Destination -RouterStateRoot $StateRoot
    Assert-SafeWritablePaths -InstallDestination $Destination -RouterStateRoot $StateRoot -LocalAppDataRoot $localAppDataRoot
    if (-not [string]::IsNullOrWhiteSpace($MuxPath) -and
        ((Test-PathIsWithin -Candidate $MuxPath -Parent $Source) -or
         (Test-PathIsWithin -Candidate $MuxPath -Parent $Destination))) {
        throw 'MuxPath must be outside both the read-only official source and the replaceable destination.'
    }
    if (-not [string]::IsNullOrWhiteSpace($LauncherPath) -and
        ((Test-PathIsWithin -Candidate $LauncherPath -Parent $Source) -or
         (Test-PathIsWithin -Candidate $LauncherPath -Parent $Destination))) {
        throw 'LauncherPath must be outside both the read-only official source and the replaceable destination.'
    }

    # Destination, StateRoot, and the per-user Start Menu shortcut are shared
    # integration surfaces. Serialize every router install for this logon
    # session, even when callers select different destinations.
    $script:InstallMutex = New-Object Threading.Mutex($false, (Get-CsrLifecycleMutexName -AllowedRoot $localAppDataRoot))
    try {
        $script:InstallMutexAcquired = $script:InstallMutex.WaitOne(0)
    }
    catch [Threading.AbandonedMutexException] {
        $script:InstallMutexAcquired = $true
        Write-Warning 'Recovered an abandoned installer mutex from a previously interrupted run; safety checks will continue.'
    }
    if (-not $script:InstallMutexAcquired) {
        throw 'Another Codex Subscription Router installation is already running for this user.'
    }

    if (-not $script:EffectiveDryRun) {
        Initialize-SecureStateRoot -RouterStateRoot $StateRoot
    }
    $logRoot = if ($script:EffectiveDryRun) {
        Join-Path $env:TEMP 'Codex Subscription Router\logs'
    }
    else {
        Join-Path $StateRoot 'logs'
    }
    New-Item -ItemType Directory -Path $logRoot -Force | Out-Null
    $logPath = Join-Path $logRoot ("install-{0}.log" -f $script:InstallStartedAt.ToString('yyyyMMdd-HHmmssfff'))
    Start-Transcript -LiteralPath $logPath -Force | Out-Null
    $script:TranscriptStarted = $true

    Write-Host 'Codex Subscription Router - Windows installer' -ForegroundColor Green
    Write-Info "Mode: $(if ($script:EffectiveDryRun) { 'DRY RUN (no app/state installation)' } else { 'INSTALL' })"
    Write-Info "Transcript: $logPath"
    Write-Info 'Safety boundary: the official OpenAI.Codex package is read-only input and will not be stopped or modified.'
    Write-Info 'This script does not request elevation. Run it as the current desktop user.'

    Write-Step 'Resolving official Codex package and independent paths'
    Write-Info "Read-only source: $Source"
    Write-Info "Independent destination: $Destination"
    Write-Info "Router state: $StateRoot"

    Write-Step 'Checking prerequisites'
    if (-not (Test-Path -LiteralPath $script:PatcherPath -PathType Leaf)) {
        throw "Windows patcher is missing: $script:PatcherPath"
    }

    $python = Resolve-Python
    $pythonVersionText = & $python.FilePath @($python.Prefix + @('-c', 'import platform; print(platform.python_version())'))
    if ($LASTEXITCODE -ne 0) { throw 'Python version check failed.' }
    $pythonVersion = Get-NumericVersion -Text ($pythonVersionText -join '') -ToolName 'Python'
    if ($pythonVersion -lt [Version]'3.10') {
        throw "Python 3.10 or newer is required; found $pythonVersion."
    }
    Write-Info "Python: $pythonVersion ($($python.FilePath))"

    $nodePath = Resolve-Executable -Names @('node.exe') -InstallHint 'Install Node.js 22.12 or newer.'
    $nodeVersionText = (& $nodePath --version) -join ''
    if ($LASTEXITCODE -ne 0) { throw 'Node.js version check failed.' }
    $nodeVersion = Get-NumericVersion -Text $nodeVersionText -ToolName 'Node.js'
    if ($nodeVersion -lt [Version]'22.12') {
        throw "Node.js 22.12 or newer is required; found $nodeVersion."
    }
    Write-Info "Node.js: $nodeVersion ($nodePath)"

    $npmPath = Resolve-Executable -Names @('npm.cmd', 'npm.exe') -InstallHint 'Reinstall Node.js with npm enabled.'
    $npmVersionText = (& $npmPath --version) -join ''
    if ($LASTEXITCODE -ne 0) { throw 'npm version check failed.' }
    Write-Info "npm: $npmVersionText ($npmPath)"

    if (-not [string]::IsNullOrWhiteSpace($MuxPath)) {
        Write-Info "Prebuilt multiplexer: $MuxPath"
    }
    if (-not [string]::IsNullOrWhiteSpace($LauncherPath)) {
        Write-Info "Prebuilt launcher: $LauncherPath"
    }
    if ([string]::IsNullOrWhiteSpace($MuxPath) -or [string]::IsNullOrWhiteSpace($LauncherPath)) {
        $goPath = Resolve-Executable -Names @('go.exe') -InstallHint 'Install Go 1.26 or newer, then open a new terminal.'
        $goVersionText = (& $goPath version) -join ''
        if ($LASTEXITCODE -ne 0) { throw 'Go version check failed.' }
        $goVersion = Get-NumericVersion -Text $goVersionText -ToolName 'Go'
        if ($goVersion -lt [Version]'1.26') {
            throw "Go 1.26 or newer is required; found $goVersion."
        }
        Write-Info "Go: $goVersion ($goPath)"
    }

    Write-Step 'Fingerprinting read-only source artifacts'
    $sourceBefore = Get-SourceFingerprint -SourceRoot $Source
    Write-Info "app.asar SHA-256: $($sourceBefore.AsarSha256)"
    Write-Info "codex.exe SHA-256: $($sourceBefore.CodexSha256)"
    Write-Info "ChatGPT.exe SHA-256: $($sourceBefore.DesktopSha256)"
    Write-Info "Codex.exe SHA-256: $($sourceBefore.DesktopShimSha256)"

    Write-Step 'Preflighting disk space and backup retention'
    $sourceBytes = Get-CsrTreeSize -Path $Source
    $calculatedFreeBytes = [Int64][Math]::Ceiling(($sourceBytes * 1.25) + 512MB)
    $requiredFreeBytes = [Math]::Max($calculatedFreeBytes, $MinimumFreeBytes)
    $availableFreeBytes = Get-CsrFreeSpace -Path $Destination
    Write-Info ("Source payload: {0:N2} GiB" -f ($sourceBytes / 1GB))
    Write-Info ("Required free space: {0:N2} GiB; available: {1:N2} GiB" -f ($requiredFreeBytes / 1GB), ($availableFreeBytes / 1GB))
    if ($availableFreeBytes -lt $requiredFreeBytes) {
        throw ("Insufficient free space for atomic staging. Required {0:N2} GiB, available {1:N2} GiB. Existing backups were not changed." -f ($requiredFreeBytes / 1GB), ($availableFreeBytes / 1GB))
    }
    $backupRoot = Join-Path (Split-Path -Parent $Destination) '.codex-subscription-router-backups'
    $existingBackupCount = @(Get-BackupDirectories -BackupRoot $backupRoot).Count
    Write-Info "Backup policy after successful verification: retain the newest $BackupRetention authenticated rollback build(s); $existingBackupCount backup container(s) currently exist."

    $destinationExists = Test-Path -LiteralPath $Destination
    if ($destinationExists -and -not $Force -and -not $script:EffectiveDryRun) {
        throw "Destination already exists: $Destination. Rerun with -Force to create a backup and replace it."
    }
    if ($destinationExists -and -not $script:EffectiveDryRun) {
        $routerProcesses = @(Get-RouterProcesses -InstallDestination $Destination)
        if ($routerProcesses.Count -gt 0) {
            $processSummary = ($routerProcesses | ForEach-Object { "$($_.Name) (PID $($_.ProcessId))" }) -join ', '
            throw "The existing router is running: $processSummary. Close only Codex Subscription Router and rerun. The installer will not terminate processes automatically."
        }
    }
    $previousInstallationMarker = if ($destinationExists -and -not $script:EffectiveDryRun) {
        Get-InstallationMarker -InstallDestination $Destination
    }
    else {
        $null
    }

    if (-not $SkipDependencyInstall) {
        if ($script:EffectiveDryRun) {
            Write-Step 'Checking locked Node build dependencies (dry run)'
            $asarTool = Join-Path $script:ProjectRoot 'node_modules\.bin\asar.cmd'
            if (-not (Test-Path -LiteralPath $asarTool -PathType Leaf)) {
                throw 'Dry run does not modify the checkout and locked node_modules is absent. Run npm ci --ignore-scripts first or perform a normal install.'
            }
            Write-Info 'Locked @electron/asar tool is present; npm ci was not run in dry-run mode.'
        }
        else {
            Write-Step 'Installing locked Node build dependencies'
            Push-Location $script:ProjectRoot
            try {
                Invoke-NativeCommand -FilePath $npmPath -ArgumentList @('ci', '--ignore-scripts', '--no-audit', '--no-fund') -FailureMessage 'npm ci failed.'
            }
            finally {
                Pop-Location
            }
        }
    }
    else {
        Write-Info 'Skipping npm ci by explicit request.'
    }

    $backupsBefore = @(Get-BackupDirectories -BackupRoot $backupRoot | ForEach-Object { $_.FullName })

    Write-Step 'Reserving a private high loopback control port'
    $script:ControlPortReservation = New-ControlPortReservation -RequestedPort $ControlPort
    $ControlPort = [int]$script:ControlPortReservation.Port
    Write-Info "Reserved control endpoint: 127.0.0.1:$ControlPort (held until the patched app is published)."

    Write-Step "$(if ($script:EffectiveDryRun) { 'Validating patch plan' } else { 'Building and installing independent router' })"
    $patchArguments = @($python.Prefix) + @(
        '-X', 'utf8',
        '-u',
        $script:PatcherPath,
        '--source', $Source,
        '--destination', $Destination,
        '--control-port', [string]$ControlPort
    )
    if (-not [string]::IsNullOrWhiteSpace($MuxPath)) {
        $patchArguments += @('--mux', $MuxPath)
    }
    if (-not [string]::IsNullOrWhiteSpace($LauncherPath)) {
        $patchArguments += @('--launcher', $LauncherPath)
    }
    if ($Force -or ($script:EffectiveDryRun -and $destinationExists)) {
        $patchArguments += '--force'
    }
    if ($script:EffectiveDryRun) {
        $patchArguments += '--dry-run'
    }
    if ($AllowUntestedSource) {
        $patchArguments += '--allow-untested-source'
    }

    $priorMuxHome = [Environment]::GetEnvironmentVariable('CODEX_MUX_HOME', 'Process')
    $priorMuxStateRoot = [Environment]::GetEnvironmentVariable('CODEX_MUX_STATE_ROOT', 'Process')
    $priorRouterDataDir = [Environment]::GetEnvironmentVariable('CODEX_ROUTER_DATA_DIR', 'Process')
    $newBackup = $null
    $patcherPublished = $false
    try {
        try {
            try {
                $env:CODEX_MUX_HOME = $StateRoot
                $env:CODEX_MUX_STATE_ROOT = $StateRoot
                $env:CODEX_ROUTER_DATA_DIR = $StateRoot
                Push-Location $script:ProjectRoot
                try {
                    try {
                        Invoke-NativeCommand -FilePath $python.FilePath -ArgumentList $patchArguments -FailureMessage 'Windows patcher failed; no partial destination should have been committed.'
                    }
                    finally {
                        if ($null -ne $script:ControlPortReservation) {
                            $script:ControlPortReservation.Listener.Stop()
                            $script:ControlPortReservation = $null
                        }
                    }
                    $patcherPublished = -not $script:EffectiveDryRun
                    if ($patcherPublished -and $destinationExists) {
                        $newBackup = Find-NewBackupDirectory -BackupRoot $backupRoot -ExistingPaths $backupsBefore -DestinationLeaf (Split-Path -Leaf $Destination) -PreviousMarker $previousInstallationMarker
                    }
                }
                finally {
                    Pop-Location
                }
            }
            finally {
                [Environment]::SetEnvironmentVariable('CODEX_MUX_HOME', $priorMuxHome, 'Process')
                [Environment]::SetEnvironmentVariable('CODEX_MUX_STATE_ROOT', $priorMuxStateRoot, 'Process')
                [Environment]::SetEnvironmentVariable('CODEX_ROUTER_DATA_DIR', $priorRouterDataDir, 'Process')
            }
        }
        finally {
            # Re-hash even when the patcher fails. A failed build is never
            # allowed to weaken the invariant that WindowsApps was read-only.
            $sourceAfter = Get-SourceFingerprint -SourceRoot $Source
            Assert-SourceUnchanged -Before $sourceBefore -After $sourceAfter
        }
    }
    catch {
        $patchFailure = $_.Exception.Message
        if (-not $patcherPublished -and -not $script:EffectiveDryRun -and
            (Test-DestinationPublishedThisRun -InstallDestination $Destination -StartedAtUtc $script:InstallStartedAt -ExpectedSourceAsarSha256 $sourceBefore.AsarSha256)) {
            $patcherPublished = $true
            if ($destinationExists) {
                $newBackup = Find-NewBackupDirectory -BackupRoot $backupRoot -ExistingPaths $backupsBefore -DestinationLeaf (Split-Path -Leaf $Destination) -PreviousMarker $previousInstallationMarker
            }
        }
        if ($patcherPublished) {
            try {
                Restore-PreviousInstallation -InstallDestination $Destination -RouterStateRoot $StateRoot -HadPreviousInstallation $destinationExists -BackupDirectory $newBackup -Reason $patchFailure
                throw "$patchFailure Automatic rollback completed."
            }
            catch {
                if ($_.Exception.Message.EndsWith('Automatic rollback completed.')) {
                    throw
                }
                throw "$patchFailure Automatic rollback also failed: $($_.Exception.Message)"
            }
        }
        throw
    }

    if ($script:EffectiveDryRun) {
        Write-Host "`nDry run completed successfully. No router app or account state was installed." -ForegroundColor Green
        Write-Info "A normal installation would write the app to: $Destination"
        Write-Info "A normal installation would keep state at: $StateRoot"
    }
    else {
        try {
            Write-Step 'Verifying installed layout and provenance metadata'
            $launcherPath = Test-InstalledLayout -InstallDestination $Destination -RouterStateRoot $StateRoot
            Write-Info "Launcher: $launcherPath"
            Invoke-LauncherSelfTest -LauncherPath $launcherPath -ExpectedStateRoot $StateRoot -ExpectedControlPort $ControlPort

            Write-Step 'Hardening private router payload and state ACLs'
            [void](Set-CsrPrivateDirectoryAcl -Path $Destination)
            [void](Set-CsrPrivateDirectoryAcl -Path $StateRoot)
            if (Test-Path -LiteralPath $backupRoot -PathType Container) {
                [void](Set-CsrPrivateDirectoryAcl -Path $backupRoot)
            }
            Write-Info 'Protected ACLs verified: only the current user and SYSTEM have router file access.'

            $verificationScript = Join-Path $script:ProjectRoot 'scripts\verify_windows_build.ps1'
            if (-not (Test-Path -LiteralPath $verificationScript -PathType Leaf)) {
                throw "Post-install verifier is missing: $verificationScript"
            }
            $currentPowerShell = (Get-Process -Id $PID).Path
            Invoke-NativeCommand -FilePath $currentPowerShell -ArgumentList @(
                '-NoProfile',
                '-ExecutionPolicy', 'Bypass',
                '-File', $verificationScript,
                '-BuildPath', $Destination,
                '-SourcePath', $Source,
                '-StateRoot', $StateRoot
            ) -FailureMessage 'Post-install Windows verification failed.'
        }
        catch {
            $verificationFailure = $_.Exception.Message
            try {
                Restore-PreviousInstallation -InstallDestination $Destination -RouterStateRoot $StateRoot -HadPreviousInstallation $destinationExists -BackupDirectory $newBackup -Reason $verificationFailure
            }
            catch {
                throw "$verificationFailure Automatic rollback also failed: $($_.Exception.Message)"
            }
            throw "$verificationFailure Automatic rollback completed."
        }

        Write-Step 'Applying authenticated backup retention'
        $newBackup = Invoke-AuthenticatedBackupRetention -InstallDestination $Destination -RouterStateRoot $StateRoot -BackupRoot $backupRoot -Keep $BackupRetention

        $shortcutRecord = $null
        if ($NoShortcut) {
            Write-Info 'Start Menu shortcut skipped by -NoShortcut.'
        }
        else {
            try {
                $shortcutRecord = Install-StartMenuShortcut -LauncherPath $launcherPath -InstallDestination $Destination -RouterStateRoot $StateRoot
            }
            catch {
                Write-Warning "The app is verified, but the optional Start Menu shortcut could not be installed: $($_.Exception.Message)"
            }
        }

        Write-Host "`nInstalled successfully: $Destination" -ForegroundColor Green
        Write-Info "State and account homes: $StateRoot"
        Write-Info "Build metadata: $(Join-Path $Destination 'codex-mux-build.json')"
        Show-RollbackInstructions -InstallDestination $Destination -RouterStateRoot $StateRoot -BackupDirectory $newBackup -ShortcutRecord $shortcutRecord

        if ($NoLaunch) {
            Write-Info 'Launch skipped by -NoLaunch. The official Codex app was not restarted.'
        }
        else {
            Write-Step 'Launching independent Codex Subscription Router'
            try {
                $launchVariableNames = @('CODEX_ROUTER_DATA_DIR', 'CODEX_MUX_HOME', 'CODEX_MUX_STATE_ROOT')
                $launchPriorValues = @{}
                foreach ($name in $launchVariableNames) {
                    $launchPriorValues[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
                    [Environment]::SetEnvironmentVariable($name, $null, 'Process')
                }
                try {
                    Start-Process -FilePath $launcherPath -WorkingDirectory $Destination
                }
                finally {
                    foreach ($name in $launchVariableNames) {
                        [Environment]::SetEnvironmentVariable($name, $launchPriorValues[$name], 'Process')
                    }
                }
                Write-Info 'Launched the independent router; the official Codex app was left running and unchanged.'
            }
            catch {
                Write-Warning "Installation and verification succeeded, but the router could not be launched automatically: $($_.Exception.Message)"
                Write-Info "Launch it manually from: $launcherPath"
            }
        }
    }
}
catch {
    $script:ExitCode = 1
    Write-Host "`nINSTALL FAILED" -ForegroundColor Red
    Write-Host $_.Exception.Message -ForegroundColor Red
    Write-Info 'The official OpenAI.Codex package was never an installation target.'
    Write-Info 'If replacement had begun, the patcher performs an automatic rollback and preserves recoverable backups.'
}
finally {
    if ($null -ne $script:ControlPortReservation) {
        try { $script:ControlPortReservation.Listener.Stop() }
        catch { Write-Warning "Could not release control-port reservation cleanly: $($_.Exception.Message)" }
        $script:ControlPortReservation = $null
    }
    if ($script:TranscriptStarted) {
        Write-Info "Transcript saved to: $logPath"
        Stop-Transcript | Out-Null
    }
    if ($null -ne $script:InstallMutex) {
        if ($script:InstallMutexAcquired) {
            try {
                $script:InstallMutex.ReleaseMutex()
            }
            catch {
                Write-Warning "Could not release the installer mutex cleanly: $($_.Exception.Message)"
            }
        }
        $script:InstallMutex.Dispose()
    }
}

exit $script:ExitCode
