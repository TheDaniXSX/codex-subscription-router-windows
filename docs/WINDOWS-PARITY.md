# Windows feature-parity specification

This document is the normative parity contract for the Windows port of Codex
Subscription Router. It describes the user-visible outcome, the implementation
boundary, and the evidence required before a Windows build may be described as
working. The macOS implementation is the behavioral baseline; Windows does not
need to reproduce macOS APIs when the same result can be achieved with a safer
Windows-native mechanism.

The project is unofficial. The official Codex application is build input only,
must remain unmodified, and must continue to work independently. Source releases
must not contain a patched app, an official ASAR, OpenAI executables, credentials,
or user data.

## Status vocabulary

| Status | Meaning |
| --- | --- |
| `Portable` | Existing Go or renderer behavior should be reused without a semantic change. |
| `Adapt` | Existing behavior is retained but paths, process handling, UI anchors, or OS APIs change on Windows. |
| `Replace` | The macOS mechanism has no Windows equivalent and needs a Windows-native implementation. |
| `Verify` | It may already work in the official Windows build, but parity is not claimed until exercised end to end. |
| `Gap` | The inherited mechanism is deliberately disabled because it would collide with official state; parity requires a new independent implementation. |
| `N/A` | The macOS mechanism is inapplicable; the equivalent Windows outcome is listed separately. |

Priorities are `P0` (required for a safe usable router), `P1` (required for full
feature parity), and `P2` (release hardening). A developer preview may stop after
P0. A generally usable Windows release must pass P0 and P1; no release artifact
may be published before P2 lifecycle and security gates pass.

## Windows identity and path contract

The exact packaging mechanism may be an independently signed per-user MSIX or an
independently installed unpackaged Electron app. Either choice must satisfy the
same identity and isolation contract.

| Logical location | Default | Contract |
| --- | --- | --- |
| Official source | Discovered from the installed `OpenAI.Codex` package | Resolve through package metadata; never hard-code or write to `WindowsApps`. |
| Router install | `%LOCALAPPDATA%\Programs\Codex Subscription Router` or an independent per-user MSIX location | Must not overlap the official package or its mutable data. |
| Router desktop profile | `%LOCALAPPDATA%\Programs\Codex Subscription Router Data\Profile` | Independent Electron/Chromium profile and single-instance scope. |
| Router state root | `%CODEX_MUX_STATE_ROOT%`, default `%LOCALAPPDATA%\Programs\Codex Subscription Router Data` | Stable across rebuilds; protected by user-scoped ACLs and outside packaged-app LocalAppData virtualization. |
| Primary Codex home | `%CODEX_HOME%`, default `%USERPROFILE%\.codex` | Uses the account currently authenticated in the user's Codex home; the installer must not overwrite it. |
| Secondary homes | `%CODEX_MUX_STATE_ROOT%\accounts\<id>\codex-home` | One credential, database, cache, and OAuth namespace per subscription. |
| Backups | `%CODEX_MUX_STATE_ROOT%\backups` | Timestamped, recoverable, and outside the installed app. |
| Build staging | Repository `.artifacts` or a user-scoped temporary directory | Never committed; removed only after path validation. |

Changing a default requires a documented migration and rollback path. Environment
variables must be expanded once, canonicalized, and checked against the expected
root before any recursive copy, move, or deletion.

## Executive parity matrix

