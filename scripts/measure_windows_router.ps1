#Requires -Version 5.1

<#
.SYNOPSIS
Measures router memory, handle, and process-count stability without changing it.

.DESCRIPTION
Live mode samples only processes whose executable path is AppRoot or one of its
children. It does not launch, stop, suspend, or attach a debugger to any process.

Fixture mode analyzes synthetic snapshots with the same code used for live data,
which keeps the release gate deterministic and credential-free.

.EXAMPLE
./scripts/measure_windows_router.ps1 -DurationSeconds 300 -IntervalSeconds 5

.EXAMPLE
./scripts/measure_windows_router.ps1 -FixturePath ./tests/windows/fixtures/diagnostics/soak-pass.json -OutputFormat Json
#>

[CmdletBinding()]
param(
    [Parameter()]
    [string]$AppRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),

    [Parameter()]
    [ValidateRange(5, 86400)]
    [int]$DurationSeconds = 60,

    [Parameter()]
    [ValidateRange(1, 300)]
    [int]$IntervalSeconds = 5,

    [Parameter()]
    [string]$FixturePath,

    [Parameter()]
    [ValidateRange(0, 1048576)]
    [double]$MaxWorkingSetGrowthMB = 256,

    [Parameter()]
    [ValidateRange(0, 1048576)]
    [double]$MaxPrivateMemoryGrowthMB = 256,

    [Parameter()]
    [ValidateRange(0, 10000000)]
    [Int64]$MaxHandleGrowth = 1000,

    [Parameter()]
    [ValidateRange(0, 10000)]
    [int]$MaxProcessGrowth = 4,

    [Parameter()]
    [ValidateSet('Text', 'Json')]
    [string]$OutputFormat = 'Text',

    [Parameter()]
    [switch]$RevealPaths,

    [Parameter()]
    [switch]$EnforceThresholds
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Resolve-SoakPath {
    param([Parameter(Mandatory = $true)][string]$Path)
    return [IO.Path]::GetFullPath([Environment]::ExpandEnvironmentVariables($Path)).TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    )
}

function Test-SoakPathWithin {
    param(
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$Parent
    )
    try {
        $child = Resolve-SoakPath $Candidate
        $root = Resolve-SoakPath $Parent
    }
    catch { return $false }
    return $child.Equals($root, [StringComparison]::OrdinalIgnoreCase) -or
        $child.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)
}

function Get-SoakProperty {
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

function Get-LiveSoakProcessList {
    try {
        return @(Get-CimInstance -ClassName Win32_Process `
                -Property ProcessId, Name, ExecutablePath, WorkingSetSize, PrivatePageCount, HandleCount `
                -ErrorAction Stop |
                Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_.ExecutablePath) })
    }
    catch {
        throw "Could not read process counters without elevation: $($_.Exception.Message)"
    }
}

function ConvertTo-SoakSample {
    param(
        [Parameter(Mandatory = $true)][object[]]$Processes,
        [Parameter(Mandatory = $true)][DateTime]$Timestamp,
        [Parameter(Mandatory = $true)][string]$Root
    )

    $matched = @($Processes | Where-Object {
            Test-SoakPathWithin -Candidate ([string](Get-SoakProperty $_ 'ExecutablePath' '')) -Parent $Root
        })
    [Int64]$workingSet = 0
    [Int64]$privateMemory = 0
    [Int64]$handles = 0
    foreach ($process in $matched) {
        $workingSet += [Int64](Get-SoakProperty $process 'WorkingSetSize' 0)
        $privateMemory += [Int64](Get-SoakProperty $process 'PrivatePageCount' 0)
        $handles += [Int64](Get-SoakProperty $process 'HandleCount' 0)
    }
    return [PSCustomObject]@{
        TimestampUtc = $Timestamp.ToUniversalTime().ToString('o')
        ProcessCount = $matched.Count
        WorkingSetBytes = $workingSet
        PrivateMemoryBytes = $privateMemory
        HandleCount = $handles
    }
}

function Get-LinearSlopePerHour {
    param(
        [Parameter(Mandatory = $true)][object[]]$Samples,
        [Parameter(Mandatory = $true)][string]$Property
    )
    if ($Samples.Count -lt 2) { return 0.0 }
    $origin = [DateTimeOffset]::Parse([string]$Samples[0].TimestampUtc)
    [double]$sumX = 0
    [double]$sumY = 0
    [double]$sumXY = 0
    [double]$sumXX = 0
    foreach ($sample in $Samples) {
        $timestamp = [DateTimeOffset]::Parse([string]$sample.TimestampUtc)
        $x = ($timestamp - $origin).TotalHours
        $y = [double]$sample.$Property
        $sumX += $x
        $sumY += $y
        $sumXY += $x * $y
        $sumXX += $x * $x
    }
    $count = [double]$Samples.Count
    $denominator = ($count * $sumXX) - ($sumX * $sumX)
    if ([Math]::Abs($denominator) -lt 0.000000001) { return 0.0 }
    return (($count * $sumXY) - ($sumX * $sumY)) / $denominator
}

