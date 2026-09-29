package spend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayCapturesAuthoritativeSSEUsageAndPreservesWireBytes(t *testing.T) {
	g, _ := gatewayFixture()
	var got Record
	g.Observe = func(record Record) { got = record }
	upstreamBytes := ": keepalive\r\ndata: {\"type\":\"response.created\"}\r\n\r\n" +
		"data: {\"type\":\"response.completed\",\r\n" +
		"data: \"response\": {\"id\":\"r1\",\r\n" +
		"data: \"model\":\"gpt-6-astra\",\r\n" +
		"data: \"service_tier\":\"priority\",\r\n" +
		"data: \"usage\":{\"input_tokens\":101,\"output_tokens\":17,\"total_tokens\":118,\"input_tokens_details\":{\"cached_tokens\":20,\"cache_write_tokens\":3},\"output_tokens_details\":{\"reasoning_tokens\":9}},\r\n" +
		"data: \"output\":[{\"type\":\"message\",\"content\":[{\"text\":\"private completion text\"}]}]}}\r\n\r\n"
	g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstreamBytes))}, nil
	})
	w := perform(g, `{"model":"gpt-6-astra","input":[]}`)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), []byte(upstreamBytes)) {
		t.Fatalf("response changed: status=%d\nwant %q\n got %q", w.Code, upstreamBytes, w.Body.String())
	}
	if got.Outcome != "completed" || got.Telemetry == nil || got.Telemetry.Usage == nil {
		t.Fatalf("terminal telemetry missing: %#v", got)
	}
	u := got.Telemetry.Usage
	if int64Value(u.InputTokens) != 101 || int64Value(u.OutputTokens) != 17 || int64Value(u.TotalTokens) != 118 || int64Value(u.CachedInputTokens) != 20 || int64Value(u.CacheWriteTokens) != 3 || int64Value(u.ReasoningTokens) != 9 || u.Source != "responses" {
		t.Fatalf("wrong captured usage: %#v", u)
	}
	if got.Telemetry.ModelServed != "gpt-6-astra" || got.Telemetry.TierServed != "priority" {
		t.Fatalf("served metadata missing: %#v", got.Telemetry)
	}
	if strings.Contains(mustJSON(t, got.Telemetry), "private completion text") {
		t.Fatal("response text leaked into persisted telemetry")
	}
}

func TestGatewayBeginGetsStableIdentityBeforeDispatchAndFinishMetadata(t *testing.T) {
	g, _ := gatewayFixture()
	called := false
	var got Record
	var requestID string
	g.Begin = func(_ context.Context, record *Record) {
		called = true
		if record.Telemetry == nil || len(record.Telemetry.RequestID) != 36 || record.Telemetry.ModelRequested != "gpt-6-astra" || record.Telemetry.TierRequested != "default" || record.Telemetry.ReasoningEffort != "high" || record.Telemetry.ThreadID != "thread-1" || record.Telemetry.TurnID != "turn-2" || record.Telemetry.ParentThreadID != "parent-3" || !record.Telemetry.Subagent {
			t.Fatalf("incomplete begin metadata: %#v", record.Telemetry)
		}
		if record.Telemetry.RequestID[8] != '-' || record.Telemetry.RequestID[13] != '-' || record.Telemetry.RequestID[18] != '-' || record.Telemetry.RequestID[23] != '-' {
			t.Fatalf("request id is not UUID-shaped: %q", record.Telemetry.RequestID)
		}
		record.Telemetry.AccountPlan = "plus"
		requestID = record.Telemetry.RequestID
	}
	g.Observe = func(record Record) { got = record }
	g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
		if !called {
			t.Fatal("upstream dispatched before begin callback")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":0}}}\n\n"))}, nil
	})
	r := httptest.NewRequest("POST", "http://127.0.0.1/v1/responses", strings.NewReader(`{"model":"gpt-6-astra","service_tier":"default","reasoning":{"effort":"high"},"client_metadata":{"thread_id":"thread-1","turn_id":"turn-2","x-openai-subagent":"child"},"input":[]}`))
	r.Header.Set("Authorization", "Bearer "+g.Token)
	r.Header.Set("X-Codex-Parent-Thread-Id", "parent-3")
	g.ServeHTTP(httptest.NewRecorder(), r)
	if !called || got.Telemetry == nil || got.Telemetry.AccountPlan != "plus" || got.Telemetry.Outcome != "completed" || got.Telemetry.FinishedAt.IsZero() || got.Telemetry.Usage == nil || int64Value(got.Telemetry.Usage.InputTokens) != 0 {
		t.Fatalf("begin/final telemetry mismatch: %#v", got)
	}
	if got.Telemetry.RequestID != requestID {
		t.Fatal("Finish changed the Begin request identity")
	}
	if !got.Telemetry.FinishedAt.Before(time.Now().Add(time.Second)) || got.Telemetry.StartedAt.IsZero() || got.Telemetry.FinishedAt.Before(got.Telemetry.StartedAt) {
		t.Fatalf("invalid telemetry timing: %#v", got.Telemetry)
	}
}

