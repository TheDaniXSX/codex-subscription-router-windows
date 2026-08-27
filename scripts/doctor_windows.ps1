#Requires -Version 5.1

<#
.SYNOPSIS
Collects a share-safe, read-only diagnostic report for Codex Subscription Router.

.DESCRIPTION
The doctor never starts, stops, or modifies a process and never deletes or writes
router data. It inventories processes by executable path, checks the loopback
control port, reports a whitelisted subset of build metadata and account status,
measures known router directories, and includes small redacted log excerpts.

The control token is read only when the listener PID belongs to AppRoot. The token
is sent in the required request header and is never included in the report.

.EXAMPLE
./scripts/doctor_windows.ps1

.EXAMPLE
./scripts/doctor_windows.ps1 -OutputFormat Json > router-doctor.json
#>

[CmdletBinding()]
param(
    [Parameter()]
    [string]$AppRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),

    [Parameter()]
    [string]$StateRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router Data'),

    [Parameter()]
    [string]$BackupRoot = (Join-Path $env:LOCALAPPDATA 'Programs\.codex-subscription-router-backups'),

    [Parameter()]
    [ValidateRange(0, 65535)]
    [int]$ControlPort = 0,

    [Parameter()]
    [ValidateSet('Text', 'Json')]
    [string]$OutputFormat = 'Text',

    [Parameter()]
    [ValidateRange(0, 10)]
    [int]$MaximumLogFiles = 3,

    [Parameter()]
    [ValidateRange(1, 200)]
    [int]$MaximumLogLines = 30,

    [Parameter()]
    [string[]]$FailedRoots,

    [Parameter()]
    [string[]]$TempRoots,

    [Parameter()]
    [string]$StateFilePath,

    [Parameter()]
    [string]$ProcessSnapshotPath,

    [Parameter()]
    [string]$AccountSnapshotPath,

    [Parameter()]
    [string]$ManifestPath,

    [Parameter()]
    [string]$LauncherConfigPath,

    [Parameter()]
    [switch]$SkipLiveControl,

    [Parameter()]
    [switch]$SkipLogExcerpts,

    [Parameter()]
    [switch]$RevealPaths
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-ObjectProperty {
    param(
        [AllowNull()][object]$Object,
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowNull()][object]$Default = $null
    )

    if ($null -eq $Object) { return $Default }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return $Default }
    return $property.Value
}

function Resolve-DiagnosticPath {
    param([Parameter(Mandatory = $true)][string]$Path)

    $expanded = [Environment]::ExpandEnvironmentVariables($Path)
    return [IO.Path]::GetFullPath($expanded).TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    )
}

function Test-DiagnosticPathWithin {
    param(
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$Parent
    )

    try {
        $candidatePath = Resolve-DiagnosticPath -Path $Candidate
        $parentPath = Resolve-DiagnosticPath -Path $Parent
    }
    catch { return $false }
    if ($candidatePath.Equals($parentPath, [StringComparison]::OrdinalIgnoreCase)) {
        return $true
    }
    return $candidatePath.StartsWith(
        $parentPath + [IO.Path]::DirectorySeparatorChar,
        [StringComparison]::OrdinalIgnoreCase
    )
}

$script:ResolvedAppRoot = Resolve-DiagnosticPath -Path $AppRoot
$script:ResolvedStateRoot = Resolve-DiagnosticPath -Path $StateRoot
$script:ResolvedBackupRoot = Resolve-DiagnosticPath -Path $BackupRoot
$script:ControlPortOverride = $ControlPort
$script:MaximumLogFileCount = $MaximumLogFiles
$script:MaximumLogLineCount = $MaximumLogLines
$script:ManifestOverridePath = $ManifestPath
$script:LauncherConfigOverridePath = $LauncherConfigPath
$script:SkipDiagnosticLogExcerpts = [bool]$SkipLogExcerpts

