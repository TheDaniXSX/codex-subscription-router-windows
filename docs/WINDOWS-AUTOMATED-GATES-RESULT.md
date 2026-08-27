# Windows automated release gates result

- Generated (UTC): 2026-08-27T15:29:50.7031497+00:00
- Candidate commit: 63841926cae9dff9345b7210a1f5a4dc7631dd5a
- Working tree dirty while tested: true
- Host: Microsoft Windows 11 Pro 10.0.26200 build 26200; PowerShell 7.6.4
- Automated source/synthetic gates: **AUTOMATED_GATES_PASSED**
- Public E2E qualification: **NOT QUALIFIED**
- Elapsed: 70.29 seconds

This report contains only evidence produced by the commands executed in this run. The harness used temporary synthetic binaries and `example.invalid` fake accounts. It did not launch, stop, patch, authenticate, or read state from the installed Codex application.

## Executed automated gates

| Gate | Result | Seconds | Executed evidence |
|---|---:|---:|---|
| SYN-01 - Offline Windows smoke suite | PASS | 55.42 | Proxy, two-account routing, control security, contracts and patcher fixtures passed. |
| SYN-02 - Synthetic packaging lifecycle | PASS | 1.67 | Initial package, overwrite backup, hash manifest, and removal fixture passed. |
| SYN-03 - Synthetic install, update, rollback, and uninstall | PASS | 2.49 | Fresh synthetic install, A->B update, failure rollback, exact rollback, -WhatIf, and uninstall-preserve-state passed. |
| SYN-04 - Protocol and Explorer isolation fixture | PASS | 0.04 | In-memory registry tests passed: independent protocol/verbs, quoting, idempotence, -WhatIf, and compare-and-delete. |
| SYN-05 - Two-account restart and short resource soak | PASS | 10.59 | 2 restarts and 160 requests passed with zero orphan synthetic processes. |
| SYN-06 - Official Chrome/protocol/Explorer invariance | PASS | 0.01 | All redacted host-integration fingerprints were identical before and after synthetic operations. |

## Synthetic soak measurements

- Restart cycles: 2
- Account-scoped requests: 160
- Peak synthetic process count: 6
- Peak working set: 92.16 MiB
- Peak private memory: 77.27 MiB
- Peak handles: 725
- Maximum within-cycle working-set growth: 3.55 MiB
- Maximum within-cycle handle growth: 4
- Remaining synthetic processes: 0

## Gates requiring controlled Windows E2E qualification

- **Signed release artifacts:** sign the project-owned launcher and mux with the public release certificate and timestamp; verify with `signtool verify /pa /all`.
- **Clean disposable VM lifecycle:** install, launch, update from the previous release, inject a failed update, rollback, reboot, and uninstall as a standard user. Prove official Codex package, profile, protocol, Explorer and Chrome state are invariant.
- **Two dedicated test subscriptions:** validate login, rename/disable/enable/logout, sticky ownership, history deduplication, plugins/MCP and controlled quota failover. Do not consume a real reset credit or force quota exhaustion.
- **Desktop UI:** validate single-instance activation, notifications, `codex-router://` URI security, Explorer directory/background verbs, Unicode/long paths, high contrast, keyboard navigation and screen reader labels.
- **Computer Use and Appshots:** validate helper provenance, consent, cancellation, insertion, multi-monitor layouts and supported DPI values without personal content.
- **Long soak:** run at least one stable-host extended session with repeated child crash/recovery and collect private memory, working set, handles, process count and orphan-process evidence.
- **Distribution/legal boundary:** publish source and project-owned build artifacts only; confirm that no OpenAI executable, ASAR, credential, account state, certificate or private screenshot is redistributed.
- **Optional MSIX only if shipped:** build with the Windows SDK, sign, install, update and uninstall it in the VM; otherwise explicitly mark MSIX unsupported for this release.

The candidate must remain `NOT QUALIFIED` for a stable public release until every applicable controlled gate has recorded evidence and the canonical Windows E2E report is changed to `QUALIFIED`. Automated evidence permits only an explicitly labelled source preview/prerelease.
