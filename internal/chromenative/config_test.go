package chromenative

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigFailsClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ConfigFileName)
	launcherPath := filepath.Join(root, "launcher-config.json")
	buildPath := filepath.Join(root, "codex-mux-build.json")
	stateRoot := filepath.Join(root, "state")
	writeJSON := func(path string, value any) {
		t.Helper()
		contents, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(launcherPath, map[string]any{"schemaVersion": 2, "stateRoot": stateRoot, "controlPort": 54321})
	writeJSON(buildPath, map[string]any{"schemaVersion": 2, "controlPort": 54321, "otherBuildEvidence": true})
	valid := Config{
		Schema: 2, HostName: HostName,
		ExtensionID:    "abcdefghijklmnopabcdefghijklmnop",
		LauncherConfig: launcherPath, BuildManifest: buildPath,
	}
	writeJSON(path, valid)
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.HostName != HostName || config.StateRoot != stateRoot || config.ControlPort != 54321 {
		t.Fatalf("unexpected resolved config: %#v", config)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	withExtra := strings.TrimSuffix(string(contents), "}") + `,"extra":true}`
	if err := os.WriteFile(path, []byte(withExtra), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown config fields must fail closed")
	}
	writeJSON(path, valid)
	contents, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(contents, []byte(` {"second":true}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("second JSON value should fail closed, got %v", err)
	}
	writeJSON(path, valid)
	writeJSON(buildPath, map[string]any{"schemaVersion": 2, "controlPort": 54322})
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("mismatched manifest ports should fail closed, got %v", err)
	}
}
