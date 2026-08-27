# Windows smoke-test runbook

This runbook validates the Windows port without modifying the official Codex
installation, reading a real `auth.json`, consuming real reset credits, or
stopping the Codex process that is running the test. Run it for every supported
upstream Codex version and release candidate.

## Safety contract

- `Offline` is the default and is safe for CI and an active Codex development
  session. It builds temporary binaries under `%TEMP%` and uses only fake
  accounts ending in `.invalid`.
- The test multiplexer, its two mock `app-server` children, and their files are
  temporary. Cleanup validates that the target is under `%TEMP%` before removal.
- `InstalledReadOnly` hashes and inspects an independently installed router. It
  does not launch, close, update, repair, or roll it back.
- The scripts never inspect the real `%USERPROFILE%\.codex\auth.json` and never
  send a request to ChatGPT. Reset tests use the test-only loopback preview.
- Do not perform the live rollback or restart sections while agents are running
  inside the Codex app being tested. Finish or move that work to an external CLI
  first.

## 1. Hermetic automated suite

Requirements: PowerShell 7 and the Go version recorded by the repository. From
the repository root:

```powershell
pwsh -NoProfile -File .\tests\windows\Invoke-WindowsSmokeTests.ps1 -Mode Offline
```

The command must exit `0` and report all stages as `PASS`. It covers:

| Capability | Automated evidence |
| --- | --- |
| Transparent proxy | The wrapper preserves argv (including spaces), environment, stdout, exit `0`, and a non-zero child exit code. |
| Account isolation | Two mock children receive distinct `CODEX_HOME` values and matching isolated `CODEX_SQLITE_HOME` values. |
| Two accounts | Primary and Secondary mock `app-server` processes initialize together without credentials or network. |
| Sticky routing | A thread is assigned to Primary and its follow-up reaches Primary. |
| Failover | Marking Primary depleted causes `thread/read` on Primary, `thread/resume` on Secondary, retry on Secondary, and persisted ownership transfer. All-depleted returns `-32026`. |
| Plugins | An `app/installed` request marked for Secondary reaches only Secondary and the private routing marker is removed. Unit contracts also cover MCP status and OAuth login. |
| Resets | Test-only balances are assigned per account; consuming a Primary preview decrements only Primary. No real credit is used. |
| Windows process behavior | Targeted Go contracts cover executable naming, process shutdown, and case-insensitive environment replacement. |
| Control API | Private routes reject missing tokens, query-string tokens, and hostile browser origins; router-only token/test variables must not reach children. |
| Diagnostics | Synthetic process/account/log fixtures prove that the doctor filters by app path, redacts private fields, cross-checks the randomized port, remains read-only, and detects controlled memory/handle growth. |

Use `-KeepArtifacts` only for diagnosis. The command prints the exact temporary
directory; delete it after reviewing the mock-only logs.

## 2. Read-only installed-build suite

After installing the router to its independent destination, but while this
Codex task may still be active, run only the read-only suite:

```powershell
pwsh -NoProfile -File .\tests\windows\Invoke-WindowsSmokeTests.ps1 `
  -Mode InstalledReadOnly `
  -AppRoot "$env:LOCALAPPDATA\Programs\Codex Subscription Router" `
  -BackupRoot "$env:LOCALAPPDATA\Programs\.codex-subscription-router-backups"
