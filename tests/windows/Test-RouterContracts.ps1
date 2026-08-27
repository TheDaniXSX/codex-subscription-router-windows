[CmdletBinding()]
param([string]$RepositoryRoot)

. (Join-Path $PSScriptRoot 'Test-Helpers.ps1')

$repo = Resolve-RepositoryRoot $RepositoryRoot
$go = Resolve-GoCommand

$groups = @(
    @{
        Name = 'account isolation, two-account state, sticky ownership, and plugin propagation'
        Package = './internal/state'
        Pattern = '^(TestStoreBootstrapsPrimaryAndPersistsThreadAffinity|TestAccountConfigInheritsManagedMCPAndPreservesLocalProjects|TestSyncManagedConfigPropagatesPluginsWithoutRestart|TestUpdateAccountPreservesController|TestWindows.*|Test.*Windows.*)$'
    },
    @{
        Name = 'sticky routing, quota selection, and failover error contracts'
        Package = './internal/mux'
        Pattern = '^(TestIsUsageLimitResponse.*|TestAllSubscriptionsDepleted.*|TestRateLimitPreview.*|TestAllDepletedPreview.*|TestRouteUrgency.*|TestAggregateRateLimits.*)$'
    },
    @{
        Name = 'account-scoped plugins and MCP OAuth'
        Package = './internal/mux'
        Pattern = '^TestScopedPluginRequest.*$'
    },
    @{
        Name = 'account-scoped reset preview, retrieval, redemption, and routing bonus'
        Package = './internal/mux'
        Pattern = '^(TestFetchRateLimitResetCredits.*|TestConsumeRateLimitResetCredits.*|TestPreviewResetCredits.*|TestDecodeResetCreditMetadata.*|TestRoutingResetCredits.*|TestRouteUrgencyWeightsBankedResetsWithoutDominating|TestRouteUrgencyCapsResetBonus)$'
    },
    @{
        Name = 'Windows wrapper and child-process contracts'
        Package = './cmd/codex-mux', './internal/backend'
        Pattern = '^(TestInteractiveAppServerDetection|TestValidateControlToken|TestWindows.*|Test.*Windows.*|TestWithEnvironment.*)$'
    }
)

foreach ($group in $groups) {
    $arguments = @('test', '-count=1', '-run', $group.Pattern) + $group.Package
    Invoke-NativeChecked -FilePath $go -ArgumentList $arguments -WorkingDirectory $repo
    Write-SmokePass $group.Name
}
