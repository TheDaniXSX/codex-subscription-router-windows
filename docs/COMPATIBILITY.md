# Compatibility

The Windows patcher is intentionally tied to reviewed Microsoft Store package
structures. It verifies the package metadata, source signatures, hashes,
renderer/main-process anchors, and required native layout, then stops before
publishing a destination if any expectation differs.

## Release 0.2.0

### Current source profile (2026-09-30)

Exact patch profile: `windows-26.928.2636.0-x64-r1`.

| Component | Inspected candidate value |
| --- | --- |
| Official package | `OpenAI.Codex`, version `26.928.2636.0`, `x64` |
| Internal desktop version/build | `26.928.21956` / `12404` |
| Bundled Codex CLI | `0.159.2` |
| Original `app.asar` SHA-256 | `fb7b2ee791bcbdb3c4a6375e9fec8fb404f3eaff995f0d293227354b298aff49` |
| OpenAI Authenticode signer thumbprint | `C8FA9121679EB208B46A7171C7973575248DE56E` |
| Computer Use tree | 2,367 files / 251,349,204 bytes; SHA-256 `75281eeee57e4f3f93403c1f06bef908cc2195301f9648b0f1e5ef6f656010e5` |
| Computer Use Node manifest / binary | `24.21.0-cua.1` / `24.21.0` |
| Computer Use runtime | `0.0.27/20260927214556-b77d38801cca` |
| `@oai/cua` | `0.2.5` |

26.928 keeps the 26.924 bundle layout and patch strategy; only minifier
bindings moved, with two structural differences:

- The native Chrome-host registry and state-path code moved from `src-*.js`
  into the bootstrap bundle (`bootstrap-DvSEn4qy.js`). The profile patches it
  there, and the verifier checks the 26.928 isolation markers as a group.
- The static "out of Codex and Work usage" depletion banner messages are gone,
  so no depletion banner is rewritten.

Other bundles: `application-network-startup-D74LEWDz.js`, `main-DkWgQSQe.js`,
`profile-dropdown-items-3bf36c95598d.js`, `app-initial-fd3c4b862660.js`,
`app-shared-a906948d8868.js`, `modal-impl-303e71e5bac4.js`,
`profile-ab5e9e0709fe.js`, `plugins-settings-cbe96e4a6efb.js` and
`local-conversation-thread-3c15e330d968.js`. The Electron integrity rebind is
the same as for 26.924 below.

### Previous source preview (2026-09-28)

Exact patch profile: `windows-26.924.2738.0-x64-r1`.

| Component | Inspected candidate value |
| --- | --- |
| Official package | `OpenAI.Codex`, version `26.924.2738.0`, `x64` |
| Internal desktop version/build | `26.924.22138` / `11645` |
| Bundled Codex CLI | `0.158.0-alpha.2.1` |
| Original `app.asar` SHA-256 | `89fba67324ffb8dd54ccf13b6f097172e697549eeb1f26396f86f972c10c5b0c` |
| OpenAI Authenticode signer thumbprint | `DB9831002B26D78F14E07806F595C6A429E9B6E9` |
| Computer Use tree | 2,366 files / 251,062,420 bytes; SHA-256 `355f5b4661571019ff76e671289320694585e6e3c5fabfddd27f77f6a48f8cb9` |
| Computer Use Node manifest / binary | `24.21.0-cua.1` / `24.21.0` |
| Computer Use runtime | `0.0.24/20260924074400-f52ea85e2a98` |
| `@oai/cua` | `0.2.5` |

The main-process bootstrap is now `bootstrap-D2PJMYEh.js`; its updater and
AppUserModelID anchors moved, and runtime caches now live under
`application-network-startup-CY4ZWOz-.js`. The profile selector now uses
`profile-dropdown-items-474171673a46.js` with its shell assembled in
`app-initial-ff48311587c5.js`; the reset modal is exported from
`modal-impl-629ff3f666ce.js`. Shared account requests, profile refresh, plugin
settings, latest-inference attribution, and native messaging are located in
`app-shared-c568b0b98683.js`, `profile-38e7880be288.js`,
`plugins-settings-b59e65830e0f.js`,
`local-conversation-thread-e96297c6d662.js`, and `src-BSSLXJxP.js` respectively.
The profile adapts those paths while retaining the router CLI override,
isolated `userData`, protocol protection, and opt-in Appshots policy.

