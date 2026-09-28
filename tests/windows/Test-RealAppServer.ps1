#Requires -Version 7.0

<#
.SYNOPSIS
Runs a narrowly scoped, opt-in smoke against the real Codex CLI in a built router.

.DESCRIPTION
Starts resources\codex.exe (the router mux) twice with fresh temporary homes:
first with all mux context absent to exercise auxiliary/pass-through behavior,
then with a fresh state root, control port/token, and request-spending enabled.
Only initialize, initialized, account/read, and thread/list are sent. No login,
inference, reset, or user-profile state is used. RPC output, stderr, and secrets
are never printed. The process receives a small allowlist of operating-system
environment variables plus fresh test-only paths, so inherited auth/config
variables cannot leak into either run.

This script is deliberately excluded from unattended CI. It never launches the
desktop launcher or Electron UI, and it only terminates the process tree it
started if EOF does not stop it within the bounded shutdown interval.

.EXAMPLE
pwsh -NoProfile -File .\tests\windows\Test-RealAppServer.ps1 `
  -AppRoot 'C:\path\to\staged\Codex Subscription Router' `
  -ConfirmRealAppServerSmoke
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$AppRoot,

    [switch]$ConfirmRealAppServerSmoke,

    [switch]$KeepArtifacts,

    [ValidateRange(5, 120)]
    [int]$RpcTimeoutSeconds = 30,

    [ValidateRange(1, 60)]
    [int]$ShutdownTimeoutSeconds = 10
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not $ConfirmRealAppServerSmoke) {
    throw 'This real-app-server smoke is opt-in; pass -ConfirmRealAppServerSmoke explicitly.'
}

. (Join-Path $PSScriptRoot 'Test-Helpers.ps1')

function Get-Sha256Hex {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Resolve-BuiltAppRoot {
    param([Parameter(Mandatory = $true)][string]$Path)

    $resolved = (Resolve-Path -LiteralPath $Path -ErrorAction Stop).Path
    $rootItem = Get-Item -LiteralPath $resolved -Force
    if (-not $rootItem.PSIsContainer -or
        ($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw 'AppRoot must be a real, non-reparse directory containing a built router.'
    }

    $manifestPath = Join-Path $resolved 'codex-mux-build.json'
    $muxPath = Join-Path $resolved 'resources\codex.exe'
    $realCodexPath = Join-Path $resolved 'resources\codex.real.exe'
    foreach ($required in @($manifestPath, $muxPath, $realCodexPath)) {
        if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
            throw 'AppRoot is missing the router manifest or a required Codex executable.'
        }
        $item = Get-Item -LiteralPath $required -Force
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw 'AppRoot contains a reparse-point executable or manifest; refusing to launch it.'
        }
    }

    try {
        $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 |
            ConvertFrom-Json -Depth 32
    }
    catch {
        throw 'AppRoot router manifest is invalid JSON.'
    }
    if ([int]$manifest.schemaVersion -ne 2 -or
        [string]::IsNullOrWhiteSpace([string]$manifest.sourceVersion) -or
        [string]$manifest.muxSha256 -notmatch '^[0-9a-fA-F]{64}$' -or
        [string]$manifest.sourceCodexSha256 -notmatch '^[0-9a-fA-F]{64}$') {
        throw 'AppRoot router manifest lacks the required build identity and hashes.'
    }
    if ((Get-Sha256Hex -Path $muxPath) -ne ([string]$manifest.muxSha256).ToLowerInvariant() -or
        (Get-Sha256Hex -Path $realCodexPath) -ne ([string]$manifest.sourceCodexSha256).ToLowerInvariant()) {
        throw 'AppRoot executable hashes do not match its build manifest.'
    }

    return [PSCustomObject]@{
        Root = $resolved
        MuxPath = $muxPath
        RealCodexPath = $realCodexPath
        SourceVersion = [string]$manifest.sourceVersion
    }
}

function New-ControlToken {
    $bytes = [byte[]]::new(32)
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
        return (($bytes | ForEach-Object { $_.ToString('x2') }) -join '')
    }
    finally {
        [Array]::Clear($bytes, 0, $bytes.Length)
        $generator.Dispose()
    }
}

function Get-FreeControlPort {
    # Windows may have a customized ephemeral range; port 0 need not satisfy
    # the router's high-port policy. Probe a bounded number in that range.
    for ($attempt = 0; $attempt -lt 20; $attempt++) {
        $port = [Security.Cryptography.RandomNumberGenerator]::GetInt32(49152, 65536)
        $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $port)
        try {
            $listener.Start()
            return $port
        }
        catch [Net.Sockets.SocketException] { }
        finally { $listener.Stop() }
    }
    throw 'No free port found in the router control range after 20 attempts.'
}

