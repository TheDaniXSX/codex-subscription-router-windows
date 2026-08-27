# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/) and
this project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

No changes yet.

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
