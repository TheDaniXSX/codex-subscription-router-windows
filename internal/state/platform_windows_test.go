package state

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsPathsEqualIgnoresCase(t *testing.T) {
	if !pathsEqual(`C:\Users\Example\.codex`, `c:\users\example\.CODEX`) {
		t.Fatal("pathsEqual() should ignore path casing on Windows")
	}
}

func TestOpenRejectsSecondaryHomeJunction(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "state")
	external := filepath.Join(base, "external")
	home := filepath.Join(root, "accounts", "secondary", "codex-home")
	if err := os.MkdirAll(filepath.Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", home, external)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("junction creation is unavailable: %v: %s", err, output)
	}
	primary := filepath.Join(base, "primary")
	persisted := persistedState{
		Version: stateVersion,
		Accounts: []Account{
			validPrimary(primary),
			{ID: "secondary", Label: "Secondary", CodexHome: home, Enabled: true},
		},
	}
	encoded, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, primary); err == nil {
		t.Fatal("Open() accepted a reparse-point secondary home")
	}
}

func TestCanonicalExistingPathResolvesJunctionAndRejectsReparse(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	junction := filepath.Join(root, "junction")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, target)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("junction creation is unavailable: %v: %s", err, output)
	}
	if !samePath(target, junction) {
		t.Fatalf("handle canonicalization did not resolve junction %q", junction)
	}
	if err := ensureNoReparsePath(root, junction); err == nil {
		t.Fatal("ensureNoReparsePath() accepted a junction")
	}
	if _, err := Open(junction, filepath.Join(root, "primary")); err == nil {
		t.Fatal("Open() accepted a reparse-point state root")
	}
}
