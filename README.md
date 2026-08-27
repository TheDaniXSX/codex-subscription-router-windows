# Codex Subscription Router for Windows

Use multiple ChatGPT subscriptions from one independent Windows desktop app.

Codex Subscription Router creates a locally patched copy of the official Codex
desktop app, balances new chats across connected subscriptions, and keeps every
thread on one subscription so follow-up turns retain conversation context and
benefit from account-level caching.

The official Codex desktop installation is used only as build input and is never
modified. This repository contains source code and build tooling—not OpenAI
binaries or a prebuilt application.

This repository is a Windows-focused fork of
[b-nnett/codex-subscription-router](https://github.com/b-nnett/codex-subscription-router).
The multiplexer and injected account experience come from that project; the
launcher, isolation, patcher, installer, packaging, and test pipeline here are
the Windows port.

> [!WARNING]
> This is an unofficial, version-sensitive project. It is not affiliated with
> or supported by OpenAI. Review the source and ensure your use complies with
> the terms governing every connected subscription.

> [!IMPORTANT]
> Version 0.2.0 is a source-only Windows preview. Automated qualification is
> extensive, but the final real-account, Appshots, Computer Use, signed MSIX,
> and clean-VM gates remain open. It is not a stable Windows support claim.

## Highlights

- **Quota-aware routing.** New chats favour weekly allowance that will expire
  sooner, with a bounded boost for accounts holding banked usage resets.
- **Sticky conversations.** Once a thread is assigned, every follow-up returns
  to the same subscription unless that subscription is depleted.
- **Automatic failover.** A depleted thread continues through another account
  with quota; if the whole pool is empty, the app shows one combined alert.
- **Native account management.** The profile-menu implementation covers pooled
  usage, masked identities, labels, enable/disable, logout, removal, recovery,
  and device-code sign-in. Its lifecycle contracts have synthetic coverage;
  the final 0.2.0 live-account qualification remains pending.
- **Account-aware settings.** Profile statistics can be viewed together or per
  subscription, while the Plugins page can switch Apps and MCP connections
  between accounts.
- **Per-account reset preview.** The native rate-limit sheet can display an
  account picker and confirmation state. Consuming a real reset is not part of
  automated testing and remains unqualified until explicitly authorized.
- **Windows-native isolation.** The launcher isolates the Electron profile,
  runtime caches, logs, account homes, process tree, and local control API.
- **Preserved Computer Use and gated Appshots components.** The OpenAI helpers
  are preserved byte-for-byte and checked against a static Windows contract.
  Appshots is off by default and accepts only the explicit opt-in value `1`.
  Live Windows qualification is tracked separately and is not implied by these
  static checks.

## How it works

The patched desktop still opens one app-server connection. A small Go
multiplexer fans that connection out to one official Codex child per account.
Each child has an isolated Codex home, while the multiplexer records the owner
of every thread.

```text
Codex Subscription Router (Windows)
        │
        │ one app-server connection
        ▼
    codex-mux
    ├── Primary        → %USERPROFILE%\.codex
    ├── Subscription 2 → isolated Codex home
    └── Subscription 3 → isolated Codex home
             │
             └── thread ID → persistent account owner
```

New-thread routing compares the quota burn rate needed before each weekly reset,
then applies a capped banked-reset boost. Short-window usage, pinned-thread
count, and stable account order break close results. Existing threads do not
migrate merely for load balancing.

Read the [Windows architecture](docs/WINDOWS-ARCHITECTURE.md),
[implementation plan](docs/WINDOWS-IMPLEMENTATION-PLAN.md), and
[security model](docs/WINDOWS-SECURITY.md) for the full design.

## Compatibility

Codex Subscription Router currently targets:

| Component | Supported value |
| --- | --- |
| Platform | Windows 10/11 x64 |
| Official Store package | `26.820.9563.0` |
| Inner desktop version/build | `26.820.71523` / `7226` |
| Bundled Codex CLI | `0.150.0-alpha.8` |
| Go | 1.26 or newer |
| Node.js | 22.12 or newer |
| Python | 3.10 or newer |

The patcher verifies the official version, build, ASAR hash, renderer anchors,
and source executable signatures before changing anything. An unknown upstream
build is rejected by default rather than being partially patched.

## Requirements

- The official `OpenAI.Codex` Microsoft Store app
- Go 1.26+
- Node.js 22.12+ and npm
- Python 3.10+
- PowerShell 5.1+ (PowerShell 7 recommended)

## Install

Clone the Windows fork, inspect the installer, and run it as the current user:

```powershell
git clone https://github.com/TheDaniXSX/codex-subscription-router-windows.git
Set-Location .\codex-subscription-router-windows
npm ci --ignore-scripts
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install_windows.ps1
```

The installer treats `WindowsApps` as immutable input, builds in staging,
verifies the result, creates a recoverable backup on updates, and installs the
independent copy under `%LOCALAPPDATA%\Programs\Codex Subscription Router`.
It never stops or unregisters the official app.

> [!TIP]
> Start with `-DryRun` to validate the exact installed Store build and all ASAR
> anchors without installing or launching the router.

### Install via prompt

> Install Codex Subscription Router from `https://github.com/TheDaniXSX/codex-subscription-router-windows` on Windows using `scripts/install_windows.ps1`, without modifying or restarting the official Codex app or deleting existing router state. Run the offline tests and full build verifier before launching the independent copy.

### Install from a clone

```powershell
git clone https://github.com/TheDaniXSX/codex-subscription-router-windows.git
Set-Location .\codex-subscription-router-windows
npm ci --ignore-scripts
python .\scripts\patch_windows_app.py --dry-run
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install_windows.ps1
```

This creates:

- `%LOCALAPPDATA%\Programs\Codex Subscription Router` — application copy
- `%LOCALAPPDATA%\Programs\Codex Subscription Router Data` — state, profile, logs and accounts
- a per-user Start Menu shortcut

See [INSTALL-WINDOWS.md](docs/INSTALL-WINDOWS.md) for custom paths, dry-run,
verification, update and rollback commands.

## Add subscriptions

1. Open the profile menu at the bottom of the sidebar.
2. Select **Add another subscription**.
3. Complete the displayed device-code sign-in in your browser.
4. Return to Codex Subscription Router and wait for the account row to appear.

While the code is visible, clicking away does not dismiss the menu. Clicking
the code copies it and opens the verification page.

The profile menu displays combined weekly usage followed by one row per
subscription. Email addresses remain masked until hovered. The final row always
starts another sign-in.

## Routing behavior

| Situation | Behaviour |
| --- | --- |
| New chat | Assigned by quota-at-risk, banked resets, and short-window pressure |
| Follow-up | Sent to the thread's persisted account owner |
| Owner depleted | Continued through another account with capacity |
| Every account depleted | Combined quota alert with the next known reset |
| Account disabled | Excluded from routing and pooled usable quota |

The subscription assigned to the current thread appears in its pinned summary.

## Profiles, plugins, and resets

**Profile statistics** begin in a combined view with overlapping account
photos. Select a photo to see only that subscription's identity and statistics;
select it again to return to the combined view.

**Settings → Plugins** includes a subscription picker. Plugin definitions and
managed MCP configuration are shared, while Apps, connection status, and OAuth
login are scoped to the selected subscription.

**Rate-limit resets** remain native to the app, with an account picker added to
the sheet. Synthetic tests verify account-scoped preview and confirmation
routing without consuming a credit. A real reset consumption has not been
qualified for 0.2.0 and must not be attempted without explicit authorization.

## Update or rebuild

The copied app's updater is disabled so a Store update cannot overwrite the
patch. After the official app updates, wait until the new hashes and anchors
have been reviewed, close only the router copy, then rebuild:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install_windows.ps1 -Force
```

Existing destinations are moved to timestamped directories beside the install
root. Account state remains outside the application and is preserved. The
installer refuses to replace a running router and never terminates it for you.

## Local data and security

| Path | Purpose |
| --- | --- |
| `%USERPROFILE%\.codex` | Primary credentials, conversations, and cache |
| `%LOCALAPPDATA%\Programs\Codex Subscription Router Data\state.json` | Account metadata and sticky ownership |
| `%LOCALAPPDATA%\Programs\Codex Subscription Router Data\accounts\<id>\codex-home` | Secondary account data |
| `%LOCALAPPDATA%\Programs\Codex Subscription Router Data\control-token` | Loopback API token |
| `%LOCALAPPDATA%\Programs\Codex Subscription Router Data\Profile` | Independent Electron profile |
| `%LOCALAPPDATA%\Programs\Codex Subscription Router Data\runtime-cache` | Independent CLI/runtime cache |
| `%LOCALAPPDATA%\Programs\Codex Subscription Router Data\logs` | Router and desktop logs |

The control service binds only to `127.0.0.1` and protects private routes with a
random 256-bit token supplied only in a request header. OAuth tokens stay
inside their account's Codex home and are never returned by the control API.
Windows DACLs restrict the state tree to the current user and LocalSystem.

Plugin configuration is intentionally synchronized from the Primary account.
Inline secrets inside shared MCP configuration are therefore copied to each
isolated account home; the account homes are not separate secret boundaries.

See [SECURITY.md](SECURITY.md) before reporting a credential, signing, or local
control-service issue.

## Development and verification

```powershell
npm ci --ignore-scripts
$env:Path = 'C:\Program Files\Go\bin;' + $env:Path
npm run check:windows
npm run smoke:windows
```

The Go backend and injected renderer have no runtime third-party dependencies.
`@electron/asar` is build-only. Deterministic UI preview routes are enabled only
when `CODEX_MUX_UI_TESTS=1` is present at launch and remain token-authenticated.

The Windows test procedure is in
[tests/windows/SMOKE-TEST.md](tests/windows/SMOKE-TEST.md), and release controls
are documented in [WINDOWS-RELEASE.md](docs/WINDOWS-RELEASE.md). The separate
[read-only doctor and resource-soak guide](docs/WINDOWS-DIAGNOSTICS.md) explains
how to collect a redacted report without stopping either desktop. The separate
[Appshots/Computer Use qualification](docs/WINDOWS-CAPABILITY-QUALIFICATION.md)
defines the non-interactive contract gate and the remaining VM checklist.

## Known limitations

- Upstream Codex desktop updates can require new, reviewed patch anchors.
- The router uses the local stdio Codex app-server contract. OpenAI documents
  remote Code Mode and its WebSocket transport as experimental, so this preview
  deliberately avoids that transport and still pins every supported desktop/app-
  server build exactly. See the [official app-server documentation](https://learn.chatgpt.com/docs/app-server).
- The initial merged history fetch is limited to 500 threads per account.
- Combined “skills explored” totals can count the same skill once per account
  because the upstream profile response exposes counts rather than skill IDs.
- The inherited Chrome Native Messaging paths remain disabled and the official
  registration is preserved unchanged. An independent Manifest V3 extension,
  native host, fixture-tested installer/uninstaller, and explicit release gate
  now live in this repository; they remain opt-in and cannot be advertised as
  parity until the extension is published and the signed clean-VM E2E passes.
  See [CHROME-CONNECTOR-RELEASE.md](docs/CHROME-CONNECTOR-RELEASE.md).
- Windows Appshots is present in the upstream package, remains experimental,
  and is disabled unless `CODEX_ROUTER_ENABLE_APPSHOTS=1` is supplied explicitly.
- Computer Use passes static layout, provenance, transport and bounds checks,
  but is not a supported Windows release claim until the documented native
  Calculator/process/isolation E2E passes for the exact release candidate.
- The local unpackaged launcher is unsigned. A redistributable MSIX requires a
  separate trusted publisher certificate and its own package identity.
- Releases are source-only; patched OpenAI binaries are never distributed.

## Contributing and releases

Read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting changes. Windows
releases follow the source-only process in
[WINDOWS-RELEASE.md](docs/WINDOWS-RELEASE.md) and require a completed smoke test
for the exact commit.

## License

Project source is available under the [MIT License](LICENSE). ChatGPT, Codex,
and the official Windows application are OpenAI products and are not covered
by this license.
