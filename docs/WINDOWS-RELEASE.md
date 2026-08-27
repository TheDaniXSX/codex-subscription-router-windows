# Windows release, update, and rollback

This document defines the release contract for the Windows port. It applies to
automation, maintainers, and local builds made from an installed official Codex
application.

## Non-negotiable boundaries

1. Public releases contain only this project's source, documentation, SBOMs,
   checksums, and (if the project later chooses to publish them) binaries built
   entirely from this repository.
2. A patched Codex application, `app.asar`, OpenAI executable, DLL, package,
   or file extracted from the official installation is never committed,
   uploaded to CI, attached to a release, cached remotely, or redistributed.
   The independently sourced MIT-licensed launcher icon under `assets/` is a
   repository build input, not an extracted OpenAI application file; its
   attribution and trademark notice must remain intact.
3. The installed official application is a read-only build input. The patcher
   must never change files in `C:\Program Files\WindowsApps`, the official
   package registration, or the official application's data directory.
4. A local patched application has an independent name, install path, package
   identity, publisher, data directory, and update channel. It must not claim or
   retain OpenAI's signature after any byte has changed.
5. Compatibility checks fail closed. An unknown upstream version, architecture,
   hash, renderer anchor, or binary layout stops before the destination changes.
6. Installation and update never run `taskkill`, restart Windows, or terminate
   Codex automatically. In particular, they must not interrupt the Codex task
   that is building or testing the router.

The default public release model is **source-only**. A locally built composite
application is a private installation artifact and is deliberately outside the
public release model.

The reproducible archive, SBOM, checksum, provenance, automated-source-gate
evidence, and verification commands are documented in
[`SOURCE-RELEASE-ARTIFACTS.md`](SOURCE-RELEASE-ARTIFACTS.md).

## Artifact classes

| Class | Examples | May enter Git/CI/release? |
| --- | --- | --- |
| Project source | Go, JavaScript, Python, PowerShell, manifests, tests | Yes |
| Project metadata | SBOM, SHA-256 manifest, provenance, release notes | Yes |
| Router-only output | `codex-mux.exe`, launcher or installer built solely from this repository | Only when the release policy explicitly enables it and it is signed and hashed |
| Official input | Official `app.asar`, `codex.exe`, DLLs, MSIX/APPX contents | No |
| Composite output | Patched/repacked Codex application or package | Local machine only; never publish |
| Local secrets/state | Auth tokens, account homes, control token, state, certificate private keys | No |
| Diagnostics | Dumps, transcripts, screenshots containing account information | No, unless manually redacted and explicitly curated |

The local build log may record the official package identity, version,
architecture, publisher, file paths relative to the package root, file sizes,
and SHA-256 hashes. It must not embed official file contents or authentication
material.

## Version model

Four values identify a build. Do not collapse them into one version number.

- **Router version**: SemVer from `VERSION`, mirrored in project manifests and
  the changelog. Tags use `vMAJOR.MINOR.PATCH`.
- **Official input version**: the full installed package version and
  architecture reported by Windows, for example `x64`. This is not the router
  version.
- **Patch profile**: a stable identifier for the reviewed set of hashes,
  renderer anchors, binary expectations, and transformations for one upstream
  build. Changing any expectation creates a new profile.
- **State schema version**: an integer stored with router state. It changes only
  when persisted data changes shape.

SemVer interpretation for the router:

- `PATCH`: compatible bug fix, test, or additional verified upstream profile;
  no breaking state or command-line changes.
- `MINOR`: backward-compatible feature or a forward state migration that the
  immediately previous release can still read.
- `MAJOR`: incompatible CLI, control protocol, package identity, or state
  change. A documented export/restore path is required.

Every local installation should expose a build record with at least:

```json
{
  "routerVersion": "0.2.0",
  "sourceCommit": "<40-character commit>",
  "officialPackage": "OpenAI.Codex",
  "officialVersion": "<full version>",
  "architecture": "x64",
  "patchProfile": "windows-<official-version>-x64-r1",
  "stateSchema": 1,
  "builtAtUtc": "<RFC3339 timestamp>",
  "builder": "<tool version>"
}
```

