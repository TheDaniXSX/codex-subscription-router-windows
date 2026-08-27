[CmdletBinding()]
param(
    [string]$AppRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),
    [switch]$RequireComputerUse
)

. (Join-Path $PSScriptRoot 'Test-Helpers.ps1')

if (-not (Test-Path -LiteralPath $AppRoot -PathType Container)) {
    throw "Installed router directory was not found: $AppRoot"
}
$resolvedRoot = (Resolve-Path -LiteralPath $AppRoot).Path
$files = @(Get-ChildItem -LiteralPath $resolvedRoot -Recurse -File)

$asar = @($files | Where-Object Name -EQ 'app.asar')
$wrapper = @($files | Where-Object Name -EQ 'codex.exe')
$real = @($files | Where-Object Name -EQ 'codex.real.exe')
if ($asar.Count -ne 1) { throw "Expected exactly one app.asar under $resolvedRoot; found $($asar.Count)." }
if ($wrapper.Count -ne 1) { throw "Expected exactly one codex.exe wrapper under $resolvedRoot; found $($wrapper.Count)." }
if ($real.Count -ne 1) { throw "Expected exactly one codex.real.exe under $resolvedRoot; found $($real.Count)." }
if ($wrapper[0].Length -eq $real[0].Length -and
    (Get-FileHash -Algorithm SHA256 -LiteralPath $wrapper[0].FullName).Hash -eq
    (Get-FileHash -Algorithm SHA256 -LiteralPath $real[0].FullName).Hash) {
    throw 'codex.exe and codex.real.exe are identical; the transparent wrapper was not installed.'
}
Write-SmokePass 'installed layout contains patched ASAR, wrapper, and preserved real Codex binary'

$computerUse = @($files | Where-Object {
    $_.Name -match '(?i)cua|computer.?use|code-mode-host' -or
    $_.DirectoryName -match '(?i)cua|computer.?use'
})
if ($computerUse.Count -eq 0) {
    $message = 'No Computer Use or CUA artifact was found in the installed copy.'
    if ($RequireComputerUse) { throw $message }
    Write-Warning $message
}
else {
    Write-SmokePass "Computer Use layout is present ($($computerUse.Count) matching artifacts)"
}

$signatures = foreach ($binary in @($wrapper[0], $real[0]) + @($computerUse | Where-Object Extension -EQ '.exe')) {
    $signature = Get-AuthenticodeSignature -LiteralPath $binary.FullName
    $subject = if ($null -ne $signature.SignerCertificate) {
        [string]$signature.SignerCertificate.Subject
    }
    else { '' }
    [pscustomobject]@{
        Path = $binary.FullName
        Status = [string]$signature.Status
        Subject = $subject
        SHA256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $binary.FullName).Hash
    }
}
$signatures | Format-Table -AutoSize

$runningFromRoot = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object {
    -not [string]::IsNullOrWhiteSpace($_.ExecutablePath) -and
    $_.ExecutablePath.StartsWith($resolvedRoot, [StringComparison]::OrdinalIgnoreCase)
})
if ($runningFromRoot.Count -gt 0) {
    Write-Host "INFO  Router is running from the inspected root; no process was stopped: $($runningFromRoot.ProcessId -join ', ')"
}
Write-SmokePass 'installed layout inspection completed without launching or stopping Codex'