| Capability | macOS baseline | Windows target | Status | Priority | Primary evidence |
| --- | --- | --- | --- | --- | --- |
| Official app protection | Copy `/Applications/ChatGPT.app`; never patch it in place | Read the installed Codex package, stage a separate application, and leave package files, registration, profile, and updater untouched | Adapt | P0 | Pre/post hashes and package metadata match |
| Independent identity | Separate bundle ID, display name, profile, single-instance scope, and URL scheme | Separate package/application identity, AUMID where applicable, display name, profile, mutex/single-instance scope, shortcuts, and URI scheme | Replace | P0 | Both official and router apps run concurrently |
| Multiplexer replacement | Rename copied `codex` to `codex.real`; install `codex-mux` at the expected path | Preserve copied `codex.exe` as `codex.real.exe`; install the Windows mux at the exact executable path expected by the copied desktop | Adapt | P0 | Transparent-proxy smoke test |
| Child app servers | One official `app-server` child per account with isolated homes | Same, with Windows environment blocks, quoting, handle inheritance, process groups/job object, and tree shutdown | Adapt | P0 | Process-tree integration test |
| Quota-aware routing | Weekly quota urgency, bounded reset-credit boost, short-window pressure, thread count, stable order | Same algorithm and deterministic tie-breaking | Portable | P0 | Shared table-driven fixtures produce identical winners |
| Sticky threads | Persist `thread ID -> account ID`; follow-ups return to the owner | Same, with atomic durable state and concurrent-transition protection | Portable | P0 | Restart and concurrent-turn tests |
| Automatic failover | Preflight an exhausted owner; retry a usage-limit response through a viable account and resume history | Same, attempting each enabled account at most once and committing the new owner only after successful resume | Portable | P0 | Preventive and reactive failover E2E |
| All-depleted state | One combined alert and next known reset | Same native alert wording and earliest useful reset information | Portable/Adapt | P0 | Deterministic depletion preview and live UI test |
| Account management | Primary plus isolated secondary accounts; label, enable, login, logout | Same; device-code login must use the system browser and return safely to the router | Adapt | P0 | Two-account login/logout E2E |
| Pooled usage | Aggregate enabled connected subscriptions and update on child notifications | Same; partial/unknown data must be explicit and must not look like available quota | Portable | P0 | Unit and UI state tests |
| Combined profile | Merge profile statistics; toggle combined/per-account via overlapping avatars | Same visual and statistical behavior using anchors from the approved Windows build | Adapt | P1 | Screenshot and aggregation fixtures |
| Scoped plugins/Apps/MCP | Share definitions; scope connection status and OAuth RPC to selected subscription | Same; private account marker is removed before forwarding to strict app-server RPC | Portable/Adapt | P1 | Per-account OAuth/status E2E |
| Per-account resets | Select an account, view credits, and consume only its reset | Same, with idempotent redemption and no speculative live consumption in tests | Portable/Adapt | P1 | Fake endpoint plus deliberate live test |
| Merged history | Fetch up to 500 threads per child, learn ownership, merge by activity | Same; partial failures are observable and duplicates are deterministic | Portable | P0 | Multi-account pagination test |
| Thread attribution | Show the owning subscription in the thread summary | Same in the Windows renderer | Adapt | P1 | Screenshot plus owner-change event test |
| Appshots | Signed copied app opens the native picker and inserts a selected capture | Preserve the official Windows capture path, attachment action, shortcut, cancellation, and insertion | Verify/Adapt | P1 | Real multi-monitor/DPI E2E |
| Computer Use | Independent signed helper with its own socket and privacy grants | Preserve official Windows CUA components under the router identity; use documented Windows IPC and permissions without bypasses | Replace/Verify | P1 | Calculator click/type E2E and process audit |
| Chrome connector / Native Messaging | Official Codex owns host `com.openai.codexextension` and its extension registration | Keep inherited paths disabled; use the independent opt-in extension/host and its publication/signing/E2E gate | Partial / release-gated | P0 safety / P1 parity | Fixture lifecycle now; published-ID, signed-host and clean-VM desktop-consumption E2E before parity |
| Self-update behavior | Disable the copied app updater; rebuild from an approved official version | Disable only the router's inherited official updater; provide a router compatibility/update workflow | Adapt | P0 | Network/process observation and update test |
| Deep-link protocol | Replace `codex://` with `codex-subscription-router://` | Register a dedicated per-user scheme; validate and forward only supported deep links | Replace | P1 | URI matrix and injection tests |
| Finder/Explorer integration | Preserve useful desktop integration under the independent app identity | Register optional Explorer verbs/“Open with” entries without taking ownership from official Codex | Replace | P1 | Path/quoting/uninstall tests |
| Install/update/revert | Staged build, signature verification, timestamped backup, atomic replacement | Transactional PowerShell workflow with transcript, visible progress for long/elevated steps, health check, rollback, and state preservation | Replace | P0 | Failure-injection lifecycle E2E |
| Security boundary | Loopback API, 256-bit token, strict origin, owner-only files, no token exposure | Same plus Windows ACLs, reparse-point defense, Authenticode/package validation, safe registry writes, and redacted Windows logs | Adapt | P0 | Automated negative security suite |

## Detailed functional requirements and acceptance criteria

### 1. Build input, compatibility, and independence

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-BLD-001 | Discover the current official Codex package from Windows package metadata and record package name, version, architecture, publisher, install path, executable versions, ASAR SHA-256, and every patched anchor. | A baseline report is generated from a read-only scan and agrees with `Get-AppxPackage`/package manifest data. |
| WIN-BLD-002 | Fail closed on an unknown version, architecture, ASAR hash, anchor count, native constant, or manifest structure. Diagnostic overrides must be explicit and must not produce an installable release artifact. | Mutating any one expected value causes staging to stop before the destination is touched. |
| WIN-BLD-003 | Copy to a temporary staging root, patch only the copy, verify it fully, then install/replace. | Source hashes before and after are identical; an interrupted build leaves the existing router usable. |
| WIN-BLD-004 | Preserve ASAR unpack rules and recalculate every integrity field used by the Windows desktop build. | `better-sqlite3` and all other native modules load; ASAR integrity verification succeeds on cold start. |
| WIN-BLD-005 | Give the router a distinct display identity, application identity/AUMID, desktop profile, single-instance key, notification identity, shortcuts, and URI registration. | Starting both apps creates separate processes and profiles; closing or updating either does not affect the other. |
| WIN-BLD-006 | Sign modified PE files and the package/installer consistently when the chosen distribution mode supports signing. Never claim the copied binaries retain OpenAI's signature. | Signature inventory identifies upstream unmodified inputs and router-modified outputs accurately; verification fails on tampering. |
| WIN-BLD-007 | Generated builds are user- and upstream-version-specific and are never checked into Git or attached to a source release. | CI rejects tracked `app.asar`, `.exe`, `.dll`, `.msix`, credentials, state, and staged output except explicitly licensed test fixtures. |