`builtAtUtc` is provenance, not an input to compiled binaries. Builds intended
to be reproducible use `SOURCE_DATE_EPOCH` derived from the commit timestamp and
must not place volatile timestamps into the binary.

## Compatibility record

Each accepted official build needs a reviewed compatibility entry containing:

- package name, full version, architecture, and publisher;
- SHA-256 of every official file read or copied by the patcher;
- SHA-256 of the original ASAR and any native executable whose layout matters;
- exact patch anchors and the expected match count for each anchor;
- required embedded Codex/App Server protocol capabilities;
- Windows version and WebView/Electron runtime used for validation;
- test date, test commit, and smoke-test result;
- known limitations and rollback target.

Compatibility entries are source data and may be public because they contain
hashes and metadata, not proprietary bytes. A wildcard version, a hash learned
and immediately trusted during installation, or a `--force` option that skips
validation is not an acceptable release mechanism. A diagnostic override may
collect evidence, but it must not activate or install the result.

## Repository and branch policy

- `main` remains releasable and protected.
- Work is merged through review with required CI checks and no force-push.
- A release tag is annotated and, where available, signed.
- `VERSION`, package manifests, lockfiles, changelog, and compatibility data are
  changed in the same pull request.
- Generated local composite artifacts stay in ignored staging/output
  directories and are deleted only after a verified install or retained in the
  local rollback area.
- GitHub Actions and other reusable CI components are pinned to immutable commit
  SHAs. Dependency updates are reviewed separately from patch-profile changes
  where practical.

## Reproducible project build

The router-only build should be reproducible from a clean tag without access to
the official application:

1. Check out the exact tag with submodules disabled unless one is explicitly
   documented.
2. Verify the tag, clean worktree, `VERSION`, lockfile, and toolchain versions.
3. Install Node dependencies with the lockfile and lifecycle scripts disabled.
4. Download Go modules using `go.mod`/`go.sum`; verify them with
   `go mod verify`.
5. Run format, static analysis, unit, integration, patch-fixture, and negative
   compatibility tests.
6. Build Go executables with `-trimpath`; omit VCS-dirty state and embed the
   router version plus exact commit through reviewed linker flags.
7. Build into a new empty output directory. Never build over the active
   installation.
8. Produce SBOMs, hashes, signatures, provenance, and the test report from the
   final bytes.

The build must not depend on a developer's home directory, account state,
certificate store enumeration order, or installed official application. Those
inputs belong only to the local composite-build stage.

## SBOM and dependency evidence

Generate a CycloneDX JSON SBOM for every public source release. If router-only
binaries are published later, also generate an SPDX JSON SBOM from the final
output directory. Use a pinned SBOM generator and record its name and version.

At minimum, the SBOM/provenance set includes:

- Go modules with resolved versions and checksums;
- npm build dependencies resolved by the lockfile;
- Python and PowerShell runtime requirements used by build scripts;
- compiler/interpreter versions;
- source commit and repository URI;
- artifact name, version, SHA-256, and target architecture;
- licenses where the scanner can determine them.

The official Codex application must not be represented as if it were produced
or licensed by this project. In a **local-only** composite SBOM it may be listed
as an external, supplied component with package identity, version, architecture,
publisher, and hashes. That local SBOM must remain beside the private build.

CI should fail on an incomplete SBOM, an unexpected direct dependency, a
critical known vulnerability without an approved time-bounded exception, or a
license incompatible with distribution. Scanner findings are evidence to
review; exceptions name an owner, reason, expiry date, and remediation issue.

## Hash manifests and provenance

Use SHA-256 for compatibility and release manifests. The manifest is generated
after signing because Authenticode changes the file bytes. Entries are sorted
by normalized relative path and include byte length to make review unambiguous:

```text
<lowercase-sha256>  <bytes>  <relative/path>
```

For a local composite build, keep two distinct manifests:

- `official-input.sha256`: hashes of the untouched read-only inputs;
- `installed-output.sha256`: hashes of the final staged and signed output.