function ConvertTo-DiagnosticPath {
    param([AllowNull()][string]$Path)

    if ([string]::IsNullOrWhiteSpace($Path)) { return $null }
    if ($RevealPaths) { return $Path }
    try {
        $full = Resolve-DiagnosticPath -Path $Path
        $mappings = @(
            [PSCustomObject]@{ Root = $script:ResolvedAppRoot; Token = '<APP>' },
            [PSCustomObject]@{ Root = $script:ResolvedStateRoot; Token = '<STATE>' },
            [PSCustomObject]@{ Root = $script:ResolvedBackupRoot; Token = '<BACKUPS>' }
        )
        if (-not [string]::IsNullOrWhiteSpace($env:TEMP)) {
            $mappings += [PSCustomObject]@{ Root = (Resolve-DiagnosticPath -Path $env:TEMP); Token = '%TEMP%' }
        }
        if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
            $mappings += [PSCustomObject]@{ Root = (Resolve-DiagnosticPath -Path $env:LOCALAPPDATA); Token = '%LOCALAPPDATA%' }
        }
        if (-not [string]::IsNullOrWhiteSpace($env:USERPROFILE)) {
            $mappings += [PSCustomObject]@{ Root = (Resolve-DiagnosticPath -Path $env:USERPROFILE); Token = '%USERPROFILE%' }
        }
        foreach ($mapping in @($mappings | Sort-Object { $_.Root.Length } -Descending)) {
            if ($full.Equals($mapping.Root, [StringComparison]::OrdinalIgnoreCase)) {
                return $mapping.Token
            }
            if (Test-DiagnosticPathWithin -Candidate $full -Parent $mapping.Root) {
                return $mapping.Token + $full.Substring($mapping.Root.Length)
            }
        }
    }
    catch { Write-Verbose "Could not normalize diagnostic display path: $($_.Exception.Message)" }
    return '<PATH>'
}

function Protect-DiagnosticText {
    param([AllowNull()][string]$Text)

    if ($null -eq $Text) { return $null }
    $value = [string]$Text
    foreach ($path in @(
            $script:ResolvedAppRoot,
            $script:ResolvedStateRoot,
            $script:ResolvedBackupRoot,
            $env:USERPROFILE,
            $env:LOCALAPPDATA,
            $env:TEMP
        )) {
        if (-not [string]::IsNullOrWhiteSpace($path)) {
            $replacement = ConvertTo-DiagnosticPath -Path $path
            $value = [regex]::Replace($value, [regex]::Escape($path), $replacement, [Text.RegularExpressions.RegexOptions]::IgnoreCase)
        }
    }
    $value = [regex]::Replace($value, '(?i)(authorization\s*:\s*(?:bearer|basic)\s+)[^\s,;]+', '$1<REDACTED>')
    $value = [regex]::Replace($value, '(?i)(x-codex-mux-token\s*[:=]\s*)[^\s,;]+', '$1<REDACTED>')
    $value = [regex]::Replace($value, '(?i)("?(?:access[_-]?token|refresh[_-]?token|id[_-]?token|control[_-]?token|client[_-]?secret|password)"?\s*[:=]\s*"?)[^"\s,;}]+', '$1<REDACTED>')
    $value = [regex]::Replace($value, '(?i)([?&](?:token|code|secret|key)=)[^&\s]+', '$1<REDACTED>')
    $value = [regex]::Replace($value, '(?i)\b(?:github_pat_|gh[opusr]_)[A-Za-z0-9_\-]+\b', '<REDACTED-TOKEN>')
    $value = [regex]::Replace($value, '(?i)\bsk-[A-Za-z0-9_\-]{12,}\b', '<REDACTED-TOKEN>')
    $value = [regex]::Replace($value, '(?i)\b[a-f0-9]{64}\b', '<REDACTED-HEX>')
    $value = [regex]::Replace($value, '(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b', '<REDACTED-EMAIL>')
    $value = [regex]::Replace($value, '(?i)(device\s+(?:authorization\s+)?code\s*[:=]\s*)[A-Z0-9\-]{4,}', '$1<REDACTED>')
    $value = [regex]::Replace($value, '(?i)\b[A-Z]:\\Users\\[^\\\s"'';,]+(?:\\[^\\\s"'';,}]+)*', '<REDACTED-PATH>')
    return $value
}

function Get-DirectoryMeasurement {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Path
    )

    [Int64]$bytes = 0
    [Int64]$files = 0
    [Int64]$directories = 0
    [Int64]$accessErrors = 0
    if (-not (Test-Path -LiteralPath $Path)) {
        return [PSCustomObject]@{
            Name = $Name; Path = (ConvertTo-DiagnosticPath $Path); Exists = $false
            Bytes = [Int64]0; GiB = 0.0; Files = [Int64]0; Directories = [Int64]0; AccessErrors = [Int64]0
        }
    }
    if (Test-Path -LiteralPath $Path -PathType Leaf) {
        try {
            $item = Get-Item -LiteralPath $Path -Force
            $bytes = [Int64]$item.Length
            $files = 1
        }
        catch { $accessErrors++ }
    }
    else {
        $enumerationErrors = @()
        $directories = 1
        foreach ($item in @(Get-ChildItem -LiteralPath $Path -Force -Recurse -ErrorAction SilentlyContinue -ErrorVariable enumerationErrors)) {
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { continue }
            if ($item.PSIsContainer) {
                $directories++
            }
            else {
                $files++
                $bytes += [Int64]$item.Length
            }
        }
        $accessErrors = @($enumerationErrors).Count
    }
    return [PSCustomObject]@{
        Name = $Name
        Path = ConvertTo-DiagnosticPath $Path
        Exists = $true
        Bytes = $bytes
        GiB = [Math]::Round($bytes / 1GB, 3)
        Files = $files
        Directories = $directories
        AccessErrors = $accessErrors
    }
}

