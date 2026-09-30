# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/) and
this project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Codex 26.928 source profile

- Add an exact compatibility profile for Windows Codex `26.928.2636.0`
  (internal desktop `26.928.21956`, build `12404`; bundled CLI `0.159.2`;
  original ASAR SHA-256
  `fb7b2ee791bcbdb3c4a6375e9fec8fb404f3eaff995f0d293227354b298aff49`). It keeps
  the 26.924 layout and patch strategy with new minifier bindings for the
  updater, AppUserModelID, runtime-cache and log roots, Appshots gate, profile
  dropdown, reset query/mutation, reset modal, profile refresh, plugin scope and
  latest-request thread section.
- The native Chrome-host registry code moved from `src-*.js` into the
  bootstrap bundle; the profile patches and verifies it there.
- 26.928 no longer ships the static "out of Codex and Work usage" banner
  messages, so this profile does not rewrite a depletion banner.
- Inventory the 26.928 Computer Use tree as 2,367 files / 251,349,204 bytes
  (tree SHA-256 `75281eeee57e4f3f93403c1f06bef908cc2195301f9648b0f1e5ef6f656010e5`),
  runtime `0.0.27/20260927214556-b77d38801cca`, Node `24.21.0-cua.1` /
  `24.21.0`, `@oai/cua` `0.2.5`. Static payload evidence only.
- The Electron integrity rebind is unchanged from 26.924: the derived
  `ChatGPT.real.exe` does not retain OpenAI's Authenticode signature.

### Codex 26.924 source preview

- Add an exact compatibility profile for Windows Codex `26.924.2738.0`
  (internal desktop `26.924.22138`, build `11645`; bundled CLI
  `0.158.0-alpha.2.1`; original ASAR SHA-256
  `89fba67324ffb8dd54ccf13b6f097172e697549eeb1f26396f86f972c10c5b0c`). The
  new bootstrap/updater and AppUserModelID bindings, runtime-cache location,
  native-messaging bundle, profile dropdown, reset modal, profile/plugin views,
  and latest-inference subscription panel have new bundle locations and anchors.
  Preserve updater policy initialization while disabling copied-app updates.
  Load shared account controls before direct profile/plugin navigation and
  defer thread event subscriptions until their helper is ready. Fix profile
  refresh and preserve React component wrappers. See the qualification record
  in `docs/UPGRADE-26924-PLAN.md` for tests and runtime limitations.
- Inventory the 26.924 Computer Use tree as 2,366 files / 251,062,420 bytes
  (tree SHA-256 `355f5b4661571019ff76e671289320694585e6e3c5fabfddd27f77f6a48f8cb9`),
  with manifest Node `24.21.0-cua.1`, Node binary `24.21.0`, runtime
  `0.0.24/20260924074400-f52ea85e2a98`, and `@oai/cua` `0.2.5`. This is static
  payload evidence, not a live Computer Use qualification.
- The 26.924 Electron integrity rebind preserves the verified upstream wrapper
  as `ChatGPT.original.exe` and derives `ChatGPT.real.exe` with the expected
  patched-ASAR digest. The derived runtime executable does not retain OpenAI's
  Authenticode signature; only local runtime validation can establish whether
  this packaging change works safely. Do not describe it as an unchanged or
  OpenAI-signed executable.
- Require PowerShell 7 for install/update/rollback/uninstall and preflight the
  payload paths projected into destination and staging against the classic
  `MAX_PATH` limit before copying. Repository-only verification retains its
  separate PowerShell 5.1-compatible path.
- Parse every modified JavaScript bundle before publishing a patched app and
  provide an opt-in, unauthenticated real app-server smoke using temporary homes.

### Fixed

- Support Windows Codex `26.917.6896.0` (inner `26.917.51856`, build `10492`,
  CLI `0.155.0-alpha.16`).
  Adapt the renamed native account menu, Usage/reset controls, profile and
  latest-request panel while retaining explicit mux routing, isolated state,
  signed upstream binaries, Computer Use payload and opt-in Appshots behavior.
  Test account actions using synthetic identities and credits only.
- Allow image-heavy inference and compaction histories up to 128 MiB at the
  local spending gateway instead of rejecting them at 32 MiB. Preserve request
  bytes and selected identity; reject oversized bodies before spending and
  distinguish interrupted uploads from size-limit errors.
- Support Windows Codex `26.915.4065.0` (inner `26.915.31945`, build `9922`,
  CLI `0.155.0-alpha.9.2`). Adapt the relocated initial-renderer account menu,
  native reset modal and redesigned profile without removing native editing.
  Update bootstrap/log isolation and disable the copied app's updater, including
  its failure-recovery path. Preserve the explicit router CLI override, original
  signed payloads, Computer Use `0.2.5`/Node `24.21.0`, and all routing semantics.
  Qualify the packed native handlers using synthetic accounts and reset credits.
