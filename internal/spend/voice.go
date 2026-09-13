package spend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Native RealtimeCallClient uses the API multipart shape for a localhost
// provider, but ChatGPT subscriptions require the backend JSON shape. Only
// call creation is routed: the native client joins the returned call ID using
// its existing direct WebRTC sideband. Never reroute an established voice call.
func (g *Gateway) createVoiceCall(w http.ResponseWriter, r *http.Request) {
	if g.Policy == nil || g.Candidates == nil || g.Credentials == nil || g.RoundTrip == nil || g.Upstream == nil || g.Upstream.Scheme != "https" || g.Upstream.User != nil || g.Upstream.RawQuery != "" || g.Upstream.Fragment != "" {
		gatewayError(w, 503, "voice boundary is not configured")
		return
	}
	if r.Header.Get("Content-Encoding") != "" {
		gatewayError(w, 415, "compressed voice setup is unsupported")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || (r.URL.RawQuery != "" && !(len(query) == 2 && len(query["intent"]) == 1 && len(query["architecture"]) == 1 && query.Get("intent") == "quicksilver" && query.Get("architecture") == "avas")) {
		gatewayError(w, 400, "unsupported voice query")
		return
	}
	contentType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "multipart/form-data" || params["boundary"] == "" {
		gatewayError(w, 415, "voice setup requires multipart SDP and session")
		return
	}
	reader := multipart.NewReader(http.MaxBytesReader(w, r.Body, 2<<20), params["boundary"])
	fields := make(map[string]json.RawMessage)
	for {
		part, readErr := reader.NextPart()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			gatewayError(w, 400, "invalid voice setup")
			return
		}
		name := part.FormName()
		if _, duplicate := fields[name]; duplicate || (name != "sdp" && name != "session") || part.FileName() != "" {
			gatewayError(w, 400, "invalid voice fields")
			return
		}
		data, readErr := io.ReadAll(part)
		if readErr != nil {
			gatewayError(w, 413, "voice setup too large")
			return
		}
		if name == "sdp" {
			if !bytes.HasPrefix(data, []byte("v=0")) {
				gatewayError(w, 400, "invalid SDP")
				return
			}
			data, _ = json.Marshal(string(data))
		} else {
			var session map[string]json.RawMessage
			if json.Unmarshal(data, &session) != nil || session == nil {
				gatewayError(w, 400, "invalid voice session")
				return
			}
		}
		fields[name] = data
	}
	if len(fields) != 2 {
		gatewayError(w, 400, "missing voice fields")
		return
	}
	started := time.Now()
	record := Record{Outcome: "not-sent"}
	defer func() {
		record.DurationMillis = time.Since(started).Milliseconds()
		if g.Observe != nil {
			g.Observe(record)
		}
	}()
	candidates, err := g.Candidates(r.Context())
	if errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil {
		candidates, err = g.Candidates(r.Context())
	}
	if err != nil {
		record.FailureKind = "capacity-" + transportFailureKind(err)
		gatewayError(w, 503, "voice capacity unavailable; call not sent")
		return
	}
	decision, release, err := g.Policy.Reserve(candidates)
	if err != nil {
		gatewayError(w, 409, err.Error())
		return
	}
	defer release()
	record.Decision = decision
	credentials, err := g.Credentials(r.Context(), decision.AccountID)
	if err != nil || credentials.Bearer == "" || credentials.AccountID == "" {
		gatewayError(w, 503, "voice subscription credentials unavailable")
		return
	}
	body, _ := json.Marshal(fields)
	destination := *g.Upstream
	destination.Path = "/backend-api/codex/realtime/calls"
	destination.RawPath = ""
	destination.RawQuery = "intent=quicksilver&architecture=avas"
	request, err := http.NewRequestWithContext(r.Context(), "POST", destination.String(), bytes.NewReader(body))
	if err != nil {
		gatewayError(w, 500, "cannot construct voice setup")
		return
	}
	request.Header.Set("Authorization", "Bearer "+credentials.Bearer)
	request.Header.Set("ChatGPT-Account-ID", credentials.AccountID)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/sdp")
	request.Header.Set("User-Agent", "Codex Subscription Router")
	// AVAS rejects a valid SDP with HTTP 400 without its protocol selector.
	// Preserve the native selector rather than guessing a version server-side.
	for _, key := range []string{"Originator", "OpenAI-Alpha", "OpenAI-Beta", "X-Session-Id", "Session-Id", "Thread-Id", "X-Codex-Turn-Metadata", "X-Codex-Session-Id", "X-Codex-Parent-Thread-Id", "Session_id", "Conversation_id"} {
		if v := r.Header.Get(key); v != "" {
			request.Header.Set(key, v)
		}
	}
	response, err := g.RoundTrip.RoundTrip(request)
	if err != nil {
		record.Outcome = "delivery-unknown"
		record.FailureKind = transportFailureKind(err)
		gatewayError(w, 502, "voice call delivery uncertain; not retried")
		return
	}
	defer response.Body.Close()
	record.Status = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		record.Outcome = "upstream-rejected"
		gatewayError(w, 502, fmt.Sprintf("voice subscription returned HTTP %d; not retried", response.StatusCode))
		return
	}
	sdp, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	callID := voiceCallID(response.Header.Get("Location"))
	if err != nil || len(sdp) > 1<<20 || !bytes.HasPrefix(sdp, []byte("v=0")) || callID == "" {
		record.Outcome = "invalid-voice-response"
		gatewayError(w, 502, "invalid voice setup response; not retried")
		return
	}
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", "/v1/realtime/calls/"+callID)
	w.Header().Set("X-Router-Account", decision.AccountID)
	w.WriteHeader(response.StatusCode)
	if _, err := w.Write(sdp); err != nil {
		record.Outcome = "client-disconnected"
	} else {
		record.Outcome = "voice-call-created"
	}
}

func voiceCallID(location string) string {
	path, _, _ := strings.Cut(location, "?")
	parts := strings.Split(path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		id := parts[i]
		if len(id) > 200 {
			continue
		}
		valid := strings.HasPrefix(id, "rtc_") && len(id) > 4
		if len(id) == 36 && id[8] == '-' && id[13] == '-' && id[18] == '-' && id[23] == '-' {
			valid = true
		}
		for _, c := range id {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				valid = false
			}
		}
		if valid {
			return id
		}
	}
	return ""
}
