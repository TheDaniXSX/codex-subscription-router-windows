[CmdletBinding()]
[Diagnostics.CodeAnalysis.SuppressMessageAttribute(
    'PSAvoidUsingWriteHost',
    '',
    Justification = 'This executable smoke-test harness emits the retained fixture path to the interactive host.'
)]
param(
    [string]$RepositoryRoot,
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
        throw "Timed out waiting for the multiplexer response to: $line"
    }
    if ($null -eq $read.Result) {
        throw 'Multiplexer stdout closed before returning an RPC response.'
    }
    return ($read.Result | ConvertFrom-Json)
}

function Read-Event {
    param([Parameter(Mandatory)][string]$CodexHome)
    $path = Join-Path $CodexHome 'mock-events.jsonl'
    if (-not (Test-Path -LiteralPath $path)) {
        return @()
    }
    return @(Get-Content -LiteralPath $path | ForEach-Object { $_ | ConvertFrom-Json })
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

$repo = Resolve-RepositoryRoot $RepositoryRoot
$go = Resolve-GoCommand
$work = New-SafeSmokeDirectory -Prefix 'codex-router-smoke-integration'
$process = $null

try {
    $mux = Join-Path $work 'codex.exe'
    $mock = Join-Path $work 'mock-codex.exe'
    Invoke-NativeChecked -FilePath $go -ArgumentList @('build', '-trimpath', '-o', $mux, './cmd/codex-mux') -WorkingDirectory $repo
    Invoke-NativeChecked -FilePath $go -ArgumentList @('build', '-trimpath', '-o', $mock, './tests/windows/fixtures') -WorkingDirectory $repo

    $muxHome = Join-Path $work 'mux-state'
    $primaryHome = Join-Path $muxHome 'accounts\primary\codex-home'
    $secondaryHome = Join-Path $muxHome 'accounts\secondary\codex-home'
    Write-MockAccount -CodexHome $primaryHome -ID 'primary' -Email 'primary@example.invalid' -UsedPercent 10
    Write-MockAccount -CodexHome $secondaryHome -ID 'secondary' -Email 'secondary@example.invalid' -UsedPercent 40
    [void](New-Item -ItemType Directory -Path $muxHome -Force)

    @{
        version = 1
        accounts = @(
            @{ id = 'primary'; label = 'Primary'; codexHome = $primaryHome; enabled = $true; controller = $true; createdAt = 1 },
            @{ id = 'secondary'; label = 'Secondary'; codexHome = $secondaryHome; enabled = $true; controller = $false; createdAt = 2 }
        )
        threadOwner = @{}
    } | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath (Join-Path $muxHome 'state.json') -Encoding utf8NoBOM

    $token = ('ab' * 32)
    $port = Get-FreeTcpPort
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
    $start.Environment['CODEX_MUX_CONTROL_PORT'] = [string]$port
    $start.Environment['CODEX_MUX_UI_TESTS'] = '1'
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $start
    if (-not $process.Start()) {
        throw 'Could not start the smoke-test multiplexer.'
    }

    $initialize = Send-Rpc -Process $process -Message @{ id = 1; method = 'initialize'; params = @{ client = 'windows-smoke-test' } }
    Assert-Equal -Actual $initialize.id -Expected 1 -Message 'Initialization response ID changed'
    $process.StandardInput.WriteLine('{"method":"initialized"}')
    $process.StandardInput.Flush()

    $thread = Send-Rpc -Process $process -Message @{ id = 2; method = 'thread/start'; params = @{ cwd = $work } }
    Assert-Equal -Actual $thread.result.accountId -Expected 'primary' -Message 'Initial quota routing did not choose the expected account'
    $threadID = [string]$thread.result.thread.id
    Assert-Equal -Actual $threadID -Expected 'thread-primary' -Message 'Unexpected mock thread ID'

    $followUp = Send-Rpc -Process $process -Message @{ id = 3; method = 'turn/start'; params = @{ threadId = $threadID; input = @() } }
    Assert-Equal -Actual $followUp.result.accountId -Expected 'primary' -Message 'Sticky follow-up left its original account'
    Write-SmokePass 'two app-server processes start and sticky routing retains the owner'

    $plugin = Send-Rpc -Process $process -Message @{
        id = 4
        method = 'app/installed'
        params = @{ forceRefresh = $true; codexMuxAccountId = 'secondary' }
    }
    Assert-Equal -Actual $plugin.result.accountId -Expected 'secondary' -Message 'Scoped plugin request reached the wrong account'
    if ($plugin.result.params.PSObject.Properties.Name -contains 'codexMuxAccountId') {
        throw 'The private codexMuxAccountId marker leaked to the real app-server.'
    }
    Write-SmokePass 'plugin request is account-scoped and strips its private routing marker'

    Write-MockAccount -CodexHome $primaryHome -ID 'primary' -Email 'primary@example.invalid' -UsedPercent 100
    $failedOver = Send-Rpc -Process $process -Message @{ id = 5; method = 'turn/start'; params = @{ threadId = $threadID; input = @() } } -TimeoutSeconds 20
    Assert-Equal -Actual $failedOver.result.accountId -Expected 'secondary' -Message 'Depleted owner did not fail over to the available account'

    $persisted = Get-Content -Raw -LiteralPath (Join-Path $muxHome 'state.json') | ConvertFrom-Json
    Assert-Equal -Actual $persisted.threadOwner.$threadID -Expected 'secondary' -Message 'Failover owner was not persisted'
    $primaryEvents = Read-Event -CodexHome $primaryHome
    $secondaryEvents = Read-Event -CodexHome $secondaryHome
    if (-not ($primaryEvents | Where-Object method -EQ 'thread/read')) {
        throw 'Failover did not read resumable history from the source account.'
    }
    if (-not ($secondaryEvents | Where-Object method -EQ 'thread/resume')) {
        throw 'Failover did not resume history on the target account.'
    }
    foreach ($entry in @($primaryEvents + $secondaryEvents)) {
        Assert-Equal -Actual $entry.codexHome -Expected $entry.sqliteHome -Message "CODEX_HOME and CODEX_SQLITE_HOME differ for $($entry.accountId)"
        if ($entry.accountId -eq 'primary') {
            Assert-Equal -Actual $entry.codexHome -Expected $primaryHome -Message 'Primary child used the wrong isolated home'
        }
        elseif ($entry.accountId -eq 'secondary') {
            Assert-Equal -Actual $entry.codexHome -Expected $secondaryHome -Message 'Secondary child used the wrong isolated home'
        }
        if ($entry.controlTokenInEnv -or $entry.uiTestFlagInEnv) {
            throw "Router-only control or UI-test variables leaked into child $($entry.accountId)."
        }
    }
    Write-SmokePass 'failover persists the new owner; homes and child environments remain isolated'

    $headers = @{ 'X-Codex-Mux-Token' = $token }
    $controlBase = "http://127.0.0.1:$port"
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    $health = $null
    do {
        try {
            $health = Invoke-RestMethod -Method Get -Uri "$controlBase/v1/health" -TimeoutSec 1
        }
        catch {
            Start-Sleep -Milliseconds 100
        }
    } until (($null -ne $health -and $health.ok) -or [DateTime]::UtcNow -ge $deadline)
    if ($null -eq $health -or -not $health.ok) {
        throw 'The loopback control service did not become healthy.'
    }

    $missingToken = Invoke-WebRequest -Method Get -Uri "$controlBase/v1/accounts" -SkipHttpErrorCheck
    Assert-Equal -Actual $missingToken.StatusCode -Expected 401 -Message 'Private control route accepted a request without a token'
    $queryToken = Invoke-WebRequest -Method Get -Uri "$controlBase/v1/accounts?token=$token" -SkipHttpErrorCheck
    Assert-Equal -Actual $queryToken.StatusCode -Expected 401 -Message 'Control token was accepted from a query string'
    $hostileOriginHeaders = @{ 'X-Codex-Mux-Token' = $token; Origin = 'https://attacker.example' }
    $hostileOrigin = Invoke-WebRequest -Method Get -Uri "$controlBase/v1/accounts" -Headers $hostileOriginHeaders -SkipHttpErrorCheck
    Assert-Equal -Actual $hostileOrigin.StatusCode -Expected 403 -Message 'Control API accepted an unexpected browser origin'
    Write-SmokePass 'control API requires header auth, rejects URL tokens, and rejects hostile origins'

    foreach ($preview in @(
        @{ accountId = 'primary'; availableCount = 2 },
        @{ accountId = 'secondary'; availableCount = 1 }
    )) {
        $body = $preview | ConvertTo-Json -Compress
        $null = Invoke-RestMethod -Method Post -Uri "$controlBase/v1/test/rate-limit-resets" -Headers $headers -ContentType 'application/json' -Body $body
    }
    $beforePrimary = Invoke-RestMethod -Method Get -Uri "$controlBase/v1/accounts/primary/rate-limit-resets" -Headers $headers
    $beforeSecondary = Invoke-RestMethod -Method Get -Uri "$controlBase/v1/accounts/secondary/rate-limit-resets" -Headers $headers
    Assert-Equal -Actual $beforePrimary.available_count -Expected 2 -Message 'Primary reset preview count is wrong'
    Assert-Equal -Actual $beforeSecondary.available_count -Expected 1 -Message 'Secondary reset preview count is wrong'
    $redeem = @{ creditId = 'smoke-preview-credit'; redeemRequestId = [Guid]::NewGuid().ToString() } | ConvertTo-Json -Compress
    $redeemed = Invoke-RestMethod -Method Post -Uri "$controlBase/v1/accounts/primary/rate-limit-resets/consume" -Headers $headers -ContentType 'application/json' -Body $redeem
    Assert-Equal -Actual $redeemed.code -Expected 'reset' -Message 'Preview reset was not redeemed'
    $afterPrimary = Invoke-RestMethod -Method Get -Uri "$controlBase/v1/accounts/primary/rate-limit-resets" -Headers $headers
    $afterSecondary = Invoke-RestMethod -Method Get -Uri "$controlBase/v1/accounts/secondary/rate-limit-resets" -Headers $headers
    Assert-Equal -Actual $afterPrimary.available_count -Expected 1 -Message 'Selected account reset count did not decrement'
    Assert-Equal -Actual $afterSecondary.available_count -Expected 1 -Message 'Unselected account reset count changed'
    Write-SmokePass 'reset preview and redemption are isolated to the selected account'

    Write-MockAccount -CodexHome $secondaryHome -ID 'secondary' -Email 'secondary@example.invalid' -UsedPercent 100
    $depleted = Send-Rpc -Process $process -Message @{ id = 6; method = 'turn/start'; params = @{ threadId = $threadID; input = @() } } -TimeoutSeconds 20
    Assert-Equal -Actual $depleted.error.code -Expected -32026 -Message 'All-depleted pool did not return the combined quota error'
    Write-SmokePass 'all-depleted pool returns the combined failover error'
}
finally {
    if ($null -ne $process) {
        try { $process.StandardInput.Close() }
        catch { Write-Verbose "Synthetic stdin was already closed: $($_.Exception.Message)" }
        if (-not $process.WaitForExit(3000)) {
            $process.Kill($true)
            $process.WaitForExit()
        }
        if ($process.ExitCode -ne 0) {
            $stderr = $process.StandardError.ReadToEnd()
            if (-not [string]::IsNullOrWhiteSpace($stderr)) {
                Write-Warning "Smoke multiplexer stderr: $stderr"
            }
        }
        $process.Dispose()
    }
    if ($KeepArtifacts) {
        Write-Host "Smoke artifacts: $work"
    }
    else {
        Remove-SafeSmokeDirectory -Path $work
    }
}
