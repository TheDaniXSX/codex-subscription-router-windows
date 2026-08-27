Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Resolve-RepositoryRoot {
    param([string]$RepositoryRoot)

    if ([string]::IsNullOrWhiteSpace($RepositoryRoot)) {
        $RepositoryRoot = Join-Path $PSScriptRoot '..\..'
    }
    $resolved = (Resolve-Path -LiteralPath $RepositoryRoot).Path
    if (-not (Test-Path -LiteralPath (Join-Path $resolved 'go.mod') -PathType Leaf)) {
        throw "Repository root does not contain go.mod: $resolved"
    }
    return $resolved
}

function Assert-CommandAvailable {
    param([Parameter(Mandatory)][string]$Name)

    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command is not available in PATH: $Name"
    }
}

function Resolve-GoCommand {
    $command = Get-Command 'go' -ErrorAction SilentlyContinue
    if ($null -ne $command) {
        return $command.Source
    }
    $programFiles = [Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFiles)
    $fallback = Join-Path $programFiles 'Go\bin\go.exe'
    if (Test-Path -LiteralPath $fallback -PathType Leaf) {
        return $fallback
    }
    throw 'Required command is not available: go. Install the supported Go toolchain and reopen the shell.'
}

function Invoke-NativeChecked {
    param(
        [Parameter(Mandatory)][string]$FilePath,
        [Parameter()][string[]]$ArgumentList = @(),
        [Parameter(Mandatory)][string]$WorkingDirectory
    )

    Push-Location $WorkingDirectory
    try {
        & $FilePath @ArgumentList
        if ($LASTEXITCODE -ne 0) {
            throw "Command failed with exit code ${LASTEXITCODE}: $FilePath $($ArgumentList -join ' ')"
        }
    }
    finally {
        Pop-Location
    }
}

function New-SafeSmokeDirectory {
    param([string]$Prefix = 'codex-router-smoke')

    $temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    $path = Join-Path $temporaryRoot ("{0}-{1}-{2}" -f $Prefix, $PID, [Guid]::NewGuid().ToString('N'))
    [void](New-Item -ItemType Directory -Path $path)
    return $path
}

function Remove-SafeSmokeDirectory {
    param([Parameter(Mandatory)][string]$Path)

    $temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    $resolved = [IO.Path]::GetFullPath($Path)
    if (-not $resolved.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove a path outside the temporary directory: $resolved"
    }
    if (-not (Split-Path -Leaf $resolved).StartsWith('codex-router-smoke-', [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove a directory without the smoke-test prefix: $resolved"
    }
    if (Test-Path -LiteralPath $resolved) {
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}

function Assert-Equal {
    param(
        [Parameter(Mandatory)]$Actual,
        [Parameter(Mandatory)]$Expected,
        [Parameter(Mandatory)][string]$Message
    )

    if ($Actual -ne $Expected) {
        throw "$Message. Expected '$Expected', got '$Actual'."
    }
}

function Write-SmokePass {
    param([Parameter(Mandatory)][string]$Name)
    Write-Host "PASS  $Name" -ForegroundColor Green
}