function Assert-PathInsideSmokeRoot {
    param(
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$SmokeRoot
    )
    $root = [IO.Path]::GetFullPath($SmokeRoot).TrimEnd([IO.Path]::DirectorySeparatorChar) +
        [IO.Path]::DirectorySeparatorChar
    $candidate = [IO.Path]::GetFullPath($Candidate)
    if (-not $candidate.StartsWith($root, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'A real-app-server smoke path resolved outside its validated temporary root.'
    }
}

function New-IsolatedProcessStartInfo {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory,
        [Parameter(Mandatory = $true)][string]$CodexHome,
        [Parameter(Mandatory = $true)][ValidateSet('passthrough', 'router')][string]$Mode,
        [string]$StateRoot,
        [int]$ControlPort,
        [string]$ControlToken
    )

    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $Executable
    $startInfo.WorkingDirectory = $WorkingDirectory
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $utf8 = [Text.UTF8Encoding]::new($false)
    $startInfo.StandardInputEncoding = $utf8
    $startInfo.StandardOutputEncoding = $utf8
    $startInfo.StandardErrorEncoding = $utf8

    # Start from an empty environment and copy only OS/runtime variables. In
    # particular this drops OPENAI_API_KEY, CODEX_API_KEY, OPENAI_BASE_URL,
    # inherited CODEX_HOME, every CODEX_MUX_* setting, and auth/context vars.
    $startInfo.Environment.Clear()
    foreach ($name in @(
        'SystemRoot', 'windir', 'SystemDrive', 'PATH', 'PATHEXT', 'COMSPEC',
        'OS', 'PROCESSOR_ARCHITECTURE', 'PROCESSOR_IDENTIFIER', 'NUMBER_OF_PROCESSORS'
    )) {
        $value = [Environment]::GetEnvironmentVariable($name, 'Process')
        if ($null -ne $value) {
            $startInfo.Environment[$name] = $value
        }
    }

    $profileRoot = Join-Path $WorkingDirectory 'user-profile'
    $roaming = Join-Path $profileRoot 'AppData\Roaming'
    $local = Join-Path $profileRoot 'AppData\Local'
    $temp = Join-Path $profileRoot 'Temp'
    foreach ($directory in @($profileRoot, $roaming, $local, $temp, $CodexHome)) {
        [void](New-Item -ItemType Directory -Path $directory -Force)
    }
    $drive = [IO.Path]::GetPathRoot($profileRoot).TrimEnd('\')
    $homePath = $profileRoot.Substring($drive.Length)
    foreach ($entry in @{
        USERPROFILE = $profileRoot
        HOME = $profileRoot
        HOMEDRIVE = $drive
        HOMEPATH = $homePath
        APPDATA = $roaming
        LOCALAPPDATA = $local
        TEMP = $temp
        TMP = $temp
        CODEX_HOME = $CodexHome
        CODEX_SQLITE_HOME = $CodexHome
    }.GetEnumerator()) {
        $startInfo.Environment[$entry.Key] = $entry.Value
    }

    if ($Mode -eq 'router') {
        foreach ($entry in @{
            CODEX_MUX_HOME = $StateRoot
            CODEX_MUX_STATE_ROOT = $StateRoot
            CODEX_MUX_CONTROL_PORT = [string]$ControlPort
            CODEX_MUX_CONTROL_TOKEN = $ControlToken
            CODEX_MUX_REQUEST_SPENDING = '1'
        }.GetEnumerator()) {
            $startInfo.Environment[$entry.Key] = $entry.Value
        }
    }

    $credentialPattern = '^(OPENAI_|AZURE_OPENAI_|ANTHROPIC_|GOOGLE_API_KEY$|GEMINI_API_KEY$|OAI_API_KEY$|CODEX_(API_KEY|ACCESS_TOKEN|REFRESH_TOKEN|AUTH|OAUTH|SESSION|ACCOUNT))'
    $credentialKeys = @($startInfo.Environment.Keys | Where-Object { [string]$_ -match $credentialPattern })
    if ($credentialKeys.Count -ne 0) {
        throw 'A credential-bearing environment variable would reach the real app-server.'
    }
    $muxKeys = @($startInfo.Environment.Keys | Where-Object { [string]$_ -like 'CODEX_MUX_*' })
    if ($Mode -eq 'passthrough' -and $muxKeys.Count -ne 0) {
        throw 'The passthrough smoke must not inherit any router environment.'
    }
    if ($Mode -eq 'router' -and $muxKeys.Count -ne 5) {
        throw 'The router smoke environment is missing required isolated router settings.'
    }
    return $startInfo
}

function Send-SmokeRpc {
    param(
        [Parameter(Mandatory = $true)][Diagnostics.Process]$Process,
        [Parameter(Mandatory = $true)][int]$ID,
        [Parameter(Mandatory = $true)][string]$Method,
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$Params,
        [Parameter(Mandatory = $true)][int]$TimeoutSeconds
    )

    $request = @{ id = $ID; method = $Method; params = $Params } |
        ConvertTo-Json -Depth 20 -Compress
    try {
        $Process.StandardInput.WriteLine($request)
        $Process.StandardInput.Flush()
    }
    catch {
        throw "Could not send the bounded app-server RPC '$Method'."
    }

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($Process.HasExited) {
            throw "The app-server exited before replying to RPC '$Method'."
        }
        $read = $Process.StandardOutput.ReadLineAsync()
        $remaining = $deadline - [DateTime]::UtcNow
        if ($remaining.TotalMilliseconds -le 0 -or -not $read.Wait($remaining)) {
            throw "RPC '$Method' exceeded its ${TimeoutSeconds}s deadline."
        }
        $line = $read.Result
        if ($null -eq $line) {
            throw "The app-server closed stdout before replying to RPC '$Method'."
        }
        try {
            $message = ConvertFrom-Json -InputObject $line -AsHashtable -Depth 64 -ErrorAction Stop
        }
        catch {
            # Ignore non-protocol startup text without exposing it in output.
            continue
        }
        if ($message -isnot [System.Collections.IDictionary] -or
            -not $message.Contains('id') -or [int64]$message.id -ne $ID) {
            continue
        }
        if ($message.Contains('error')) {
            $code = if ($message.error -is [System.Collections.IDictionary] -and
                $message.error.Contains('code')) { [string]$message.error.code } else { 'unknown' }
            throw "App-server RPC '$Method' returned error code $code."
        }
        if (-not $message.Contains('result')) {
            throw "App-server RPC '$Method' returned neither a result nor a JSON-RPC error."
        }
        return $message.result
    }
    throw "RPC '$Method' exceeded its ${TimeoutSeconds}s deadline."
}

