//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestPackagedLauncherConvertsPrivateProtocolToOnePathArgument(t *testing.T) {
	goExecutable := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	layout := filepath.Join(t.TempDir(), "router protocol layout")
	resources := filepath.Join(layout, "resources")
	if err := os.MkdirAll(resources, 0o700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(layout, "ChatGPT.exe")
	realApp := filepath.Join(layout, realAppName)
	buildGoBinary(t, goExecutable, launcher, ".", "-s -w -H=windowsgui")
	buildGoBinary(t, goExecutable, realApp, "./testdata/child", "-s -w")
	for _, path := range []string{filepath.Join(layout, muxRelativePath), filepath.Join(layout, realCodexRelative)} {
		if err := os.WriteFile(path, []byte("test executable placeholder"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	launcherConfigDirectory := filepath.Join(resources, "codex-router")
	if err := os.MkdirAll(launcherConfigDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(layout, "project % & Unicode-ñ")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := "codex-router://open?" + url.Values{"path": []string{target}}.Encode()
	stateRoot := filepath.Join(layout, "router state")
	writeTestLauncherConfiguration(
		t,
		filepath.Join(launcherConfigDirectory, "launcher-config.json"),
		stateRoot,
		61235,
	)
	outputPath := filepath.Join(layout, "protocol-observation.json")
	command := exec.Command(launcher, link)
	command.Env = testEnvironment(os.Environ(), map[string]string{
		"CODEX_ROUTER_DATA_DIR":    filepath.Join(layout, "ignored-router-root"),
		"CODEX_ROUTER_TEST_OUTPUT": outputPath,
	})
	err := command.Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 37 {
		t.Fatalf("launcher did not propagate child exit 37: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var observed childObservation
	if err := json.Unmarshal(data, &observed); err != nil {
		t.Fatal(err)
	}
	want := []string{"--user-data-dir=" + filepath.Join(stateRoot, profileDirectoryName), target}
	if !reflect.DeepEqual(observed.Arguments, want) {
		t.Fatalf("private protocol was not converted safely:\n got: %#v\nwant: %#v", observed.Arguments, want)
	}
}
