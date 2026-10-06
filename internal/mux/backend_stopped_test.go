package mux

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

func TestBackendStoppedCallbackIsSynchronousAndOnce(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(root, "state"), home)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFakeSpec(home, fakeAppServerSpec{AccountID: "primary"}); err != nil {
		t.Fatal(err)
	}
	var callbacks atomic.Int32
	m, err := New(Options{Store: store, Output: io.Discard, RealExecutable: os.Args[0],
		RealArgs:    []string{"-test.run=^TestMuxFakeAppServerProcess$"},
		Environment: append(os.Environ(), fakeProcessEnvironment+"=1", fakeRunRootEnvironment+"="+filepath.Join(root, "runtime")),
		OnBackendStopped: func(accountID string) {
			if accountID == "primary" {
				callbacks.Add(1)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.stopChild("primary", "disabled"); err != nil {
		t.Fatal(err)
	}
	if callbacks.Load() != 1 {
		t.Fatalf("stop returned before one callback: %d", callbacks.Load())
	}
	if err := m.stopChild("primary", "disabled"); err != nil {
		t.Fatal(err)
	}
	if callbacks.Load() != 1 {
		t.Fatalf("duplicate callback: %d", callbacks.Load())
	}
}
