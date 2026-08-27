# Releasing on Windows

The canonical policy and full checklist are in
[WINDOWS-RELEASE.md](WINDOWS-RELEASE.md). Releases from this fork are
source-only: never attach a patched application, ASAR, extracted official file,
Microsoft Store package, account data, credential, signing private key, or
private composite build.

Release metadata must agree across `VERSION`, `package.json`,
`package-lock.json`, `CHANGELOG.md`, [COMPATIBILITY.md](COMPATIBILITY.md), and
[E2E-REPORT-WINDOWS.md](E2E-REPORT-WINDOWS.md).

Before proposing a tag:

```powershell
npm ci --ignore-scripts
npm run check:windows
npm run smoke:windows
npm run release:check
```

Then qualify a recorded candidate commit on the locked Windows baseline and
record only redacted evidence in `E2E-REPORT-WINDOWS.md`. Because committing
that report changes Git's commit ID, tag CI separately generates qualification
and provenance for its own `GITHUB_SHA`. The strict metadata command:

```powershell
npm run release:gate
```

must pass before creating `vX.Y.Z`. It validates report structure, locked
baseline metadata, forbidden artifacts, and consistent versions. A tracked
report marked `QUALIFIED` remains historical evidence rather than publication
authority.

The protected tag workflow repeats the cross-platform, Windows, security, and
metadata gates for its exact `GITHUB_SHA`. Only afterwards does it generate
commit-bound `AUTOMATED_GATES_PASSED` evidence, source
SBOM/provenance/checksums, and a draft source release.

Automated evidence does not qualify real accounts, Appshots, Computer Use,
reset consumption, signed MSIX installation, or a clean VM. While
`E2E-REPORT-WINDOWS.md` remains `NOT QUALIFIED`, the GitHub release must be
marked preview/prerelease and its notes must not claim stable Windows support.
A stable release additionally requires the completed manual report and the
signing gates in `WINDOWS-RELEASE.md`. Review every draft manually before
publication. Published tags and assets are immutable; corrections use a new
version rather than replacing existing assets.
