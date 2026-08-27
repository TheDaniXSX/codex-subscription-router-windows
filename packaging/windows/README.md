# Windows packaging

The Windows port uses an **unpackaged, per-user build first**. MSIX is an
optional second stage. Neither path modifies the installed `OpenAI.Codex`
package or writes into `C:\Program Files\WindowsApps`.

These scripts package an already-patched app; they do not patch `app.asar`,
replace executables, isolate the Chromium profile, or authenticate accounts.
Run the Windows patch/launcher pipeline before packaging. In particular, the
payload must contain `ChatGPT.real.exe`, `resources\codex.real.exe`, and the
router replacements at their original executable names.

## Unpackaged baseline

`Build-Unpackaged.ps1` accepts either:

- a package root containing `app\ChatGPT.exe`; or
- the patcher's app root containing `ChatGPT.exe`.

It rejects reparse points in the source, assets, output, and their existing
ancestors. Each tree is hashed before copying, copied into private staging,
hashed again at the source and destination, and committed only when all three
inventories agree. It never edits the source and normalizes output to this
layout:

```text
CodexSubscriptionRouter/
├── app/
│   ├── ChatGPT.exe
│   ├── ChatGPT.real.exe
│   └── resources/
│       ├── app.asar
│       ├── codex.exe
│       └── codex.real.exe
├── assets/                    # optional for unpackaged execution
├── router-package.json        # provenance, launch target, component hashes
└── router-package.files.json  # SHA-256 and length of every other packaged file
```

Example:

```powershell
pwsh -NoProfile -File .\packaging\windows\Build-Unpackaged.ps1 `
  -SourceRoot .\artifacts\patched-app `
  -OutputPath .\dist\windows\unpackaged\CodexSubscriptionRouter

.\dist\windows\unpackaged\CodexSubscriptionRouter\app\ChatGPT.exe
```

Existing output is refused by default. `-Overwrite` retains the previous tree
as a timestamped sibling backup instead of deleting it. Install a tested build
per-user under `%LOCALAPPDATA%\Programs\Codex Subscription Router`; keep runtime
state under `%LOCALAPPDATA%\Programs\Codex Subscription Router Data`. The launcher and patched
`owl-app.ini` are responsible for ensuring the router does not reuse the
official app's `Codex` user-data directory.

`router-package.files.json` is authoritative for the complete unpackaged tree.
It intentionally excludes only itself, because a file cannot contain its own
final hash. Verification rejects missing, additional, renamed, resized, or
modified files. Rebuild from reviewed inputs instead of regenerating a manifest
to bless an unexplained change.
When `-Version` is omitted, both packaging stages derive it from the repository
`VERSION` file; MSIX maps SemVer `major.minor.patch` to
`major.minor.patch.0`. Release mode rejects a mismatched explicit MSIX version.

## Optional MSIX

MSIX is deliberately separate and experimental:

```powershell
pwsh -NoProfile -File .\packaging\windows\Build-Msix.ps1 `
  -PayloadRoot .\dist\windows\unpackaged\CodexSubscriptionRouter `
  -AssetsPath C:\path\to\independent-router-assets `
  -OutputPath .\dist\windows\msix\CodexSubscriptionRouter.msix `
  -Publisher 'CN=CodexSubscriptionRouter.Local' `
  -CertificateThumbprint 'CURRENT_USER_MY_SHA1_CERT_THUMBPRINT'