$resolvedRoot = Resolve-SoakPath $AppRoot
$samples = [Collections.Generic.List[object]]::new()
$mode = 'live'
$startedAt = [DateTime]::UtcNow

if (-not [string]::IsNullOrWhiteSpace($FixturePath)) {
    $mode = 'fixture'
    $fixture = Get-Content -Raw -LiteralPath $FixturePath | ConvertFrom-Json
    if ([int](Get-SoakProperty $fixture 'schemaVersion' 0) -ne 1) {
        throw 'The soak fixture must use schemaVersion 1.'
    }
    $fixtureSamples = @(Get-SoakProperty $fixture 'samples' @())
    if ($fixtureSamples.Count -eq 0) { throw 'The soak fixture contains no samples.' }
    foreach ($fixtureSample in $fixtureSamples) {
        $timestamp = [DateTime]::Parse(
            [string](Get-SoakProperty $fixtureSample 'timestampUtc' ''),
            [Globalization.CultureInfo]::InvariantCulture,
            [Globalization.DateTimeStyles]::RoundtripKind
        )
        $processes = @(Get-SoakProperty $fixtureSample 'processes' @())
        $samples.Add((ConvertTo-SoakSample -Processes $processes -Timestamp $timestamp -Root $resolvedRoot))
    }
    $startedAt = [DateTime]::Parse([string]$samples[0].TimestampUtc).ToUniversalTime()
}
else {
    if ($DurationSeconds -lt $IntervalSeconds) {
        throw 'DurationSeconds must be greater than or equal to IntervalSeconds.'
    }
    $deadline = [DateTime]::UtcNow.AddSeconds($DurationSeconds)
    while ($true) {
        $now = [DateTime]::UtcNow
        $samples.Add((ConvertTo-SoakSample -Processes @(Get-LiveSoakProcessList) -Timestamp $now -Root $resolvedRoot))
        if ($now.AddSeconds($IntervalSeconds) -gt $deadline) { break }
        Start-Sleep -Seconds $IntervalSeconds
    }
}

$orderedSamples = @($samples | Sort-Object { [DateTimeOffset]::Parse([string]$_.TimestampUtc) })
$baseline = $orderedSamples[0]
$final = $orderedSamples[$orderedSamples.Count - 1]
$peakWorkingSet = [Int64](($orderedSamples | Measure-Object WorkingSetBytes -Maximum).Maximum)
$peakPrivateMemory = [Int64](($orderedSamples | Measure-Object PrivateMemoryBytes -Maximum).Maximum)
$peakHandles = [Int64](($orderedSamples | Measure-Object HandleCount -Maximum).Maximum)
$peakProcesses = [int](($orderedSamples | Measure-Object ProcessCount -Maximum).Maximum)
$workingSetGrowthMB = [Math]::Round(([Int64]$final.WorkingSetBytes - [Int64]$baseline.WorkingSetBytes) / 1MB, 2)
$privateGrowthMB = [Math]::Round(([Int64]$final.PrivateMemoryBytes - [Int64]$baseline.PrivateMemoryBytes) / 1MB, 2)
$handleGrowth = [Int64]$final.HandleCount - [Int64]$baseline.HandleCount
$processGrowth = [int]$final.ProcessCount - [int]$baseline.ProcessCount

$violations = [Collections.Generic.List[string]]::new()
if ($workingSetGrowthMB -gt $MaxWorkingSetGrowthMB) {
    $violations.Add("Working-set growth $workingSetGrowthMB MB exceeds $MaxWorkingSetGrowthMB MB.")
}
if ($privateGrowthMB -gt $MaxPrivateMemoryGrowthMB) {
    $violations.Add("Private-memory growth $privateGrowthMB MB exceeds $MaxPrivateMemoryGrowthMB MB.")
}
if ($handleGrowth -gt $MaxHandleGrowth) {
    $violations.Add("Handle growth $handleGrowth exceeds $MaxHandleGrowth.")
}
if ($processGrowth -gt $MaxProcessGrowth) {
    $violations.Add("Process growth $processGrowth exceeds $MaxProcessGrowth.")
}
$everObserved = @($orderedSamples | Where-Object { $_.ProcessCount -gt 0 }).Count -gt 0
$status = if (-not $everObserved) { 'NoProcesses' } elseif ($violations.Count -gt 0) { 'ThresholdExceeded' } else { 'Pass' }
$finishedAt = [DateTime]::Parse([string]$orderedSamples[$orderedSamples.Count - 1].TimestampUtc).ToUniversalTime()

