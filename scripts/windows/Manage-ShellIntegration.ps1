#Requires -Version 5.1

<#
.SYNOPSIS
Manages optional, per-user Windows shell integration for Codex Subscription Router.

.DESCRIPTION
Registers or unregisters the router-owned codex-router:// protocol and classic
Explorer directory verbs below HKCU\Software\Classes. The integration is opt-in,
uses no elevation, and never writes the official codex:// protocol, AppX keys,
CLSID registrations, or OpenAI Explorer verbs.

Registration fails closed if a selected key already contains anything other
than the exact router-owned schema. Unregistration is compare-and-delete: an
entire selected tree is removed only when every key, value, kind, and datum is
still an exact match. Use -WhatIf to inspect all proposed writes/deletions.

.PARAMETER Action
Register, Unregister, or report Status. Status is the default and never writes.

.PARAMETER Feature
Protocol, Explorer, or All. Registration is always explicit.

.PARAMETER LauncherPath
Absolute path to the unpackaged router launcher. It must exist for Register.

.EXAMPLE
pwsh -NoProfile -File .\scripts\windows\Manage-ShellIntegration.ps1 `
    -Action Register -Feature All -WhatIf

.EXAMPLE
pwsh -NoProfile -File .\scripts\windows\Manage-ShellIntegration.ps1 `
    -Action Register -Feature Protocol