The 26.924 integrity workaround is intentionally different from older
byte-identical-wrapper profiles: the verified upstream `ChatGPT.exe` is retained
as `ChatGPT.original.exe`; `ChatGPT.real.exe` is derived by changing the
`INTEGRITY/ELECTRONASAR` expected ASAR digest. That derived executable does not
retain OpenAI's Authenticode signature and must not be represented as an
unchanged or OpenAI-signed binary. The original signature must be verified
before the derivation, and the exact resource-only change plus launch behavior
remain explicit security/runtime gates.

The exact input passed signature/hash inventory. Local tests passed: 129
Windows Python tests, 45 JS tests, 9 release tests, Go tests/vet and offline
Windows smoke. The packed-menu contract covers Rename, Auto/account routing,
Usage, scoped reset query/redemption, profile refresh, plugin scope and lazy
helper readiness. All 13 modified JavaScript bundles parse. Tests use synthetic
accounts and do not redeem real credits. The CUA digest and manifest are static
payload checks, not proof of a live Computer Use session. Build verification,
activation and live acceptance are recorded separately in
[the 26.924 qualification record](UPGRADE-26924-PLAN.md).

### Previous source checkout (September 22 update)

Additional patch profile: `windows-26.917.6896.0-x64-r1`.

| Component | Reviewed value |
| --- | --- |
| Official package | `OpenAI.Codex_26.917.6896.0_x64__2p2nqsd0c76g0` |
| Internal desktop version/build | `26.917.51856` / `10492` |
| Bundled CLI | `0.155.0-alpha.16` |
| Original `app.asar` SHA-256 | `00b7936388d11a3faede5fc736a8c6264eb66e1907bac4ef72c39b7399175d68` |
| Original `codex.exe` SHA-256 | `97d4d67419d0ac2f71342f9a5e850f9468aa622618de8ea823223edb9a91926a` |
| Original `ChatGPT.exe` SHA-256 | `03e193b8beff46f155272af70fedaa6b1404b7eb9c8f8ff13efb8f1b046a0b34` |
| Computer Use tree SHA-256 | `e312411601e65bd24cfb85434ae688eb5fa89470bb42625aa56de9ced6706087` |
| Computer Use Node/package | `24.21.0` / `0.2.5` |
| Computer Use runtime | `0.0.16/20260915001755-492f19756c31` |

This exact profile adapts the renamed native account menu, Usage/reset controls,
profile, plugins and latest-request task panel. It keeps the official signed
executables and Computer Use payload byte-identical and checks Appshots remains
opt-in. Automated tests use synthetic accounts and credits; they do not redeem
real credits or establish a live voice/Computer Use session.
Qualification on the reviewed local package: 118 Windows tests, 45 UI tests,
9 release tests, Go tests/vet, 57 full-build checks, parse checks for all 9
changed JavaScript bundles, and isolated auxiliary app-server initialization
without inference. Activation still requires the running router to exit.

### Previous source checkout (September 21 update)

Additional patch profile: `windows-26.915.4065.0-x64-r1`.

| Component | Reviewed value |
| --- | --- |
| Official package | `OpenAI.Codex_26.915.4065.0_x64__2p2nqsd0c76g0` |
| Internal desktop version/build | `26.915.31945` / `9922` |
| Bundled CLI | `0.155.0-alpha.9.2` |
| Original `app.asar` SHA-256 | `b8aeb817cd1ee6ef50efe8a97985d3be41de89688a5addfe0a444e1e52348096` |
| Original `codex.exe` SHA-256 | `bc45017e8239dc150258f69309ced9df6bbcdf5b8e4f346decf780ac0999e226` |
| Original `ChatGPT.exe` SHA-256 | `0d27aef4010466bd8d2a95f6483938cfdb8926f6668ecc1182facec9b85b75d2` |
| Computer Use tree SHA-256 | `4acc27c77290487692fac3887db988ee853ead31d6c12f2b38cc25653664b79d` |
| Computer Use Node/package | `24.21.0` / `0.2.5` |
| Computer Use runtime | `0.0.16/20260915001755-492f19756c31` |

