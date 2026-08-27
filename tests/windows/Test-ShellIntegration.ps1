[CmdletBinding()]
param([string]$RepositoryRoot)

. (Join-Path $PSScriptRoot 'Test-Helpers.ps1')
$repo = Resolve-RepositoryRoot $RepositoryRoot
. (Join-Path $repo 'scripts\windows\Manage-ShellIntegration.ps1')

function Assert-True {
    param([Parameter(Mandatory = $true)][bool]$Condition, [Parameter(Mandatory = $true)][string]$Message)
    if (-not $Condition) { throw $Message }
}

$launcher = 'C:\Program Files\Codex Subscription Router & Test\ChatGPT.exe'

# All tests use the explicit in-memory adapter. Dot-sourcing the implementation
# never enters its production HKCU entry point.
$context = Get-EmptyMemoryRegistryContext
$initial = @(Invoke-ShellIntegration -RequestedAction Status -SelectedFeature All -Launcher $launcher -Context $context)
Assert-True ($initial.Count -eq 3) 'Expected protocol and two Explorer registrations.'
Assert-True (@($initial | Where-Object State -ne 'Missing').Count -eq 0) 'Fresh memory registry was not empty.'

$whatIfContext = Get-EmptyMemoryRegistryContext
[void](Invoke-ShellIntegration -RequestedAction Register -SelectedFeature All -Launcher $launcher -Context $whatIfContext -WhatIf)
Assert-True ($whatIfContext.Keys.Count -eq 0) '-WhatIf mutated the in-memory registry.'

$collisionContext = Get-EmptyMemoryRegistryContext
$collisionValues = [Collections.Hashtable]::new([StringComparer]::OrdinalIgnoreCase)
$collisionValues[''] = [pscustomobject]@{ Kind = 'String'; Data = 'Another application' }
$collisionContext.Keys['Software\Classes\codex-router'] = $collisionValues
$collisionRefused = $false
try { [void](Invoke-ShellIntegration -RequestedAction Register -SelectedFeature Protocol -Launcher $launcher -Context $collisionContext) }
catch { $collisionRefused = $_.Exception.Message -match 'Refusing to register conflicting registry data' }
Assert-True $collisionRefused 'Registration overwrote an existing unowned protocol.'
Assert-Equal $collisionContext.Keys['Software\Classes\codex-router'][''].Data 'Another application' 'Protocol collision was mutated.'

$registered = @(Invoke-ShellIntegration -RequestedAction Register -SelectedFeature All -Launcher $launcher -Context $context)
Assert-True (@($registered | Where-Object State -ne 'Exact').Count -eq 0) 'Registration did not create the exact schema.'
$keyCount = $context.Keys.Count
[void](Invoke-ShellIntegration -RequestedAction Unregister -SelectedFeature All -Launcher $launcher -Context $context -WhatIf)
Assert-True ($context.Keys.Count -eq $keyCount) 'Unregister -WhatIf mutated the in-memory registry.'
[void](Invoke-ShellIntegration -RequestedAction Register -SelectedFeature All -Launcher $launcher -Context $context)
Assert-True ($context.Keys.Count -eq $keyCount) 'Idempotent registration changed the key count.'

$models = @(Get-IntegrationRegistration -Launcher $launcher -SelectedFeature All)
$protocol = $models | Where-Object Name -eq 'Protocol'
$directory = $models | Where-Object Name -eq 'ExplorerDirectory'
$background = $models | Where-Object Name -eq 'ExplorerBackground'
Assert-Equal $protocol.Root 'Software\Classes\codex-router' 'Protocol uses an unexpected identity'
Assert-Equal $directory.Root 'Software\Classes\Directory\shell\OpenProjectInCodexRouter' 'Directory verb uses an unexpected identity'
Assert-Equal $background.Root 'Software\Classes\Directory\Background\shell\OpenProjectInCodexRouter' 'Background verb uses an unexpected identity'