function Get-LiveProcessInventory {
    try {
        return @(Get-CimInstance -ClassName Win32_Process -Property ProcessId, ParentProcessId, Name, ExecutablePath, WorkingSetSize, HandleCount -ErrorAction Stop |
                Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_.ExecutablePath) })
    }
    catch {
        $script:ProcessInventoryError = Protect-DiagnosticText $_.Exception.Message
        return @()
    }
}

function ConvertTo-ProcessRecord {
    param(
        [Parameter(Mandatory = $true)][object]$Process,
        [Parameter(Mandatory = $true)][string]$Kind
    )

    $workingSet = [Int64](Get-ObjectProperty -Object $Process -Name 'WorkingSetSize' -Default 0)
    return [PSCustomObject]@{
        Kind = $Kind
        PID = [int](Get-ObjectProperty -Object $Process -Name 'ProcessId' -Default 0)
        ParentPID = [int](Get-ObjectProperty -Object $Process -Name 'ParentProcessId' -Default 0)
        Name = [string](Get-ObjectProperty -Object $Process -Name 'Name' -Default 'unknown')
        ExecutablePath = ConvertTo-DiagnosticPath ([string](Get-ObjectProperty -Object $Process -Name 'ExecutablePath' -Default ''))
        WorkingSetMB = [Math]::Round($workingSet / 1MB, 1)
        Handles = [Int64](Get-ObjectProperty -Object $Process -Name 'HandleCount' -Default 0)
    }
}

function Get-ConfiguredControlPort {
    if ($script:ControlPortOverride -gt 0) {
        return [PSCustomObject]@{ Port = $script:ControlPortOverride; Source = 'explicit-override'; Error = $null }
    }
    $sidecarPath = if (-not [string]::IsNullOrWhiteSpace($script:LauncherConfigOverridePath)) {
        $script:LauncherConfigOverridePath
    }
    else {
        Join-Path $script:ResolvedAppRoot 'resources\codex-router\launcher-config.json'
    }
    $manifestCandidates = if (-not [string]::IsNullOrWhiteSpace($script:ManifestOverridePath)) {
        @($script:ManifestOverridePath)
    }
    else {
        @(
            (Join-Path $script:ResolvedAppRoot 'codex-mux-build.json'),
            (Join-Path $script:ResolvedAppRoot 'app\codex-mux-build.json')
        )
    }

    function Read-ControlPortRecord {
        param([Parameter(Mandatory = $true)][string]$Path, [Parameter(Mandatory = $true)][string]$Source)
        if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
            return [PSCustomObject]@{ Present = $false; Valid = $false; Port = 0; Source = $Source; Error = $null }
        }
        try {
            $configuration = Get-Content -Raw -LiteralPath $Path | ConvertFrom-Json
            $schema = [int](Get-ObjectProperty $configuration 'schemaVersion' 0)
            $portValue = [int](Get-ObjectProperty $configuration 'controlPort' 0)
            if ($schema -ne 2) {
                return [PSCustomObject]@{ Present = $true; Valid = $false; Port = 0; Source = $Source; Error = "unsupported schemaVersion $schema" }
            }
            if ($portValue -lt 49152 -or $portValue -gt 65535) {
                return [PSCustomObject]@{ Present = $true; Valid = $false; Port = 0; Source = $Source; Error = 'controlPort is outside 49152..65535' }
            }
            return [PSCustomObject]@{ Present = $true; Valid = $true; Port = $portValue; Source = $Source; Error = $null }
        }
        catch {
            return [PSCustomObject]@{ Present = $true; Valid = $false; Port = 0; Source = $Source; Error = 'invalid JSON or controlPort value' }
        }
    }

    $sidecar = Read-ControlPortRecord -Path $sidecarPath -Source 'launcher-config'
    $manifestRecord = [PSCustomObject]@{ Present = $false; Valid = $false; Port = 0; Source = 'manifest'; Error = $null }
    foreach ($candidate in $manifestCandidates) {
        $candidateRecord = Read-ControlPortRecord -Path $candidate -Source 'manifest'
        if ($candidateRecord.Present) {
            $manifestRecord = $candidateRecord
            break
        }
    }
    foreach ($record in @($sidecar, $manifestRecord)) {
        if ($record.Present -and -not $record.Valid) {
            return [PSCustomObject]@{ Port = 0; Source = "$($record.Source)-invalid"; Error = $record.Error }
        }
    }
    if ($sidecar.Valid -and $manifestRecord.Valid) {
        if ([int]$sidecar.Port -ne [int]$manifestRecord.Port) {
            return [PSCustomObject]@{ Port = 0; Source = 'configuration-mismatch'; Error = 'launcher configuration and build manifest control ports differ' }
        }
        return [PSCustomObject]@{ Port = [int]$sidecar.Port; Source = 'launcher-config+manifest'; Error = $null }
    }
    if ($sidecar.Valid) {
        return [PSCustomObject]@{ Port = [int]$sidecar.Port; Source = 'launcher-config'; Error = $null }
    }
    if ($manifestRecord.Valid) {
        return [PSCustomObject]@{ Port = [int]$manifestRecord.Port; Source = 'manifest'; Error = $null }
    }
    return [PSCustomObject]@{ Port = 0; Source = 'not-discovered'; Error = $null }
}

