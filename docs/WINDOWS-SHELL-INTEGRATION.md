# Optional Windows shell integration

The unpackaged build supports two independent, per-user desktop integrations:

- the private `codex-router://` protocol;
- classic Explorer **Open in Codex Subscription Router** verbs for a directory
  and for a directory background.

They are deliberately opt-in. Installing the application does not change the
registry, file associations, Default Apps, or the official Codex installation.
Neither integration requires elevation.

## Safety boundary

The manager writes only these router-owned trees:

```text
HKCU\Software\Classes\codex-router
HKCU\Software\Classes\Directory\shell\OpenProjectInCodexRouter
HKCU\Software\Classes\Directory\Background\shell\OpenProjectInCodexRouter
```

It never writes `HKCU\Software\Classes\codex`, a CLSID, an AppX/package key,
the official `OpenAI.Codex` Explorer verb, or the Chrome Native Messaging host.
Each command invokes the independent `ChatGPT.exe` launcher directly with a
quoted `%1` or `%V` argument. No shell, `cmd.exe`, or PowerShell command string
is placed between Explorer and the launcher.

Registration fails when a selected root contains data that is not the exact
router schema. Unregistration is compare-and-delete: it removes a tree only if
all key names, value names, registry kinds, and data still match. A modified or
third-party tree is reported as `Conflict` and left untouched.

## Preview, enable, inspect, and disable

From the repository root, preview both integrations without changing HKCU:

```powershell
pwsh -NoProfile -File .\scripts\windows\Manage-ShellIntegration.ps1 `
  -Action Register -Feature All -WhatIf
```

Enable both, or select only `Protocol` or `Explorer`:

```powershell
pwsh -NoProfile -File .\scripts\windows\Manage-ShellIntegration.ps1 `
  -Action Register -Feature All
```

Inspect their exact/missing/conflict state (read-only):

```powershell
pwsh -NoProfile -File .\scripts\windows\Manage-ShellIntegration.ps1 `
  -Action Status -Feature All
```

Disable them, with the same `-WhatIf` support:

```powershell
pwsh -NoProfile -File .\scripts\windows\Manage-ShellIntegration.ps1 `
  -Action Unregister -Feature All
```

Use `-LauncherPath <absolute-path>` for a non-default unpackaged installation.
The launcher must exist when registering. If the application is moved later,
pass the original launcher path when unregistering so the exact registered
schema can be compared safely.

`scripts/uninstall_windows.ps1` invokes this same unregister operation before
removing application files. A conflicting/customized tree therefore blocks the
uninstall instead of being deleted or orphaned silently; inspect it and resolve
the conflict explicitly before retrying. `-WhatIf` propagates through both
scripts.

## Protocol contract

The only accepted form is:

```text
codex-router://open?path=<percent-encoded-absolute-local-existing-path>
```

The launcher converts it to the same single path argument used by Explorer.
It rejects unsupported actions and parameters, credentials, ports, fragments,
duplicate parameters, extra process arguments, relative/UNC/device paths,
alternate data streams, traversal components, missing targets, invalid UTF-8,
CR/LF/NUL/quote injection, and links larger than 4 KiB. Ampersands, percent
characters, spaces, and Unicode in a valid filename remain one literal process
argument. Other arguments, including the official `codex://` protocol, are
preserved and never claimed by this registration.

## Automated verification

`tests/windows/Test-ShellIntegration.ps1` uses an in-memory registry adapter;
it never writes the real registry. It covers idempotence, `-WhatIf`, quoting,
namespace isolation, feature-by-feature disable, conflicts, and
compare-and-delete. Go unit and launcher integration tests cover URI parsing,
injection rejection, official-protocol preservation, and the final child argv.
