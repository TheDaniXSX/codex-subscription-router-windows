package spend

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

// Explicit opt-in: creates one real call using a synthetic offer, no audio or
// user history. Supply an existing auth file and a generated SDP via environment.
func TestVoiceLiveGateway(t *testing.T) {
	path, sdp := os.Getenv("CODEX_ROUTER_VOICE_TEST_AUTH"), os.Getenv("CODEX_ROUTER_VOICE_TEST_SDP")
	if path == "" || sdp == "" {
		t.Skip("live voice qualification not explicitly requested")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read qualification account")
	}
	var auth struct {
		Tokens struct {
			Access  string `json:"access_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil || auth.Tokens.Access == "" || auth.Tokens.Account == "" {
		t.Fatal("invalid qualification account")
	}
	g, _ := gatewayFixture()
	g.Upstream, _ = url.Parse("https://chatgpt.com/backend-api/codex/responses")
	g.RoundTrip = http.DefaultTransport
	g.Credentials = func(context.Context, string) (Credentials, error) {
		return Credentials{Bearer: auth.Tokens.Access, AccountID: auth.Tokens.Account}, nil
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("sdp", sdp)
	_ = w.WriteField("session", `{"instructions":"Isolated transport validation.","audio":{"output":{"voice":"cove"}},"delegation":{"type":"client"},"model":"gpt-live-1-codex"}`)
	_ = w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	r := httptest.NewRequest("POST", "http://localhost/v1/live", &body).WithContext(ctx)
	r.Header.Set("Content-Type", w.FormDataContentType())
	r.Header.Set("X-Codex-Spend-Token", g.Token)
	r.Header.Set("OpenAI-Alpha", "quicksilver=v2")
	r.Header.Set("Originator", "codex_cli_rs")
	result := httptest.NewRecorder()
	g.ServeHTTP(result, r)
	if result.Code != 201 || voiceCallID(result.Header().Get("Location")) == "" {
		t.Fatalf("voice setup rejected: status=%d", result.Code)
	}
	t.Log("Real backend returned 201 through the gateway; SDP and call ID validated; no audio sent")
}
