package spend

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func voiceRequest(g *Gateway) *http.Request {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("sdp", "v=0\r\n")
	_ = writer.WriteField("session", `{"model":"voice-test","delegation":{"type":"client"}}`)
	_ = writer.Close()
	r := httptest.NewRequest("POST", "http://localhost/v1/live", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("X-Codex-Spend-Token", g.Token)
	r.Header.Set("OpenAI-Alpha", "quicksilver=v2")
	r.Header.Set("X-Session-Id", "synthetic-session")
	r.Header.Set("Session-Id", "synthetic-session")
	r.Header.Set("Thread-Id", "synthetic-thread")
	return r
}

func TestVoiceCallUsesSelectedSubscriptionAndBackendShape(t *testing.T) {
	g, _ := gatewayFixture()
	g.Policy.Set(Mode{AccountID: "second"})
	var record Record
	g.Observe = func(r Record) { record = r }
	calls := 0
	g.RoundTrip = transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("OpenAI-Alpha") != "quicksilver=v2" || r.Header.Get("X-Session-Id") != "synthetic-session" || r.Header.Get("Session-Id") != "synthetic-session" || r.Header.Get("Thread-Id") != "synthetic-thread" {
			t.Fatal("native voice headers lost")
		}
		if r.URL.Path != "/backend-api/codex/realtime/calls" || r.URL.Query().Get("architecture") != "avas" || r.Header.Get("Authorization") != "Bearer fake-second" || r.Header.Get("ChatGPT-Account-ID") != "second" || r.Header.Get("X-Codex-Spend-Token") != "" {
			t.Fatal(r.URL, r.Header)
		}
		var body struct {
			SDP     string         `json:"sdp"`
			Session map[string]any `json:"session"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.SDP != "v=0\r\n" || body.Session["model"] != "voice-test" {
			t.Fatal(body)
		}
		return &http.Response{StatusCode: 201, Header: http.Header{"Location": []string{"/v1/live/rtc_test"}}, Body: io.NopCloser(strings.NewReader("v=0\r\n"))}, nil
	})
	w := httptest.NewRecorder()
	g.ServeHTTP(w, voiceRequest(g))
	if w.Code != 201 || w.Header().Get("Location") != "/v1/realtime/calls/rtc_test" || calls != 1 || record.Outcome != "voice-call-created" || record.AccountID != "second" {
		t.Fatal(w, calls, record)
	}
}

func TestVoiceRejectsInvalidSetupBeforeSpending(t *testing.T) {
	for _, name := range []string{"auth", "origin", "query", "encoding", "type"} {
		t.Run(name, func(t *testing.T) {
			g, count := gatewayFixture()
			r := voiceRequest(g)
			switch name {
			case "auth":
				r.Header.Del("X-Codex-Spend-Token")
			case "origin":
				r.Header.Set("Origin", "https://untrusted.invalid")
			case "query":
				r.URL.RawQuery = "destination=https://untrusted.invalid"
			case "encoding":
				r.Header.Set("Content-Encoding", "gzip")
			case "type":
				r.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			if w.Code < 400 || *count != 0 {
				t.Fatal(w, *count)
			}
		})
	}
}

func TestVoiceUpstreamFailureIsNotRetried(t *testing.T) {
	g, _ := gatewayFixture()
	calls := 0
	g.RoundTrip = transport(func(*http.Request) (*http.Response, error) { calls++; return nil, io.ErrUnexpectedEOF })
	w := httptest.NewRecorder()
	g.ServeHTTP(w, voiceRequest(g))
	if w.Code != 502 || calls != 1 {
		t.Fatal(w, calls)
	}
}

func TestStreamEOFDiagnostic(t *testing.T) {
	g, _ := gatewayFixture()
	var record Record
	g.Observe = func(r Record) { record = r }
	perform(g, `{"input":[]}`)
	if record.FailureKind != "stream-eof-before-terminal" {
		t.Fatal(record)
	}
}
