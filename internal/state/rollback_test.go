package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRollsBackMemoryWhenPersistenceFails(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	store.path = root // Replacing a directory with a file must fail.

	beforeAccounts := store.Accounts()
	if _, err := store.AddAccount("must roll back"); err == nil {
		t.Fatal("AddAccount() unexpectedly succeeded")
	}
	if got := store.Accounts(); len(got) != len(beforeAccounts) {
		t.Fatalf("AddAccount() left %d in-memory accounts, want %d", len(got), len(beforeAccounts))
	}

	newLabel := "mutated"
	if _, err := store.UpdateAccount("primary", &newLabel, nil); err == nil {
		t.Fatal("UpdateAccount() unexpectedly succeeded")
	}
	primary, ok := store.Account("primary")
	if !ok || primary.Label != beforeAccounts[0].Label {
		t.Fatalf("UpdateAccount() left in-memory label %q, want %q", primary.Label, beforeAccounts[0].Label)
	}

	if err := store.SetThreadOwner("thread-1", "primary"); err == nil {
		t.Fatal("SetThreadOwner() unexpectedly succeeded")
	}
	if _, ok := store.ThreadOwner("thread-1"); ok {
		t.Fatal("SetThreadOwner() left in-memory ownership after persistence failure")
	}
}

func TestRemoveAccountRollsBackBeforeFilesystemCleanupWhenPersistenceFails(t *testing.T) {
	base := t.TempDir()
	store, err := Open(filepath.Join(base, "mux"), filepath.Join(base, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Secondary")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadOwner("thread-1", secondary.ID); err != nil {
		t.Fatal(err)
	}
	store.path = base // Replacing a directory with state JSON must fail.
	if err := store.RemoveAccount(secondary.ID); err == nil {
		t.Fatal("RemoveAccount() unexpectedly succeeded")
	}
	if _, exists := store.Account(secondary.ID); !exists {
		t.Fatal("RemoveAccount() discarded the in-memory account")
	}
	if owner, exists := store.ThreadOwner("thread-1"); !exists || owner != secondary.ID {
		t.Fatalf("RemoveAccount() discarded affinity: owner=%q exists=%v", owner, exists)
	}
	if info, err := os.Stat(secondary.CodexHome); err != nil || !info.IsDir() {
		t.Fatalf("RemoveAccount() touched the account home before persistence: %v", err)
	}
}
