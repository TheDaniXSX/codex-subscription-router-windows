package spend

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"syscall"
	"time"
)

// Gateway is an HTTP inference boundary, not a general-purpose proxy. It
// uses stateless HTTP requests with full input; WebSocket delta contexts are
// rejected rather than charged to a different account without their history.
type Gateway struct {
	Policy      *Policy
	Token       string
	Candidates  func(context.Context) ([]Candidate, error)
	Credentials func(context.Context, string) (Credentials, error)
	// RoundTrip must have a fixed, trusted destination; requests never choose it.
	RoundTrip http.RoundTripper
	Upstream  *url.URL
	Observe   func(Record)
}

type Credentials struct{ Bearer, AccountID string }
type Record struct {
	Decision
	Status         int    `json:"status"`
	Outcome        string `json:"outcome"`
	DurationMillis int64  `json:"durationMillis"`
	ThreadID       string `json:"threadId,omitempty"`
	TurnID         string `json:"turnId,omitempty"`
	Subagent       bool   `json:"subagent"`
	FailureKind    string `json:"failureKind,omitempty"`
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if len(g.Token) < 32 || (subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Codex-Spend-Token")), []byte(g.Token)) != 1 && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+g.Token)) != 1) {
		gatewayError(w, 401, "unauthorized")
		return
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Upgrade") != "" {
		gatewayError(w, 403, "browser and WebSocket traffic is not qualified")
		return
	}
	if r.Method == "POST" && (r.URL.Path == "/v1/live" || r.URL.Path == "/v1/realtime/calls") {
		g.createVoiceCall(w, r)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/v1/models" {
		g.catalog(w, r)
		return
	}
	compact := r.URL.Path == "/v1/responses/compact"
	if r.Method != "POST" || (r.URL.Path != "/v1/responses" && !compact) || r.URL.RawQuery != "" {
		gatewayError(w, 404, "unsupported inference operation")
		return
	}
	if g.Policy == nil || g.Candidates == nil || g.Credentials == nil || g.RoundTrip == nil || g.Upstream == nil || g.Upstream.Scheme != "https" || g.Upstream.User != nil || g.Upstream.RawQuery != "" || g.Upstream.Fragment != "" {
		gatewayError(w, 503, "inference boundary is not configured")
		return
	}
	if r.Header.Get("Content-Encoding") != "" {
		gatewayError(w, 415, "compressed requests are not qualified")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		gatewayError(w, 413, "inference request too large")
		return
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil || envelope == nil {
		gatewayError(w, 400, "invalid inference JSON")
		return
	}
	// Account-bound continuation handles cannot be blindly moved. Refuse them
	// until the exact native transport has a verified full-context replay path.
	for _, key := range []string{"previous_response_id", "conversation"} {
		if v, ok := envelope[key]; ok && string(v) != "null" && string(v) != "\"\"" {
			gatewayError(w, 409, "account-bound continuation requires a qualified replay path")
			return
		}
	}
	// Preserve encrypted reasoning byte-for-byte. Cross-account stateless replay
	// was qualified with the installed Astra runtime; upstream rejection is never
	// repaired by stripping reasoning or silently spending from another account.
	if v, ok := envelope["background"]; ok && string(v) != "false" {
		gatewayError(w, 400, "background responses are not qualified")
		return
	}
	capacityStarted := time.Now()
	candidates, err := g.Candidates(r.Context())
	// Only retry metadata timeouts, before credentials or inference are sent.
	if errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil {
		candidates, err = g.Candidates(r.Context())
	}
	if err != nil {
		kind := transportFailureKind(err)
		if g.Observe != nil {
			g.Observe(Record{Status: 503, Outcome: "not-sent", FailureKind: "capacity-" + kind, DurationMillis: time.Since(capacityStarted).Milliseconds()})
		}
		gatewayError(w, 503, "subscription capacity "+kind+"; inference not sent")
		return
	}
	decision, release, err := g.Policy.Reserve(candidates)
	if err != nil {
		gatewayError(w, 409, err.Error())
		return
	}
	defer release()
	started := time.Now()
	record := Record{Decision: decision, Outcome: "not-sent"}
	var metadata map[string]string
	_ = json.Unmarshal(envelope["client_metadata"], &metadata)
	if len(metadata["thread_id"]) <= 64 {
		record.ThreadID = metadata["thread_id"]
	}
	if len(metadata["turn_id"]) <= 64 {
		record.TurnID = metadata["turn_id"]
	}
	record.Subagent = metadata["x-openai-subagent"] != ""
	defer func() {
		record.DurationMillis = time.Since(started).Milliseconds()
		if g.Observe != nil {
			g.Observe(record)
		}
	}()
	credentials, err := g.Credentials(r.Context(), decision.AccountID)
	if err != nil || credentials.Bearer == "" || credentials.AccountID == "" {
		gatewayError(w, 503, "selected subscription credentials unavailable")
		return
	}
	destination := *g.Upstream
	if compact {
		destination.Path += "/compact"
	}
	upstream, err := http.NewRequestWithContext(r.Context(), "POST", destination.String(), bytes.NewReader(body))
	if err != nil {
		gatewayError(w, 500, "cannot construct inference request")
		return
	}
	upstream.Header.Set("Authorization", "Bearer "+credentials.Bearer)
	upstream.Header.Set("ChatGPT-Account-ID", credentials.AccountID)
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "text/event-stream")
	// Do not forward caller cookies, account IDs, proxy headers or credentials.
	upstream.Header.Set("User-Agent", "Codex Subscription Router")
	for _, key := range []string{"Originator", "OpenAI-Beta", "X-Codex-Turn-Metadata", "X-Codex-Session-Id", "X-Codex-Parent-Thread-Id", "X-OpenAI-Subagent"} {
		if value := r.Header.Get(key); value != "" {
			upstream.Header.Set(key, value)
		}
	}
	response, err := g.RoundTrip.RoundTrip(upstream)
	if err != nil {
		record.Outcome = "delivery-unknown"
		record.FailureKind = transportFailureKind(err)
		gatewayError(w, 502, "inference "+record.FailureKind+"; delivery uncertain; not retried to avoid duplicate spending")
		return
	}
	defer response.Body.Close()
	record.Status = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		record.Outcome = "upstream-rejected"
		// Never redirect or retry onto another identity. Do not expose upstream
		// error bodies, which may echo sensitive request data or credentials.
		gatewayError(w, 502, fmt.Sprintf("selected subscription returned HTTP %d; no automatic retry", response.StatusCode))
		return
	}
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.Header().Set("X-Router-Account", decision.AccountID)
	w.Header().Set("X-Router-Decision", strconv.FormatUint(decision.Sequence, 10))
	if compact {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, (32<<20)+1))
		if readErr != nil || len(data) > 32<<20 || !json.Valid(data) {
			record.Outcome = "invalid-compaction-response"
			gatewayError(w, 502, "invalid compaction response; not retried")
			return
		}
		_, writeErr := w.Write(data)
		if writeErr != nil {
			record.Outcome = "client-disconnected"
		} else {
			record.Outcome = "completed"
		}
		return
	}
	w.WriteHeader(response.StatusCode)
	controller := http.NewResponseController(w)
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 32*1024), 32<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		terminal := ""
		if bytes.HasPrefix(line, []byte("data:")) {
			var event struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(bytes.TrimSpace(line[5:]), &event) == nil {
				switch event.Type {
				case "response.completed":
					terminal = "completed"
				case "response.failed", "response.incomplete", "error":
					terminal = "upstream-failed"
				}
			}
		}
		if _, err = w.Write(append(append([]byte(nil), line...), '\n')); err != nil {
			record.Outcome = "client-disconnected"
			return
		}
		if terminal != "" {
			if _, err = w.Write([]byte("\n")); err != nil {
				record.Outcome = "client-disconnected"
				record.FailureKind = transportFailureKind(err)
				return
			}
			if err = controller.Flush(); err != nil {
				record.Outcome = "client-disconnected"
				record.FailureKind = transportFailureKind(err)
				return
			}
			record.Outcome = terminal
			return
		}
		if err = controller.Flush(); err != nil {
			record.Outcome = "client-disconnected"
			record.FailureKind = transportFailureKind(err)
			return
		}
	}
	record.Outcome = "stream-interrupted"
	if err := scanner.Err(); err != nil {
		record.FailureKind = "stream-" + transportFailureKind(err)
	} else {
		record.FailureKind = "stream-eof-before-terminal"
	}
}

