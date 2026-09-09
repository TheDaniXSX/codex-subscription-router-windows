package mux

import (
	"context"
	"encoding/json"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSelectedSpendingAccountCannotSilentlyResetToAuto(t *testing.T) {
	root := t.TempDir()
	s, err := state.Open(filepath.Join(root, "state"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AddAccount("Second")
	if err != nil {
		t.Fatal(err)
	}
	mode := state.RoutingMode{Mode: "account", AccountID: a.ID}
	if err := s.SetRoutingMode(mode); err != nil {
		t.Fatal(err)
	}
	m := &Multiplexer{store: s, requestSpending: true}
	disabled := false
	if _, err := m.UpdateAccount(context.Background(), a.ID, nil, &disabled); err == nil {
		t.Fatal("disabled spending account")
	}
	if err := m.DeleteAccount(context.Background(), a.ID); err == nil {
		t.Fatal("deleted spending account")
	}
	if s.RoutingMode() != mode {
		t.Fatal("silently enabled Auto")
	}
}

func TestSpendingBindsOldAndNewHistories(t *testing.T) {
	m := &Multiplexer{requestSpending: true}
	for _, method := range []string{"thread/start", "thread/resume", "thread/fork"} {
		var got map[string]any
		if err := json.Unmarshal(m.bindSpendProvider(method, json.RawMessage(`{"threadId":"old","modelProvider":"openai","unknown":42}`)), &got); err != nil {
			t.Fatal(err)
		}
		if got["modelProvider"] != spendProvider || got["threadId"] != "old" || got["unknown"] != float64(42) {
			t.Fatal(got)
		}
	}
}

func TestSpendingConfigurationDeterministicAndPrivate(t *testing.T) {
	m := &Multiplexer{requestSpending: true, spendToken: "new-secret", spendURL: "http://127.0.0.1:12345/v1", realArgs: []string{"app-server"}, environment: []string{"PATH=kept", "codex_spend_gateway_token=stale", "CODEX_SPEND_GATEWAY_TOKEN=other"}}
	if !reflect.DeepEqual(m.spendEnvironment(), []string{"PATH=kept", "CODEX_SPEND_GATEWAY_TOKEN=new-secret"}) {
		t.Fatal("stale token inherited")
	}
	args := m.spendArgs()
	if !reflect.DeepEqual(args, m.spendArgs()) || strings.Contains(strings.Join(args, " "), m.spendToken) {
		t.Fatal("unstable args or secret in command line")
	}
	joined := strings.Join(args, " ")
	for _, expected := range []string{"supports_websockets=false", "request_max_retries=0", "stream_max_retries=0", "requires_openai_auth=true"} {
		if !strings.Contains(joined, expected) {
			t.Fatal("missing safety setting", expected)
		}
	}
}