### 2. App-server transport and process lifecycle

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-RPC-001 | The renderer keeps one newline-delimited JSON-RPC app-server connection to `codex-mux`; the mux owns one real child per configured account. | A transparent single-account run produces the same initialize response and normal chat behavior as the official app. |
| WIN-RPC-002 | Preserve numeric and string request IDs, rewrite child-originated server request IDs uniquely, and route approvals/responses back to the originating child. | Colliding child request IDs cannot cross accounts in a concurrency test. |
| WIN-RPC-003 | Initialize all available children with identical client parameters, replay `initialized` for late children, and keep one controller account for global methods. | Adding an account after startup makes it usable without restarting the desktop. |
| WIN-RPC-004 | Do not inherit unintended handles. Capture child stdin/stdout/stderr deliberately and prevent a child from attaching to the user's terminal. | Handle inspection finds no unrelated inheritable handle; GUI launch shows no console window. |
| WIN-RPC-005 | Place router children in a managed Windows process tree and terminate only that tree on router shutdown. Never terminate official Codex processes by image name alone. | Normal exit and forced parent termination leave no router child; official Codex stays running. |
| WIN-RPC-006 | Bound queues, request bodies, response bodies, line length, timeouts, and shutdown waits. Apply backpressure or return a clear error rather than consuming unbounded memory. | Oversized/malformed/flood tests remain within documented CPU/memory bounds and do not crash the desktop. |
| WIN-RPC-007 | Preserve cancellation, approval, hook, thread, turn, item, and raw-response notifications for the owning account while controller-only global notifications remain single-sourced. | Notification fixtures have neither duplicates nor missing lifecycle events. |

### 3. Routing and sticky ownership

The routing score remains the macOS algorithm:

1. Exclude disabled, disconnected, non-ChatGPT, explicitly excluded, and
   weekly-depleted accounts.
2. Calculate weekly remaining allowance divided by hours until reset, bounded by
   a one-minute minimum horizon. Use the advertised window duration or seven days
   when the reset is unknown.
3. Boost urgency by 15% per known banked reset, capped at three credits.
4. Sort descending by urgency, then ascending by short-window usage, weekly
   usage, pinned-thread count, and account creation time.
5. Treat reset-credit lookup failure as neutral. Fetch credit metadata in
   parallel, cache success for five minutes, and bound lookup latency.

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-RTE-001 | Route only `thread/start` through the new-thread scorer. Do not migrate an existing thread merely to rebalance load. | Golden fixtures match macOS choices, including ties, missing windows, resets in the past, and unknown credits. |
| WIN-RTE-002 | Learn and persist ownership from `thread/start`, `thread/started`, `thread/fork`, `thread/resume`, `thread/unarchive`, and merged history. | Each creation/resume path populates `state.json`; a desktop restart retains it. |
| WIN-RTE-003 | Route all requests carrying `threadId`/`thread_id` to the owner; use the controller only when a request is genuinely global or ownership is unavailable. | Two simultaneous active threads on different accounts never exchange requests or approvals. |
| WIN-RTE-004 | Persist state through an atomic same-volume replace, flush before commit where supported, retain the previous valid state, and serialize mutations. | Power-loss/fault-injection tests yield either the old or new complete JSON, never truncated state. |
| WIN-RTE-005 | Serialize failover for the same thread. Resume the source history on the target and change ownership only after target resume succeeds. | Concurrent turns cannot produce two owners; a failed resume leaves the old owner recorded. |
| WIN-RTE-006 | Perform preventive failover when the known owner is depleted and reactive failover when `turn/start` returns a recognized usage-limit error. | Both paths continue the same conversation on another account without a duplicate user turn. |
| WIN-RTE-007 | Maintain an exclusion set across retries and try each eligible account no more than once. Do not classify unrelated authentication, network, model, policy, or tool errors as quota depletion. | Error-classification tests prove only known usage-limit shapes trigger failover. |
| WIN-RTE-008 | If no account can continue, return one combined depletion error with the next useful known reset and emit one UI state change. | All-depleted tests show one alert, no retry loop, and no ownership mutation. |
| WIN-RTE-009 | Disabling an account excludes it from new routing and pooled usable quota. Its history stays readable; a new turn on an owned thread must fail over or produce a clear unavailable/depleted result. | Disable/enable round trip preserves credentials and history and never assigns a new thread while disabled. |
| WIN-RTE-010 | Surface route and failover reasons without credentials or prompt content. | Event/log inspection exposes account ID/label, score inputs, thread ID, and transition only. |

