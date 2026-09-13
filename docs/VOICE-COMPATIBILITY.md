# Voice compatibility

The local inference provider makes native RealtimeCallClient select the API
multipart request shape (`POST /v1/live` for v3, `/v1/realtime/calls` for v1).
ChatGPT subscriptions instead use JSON `{sdp, session}` at the fixed
`/backend-api/codex/realtime/calls?intent=quicksilver&architecture=avas` endpoint.

The router authenticates the local call, validates and bounds its multipart
fields, reserves the selected subscription using the normal spending policy,
and translates only the envelope. Session contents are preserved. It returns
the SDP and a sanitized Location containing the upstream call ID. The native
client uses its existing direct WebRTC sideband to join that call. No generic
WebSocket proxy or arbitrary destination is enabled.

Subscription selection occurs once per voice call. Changing modes affects new
calls and new text inference requests, not an already established audio session.
Call creation is never retried after dispatch; metadata timeouts may be retried
once before dispatch. Records say `voice-call-created`, not `completed`, because
the gateway cannot observe the lifetime of the direct audio session.

Validation: Go tests cover subscription identity, envelope conversion, returned
SDP/Location, authentication, rejected browser traffic and unsupported inputs,
and no retry after uncertain dispatch. These tests do not certify microphone,
audio playback, native sideband, or backend account entitlement. A real desktop
voice start, spoken response, and voice delegation remain required for end-to-end
qualification after activating the binary.

2026-09-10 update inspection: official Windows package 26.903.9818.0, inner app
26.903.71938, build 8576. Do not treat package discovery as a completed router
upgrade: patch-anchor validation and deployment are separate steps.

The original dry-run failed at `bundled-runtime cache root anchor`. The patcher
has now been adapted and a complete 26.903.9818.0 build passes all 53 Windows
verification checks. Activation is queued pending desktop shutdown; check
`UPGRADE-26903-9818-HANDOFF.md` and the activation log before claiming it is active.

Stream failures: a 200 response may still end before its terminal SSE event.
The gateway now distinguishes scanner/network errors from clean EOF without a
terminal event. Neither is retried automatically, to avoid duplicate spending.

## Real backend qualification: missing native protocol selector

The installed 0.153.4 native client emits `OpenAI-Alpha: quicksilver=v2` plus
`X-Session-Id`, `Session-Id`, and `Thread-Id`. The original gateway allowlist
dropped these headers. A real backend A/B test with generated valid SDP returned
400 `invalid_quicksilver_alpha_header` without the selector and 201 with it.
The corrected gateway preserves the native headers. Its opt-in
`TestVoiceLiveGateway` also received 201 and validated returned SDP and call ID
against the real backend. No microphone, audio, or user history was used.
This qualifies call creation, not audio playback or sideband/delegation across
different account identities, which still require a real desktop call test.