.EXAMPLE
pwsh -NoProfile -File .\scripts\windows\Manage-ShellIntegration.ps1 `
    -Action Unregister -Feature All
#>

[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'Medium')]
param(
    [ValidateSet('Register', 'Unregister', 'Status')]
    [string]$Action = 'Status',

    [ValidateSet('Protocol', 'Explorer', 'All')]
    [string]$Feature = 'All',

    [string]$LauncherPath = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router\ChatGPT.exe')
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'

$script:IntegrationOwner = 'TheDaniXSX/codex-subscription-router-windows'
$script:IntegrationSchemaVersion = '1'
$script:RegistryBaseDescription = 'HKCU\Software\Classes'

function Resolve-SafeLauncherPath {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [switch]$MustExist
    )

    if ([string]::IsNullOrWhiteSpace($Path) -or $Path.IndexOf([char]0) -ge 0 -or $Path.IndexOf('"') -ge 0 -or $Path -match '[\r\n]') {
        throw 'LauncherPath contains invalid text.'
    }
    $expanded = [Environment]::ExpandEnvironmentVariables($Path)
    if (-not [IO.Path]::IsPathRooted($expanded)) {
        throw "LauncherPath must be absolute: $Path"
    }
    $fullPath = [IO.Path]::GetFullPath($expanded)
    $volumeRoot = [IO.Path]::GetPathRoot($fullPath)
    if ($fullPath.StartsWith('\\', [StringComparison]::OrdinalIgnoreCase) -or
        $volumeRoot -notmatch '^[A-Za-z]:\\$') {
        throw 'LauncherPath must be on a local drive; UNC and device paths are not supported.'
    }
    if ($fullPath.Substring($volumeRoot.Length).Contains(':')) {
        throw 'LauncherPath must not use an alternate data stream.'
    }
    if (-not (Split-Path -Leaf $fullPath).Equals('ChatGPT.exe', [StringComparison]::OrdinalIgnoreCase)) {
        throw 'LauncherPath must identify the router-owned ChatGPT.exe launcher.'
    }
    if ($MustExist -and -not (Test-Path -LiteralPath $fullPath -PathType Leaf)) {
        throw "Router launcher does not exist: $fullPath"
    }
    return $fullPath
}

function ConvertTo-RegistryCommand {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Launcher,
        [Parameter(Mandatory = $true)][ValidateSet('%1', '%V')][string]$Placeholder
    )

    $safeLauncher = Resolve-SafeLauncherPath -Path $Launcher
    return '"{0}" "{1}"' -f $safeLauncher, $Placeholder
}

function Get-IntegrationRegistrationModel {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][ValidateSet('Protocol', 'ExplorerDirectory', 'ExplorerBackground')][string]$Name,
        [Parameter(Mandatory = $true)][string]$Launcher
    )

    $safeLauncher = Resolve-SafeLauncherPath -Path $Launcher
    $installLocation = Split-Path -Parent $safeLauncher
    $ownedValues = [ordered]@{
        'CodexSubscriptionRouter.Owner' = $script:IntegrationOwner
        'CodexSubscriptionRouter.SchemaVersion' = $script:IntegrationSchemaVersion
        'CodexSubscriptionRouter.InstallLocation' = $installLocation
    }

    switch ($Name) {
        'Protocol' {
            $root = 'Software\Classes\codex-router'
            $featureName = 'Protocol'
            $keys = [ordered]@{
                '' = [ordered]@{
                    '' = 'URL:Codex Subscription Router Protocol'
                    'URL Protocol' = ''
                    'CodexSubscriptionRouter.Owner' = $ownedValues['CodexSubscriptionRouter.Owner']
                    'CodexSubscriptionRouter.SchemaVersion' = $ownedValues['CodexSubscriptionRouter.SchemaVersion']
                    'CodexSubscriptionRouter.InstallLocation' = $ownedValues['CodexSubscriptionRouter.InstallLocation']
                }
                'DefaultIcon' = [ordered]@{ '' = '"{0}",0' -f $safeLauncher }
                'shell' = [ordered]@{}
                'shell\open' = [ordered]@{}
                'shell\open\command' = [ordered]@{ '' = ConvertTo-RegistryCommand -Launcher $safeLauncher -Placeholder '%1' }
            }
        }
        'ExplorerDirectory' {
            $root = 'Software\Classes\Directory\shell\OpenProjectInCodexRouter'
            $featureName = 'Explorer'
            $keys = [ordered]@{
                '' = [ordered]@{
                    '' = 'Open in Codex Subscription Router'
                    'Icon' = '"{0}",0' -f $safeLauncher
                    'MultiSelectModel' = 'Single'
                    'CodexSubscriptionRouter.Owner' = $ownedValues['CodexSubscriptionRouter.Owner']
                    'CodexSubscriptionRouter.SchemaVersion' = $ownedValues['CodexSubscriptionRouter.SchemaVersion']
                    'CodexSubscriptionRouter.InstallLocation' = $ownedValues['CodexSubscriptionRouter.InstallLocation']
                }
                'command' = [ordered]@{ '' = ConvertTo-RegistryCommand -Launcher $safeLauncher -Placeholder '%1' }
            }
        }
        'ExplorerBackground' {
            $root = 'Software\Classes\Directory\Background\shell\OpenProjectInCodexRouter'
            $featureName = 'Explorer'
            $keys = [ordered]@{
                '' = [ordered]@{
                    '' = 'Open in Codex Subscription Router'
                    'Icon' = '"{0}",0' -f $safeLauncher
                    'CodexSubscriptionRouter.Owner' = $ownedValues['CodexSubscriptionRouter.Owner']
                    'CodexSubscriptionRouter.SchemaVersion' = $ownedValues['CodexSubscriptionRouter.SchemaVersion']
                    'CodexSubscriptionRouter.InstallLocation' = $ownedValues['CodexSubscriptionRouter.InstallLocation']
                }
                'command' = [ordered]@{ '' = ConvertTo-RegistryCommand -Launcher $safeLauncher -Placeholder '%V' }
            }
        }
    }

    return [pscustomobject]@{
        Name = $Name
        Feature = $featureName
        Root = $root
        Keys = $keys
    }
}

function Get-IntegrationRegistration {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Launcher,
        [Parameter(Mandatory = $true)][ValidateSet('Protocol', 'Explorer', 'All')][string]$SelectedFeature
    )

    $all = @(
        Get-IntegrationRegistrationModel -Name Protocol -Launcher $Launcher
        Get-IntegrationRegistrationModel -Name ExplorerDirectory -Launcher $Launcher
        Get-IntegrationRegistrationModel -Name ExplorerBackground -Launcher $Launcher
    )
    if ($SelectedFeature -eq 'All') {
        return $all
    }
    return @($all | Where-Object { $_.Feature -eq $SelectedFeature })
}

function Get-EmptyMemoryRegistryContext {
    [CmdletBinding()]
    param()

    return [pscustomobject]@{
        Kind = 'Memory'
        Keys = [Collections.Hashtable]::new([StringComparer]::OrdinalIgnoreCase)
    }
}

function Get-WindowsRegistryContext {
    [CmdletBinding()]
    param()

    return [pscustomobject]@{ Kind = 'Windows' }
}

function Join-RegistryRelativePath {
    param([Parameter(Mandatory = $true)][string]$Root, [string]$Child)
    if ([string]::IsNullOrEmpty($Child)) { return $Root }
    return "$Root\$Child"
}

function Set-RegistryRegistration {
    [CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'Medium')]
    param(
        [Parameter(Mandatory = $true)]$Context,
        [Parameter(Mandatory = $true)]$Registration
    )

    $target = "$script:RegistryBaseDescription\$($Registration.Root.Substring('Software\Classes\'.Length))"
    if (-not $PSCmdlet.ShouldProcess($target, 'Register exact router-owned shell integration')) { return }

    if ($Context.Kind -eq 'Memory') {
        foreach ($relativeKey in $Registration.Keys.Keys) {
            $path = Join-RegistryRelativePath -Root $Registration.Root -Child $relativeKey
            $values = [Collections.Hashtable]::new([StringComparer]::OrdinalIgnoreCase)
            foreach ($name in $Registration.Keys[$relativeKey].Keys) {
                $values[$name] = [pscustomobject]@{ Kind = 'String'; Data = [string]$Registration.Keys[$relativeKey][$name] }
            }
            $Context.Keys[$path] = $values
        }
        return
    }
    if ($Context.Kind -ne 'Windows') { throw "Unknown registry context: $($Context.Kind)" }

    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey(
        [Microsoft.Win32.RegistryHive]::CurrentUser,
        [Microsoft.Win32.RegistryView]::Default
    )
    try {
        foreach ($relativeKey in $Registration.Keys.Keys) {
            $path = Join-RegistryRelativePath -Root $Registration.Root -Child $relativeKey
            $key = $base.CreateSubKey($path, $true)
            if ($null -eq $key) { throw "Could not create registry key HKCU\$path" }
            try {
                foreach ($name in $Registration.Keys[$relativeKey].Keys) {
                    $key.SetValue($name, [string]$Registration.Keys[$relativeKey][$name], [Microsoft.Win32.RegistryValueKind]::String)
                }
            }
            finally { $key.Dispose() }
        }
    }
    finally { $base.Dispose() }
}

function Remove-RegistryRegistration {
    [CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'Medium')]
    param(
        [Parameter(Mandatory = $true)]$Context,
        [Parameter(Mandatory = $true)]$Registration
    )

    $target = "$script:RegistryBaseDescription\$($Registration.Root.Substring('Software\Classes\'.Length))"
    if (-not $PSCmdlet.ShouldProcess($target, 'Delete exact router-owned shell integration')) { return }

    if ($Context.Kind -eq 'Memory') {
        $prefix = $Registration.Root + '\'
        foreach ($path in @($Context.Keys.Keys)) {
            if ($path.Equals($Registration.Root, [StringComparison]::OrdinalIgnoreCase) -or
                $path.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
                $Context.Keys.Remove($path)
            }
        }
        return
    }
    if ($Context.Kind -ne 'Windows') { throw "Unknown registry context: $($Context.Kind)" }

    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey(
        [Microsoft.Win32.RegistryHive]::CurrentUser,
        [Microsoft.Win32.RegistryView]::Default
    )
    try { $base.DeleteSubKeyTree($Registration.Root, $false) }
    finally { $base.Dispose() }
}

function Get-RegistryRegistrationSnapshot {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]$Context,
        [Parameter(Mandatory = $true)][string]$Root
    )

    $result = [Collections.Hashtable]::new([StringComparer]::OrdinalIgnoreCase)
    if ($Context.Kind -eq 'Memory') {
        $prefix = $Root + '\'
        foreach ($path in $Context.Keys.Keys) {
            if (-not ($path.Equals($Root, [StringComparison]::OrdinalIgnoreCase) -or
                $path.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase))) { continue }
            $relative = if ($path.Length -eq $Root.Length) { '' } else { $path.Substring($Root.Length + 1) }
            $values = [Collections.Hashtable]::new([StringComparer]::OrdinalIgnoreCase)
            foreach ($name in $Context.Keys[$path].Keys) { $values[$name] = $Context.Keys[$path][$name] }
            $result[$relative] = $values
        }
        return [pscustomobject]@{ Exists = $result.Count -gt 0; Keys = $result }
    }
    if ($Context.Kind -ne 'Windows') { throw "Unknown registry context: $($Context.Kind)" }

    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey(
        [Microsoft.Win32.RegistryHive]::CurrentUser,
        [Microsoft.Win32.RegistryView]::Default
    )
    try {
        $rootKey = $base.OpenSubKey($Root, $false)
        if ($null -eq $rootKey) { return [pscustomobject]@{ Exists = $false; Keys = $result } }
        try { Read-RegistryTree -Key $rootKey -RelativePath '' -Destination $result }
        finally { $rootKey.Dispose() }
    }
    finally { $base.Dispose() }
    return [pscustomobject]@{ Exists = $true; Keys = $result }
}

function Read-RegistryTree {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][Microsoft.Win32.RegistryKey]$Key,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RelativePath,
        [Parameter(Mandatory = $true)]$Destination
    )

    $values = [Collections.Hashtable]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($name in $Key.GetValueNames()) {
        $kind = $Key.GetValueKind($name).ToString()
        $data = $Key.GetValue($name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $values[$name] = [pscustomobject]@{ Kind = $kind; Data = [string]$data }
    }
    $Destination[$RelativePath] = $values
    foreach ($childName in $Key.GetSubKeyNames()) {
        $child = $Key.OpenSubKey($childName, $false)
        if ($null -eq $child) { throw "Could not read registry child key: $childName" }
        $childRelative = if ($RelativePath -eq '') { $childName } else { "$RelativePath\$childName" }
        try { Read-RegistryTree -Key $child -RelativePath $childRelative -Destination $Destination }
        finally { $child.Dispose() }
    }
}

function Compare-RegistrationSnapshot {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]$Registration,
        [Parameter(Mandatory = $true)]$Snapshot
    )

    if (-not $Snapshot.Exists) {
        return [pscustomobject]@{ State = 'Missing'; Detail = 'root key is absent' }
    }
    $differences = [Collections.Generic.List[string]]::new()
    foreach ($relativeKey in $Registration.Keys.Keys) {
        if (-not $Snapshot.Keys.ContainsKey($relativeKey)) {
            $differences.Add("missing key '$relativeKey'")
            continue
        }
        $actualValues = $Snapshot.Keys[$relativeKey]
        $expectedValues = $Registration.Keys[$relativeKey]
        foreach ($name in $expectedValues.Keys) {
            $displayName = if ($name -eq '') { '(Default)' } else { $name }
            if (-not $actualValues.ContainsKey($name)) {
                $differences.Add("missing value '$relativeKey::$displayName'")
                continue
            }
            $actual = $actualValues[$name]
            if ($actual.Kind -ne 'String' -or [string]$actual.Data -cne [string]$expectedValues[$name]) {
                $differences.Add("different value '$relativeKey::$displayName'")
            }
        }
        foreach ($name in $actualValues.Keys) {
            if (-not $expectedValues.Contains($name)) {
                $displayName = if ($name -eq '') { '(Default)' } else { $name }
                $differences.Add("unexpected value '$relativeKey::$displayName'")
            }
        }
    }
    foreach ($relativeKey in $Snapshot.Keys.Keys) {
        if (-not $Registration.Keys.Contains($relativeKey)) { $differences.Add("unexpected key '$relativeKey'") }
    }
    if ($differences.Count -eq 0) {
        return [pscustomobject]@{ State = 'Exact'; Detail = 'exact router-owned schema' }
    }
    return [pscustomobject]@{ State = 'Conflict'; Detail = ($differences -join '; ') }
}

function Invoke-ShellIntegration {
    [CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'Medium')]
    param(
        [Parameter(Mandatory = $true)][ValidateSet('Register', 'Unregister', 'Status')][string]$RequestedAction,
        [Parameter(Mandatory = $true)][ValidateSet('Protocol', 'Explorer', 'All')][string]$SelectedFeature,
        [Parameter(Mandatory = $true)][string]$Launcher,
        [Parameter(Mandatory = $true)]$Context
    )

    $safeLauncher = Resolve-SafeLauncherPath -Path $Launcher -MustExist:($RequestedAction -eq 'Register' -and $Context.Kind -eq 'Windows')
    $registrations = @(Get-IntegrationRegistration -Launcher $safeLauncher -SelectedFeature $SelectedFeature)
    $states = foreach ($registration in $registrations) {
        $snapshot = Get-RegistryRegistrationSnapshot -Context $Context -Root $registration.Root
        $comparison = Compare-RegistrationSnapshot -Registration $registration -Snapshot $snapshot
        [pscustomobject]@{
            Name = $registration.Name
            Feature = $registration.Feature
            RegistryPath = "$script:RegistryBaseDescription\$($registration.Root.Substring('Software\Classes\'.Length))"
            State = $comparison.State
            Detail = $comparison.Detail
            Registration = $registration
        }
    }

    if ($RequestedAction -eq 'Status') {
        return @($states | Select-Object Name, Feature, RegistryPath, State, Detail)
    }
    $conflicts = @($states | Where-Object { $_.State -eq 'Conflict' })
    if ($conflicts.Count -gt 0) {
        $summary = $conflicts | ForEach-Object { "$($_.RegistryPath): $($_.Detail)" }
        throw "Refusing to $($RequestedAction.ToLowerInvariant()) conflicting registry data. $($summary -join ' | ')"
    }

    foreach ($state in $states) {
        if ($RequestedAction -eq 'Register' -and $state.State -eq 'Missing') {
            Set-RegistryRegistration -Context $Context -Registration $state.Registration -WhatIf:$WhatIfPreference
        }
        elseif ($RequestedAction -eq 'Unregister' -and $state.State -eq 'Exact') {
            Remove-RegistryRegistration -Context $Context -Registration $state.Registration -WhatIf:$WhatIfPreference
        }
    }
    return @(Invoke-ShellIntegration -RequestedAction Status -SelectedFeature $SelectedFeature -Launcher $safeLauncher -Context $Context)
}

if ($MyInvocation.InvocationName -ne '.') {
    $context = Get-WindowsRegistryContext
    $results = Invoke-ShellIntegration `
        -RequestedAction $Action `
        -SelectedFeature $Feature `
        -Launcher $LauncherPath `
        -Context $context `
        -WhatIf:$WhatIfPreference
    $results | Format-Table -AutoSize
}
