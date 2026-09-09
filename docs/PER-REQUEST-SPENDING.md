# Per-request spending on Windows

## Contract

The Windows launcher enables `CODEX_MUX_REQUEST_SPENDING=1`. Every inference
handled by its native app servers uses an authenticated, loopback-only HTTP
gateway. A saved explicit account is a strict spending instruction, not a
preference. Unavailable capacity or credentials stops the request without spending
another account. Auto reserves an account for each request using remaining
short/weekly capacity, expiry urgency and current in-flight pressure.

Reservations are synchronized with policy changes. An already reserved request
keeps its identity until completion. Changing mode affects subsequent reservations,
including parent continuations, native subagents and existing resumed tasks.
Storage ownership, plugins and account-management RPCs remain separate.

Disable/delete of the selected spending account requires choosing another mode
first. Quota snapshots are shared for two seconds and rejected after five seconds;
in-flight pressure is not a guarantee of exact remaining tokens. Limits can change
upstream between checking and sending. No automatic cross-account retry is made.

## Transport and privacy

- Native provider overrides disable WebSocket deltas and request/stream retries.
- Full input, including opaque encrypted reasoning, is preserved byte-for-byte.
- Server-side `previous_response_id` / `conversation` handles and background or
  compressed requests are rejected before spending; they need separate qualification.
- Only Responses, compaction and fixed read-only model discovery endpoints exist.
  Redirects are not followed. Caller cookies and inference credentials are replaced
  with the selected subscription's credentials. Official account RPCs refresh auth.
- The gateway secret is random per process, passed by environment rather than
  command line, and never stored in the decision history. Browser origins are blocked.
- `GET /v1/spending` uses the existing authenticated control API. The last 100
  finished decisions are retained in memory, cleared on restart. Records include
  account, policy revision, sequence, outcome, duration and bounded task/turn IDs;
  they contain no prompts, tokens, emails or response content.
- The menu's **Last inference** is the last finished request, not all active requests
  or the conversation's owner. Other running clients can still consume quota.

## Validation and limits

The 26.903.8094.0 package's CLI (also reporting 0.153.4, different executable
hash) passed the same two-account real inference and native synthetic-subagent
tests on 2026-09-09. Renderer syntax and all patch anchors were checked on its
actual extracted payload; this does not substitute for manual visual review.

On 2026-09-09, native CLI 0.153.4 was exercised with two private, temporary copies
of the user's authorized subscription credentials. Two small Astra inferences in
one native task used first Primary and then the selected second subscription.
An additional stateless Astra replay preserved encrypted reasoning across accounts.
No existing user conversations or original auth files were edited.

A separate native-server test used synthetic inference responses to trigger a
real native subagent. Switching policy after the first response routed both the
parent's continuation and its child to the second subscription. This verifies the
native request path, not model quality. Live tests are opt-in via
`ROUTER_LIVE_SPEND_SOURCE` and `ROUTER_LIVE_SPEND_EXE`; the synthetic-inference
variant also sets `ROUTER_LIVE_SPEND_FAKE_INFERENCE=1`.

Unit tests cover strict exhaustion, stale/unknown capacity, concurrent reservations,
identity replacement, uncertain delivery without retries, continuation rejection,
compaction, SSE terminal outcomes, auth, native provider configuration and menu text.

Other native CLI versions, non-Astra models and model-specific quota buckets still
need qualification. Current admission uses the general short/weekly quota snapshot;
an upstream model-specific rejection stops safely rather than trying another account.
Tools launching independent API clients, cloud tasks, or CLI processes outside this
router are not intercepted. This is not a claim of universal endpoint compatibility.

## Installing and rollback

Close the existing router and its tasks before replacing executables. From the
repository, run `scripts/install_windows.ps1 -Force -NoLaunch -SkipDependencyInstall
-BackupRetention 100` after installing the locked dependencies. The installer builds
the mux/launcher, patches the renderer, updates integrity metadata, and preserves
the previous installation as a recoverable backup. Reopen the router afterward.
Do not stop the process hosting an active installation task.

Rollback uses the existing `scripts/rollback_windows.ps1` workflow. An older
launcher/mux restores its old new-chat preference semantics, not strict spending.
Direct non-Windows integrations must explicitly enable the environment flag;
without it their existing routing behavior is unchanged.
