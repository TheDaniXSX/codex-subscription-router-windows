package mux

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/spend"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

func threadSpendingFixture(t *testing.T) (*Multiplexer, state.Account) {
	t.Helper()
	root := t.TempDir()
	s, err := state.Open(filepath.Join(root, "state"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(Options{Store: s, RealExecutable: "not-started", Output: io.Discard, RequestSpending: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, second
}

func acceptedSpend(thread, account string, sequence uint64) spend.Record {
	return spend.Record{Decision: spend.Decision{AccountID: account, Sequence: sequence}, ThreadID: thread, Status: 200, Outcome: "accepted"}
}

func TestThreadSpendingTracksRequestNotOwnerModeOrCompletionOrder(t *testing.T) {
	m, second := threadSpendingFixture(t)
	ctx := context.Background()
	if err := m.store.SetThreadOwner("task", "primary"); err != nil {
		t.Fatal(err)
	}
	if got := m.ThreadSpending(ctx, "task"); got.Account != nil || got.Request != nil {
		t.Fatal(got)
	}
	m.acceptThreadSpend(acceptedSpend("task", second.ID, 2))
	m.acceptThreadSpend(acceptedSpend("task", "primary", 1)) // late upstream headers
	m.recordSpend(spend.Record{Decision: spend.Decision{AccountID: "primary", Sequence: 1}, ThreadID: "task", Status: 200, Outcome: "completed"})
	if err := m.store.SetRoutingMode(state.RoutingMode{Mode: "account", AccountID: "primary"}); err != nil {
		t.Fatal(err)
	}
	got := m.ThreadSpending(ctx, "task")
	if got.Account.ID != second.ID || got.Request.Sequence != 2 || !got.Persisted {
		t.Fatal(got)
	}
	owner, err := m.ThreadAccount(ctx, "task")
	if err != nil || owner.ID != "primary" {
		t.Fatal(owner, err)
	}
	for i := 3; i < 110; i++ {
		m.recordSpend(acceptedSpend("another-task", "primary", uint64(i)))
	}
	child := acceptedSpend("subagent-task", "primary", 110)
	child.Subagent = true
	m.acceptThreadSpend(child)
	if got := m.ThreadSpending(ctx, "task"); got.Account.ID != second.ID {
		t.Fatal(got)
	}
	if err := m.store.SetRoutingMode(state.RoutingMode{Mode: "auto"}); err != nil {
		t.Fatal(err)
	}
	m.acceptThreadSpend(acceptedSpend("task", "primary", 111))
	if got := m.ThreadSpending(ctx, "task"); got.Account.ID != "primary" {
		t.Fatal(got)
	}
}

func TestThreadSpendingSurvivesRestartAndSequenceReset(t *testing.T) {
	m, second := threadSpendingFixture(t)
	m.acceptThreadSpend(acceptedSpend("task", second.ID, 500))
	primary, ok := m.store.Account("primary")
	if !ok {
		t.Fatal("fixture primary account is missing")
	}
	// Reuse the configured home, not a path derived from the canonical state
	// root: Windows 8.3 aliases and macOS /var can have different spellings.
	s, err := state.Open(m.store.Root(), primary.CodexHome)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &Multiplexer{store: s, requestSpending: true}
	if got := restarted.ThreadSpending(context.Background(), "task"); got.Account.ID != second.ID || !got.Persisted {
		t.Fatal(got)
	}
	restarted.acceptThreadSpend(acceptedSpend("task", "primary", 1))
	if got := restarted.ThreadSpending(context.Background(), "task"); got.Account.ID != "primary" || got.Request.Sequence != 1 {
		t.Fatal(got)
	}
}

func TestThreadSpendingRejectsUnconfirmedRequestsAndSurvivesDiskFailure(t *testing.T) {
	m, second := threadSpendingFixture(t)
	for _, outcome := range []string{"not-sent", "delivery-unknown", "upstream-rejected"} {
		r := acceptedSpend("task", second.ID, 2)
		r.Outcome = outcome
		m.acceptThreadSpend(r)
	}
	r := acceptedSpend("task", second.ID, 2)
	r.Status = 429
	m.acceptThreadSpend(r)
	if got := m.ThreadSpending(context.Background(), "task"); got.Request != nil {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(m.store.Root(), "thread-spending"), []byte("blocked directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.acceptThreadSpend(acceptedSpend("task", second.ID, 3))
	got := m.ThreadSpending(context.Background(), "task")
	if got.Account.ID != second.ID || got.Persisted || got.Error == "" {
		t.Fatal(got)
	}
}

func TestThreadSpendingRetainsRemovedAccountIdentity(t *testing.T) {
	m, second := threadSpendingFixture(t)
	m.acceptThreadSpend(acceptedSpend("task", second.ID, 1))
	if err := m.DeleteAccount(context.Background(), second.ID); err != nil {
		t.Fatal(err)
	}
	got := m.ThreadSpending(context.Background(), "task")
	if got.Account.ID != second.ID || got.Account.Label != "Subscription 2" || got.Account.Status != "unavailable" {
		t.Fatal(got)
	}
}

func TestThreadSpendingConcurrentAcceptanceKeepsNewestDispatch(t *testing.T) {
	m, second := threadSpendingFixture(t)
	var workers sync.WaitGroup
	for sequence := uint64(1); sequence <= 20; sequence++ {
		workers.Add(1)
		go func(seq uint64) {
			defer workers.Done()
			id := "primary"
			if seq == 20 {
				id = second.ID
			}
			m.acceptThreadSpend(acceptedSpend("task", id, seq))
			_ = m.ThreadSpending(context.Background(), "task")
		}(sequence)
	}
	workers.Wait()
	got := m.ThreadSpending(context.Background(), "task")
	if got.Request.Sequence != 20 || got.Account.ID != second.ID {
		t.Fatal(got)
	}
}