```

The asset directory is explicit so a distributable package does not
accidentally reuse OpenAI branding. It must contain `icon.png`,
`Square44x44Logo.png`, and `Square150x150Logo.png`. The certificate must be in
`Cert:\CurrentUser\My`, and its subject must exactly equal `-Publisher`. The
scripts never create, trust, export, or commit a signing key. An unsigned MSIX
can be produced for local inspection only by omitting the thumbprint and adding
the explicit `-AllowUnsigned` switch. There is no implicit unsigned fallback,
and Windows will not install it until it is signed by a trusted certificate.

For a public artifact, use `-Release` with explicit, non-placeholder identity
and publisher values:

```powershell
pwsh -NoProfile -File .\packaging\windows\Build-Msix.ps1 `
  -PayloadRoot .\dist\windows\unpackaged\CodexSubscriptionRouter `
  -AssetsPath C:\secure-build-inputs\router-assets `
  -OutputPath .\dist\windows\msix\CodexSubscriptionRouter.msix `
  -IdentityName 'YourPublisher.CodexSubscriptionRouter' `
  -Publisher 'CN=Your exact certificate subject' `
  -PublisherDisplayName 'Your publisher display name' `
  -CertificateThumbprint 'CURRENT_USER_MY_SHA1_CERT_THUMBPRINT' `
  -TimestampUrl 'https://your-rfc3161-service.example' `
  -Release
```

Release mode rejects the `.Local` identity and publisher defaults, missing
certificate/private key, expired or not-yet-valid certificates, certificates
without the Code Signing EKU, and missing RFC 3161 timestamp configuration.
After signing, `signtool verify /pa /all /v` must succeed. The final signed bytes
are rehashed after commit and accompanied by
`CodexSubscriptionRouter.msix.sha256`.

As with the unpackaged build, existing MSIX output is refused unless
`-Overwrite` is explicit. The package is built and signed at a temporary sibling
path, then moved into place; an overwritten package remains as a timestamped
backup together with its checksum. `router-msix.files.json` inside the package
enumerates the complete pre-pack payload, including the generated manifest and
independent assets, but excluding itself. `router-package.json` records the
source unpackaged metadata/manifest hashes without embedding the official
application manifest.

The template intentionally uses:

- package identity `CodexSubscriptionRouter.Local`;
- application id `Router`;
- protocol `codex-router://`;
- a non-OpenAI publisher; and
- only the `runFullTrust` capability.

It intentionally omits the official `OpenAI.Codex` identity and publisher,
`codex://`, licensing capability, Office/skill associations, firewall rules,
COM server/CLSID, Explorer context menu, and automatic updates. A future modern
Explorer menu needs an independently implemented `IExplorerCommand` DLL and a
unique CLSID; do not copy the official registration.

The unpackaged build now provides optional classic HKCU integration through
`scripts/windows/Manage-ShellIntegration.ps1`. It uses the independent
`codex-router` protocol and `OpenProjectInCodexRouter` verb, never writes
`HKCU\Software\Classes\codex` or the official `OpenProjectInCodex` verb, and
removes a selected tree only after an exact compare-and-delete check. This is
separate from the MSIX manifest and remains disabled until the user explicitly
registers it. See `docs/WINDOWS-SHELL-INTEGRATION.md`.

## Validation

Run the hermetic contract tests on any Windows CI runner with PowerShell 7.2+:

```powershell
pwsh -NoProfile -File .\packaging\windows\Test-Packaging.ps1
```

The test uses tiny synthetic files, requires no OpenAI binaries, and does not
need the Windows SDK. It exercises complete-manifest verification and tamper
failure, source and output junction rejection, identity/publisher/unsigned
release gates, and a synthetic create/update/backup/remove lifecycle entirely
under `%TEMP%`. It does not register or remove a real Appx package. A real MSIX
build additionally needs `makeappx.exe`; a signed build needs `signtool.exe`
from the Windows SDK.

Release engineering can additionally exercise the real `makeappx.exe` schema
validation with a directory of valid, independently owned PNG assets:

```powershell
pwsh -NoProfile -File .\packaging\windows\Test-Packaging.ps1 `
  -SdkValidationAssetsPath C:\path\to\independent-router-assets
```

Official binaries are licensed build inputs for local use, not repository or
release artifacts. Never commit or redistribute the copied application,
credentials, certificates, `app.asar`, or generated packages. The hermetic test
also fails if executable/Appx/MSIX/ASAR/certificate payloads appear anywhere
under `packaging/windows`; CI must continue to build only synthetic fixtures.
