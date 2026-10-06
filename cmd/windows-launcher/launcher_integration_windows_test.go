//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type childObservation struct {
	Arguments   []string          `json:"arguments"`
	Environment map[string]string `json:"environment"`
}

func TestPackagedLauncherEndToEnd(t *testing.T) {
	goExecutable := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	layout := filepath.Join(t.TempDir(), "layout with spaces")
	resources := filepath.Join(layout, "resources")
	if err := os.MkdirAll(resources, 0o700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(layout, "ChatGPT.exe")
	realApp := filepath.Join(layout, realAppName)
	buildGoBinary(t, goExecutable, launcher, ".", "-s -w -H=windowsgui")
	buildGoBinary(t, goExecutable, realApp, "./testdata/child", "-s -w")
	for _, path := range []string{
		filepath.Join(layout, muxRelativePath),
		filepath.Join(layout, realCodexRelative),
	} {
		if err := os.WriteFile(path, []byte("test executable placeholder"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	launcherConfigDirectory := filepath.Join(layout, `resources\codex-router`)
	if err := os.MkdirAll(launcherConfigDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	launcherConfigPath := filepath.Join(launcherConfigDirectory, "launcher-config.json")
	stateRoot := filepath.Join(layout, "router state")
	writeTestLauncherConfiguration(t, launcherConfigPath, stateRoot, 61234)

	selfTestRoot := filepath.Join(layout, "self-test must not create")
	selfTest := exec.Command(launcher, selfTestArgument)
	selfTest.Env = testEnvironment(os.Environ(), map[string]string{
		"CODEX_ROUTER_DATA_DIR": selfTestRoot,
		"CODEX_MUX_HOME":        "",
		"CODEX_MUX_STATE_ROOT":  "",
	})
	selfTestOutput, err := selfTest.CombinedOutput()
	if err != nil {
		t.Fatalf("self-test failed: %v\n%s", err, selfTestOutput)
	}
	if !strings.Contains(string(selfTestOutput), "root_source=sidecar:"+sidecarRelativePath) ||
		!strings.Contains(string(selfTestOutput), "state_root="+stateRoot) ||
		!strings.Contains(string(selfTestOutput), "control_port=61234") ||
		!strings.Contains(string(selfTestOutput), "launcher_config_schema=2") {
		t.Fatalf("self-test did not report its non-secret source/root:\n%s", selfTestOutput)
	}
	if _, err := os.Stat(selfTestRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("self-test wrote its state root: %v", err)
	}
	if _, err := os.Stat(stateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("self-test wrote the canonical state root: %v", err)
	}
	diagnostics := exec.Command(launcher, diagnosticsArgument)
	diagnostics.Env = selfTest.Env
	diagnosticsOutput, err := diagnostics.CombinedOutput()
	if err != nil {
		t.Fatalf("diagnostics failed: %v\n%s", err, diagnosticsOutput)
	}
	if !strings.Contains(string(diagnosticsOutput), "process_supervision=windows-job-object") ||
		!strings.Contains(string(diagnosticsOutput), "appshots=disabled-default") {
		t.Fatalf("diagnostics did not report safe defaults:\n%s", diagnosticsOutput)
	}
	if err := os.WriteFile(
		launcherConfigPath,
		[]byte(`{"schemaVersion":1,"stateRoot":"D:\\legacy-diagnostic"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	legacyDiagnostics := exec.Command(launcher, diagnosticsArgument)
	legacyDiagnostics.Env = selfTest.Env
	legacyOutput, err := legacyDiagnostics.CombinedOutput()
	if err != nil {
		t.Fatalf("legacy diagnostics failed: %v\n%s", err, legacyOutput)
	}
	if !strings.Contains(string(legacyOutput), "control_port=48123") ||
		!strings.Contains(string(legacyOutput), "warning=legacy-control-port-48123-upgrade-required") {
		t.Fatalf("legacy diagnostics omitted the upgrade warning:\n%s", legacyOutput)
	}
	writeTestLauncherConfiguration(t, launcherConfigPath, stateRoot, 61234)

	outputPath := filepath.Join(layout, "child-observation.json")
	treeReadyPath := filepath.Join(layout, "tree-ready")
	treeSurvivedPath := filepath.Join(layout, "tree-survived")
	primaryHome := filepath.Join(layout, "primary account")
	arguments := []string{
		`codex://thread/abc?value=two%20words`,
		`two words`,
		`quote"inside`,
		`trailing-backslash\`,
		``,
	}
	command := exec.Command(launcher, arguments...)
	command.Env = testEnvironment(os.Environ(), map[string]string{
		"CODEX_ROUTER_DATA_DIR":           filepath.Join(layout, "ignored-router-root"),
		"CODEX_MUX_HOME":                  filepath.Join(layout, "lower-priority-root"),
		"CODEX_MUX_STATE_ROOT":            filepath.Join(layout, "legacy-root"),
		"CODEX_ROUTER_TEST_OUTPUT":        outputPath,
		"CODEX_HOME":                      primaryHome,
		"CODEX_SQLITE_HOME":               primaryHome,
		"CODEX_SPARKLE_ENABLED":           "true",
		"CODEX_ROUTER_TEST_STDIO":         "1",
		"CODEX_ROUTER_TEST_TREE_READY":    treeReadyPath,
		"CODEX_ROUTER_TEST_TREE_SURVIVED": treeSurvivedPath,
		"CODEX_MUX_CONTROL_PORT":          "50000",
		"CODEX_MUX_CONTROL_TOKEN":         "must-not-reach-child",
		"CODEX_MUX_UI_TESTS":              "1",
	})
	childOutput, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 37 {
		t.Fatalf("launcher did not propagate child exit 37: %v\n%s", err, childOutput)
	}
	if !strings.Contains(string(childOutput), "synthetic-child-stdout") ||
		!strings.Contains(string(childOutput), "synthetic-child-stderr") {
		t.Fatalf("launcher did not propagate child output streams:\n%s", childOutput)
	}
	if _, err := os.Stat(treeReadyPath); err != nil {
		t.Fatalf("synthetic grandchild was not started: %v", err)
	}
	time.Sleep(2300 * time.Millisecond)
	if _, err := os.Stat(treeSurvivedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Job Object did not terminate the synthetic descendant: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var observed childObservation
	if err := json.Unmarshal(data, &observed); err != nil {
		t.Fatal(err)
	}
	expectedArguments := append([]string{"--user-data-dir=" + filepath.Join(stateRoot, profileDirectoryName)}, arguments...)
	if !reflect.DeepEqual(observed.Arguments, expectedArguments) {
		t.Fatalf("arguments were not preserved:\n got: %#v\nwant: %#v", observed.Arguments, expectedArguments)
	}
	expectedEnvironment := map[string]string{
		"CODEX_ROUTER_DATA_DIR":         stateRoot,
		"CODEX_MUX_HOME":                stateRoot,
		"CODEX_MUX_STATE_ROOT":          stateRoot,
		"CODEX_ELECTRON_USER_DATA_PATH": filepath.Join(stateRoot, profileDirectoryName),
		"CODEX_CLI_PATH":                filepath.Join(layout, muxRelativePath),
		"CODEX_MUX_REAL_CODEX":          filepath.Join(layout, realCodexRelative),
		"CODEX_SPARKLE_ENABLED":         "false",
		"CODEX_ROUTER_ENABLE_APPSHOTS":  "0",
		"CODEX_MUX_CONTROL_PORT":        "61234",
		"CODEX_MUX_CONTROL_TOKEN":       "",
		"CODEX_MUX_UI_TESTS":            "",
		"CODEX_HOME":                    primaryHome,
		"CODEX_SQLITE_HOME":             primaryHome,
	}
	if !reflect.DeepEqual(observed.Environment, expectedEnvironment) {
		t.Fatalf("child environment differs:\n got: %#v\nwant: %#v", observed.Environment, expectedEnvironment)
	}
	if information, err := os.Stat(filepath.Join(stateRoot, profileDirectoryName)); err != nil || !information.IsDir() {
		t.Fatalf("isolated profile directory was not created: %v", err)
	}

	developmentStateRoot := filepath.Join(layout, "development state")
	writeTestLauncherConfigurationForChannel(t, launcherConfigPath, developmentStateRoot, 61235, developmentChannel)
	developmentSelfTest := exec.Command(launcher, selfTestArgument)
	developmentSelfTest.Env = selfTest.Env
	developmentSelfTestOutput, err := developmentSelfTest.CombinedOutput()
	if err != nil {
		t.Fatalf("development self-test failed: %v\n%s", err, developmentSelfTestOutput)
	}
	for _, expected := range []string{
		"install_channel=development",
		"app_user_model_id=" + developmentAppUserModelID,
		"display_name=" + developmentProductName,
		"primary_codex_home=" + filepath.Join(developmentStateRoot, developmentPrimaryHome),
		"primary_sqlite_home=" + filepath.Join(developmentStateRoot, developmentPrimaryHome),
	} {
		if !strings.Contains(string(developmentSelfTestOutput), expected) {
			t.Fatalf("development self-test omitted %q:\n%s", expected, developmentSelfTestOutput)
		}
	}

	developmentOutputPath := filepath.Join(layout, "development-child-observation.json")
	developmentCommand := exec.Command(launcher)
	developmentCommand.Env = testEnvironment(os.Environ(), map[string]string{
		"CODEX_ROUTER_TEST_OUTPUT": developmentOutputPath,
		"CODEX_HOME":               primaryHome,
		"CODEX_SQLITE_HOME":        primaryHome,
		"CODEX_MUX_CONTROL_PORT":   "50000",
	})
	developmentChildOutput, err := developmentCommand.CombinedOutput()
	if !errors.As(err, &exitError) || exitError.ExitCode() != 37 {
		t.Fatalf("development launcher did not propagate child exit 37: %v\n%s", err, developmentChildOutput)
	}
	data, err = os.ReadFile(developmentOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	var developmentObserved childObservation
	if err := json.Unmarshal(data, &developmentObserved); err != nil {
		t.Fatal(err)
	}
	if developmentObserved.Environment["CODEX_HOME"] != filepath.Join(developmentStateRoot, developmentPrimaryHome) ||
		developmentObserved.Environment["CODEX_SQLITE_HOME"] != filepath.Join(developmentStateRoot, developmentPrimaryHome) ||
		developmentObserved.Environment["CODEX_HOME"] != developmentObserved.Environment["CODEX_SQLITE_HOME"] {
		t.Fatalf("development launch inherited production primary homes: %#v", developmentObserved.Environment)
	}
	for _, relative := range []string{profileDirectoryName, developmentPrimaryHome} {
		if info, err := os.Stat(filepath.Join(developmentStateRoot, relative)); err != nil || !info.IsDir() {
			t.Fatalf("development home %q was not isolated and created: %v", relative, err)
		}
	}
	if _, err := os.Stat(primaryHome); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("development launch created or touched the inherited production home %q: %v", primaryHome, err)
	}
}

func buildGoBinary(t *testing.T, goExecutable, output, packagePath, linkerFlags string) {
	t.Helper()
	command := exec.Command(
		goExecutable,
		"build",
		"-trimpath",
		"-ldflags="+linkerFlags,
		"-o",
		output,
		packagePath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", packagePath, err, output)
	}
}

func writeTestLauncherConfiguration(t *testing.T, path, stateRoot string, controlPort int) {
	t.Helper()
	contents, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"stateRoot":     stateRoot,
		"controlPort":   controlPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	appDirectory := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	writeTestBuildIdentity(t, filepath.Join(appDirectory, "codex-mux-build.json"), stateRoot, controlPort, productionChannel, "", "")
}

func writeTestLauncherConfigurationForChannel(t *testing.T, path, stateRoot string, controlPort int, channel string) {
	t.Helper()
	configuration := map[string]any{
		"schemaVersion": 2,
		"stateRoot":     stateRoot,
		"controlPort":   controlPort,
	}
	primaryCodexHome, primarySQLiteHome := "", ""
	if channel == developmentChannel {
		primaryCodexHome = filepath.Join(stateRoot, developmentPrimaryHome)
		primarySQLiteHome = filepath.Join(stateRoot, developmentPrimaryHome)
		configuration["installChannel"] = channel
		configuration["primaryCodexHome"] = primaryCodexHome
		configuration["primarySqliteHome"] = primarySQLiteHome
	}
	contents, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	appDirectory := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	writeTestBuildIdentity(t, filepath.Join(appDirectory, "codex-mux-build.json"), stateRoot, controlPort, channel, primaryCodexHome, primarySQLiteHome)
}

func writeTestBuildIdentity(t *testing.T, path, stateRoot string, controlPort int, channel, primaryCodexHome, primarySQLiteHome string) {
	t.Helper()
	appUserModelID, displayName := appIdentity(channel)
	manifest := map[string]any{
		"schemaVersion":     2,
		"profilePath":       filepath.Join(stateRoot, profileDirectoryName),
		"controlPort":       controlPort,
		"installChannel":    channel,
		"primaryCodexHome":  primaryCodexHome,
		"primarySqliteHome": primarySQLiteHome,
		"windowsIntegrationIsolation": map[string]string{
			"appUserModelId": appUserModelID,
			"displayName":    displayName,
		},
	}
	contents, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func testEnvironment(base []string, replacements map[string]string) []string {
	filtered := make([]string, 0, len(base)+len(replacements))
	for _, entry := range base {
		separator := strings.IndexByte(entry, '=')
		if separator > 0 {
			if _, replace := replacements[strings.ToUpper(entry[:separator])]; replace {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	for key, value := range replacements {
		if value != "" {
			filtered = append(filtered, key+"="+value)
		}
	}
	return filtered
}
