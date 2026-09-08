package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRoutingModePersistenceAndAccountLifecycle(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "state"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	if store.RoutingMode().Mode != "auto" {
		t.Fatal("default must be auto")
	}
	before, _ := os.ReadFile(store.path)
	if err := store.SetRoutingMode(RoutingMode{Mode: "account", AccountID: "primary"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(store.path)
	if string(before) != string(after) {
		t.Fatal("routing preference altered legacy state")
	}
	reopened, err := Open(store.root, store.primaryCodexHome)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.RoutingMode() != store.RoutingMode() {
		t.Fatal("preference not persisted")
	}
	for _, invalid := range []RoutingMode{{}, {Mode: "auto", AccountID: "primary"}, {Mode: "account", AccountID: "../bad"}, {Mode: "account", AccountID: "missing"}} {
		if err := store.SetRoutingMode(invalid); err == nil {
			t.Fatalf("accepted %#v", invalid)
		}
	}
	account, err := store.AddAccount("Secondary")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRoutingMode(RoutingMode{Mode: "account", AccountID: account.ID}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := store.UpdateAccount(account.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	if store.RoutingMode().Mode != "auto" {
		t.Fatal("disable must clear preference")
	}
	if err := store.SetRoutingMode(RoutingMode{Mode: "account", AccountID: account.ID}); err == nil {
		t.Fatal("selected disabled account")
	}
	enabled := true
	if _, err := store.UpdateAccount(account.ID, nil, &enabled); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRoutingMode(RoutingMode{Mode: "account", AccountID: account.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveAccount(account.ID); err != nil {
		t.Fatal(err)
	}
	if store.RoutingMode().Mode != "auto" {
		t.Fatal("delete must clear preference")
	}
}

func TestRoutingModeConcurrentChangesAndFailedPersistence(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "state"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for index := 0; index < 8; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			mode := RoutingMode{Mode: "auto"}
			if index%2 == 0 {
				mode = RoutingMode{Mode: "account", AccountID: "primary"}
			}
			if err := store.SetRoutingMode(mode); err != nil {
				t.Error(err)
			}
			_ = store.RoutingMode()
		}(index)
	}
	workers.Wait()
	reopened, err := Open(store.root, store.primaryCodexHome)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.RoutingMode() != store.RoutingMode() {
		t.Fatal("disk and memory diverged")
	}
	previous := store.RoutingMode()
	// A directory at the destination forces an atomic replacement failure.
	path := filepath.Join(store.root, "routing-mode.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRoutingMode(RoutingMode{Mode: "account", AccountID: "primary"}); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if store.RoutingMode() != previous {
		t.Fatal("failed write changed active mode")
	}
}

func TestRoutingModeRejectsCorruptionAndRepairsOldAccountRemoval(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "state"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, "routing-mode.json")
	for _, raw := range []string{`{"version":1,"mode":"auto","mode":"account"}`, `{"version":1,"mode":"auto","unknown":1}`, `{"version":9,"mode":"auto"}`, `{"version":1,"mode":"auto"} {}`} {
		if err := atomicWriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(store.root, store.primaryCodexHome); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if err := atomicWriteFile(path, []byte(`{"version":1,"mode":"account","accountId":"deleted"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.root, store.primaryCodexHome)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.RoutingMode().Mode != "auto" {
		t.Fatal("dangling preference not repaired")
	}
}