function Send-SmokeNotification {
    param(
        [Parameter(Mandatory = $true)][Diagnostics.Process]$Process,
        [Parameter(Mandatory = $true)][string]$Method
    )
    try {
        $Process.StandardInput.WriteLine(
            (@{ method = $Method; params = @{} } | ConvertTo-Json -Depth 8 -Compress)
        )
        $Process.StandardInput.Flush()
    }
    catch {
        throw "Could not send app-server notification '$Method'."
    }
}

function Stop-SmokeProcessTree {
    param(
        [Parameter(Mandatory = $true)][Diagnostics.Process]$Process,
        [Parameter(Mandatory = $true)][int]$GraceSeconds
    )

    $forced = $false
    if (-not $Process.HasExited) {
        try { $Process.StandardInput.Close() } catch { }
        if (-not $Process.WaitForExit($GraceSeconds * 1000)) {
            # This is the only termination path: kill only the process started
            # by this script and its descendants, never matching names globally.
            try { $Process.Kill($true) } catch {
                if (-not $Process.HasExited) {
                    throw 'The isolated app-server process tree could not be stopped safely.'
                }
            }
            if (-not $Process.WaitForExit(10000)) {
                throw 'The isolated app-server process tree remained alive after termination.'
            }
            $forced = $true
        }
    }
    return $forced
}