function Get-PortRecord {
    param(
        [Parameter(Mandatory = $true)][object[]]$Processes,
        [AllowNull()][object]$FixturePort,
        [Parameter(Mandatory = $true)][object]$Configuration
    )

    $listening = $false
    $ownerPID = 0
    $portNumber = [int]$Configuration.Port
    $portSource = [string]$Configuration.Source
    if ($null -ne $FixturePort) {
        $fixturePortNumber = [int](Get-ObjectProperty -Object $FixturePort -Name 'port' -Default 0)
        if ($portNumber -eq 0 -and $Configuration.Source -eq 'not-discovered' -and
            $fixturePortNumber -gt 0 -and $fixturePortNumber -le 65535) {
            $portNumber = $fixturePortNumber
            $portSource = 'process-snapshot'
        }
        if ($portNumber -gt 0 -and ($fixturePortNumber -eq 0 -or $fixturePortNumber -eq $portNumber)) {
            $listening = [bool](Get-ObjectProperty -Object $FixturePort -Name 'listening' -Default $false)
            $ownerPID = [int](Get-ObjectProperty -Object $FixturePort -Name 'owningProcessId' -Default 0)
        }
    }
    elseif ($portNumber -gt 0) {
        try {
            $listener = @(Get-NetTCPConnection -State Listen -LocalPort $portNumber -ErrorAction Stop |
                    Where-Object { $_.LocalAddress -in @('127.0.0.1', '::1') } |
                    Select-Object -First 1)
            if ($listener.Count -gt 0) {
                $listening = $true
                $ownerPID = [int]$listener[0].OwningProcess
            }
        }
        catch {
            $client = [Net.Sockets.TcpClient]::new()
            try {
                $connect = $client.BeginConnect('127.0.0.1', $portNumber, $null, $null)
                if ($connect.AsyncWaitHandle.WaitOne(350)) {
                    $client.EndConnect($connect)
                    $listening = $true
                }
            }
            catch { Write-Verbose "Loopback listener probe failed: $($_.Exception.Message)" }
            finally { $client.Dispose() }
        }
    }
    $owner = @($Processes | Where-Object { [int](Get-ObjectProperty $_ 'ProcessId' 0) -eq $ownerPID } | Select-Object -First 1)
    $ownerPath = if ($owner.Count -gt 0) { [string](Get-ObjectProperty $owner[0] 'ExecutablePath' '') } else { '' }
    $ownedByRouter = $ownerPID -gt 0 -and (Test-DiagnosticPathWithin -Candidate $ownerPath -Parent $script:ResolvedAppRoot)
    return [PSCustomObject]@{
        Port = if ($portNumber -gt 0) { $portNumber } else { $null }
        Source = $portSource
        ConfigurationError = Get-ObjectProperty $Configuration 'Error' $null
        Address = if ($portNumber -gt 0) { "127.0.0.1:$portNumber" } else { $null }
        Discovered = ($portNumber -gt 0)
        Listening = $listening
        OwningPID = if ($ownerPID -gt 0) { $ownerPID } else { $null }
        OwnedByRouter = $ownedByRouter
        OwnerPath = if ($ownerPath) { ConvertTo-DiagnosticPath $ownerPath } else { $null }
        Health = 'not-checked'
    }
}

