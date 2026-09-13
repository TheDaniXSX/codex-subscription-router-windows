package spend

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func TestStreamFailureCategories(t *testing.T) {
	for _, tc := range []struct {
		err  error
		kind string
	}{
		{io.ErrUnexpectedEOF, "unexpected-eof"},
		{syscall.ECONNRESET, "connection-reset"},
		{syscall.ECONNABORTED, "connection-aborted"},
		{syscall.EPIPE, "broken-pipe"},
	} {
		if got := transportFailureKind(tc.err); got != tc.kind {
			t.Fatalf("got %s, want %s", got, tc.kind)
		}
	}
}

type failingFlushWriter struct{ *httptest.ResponseRecorder }

func (w failingFlushWriter) FlushError() error { return io.ErrClosedPipe }

func TestFailedTerminalFlushIsNotReportedCompleted(t *testing.T) {
	g, _ := gatewayFixture()
	g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\n"))}, nil
	})
	var record Record
	g.Observe = func(r Record) { record = r }
	r := httptest.NewRequest("POST", "http://localhost/v1/responses", strings.NewReader(`{"input":[]}`))
	r.Header.Set("Authorization", "Bearer "+g.Token)
	g.ServeHTTP(failingFlushWriter{httptest.NewRecorder()}, r)
	if record.Outcome != "client-disconnected" {
		t.Fatal(record)
	}
}

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func gatewayFixture() (*Gateway, *int) {
	p, c := fixture()
	count := 0
	u, _ := url.Parse("https://example.invalid/responses")
	g := &Gateway{Policy: p, Token: strings.Repeat("x", 64), Upstream: u, Candidates: func(context.Context) ([]Candidate, error) { return c, nil }, Credentials: func(_ context.Context, id string) (Credentials, error) { return Credentials{"fake-" + id, id}, nil }}
	g.RoundTrip = transport(func(r *http.Request) (*http.Response, error) {
		count++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {}\n\n"))}, nil
	})
	return g, &count
}
func perform(g *Gateway, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1/v1/responses", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+g.Token)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func TestGatewayStrictPerRequest(t *testing.T) {
	g, count := gatewayFixture()
	g.Policy.Set(Mode{"primary"})
	a := perform(g, `{"model":"test","input":[]}`)
	g.Policy.Set(Mode{"second"})
	b := perform(g, `{"model":"test","input":[]}`)
	if a.Code != 200 || b.Code != 200 || a.Header().Get("X-Router-Account") != "primary" || b.Header().Get("X-Router-Account") != "second" || *count != 2 {
		t.Fatal(a, b, *count)
	}
}
func TestGatewayBlocksUnqualifiedContinuityBeforeSpending(t *testing.T) {
	g, count := gatewayFixture()
	for _, body := range []string{`{"previous_response_id":"r"}`, `{"conversation":"c"}`} {
		if perform(g, body).Code != 409 {
			t.Fatal(body)
		}
	}
	if *count != 0 {
		t.Fatal("spent despite unqualified continuity")
	}
}
func TestGatewayDoesNotRetryUncertainDelivery(t *testing.T) {
	g, count := gatewayFixture()
	var record Record
	g.Observe = func(r Record) { record = r }
	g.RoundTrip = transport(func(*http.Request) (*http.Response, error) { *count++; return nil, errors.New("secret upstream error") })
	w := perform(g, `{"input":[]}`)
	if w.Code != 502 || *count != 1 || record.Outcome != "delivery-unknown" || strings.Contains(w.Body.String(), "secret upstream") {
		t.Fatal(w, record, *count)
	}
}

func TestCapacityTimeoutRetriesOnlyMetadata(t *testing.T) {
	g, count := gatewayFixture()
	original := g.Candidates
	reads := 0
	g.Candidates = func(ctx context.Context) ([]Candidate, error) {
		reads++
		if reads == 1 {
			return nil, context.DeadlineExceeded
		}
		return original(ctx)
	}
	if w := perform(g, `{"input":[]}`); w.Code != 200 || reads != 2 || *count != 1 {
		t.Fatalf("status=%d reads=%d sends=%d", w.Code, reads, *count)
	}
}

func TestCapacityFailureIsRecordedWithoutDispatch(t *testing.T) {
	g, count := gatewayFixture()
	reads := 0
	var record Record
	g.Observe = func(r Record) { record = r }
	g.Candidates = func(context.Context) ([]Candidate, error) { reads++; return nil, context.DeadlineExceeded }
	w := perform(g, `{"input":[]}`)
	if w.Code != 503 || reads != 2 || *count != 0 || record.FailureKind != "capacity-timeout" || record.Outcome != "not-sent" {
		t.Fatal(w, reads, *count, record)
	}
}

