[CmdletBinding()]
param(
    [string]$RepositoryRoot,
    [switch]$KeepArtifacts
)

. (Join-Path $PSScriptRoot 'Test-Helpers.ps1')

$repo = Resolve-RepositoryRoot $RepositoryRoot
$go = Resolve-GoCommand
$work = New-SafeSmokeDirectory -Prefix 'codex-router-smoke-proxy'

try {
    $mux = Join-Path $work 'codex.exe'
    $mock = Join-Path $work 'mock-codex.exe'
    Invoke-NativeChecked -FilePath $go -ArgumentList @('build', '-trimpath', '-o', $mux, './cmd/codex-mux') -WorkingDirectory $repo
    Invoke-NativeChecked -FilePath $go -ArgumentList @('build', '-trimpath', '-o', $mock, './tests/windows/fixtures') -WorkingDirectory $repo

    $primaryHome = Join-Path $work 'primary-home'
    $muxHome = Join-Path $work 'mux-home'
    [void](New-Item -ItemType Directory -Path $primaryHome, $muxHome)

    $saved = @{}
    foreach ($name in @('CODEX_MUX_REAL_CODEX', 'CODEX_HOME', 'CODEX_MUX_HOME', 'MOCK_CODEX_MARKER', 'MOCK_CODEX_EXIT_CODE')) {
        $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
    }
    try {
        $env:CODEX_MUX_REAL_CODEX = $mock
        $env:CODEX_HOME = $primaryHome
        $env:CODEX_MUX_HOME = $muxHome
        $env:MOCK_CODEX_MARKER = 'transparent-proxy-smoke'
        $env:MOCK_CODEX_EXIT_CODE = '0'

        $output = @(& $mux exec '--probe=alpha' 'value with spaces' 2>&1)
        Assert-Equal -Actual $LASTEXITCODE -Expected 0 -Message 'Transparent proxy did not preserve a successful exit code'
        $payload = ($output -join "`n") | ConvertFrom-Json
        Assert-Equal -Actual $payload.args.Count -Expected 3 -Message 'Transparent proxy changed the argument count'
        Assert-Equal -Actual $payload.args[0] -Expected 'exec' -Message 'Transparent proxy changed argument 1'
        Assert-Equal -Actual $payload.args[1] -Expected '--probe=alpha' -Message 'Transparent proxy changed argument 2'
        Assert-Equal -Actual $payload.args[2] -Expected 'value with spaces' -Message 'Transparent proxy changed argument 3'
        Assert-Equal -Actual $payload.codexHome -Expected $primaryHome -Message 'Transparent proxy changed CODEX_HOME'
        Assert-Equal -Actual $payload.muxHome -Expected $muxHome -Message 'Transparent proxy changed CODEX_MUX_HOME'
        Assert-Equal -Actual $payload.probeMarker -Expected 'transparent-proxy-smoke' -Message 'Transparent proxy changed an unrelated environment variable'
        Write-SmokePass 'transparent proxy preserves arguments, environment, stdout, and success status'

        $env:MOCK_CODEX_EXIT_CODE = '23'
        $null = & $mux exec '--probe=nonzero' 2>$null
        Assert-Equal -Actual $LASTEXITCODE -Expected 23 -Message 'Transparent proxy did not preserve a non-zero child exit code'
        Write-SmokePass 'transparent proxy preserves non-zero exit status'
    }
    finally {
        foreach ($entry in $saved.GetEnumerator()) {
            [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, 'Process')
        }
    }
}
finally {
    if ($KeepArtifacts) {
        Write-Host "Smoke artifacts: $work"
    }
    else {
        Remove-SafeSmokeDirectory -Path $work
    }
}
