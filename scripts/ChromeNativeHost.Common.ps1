Set-StrictMode -Version 3.0

$script:ChromeNativeHostName = 'io.github.thedanixsx.codex_subscription_router'
$script:ChromeNativeHostRegistryPath = "Software\Google\Chrome\NativeMessagingHosts\$script:ChromeNativeHostName"

function Assert-ChromeExtensionId {
    param([Parameter(Mandatory = $true)][string]$ExtensionId)
    if ($ExtensionId -cnotmatch '^[a-p]{32}$') {
        throw 'ExtensionId must contain exactly 32 lowercase letters in the range a-p.'
    }
}

function Resolve-ChromeConnectorPath {
    param([Parameter(Mandatory = $true)][string]$Path)
    $expanded = [Environment]::ExpandEnvironmentVariables($Path)
    return [IO.Path]::GetFullPath($expanded).TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    )
}

function Assert-ChromeConnectorLocalPath {
    param([Parameter(Mandatory = $true)][string]$Path)
    $candidate = (Resolve-ChromeConnectorPath -Path $Path) + [IO.Path]::DirectorySeparatorChar
    $localRoot = (Resolve-ChromeConnectorPath -Path $env:LOCALAPPDATA) + [IO.Path]::DirectorySeparatorChar
    if (-not $candidate.StartsWith($localRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Chrome connector paths must be beneath LOCALAPPDATA: $Path"
    }
}

function Get-ChromeRegistryFixtureValuePath {
    param([Parameter(Mandatory = $true)][string]$RegistryFixtureRoot)
    return Join-Path $RegistryFixtureRoot (Join-Path $script:ChromeNativeHostRegistryPath '(default).txt')
}

function Get-ChromeNativeHostRegistryValue {
    param([string]$RegistryFixtureRoot)
    if (-not [string]::IsNullOrWhiteSpace($RegistryFixtureRoot)) {
        $fixtureValue = Get-ChromeRegistryFixtureValuePath -RegistryFixtureRoot $RegistryFixtureRoot
        if (-not (Test-Path -LiteralPath $fixtureValue -PathType Leaf)) {
            return $null
        }
        return [IO.File]::ReadAllText($fixtureValue)
    }
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($script:ChromeNativeHostRegistryPath, $false)
    if ($null -eq $key) {
        return $null
    }
    try {
        return $key.GetValue($null, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    }
    finally {
        $key.Dispose()
    }
}

function Set-ChromeNativeHostRegistryValue {
    [CmdletBinding(SupportsShouldProcess = $true)]
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [string]$RegistryFixtureRoot
    )
    if (-not [string]::IsNullOrWhiteSpace($RegistryFixtureRoot)) {
        $fixtureValue = Get-ChromeRegistryFixtureValuePath -RegistryFixtureRoot $RegistryFixtureRoot
        if (-not $PSCmdlet.ShouldProcess($fixtureValue, 'Set simulated Chrome Native Messaging default value')) {
            return
        }
        [IO.Directory]::CreateDirectory((Split-Path -Parent $fixtureValue)) | Out-Null
        [IO.File]::WriteAllText($fixtureValue, $Value, [Text.UTF8Encoding]::new($false))
        return
    }
    if (-not $PSCmdlet.ShouldProcess("HKCU\$script:ChromeNativeHostRegistryPath", 'Set Chrome Native Messaging default value')) {
        return
    }
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($script:ChromeNativeHostRegistryPath, $true)
    if ($null -eq $key) {
        throw 'Could not create the per-user Chrome Native Messaging registry key.'
    }
    try {
        $key.SetValue($null, $Value, [Microsoft.Win32.RegistryValueKind]::String)
    }
    finally {
        $key.Dispose()
    }
}

function Remove-ChromeNativeHostRegistryValueIfOwned {
    [CmdletBinding(SupportsShouldProcess = $true)]
    param(
        [Parameter(Mandatory = $true)][string]$ExpectedValue,
        [string]$RegistryFixtureRoot,
        [switch]$DryRun
    )
    $actual = Get-ChromeNativeHostRegistryValue -RegistryFixtureRoot $RegistryFixtureRoot
    if ($null -eq $actual) {
        return [PSCustomObject]@{ Removed = $false; Reason = 'missing' }
    }
    if (-not [string]::Equals([string]$actual, $ExpectedValue, [StringComparison]::Ordinal)) {
        return [PSCustomObject]@{ Removed = $false; Reason = 'value-mismatch' }
    }
    if ($DryRun) {
        return [PSCustomObject]@{ Removed = $false; Reason = 'dry-run-owned' }
    }
    $target = if ([string]::IsNullOrWhiteSpace($RegistryFixtureRoot)) {
        "HKCU\$script:ChromeNativeHostRegistryPath"
    }
    else {
        Get-ChromeRegistryFixtureValuePath -RegistryFixtureRoot $RegistryFixtureRoot
    }
    if (-not $PSCmdlet.ShouldProcess($target, 'Remove exact owned Chrome Native Messaging registration')) {
        return [PSCustomObject]@{ Removed = $false; Reason = 'what-if-owned' }
    }
    if (-not [string]::IsNullOrWhiteSpace($RegistryFixtureRoot)) {
        $fixtureValue = Get-ChromeRegistryFixtureValuePath -RegistryFixtureRoot $RegistryFixtureRoot
        Remove-Item -LiteralPath $fixtureValue -Force
    }
    else {
        [Microsoft.Win32.Registry]::CurrentUser.DeleteSubKey($script:ChromeNativeHostRegistryPath, $false)
    }
    return [PSCustomObject]@{ Removed = $true; Reason = 'exact-match' }
}

function Get-ChromeConnectorSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Write-ChromeConnectorJson {
    param(
        [Parameter(Mandatory = $true)]$Value,
        [Parameter(Mandatory = $true)][string]$Path
    )
    $json = $Value | ConvertTo-Json -Depth 8
    [IO.File]::WriteAllText($Path, $json + "`n", [Text.UTF8Encoding]::new($false))
}

function Remove-ChromeConnectorFileIfOwned {
    [CmdletBinding(SupportsShouldProcess = $true)]
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$ExpectedSha256,
        [switch]$DryRun
    )
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return [PSCustomObject]@{ Removed = $false; Reason = 'missing'; Path = $Path }
    }
    $actualHash = Get-ChromeConnectorSha256 -Path $Path
    if (-not [string]::Equals($actualHash, $ExpectedSha256, [StringComparison]::OrdinalIgnoreCase)) {
        return [PSCustomObject]@{ Removed = $false; Reason = 'hash-mismatch'; Path = $Path }
    }
    if (-not $DryRun -and $PSCmdlet.ShouldProcess($Path, 'Remove exact owned Chrome connector file')) {
        Remove-Item -LiteralPath $Path -Force
    }
    $removed = -not $DryRun -and -not (Test-Path -LiteralPath $Path)
    return [PSCustomObject]@{ Removed = $removed; Reason = $(if ($DryRun) { 'dry-run-owned' } elseif ($removed) { 'exact-match' } else { 'what-if-owned' }); Path = $Path }
}