func TestTransportDiagnosticsAreClassifiedAndNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		err  error
		kind string
	}{
		{context.DeadlineExceeded, "timeout"}, {context.Canceled, "canceled"}, {errors.New("private credential detail"), "transport-error"},
	} {
		g, count := gatewayFixture()
		var record Record
		g.Observe = func(r Record) { record = r }
		g.RoundTrip = transport(func(*http.Request) (*http.Response, error) { *count++; return nil, tc.err })
		w := perform(g, `{"input":[]}`)
		if w.Code != 502 || *count != 1 || record.FailureKind != tc.kind || strings.Contains(w.Body.String(), "private") {
			t.Fatal(w, record, *count)
		}
	}
}
func TestGatewayDoesNotForwardCallerIdentity(t *testing.T) {
	g, _ := gatewayFixture()
	g.Policy.Set(Mode{"second"})
	g.RoundTrip = transport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer fake-second" || r.Header.Get("ChatGPT-Account-ID") != "second" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect credentials")
		}
		return &http.Response{StatusCode: 307, Body: io.NopCloser(strings.NewReader("secret"))}, nil
	})
	r := httptest.NewRequest("POST", "http://localhost/v1/responses", strings.NewReader(`{"input":[]}`))
	r.Header.Set("Authorization", "Bearer "+g.Token)
	r.Header.Set("ChatGPT-Account-ID", "wrong")
	r.Header.Set("Cookie", "private")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 502 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w)
	}
}
func TestGatewayRejectsBrowserAndWebsocket(t *testing.T) {
	g, count := gatewayFixture()
	for _, header := range []string{"Origin", "Upgrade"} {
		r := httptest.NewRequest("POST", "http://localhost/v1/responses", strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+g.Token)
		r.Header.Set(header, "unqualified")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	if *count != 0 {
		t.Fatal(*count)
	}
}

func TestGatewayTerminalOutcomes(t *testing.T) {
	for _, tc := range []struct{ stream, outcome string }{
		{"data: {\"type\":\"response.completed\"}\n\n", "completed"},
		{"data: {\"type\":\"response.failed\"}\n\n", "upstream-failed"},
		{"data: {\"type\":\"response.incomplete\"}\n\n", "upstream-failed"},
		{"data: {\"type\":\"response.created\"}\n\n", "stream-interrupted"},
	} {
		t.Run(tc.outcome+tc.stream, func(t *testing.T) {
			g, _ := gatewayFixture()
			var got Record
			g.Observe = func(r Record) { got = r }
			g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.stream))}, nil
			})
			perform(g, `{"input":[]}`)
			if got.Outcome != tc.outcome {
				t.Fatalf("got %s, want %s", got.Outcome, tc.outcome)
			}
		})
	}
}

func TestGatewayPreservesReasoningAndCompactIdentity(t *testing.T) {
	g, _ := gatewayFixture()
	g.Policy.Set(Mode{"second"})
	body := `{"input":[{"type":"reasoning","encrypted_content":"opaque-test"}]}`
	g.RoundTrip = transport(func(r *http.Request) (*http.Response, error) {
		got, _ := io.ReadAll(r.Body)
		if string(got) != body || r.URL.Path != "/responses/compact" || r.Header.Get("ChatGPT-Account-ID") != "second" {
			t.Fatal("compaction changed history or identity")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"output":[]}`))}, nil
	})
	r := httptest.NewRequest("POST", "http://localhost/v1/responses/compact", strings.NewReader(body))
	r.Header.Set("X-Codex-Spend-Token", g.Token)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != `{"output":[]}` {
		t.Fatal(w)
	}
}

func TestGatewayExhaustedStrictNeverCallsUpstream(t *testing.T) {
	g, count := gatewayFixture()
	g.Policy.Set(Mode{"second"})
	original := g.Candidates
	g.Candidates = func(ctx context.Context) ([]Candidate, error) {
		c, err := original(ctx)
		for i := range c {
			if c[i].ID == "second" {
				c[i].Remaining = 0
			}
		}
		return c, err
	}
	if w := perform(g, `{}`); w.Code != 409 || *count != 0 {
		t.Fatal("exhaustion spent on another account", w, *count)
	}
}

func TestGatewayAuthenticationPrecedesSpending(t *testing.T) {
	g, count := gatewayFixture()
	r := httptest.NewRequest("POST", "http://localhost/v1/responses", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 401 || *count != 0 {
		t.Fatal(w, *count)
	}
}
