[CmdletBinding()]
[Diagnostics.CodeAnalysis.SuppressMessageAttribute(
    'PSAvoidUsingWriteHost',
    '',
    Justification = 'This executable qualification harness emits human-readable progress in CI.'
)]
[Diagnostics.CodeAnalysis.SuppressMessageAttribute(
    'PSUseShouldProcessForStateChangingFunctions',
    '',
    Justification = 'Only uniquely named processes created by this fixture are stopped.'
)]
param(
    [string]$RepositoryRoot,
    [ValidateRange(10, 10000)]
    [int]$RequestsPerCycle = 80,
    [ValidateRange(2, 20)]
    [int]$RestartCycles = 2,
    [ValidateRange(64, 4096)]
    [int]$MaxWorkingSetMB = 512,
    [ValidateRange(8, 4096)]
    [int]$MaxWorkingSetGrowthMB = 96,
    [ValidateRange(16, 10000)]
    [int]$MaxHandleGrowth = 128,
    [switch]$KeepArtifacts
)

. (Join-Path $PSScriptRoot 'Test-Helpers.ps1')

function Write-MockAccount {
    param(
        [Parameter(Mandatory)][string]$CodexHome,
        [Parameter(Mandatory)][string]$ID,
        [Parameter(Mandatory)][string]$Email,
        [Parameter(Mandatory)][double]$UsedPercent
    )

    [void](New-Item -ItemType Directory -Path $CodexHome -Force)
    @{
        id = $ID
        email = $Email
        usedPercent = $UsedPercent
    } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $CodexHome 'mock-account.json') -Encoding utf8NoBOM
}

function Get-FreeTcpPort {
    for ($attempt = 0; $attempt -lt 128; $attempt++) {
        $port = Get-Random -Minimum 49152 -Maximum 65536
        $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $port)
        try {
            $listener.Start()
            return $port
        }
        catch [Net.Sockets.SocketException] { continue }
        finally { $listener.Stop() }
    }
    throw 'Could not reserve a free high dynamic TCP port for the synthetic control server.'
}

function Send-Rpc {
    param(
        [Parameter(Mandatory)][Diagnostics.Process]$Process,
        [Parameter(Mandatory)]$Message,
        [int]$TimeoutSeconds = 15
    )

    $line = $Message | ConvertTo-Json -Depth 20 -Compress
    $Process.StandardInput.WriteLine($line)
    $Process.StandardInput.Flush()
    $read = $Process.StandardOutput.ReadLineAsync()
    if (-not $read.Wait([TimeSpan]::FromSeconds($TimeoutSeconds))) {
        throw "Timed out waiting for the synthetic multiplexer response to: $line"
    }
    if ($null -eq $read.Result) {
        throw 'Synthetic multiplexer stdout closed before returning an RPC response.'
    }
    return ($read.Result | ConvertFrom-Json)
}

function Get-ProcessTreeMetric {
    param([Parameter(Mandatory)][int]$RootProcessID)

    $rows = @(Get-CimInstance Win32_Process -ErrorAction Stop)
    $selected = [Collections.Generic.HashSet[int]]::new()
    [void]$selected.Add($RootProcessID)
    do {
        $changed = $false
        foreach ($row in $rows) {
            if ($selected.Contains([int]$row.ParentProcessId) -and -not $selected.Contains([int]$row.ProcessId)) {
                [void]$selected.Add([int]$row.ProcessId)
                $changed = $true
            }
        }
    } while ($changed)

    [int64]$workingSet = 0
    [int64]$privateMemory = 0
    [int64]$handles = 0
    [int]$live = 0
    foreach ($processID in $selected) {
        $process = Get-Process -Id $processID -ErrorAction SilentlyContinue
        if ($null -eq $process) { continue }
        $workingSet += [int64]$process.WorkingSet64
        $privateMemory += [int64]$process.PrivateMemorySize64
        $handles += [int64]$process.HandleCount
        $live++
    }
    return [pscustomobject]@{
        ProcessCount = $live
        WorkingSetBytes = $workingSet
        PrivateMemoryBytes = $privateMemory
        Handles = $handles
    }
}

function Get-SyntheticProcess {
    param([Parameter(Mandatory)][string[]]$ExecutablePaths)

    $expected = @($ExecutablePaths | ForEach-Object { [IO.Path]::GetFullPath($_) })
    return @(Get-CimInstance Win32_Process -ErrorAction Stop | Where-Object {
        $path = [string]$_.ExecutablePath
        if ([string]::IsNullOrWhiteSpace($path)) { return $false }
        foreach ($candidate in $expected) {
            if ($path.Equals($candidate, [StringComparison]::OrdinalIgnoreCase)) { return $true }
        }
        return $false
    })
}

