# Windows Appshots and Computer Use qualification

This document separates static contract evidence from live Windows behavior.
Neither a preserved file tree nor a visible menu item proves that screen capture
or input automation works. A public release must report both classes of evidence
without treating one as a substitute for the other.

## Automated, non-interactive gate

The repository validates these contracts without opening Codex or starting a
Computer Use process:

- `resources/cua_node` is a real directory containing only ordinary files and
  stays below 20,000 files, 1 GiB total, and 256 MiB per file;
- the bounded manifest declares `windows`, `x64`, `windows-x64`, safe relative
  Node/module paths, and PE executables at those paths;
- `@oai/cua` is an ES module with a bounded package manifest, a safe relative
  entry point, and a PE helper at `bin/windows/codex-computer-use.exe`;
- the code-mode host is a PE file and the helper transport retains hidden
  stdin/stdout/stderr pipes, `CODEX_HOME`, request timeouts, and no PowerShell,
  cmd, AppleScript, SendInput, or UIAutomation fallback;
- the native-pipe client requires `SKY_CUA_NATIVE_PIPE=1` and
  `SKY_CUA_NATIVE_PIPE_DIRECTORY`, uses `createConnection`, supplies
  `--parent-pid`, and retains 8 MiB outbound and 64 MiB inbound frame limits;
- the complete CUA tree has an approved deterministic digest and is preserved
  byte-for-byte in staging;
- the patched Appshots bridge has exactly two strict checks for
  `CODEX_ROUTER_ENABLE_APPSHOTS="1"`, still requires a native capture bridge,
  and has no unconditional enablement path;
- the launcher defaults that variable to `0`; only the literal value `1` opts
  in. Values such as `true`, `yes`, an empty value, or conflicting duplicates
  remain disabled.

Run the hermetic synthetic contracts:

```powershell
python -m unittest discover -s tests/windows -p "test_*.py" -v
```

Run the same read-only inspection against a staged or installed candidate. This
extracts its ASAR only to a temporary directory and does not launch any helper:

```powershell
python .\scripts\qualify_windows_capabilities.py `
  --app-root "$env:LOCALAPPDATA\Programs\Codex Subscription Router"
```

For the approved `OpenAI.Codex 26.820.9563.0` baseline, the static evidence
recorded on 27 August 2026 is:

| Contract | Recorded value | Result |
| --- | --- | --- |
| CUA runtime | 4,680 files, 334,417,714 bytes | Pass |
| CUA deterministic tree SHA-256 | `2726d48210704778798c61a8df08e8747d8adde0a721464e334acab72eead1cf` | Pass |
| Node/runtime | `24.19.0`; `0.0.9/20260825190732-1bc5ee2d44ce-pr-1350514` | Pass |
| `@oai/cua` | `0.2.3-202608251207-pr-1350514-1bc5ee2d44ce` | Pass |
| Stdio/native-pipe contract | Three hidden pipes; length-prefixed JSON-RPC; 8/64 MiB bounds | Pass |
| Appshots gate | Default off; literal `1`; native bridge required | Pass |

The generated `codex-mux-build.json` records this evidence as
`static-contract-only`. That wording is intentional.

## Live/VM release gate

The following items are **not automated by the repository and were not run from
the development agent session**. Use a disposable Windows VM or a dedicated
qualification session after all active Codex work is complete. Do not use UI
automation to drive Codex itself while an agent session is active.

### Appshots checklist

- [ ] Start the candidate with explicit opt-in:
  `$env:CODEX_ROUTER_ENABLE_APPSHOTS='1'; & '<candidate>\ChatGPT.exe'`.
- [ ] Verify the attachment-menu action and the official Windows shortcut each
  open exactly one native picker.
- [ ] Cancel once and confirm no attachment or temporary capture remains.
- [ ] Select a window and confirm exactly one image is inserted and can be
  removed before submission.
- [ ] Repeat with two monitors, a monitor left of the primary (negative
  coordinates), mixed 100%/150% or 200% scaling, and an occluded window.
- [ ] Exercise denied/unavailable capture consent and confirm a recoverable
  error, no broader fallback capture, and no residual image.
- [ ] Confirm captures and temporary files stay under the router profile, obey
  cleanup, and are absent from backup/diagnostic bundles by default.
- [ ] Relaunch without the opt-in and confirm the capability is unavailable.

### Computer Use checklist

- [ ] Record the candidate path, source version, CUA tree digest, hashes and
  Authenticode status before launch.
- [ ] Request a test that opens Calculator, clicks controls, types `12+30`, reads
  `42`, stops, and returns control.
- [ ] Record a process trace proving the helper and Node runtime came from the
  independent router tree; confirm no PowerShell, cmd, UIAutomation, SendInput,
  or macOS fallback process appeared.
- [ ] Confirm the helper terminates with the turn and no process or pipe remains
  orphaned.
- [ ] Remove or rename the helper in a disposable copy and confirm a precise
  unavailable error with no alternate automation process.
- [ ] Attempt an action requiring UAC/secure desktop and confirm safe refusal;
  never weaken UAC or automate the secure desktop to make the test pass.
- [ ] Run official Codex and the router sequentially and confirm neither can
  attach to the other's pipe/profile.
- [ ] Rebuild the same approved source and repeat once without duplicate consent
  or registration side effects.

### Evidence record

Attach this redacted record to the release qualification issue:

```text
Release commit/tag:
Windows build and VM image:
Official Codex package/ASAR build:
Candidate manifest SHA-256:
Appshots static gate: PASS/FAIL
Appshots picker/cancel/insert: PASS/FAIL
Appshots monitor/DPI/denial matrix: PASS/FAIL
Computer Use static gate: PASS/FAIL
Computer Use Calculator/native process trace: PASS/FAIL
Computer Use unavailable/UAC/isolation: PASS/FAIL
Rebuild regression: PASS/FAIL
Evidence paths (release-owned, redacted):
Reviewer/date:
```

Mask usernames, account IDs, email addresses, tokens, window titles, captured
screen contents, and private filesystem paths. Keep full raw traces only in an
access-controlled release evidence store; never commit them to this repository.

## Release interpretation

Static qualification passing means the port preserved and constrained the
known implementation. Until every live/VM checkbox above passes for the exact
release commit and approved Codex baseline:

- Appshots remains experimental and off by default;
- Computer Use remains unqualified for a public support claim;
- historical macOS/upstream evidence must not be cited as Windows evidence; and
- release notes must state these limitations or omit the capabilities from the
  supported feature list.