// Only bounded categories are exposed; raw network errors can contain secrets.
func transportFailureKind(err error) string {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected-eof"
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return "connection-reset"
	}
	if errors.Is(err, syscall.ECONNABORTED) {
		return "connection-aborted"
	}
	if errors.Is(err, syscall.EPIPE) {
		return "broken-pipe"
	}
	if errors.Is(err, bufio.ErrTooLong) {
		return "event-too-large"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
		return "timeout"
	}
	return "transport-error"
}

// Model discovery is not inference and keeps the caller's account identity.
// Only this fixed read-only endpoint is allowed, never arbitrary proxy URLs.
func (g *Gateway) catalog(w http.ResponseWriter, r *http.Request) {
	if g.Upstream == nil || g.Upstream.Scheme != "https" || g.Upstream.User != nil || g.Upstream.RawQuery != "" || g.Upstream.Fragment != "" || g.RoundTrip == nil {
		gatewayError(w, 503, "catalog unavailable")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 || len(query["client_version"]) != 1 || len(query.Get("client_version")) > 64 {
		gatewayError(w, 400, "invalid catalog query")
		return
	}
	u := *g.Upstream
	u.Path = "/backend-api/codex/models"
	u.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(r.Context(), "GET", u.String(), nil)
	if err != nil {
		gatewayError(w, 500, "catalog unavailable")
		return
	}
	for _, key := range []string{"Authorization", "ChatGPT-Account-ID", "Originator", "User-Agent"} {
		request.Header.Set(key, r.Header.Get(key))
	}
	response, err := g.RoundTrip.RoundTrip(request)
	if err != nil {
		gatewayError(w, 502, "catalog unavailable")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		gatewayError(w, 502, "catalog rejected by upstream")
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(body) > 4<<20 || !json.Valid(body) {
		gatewayError(w, 502, "invalid catalog response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func gatewayError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "router_spend_error", "message": message}})
}