The account menu and usage sheet moved into `app-initial`; this build uses a
dedicated exact renderer profile. Rename, Usage, scoped reset reads/redemption,
Auto/subscription choices, profile/plugin selectors and last-request attribution
are exercised against the packed native bindings with synthetic HTTP responses.
The redesigned native profile/editing UI is preserved alongside the account
selector; the router scopes the legacy profile-data query, not public-profile
editing. Protocol registration and logs now live in bootstrap. The copied updater
is disabled at policy, startup and failure recovery; official Store updates remain
untouched. The launcher retains its explicit `CODEX_CLI_PATH` mux override, which
excludes the new app-contained-core selection path.

Qualification: 114 Windows tests, 45 UI tests, 9 release tests, Go tests/vet,
57 full build checks, syntax of all 9 changed bundles and isolated auxiliary
app-server initialization without inference. Signed upstream executables and
`chrome.dll` remain byte-identical, with no fuse changes. Real microphone/WebRTC,
live Computer Use UI interaction and real reset redemption are not covered by
these automated checks. Activation requires the existing router to exit first.

### Previous source checkout (September 17 update)

Additional patch profile: `windows-26.911.7940.0-x64-r1`.

| Component | Reviewed value |
| --- | --- |
| Official package | `OpenAI.Codex_26.911.7940.0_x64__2p2nqsd0c76g0` |
| Internal desktop version/build | `26.911.61220` / `9647` |
| Bundled CLI | `0.155.0-alpha.2.6` |
| Original `app.asar` SHA-256 | `74e7aaf2c112f84ef68a7846d10d1411403e72763f7e93fe046df2f264adf6e0` |
| Original `codex.exe` SHA-256 | `be793ab45adbcbd9fa716df04cb6bc68eb9e353c6e6af20886af45c11abc2413` |
| Original `ChatGPT.exe` SHA-256 | `f28e7d55de4ee465747e252741218bf5a58c33620d014598e01bddb9189abaca` |
| Computer Use tree SHA-256 | `336fd5a7af792ccaa406f8c307057dc64b90198fe75208be888056ffe8d98959` |
| Computer Use Node/package | `24.20.0` / `0.2.4` |

The renderer profile follows the new native menu/modal exports and lazy
initializers, reset-sheet header, profile query and task-summary layout.
Packed-ASAR tests exercise Rename, Usage, account-scoped reset requests,
expanded routing choices, profile/plugins and the latest-request display.
Reset tests use synthetic HTTP responses; no real credits are redeemed.

The main-process profile adapts runtime-cache paths and the two isolated native
host state paths, with native-host registration/deletion disabled. Appshots stays
opt-in and still requires upstream eligibility and the native bridge. Signed
desktop/CLI/native payloads and `chrome.dll` are preserved byte-for-byte; no
Electron fuse or integrity policy is changed. Automated qualification includes
108 Windows tests, 45 UI tests, 9 release tests, Go tests/vet and an isolated
auxiliary app-server initialization without inference. These checks do not claim
a live microphone/WebRTC call or real reset redemption on this build.

### Previous source checkout (September 9 update)

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
Electron integrity enforcement. Unlike 26.901, the original 26.903 executable
ships without `INTEGRITY/ELECTRONASAR` and its original fuse wire is `101100011`.
This profile therefore preserves both signed `ChatGPT.real.exe` and `chrome.dll`
byte-for-byte instead of rebinding a nonexistent resource. The runtime DLL is
checked against the reviewed SHA-256; no fuse is changed. Tests on the actual payload covered renderer
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