$protocolCommand = $protocol.Keys['shell\open\command']['']
$directoryCommand = $directory.Keys['command']['']
$backgroundCommand = $background.Keys['command']['']
Assert-Equal $protocolCommand '"C:\Program Files\Codex Subscription Router & Test\ChatGPT.exe" "%1"' 'Protocol command quoting is unsafe'
Assert-Equal $directoryCommand '"C:\Program Files\Codex Subscription Router & Test\ChatGPT.exe" "%1"' 'Directory command quoting is unsafe'
Assert-Equal $backgroundCommand '"C:\Program Files\Codex Subscription Router & Test\ChatGPT.exe" "%V"' 'Background command quoting is unsafe'

foreach ($model in $models) {
    Assert-True (-not $model.Root.Equals('Software\Classes\codex', [StringComparison]::OrdinalIgnoreCase)) 'Model claimed official codex:// protocol.'
    Assert-True ($model.Root -notmatch '(?i)CLSID|NativeMessagingHosts|OpenAI\.Codex') 'Model touched a forbidden official/COM namespace.'
    Assert-Equal $model.Keys['']['CodexSubscriptionRouter.Owner'] 'TheDaniXSX/codex-subscription-router-windows' 'Owner marker is not independent'
}

# Compare-and-delete must refuse a tree changed after registration and preserve
# every byte of that conflicting tree.
$protocolCommandPath = $protocol.Root + '\shell\open\command'
$context.Keys[$protocolCommandPath][''] = [pscustomobject]@{ Kind = 'String'; Data = '"C:\Windows\System32\cmd.exe" /c calc.exe' }
$beforeConflictCount = $context.Keys.Count
$refused = $false
try {
    [void](Invoke-ShellIntegration -RequestedAction Unregister -SelectedFeature Protocol -Launcher $launcher -Context $context)
}
catch { $refused = $_.Exception.Message -match 'Refusing to unregister conflicting registry data' }
Assert-True $refused 'Unregistration did not fail closed for a changed command.'
Assert-True ($context.Keys.Count -eq $beforeConflictCount) 'Failed compare-and-delete mutated the registry.'
Assert-Equal $context.Keys[$protocolCommandPath][''].Data '"C:\Windows\System32\cmd.exe" /c calc.exe' 'Conflicting command was overwritten or deleted.'

# Restore the exact owned value, remove only the protocol, and prove the two
# Explorer verbs remain independently enabled.
$context.Keys[$protocolCommandPath][''] = [pscustomobject]@{ Kind = 'String'; Data = $protocolCommand }
$afterProtocolRemoval = @(Invoke-ShellIntegration -RequestedAction Unregister -SelectedFeature Protocol -Launcher $launcher -Context $context)
Assert-Equal $afterProtocolRemoval[0].State 'Missing' 'Protocol was not removed.'
$explorerState = @(Invoke-ShellIntegration -RequestedAction Status -SelectedFeature Explorer -Launcher $launcher -Context $context)
Assert-True (@($explorerState | Where-Object State -ne 'Exact').Count -eq 0) 'Disabling protocol changed Explorer integration.'

[void](Invoke-ShellIntegration -RequestedAction Unregister -SelectedFeature Explorer -Launcher $launcher -Context $context)
Assert-True ($context.Keys.Count -eq 0) 'Exact unregistration left router-owned keys behind.'

foreach ($invalidLauncher in @(
    'relative\ChatGPT.exe',
    '\\server\share\ChatGPT.exe',
    "C:\Router`nChatGPT.exe",
    'C:\Router\bad"name.exe',
    'C:\Router\ChatGPT.exe:stream',
    'C:\Windows\System32\calc.exe'
)) {
    $rejected = $false
    try { [void](Resolve-SafeLauncherPath -Path $invalidLauncher) }
    catch { $rejected = $true }
    Assert-True $rejected "Unsafe launcher path was accepted: $invalidLauncher"
}

Write-SmokePass 'Optional shell integration (in-memory registry, quoting, isolation, compare-and-delete)'
