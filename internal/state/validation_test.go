package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestState(t *testing.T, root string, persisted persistedState) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func validPrimary(home string) Account {
	return Account{ID: "primary", Label: "Primary", CodexHome: home, Enabled: true, Controller: true}
}

func TestOpenRejectsDuplicateAccountIDs(t *testing.T) {
	base := t.TempDir()
	primary := filepath.Join(base, "primary")
	root := filepath.Join(base, "state")
	writeTestState(t, root, persistedState{
		Version: stateVersion,
		Accounts: []Account{
			validPrimary(primary),
			{ID: "PRIMARY", Label: "Duplicate", CodexHome: primary, Enabled: true},
		},
	})
	if _, err := Open(root, primary); err == nil || !strings.Contains(err.Error(), "duplicate account ID") {
		t.Fatalf("Open() error = %v, want duplicate account rejection", err)
	}
}

func TestOpenRejectsUnknownThreadOwner(t *testing.T) {
	base := t.TempDir()
	primary := filepath.Join(base, "primary")
	root := filepath.Join(base, "state")
	writeTestState(t, root, persistedState{
		Version:     stateVersion,
		Accounts:    []Account{validPrimary(primary)},
		ThreadOwner: map[string]string{"thread-1": "missing"},
	})
	if _, err := Open(root, primary); err == nil || !strings.Contains(err.Error(), "unknown account") {
		t.Fatalf("Open() error = %v, want unknown owner rejection", err)
	}
}

func TestOpenRejectsDuplicateJSONOwnerKey(t *testing.T) {
	base := t.TempDir()
	primary := filepath.Join(base, "primary")
	root := filepath.Join(base, "state")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	encodedPrimary, err := json.Marshal(primary)
	if err != nil {
		t.Fatal(err)
	}
	contents := []byte(`{"version":1,"accounts":[{"id":"primary","label":"Primary","codexHome":` + string(encodedPrimary) + `,"enabled":true,"controller":true,"createdAt":0}],"threadOwner":{"thread-1":"primary","thread-1":"primary"}}`)
	if err := os.WriteFile(filepath.Join(root, "state.json"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, primary); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("Open() error = %v, want duplicate JSON key rejection", err)
	}
}

func TestOpenRejectsSecondaryHomeOutsideManagedTree(t *testing.T) {
	base := t.TempDir()
	primary := filepath.Join(base, "primary")
	root := filepath.Join(base, "state")
	external := filepath.Join(base, "must-not-touch")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(external, "marker")
	if err := os.WriteFile(marker, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestState(t, root, persistedState{
		Version: stateVersion,
		Accounts: []Account{
			validPrimary(primary),
			{ID: "secondary", Label: "Secondary", CodexHome: external, Enabled: true},
		},
	})
	if _, err := Open(root, primary); err == nil || !strings.Contains(err.Error(), "outside its managed location") {
		t.Fatalf("Open() error = %v, want managed-tree rejection", err)
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "safe" {
		t.Fatalf("external path was changed: contents=%q err=%v", contents, err)
	}
}

func TestSetThreadOwnersValidatesWholeBatchAndPersistsOnce(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "state"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Secondary")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadOwners(map[string]string{
		"thread-1": "primary",
		"thread-2": secondary.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadOwners(map[string]string{
		"thread-3": secondary.ID,
		"thread-4": "missing",
	}); err == nil {
		t.Fatal("invalid ownership batch unexpectedly succeeded")
	}
	if _, exists := store.ThreadOwner("thread-3"); exists {
		t.Fatal("part of invalid ownership batch was applied")
	}
	reopened, err := Open(filepath.Join(root, "state"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	if owner, ok := reopened.ThreadOwner("thread-2"); !ok || owner != secondary.ID {
		t.Fatalf("persisted owner = %q, %v", owner, ok)
	}
}

func TestRemoveAccountRemovesAffinitiesAndOnlyManagedDirectory(t *testing.T) {
	base := t.TempDir()
	store, err := Open(filepath.Join(base, "state"), filepath.Join(base, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Secondary")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondary.CodexHome, "auth.json"), []byte("test credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadOwner("thread-1", secondary.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveAccount(secondary.ID); err != nil {
		t.Fatal(err)
	}
	if _, exists := store.Account(secondary.ID); exists {
		t.Fatal("removed account remains in memory")
	}
	if _, exists := store.ThreadOwner("thread-1"); exists {
		t.Fatal("removed account affinity remains in memory")
	}
	if _, err := os.Stat(filepath.Join(store.root, "accounts", secondary.ID)); !os.IsNotExist(err) {
		t.Fatalf("managed account directory still exists: %v", err)
	}
	if _, err := Open(filepath.Join(base, "state"), filepath.Join(base, "primary")); err != nil {
		t.Fatalf("reopen after removal: %v", err)
	}
}

func TestRemoveAccountRefusesPrimaryAndTamperedHome(t *testing.T) {
	base := t.TempDir()
	store, err := Open(filepath.Join(base, "state"), filepath.Join(base, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveAccount("primary"); err == nil {
		t.Fatal("primary removal unexpectedly succeeded")
	}
	secondary, err := store.AddAccount("Secondary")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalidID := range []string{"../" + secondary.ID, `..\` + secondary.ID} {
		if err := store.RemoveAccount(invalidID); err == nil {
			t.Fatalf("account removal accepted path-bearing ID %q", invalidID)
		}
		if _, exists := store.Account(secondary.ID); !exists {
			t.Fatalf("path-bearing ID %q removed the managed account", invalidID)
		}
	}
	external := filepath.Join(base, "external")
	if err := os.MkdirAll(external, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(external, "marker")
	if err := os.WriteFile(marker, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index := range store.accounts {
		if store.accounts[index].ID == secondary.ID {
			store.accounts[index].CodexHome = external
		}
	}
	if err := store.RemoveAccount(secondary.ID); err == nil {
		t.Fatal("tampered account removal unexpectedly succeeded")
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "safe" {
		t.Fatalf("external path was changed: contents=%q err=%v", contents, err)
	}
}
