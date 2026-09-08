package mux

import (
	"context"
	"testing"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

func TestPreferredRoutingWinsAndFallsBackWithoutClearingPreference(t *testing.T) {
	for _, tc := range []struct {
		name         string
		used         float64
		disconnected bool
		preferred    bool
	}{
		{"preferred", 90, false, true}, {"depleted", 100, false, false}, {"disconnected", 20, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMuxIntegrationHarness(t, []fakeAppServerSpec{{AccountID: "primary", UsedPercent: tc.used, Disconnected: tc.disconnected}, {UsedPercent: 10}}, defaultIntegrationRequestTimeout)
			mode := state.RoutingMode{Mode: "account", AccountID: "primary"}
			if err := h.mux.SetRoutingMode(mode); err != nil {
				t.Fatal(err)
			}
			account, reason, err := h.mux.chooseAccount(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if (account.ID == "primary") != tc.preferred {
				t.Fatalf("wrong account %s (%#v)", account.ID, reason)
			}
			if h.mux.RoutingMode() != mode {
				t.Fatal("fallback cleared preference")
			}
			account, _, err = h.mux.chooseAccountExcluding(context.Background(), map[string]struct{}{"primary": {}})
			if err != nil || account.ID == "primary" {
				t.Fatalf("excluded account retried: %s %v", account.ID, err)
			}
		})
	}
}

func TestPreferredRoutingSkipsExhaustedShortWindow(t *testing.T) {
	exhausted := 100.0
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{AccountID: "primary", UsedPercent: 1, ShortUsedPercent: &exhausted},
		{UsedPercent: 90},
	}, defaultIntegrationRequestTimeout)
	if err := h.mux.SetRoutingMode(state.RoutingMode{Mode: "account", AccountID: "primary"}); err != nil {
		t.Fatal(err)
	}
	account, reason, err := h.mux.chooseAccount(context.Background())
	if err != nil || account.ID == "primary" || reason.Selection != "preferred-fallback" {
		t.Fatalf("exhausted preference reused: %s %#v %v", account.ID, reason, err)
	}
}

func TestPreferredRoutingQuotaRetryAndExistingAffinity(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{AccountID: "primary", UsedPercent: 90, QuotaOnThreadStart: true},
		{UsedPercent: 10, ThreadStartID: "preferred-fallback-thread"},
	}, defaultIntegrationRequestTimeout)
	mode := state.RoutingMode{Mode: "account", AccountID: "primary"}
	if err := h.mux.SetRoutingMode(mode); err != nil {
		t.Fatal(err)
	}
	response := h.request(1, "thread/start", map[string]any{"cwd": t.TempDir()})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	if h.eventCount("primary", "thread/start") != 1 || h.eventCount(h.accounts()[1].ID, "thread/start") != 1 {
		t.Fatal("quota retry did not try preference exactly once then fallback")
	}
	threadID := threadIDFromResult(response.Result)
	owner, ok := h.store.ThreadOwner(threadID)
	if !ok || owner != h.accounts()[1].ID {
		t.Fatal("wrong fallback owner")
	}
	if h.mux.RoutingMode() != mode {
		t.Fatal("quota retry cleared preference")
	}
	if err := h.mux.SetRoutingMode(state.RoutingMode{Mode: "auto"}); err != nil {
		t.Fatal(err)
	}
	if err := h.mux.SetRoutingMode(mode); err != nil {
		t.Fatal(err)
	}
	response = h.request(2, "turn/start", map[string]any{"threadId": threadID, "input": []any{}})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	if h.eventCount(owner, "turn/start") != 1 || h.eventCount("primary", "turn/start") != 0 {
		t.Fatal("mode change moved existing conversation")
	}
}
