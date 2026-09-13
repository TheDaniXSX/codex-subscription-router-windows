# Continuity: upgrade to 26.903.9818.0

User explicitly requests upgrading the independent router, retaining all current
features. Official installation is read-only. Do not interrupt the active router
session, touch company GitHub, or discard uncommitted changes.

Source: C:/Program Files/WindowsApps/OpenAI.Codex_26.903.9818.0_x64__2p2nqsd0c76g0/app
Inner package: 26.903.71938, build 8576.
ASAR SHA256: 5d9b0399491060b2756fca70c2e5cca746fa8eb38372b7ad8f236dc69bda0bc6.
Destination: %LOCALAPPDATA%/Programs/Codex Subscription Router.
State sibling: Codex Subscription Router Data; control port 55876.
Never print control tokens, auth files, or whole configuration.

Last full installed desktop: 26.903.8094.0 / inner 26.903.61454.
Installed custom mux SHA256:
085243ea8131a67f623315790e42cf618586077e78a2f51cf0d850e39929fad5.
Contains CUA auxiliary passthrough, per-request spending, capacity retry/isolation,
voice setup translation, and stream error diagnostics. All Go tests and relevant
vet passed. Voice end-to-end remains unverified. Older processes may still run.

Latest dry-run failed before installation at bundled-runtime cache root anchor.
Patcher scripts/patch_windows_app.py contains version-specific runtime/native
aliases; scripts/windows_renderer_26903.py contains old renderer aliases. Adapt
strictly, preserving safety checks, and qualify packed profile menu render.

New voice code: internal/spend/voice.go and voice_test.go; design/limitations:
docs/VOICE-COMPATIBILITY.md. Latest stream incident: 200 then interrupted after
114698 ms; old runtime did not record scanner error. Do not promise all network
failures eliminated or retry uncertain inference.

Use GOMAXPROCS=2 and go test -p 2 to avoid excessive load. No subagents requested
for this turn. Backups must be recoverable; do not change signed upstream EXEs.

## Upgrade work completed 2026-09-10

All native and renderer anchors adapted. Scoped renderer alias maps preserve
strings and injected code; new menu binding resolves Zy as the actual native
function. Profile refresh uses N (query), not M (user ID). Thread panel uses
s(ps), React getter y(), and the new section order. Source hashes registered.
Signed ChatGPT/chrome.dll preserved byte-for-byte using exact reviewed hashes.

96 Windows Python tests, 34 UI tests, real packed menu rendering and native
auxiliary initialize passed. Initial 53-check verifier found only staging ACL;
private ACL applied and full verification repeated.

Direct replacement failed with WinError 32 because current app is open. Original
installation untouched. Complete new build installed as staging at:
%LOCALAPPDATA%/Programs/Codex Subscription Router Update 9818.
Activation helper: artifacts/Apply-Pending-9818.ps1. It validates hashes/paths,
waits at most 30 minutes for user shutdown (never kills processes), backs up old
directory, swaps staging with rollback on failure, then starts the launcher.
See state logs/activate-9818.log for outcome. Do not claim active version until
that log, final manifest and new processes have been checked.

All 53 verification checks passed after ACL hardening. Activation helper started
as PID 39040, waiting for user shutdown for up to 30 minutes. Staging manifest
now declares final destination and fixed recoverable backup. Do not run a second
helper while this PID is active. Full voice audio/delegation still needs a real
user test after restart. Git changes remain local and uncommitted.

## Voice 400 correction, 2026-09-10 18:05

Native offline capture identified dropped OpenAI-Alpha: quicksilver=v2 and
voice session headers. Valid synthetic SDP A/B against the real backend gave
400 invalid_quicksilver_alpha_header without it and 201 with it. The opt-in
TestVoiceLiveGateway then passed with real backend 201 through corrected Go
gateway. No microphone/audio or user task content sent. All Go tests/vet pass.

Both current installation and pending 9818 staging now contain mux SHA256
8712f4d443f884b21ee83b1481203883f6067c94a8cc961fd922286cbc0b1181;
manifests and file ACLs verified. No app restart performed (user asked to fix
before restarting). Old helper PID 39040 is no longer running; no new activation
helper started. Use -ValidateOnly or restart helper only when appropriate.
Real desktop voice audio/sideband remains unverified; creation is qualified.

## 2026-09-11 activation recovery and stream diagnosis

Current installation still 26.903.8094.0; previous helper ended without logging
completion/expiration. Exact termination cause unknown. Replace fragile child
process activation with a one-shot Windows Task Scheduler action, interactive
current-user, limited privilege, no recurring trigger. Helper requests graceful
close after 45 seconds, never forces termination, waits for all router processes
then swaps directories and reopens. Check task result/log and actual manifest.

The reported stream interruption is 44121ms, HTTP 200, generic transport error;
underlying error was not retained and cannot be recovered retrospectively.
New code classifies unexpected EOF/reset/abort/broken pipe/oversized event, and
checks Flush errors so unsuccessful terminal delivery is not recorded completed.
This is a confirmed delivery-reporting bug fix, NOT proof of the original cause.
All Go tests and spend vet pass. No uncertain inference replay added.
Staging mux SHA256 26c7c3c9aa952e236123d46db765314da08e0b87a3973edcd118752cc02c9997.