### 4. Accounts, authentication, usage, and profile

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-ACC-001 | On first launch, create exactly one `Primary` record pointing to the current primary Codex home; never copy, replace, or log its authentication data. | The account shown by the router matches the account currently returned by the primary app-server. |
| WIN-ACC-002 | Add secondary records with random IDs and isolated Codex homes. Synchronize only the managed shared configuration allowed by the security model. | Authenticating account B cannot change account A's `account/read`, database, cache, or OAuth token. |
| WIN-ACC-003 | Support `chatgpt` and `chatgptDeviceCode` login through the real child. Device-code copy/open behavior must use HTTPS and a trusted host allowlist. | Two different subscriptions can be connected from the native menu in one session. |
| WIN-ACC-004 | Show deterministic loading, connected, logged-out, disabled, child-unavailable, and partial-error states. Mask email by default and reveal only on deliberate hover/focus. | UI automation covers every state; error text contains no token, raw auth payload, or full email in logs. |
| WIN-ACC-005 | Support label edits, enable/disable, and logout. Logout removes credentials only through the selected child and does not delete routing/history metadata. | Logging out B leaves A connected and all application state readable. |
| WIN-ACC-006 | Account removal is not present in the macOS baseline and is not required for parity. If added later, it must be a separate explicit destructive action with an export/backup and exact-target confirmation. | No current UI action silently deletes an account home. |
| WIN-USE-001 | Aggregate rate-limit reads across enabled connected ChatGPT subscriptions and re-aggregate on child update notifications. | A change in either child's usage is reflected without restarting. |
| WIN-USE-002 | Match the baseline calculation: average used percentage for like windows, longest duration, earliest reset, and depleted only when no eligible account has capacity. | Table-driven mixed-window fixtures match expected values exactly. |
| WIN-USE-003 | Do not interpret missing/error data as zero usage. Preserve usable partial data and label it partial/unknown. | Taking one mock profile endpoint offline never displays a falsely healthy pool. |
| WIN-USE-004 | Display combined weekly usage followed by per-account usage, plan label, profile image, masked email, and thread count. | Two-account screenshot matches the approved Windows reference at 100%, 125%, 150%, and 200% scaling. |
| WIN-PRF-001 | Combined profile sums lifetime/thread/skill counts, merges daily and weekly buckets, recomputes streaks, weights percentages, merges top invocations, and uses the latest stats timestamp. | Existing aggregation unit fixtures and Windows locale/time-zone fixtures pass. |
| WIN-PRF-002 | Profile opens combined, displays overlapping 20 px avatars, hides a misleading single identity, toggles to one account on selection, and toggles back on reselection. | Keyboard, mouse, and screenshot tests pass; the selected account remains visually unambiguous. |
| WIN-PRF-003 | Mark a combined profile partial if one account fails and never substitute another account's identity for the selected account. | Partial-response and selected-account-error UI tests pass. |

### 5. Plugins, Apps, MCP, and shared configuration

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-PLG-001 | Share plugin/marketplace/skill definitions and managed MCP configuration from Primary while excluding auth-store selection, credentials, local OAuth material, databases, logs, caches, and project trust. | A manifest-level allow/deny fixture proves prohibited paths are never copied. |
| WIN-PLG-002 | Synchronize safely and atomically without following junctions/symlinks/reparse points outside either Codex home. Preserve Windows path syntax and file ACLs. | A malicious reparse-point test cannot read or overwrite a file outside the test roots. |
| WIN-PLG-003 | Add a subscription picker to Settings -> Plugins. Scope Apps lists, installed Apps, App reads, MCP server status, and MCP OAuth login to that account. | Selecting A and B displays their distinct connection states without a desktop restart. |
| WIN-PLG-004 | Add a private account marker only in the renderer-to-mux request and strip it before forwarding strict RPC parameters to the child. Reject unknown/disabled account IDs. | Child RPC golden tests contain no private marker; tampered IDs cannot route to another account. |
| WIN-PLG-005 | Keep plugin installation/definition changes shared but connection credentials per account. Document that inline secrets in shared definitions are copied and are therefore not isolated between account homes. | Editing a shared definition reaches both homes; logging in on B does not authenticate A. |
| WIN-PLG-006 | Preserve plugin permissions, connection prompts, and uninstall behavior from the approved official Windows build. | A representative local MCP and OAuth App complete connect, use, disconnect, and reconnect on both accounts. |

### 6. Usage-reset credits

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-RST-001 | Show a subscription picker in the native usage/reset sheet and key its query cache by account ID. | Switching accounts changes displayed balances and never shows stale credits from the previous account. |
| WIN-RST-002 | Fetch and consume credits with only the selected account's access token and ChatGPT account ID. Never return tokens through the control API. | A two-endpoint test server proves headers and mutations belong to the selected account only. |
| WIN-RST-003 | Preserve the upstream `redeemRequestId`/idempotency behavior and correctly handle `reset` and `already_redeemed`. Do not automatically retry an ambiguous consume response with a new ID. | Timeout-after-commit simulation does not consume a second credit. |
| WIN-RST-004 | Cache successful metadata for five minutes and errors briefly; invalidate after confirmed consumption and publish an account refresh. | Fake-clock tests validate cache hit, expiry, invalidation, and UI refresh. |
| WIN-RST-005 | Deterministic reset preview endpoints exist only with `CODEX_MUX_UI_TESTS=1`, remain authenticated, and can never fall through to the live consume endpoint. | Release-mode 404/absence test and preview-redemption isolation test pass. |

### 7. History and thread attribution

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-HIS-001 | Query children in parallel, paginate defensively, cap the initial import at 500 threads per account, detect repeated cursors, and bound each request. | 2 x 500 synthetic histories merge within the performance budget and terminate on a repeated cursor. |
| WIN-HIS-002 | Merge by `updatedAt`, falling back to `createdAt`, with deterministic stable ordering and a documented duplicate-ID policy. | Shuffled fixtures always produce the same result and ownership. |
| WIN-HIS-003 | Learn ownership from the account that returned each thread before it becomes interactive. Never overwrite a known owner from an ambiguous duplicate silently. | Duplicate fixture produces a visible diagnostic and the documented deterministic owner. |
| WIN-HIS-004 | A failure in one account must not corrupt results from others. The UI must indicate partial history rather than silently implying completeness. | One-child timeout still shows the other child's history and a non-blocking warning. |
| WIN-HIS-005 | Route read, resume, fork, archive/unarchive, approval, and turn operations consistently to the owner and update ownership for returned thread IDs. | Full thread lifecycle tests pass independently on two accounts. |
| WIN-HIS-006 | Show the account label/photo/plan in the pinned thread summary and update it after failover. | SSE event changes attribution without reloading the thread. |