- Support Windows Codex `26.911.7940.0` (inner `26.911.61220`, build `9647`,
  CLI `0.155.0-alpha.2.6`) with exact source identity checks. Adapt the native
  menu, usage/reset sheet, profile refresh, last-request panel, runtime cache
  and Chrome native-host isolation to the new bundle. Preserve signed upstream
  executables, routing semantics, auxiliary CUA and the opt-in Appshots gate.
  Validate real packed native handlers with synthetic account/reset requests.
- Shorten the patcher staging directory prefix so copying the 26.908 payload
  no longer exceeds `MAX_PATH` under the default destination when Windows long
  paths are disabled. Record open launcher-bypass and long-path issues in
  `docs/FIELD-REPORT-LAUNCHER-BYPASS.md`.
- Replace unsupported Electron `window.prompt` renaming with an inline editor
  supporting Save, Cancel, validation, errors and duplicate-submit protection.
- Resolve the usage/reset modal opener from the native profile button instead
  of the stale 26.908 analytics alias. Initialize its native module and refresh
  subscription reset counts after redemption. Test actual packed click handlers
  and native modal state, with account-scoped synthetic credits only.
- Show the task's latest accepted inference subscription independently of its
  history owner, including live Auto updates and private restart persistence.

- Support Windows Codex `26.908.4834.0` (inner `26.908.40834`, build `8881`,
  bundled CLI `0.154.0-alpha.6.2`). Review exact source hashes and adapt runtime
  isolation, native messaging and renderer bindings. Retain signed upstream
  binaries, per-request spending, voice setup translation and auxiliary CUA.
- Adapt the new asset-based profile menu while preserving its legacy icon API;
  explicitly initialize the router's usage icon. Preserve the profile query
  object for account refresh and adapt the redesigned reset sheet and thread
  summary. Test native component exports and the packed expanded selector.
- Keep Appshots explicitly opt-in on 26.908 without overriding the upstream
  feature-availability decision or enabling an absent native bridge.

- Preserve native `OpenAI-Alpha: quicksilver=v2` and voice session headers.
  Real backend A/B reproduced the previous 400 and verified 201 through the
  corrected gateway with a generated SDP offer, without microphone/audio.

- Qualify Windows Codex 26.903.9818.0 (inner 26.903.71938, build 8576):
  updated renderer bindings, profile refresh, thread subscription panel and
  native profile-menu render contract. Preserve signed Electron binaries and
  retain all capacity, voice setup and auxiliary app-server fixes.

- Add authenticated native voice call setup translation from API multipart to
  the ChatGPT subscription backend, retaining selected-account spending and
  native direct WebRTC sideband. No automatic call replay after dispatch.
  Record stream EOF separately from network/scanner errors.

- Isolate strict spending capacity reads to the selected subscription. Auto
  rechecks responsive accounts after a peer timeout without rejuvenating stale
  quota. Capacity lock waits honor cancellation; cache entries are mode-specific.
  Optional reset scoring uses cached metadata on the inference path. Retry a
  capacity timeout once before dispatch and record sanitized pre-dispatch failures.

- Timestamp capacity snapshots after refresh completion: slow account/reset
  metadata reads no longer expire a fresh snapshot before its first reservation.
  Increase inference response-header wait from 60s to 5 minutes and expose
  sanitized timeout/cancellation/transport diagnostics without replaying uncertain
  requests or silently changing a strict subscription selection.

- Computer Use auxiliary app-server startup: invocations without router control
  context pass through to the original CLI, instead of failing on a missing
  control port. Partial router configurations still fail closed.
  The bundled Windows CUA helper's bare app-server call is also recognized by
  its exact parent executable path, since it inherits the desktop's router
  context. This avoids the duplicate-instance lock without bypassing desktop
  conversations or native subagent inference.

- Added a backed-up, idempotent repair for opening router-provider histories
  in the official app, without persisting gateway addresses or tokens. See
  [provider compatibility](docs/PROVIDER-COMPATIBILITY.md).

- Fixed the 26.903 profile-menu crash (React error 130): resolve the menu item
  from native usage instead of confusing it with a minified keyboard-map alias.
  Validate the packed menu and expanded routing selector before installation.

### Added

- Reviewed Windows package `26.903.8094.0` (desktop `26.903.61454`, build
  `8378`, CLI `0.153.4`): dedicated renderer aliases, native-host isolation,
  runtime-cache and Appshots gates, exact payload hashes and regression tests.

- Windows per-inference spending gateway: strict selected-account billing,
  expiry/load-aware Auto, existing histories and native subagent continuations,
  bounded sanitized decision history and authenticated status endpoint.
- Override the previous new-chat preference/fallback semantics in the updated
  Windows launcher; retain legacy behavior for non-opted-in CLI integrations.