function ConvertTo-SafeAccountRecordList {
    param(
        [Parameter(Mandatory = $true)][object[]]$Accounts,
        [Parameter(Mandatory = $true)][string]$Source,
        [AllowNull()][object]$ThreadOwner
    )

    $threadCounts = @{}
    if ($null -ne $ThreadOwner) {
        foreach ($property in @($ThreadOwner.PSObject.Properties)) {
            $owner = [string]$property.Value
            if (-not $threadCounts.ContainsKey($owner)) { $threadCounts[$owner] = 0 }
            $threadCounts[$owner]++
        }
    }
    $records = [Collections.Generic.List[object]]::new()
    $index = 0
    foreach ($account in $Accounts) {
        $index++
        $id = [string](Get-ObjectProperty $account 'id' '')
        $controller = [bool](Get-ObjectProperty $account 'controller' $false)
        $connectedProperty = $account.PSObject.Properties['connected']
        $connected = if ($null -eq $connectedProperty) { $null } else { [bool]$connectedProperty.Value }
        $threadCountProperty = $account.PSObject.Properties['threadCount']
        $threadCount = if ($null -ne $threadCountProperty) {
            [int]$threadCountProperty.Value
        }
        elseif ($threadCounts.ContainsKey($id)) {
            [int]$threadCounts[$id]
        }
        else { 0 }
        $rawError = [string](Get-ObjectProperty $account 'error' '')
        $records.Add([PSCustomObject]@{
                Account = if ($controller) { 'Controller' } else { "Secondary-$index" }
                Enabled = [bool](Get-ObjectProperty $account 'enabled' $false)
                Controller = $controller
                Connected = $connected
                AuthType = [string](Get-ObjectProperty $account 'authType' '')
                PlanType = [string](Get-ObjectProperty $account 'planType' '')
                ThreadCount = $threadCount
                Status = if (-not [bool](Get-ObjectProperty $account 'enabled' $false)) {
                    'disabled'
                }
                elseif ($null -eq $connected) {
                    'unknown-router-stopped'
                }
                elseif ($connected) { 'connected' } else { 'disconnected' }
                Error = if ($rawError) { Protect-DiagnosticText $rawError } else { $null }
                Source = $Source
            })
    }
    return @($records)
}

function Get-SafeManifest {
    $candidates = if (-not [string]::IsNullOrWhiteSpace($script:ManifestOverridePath)) {
        @($script:ManifestOverridePath)
    }
    else {
        @(
            (Join-Path $script:ResolvedAppRoot 'codex-mux-build.json'),
            (Join-Path $script:ResolvedAppRoot 'app\codex-mux-build.json'),
            (Join-Path $script:ResolvedAppRoot 'router-package.json')
        ) | Select-Object -Unique
    }
    $allowedFields = @(
        'schemaVersion', 'projectVersion', 'routerVersion', 'sourceCommit',
        'sourcePackage', 'sourceVersion', 'sourceAsarVersion', 'sourceBuild',
        'architecture', 'patchProfile', 'stateSchema', 'builtAtUtc', 'createdAtUtc', 'controlPort'
    )
    foreach ($path in $candidates) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { continue }
        try {
            $raw = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json
            $fields = [ordered]@{}
            foreach ($name in $allowedFields) {
                $property = $raw.PSObject.Properties[$name]
                if ($null -ne $property -and $null -ne $property.Value -and [string]$property.Value -ne '') {
                    $fields[$name] = $property.Value
                }
            }
            return [PSCustomObject]@{
                Present = $true
                Path = ConvertTo-DiagnosticPath $path
                Fields = [PSCustomObject]$fields
                Error = $null
            }
        }
        catch {
            return [PSCustomObject]@{
                Present = $true; Path = (ConvertTo-DiagnosticPath $path); Fields = $null
                Error = Protect-DiagnosticText $_.Exception.Message
            }
        }
    }
    return [PSCustomObject]@{ Present = $false; Path = $null; Fields = $null; Error = $null }
}