### 8. Appshots on Windows

The Windows port must preserve the official Windows implementation rather than
porting ScreenCaptureKit/TCC code. No parity claim is permitted merely because a
renderer menu item is visible.

Current evidence is intentionally limited to the fail-closed static contract in
`WINDOWS-CAPABILITY-QUALIFICATION.md`: two strict opt-in gates, native-bridge
requirement, default-off launcher behavior and synthetic negatives. Mixed-DPI
picker/cancel/insertion/denial E2E remains a release gate, not a completed claim.

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-SHO-001 | Identify every ASAR, native module, helper executable, package capability, and IPC endpoint involved in Appshots in the approved Windows baseline. | The compatibility record lists hashes/versions and a traced successful capture path. |
| WIN-SHO-002 | Keep the attachment-menu action, the official Windows keyboard shortcut, the settings trigger if present, picker cancellation, and attachment insertion. | Each entry point opens one picker; selection inserts the expected image and cancel inserts nothing. |
| WIN-SHO-003 | Work with multiple monitors, negative coordinates, mixed DPI, minimized/occluded windows as supported upstream, and 100%-200% display scaling. | A recorded manual/automated matrix covers at least two monitors and mixed scaling. |
| WIN-SHO-004 | Preserve upstream permission/consent behavior. A denied or unavailable capture API yields a clear recoverable error and no fallback that captures more than requested. | Denial test leaves no image/temp artifact and the app remains usable. |
| WIN-SHO-005 | Store temporary captures under the router's private profile, apply upstream cleanup rules, and exclude captures from diagnostics/backups by default. | Temp cleanup and backup-content tests pass. |

### 9. Computer Use on Windows

Computer Use is security-sensitive. The port must use the official Windows CUA
runtime/components copied from the approved source build and Windows-native IPC.
It must not simulate parity by silently falling back to unrestricted PowerShell,
`cmd.exe`, UIAutomation scripts, SendInput, or another automation path when the
official CUA route is unavailable.

Current evidence covers the preserved tree digest, PE/file types, bounded
manifests, hidden three-pipe transport, named-pipe environment/framing, frame
limits and forbidden-fallback negatives. It is recorded as
`static-contract-only`; Calculator, UAC, process isolation and rebuild E2E remain
open until the VM checklist in `WINDOWS-CAPABILITY-QUALIFICATION.md` is signed.

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-CUA-001 | Inventory `cua_node`, code-mode host, Node runtime, native modules, manifests, callers, IPC, environment variables, and peer/identity checks for the exact Windows build. | Architecture documentation contains a process/IPC trace from request to action. |
| WIN-CUA-002 | Assign router-specific endpoints/profile paths without colliding with official Codex, and preserve caller verification rather than disabling it. | Official and router Computer Use sessions can run sequentially and cannot attach to each other's endpoint. |
| WIN-CUA-003 | Sign/identify every modified caller consistently and preserve Windows capabilities, Defender/SmartScreen visibility, and user consent. Never bypass UAC or secure desktop. | Signature and package-capability audit passes; UAC/secure-desktop action is refused safely. |
| WIN-CUA-004 | If Computer Use is unavailable, report that exact failure; do not launch an alternate automation mechanism. | Removing/stopping the helper produces a clear failure and no shell/automation child process. |
| WIN-CUA-005 | Complete a live task that opens Calculator, clicks controls, types input, reads the result, and returns control without orphan processes. | Screen recording plus process/IPC log demonstrates the native route; no fallback appears. |
| WIN-CUA-006 | Work after a same-identity rebuild without duplicate registrations or repeated permission prompts beyond normal Windows behavior. | Rebuild-and-retest passes and existing official Codex behavior is unchanged. |

### 10. Chrome connector and Native Messaging

The approved Windows baseline uses the Native Messaging host name
`com.openai.codexextension`, registry key
`HKCU\Software\Google\Chrome\NativeMessagingHosts\com.openai.codexextension`,
and manifest `%LOCALAPPDATA%\OpenAI\extension\com.openai.codexextension.json`.
Their extension relationship belongs to official `OpenAI.Codex`. Upstream uses
forced `reg add` and `reg delete`; reusing the name is not isolation because the
router could overwrite the official host on add or remove it on
delete/uninstall.

