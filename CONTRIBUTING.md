# Contributing

## Development setup

The Windows port targets Windows 10/11 x64. Install Go 1.26 or newer,
Node.js 22.12 or newer, npm, Python 3.10 or newer, and PowerShell 5.1 or
PowerShell 7. An official Microsoft Store installation is needed only for a
local compatibility dry run or private composite build; source-only CI and
unit tests use synthetic fixtures.

```powershell
npm ci --ignore-scripts
npm run check:windows
npm run smoke:windows
npm run release:check
```

Run `npm run release:gate` before proposing a release tag. It validates the
versioned report and release metadata but does not turn the tracked report into
authorization for a stable release. Tag CI reruns all automated jobs for its
own commit and emits `AUTOMATED_GATES_PASSED` evidence only after they pass. A
normal pull request is expected to pass `release:check` while the report is
still a `NOT QUALIFIED` template; such a tag may only be a preview/prerelease.

Do not commit an official or patched application, extracted ASAR, executable,
DLL, MSIX/APPX, credentials, control tokens, account state, signing
certificates, private keys, or captures containing unmasked identities or
device codes.

## Patch-profile changes

Renderer and main-process patches depend on exact upstream anchors. A change
must:

1. Keep the official Microsoft Store package immutable.
2. Fail closed when a version, hash, signature, anchor, or binary constant is
   unexpected.
3. Preserve account isolation, sticky thread ownership, and quota failover.
4. Keep control services on loopback with header-only token authentication.
5. Preserve the official `OpenAI.Codex` package identity, `codex://` protocol,
   and Chrome Native Messaging registration unchanged.
6. Add focused synthetic tests and, for user-visible behavior, update the
   Windows qualification checklist when appropriate.

Test against the profile recorded in [the compatibility matrix](docs/COMPATIBILITY.md).
Supporting a new official build requires a separate reviewed profile with its
own exact metadata and hashes. Never weaken an anchor-count or signature check
merely to accept an update.

## Pull requests

Keep changes focused and explain security-sensitive behavior explicitly. Pull
requests must pass the Windows CI gates, including Go formatting/tests/vetting,
JavaScript tests, Python and PowerShell validation, packaging checks, offline
smoke tests, forbidden-artifact scanning, and release metadata consistency.

Changes to release metadata must keep `VERSION`, `package.json`,
`package-lock.json`, `CHANGELOG.md`, `docs/COMPATIBILITY.md`, and the candidate
E2E report aligned. Public releases are source-only and follow
[the Windows release policy](docs/WINDOWS-RELEASE.md).

## Local qualification

Tests involving real accounts, Computer Use, Appshots, installation upgrades,
or rollback run on a maintainer-controlled Windows machine, never in public
CI. Use a recorded reviewed commit and the exact baseline, redact the resulting
report, and verify the official installation before and after. Tag CI must
independently generate evidence for its own `GITHUB_SHA`. Do not consume a usage
reset or perform another account-changing action unless that specific action
was deliberately authorized.