function Get-RedactedLogExcerptList {
    if ($script:SkipDiagnosticLogExcerpts -or $script:MaximumLogFileCount -eq 0) { return @() }
    $roots = @((Join-Path $script:ResolvedStateRoot 'logs'))
    if ($null -ne $TempRoots) { $roots += $TempRoots }
    $files = [Collections.Generic.List[object]]::new()
    foreach ($root in @($roots | Select-Object -Unique)) {
        if (-not (Test-Path -LiteralPath $root -PathType Container)) { continue }
        try {
            foreach ($file in @(Get-ChildItem -LiteralPath $root -File -Force -Recurse -ErrorAction Stop |
                    Where-Object { $_.Extension -in @('.log', '.txt') })) {
                $files.Add($file)
            }
        }
        catch { Write-Verbose "Could not enumerate diagnostic logs under '$root': $($_.Exception.Message)" }
    }
    $result = [Collections.Generic.List[object]]::new()
    foreach ($file in @($files | Sort-Object LastWriteTimeUtc -Descending | Select-Object -First $script:MaximumLogFileCount)) {
        try {
            $lines = @(Get-Content -LiteralPath $file.FullName -Tail $script:MaximumLogLineCount -ErrorAction Stop |
                    ForEach-Object { Protect-DiagnosticText ([string]$_) })
            $result.Add([PSCustomObject]@{
                    Path = ConvertTo-DiagnosticPath $file.FullName
                    LastWriteTimeUtc = $file.LastWriteTimeUtc.ToString('o')
                    Lines = $lines
                })
        }
        catch {
            $result.Add([PSCustomObject]@{
                    Path = ConvertTo-DiagnosticPath $file.FullName
                    LastWriteTimeUtc = $file.LastWriteTimeUtc.ToString('o')
                    Lines = @("<unreadable: $(Protect-DiagnosticText $_.Exception.Message)>")
                })
        }
    }
    return @($result)
}

if ($null -eq $FailedRoots) {
    $FailedRoots = @((Join-Path $script:ResolvedStateRoot 'failed-installations'))
    $packagesRoot = if ($env:LOCALAPPDATA) { Join-Path $env:LOCALAPPDATA 'Packages' } else { $null }
    if ($packagesRoot -and (Test-Path -LiteralPath $packagesRoot -PathType Container)) {
        foreach ($package in @(Get-ChildItem -LiteralPath $packagesRoot -Directory -Filter 'OpenAI.Codex_*' -ErrorAction SilentlyContinue)) {
            $FailedRoots += Join-Path $package.FullName 'LocalCache\Local\Codex Subscription Router Data\failed-installations'
        }
    }
}
if ($null -eq $TempRoots) {
    $TempRoots = @()
    if (-not [string]::IsNullOrWhiteSpace($env:TEMP) -and (Test-Path -LiteralPath $env:TEMP -PathType Container)) {
        $fixedTemp = Join-Path $env:TEMP 'Codex Subscription Router'
        if (Test-Path -LiteralPath $fixedTemp) { $TempRoots += $fixedTemp }
        foreach ($item in @(Get-ChildItem -LiteralPath $env:TEMP -Directory -Force -ErrorAction SilentlyContinue |
                Where-Object { $_.Name -like 'codex-router-*' -or $_.Name -like 'codex-subscription-router-*' })) {
            $TempRoots += $item.FullName
        }
    }
}
if ([string]::IsNullOrWhiteSpace($StateFilePath)) {
    $StateFilePath = Join-Path $script:ResolvedStateRoot 'state.json'
}