- Added a persistent Auto/preferred-subscription selector to the profile menu,
  with automatic fallback, existing-chat affinity, keyboard navigation, and
  authenticated local configuration synchronized across windows.
- Added rollback-compatible private routing preferences without changing
  subscription enablement or the existing account-state format.
- Added the reviewed Windows package `26.901.6511.0` (desktop `26.901.51231`,
  build `8109`, CLI `0.153.4`), adapting the split renderer bundles while
  retaining the August package profile and router isolation.
- Rebound the September runtime's embedded ASAR header digest to the patched
  archive while keeping integrity enforcement enabled and preserving the signed
  original executable separately; verification rejects changes outside that digest.
- Embedded the attributed Codex color icon in the x64 Windows launcher and
  added a Windows-native build check for the icon resource and ICO size table.
- Kept the visible Electron child windows synchronized with the launcher icon
  and explicit AppUserModelID, and assigned the matching identity to newly
  installed Start Menu shortcuts. The pinned launcher and running window can
  now share one taskbar group without modifying the preserved official binary.

## [0.2.0] - 2026-08-27

This is a source-only Windows preview. Automated gates qualify the repository
artifacts, not real-account, Appshots, Computer Use, or clean-VM behavior. A
stable support claim remains blocked until the manual Windows E2E report is
completed.

### Added

- Windows 10/11 x64 port built as an independent, per-user application from a
  locally installed official Codex package.
- Windows launcher with isolated Electron profile, runtime caches, logs,
  account homes, and a strict persisted state-root sidecar.
- Windows process supervision for per-account App Server children and a
  loopback-only, header-authenticated control service.
- Fail-closed patch profile for official package `26.820.9563.0`, internal
  desktop `26.820.71523` build `7226`, and bundled Codex CLI
  `0.150.0-alpha.8`.
- Transactional PowerShell installer, source inventory, installed-build
  verifier, recoverable backups, offline smoke tests, and Windows packaging
  tooling.
- Product commands for rollback, state-preserving uninstall, backup cleanup,
  read-only diagnostics, and bounded resource-soak measurement.
- Account lifecycle UI for a single pending login, retry/cancel, rename,
  enable/disable, logout, recovery, and safe secondary-account removal.
- Opt-in `codex-router://` protocol and Explorer commands with a router-owned
  registration manager and compare-and-delete uninstall behavior.
- Windows CI with synthetic fixtures; no official or composite OpenAI
  application files enter GitHub Actions or public release artifacts.
- Reproducible source archive, CycloneDX/SPDX SBOMs, checksums, provenance,
  commit-bound automated-gate evidence, secret scans, and draft prerelease
  automation.
- Reset-aware routing that prioritizes weekly quota at risk of expiring and
  gives a bounded boost to subscriptions with banked usage resets.

### Changed

- The public release model is source-only. Users build the independent local
  copy from their own signed Microsoft Store installation.
- Updated the pinned build-only `@electron/asar` dependency to `4.3.0`.
- Chrome Native Messaging registration for the official Codex extension is
  left untouched. A separately named router extension and native host are
  available as opt-in source but remain unsupported until publication, signing,
  and clean-VM E2E.
- Appshots is default-off until live multi-monitor/DPI qualification. Computer
  Use is preserved behind static contract checks but remains pending live
  process-isolation qualification.

### Security

- Official package inputs are read-only and checked by exact version, build,
  hashes, renderer anchors, and Windows signatures before patching.
- Account state and the control token use a protected per-user Windows DACL;
  control credentials are accepted only through a request header.
- Release checks reject credentials, private keys, official binaries, patched
  ASAR files, composite packages, and other local build artifacts.
- Inventory and verification cover the complete preserved payload, sensitive
  native-tree digests, independent shell registrations, and schema-2 random
  high-port agreement.

## [0.1.0] - 2026-08-15

This was the original macOS release from the upstream project and is retained
here for attribution and history. It is not a Windows release.

### Added

- Multi-subscription routing with quota-aware balancing and sticky threads.
- Account isolation, device-code sign-in, pooled usage, and quota failover.
- Native account menu, masked emails, plan labels, and profile photos.
- Combined Profile statistics with per-account selection.
- Account-scoped Apps and MCP connection state in Settings → Plugins.
- Per-account rate-limit reset selection and pooled depletion handling.
- Independently signed Appshots and Computer Use support.
- Fail-closed upstream compatibility checks and deepest-first nested helper signing.
- Loopback-only, token-authenticated diagnostic UI states.
- Source-only CI, draft release automation, security documentation, and smoke tests.

[Unreleased]: https://github.com/TheDaniXSX/codex-subscription-router-windows/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/TheDaniXSX/codex-subscription-router-windows/releases/tag/v0.2.0
[0.1.0]: https://github.com/b-nnett/codex-subscription-router/releases/tag/v0.1.0