Never overwrite the input manifest with output hashes. The installer verifies
the input manifest before patching, the output manifest before activation, and
the installed directory after activation. Public router-only releases attach
their checksum manifest, SBOM, provenance/attestation, and signature alongside
the source release.

## CI gates

Pull-request CI should run without the official Codex application and include:

- clean checkout and lockfile-only dependency installation;
- Go formatting, `go test ./...`, `go vet ./...`, and platform builds for
  Windows plus supported development platforms;
- JavaScript syntax/tests and deterministic renderer-patch fixture tests;
- Python compilation/tests and PowerShell Pester/static-analysis tests;
- fail-closed tests for unknown versions, changed hashes, missing anchors,
  duplicate anchors, partial staging, and simulated signature failure;
- state migration, downgrade refusal, backup, and rollback tests;
- installer path tests using directories containing spaces and non-ASCII
  characters;
- forbidden-artifact and secret scanning, including Git history for releases;
- SBOM generation and policy validation;
- release metadata consistency and `git diff --check`.

Use sanitized synthetic fixtures in CI. Do not upload an ASAR or executable
copied from the official application merely to make a test realistic.

Tag CI repeats every required check on the tagged commit and creates a **draft**
release. Publishing remains a reviewed action in a protected release
environment. The pipeline must have minimal permissions, no long-lived signing
secret in pull-request jobs, no untrusted workflow code with secret access, and
no mutable action tags.

The local Windows qualification stage runs separately on a maintainer machine:

1. Record the exact official installed package metadata and input hashes.
2. Patch into a fresh staging directory.
3. Verify the independent identity and signatures.
4. Exercise install, first launch, account isolation, routing, sticky threads,
   quota/failover, plugins, App Server restart, and clean shutdown.
5. Confirm the official application still launches and its hashes are
   unchanged.
6. Perform one update from the previous router release and one rollback.
7. Attach only the redacted test record to the source release draft.

## Windows signing and identity

There are three independent trust decisions:

1. The official installed input must have a valid Windows package/catalog and
   Authenticode signature from the expected publisher before it is copied.
2. Executables produced by this repository are signed with the router
   publisher's Authenticode certificate.
3. A locally repackaged MSIX, if supported, uses a new package identity and is
   signed as the router publisher. It is never presented as the original
   OpenAI package.

Signing requirements:

- SHA-256 digest and an RFC 3161 timestamp server;
- certificate selected by explicit thumbprint, not "first match";
- private key held in the Windows certificate store, hardware-backed provider,
  or protected CI signing service; never in the repository or build directory;
- verify subject, thumbprint, EKU, validity, chain, timestamp, and final file
  hash after signing;
- log certificate subject/thumbprint and timestamp result, never private-key
  material;
- test certificates are clearly named, scoped to local diagnostics, and never
  used for a public artifact.

Windows signatures on copied official files are verification evidence only.
Any modified file invalidates that evidence. Do not attempt to preserve, copy,
or imitate OpenAI's signature or package publisher identity.

If the initial implementation is unpackaged, install it under a user-writable,
independent directory such as:

```text
%LOCALAPPDATA%\Programs\Codex Subscription Router\
```

If MSIX is later adopted, it must have a distinct package family name,
application user model ID, protocol registration, icons, data location, and
update feed. Changing from unpackaged to MSIX is a migration and is at least a
minor release.

## Local installation layout

Use immutable, side-by-side program versions and keep mutable state outside
them:

```text
%LOCALAPPDATA%\Programs\Codex Subscription Router\
  launcher\
  versions\<router>-<patch-profile>\
  versions\<previous-router>-<previous-profile>\
  current.json

%LOCALAPPDATA%\Programs\Codex Subscription Router Data\
  data\
  accounts\
  logs\
  backups\
  control-token
```

`current.json` contains only a validated relative version-directory name and is
replaced atomically. It must reject absolute paths and traversal. The launcher
opens the selected immutable version. Credentials, account homes, logs, and
state are never stored below `versions`.

Apply owner-only ACLs to account data, control tokens, backup state, and logs
that can contain identifiers. Log values are redacted by default. Build and
diagnostic logs must not contain OAuth tokens, device codes, authorization
headers, raw control tokens, or unmasked email addresses.

## Safe update sequence

