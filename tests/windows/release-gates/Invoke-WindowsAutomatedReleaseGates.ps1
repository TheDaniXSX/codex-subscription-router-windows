[CmdletBinding()]
param(
    [string]$RepositoryRoot,
    [string]$ReportPath,
    [ValidateRange(10, 10000)]
    [int]$SoakRequestsPerCycle = 80,
    [ValidateRange(2, 20)]
    [int]$SoakRestartCycles = 2,
    [switch]$KeepArtifacts
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$engine = Join-Path $PSScriptRoot '..\qualification\Invoke-WindowsReleaseQualification.ps1'
& $engine @PSBoundParameters
