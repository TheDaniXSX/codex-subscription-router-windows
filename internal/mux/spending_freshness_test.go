package mux

import (
	"context"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

func TestSlowCapacityRefreshIsFreshWhenItCompletes(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{{AccountID: "primary", UsedPercent: 62, DelayMilliseconds: map[string]int{"account/rateLimits/read": 5200}}}, 15*time.Second)
	started := time.Now()
	candidates, err := h.mux.spendCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 5*time.Second {
		t.Fatal("test did not exercise old expiry threshold")
	}
	if len(candidates) != 1 || !candidates[0].Known || candidates[0].Remaining != 38 {
		t.Fatalf("invalid fresh quota: %+v", candidates)
	}
	decision, release, err := h.mux.spendPolicy.Reserve(candidates)
	if err != nil {
		t.Fatalf("freshly fetched capacity incorrectly rejected: %v", err)
	}
	release()
	if decision.AccountID != "primary" {
		t.Fatal(decision)
	}
	cached, err := h.mux.spendCandidates(context.Background())
	if err != nil || !cached[0].ObservedAt.Equal(candidates[0].ObservedAt) {
		t.Fatal("cache must preserve the actual completion timestamp")
	}
}

func TestSpendingAllowsSlowInitialResponse(t *testing.T) {
	if spendResponseHeaderTimeout != 5*time.Minute {
		t.Fatal("unexpected header wait bound")
	}
}

func TestStrictCapacityDoesNotWaitForOtherAccount(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{AccountID: "primary", UsedPercent: 62},
		{AccountID: "second", UsedPercent: 20, DelayMilliseconds: map[string]int{"account/rateLimits/read": 5200}},
	}, 15*time.Second)
	if err := h.mux.SetRoutingMode(state.RoutingMode{Mode: "account", AccountID: "primary"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	candidates, err := h.mux.spendCandidates(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].ID != "primary" || !candidates[0].Known {
		t.Fatalf("%+v %v", candidates, err)
	}
}

func TestCapacityWaitCanBeCanceled(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{{AccountID: "primary", UsedPercent: 62}}, 15*time.Second)
	h.mux.spendCacheMu.Lock()
	defer h.mux.spendCacheMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.mux.spendCandidates(ctx); err != context.DeadlineExceeded {
		t.Fatal(err)
	}
}

func TestAutoRechecksResponsiveAccountAfterPeerTimeout(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{AccountID: "primary", UsedPercent: 62},
		{UsedPercent: 20, DelayMilliseconds: map[string]int{"account/rateLimits/read": 5200}},
	}, time.Second)
	candidates, err := h.mux.spendCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	decision, release, err := h.mux.spendPolicy.Reserve(candidates)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if decision.AccountID != "primary" {
		t.Fatal(decision)
	}
}
