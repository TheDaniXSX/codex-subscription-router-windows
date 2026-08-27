# Source-only release artifacts

Public releases are built from Git objects and contain no installed Codex file,
patched ASAR, composite application, account state, credential, certificate, or
private diagnostic. The release tools do not read the installed Codex package.

The source tree includes the attributed MIT-licensed Codex color icon, its ICO
rendering, and the x64 COFF resource consumed by the Go linker. These are
launcher build inputs only; the archive still contains no OpenAI executable or
patched application payload.

## Build and verify

From a clean checkout of the release commit and an empty output directory:

```powershell
python scripts/release/create_source_gate_evidence.py `
  --output artifacts/source-gates.json
python scripts/release/build_source_release.py `
  --source-gates artifacts/source-gates.json `
  --output-dir artifacts/source-release
python scripts/release/verify_source_release.py `
  --input-dir artifacts/source-release
```

The evidence command is a post-gate CI step. The tracked E2E report is included
by digest as supporting context, but its editable markers do not grant release
authority. The trusted workflow order and GitHub attestation establish only
that the automated source gates succeeded for the exact `sourceCommit`.

The build fails when the worktree is dirty. `--allow-dirty` exists for local
tool development only: it still packages the committed `HEAD` blobs and never
packages untracked or ignored content.

The output set is deliberately closed:

- `codex-subscription-router-windows-v<VERSION>.tar.gz`: deterministic source
  archive with one root directory, sorted paths, fixed owner/mode metadata, and
  timestamps derived from the commit. If `SOURCE_DATE_EPOCH` is set, it must
  equal that commit timestamp.
- `*.cdx.json`: CycloneDX 1.5 source/dependency SBOM from `package-lock.json`,
  `go.mod`, and `go.sum`.
- `*.spdx.json`: SPDX 2.3 JSON source/dependency SBOM.
- `*.provenance.json`: in-toto Statement v1 with a SLSA provenance v1 predicate,
  the exact Git commit, builder identity, and SHA-256 subjects.
- `*.source-gates.json`: canonical CI evidence binding status
  `AUTOMATED_GATES_PASSED`, scope `source-gates`, version, exact source commit,
  and SHA-256 of the committed Windows E2E report. It does not claim live E2E
  qualification.
- `SHA256SUMS`: sorted `<sha256>  <bytes>  <relative-path>` records for every
  release attachment except the manifest itself.

The verifier checks the closed artifact set, every hash and byte length, archive
ordering/ownership/modes/timestamps, byte-for-byte correspondence with the Git
commit, SBOM identity and uniqueness, provenance subjects, automated-gate
schema/scope/status/commit/report digest, and the forbidden-payload policy. It
reads archive members directly and does not extract untrusted paths.

## Reproducibility check

Run the builder twice in two empty directories with the same commit,
`SOURCE_DATE_EPOCH`, Python, Node, and Go versions, then compare the files byte
for byte. The source archive depends only on the selected Git commit and epoch;
provenance additionally records the toolchain versions.

```powershell
python -m unittest discover -s tests/release -p "test_*.py" -v
```

The source archive intentionally rejects symlinks, submodules, executable and
package payload suffixes, local state filenames, oversized tracked files, and
well-known private-key/token markers. A release that needs a new binary class
must update and review the policy rather than bypass it.