function Invoke-RealAppServerMode {
    param(
        [Parameter(Mandatory = $true)]$Candidate,
        [Parameter(Mandatory = $true)][string]$SmokeRoot,
        [Parameter(Mandatory = $true)][ValidateSet('passthrough', 'router')][string]$Mode,
        [Parameter(Mandatory = $true)][int]$RpcTimeout,
        [Parameter(Mandatory = $true)][int]$ShutdownTimeout
    )

    $modeRoot = Join-Path $SmokeRoot $Mode
    $workingDirectory = Join-Path $modeRoot 'work'
    $codexHome = Join-Path $modeRoot 'codex-home'
    $stateRoot = Join-Path $modeRoot 'router-state'
    foreach ($path in @($modeRoot, $workingDirectory, $codexHome)) {
        Assert-PathInsideSmokeRoot -Candidate $path -SmokeRoot $SmokeRoot
        [void](New-Item -ItemType Directory -Path $path -Force)
    }
    if ($Mode -eq 'router') {
        Assert-PathInsideSmokeRoot -Candidate $stateRoot -SmokeRoot $SmokeRoot
        [void](New-Item -ItemType Directory -Path $stateRoot -Force)
    }

    $controlPort = 0
    $controlToken = $null
    if ($Mode -eq 'router') {
        $controlPort = Get-FreeControlPort
        $controlToken = New-ControlToken
    }
    $startInfo = New-IsolatedProcessStartInfo `
        -Executable $Candidate.MuxPath `
        -WorkingDirectory $workingDirectory `
        -CodexHome $codexHome `
        -Mode $Mode `
        -StateRoot $stateRoot `
        -ControlPort $controlPort `
        -ControlToken $controlToken
    $startInfo.ArgumentList.Add('app-server')

    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    $stderrTask = $null
    $processStarted = $false
    $forcedTermination = $false
    try {
        try {
            if (-not $process.Start()) {
                throw 'Process.Start returned false.'
            }
            $processStarted = $true
        }
        catch {
            throw "Could not start the isolated '$Mode' app-server process."
        }
        # Drain stderr silently so a verbose CLI cannot block on a full pipe.
        $stderrTask = $process.StandardError.ReadToEndAsync()

        $initialize = Send-SmokeRpc -Process $process -ID 1 -Method 'initialize' `
            -Params @{ clientInfo = @{ name = 'codex_router_real_appserver_smoke'; version = '1' }; capabilities = @{ experimentalApi = $true } } `
            -TimeoutSeconds $RpcTimeout
        if ($initialize -isnot [System.Collections.IDictionary]) {
            throw 'The app-server initialize result was not an object.'
        }
        Send-SmokeNotification -Process $process -Method 'initialized'

        $account = Send-SmokeRpc -Process $process -ID 2 -Method 'account/read' `
            -Params @{} -TimeoutSeconds $RpcTimeout
        if ($account -isnot [System.Collections.IDictionary] -or
            -not $account.Contains('account') -or $null -ne $account.account) {
            throw 'account/read was not unauthenticated; isolated state did not return account=null.'
        }

        $threads = Send-SmokeRpc -Process $process -ID 3 -Method 'thread/list' `
            -Params @{ limit = 1 } -TimeoutSeconds $RpcTimeout
        if ($threads -isnot [System.Collections.IDictionary] -or
            -not $threads.Contains('data') -or $threads.data -isnot [array] -or
            $threads.data.Count -ne 0) {
            throw 'thread/list(limit=1) did not return an empty data array for the fresh home.'
        }
    }
    finally {
        if ($processStarted -and -not $process.HasExited) {
            $forcedTermination = Stop-SmokeProcessTree -Process $process -GraceSeconds $ShutdownTimeout
        }
        if ($processStarted -and $process.HasExited) {
            $process.WaitForExit()
        }
        if ($null -ne $stderrTask) {
            try { $null = $stderrTask.GetAwaiter().GetResult() } catch { }
        }
        $process.Dispose()
        if ($null -ne $controlToken) {
            $controlToken = $null
        }
    }

    if ($forcedTermination) {
        throw "The '$Mode' app-server did not exit after stdin EOF; its owned process tree was terminated."
    }
    Write-SmokePass "real Codex app-server $Mode mode: initialize, initialized, unauthenticated account/read, empty thread/list"
}

$candidate = Resolve-BuiltAppRoot -Path $AppRoot
$smokeRoot = New-SafeSmokeDirectory -Prefix 'codex-router-smoke-real-appserver'
try {
    Assert-PathInsideSmokeRoot -Candidate (Join-Path $smokeRoot 'passthrough') -SmokeRoot $smokeRoot
    Assert-PathInsideSmokeRoot -Candidate (Join-Path $smokeRoot 'router') -SmokeRoot $smokeRoot
    Invoke-RealAppServerMode -Candidate $candidate -SmokeRoot $smokeRoot `
        -Mode 'passthrough' -RpcTimeout $RpcTimeoutSeconds -ShutdownTimeout $ShutdownTimeoutSeconds
    Invoke-RealAppServerMode -Candidate $candidate -SmokeRoot $smokeRoot `
        -Mode 'router' -RpcTimeout $RpcTimeoutSeconds -ShutdownTimeout $ShutdownTimeoutSeconds
    Write-SmokePass 'real app-server smoke completed without login, inference, reset, or user-profile access'
}
finally {
    if ($KeepArtifacts) {
        Write-Host 'Isolated smoke artifacts were retained under the current user temporary directory.'
    }
    else {
        Remove-SafeSmokeDirectory -Path $smokeRoot
    }
}