The port patches both inherited registration directions out of the copied
ASAR. A separate Manifest V3 extension and native host now implement an opt-in
bridge under `io.github.thedanixsx.codex_subscription_router`, with an exact
origin, authenticated loopback hop, bounded private context storage and
fixture-tested compare-and-delete. It remains release-gated until the final
extension ID is published, the host is signed, and clean-VM desktop consumption
passes; source availability alone is not a parity claim.

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-NMH-001 | Identify every add, update, delete, manifest-write, and registry path for `com.openai.codexextension` in the approved baseline. | Compatibility evidence maps both registration directions and records the official key/value state without exposing private browser data. |
| WIN-NMH-002 | Neutralize the baseline's inherited `RJ` manifest paths and `sY`/`oY` add/delete operations, failing closed if any semantic anchor changes; isolate auxiliary `yJ` JSON under router state. | Starting, closing, updating, rolling back, and uninstalling the router leave the official Native Messaging registry key and manifest byte-for-byte/value-for-value unchanged. |
| WIN-NMH-003 | Do not claim Chrome connector parity until the router has its own published extension ID, Native Messaging host name, manifest path, `allowed_origins`, executable target, and user-visible install flow. | Repository gate passes now; release gate requires the published ID, signed host and clean-VM report. |
| WIN-NMH-004 | Register router-owned values per user and uninstall them only after exact ownership/value comparison. Never delete `com.openai.codexextension`. | Fixture E2E covers idempotency, a foreign value, preservation and exact removal; real clean-VM invariance remains gated. |
| WIN-NMH-005 | Mark Chrome/native messaging unavailable or unsupported in the portable build unless explicitly installed, rather than silently falling back to the official host. | Default builds perform no registration; the separate installer requires an exact extension ID and schema-2 router manifests. |

### 11. Updater, deep links, and Explorer integration

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-DES-001 | Remove/disable only the inherited updater initialization in the copied app. The router must never accept and install an official update directly over its patched files. | Update endpoints/processes are not invoked by the router during an observation window; official Codex still updates normally. |
| WIN-DES-002 | Surface the router build version, source Codex version/hash, and compatibility status. An unsupported official update must not break the installed last-known-good router. | Updating official Codex leaves the router launchable and reports “rebuild required/unsupported” accurately. |
| WIN-DES-003 | Register a distinct per-user URI scheme such as `codex-subscription-router://`. Do not take over `codex://` from official Codex. | Windows Default Apps/protocol inspection shows separate ownership. |
| WIN-DES-004 | Parse deep links as structured URIs, allow only documented hosts/actions/parameters, size-bound input, reject credentials and network paths where inappropriate, and pass paths as arguments rather than shell text. | Quoting, spaces, Unicode, `%`, `&`, quotes, traversal, overlength, and command-injection fixtures are safe. |
| WIN-DES-005 | Support single-instance deep-link forwarding into the router's own instance and chosen profile without waking or mutating official Codex. | Closed/running router cases each open exactly one target window. |
| WIN-DES-006 | Add optional per-user Explorer verbs/shortcuts only for supported files/folders. Use an independent icon/name and avoid global file-association takeover. | Open folder, path with spaces/Unicode, multi-select policy, and missing-path cases behave as documented. |
| WIN-DES-007 | Installation/update must be idempotent and remove stale router-only registry entries/shortcuts. Uninstall removes only exact router entries and preserves official Codex. | Registry/shortcut snapshots before install, after reinstall, and after uninstall have no unrelated changes. |
| WIN-DES-008 | Toast notifications, taskbar grouping, jump lists, and Start-menu entries, if present upstream, belong to the router identity and never impersonate official Codex. | Notifications activate the correct app and deep link under both apps installed. |

Estado de implementación unpackaged: WIN-DES-003/004/006 y la parte de
registro de WIN-DES-007 están implementados por
`scripts/windows/Manage-ShellIntegration.ps1` y el launcher. Son opt-in, usan
solo identidad HKCU propia y cuentan con pruebas herméticas de URI, quoting,
inyección, idempotencia, aislamiento y compare-and-delete. La prueba real de
instancia cerrada/abierta de WIN-DES-005 y la invariancia completa de una
instalación/actualización/desinstalación siguen siendo puertas E2E; las pruebas
PowerShell no escriben el registro real.

### 12. Control API and security

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-SEC-001 | Bind the control API only to `127.0.0.1` on the per-installation schema-2 `controlPort`; reject missing, legacy fixed, inconsistent, remote, or interface-wide endpoints. | Sidecar/manifest/ASAR consistency and remote-interface/port-hijack negative tests pass without a `48123` fallback. |
| WIN-SEC-002 | Protect every private route and SSE stream with a random 256-bit token compared in constant time. `/health` may expose only `{ok:true}`. | Missing, malformed, short, and incorrect tokens receive no private data. |
| WIN-SEC-003 | Restrict CORS to the exact router renderer origin, set no-store/nosniff/no-referrer headers, bound headers/bodies/responses, and reject unknown JSON fields/methods. | Browser-origin and fuzz tests pass. |
| WIN-SEC-004 | The control API may return account metadata, aggregated usage/profile data, thread ownership, and operation status, but never access/refresh tokens, raw `auth.json`, secrets, prompt/response content, or arbitrary paths. | Contract tests and a response secret scanner find no prohibited fields. |
| WIN-SEC-005 | Apply explicit ACLs to state, tokens, transcripts, backups, and secondary homes for the current user plus only required Windows principals. Validate/repair ACLs at startup. | A second standard-user test account cannot read them; same-user process limitation is documented. |
| WIN-SEC-006 | Treat junctions, symlinks, mount points, and other reparse points as untrusted during copy, sync, update, backup, restore, and cleanup. | Reparse-point escape tests cannot affect files outside test roots. |
| WIN-SEC-007 | Validate source publisher/package identity and exact hashes before copying. Record that modification invalidates upstream Authenticode signatures and sign resulting artifacts under the router publisher where applicable. | A lookalike package or replaced source executable is rejected. |
| WIN-SEC-008 | Redact bearer tokens, refresh tokens, auth JSON, device codes after use, emails by default, environment secrets, prompts, and MCP inline secrets from logs/crash reports. | Seeded-canary secret scan over all generated logs/backups is clean. |
| WIN-SEC-009 | HTTPS profile images are limited to trusted hosts and bounded by type/size/time. No renderer-supplied arbitrary fetch proxy is allowed. | HTTP, untrusted host, redirect escape, oversized, and invalid-image tests fail closed. |
| WIN-SEC-010 | Diagnostic UI endpoints are compile/runtime gated, token protected, loopback only, conspicuously logged, and absent from release launches. | Release configuration cannot invoke preview or screenshot-control routes. |
| WIN-SEC-011 | Preserve upstream content security policy and add only the minimum loopback control origin. Do not enable Node integration or disable renderer sandboxing as a shortcut. | Electron security checklist and effective CSP inspection pass. |
| WIN-SEC-012 | Document the trust limitation: account homes isolate accidental cross-routing, not malicious code executing as the same Windows user; shared inline MCP secrets are intentionally replicated. | Security documentation and UI help do not overstate isolation. |