There are two update axes and they are handled independently:

- **Router update**: select a newer reviewed router tag.
- **Official application update**: add and review a patch profile for the new
  official version, then rebuild the local copy.

The official application's updater never updates the router copy in place. An
unknown new official version is not an emergency reason to bypass validation;
the previously validated router build remains selected until a new profile is
available.

Update transaction:

1. Preflight disk space, ACLs, toolchain, official package signature/version,
   compatibility profile, signing certificate, and current state schema.
2. Acquire a per-user update lock. A stale lock is validated by process ID and
   creation time before removal.
3. Snapshot configuration/state to a timestamped owner-only backup. Do not copy
   credentials unless recovery genuinely requires it; never export them from
   their account homes.
4. Build and test the new version in a fresh staging directory while the active
   application may continue running.
5. Generate the local SBOM, input/output manifests, build record, and logs.
6. Detect active router, helper, and child App Server processes. If any are
   active, stop with a clear `activation pending` result. Do not kill them or
   close the official Codex application.
7. After the user has ended active Codex work and explicitly retries, migrate a
   copy of state, verify it, then atomically replace `current.json`.
8. Launch the new version and run a bounded health check. Do not reboot Windows.
9. If health checks fail, restore the previous pointer and compatible state
   backup automatically; preserve failure evidence.
10. Mark the update successful only after smoke tests pass. Retain at least the
    current and previous validated program versions and their metadata.

The installer should support separate `stage`, `verify`, `activate`, and
`rollback` operations so build automation can finish without endangering the
session that invoked it.

## Rollback

Rollback is a product feature, not a manual file-copy recipe.

### Program-only rollback

Use when state schemas are compatible:

1. Verify the previous version directory against its saved output manifest.
2. Confirm no router/helper/App Server process is active; otherwise exit without
   making changes.
3. Atomically point `current.json` to the previous validated version.
4. Launch and run the same bounded health check used after update.

### Program and state rollback

Use after a state migration or suspected state corruption:

1. Preserve the failed state for diagnosis without including credentials in a
   shareable archive.
2. Verify the selected backup's schema, checksum, ACL, and source version.
3. Restore into a temporary file/directory and atomically replace state only
   after validation.
4. Select the corresponding program version and run health checks.

Migrations write a new file and replace atomically; they never mutate the only
copy in place. A release that cannot read its predecessor's schema must ship a
tested reverse migration or refuse automatic rollback and provide an explicit
export/import recovery path.

### Official input rollback

The official installation remains untouched, so it needs no rollback from this
project. A previous locally patched version may remain in the private version
store if it passed verification. Never download or redistribute an old official
package through the router. If the required official input is no longer
available locally, fail safely and keep the last validated router version.

## Release checklist

Before tagging:

- [ ] Version, manifests, changelog, and compatibility data agree.
- [ ] Working tree is clean and the reviewed commit is selected.
- [ ] CI passes on Windows and every supported development platform.
- [ ] Negative compatibility and rollback tests pass.
- [ ] Secret and forbidden-artifact scans find no official or private material.
- [ ] SBOM and vulnerability/license review are complete.
- [ ] Local qualification used the exact commit and compatibility profile.
- [ ] The official installed application is unchanged after testing.
- [ ] Upgrade and rollback were exercised without forced process termination.

Before publishing the draft:

- [ ] Tag and any router-only artifacts have verified signatures.
- [ ] Hash manifest matches the final signed bytes.
- [ ] SBOM and provenance name the exact tag and commit.
- [ ] Smoke-test evidence is redacted.
- [ ] Release attachments contain no official/composite application files,
      account data, credentials, private certificates, or diagnostic secrets.
- [ ] Known limitations and the rollback target are in the notes.

After publishing:

- [ ] Reinstall from the public source release in a clean local staging area.
- [ ] Verify tag/signature/checksum before building.
- [ ] Monitor failures without collecting credentials or official binaries.
- [ ] Revoke a compromised signing certificate and publish an advisory; do not
      silently replace assets under an existing tag.

Published tags and assets are immutable. A bad release is deprecated and
superseded by a new version; it is not rebuilt in place.