$report = [PSCustomObject]@{
    SchemaVersion = 1
    Mode = $mode
    ReadOnly = $true
    AppRoot = if ($RevealPaths) { $resolvedRoot } else { '<APP>' }
    StartedAtUtc = $startedAt.ToString('o')
    FinishedAtUtc = $finishedAt.ToString('o')
    DurationSeconds = [Math]::Round(($finishedAt - $startedAt).TotalSeconds, 1)
    SampleCount = $orderedSamples.Count
    Status = $status
    Summary = [PSCustomObject]@{
        BaselineProcessCount = [int]$baseline.ProcessCount
        PeakProcessCount = $peakProcesses
        FinalProcessCount = [int]$final.ProcessCount
        ProcessGrowth = $processGrowth
        BaselineWorkingSetMB = [Math]::Round([Int64]$baseline.WorkingSetBytes / 1MB, 2)
        PeakWorkingSetMB = [Math]::Round($peakWorkingSet / 1MB, 2)
        FinalWorkingSetMB = [Math]::Round([Int64]$final.WorkingSetBytes / 1MB, 2)
        WorkingSetGrowthMB = $workingSetGrowthMB
        WorkingSetSlopeMBPerHour = [Math]::Round((Get-LinearSlopePerHour $orderedSamples 'WorkingSetBytes') / 1MB, 2)
        BaselinePrivateMemoryMB = [Math]::Round([Int64]$baseline.PrivateMemoryBytes / 1MB, 2)
        PeakPrivateMemoryMB = [Math]::Round($peakPrivateMemory / 1MB, 2)
        FinalPrivateMemoryMB = [Math]::Round([Int64]$final.PrivateMemoryBytes / 1MB, 2)
        PrivateMemoryGrowthMB = $privateGrowthMB
        PrivateMemorySlopeMBPerHour = [Math]::Round((Get-LinearSlopePerHour $orderedSamples 'PrivateMemoryBytes') / 1MB, 2)
        BaselineHandles = [Int64]$baseline.HandleCount
        PeakHandles = $peakHandles
        FinalHandles = [Int64]$final.HandleCount
        HandleGrowth = $handleGrowth
        HandleSlopePerHour = [Math]::Round((Get-LinearSlopePerHour $orderedSamples 'HandleCount'), 2)
    }
    Thresholds = [PSCustomObject]@{
        MaxWorkingSetGrowthMB = $MaxWorkingSetGrowthMB
        MaxPrivateMemoryGrowthMB = $MaxPrivateMemoryGrowthMB
        MaxHandleGrowth = $MaxHandleGrowth
        MaxProcessGrowth = $MaxProcessGrowth
    }
    Violations = @($violations)
    Samples = $orderedSamples
}

if ($OutputFormat -eq 'Json') {
    $report | ConvertTo-Json -Depth 8
}
else {
    Write-Output 'Codex Subscription Router resource soak (read-only)'
    Write-Output "Mode: $mode; samples=$($report.SampleCount); duration=$($report.DurationSeconds)s; status=$status"
    Write-Output ("Processes: baseline={0}, peak={1}, final={2}, growth={3}" -f $baseline.ProcessCount, $peakProcesses, $final.ProcessCount, $processGrowth)
    Write-Output ("Working set: baseline={0:N2} MB, peak={1:N2} MB, final={2:N2} MB, growth={3:N2} MB" -f $report.Summary.BaselineWorkingSetMB, $report.Summary.PeakWorkingSetMB, $report.Summary.FinalWorkingSetMB, $workingSetGrowthMB)
    Write-Output ("Private memory: baseline={0:N2} MB, peak={1:N2} MB, final={2:N2} MB, growth={3:N2} MB" -f $report.Summary.BaselinePrivateMemoryMB, $report.Summary.PeakPrivateMemoryMB, $report.Summary.FinalPrivateMemoryMB, $privateGrowthMB)
    Write-Output ("Handles: baseline={0}, peak={1}, final={2}, growth={3}" -f $baseline.HandleCount, $peakHandles, $final.HandleCount, $handleGrowth)
    foreach ($violation in $violations) { Write-Output "  - $violation" }
}

if ($EnforceThresholds -and $status -ne 'Pass') {
    exit 2
}
