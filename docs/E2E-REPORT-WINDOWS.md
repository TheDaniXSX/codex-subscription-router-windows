# Windows release qualification report

<!-- release-version: 0.2.0 -->
<!-- release-qualification: NOT QUALIFIED -->
<!-- release-commit: PENDING -->
<!-- release-date: PENDING -->

> **Local E2E status: not qualified.** This versioned report is a template and
> has not been completed against a final candidate. It is supporting context,
> not publication authority. Tag CI must rerun every required automated gate
> for its exact `GITHUB_SHA` before emitting external
> `AUTOMATED_GATES_PASSED` evidence. That permits only a source preview, not a
> stable support claim.

## Candidate

| Item | Required value |
| --- | --- |
| Router version | `0.2.0` |
| Patch profile | `windows-26.820.9563.0-x64-r1` |
| Official package | `OpenAI.Codex_26.820.9563.0_x64__2p2nqsd0c76g0` |
| Internal desktop version/build | `26.820.71523` / `7226` |
| Bundled Codex CLI | `0.150.0-alpha.8` |
| Original `app.asar` SHA-256 | `e353c580ef4939d36f4ae32a35c896d089205c1d06b9f711cf78ffa4a3578a8a` |
| Original `codex.exe` SHA-256 | `799ff77125c47b0736ceb36e9b33975bb93d4162bca663730f3a4c90faf2add9` |
| Original `ChatGPT.exe` SHA-256 | `4ec11307b67796338d666f40c431b2804e41669576d3bc350dece8703bf4a114` |
| Release artifact model | Source-only; no official or composite binaries |
| Exact source commit | **PENDING** |
| Qualification date | **PENDING** |
| Windows build and machine architecture | **PENDING** |
| Reviewer | **PENDING** |

## Evidence status

Earlier development runs established that the locked official baseline can be
inventoried, patched locally, and verified without modifying the Microsoft
Store installation. They are useful development evidence, but they do not
qualify the final candidate because its commit did not yet exist.

The latest [Windows automated gate result](WINDOWS-AUTOMATED-GATES-RESULT.md)
records 6/6 automated gates passing: offline smoke, packaging, lifecycle,
in-memory shell contracts, a two-account synthetic restart/soak, and official
host/Chrome invariance fingerprints. It used no real Codex account and did not
exercise a live Codex desktop, Appshots, Computer Use, reset consumption, or a
clean VM. It therefore leaves every corresponding manual checkbox below open.

No item below is recorded as passed until it has been rerun against the commit
placed in the machine-readable header of this report. Committing the completed
report necessarily creates a new commit ID; the tag workflow must therefore
rerun its automated source gates and emit external provenance bound to the
actual `GITHUB_SHA`.

### Source-only and CI gates

- [ ] Clean checkout of the exact candidate commit.
- [ ] Windows CI succeeds, including Go race tests, vetting, JavaScript tests,
      Python tests, PowerShell analysis, packaging checks, and offline smoke.
- [ ] `npm ci --ignore-scripts`, `npm run check:windows`, and
      `npm run release:check` succeed locally.
- [ ] Forbidden-artifact, credential, and private-key scans succeed over the
      release tree and relevant Git history.
- [ ] Source SBOM, checksums, and provenance reference the exact version, tag,
      repository, and commit.

### Official-input and installation gates

- [ ] The installed package identity, version, architecture, catalog, and
      Authenticode signatures match the locked profile.
- [ ] Inventory and dry run succeed before any destination is changed.
- [ ] Fresh install with `-NoLaunch` succeeds into the independent per-user
      paths and the installed-build verifier succeeds.
- [ ] Official input hashes before and after qualification are identical.
- [ ] Official Codex still launches and its package registration, `codex://`
      protocol, and Chrome Native Messaging registration are unchanged.
- [ ] Router uninstall preserves account state when requested and removes only
      router-owned files, shortcut, protocol, and registrations.

### Functional gates with real accounts

- [ ] Primary account works without creating a duplicate credential store.
- [ ] A secondary subscription completes device-code login and remains
      isolated from Primary.
- [ ] New-thread quota routing, sticky follow-ups, persisted ownership, and
      failover are exercised with two accounts.
- [ ] Disabled, logged-out, failed-login, and removed accounts leave no hidden
      App Server process and can be recovered through the UI.
- [ ] Merged history is deterministic and reports partial child failures.
- [ ] Combined quota, Profile, account-scoped Apps/MCP, and reset preview are
      verified without consuming a real reset.
- [ ] A child App Server crash recovers with bounded backoff and no orphaned
      process tree.
- [ ] Closing the router leaves no router-owned process and does not stop the
      official Codex application.

### Windows UX and lifecycle gates

Use [the capability qualification runbook](WINDOWS-CAPABILITY-QUALIFICATION.md)
for Appshots and Computer Use, and
[the shell-integration contract](WINDOWS-SHELL-INTEGRATION.md) for protocol and
Explorer checks. Static fixtures do not satisfy the live items below.

- [ ] Appshots capture and attachment insertion are tested on the supported
      display/DPI configuration, or the feature is disabled and documented.
- [ ] Computer Use performs one bounded click/type task and all created
      processes are accounted for, or the feature is disabled and documented.
- [ ] Notifications use only the router identity, or are documented as
      unsupported.
- [ ] `codex-router://` activation and Explorer commands reach the existing
      router instance with hostile/oversized path inputs rejected; uninstall
      removes only registrations whose values still match router ownership.
- [ ] Upgrade from the previous router version, failed activation recovery,
      program rollback, and state-preserving uninstall all succeed.
- [ ] Backup retention/cleanup is tested with preview mode and never targets
      the official installation or active/selected rollback data.
- [ ] A representative soak run records process count, private working set,
      handle count, and recovery after child failure without exposing account
      data.

## Expected limitations for a source-only 0.2.0 release

The following limitations may remain only when the README and release notes
state them clearly and the reviewer accepts them:

- A future official Store update requires a separately reviewed patch profile.
- A router-specific Chrome extension and host now exist as opt-in source, but
  are not parity/support claims until the extension is published, the host is
  signed, and clean-VM E2E passes; the official host remains untouched.
- The redistributable MSIX path may remain experimental. The supported 0.2.0
  flow is a private unpackaged build from the user's own official installation.
- Router-produced executables in a private local composite build may be
  unsigned because no binaries are distributed by the source-only release.

## Completing this report

After all required gates pass, replace the four machine-readable markers at
the top with:

```text
release-version: 0.2.0
release-qualification: QUALIFIED
release-commit: <40 lowercase hexadecimal characters>
release-date: <YYYY-MM-DD>
```

Fill every pending field, check only items backed by retained redacted
evidence, and add deviations plus their disposition. Do not mark a manual or
real-account test complete based solely on a fixture, successful build, or UI
render. `npm run release:gate` then validates the report structure and release
metadata. Regardless of this report's historical status, the tag workflow must
rerun its automated gates for its own commit. A `NOT QUALIFIED` report restricts
that draft to preview/prerelease status.