function Stop-SyntheticMux {
    param([Diagnostics.Process]$Process)

    if ($null -eq $Process) { return }
    try { $Process.StandardInput.Close() }
    catch { Write-Verbose "Synthetic stdin was already closed: $($_.Exception.Message)" }
    if (-not $Process.WaitForExit(5000)) {
        # This process was created by this test from a uniquely named temporary path.
        $Process.Kill($true)
        $Process.WaitForExit()
    }
}

$repo = Resolve-RepositoryRoot $RepositoryRoot
$go = Resolve-GoCommand
$work = New-SafeSmokeDirectory -Prefix 'codex-router-smoke-soak'
$muxProcess = $null
$samples = [Collections.Generic.List[object]]::new()
$totalRequests = 0

try {
    $mux = Join-Path $work 'synthetic-codex-mux.exe'
    $mock = Join-Path $work 'synthetic-codex-app-server.exe'
    Invoke-NativeChecked -FilePath $go -ArgumentList @('build', '-trimpath', '-o', $mux, './cmd/codex-mux') -WorkingDirectory $repo
    Invoke-NativeChecked -FilePath $go -ArgumentList @('build', '-trimpath', '-o', $mock, './tests/windows/fixtures') -WorkingDirectory $repo

    $muxHome = Join-Path $work 'mux-state'
    $primaryHome = Join-Path $muxHome 'accounts\primary\codex-home'
    $secondaryHome = Join-Path $muxHome 'accounts\secondary\codex-home'
    Write-MockAccount -CodexHome $primaryHome -ID 'primary' -Email 'primary@example.invalid' -UsedPercent 10
    Write-MockAccount -CodexHome $secondaryHome -ID 'secondary' -Email 'secondary@example.invalid' -UsedPercent 20
    [void](New-Item -ItemType Directory -Path $muxHome -Force)
    @{
        version = 1
        accounts = @(
            @{ id = 'primary'; label = 'Primary'; codexHome = $primaryHome; enabled = $true; controller = $true; createdAt = 1 },
            @{ id = 'secondary'; label = 'Secondary'; codexHome = $secondaryHome; enabled = $true; controller = $false; createdAt = 2 }
        )
        threadOwner = @{}
    } | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath (Join-Path $muxHome 'state.json') -Encoding utf8NoBOM

    for ($cycle = 1; $cycle -le $RestartCycles; $cycle++) {
        $token = ([Guid]::NewGuid().ToString('N') + [Guid]::NewGuid().ToString('N'))
        $start = [Diagnostics.ProcessStartInfo]::new()
        $start.FileName = $mux
        $start.ArgumentList.Add('app-server')
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardInput = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $start.Environment['CODEX_MUX_REAL_CODEX'] = $mock
        $start.Environment['CODEX_MUX_HOME'] = $muxHome
        $start.Environment['CODEX_HOME'] = $primaryHome
        $start.Environment['CODEX_MUX_CONTROL_TOKEN'] = $token
        $start.Environment['CODEX_MUX_CONTROL_PORT'] = [string](Get-FreeTcpPort)
        $muxProcess = [Diagnostics.Process]::new()
        $muxProcess.StartInfo = $start
        if (-not $muxProcess.Start()) { throw 'Could not start the synthetic multiplexer.' }

        $initialize = Send-Rpc -Process $muxProcess -Message @{ id = 1; method = 'initialize'; params = @{ client = 'release-qualification-soak' } }
        Assert-Equal -Actual $initialize.id -Expected 1 -Message "Cycle $cycle initialization response changed"
        $muxProcess.StandardInput.WriteLine('{"method":"initialized"}')
        $muxProcess.StandardInput.Flush()

        $primaryThread = Send-Rpc -Process $muxProcess -Message @{ id = 2; method = 'thread/start'; params = @{ cwd = $work } }
        $threadID = [string]$primaryThread.result.thread.id
        if ([string]::IsNullOrWhiteSpace($threadID)) { throw "Cycle $cycle did not create a synthetic thread." }
        $secondaryProbe = Send-Rpc -Process $muxProcess -Message @{
            id = 3; method = 'app/list'; params = @{ codexMuxAccountId = 'secondary' }
        }
        Assert-Equal -Actual $secondaryProbe.result.accountId -Expected 'secondary' -Message "Cycle $cycle did not reach the secondary fake account"

        Start-Sleep -Milliseconds 150
        $baseline = Get-ProcessTreeMetric -RootProcessID $muxProcess.Id
        $samples.Add([pscustomobject]@{ Cycle = $cycle; Point = 0; Metrics = $baseline })

        for ($requestIndex = 1; $requestIndex -le $RequestsPerCycle; $requestIndex++) {
            $accountID = if (($requestIndex % 2) -eq 0) { 'primary' } else { 'secondary' }
            $response = Send-Rpc -Process $muxProcess -Message @{
                id = 1000 + $requestIndex
                method = 'app/list'
                params = @{ forceRefresh = (($requestIndex % 7) -eq 0); codexMuxAccountId = $accountID }
            }
            Assert-Equal -Actual $response.result.accountId -Expected $accountID -Message "Cycle $cycle request $requestIndex reached the wrong fake account"
            $totalRequests++
            if (($requestIndex % 10) -eq 0 -or $requestIndex -eq $RequestsPerCycle) {
                $samples.Add([pscustomobject]@{
                    Cycle = $cycle
                    Point = $requestIndex
                    Metrics = (Get-ProcessTreeMetric -RootProcessID $muxProcess.Id)
                })
            }
        }

        $final = Get-ProcessTreeMetric -RootProcessID $muxProcess.Id
        $samples.Add([pscustomobject]@{ Cycle = $cycle; Point = $RequestsPerCycle + 1; Metrics = $final })
        Stop-SyntheticMux -Process $muxProcess
        $muxProcess.Dispose()
        $muxProcess = $null

        $deadline = [DateTime]::UtcNow.AddSeconds(5)
        do {
            $leftovers = @(Get-SyntheticProcess -ExecutablePaths @($mux, $mock))
            if ($leftovers.Count -eq 0) { break }
            Start-Sleep -Milliseconds 100
        } while ([DateTime]::UtcNow -lt $deadline)
        if ($leftovers.Count -ne 0) {
            throw "Synthetic process tree survived restart cycle ${cycle}: $($leftovers.ProcessId -join ', ')"
        }
    }

    $allMetrics = @($samples | ForEach-Object Metrics)
    $baselineMetrics = @($samples | Where-Object Point -EQ 0 | ForEach-Object Metrics)
    $finalMetrics = @($samples | Where-Object { $_.Point -eq ($RequestsPerCycle + 1) } | ForEach-Object Metrics)
    $peakWorkingSet = [int64](($allMetrics | Measure-Object WorkingSetBytes -Maximum).Maximum)
    $peakPrivateMemory = [int64](($allMetrics | Measure-Object PrivateMemoryBytes -Maximum).Maximum)
    $peakHandles = [int64](($allMetrics | Measure-Object Handles -Maximum).Maximum)
    $peakProcesses = [int](($allMetrics | Measure-Object ProcessCount -Maximum).Maximum)
    $maxWorkingSetGrowth = [int64]0
    $measuredMaxHandleGrowth = [int64]0
    for ($index = 0; $index -lt $baselineMetrics.Count; $index++) {
        $maxWorkingSetGrowth = [Math]::Max($maxWorkingSetGrowth, [int64]$finalMetrics[$index].WorkingSetBytes - [int64]$baselineMetrics[$index].WorkingSetBytes)
        $measuredMaxHandleGrowth = [Math]::Max($measuredMaxHandleGrowth, [int64]$finalMetrics[$index].Handles - [int64]$baselineMetrics[$index].Handles)
    }

    if ($peakWorkingSet -gt ([int64]$MaxWorkingSetMB * 1MB)) {
        throw "Synthetic process tree exceeded the working-set ceiling: $([Math]::Round($peakWorkingSet / 1MB, 2)) MiB > $MaxWorkingSetMB MiB."
    }
    if ($maxWorkingSetGrowth -gt ([int64]$MaxWorkingSetGrowthMB * 1MB)) {
        throw "Synthetic process tree working set grew excessively within a cycle: $([Math]::Round($maxWorkingSetGrowth / 1MB, 2)) MiB > $MaxWorkingSetGrowthMB MiB."
    }
    if ($measuredMaxHandleGrowth -gt $MaxHandleGrowth) {
        throw "Synthetic process tree leaked too many handles within a cycle: $measuredMaxHandleGrowth > $MaxHandleGrowth."
    }

    Write-SmokePass "$RestartCycles synthetic start/stop cycles and $totalRequests account-scoped requests completed without orphan processes"
    [pscustomobject][ordered]@{
        Passed = $true
        SyntheticOnly = $true
        RestartCycles = $RestartCycles
        Requests = $totalRequests
        PeakProcessCount = $peakProcesses
        PeakWorkingSetMB = [Math]::Round($peakWorkingSet / 1MB, 2)
        PeakPrivateMemoryMB = [Math]::Round($peakPrivateMemory / 1MB, 2)
        PeakHandles = $peakHandles
        MaxCycleWorkingSetGrowthMB = [Math]::Round($maxWorkingSetGrowth / 1MB, 2)
        MaxCycleHandleGrowth = $measuredMaxHandleGrowth
        RemainingSyntheticProcesses = 0
    }
}
finally {
    if ($null -ne $muxProcess) {
        Stop-SyntheticMux -Process $muxProcess
        $muxProcess.Dispose()
    }
    if ($KeepArtifacts) {
        Write-Host "Synthetic soak artifacts: $work"
    }
    else {
        Remove-SafeSmokeDirectory -Path $work
    }
}
