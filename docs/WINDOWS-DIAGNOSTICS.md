# Windows diagnostics and resource soak

The Windows diagnostics are intentionally conservative. They never start,
stop, suspend, or terminate a process, do not attach a debugger, do not change
the registry, and do not create, remove, or rewrite router data.

## Read-only doctor

Run the default share-safe report from the repository root:

```powershell
pwsh -NoProfile -File .\scripts\doctor_windows.ps1
```

To produce machine-readable output:

```powershell
pwsh -NoProfile -File .\scripts\doctor_windows.ps1 -OutputFormat Json |
  Set-Content .\router-doctor.json
```

Writing that JSON file is performed by the calling shell, not by the doctor.
Review it before sharing even though the default report applies redaction.

The doctor reports:

- router processes selected only by an executable path inside the configured
  application root;
- official Codex processes separately, without reading command lines;
- aggregate working set and handle counts;
- the loopback listener and whether its owning executable is under the router
  application root;
- account enabled/connected/plan status and thread counts without account IDs,
  labels, email addresses, credential homes, profile images, or rate-limit
  payloads;
- a whitelist of version and build-manifest fields;
- byte counts for the active application, persistent state, authenticated
  backup root, failed installations, and known router temporary roots;
- short excerpts from recent logs after replacing credentials, authorization
  headers, email addresses, user-profile paths, device codes, and sensitive
  query parameters.

The release configuration uses `launcher-config.json` schema 2 and
`codex-mux-build.json` schema 2. Both contain a random `controlPort` in the
49152–65535 range and must agree. The doctor refuses live control access if
they disagree or are invalid. `-ControlPort` exists only for an explicit legacy
or recovery diagnostic; there is no assumed fixed port.

The control token is read only after all of these checks succeed:

1. the persisted port records agree;
2. the port is listening on loopback;
3. Windows reports an owning PID; and
4. that PID's executable path is inside the expected router app root.

It is then sent only in `X-Codex-Mux-Token` to the local accounts endpoint. The
token value is never returned or printed. If ownership cannot be verified, the
doctor does not send it and falls back to the non-secret routing state.

`-RevealPaths` is intentionally opt-in and makes the result unsuitable for
sharing without manual review.

## Normal-use performance guidance

Codex Subscription Router is a separate Electron desktop and starts one Codex
App Server per enabled subscription. Running it beside the official Codex
desktop is supported for qualification, but duplicates much of the desktop
cost. For normal use, open the router **instead of** the official desktop and
manually close whichever application is not needed. The diagnostics never do
that for you.

The doctor lists storage; it does not classify arbitrary directories as safe
to remove. Preview authenticated cleanup separately:

```powershell
pwsh -NoProfile -File .\scripts\cleanup_windows.ps1 -WhatIf
```

Add `-IncludeFailedInstallations` only when intentionally reviewing failed
builds. The lifecycle command validates manifests, hashes, allowed roots, and
reparse points and preserves the active app and account state. Do not replace
that validation with manual recursive deletion.

## Resource soak

A five-minute observation of an already-running router is:

```powershell
pwsh -NoProfile -File .\scripts\measure_windows_router.ps1 `
  -DurationSeconds 300 -IntervalSeconds 5
```

The sampler reads `Win32_Process` counters for executable paths under the app
root. It records process count, working set, private memory, and handles. It
does not launch or terminate the router. The default growth budgets are:

| Counter | Default maximum final-minus-baseline growth |
| --- | ---: |
| Working set | 256 MiB |
| Private memory | 256 MiB |
| Handles | 1,000 |
| Processes | 4 |

Use `-EnforceThresholds` in a qualification job to return exit code 2 when a
budget is exceeded or no router process was observed. The report also includes
peaks and least-squares slopes per hour. A short pass does not prove that a
long-running leak is absent; release qualification should use a representative
duration and workload.

CI analyzes synthetic snapshots through `-FixturePath`. The fixtures include
an unrelated high-memory process to prove that path filtering excludes it and
a controlled growth case to prove that every threshold is active. The hermetic
test is:

```powershell
pwsh -NoProfile -File .\tests\windows\Test-DoctorAndSoak.ps1
```

The release qualification additionally runs the mux with two synthetic account
servers and performs runtime requests and restart checks. Synthetic results do
not replace the documented real-account Windows qualification.

## Limits

- Process counters are point-in-time operating-system observations and can
  change immediately after each sample.
- Some process executable paths may be inaccessible under a hardened Windows
  policy; those processes are omitted instead of guessed.
- Log redaction is defense in depth, not a guarantee that arbitrary user text
  is anonymous. Always review a report before publication.
- The doctor deliberately does not read OAuth files, Chromium profile data,
  conversation databases, MCP credentials, process command lines, or dump
  files.