## Install, update, rollback, and uninstall contract

### Installation transaction

1. Acquire one global per-user installer lock—independent of destination/state
   choices—and detect any previous incomplete transaction.
2. Discover and inventory the official package read-only.
3. Check tools, free disk space, path lengths, architecture, source compatibility,
   signing configuration, and destination ownership. Before any state log/token,
   create StateRoot with inheritance disabled and only the current user plus SYSTEM
   holding FullControl; read the DACL back and fail if it differs.
4. Build in a new staging directory. Never stop or restart official Codex and do
   not launch the new router while the current Codex-hosted implementation agents
   depend on it.
5. Run static, unit, integration, ASAR, executable, manifest, signature, and
   staged-tree checks; compare the official Native Messaging key/manifest before
   and after without invoking its host. Clear state-root environment overrides and
   require launcher self-test output to resolve the exact persisted sidecar/root.
6. Stop only router processes using their recorded PID/executable path/package
   identity. Long or elevated operations must show progress in a visible console,
   save a transcript, and close when complete.
7. Move an existing router app to a timestamped backup, install the verified
   staged build atomically where possible, register router-only integration, and
   launch a bounded health/smoke check.
8. If health fails, unregister the failed build, restore the previous app and
   registrations, preserve the failed staging/logs for diagnosis, and leave account
   state untouched.

### Update contract

- The official application updates independently. A router update is a reviewed
  rebuild against an explicitly approved official version and ASAR hash.
- The installed last-known-good router remains runnable if the official package
  becomes newer or incompatible.
- Before a state-schema migration, create a timestamped state backup. Migrations
  are versioned, forward-only during startup, and must have a tested rollback/read
  strategy before release.
- Reuse the same Windows publisher/certificate and application identity when
  applicable so shortcuts, URI handlers, permissions, and update semantics remain
  stable. A publisher change is an explicit migration, never an automatic choice.
- Old app backups may be pruned only by an explicit retention policy after the
  replacement passes the complete smoke test. Credentials and conversation data
  are never part of automatic app-backup pruning.

### Rollback and uninstall contract

| ID | Requirement | Acceptance criterion |
| --- | --- | --- |
| WIN-LCM-001 | `install` and `update` are idempotent and recover from interruption at every transaction boundary. | Failure injection after each numbered install step restores a launchable old version or leaves no partial first install. |
| WIN-LCM-002 | `rollback` selects an exact recorded backup, verifies its manifest/hashes/signature, and restores app plus router-only registrations without changing state. | A real upgrade can roll back and reopen existing accounts/threads. |
| WIN-LCM-003 | `uninstall` removes app files, router processes, exact router-owned registry entries, shortcuts, scheduled tasks/services created by this project, and optionally build cache. It preserves state by default and never removes `com.openai.codexextension`. | Official Codex, its Native Messaging registration, and `%USERPROFILE%\.codex` are byte-for-byte/value-for-value unaffected. |
| WIN-LCM-004 | Credential/state purge is a separate explicit command displaying exact resolved paths and backup implications. It refuses roots, profile roots, unresolved variables, globs, and reparse-point escapes. | Destructive-path test suite rejects every broad/ambiguous target. |
| WIN-LCM-005 | Every lifecycle run writes a redacted structured result plus human-readable transcript including time, previous/new version, paths, hashes, backup, checks, and rollback result. | A failed and successful run are independently auditable without exposing secrets. |

## Verification strategy and release gates

### Automated test layers

