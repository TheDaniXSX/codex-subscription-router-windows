package chromenative

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRouterRequestAuthenticatesWithoutExposingToken(t *testing.T) {
	token := strings.Repeat("ab", 32)
	stateRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateRoot, "control-token"), []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/accounts" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("X-Codex-Mux-Token") != token {
			t.Error("control token header was not forwarded")
		}
		if request.Header.Get("Origin") != "" {
			t.Error("native host must not forge a browser Origin header")
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"accounts":[{"id":"primary"}]}`))
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	bridge := &Bridge{
		config: Config{StateRoot: stateRoot, ControlPort: port},
		client: server.Client(),
	}
	response := bridge.Handle(context.Background(), Request{Protocol: ProtocolVersion, ID: "accounts-1", Type: "router.accounts"})
	if !response.OK {
		t.Fatalf("request failed: %#v", response.Error)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), token) {
		t.Fatal("control token leaked into the native response")
	}
}

func TestRouterRequestNeverFollowsRedirectWithToken(t *testing.T) {
	token := strings.Repeat("ab", 32)
	stateRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateRoot, "control-token"), []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Store(true)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Codex-Mux-Token") != token {
			t.Error("initial loopback request did not contain the control token")
		}
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	bridge := &Bridge{
		config: Config{StateRoot: stateRoot, ControlPort: redirect.Listener.Addr().(*net.TCPAddr).Port},
		client: redirect.Client(),
	}
	response := bridge.Handle(context.Background(), Request{Protocol: ProtocolVersion, ID: "redirect-1", Type: "router.accounts"})
	if response.OK || response.Error == nil {
		t.Fatalf("redirect should fail, got %#v", response)
	}
	if redirected.Load() {
		t.Fatal("redirect target was reached; the token could have leaked")
	}
}

func TestRouterRequestRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"padding":"` + strings.Repeat("x", MaxMessageSize) + `"}`))
	}))
	defer server.Close()
	bridge := &Bridge{
		config: Config{ControlPort: server.Listener.Addr().(*net.TCPAddr).Port},
		client: server.Client(),
	}
	response := bridge.Handle(context.Background(), Request{Protocol: ProtocolVersion, ID: "large-1", Type: "router.health"})
	if response.OK || response.Error == nil || !strings.Contains(response.Error.Message, "exceeds") {
		t.Fatalf("oversized response should fail, got %#v", response)
	}
}