```

It verifies that the copied build contains one patched `app.asar`, the router
as `codex.exe`, the preserved official binary as `codex.real.exe`, recognizable
Computer Use/CUA artifacts, and a non-empty rollback backup. It prints hashes
and Authenticode status as evidence. An untrusted development signature is
reported, not silently treated as an OpenAI signature.

The installed read-only mode also extracts the candidate ASAR into a temporary
directory and runs the static Appshots/Computer Use contract verifier. This does
not open Codex or start a capture/automation helper. See
`docs/WINDOWS-CAPABILITY-QUALIFICATION.md` for what it proves and the VM checks
that remain mandatory.

Record:

```text
Commit:
Windows version:
Official Codex package version:
Official app.asar SHA-256:
Router app.asar SHA-256:
codex.exe SHA-256:
codex.real.exe SHA-256:
Install root:
Backup root:
Automated result:
Deviations:
```

## 3. Live two-subscription validation

Run this section only after automated tests pass and no active work depends on
restarting the router. Use two subscriptions owned and authorized by the tester.
Do not copy tokens between homes.

1. Launch the independent router, leaving the official Codex app unchanged.
2. Confirm the existing Primary account appears exactly once.
3. Add Secondary through the device-code flow. Confirm the code opens only the
   documented OpenAI verification page and that Secondary obtains its own home.
4. In the profile menu, verify both identities, plan labels, masked emails, and
   pooled quota. Capture a screenshot with private data masked.
5. Start one disposable chat. Record the account badge and thread ID. Send two
   follow-ups and verify the badge and persisted owner do not change.
6. Use only the test-preview quota controls to mark that owner depleted. Send a
   follow-up and verify one failover event, the target badge, preserved history,
   and the updated owner. Do not exhaust a real subscription to force this test.

Expected result: two distinct account homes, one child process per enabled
subscription, sticky follow-ups, and one deterministic ownership change on
failover.

## 4. Plugins and reset UI

1. Open **Settings → Plugins**. Select Primary, then Secondary.
2. For each selection, verify Apps, MCP status, and MCP OAuth state match that
   subscription while installed plugin definitions remain shared.
3. Change a harmless plugin enablement and confirm the managed configuration is
   synchronized without copying project trust or credential files.
4. Open the usage-reset sheet with test mode enabled. Give Primary two preview
   credits and Secondary one. Select Primary, redeem one preview, and verify the
   counts become `1` and `1`.
5. Confirm the final button explicitly says it is a preview/test action. Never
   press a control that would consume a real reset during a smoke run.

## 5. Computer Use

Computer Use cannot be proven by file inspection alone. After the current agent
session is complete:

1. Confirm Windows reports the expected publisher/hash for the copied helper.
2. From the router, request: `Open Calculator with Computer Use, then stop.`
3. Confirm Calculator opens once, the task stops, and no macOS fallback such as
   `osascript` appears in logs.
4. Confirm the helper path belongs to the independent router, not the official
   package, and that no shell window remains open.
5. Repeat once after rebuilding the same upstream version. Existing settings and
   account homes must remain intact.

Record the helper path, hash, exit status, and relevant redacted log excerpt.
Do not count a passing static contract, a visible tool, or the macOS E2E report
as evidence that this Windows test passed.

## 6. Rollback

First rehearse rollback against a disposable copy, never the running install:

1. Copy the release candidate and its backup into a new directory under
   `%TEMP%\codex-router-smoke-rollback-*`.
2. Run the rollback command in dry-run/`-WhatIf` mode. Verify every proposed
   source and destination resolves inside that disposable root.
3. Run the actual rollback on the disposable copy. Compare hashes with the
   recorded pre-update manifest and confirm account-state directories were not
   touched.
4. Only after the rehearsal passes, finish all Codex work, close the independent
   router manually, and exercise production rollback if release validation
   requires it.
5. Relaunch manually and check Primary, Secondary, sticky ownership, plugins,
   and Computer Use. Keep the failed candidate backup until validation ends.

Rollback passes only when the prior application payload is restored byte for
byte, the official Codex package is unchanged, user state survives, and the
router launches normally. A readiness check is not evidence of a completed
rollback; record both separately.

## Release gate

A release is blocked by any automated failure, an unknown upstream hash, missing
rollback payload, cross-account credential/project leakage, sticky-owner drift,
failed history resume, real reset consumption during testing, or a Computer Use
helper that comes from the official package instead of the independent copy.
