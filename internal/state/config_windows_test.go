//go:build windows

package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSamePathWindowsIgnoresCase(t *testing.T) {
	root := t.TempDir()
	canonical := filepath.Join(root, "Accounts", "Primary", ".codex")
	caseVariant := filepath.Join(root, "accounts", "primary", ".CODEX")

	if !samePath(canonical, caseVariant) {
		t.Fatalf("samePath(%q, %q) = false on Windows", canonical, caseVariant)
	}
}

func TestOpenWindowsDoesNotResyncPrimaryHomeWithDifferentCase(t *testing.T) {
	root := t.TempDir()
	primaryHome := filepath.Join(root, "Primary", ".codex")
	if err := os.MkdirAll(primaryHome, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(primaryHome, "config.toml")
	wantConfig := []byte("model = \"gpt-test\"\n\n[projects.\"C:\\\\work\"]\ntrust_level = \"trusted\"\n")
	if err := os.WriteFile(configPath, wantConfig, 0o600); err != nil {
		t.Fatal(err)
	}

	stateRoot := filepath.Join(root, "mux")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	persisted := persistedState{
		Version: stateVersion,
		Accounts: []Account{{
			ID:         "primary",
			Label:      "Primary",
			CodexHome:  filepath.Join(root, "primary", ".CODEX"),
			Enabled:    true,
			Controller: true,
		}},
		ThreadOwner: map[string]string{},
	}
	encoded, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, "state.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(stateRoot, primaryHome); err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotConfig) != string(wantConfig) {
		t.Fatalf("primary config was treated as isolated and rewritten:\ngot:\n%s\nwant:\n%s", gotConfig, wantConfig)
	}
}
