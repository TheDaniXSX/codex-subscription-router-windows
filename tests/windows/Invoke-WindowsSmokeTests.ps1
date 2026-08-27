[CmdletBinding()]
param(
    [ValidateSet('Offline', 'InstalledReadOnly')]
    [string]$Mode = 'Offline',
    [string]$RepositoryRoot,
    [string]$AppRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),
    [string]$BackupRoot,
    [switch]$KeepArtifacts,
    [switch]$SkipPatchTests
)

. (Join-Path $PSScriptRoot 'Test-Helpers.ps1')
$repo = Resolve-RepositoryRoot $RepositoryRoot
$started = Get-Date
$results = [Collections.Generic.List[object]]::new()

function Invoke-SmokeStage {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][scriptblock]$Action
    )
    $stageStarted = Get-Date
    try {
        & $Action
        $script:results.Add([pscustomobject]@{
            Stage = $Name; Status = 'PASS'; Seconds = [math]::Round(((Get-Date) - $stageStarted).TotalSeconds, 2); Error = $null
        })
    }
    catch {
        $script:results.Add([pscustomobject]@{
            Stage = $Name; Status = 'FAIL'; Seconds = [math]::Round(((Get-Date) - $stageStarted).TotalSeconds, 2); Error = $_.Exception.Message
        })
        throw
    }
}

try {
    Invoke-SmokeStage 'Transparent proxy' {
        & (Join-Path $PSScriptRoot 'Test-TransparentProxy.ps1') -RepositoryRoot $repo -KeepArtifacts:$KeepArtifacts
    }
    Invoke-SmokeStage 'Mock two-account integration' {
        & (Join-Path $PSScriptRoot 'Test-MultiplexerIntegration.ps1') -RepositoryRoot $repo -KeepArtifacts:$KeepArtifacts
    }
    Invoke-SmokeStage 'Router contracts' {
        & (Join-Path $PSScriptRoot 'Test-RouterContracts.ps1') -RepositoryRoot $repo
    }
    Invoke-SmokeStage 'Read-only doctor and synthetic resource thresholds' {
        & (Join-Path $PSScriptRoot 'Test-DoctorAndSoak.ps1') -RepositoryRoot $repo
    }
    Invoke-SmokeStage 'Optional Windows shell integration' {
        & (Join-Path $PSScriptRoot 'Test-ShellIntegration.ps1') -RepositoryRoot $repo
    }
    Invoke-SmokeStage 'Chrome Native Messaging fixture integration' {
        & (Join-Path $PSScriptRoot 'Test-ChromeNativeHost.ps1') -RepositoryRoot $repo -KeepArtifacts:$KeepArtifacts
    }

    if (-not $SkipPatchTests) {
        $pythonTests = Join-Path $repo 'tests\windows'
        if (Test-Path -LiteralPath $pythonTests -PathType Container) {
            Invoke-SmokeStage 'Windows Python contract tests' {
                Assert-CommandAvailable 'python'
                Invoke-NativeChecked -FilePath 'python' -ArgumentList @(
                    '-m', 'unittest', 'discover', '-s', 'tests/windows', '-p', 'test_*.py', '-v'
                ) -WorkingDirectory $repo
            }
        }
    }

    if ($Mode -eq 'InstalledReadOnly') {
        Invoke-SmokeStage 'Installed layout and Computer Use artifacts' {
            & (Join-Path $PSScriptRoot 'Test-InstalledLayout.ps1') -AppRoot $AppRoot -RequireComputerUse
        }
        Invoke-SmokeStage 'Static Appshots and Computer Use contracts' {
            Assert-CommandAvailable 'python'
            Invoke-NativeChecked -FilePath 'python' -ArgumentList @(
                (Join-Path $repo 'scripts\qualify_windows_capabilities.py'), '--app-root', $AppRoot
            ) -WorkingDirectory $repo
        }
        Invoke-SmokeStage 'Rollback readiness' {
            & (Join-Path $PSScriptRoot 'Test-RollbackReadiness.ps1') -RepositoryRoot $repo -AppRoot $AppRoot -BackupRoot $BackupRoot
        }
    }
}
finally {
    Write-Host ''
    Write-Host 'Windows smoke-test summary'
    $results | Format-Table -AutoSize
    Write-Host ("Mode: {0}; elapsed: {1:N2}s" -f $Mode, ((Get-Date) - $started).TotalSeconds)
}