| Layer | Minimum gate |
| --- | --- |
| Go unit | `go test ./...` on Windows with routing, state, aggregation, reset, account scoping, RPC-ID, and error-classification coverage |
| Go static | `gofmt` clean, `go vet ./...`, vulnerability scan, and race tests where the supported Windows toolchain permits them |
| Renderer | JavaScript syntax/lint checks plus DOM/component tests for account, profile, plugin, reset, partial/error, and thread attribution states |
| Python/PowerShell | Syntax, formatter/linter, Pester tests for discovery/lifecycle/registry/ACL/path safety, and failure-injection tests |
| ASAR compatibility | Exact anchor-count, version/hash, unpack layout, integrity, CSP, updater removal, both Native Messaging add/delete neutralizations, and “already patched” rejection tests |
| Security | Loopback auth/CORS/body bounds, secret canaries, ACLs, reparse points, URI/Explorer/Native-Messaging isolation, package publisher, signature, and source-tree binary scan |
| Integration | Fake child app servers covering initialize, request collisions, notification routing, 2-20 accounts, quota selection, failover, partial failure, pagination, and clean shutdown |
| Real-app E2E | Exact approved Windows/Codex build with two real subscriptions: chat/follow-up, failover preview, history, profiles, plugins, reset preview, Appshots, Computer Use, protocol, Explorer, official Chrome-host invariance, restart, update, and rollback |

### Performance and reliability budgets

- One-account transparent proxy overhead: target p95 below 10 ms for local RPC
  forwarding, excluding child/network latency.
- Control API account snapshot: bounded to 20 seconds overall with parallel child
  reads; one slow account must not serialize the pool.
- New-thread decision: bounded by the existing 30-second request deadline and by
  the 1.5-second reset-credit lookup per candidate; cached/unknown credit data must
  not block indefinitely.
- History aggregation: bounded memory and no more than 500 initial threads per
  account; 20-account synthetic E2E must complete without goroutine, handle, or
  process leaks.
- Shutdown: all router children and helper processes exit within a documented
  bounded interval; escalation targets only verified router PIDs.
- A 24-hour soak with repeated account refresh, SSE reconnect, history refresh,
  and chats shows no monotonically growing handles, processes, subscriptions,
  goroutines, or unbounded state writes.

### Definition of done

The Windows router is **P0 usable** only when all of the following are true:

- The exact currently installed official Codex version is recorded and approved,
  and the official app is proven unchanged.
- The independent router launches without a console, uses a separate profile, and
  can run beside official Codex.
- Primary and at least one secondary real subscription authenticate independently.
- New chats route deterministically, follow-ups are sticky across restarts, both
  preventive and reactive failover work, and all-depleted behavior is bounded.
- Merged history and pooled usage work with partial failures.
- Install, update, failed-update rollback, and uninstall-preserve-state tests pass.
- The P0 security suite passes with no high/critical dependency finding or known
  secret leakage.
- Starting, updating, rolling back, and uninstalling the router do not add,
  overwrite, or delete the official `com.openai.codexextension` host.

It has **full Windows feature parity** only when, in addition:

- Combined/per-account profiles, account-scoped plugins/MCP, reset-credit selection,
  thread attribution, dedicated protocol, and Explorer integration pass real UI
  tests.
- Appshots selects and inserts a real capture on the approved Windows build.
- Computer Use completes the native Calculator test with no fallback mechanism.
- Chrome connector/native messaging uses a router-owned extension ID and host
  name and passes browser/registry/uninstall E2E without touching
  `com.openai.codexextension`; until then it remains an explicit gap.
- A rebuild with the same identity preserves accounts, sticky ownership, router
  integrations, and expected Windows permissions.
- Accessibility is verified for keyboard-only operation, focus, screen-reader
  labels, contrast, reduced motion, and Windows scaling from 100% through 200%.

It is **release-ready** only when:

- CI is green from a clean checkout with locked build dependencies and a generated
  SBOM/provenance record for project-owned artifacts.
- The source-only release check proves no OpenAI binary, patched ASAR, credential,
  personal path, token, screenshot, or runtime state is included.
- A signed Windows E2E report records commit, OS build, architecture, official
  Codex version/package/hash, router publisher/certificate fingerprint, test
  accounts used, deviations, and manual checks.
- Rollback instructions have been executed—not merely reviewed—on the exact release
  candidate.

## Recommended implementation order

1. **Freeze the baseline:** inventory the newly updated Windows app and current
   primary account behavior; approve hashes and anchors without modifying it.
2. **Make the process layer portable:** Windows paths, `.exe` discovery, environment,
   handle inheritance, hidden launch, job-object shutdown, ACLs, and state atomicity.
3. **Prove the transparent proxy:** build a staged copy that behaves exactly like
   one official account before introducing renderer changes.
4. **Enable the account pool:** isolated children, login, quota routing, sticky
   ownership, failover, aggregated usage, history, and the authenticated control API.
5. **Patch the Windows renderer:** account menu, profile, plugins, reset sheet,
   depletion wording, thread attribution, CSP, profile isolation, and updater removal
   against exact reviewed anchors.
6. **Restore desktop integrations:** Appshots and Computer Use first in diagnostic
   staging, then dedicated protocol, notifications, shortcuts, and Explorer verbs.
   Keep inherited Chrome Native Messaging disabled. Qualify the independent
   router-owned extension and host through its dedicated release gate.
7. **Productize lifecycle:** transactional install/update/rollback/uninstall,
   signing, compatibility manifest, transcripts, and last-known-good recovery.
8. **Qualify the release:** clean CI, security negatives, 2-20-account integration,
   real two-account UI E2E, mixed-DPI Appshots, native Computer Use, rebuild, rollback,
   uninstall, soak, and source-only release audit.

No phase may weaken an anchor, disable a caller/peer security check, modify the
official package in place, or add a broad automation fallback merely to make a
test appear green. A capability remains unimplemented until its corresponding
acceptance evidence exists.