$fixture = $null
$script:ProcessInventoryError = $null
if (-not [string]::IsNullOrWhiteSpace($ProcessSnapshotPath)) {
    $fixture = Get-Content -Raw -LiteralPath $ProcessSnapshotPath | ConvertFrom-Json
    $processes = @(Get-ObjectProperty $fixture 'processes' @())
    foreach ($process in $processes) {
        $pathProperty = $process.PSObject.Properties['ExecutablePath']
        if ($null -ne $pathProperty -and [string]$pathProperty.Value -like '<APP>*') {
            $suffix = ([string]$pathProperty.Value).Substring(5).TrimStart('\', '/')
            $pathProperty.Value = if ($suffix) { Join-Path $script:ResolvedAppRoot $suffix } else { $script:ResolvedAppRoot }
        }
        elseif ($null -ne $pathProperty -and [string]$pathProperty.Value -like '<OFFICIAL>*') {
            $suffix = ([string]$pathProperty.Value).Substring(10).TrimStart('\', '/')
            $officialRoot = Join-Path ([IO.Path]::GetPathRoot($script:ResolvedAppRoot)) 'Program Files\WindowsApps\OpenAI.Codex_fixture_x64__2p2nqsd0c76g0'
            $pathProperty.Value = if ($suffix) { Join-Path $officialRoot $suffix } else { $officialRoot }
        }
    }
}
else {
    $processes = @(Get-LiveProcessInventory)
}
$routerRaw = @($processes | Where-Object {
        Test-DiagnosticPathWithin -Candidate ([string](Get-ObjectProperty $_ 'ExecutablePath' '')) -Parent $script:ResolvedAppRoot
    })
$officialRaw = @($processes | Where-Object {
        $path = [string](Get-ObjectProperty $_ 'ExecutablePath' '')
        $path -match '(?i)[\\/]WindowsApps[\\/]OpenAI\.Codex_' -and
            -not (Test-DiagnosticPathWithin -Candidate $path -Parent $script:ResolvedAppRoot)
    })
$routerProcesses = @($routerRaw | ForEach-Object { ConvertTo-ProcessRecord $_ 'Router' })
$officialProcesses = @($officialRaw | ForEach-Object { ConvertTo-ProcessRecord $_ 'OfficialCodex' })
$fixturePort = if ($null -ne $fixture) { Get-ObjectProperty $fixture 'port' $null } else { $null }
$configuredPort = Get-ConfiguredControlPort
$port = Get-PortRecord -Processes $processes -FixturePort $fixturePort -Configuration $configuredPort

$accounts = @()
$accountSource = 'unavailable'
if (-not [string]::IsNullOrWhiteSpace($AccountSnapshotPath)) {
    $snapshot = Get-Content -Raw -LiteralPath $AccountSnapshotPath | ConvertFrom-Json
    $accounts = ConvertTo-SafeAccountRecordList -Accounts @(Get-ObjectProperty $snapshot 'accounts' @()) -Source 'snapshot' -ThreadOwner $null
    $accountSource = 'snapshot'
}
elseif ([string]::IsNullOrWhiteSpace($ProcessSnapshotPath) -and -not $SkipLiveControl -and
    $port.Listening -and $port.OwnedByRouter) {
    try {
        $health = Invoke-RestMethod -Method Get -Uri "http://127.0.0.1:$($port.Port)/v1/health" -TimeoutSec 2
        $port.Health = if ([bool](Get-ObjectProperty $health 'ok' $false)) { 'ok' } else { 'unexpected-response' }
        $tokenPath = Join-Path $script:ResolvedStateRoot 'control-token'
        if (Test-Path -LiteralPath $tokenPath -PathType Leaf) {
            $token = (Get-Content -Raw -LiteralPath $tokenPath).Trim()
            if ($token -match '^[0-9a-fA-F]{64}$') {
                $response = Invoke-RestMethod -Method Get -Uri "http://127.0.0.1:$($port.Port)/v1/accounts" `
                    -Headers @{ 'X-Codex-Mux-Token' = $token } -TimeoutSec 5
                $accounts = ConvertTo-SafeAccountRecordList -Accounts @(Get-ObjectProperty $response 'accounts' @()) -Source 'live-control' -ThreadOwner $null
                $accountSource = 'live-control'
            }
        }
    }
    catch {
        $port.Health = 'error'
    }
}
if ($accounts.Count -eq 0 -and (Test-Path -LiteralPath $StateFilePath -PathType Leaf)) {
    try {
        $state = Get-Content -Raw -LiteralPath $StateFilePath | ConvertFrom-Json
        $accounts = ConvertTo-SafeAccountRecordList -Accounts @(Get-ObjectProperty $state 'accounts' @()) -Source 'offline-state' `
            -ThreadOwner (Get-ObjectProperty $state 'threadOwner' $null)
        $accountSource = 'offline-state'
    }
    catch { $accountSource = 'invalid-offline-state' }
}

$storage = [Collections.Generic.List[object]]::new()
$storage.Add((Get-DirectoryMeasurement -Name 'ActiveApp' -Path $script:ResolvedAppRoot))
$storage.Add((Get-DirectoryMeasurement -Name 'State' -Path $script:ResolvedStateRoot))
$storage.Add((Get-DirectoryMeasurement -Name 'Backups' -Path $script:ResolvedBackupRoot))
foreach ($path in @($FailedRoots | Select-Object -Unique)) {
    $storage.Add((Get-DirectoryMeasurement -Name 'FailedInstallation' -Path $path))
}
foreach ($path in @($TempRoots | Select-Object -Unique)) {
    $storage.Add((Get-DirectoryMeasurement -Name 'KnownTemporary' -Path $path))
}

$recommendations = [Collections.Generic.List[string]]::new()
if ($routerProcesses.Count -gt 0 -and $officialProcesses.Count -gt 0) {
    $recommendations.Add('For normal use, run the router instead of the official Codex desktop at the same time. Concurrent use is supported for qualification but duplicates the Electron and app-server cost; close either app manually if it is not needed.')
}
elseif ($routerProcesses.Count -eq 0) {
    $recommendations.Add('The router is stopped. This is safe; start it from its own shortcut only when you want multi-subscription routing.')
}
if ($port.Listening -and -not $port.OwnedByRouter) {
    $recommendations.Add("Control port $($port.Port) is listening but its owner is not verified under the router application path. Do not send the control token to it; close or investigate that process manually before starting the router.")
}
elseif (-not $port.Discovered) {
    $recommendations.Add('The control port was not found in launcher configuration or build metadata. Use -ControlPort only as an explicit diagnostic override; do not assume a fixed port.')
}
if ($null -ne $script:ProcessInventoryError) {
    $recommendations.Add('Windows process paths could not be inventoried. Process counts and port ownership are incomplete; rerun in a normal user PowerShell session and do not infer that the router is stopped.')
}
$recoverableBytes = [Int64](($storage | Where-Object { $_.Name -in @('Backups', 'FailedInstallation', 'KnownTemporary') } |
        Measure-Object -Property Bytes -Sum).Sum)
if ($recoverableBytes -ge 1GB) {
    $recommendations.Add(('Recoverable backups, failed builds, and known temporary folders use {0:N2} GiB. Review them with the lifecycle cleanup command in -WhatIf mode; never delete the active app or account state by hand.' -f ($recoverableBytes / 1GB)))
}
if ($accounts.Count -gt 0 -and @($accounts | Where-Object { $_.Enabled -and $_.Status -ne 'connected' -and $_.Status -ne 'unknown-router-stopped' }).Count -gt 0) {
    $recommendations.Add('At least one enabled account is not connected. Use account management to retry login or disable it before a normal session.')
}
$manifest = Get-SafeManifest
if (-not $manifest.Present) {
    $recommendations.Add('No router build manifest was found. Treat this layout as unauthenticated and reinstall from a reviewed source checkout.')
}

[double]$routerWorkingSetMB = 0
foreach ($process in $routerProcesses) { $routerWorkingSetMB += [double]$process.WorkingSetMB }
[double]$officialWorkingSetMB = 0
foreach ($process in $officialProcesses) { $officialWorkingSetMB += [double]$process.WorkingSetMB }
$report = [PSCustomObject]@{
    SchemaVersion = 1
    GeneratedAtUtc = [DateTime]::UtcNow.ToString('o')
    ReadOnly = $true
    ShareSafeByDefault = (-not $RevealPaths)
    Manifest = $manifest
    Processes = [PSCustomObject]@{
        InventorySource = if ($ProcessSnapshotPath) { 'snapshot' } else { 'windows' }
        InventoryStatus = if ($null -eq $script:ProcessInventoryError) { 'ok' } else { 'error' }
        InventoryError = $script:ProcessInventoryError
        Router = $routerProcesses
        OfficialCodex = $officialProcesses
        RouterWorkingSetMB = [Math]::Round($routerWorkingSetMB, 1)
        OfficialWorkingSetMB = [Math]::Round($officialWorkingSetMB, 1)
        SimultaneousDesktopApps = ($routerProcesses.Count -gt 0 -and $officialProcesses.Count -gt 0)
    }
    ControlPort = $port
    Accounts = [PSCustomObject]@{ Source = $accountSource; Count = $accounts.Count; Items = $accounts }
    Storage = @($storage)
    RedactedLogs = @(Get-RedactedLogExcerptList)
    Recommendations = @($recommendations)
}

if ($OutputFormat -eq 'Json') {
    $report | ConvertTo-Json -Depth 12
    return
}

Write-Output 'Codex Subscription Router doctor (read-only)'
Write-Output "Manifest: $($manifest.Present); router processes: $($routerProcesses.Count); official Codex processes: $($officialProcesses.Count)"
Write-Output "Control port: $($port.Address); listening=$($port.Listening); ownedByRouter=$($port.OwnedByRouter); health=$($port.Health)"
Write-Output "Accounts: $($accounts.Count) ($accountSource)"
foreach ($account in $accounts) {
    Write-Output ("  {0}: enabled={1}, status={2}, plan={3}, threads={4}" -f $account.Account, $account.Enabled, $account.Status, $account.PlanType, $account.ThreadCount)
}
Write-Output 'Storage:'
foreach ($entry in $storage) {
    Write-Output ("  {0}: {1:N3} GiB, files={2}, path={3}" -f $entry.Name, $entry.GiB, $entry.Files, $entry.Path)
}
Write-Output 'Recommendations:'
foreach ($recommendation in $recommendations) { Write-Output "  - $recommendation" }
if ($report.RedactedLogs.Count -gt 0) {
    Write-Output 'Redacted log excerpts:'
    foreach ($log in $report.RedactedLogs) {
        Write-Output "  $($log.Path)"
        foreach ($line in $log.Lines) { Write-Output "    $line" }
    }
}
