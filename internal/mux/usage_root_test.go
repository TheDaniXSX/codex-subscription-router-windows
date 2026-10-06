package mux

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/usage"
)

func usageRootTestStore(t *testing.T) *state.Store {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "shared-state"), filepath.Join(root, "shared-primary"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func newUsageRootTestMux(t *testing.T, store *state.Store, root string, enabled bool) *Multiplexer {
	t.Helper()
	m, err := New(Options{RealExecutable: "not-started", Store: store, Output: io.Discard, RequestSpending: enabled, UsageRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}

func TestUsageRootExplicitKeepsTelemetryOutsideSharedStore(t *testing.T) {
	store := usageRootTestStore(t)
	root := filepath.Join(t.TempDir(), "dev", "calibration")
	m := newUsageRootTestMux(t, store, root, true)
	if m.usageLedger == nil {
		t.Fatal("DEV ledger not initialized", m.usageTelemetry.err)
	}
	if m.store != store || m.usageRoot != root || m.usageRelations.path != filepath.Join(root, "usage-relations.json") {
		t.Fatal("account store or telemetry path changed unexpectedly")
	}
	ctx := context.Background()
	if err := m.usageLedger.Begin(ctx, usage.Record{RequestID: "shared-request", RootTurnID: "shared-chat:turn-1", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	usageRelationsClientTurn(m, "shared-chat", "turn-1")
	for _, name := range []string{"usage-ledger.sqlite", "usage-relations.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("DEV artifact %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(store.Root(), name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("telemetry appeared in shared store: %s (%v)", name, err)
		}
	}
	m.Close()
	reopened := newUsageRootTestMux(t, store, root, true)
	view, err := reopened.usageLedger.ViewTurn(ctx, "shared-chat:turn-1")
	if err != nil || len(view.Requests) != 1 {
		t.Fatalf("separate ledger was not reopened: %+v %v", view, err)
	}
}

func TestUsageRootEmptyPreservesLegacyStoreRoot(t *testing.T) {
	for _, root := range []string{"", " \t "} {
		t.Run(root, func(t *testing.T) {
			store := usageRootTestStore(t)
			m := newUsageRootTestMux(t, store, root, true)
			if m.usageLedger == nil || m.usageRoot != store.Root() {
				t.Fatalf("legacy root not retained: %q %v", m.usageRoot, m.usageTelemetry.err)
			}
			if _, err := os.Stat(filepath.Join(store.Root(), "usage-ledger.sqlite")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUsageRootFailureDoesNotBlockMuxOrWriteFallbackLedger(t *testing.T) {
	store := usageRootTestStore(t)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newUsageRootTestMux(t, store, blocked, true)
	if m.usageLedger != nil || m.usageTelemetry.err == "" || m.store != store {
		t.Fatal("ledger failure must be diagnostic while mux creation succeeds")
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "usage-ledger.sqlite")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid explicit path fell back to shared store")
	}
	contents, err := os.ReadFile(blocked)
	if err != nil || string(contents) != "unchanged" {
		t.Fatal("invalid root target was modified")
	}
}

func TestUsageRootDisabledDoesNotCreateTelemetryFiles(t *testing.T) {
	store := usageRootTestStore(t)
	root := filepath.Join(t.TempDir(), "disabled-usage")
	m := newUsageRootTestMux(t, store, root, false)
	if m.usageLedger != nil || m.usageRelations != nil || m.usageTelemetry.err != "" {
		t.Fatal("disabled telemetry initialized storage")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disabled telemetry created directory")
	}
}
