# Compatibility

The Windows patcher is intentionally tied to reviewed Microsoft Store package
structures. It verifies the package metadata, source signatures, hashes,
renderer/main-process anchors, and required native layout, then stops before
publishing a destination if any expectation differs.

## Release 0.2.0

Patch profile: `windows-26.820.9563.0-x64-r1`

| Component | Locked value |
| --- | --- |
| Platform | Windows 10/11 x64 |
| Official package name | `OpenAI.Codex` |
| Official package full name | `OpenAI.Codex_26.820.9563.0_x64__2p2nqsd0c76g0` |
| Official package version | `26.820.9563.0` |
| Architecture | `x64` |
| Internal desktop version | `26.820.71523` |
| Internal desktop build | `7226` |
| Bundled Codex CLI | `0.150.0-alpha.8` |
| Electron | `42.3.0` |
| Original `app.asar` SHA-256 | `e353c580ef4939d36f4ae32a35c896d089205c1d06b9f711cf78ffa4a3578a8a` |
| Original `codex.exe` SHA-256 | `799ff77125c47b0736ceb36e9b33975bb93d4162bca663730f3a4c90faf2add9` |
| Original `ChatGPT.exe` SHA-256 | `4ec11307b67796338d666f40c431b2804e41669576d3bc350dece8703bf4a114` |
| Public artifact model | Source-only |
| Local/manual qualification | Tracked in `E2E-REPORT-WINDOWS.md`; currently `NOT QUALIFIED` |
| Automated tag evidence | `AUTOMATED_GATES_PASSED`, generated outside the source tree for the exact CI commit |
| Stable release eligibility | Blocked until manual Windows E2E is `QUALIFIED` and signing/release gates pass |

This profile has been used for local dry runs and private installed-build
verification. Those results do not qualify an arbitrary future commit. Public
release qualification must be repeated against the exact tagged candidate and
recorded in [the Windows E2E report](E2E-REPORT-WINDOWS.md).

Only the exact values above are accepted for normal installation. A different
official package may require no semantic changes, but it is still unverified
until a separate profile records and reviews its identity, hashes, anchors,
signatures, and qualification results. Diagnostic overrides must not activate
or install their output.

## Release 0.1.0 (upstream history)

Version 0.1.0 was the original upstream macOS release. It is retained in the
fork's changelog for attribution but is not a supported Windows profile.

| Component | Historical value |
| --- | --- |
| Official ChatGPT version | `26.803.61601` |
| Official bundle build | `6396` |
| `app.asar` SHA-256 | `d5a44ed9e2f1db5f81dbbe85408aed256f3203c5b16f00817bb9d7cd941343cf` |
| Architecture | Apple silicon (`arm64`) |