func TestGatewayRetainsReportedUsageForIncompleteAndFailedResponses(t *testing.T) {
	for _, event := range []string{"response.incomplete", "response.failed"} {
		t.Run(event, func(t *testing.T) {
			g, _ := gatewayFixture()
			var got Record
			g.Observe = func(record Record) { got = record }
			stream := `data: {"type":"` + event + `","response":{"model":"gpt-6-astra","usage":{"input_tokens":22,"output_tokens":4}}}` + "\n\n"
			g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(stream))}, nil
			})
			perform(g, `{"input":[]}`)
			if got.Outcome != "upstream-failed" || got.Telemetry == nil || got.Telemetry.Usage == nil || int64Value(got.Telemetry.Usage.InputTokens) != 22 || int64Value(got.Telemetry.Usage.OutputTokens) != 4 || got.Telemetry.Usage.TotalTokens != nil {
				t.Fatalf("reported partial usage not preserved: %#v", got)
			}
		})
	}
}

func TestGatewayUnknownUsageStaysAbsentAndCompactionUsageIsCaptured(t *testing.T) {
	t.Run("absent terminal usage", func(t *testing.T) {
		g, _ := gatewayFixture()
		var got Record
		g.Observe = func(record Record) { got = record }
		g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{}}\n\n"))}, nil
		})
		perform(g, `{}`)
		if got.Telemetry == nil || got.Telemetry.Usage != nil {
			t.Fatalf("missing usage was fabricated: %#v", got.Telemetry)
		}
	})
	t.Run("compaction usage", func(t *testing.T) {
		g, _ := gatewayFixture()
		var got Record
		g.Observe = func(record Record) { got = record }
		g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"model":"gpt-6-astra","usage":{"input_tokens":8,"total_tokens":8}}`))}, nil
		})
		r := httptest.NewRequest("POST", "http://localhost/v1/responses/compact", strings.NewReader(`{"input":[]}`))
		r.Header.Set("Authorization", "Bearer "+g.Token)
		g.ServeHTTP(httptest.NewRecorder(), r)
		if got.Outcome != "completed" || got.Telemetry == nil || got.Telemetry.Usage == nil || int64Value(got.Telemetry.Usage.InputTokens) != 8 || got.Telemetry.ModelServed != "gpt-6-astra" || got.Telemetry.Usage.Source != "compaction" {
			t.Fatalf("compaction usage missing: %#v", got)
		}
	})
}

func TestTelemetryBeginPanicDoesNotBreakInference(t *testing.T) {
	g, count := gatewayFixture()
	g.Begin = func(context.Context, *Record) { panic("telemetry storage failure") }
	if response := perform(g, `{"input":[]}`); response.Code != 200 || *count != 1 {
		t.Fatalf("telemetry callback affected inference: status=%d sends=%d", response.Code, *count)
	}
}

func TestGatewayCapturesBoundedUsageFromRejectedJSONWithoutRelayingBody(t *testing.T) {
	g, _ := gatewayFixture()
	var got Record
	g.Observe = func(record Record) { got = record }
	g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(`{"response":{"model":"gpt-6-astra","usage":{"input_tokens":31}}}`))}, nil
	})
	w := perform(g, `{"input":[]}`)
	if w.Code != 502 || strings.Contains(w.Body.String(), `"input_tokens"`) || got.Outcome != "upstream-rejected" || got.Telemetry == nil || got.Telemetry.Usage == nil || int64Value(got.Telemetry.Usage.InputTokens) != 31 {
		t.Fatalf("rejected-response handling mismatch: response=%s record=%#v", w.Body.String(), got)
	}
}

func TestGatewayUsesQualifiedSessionFallbackAndSubagentHeader(t *testing.T) {
	g, _ := gatewayFixture()
	g.Begin = func(_ context.Context, record *Record) {
		if record.Telemetry == nil || record.Telemetry.ThreadID != "123e4567-e89b-12d3-a456-426614174000" || !record.Telemetry.Subagent {
			t.Fatalf("header identity fallback missing: %#v", record.Telemetry)
		}
	}
	g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\n"))}, nil
	})
	r := httptest.NewRequest("POST", "http://localhost/v1/responses", strings.NewReader(`{"input":[]}`))
	r.Header.Set("Authorization", "Bearer "+g.Token)
	r.Header.Set("X-Codex-Session-Id", "123e4567-e89b-12d3-a456-426614174000")
	r.Header.Set("X-OpenAI-Subagent", "true")
	g.ServeHTTP(httptest.NewRecorder(), r)
}

func TestGatewayCapturesNonStreamingJSONResponseUsage(t *testing.T) {
	for _, status := range []string{"completed", "failed", "incomplete"} {
		t.Run(status, func(t *testing.T) {
			g, _ := gatewayFixture()
			var got Record
			g.Observe = func(record Record) { got = record }
			body := `{"model":"gpt-6-astra","status":"` + status + `","usage":{"input_tokens":123,"output_tokens":4,"input_tokens_details":{"cached_tokens":9,"cache_write_tokens":2}}}`
			g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			w := perform(g, `{"model":"gpt-6-astra","stream":false,"input":[]}`)
			if w.Code != 200 || w.Body.String() != body || got.Telemetry == nil || got.Telemetry.Usage == nil || int64Value(got.Telemetry.Usage.CacheWriteTokens) != 2 {
				t.Fatalf("JSON response changed or usage lost: %s %+v", w.Body.String(), got)
			}
			want := "completed"
			if status != "completed" {
				want = "upstream-failed"
			}
			if got.Outcome != want {
				t.Fatalf("outcome=%s want=%s", got.Outcome, want)
			}
		})
	}
}

func TestGatewaySSEPreservesLineEndingsAndInitialBOM(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		t.Run(mustJSON(t, newline), func(t *testing.T) {
			g, _ := gatewayFixture()
			var got Record
			g.Observe = func(record Record) { got = record }
			body := "\xef\xbb\xbfdata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":7}}}" + newline + newline
			g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			w := perform(g, `{}`)
			if w.Body.String() != body || got.Outcome != "completed" || got.Telemetry == nil || got.Telemetry.Usage == nil || int64Value(got.Telemetry.Usage.InputTokens) != 7 {
				t.Fatalf("SSE framing changed or usage lost: got=%q telemetry=%+v", w.Body.String(), got)
			}
		})
	}
}

func TestGatewayKeepsEarlyServingMetadataButOnlyFinalUsage(t *testing.T) {
	g, _ := gatewayFixture()
	var got Record
	g.Observe = func(record Record) { got = record }
	body := "data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-6-sol\",\"service_tier\":\"priority\",\"usage\":{\"input_tokens\":999}}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n"
	g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	perform(g, `{"model":"gpt-6-astra","service_tier":"default"}`)
	if got.Telemetry == nil || got.Telemetry.ModelRequested != "gpt-6-astra" || got.Telemetry.ModelServed != "gpt-6-sol" || got.Telemetry.TierRequested != "default" || got.Telemetry.TierServed != "priority" || got.Telemetry.Usage == nil || int64Value(got.Telemetry.Usage.InputTokens) != 5 {
		t.Fatalf("serving metadata/final usage mismatch: %+v", got.Telemetry)
	}
}

func TestGatewayInterruptedStreamDoesNotPromoteProgressUsage(t *testing.T) {
	g, _ := gatewayFixture()
	var got Record
	g.Observe = func(record Record) { got = record }
	body := "data: {\"type\":\"response.in_progress\",\"response\":{\"model\":\"gpt-6-sol\",\"usage\":{\"input_tokens\":999}}}\n\n" + "data: incomplete"
	g.RoundTrip = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	w := perform(g, `{}`)
	if w.Body.String() != body || got.Outcome != "stream-interrupted" || got.Telemetry == nil || got.Telemetry.Usage != nil || got.Telemetry.ModelServed != "gpt-6-sol" {
		t.Fatalf("interruption lost bytes or invented final usage: %+v", got)
	}
}

func TestGatewayTelemetryIDsDifferAcrossRequests(t *testing.T) {
	g, _ := gatewayFixture()
	ids := map[string]bool{}
	g.Begin = func(_ context.Context, record *Record) {
		if record.Telemetry == nil || ids[record.Telemetry.RequestID] || !isUUID(record.Telemetry.RequestID) {
			t.Fatalf("missing or duplicate request identity: %+v", record.Telemetry)
		}
		ids[record.Telemetry.RequestID] = true
	}
	for range 3 {
		perform(g, `{}`)
	}
	if len(ids) != 3 {
		t.Fatalf("captured request count=%d want=3", len(ids))
	}
}

func int64Value(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
