# Compatibility

The Windows patcher is intentionally tied to reviewed Microsoft Store package
structures. It verifies the package metadata, source signatures, hashes,
renderer/main-process anchors, and required native layout, then stops before
publishing a destination if any expectation differs.

## Release 0.2.0

### Current source checkout (September 9 update)

Additional patch profile: `windows-26.903.8094.0-x64-r1`.

| Component | Reviewed value |
| --- | --- |
| Official package | `OpenAI.Codex_26.903.8094.0_x64__2p2nqsd0c76g0` |
| Internal desktop version/build | `26.903.61454` / `8378` |
| Bundled CLI | `0.153.4` (new executable hash) |
| Original `app.asar` SHA-256 | `3b8e61c9b7afefeda3166f251270724a138af15eb947b7c3907691a695bce66c` |
| Original `codex.exe` SHA-256 | `ccdc9eb9dd71fbcfb03ad42c4eca2b0d6ff6fbd32ebe9416550e6244561e559b` |
| Computer Use Node/package | `24.20.0` / `0.2.4` |

The dedicated `windows_renderer_26903.py` profile adapts the renamed bindings and
restructured task-summary section, preserving the account selector and all prior
router components. The patcher pins every source hash, validates unique anchors,
retains the native-host isolation, and keeps Appshots opt-in. It does not disable
Electron integrity enforcement. Tests on the actual payload covered renderer
syntax, native subagent continuations and two real Astra requests changing the
spending subscription in one task. Visual interaction still requires manual review.

### Previous source checkout (September 8 update)

Additional patch profile: `windows-26.901.6511.0-x64-r1`.

| Component | Reviewed value |
| --- | --- |
| Official package | `OpenAI.Codex_26.901.6511.0_x64__2p2nqsd0c76g0` |
| Internal desktop version/build | `26.901.51231` / `8109` |
| Bundled CLI | `0.153.4` |
| Original `app.asar` SHA-256 | `e75bae2b8a02f174c7ceeed6d631aaff355e44f8af5c798fa3628089f11d659e` |
| Original `codex.exe` SHA-256 | `e5aa76d19c7c94e2e9ef9b707d590206a73ac0e97c8ddc8382181242494bef75` |
| Original `ChatGPT.exe` SHA-256 | `814e9fbd141cfa2aaefa33220bc3a7170824e18089946bf1947617597353851d` |
| Computer Use tree SHA-256 | `c2b1cb4ea9e1394bf5c7ae189d3b82d1732c105a680a3762894dc7a08f531ee7` |
| Computer Use Node/package | `24.19.0` / `0.2.4` |

The production update manifest advertised this package on 2026-09-08.
Its renderer splits the profile menu into `app-primary` while request and
reset-query helpers remain in `app-initial`. The dedicated compatibility
module patches both and preserves account management, profile/plugin/reset
selection, thread attribution, runtime isolation, and the opt-in Appshots gate.

This package also enforces Electron's embedded ASAR integrity check. The local
runtime updates only the existing 64-character header digest in the named PE
resource; integrity enforcement remains enabled. `ChatGPT.original.exe` preserves
the signed official executable byte-for-byte. The modified `ChatGPT.real.exe` is
not Authenticode-valid: verification checks its exact permitted difference from
that original, its recorded hash, and its match to the patched ASAR header.
The official Store installation and bundled CLI remain unchanged.

The original release profile below remains supported. September changes are
available from source; the historical release qualification is not a claim
that all native capabilities have been manually requalified on every machine.

### Original August release profile

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

Only the exact reviewed profiles above are accepted for normal installation. A different
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
