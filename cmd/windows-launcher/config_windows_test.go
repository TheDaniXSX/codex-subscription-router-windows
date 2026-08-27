//go:build windows

package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testLookup(values map[string]string) environmentLookup {
	return func(key string) (string, bool) {
		value, exists := values[key]
		return value, exists
	}
}

func missingSidecar(string) ([]byte, bool, error) {
	return nil, false, nil
}

func schema2Sidecar(stateRoot string, controlPort int) optionalFileReader {
	return func(string) ([]byte, bool, error) {
		return []byte(fmt.Sprintf(
			`{"schemaVersion":2,"stateRoot":%q,"controlPort":%d}`,
			stateRoot, controlPort,
		)), true, nil
	}
}

func TestResolveStateRootPrecedence(t *testing.T) {
	appDirectory := `C:\Router`
	root, source, err := resolveStateRoot(appDirectory, testLookup(map[string]string{
		"CODEX_ROUTER_DATA_DIR": `D:\explicit-router`,
		"CODEX_MUX_HOME":        `D:\explicit-mux`,
		"CODEX_MUX_STATE_ROOT":  `D:\legacy`,
		"LOCALAPPDATA":          `C:\Users\Test\AppData\Local`,
	}), func(string) ([]byte, bool, error) {
		return []byte(`{"schemaVersion":1,"stateRoot":"D:\\sidecar"}`), true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if root != `D:\explicit-router` || source != "environment:CODEX_ROUTER_DATA_DIR" {
		t.Fatalf("unexpected root/source: %q %q", root, source)
	}
}

func TestResolveStateRootUsesVersionedSidecar(t *testing.T) {
	root, source, err := resolveStateRoot(`C:\Router`, testLookup(map[string]string{
		"LOCALAPPDATA": `C:\Users\Test\AppData\Local`,
	}), func(path string) ([]byte, bool, error) {
		if !strings.HasSuffix(path, sidecarRelativePath) {
			t.Fatalf("unexpected sidecar path %q", path)
		}
		return []byte(`{"schemaVersion":1,"stateRoot":"D:\\Persistent Router"}`), true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if root != `D:\Persistent Router` || source != "sidecar:"+sidecarRelativePath {
		t.Fatalf("unexpected root/source: %q %q", root, source)
	}
}

func TestResolveLauncherConfigurationUsesCanonicalSidecarRootAndPort(t *testing.T) {
	root, source, port, schema, err := resolveLauncherConfiguration(
		`C:\Router`,
		testLookup(map[string]string{"CODEX_ROUTER_DATA_DIR": `E:\Override State`}),
		schema2Sidecar(`D:\Persisted State`, 61234),
	)
	if err != nil {
		t.Fatal(err)
	}
	if root != `D:\Persisted State` || source != "sidecar:"+sidecarRelativePath || port != 61234 || schema != 2 {
		t.Fatalf("unexpected resolved configuration: root=%q source=%q port=%d schema=%d", root, source, port, schema)
	}
}

func TestValidateReleaseLaunchPlanRejectsLegacyAndInvalidPorts(t *testing.T) {
	for name, plan := range map[string]launchPlan{
		"legacy":    {ConfigSchemaVersion: 1, ControlPort: legacyControlPort},
		"low port":  {ConfigSchemaVersion: 2, ControlPort: minimumControlPort - 1},
		"high port": {ConfigSchemaVersion: 2, ControlPort: maximumControlPort + 1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateReleaseLaunchPlan(plan); err == nil {
				t.Fatal("expected release launch plan to fail closed")
			}
		})
	}
	if err := validateReleaseLaunchPlan(launchPlan{
		ConfigSchemaVersion: 2,
		ControlPort:         minimumControlPort,
	}); err != nil {
		t.Fatalf("valid release plan was rejected: %v", err)
	}
}

func TestRunReleaseLaunchRejectsLegacyBeforeSpawnCallback(t *testing.T) {
	spawnAttempts := 0
	err := runReleaseLaunch(launchPlan{
		ConfigSchemaVersion: 1,
		ControlPort:         legacyControlPort,
	}, func() error {
		spawnAttempts++
		return errors.New("spawn callback must not run")
	})
	if err == nil {
		t.Fatal("legacy sidecar unexpectedly passed the release launch barrier")
	}
	if spawnAttempts != 0 {
		t.Fatalf("legacy sidecar invoked the child spawn callback %d times", spawnAttempts)
	}
}

func TestRunReleaseLaunchInvokesCallbackOnlyAfterValidPlan(t *testing.T) {
	spawnAttempts := 0
	err := runReleaseLaunch(launchPlan{
		ConfigSchemaVersion: 2,
		ControlPort:         minimumControlPort,
	}, func() error {
		spawnAttempts++
		return nil
	})
	if err != nil {
		t.Fatalf("valid release launch was rejected: %v", err)
	}
	if spawnAttempts != 1 {
		t.Fatalf("valid release launch invoked callback %d times", spawnAttempts)
	}
}

func TestResolveLauncherConfigurationAcceptsLegacySidecarForDiagnostics(t *testing.T) {
	root, source, port, schema, err := resolveLauncherConfiguration(
		`C:\Router`,
		testLookup(nil),
		func(string) ([]byte, bool, error) {
			return []byte(`{"schemaVersion":1,"stateRoot":"D:\\Legacy"}`), true, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if root != `D:\Legacy` || source != "sidecar:"+sidecarRelativePath || port != legacyControlPort || schema != 1 {
		t.Fatalf("unexpected legacy configuration: root=%q source=%q port=%d schema=%d", root, source, port, schema)
	}
}

func TestResolveLauncherConfigurationRequiresSidecar(t *testing.T) {
	_, _, _, _, err := resolveLauncherConfiguration(
		`C:\Router`,
		testLookup(map[string]string{"LOCALAPPDATA": `C:\Users\Test\AppData\Local`}),
		missingSidecar,
	)
	if err == nil {
		t.Fatal("expected a missing control-port sidecar to fail closed")
	}
}

func TestResolveStateRootUsesNonVirtualizedDefault(t *testing.T) {
	root, source, err := resolveStateRoot(`C:\Router`, testLookup(map[string]string{
		"LOCALAPPDATA": `C:\Users\Test\AppData\Local`,
	}), missingSidecar)
	if err != nil {
		t.Fatal(err)
	}
	want := `C:\Users\Test\AppData\Local\Programs\Codex Subscription Router Data`
	if root != want || source != "default:LOCALAPPDATA" {
		t.Fatalf("unexpected root/source: %q %q; want %q", root, source, want)
	}
}

func TestResolveStateRootFailsClosedForInvalidSidecar(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"schemaVersion":1,"stateRoot":"relative"}`),
		[]byte(`{"schemaVersion":2,"stateRoot":"D:\\root"}`),
		[]byte(`{"schemaVersion":2,"stateRoot":"D:\\root","controlPort":48123}`),
		[]byte(`{"schemaVersion":2,"stateRoot":"D:\\root","controlPort":65536}`),
		[]byte(`{"schemaVersion":1,"stateRoot":"D:\\root","controlPort":61234}`),
		[]byte(`{"schemaVersion":1,"stateRoot":"D:\\root","token":"secret"}`),
		[]byte(`not-json`),
	}
	for _, contents := range tests {
		_, _, err := resolveStateRoot(`C:\Router`, testLookup(map[string]string{
			"LOCALAPPDATA": `C:\Users\Test\AppData\Local`,
		}), func(string) ([]byte, bool, error) {
			return contents, true, nil
		})
		if err == nil {
			t.Fatalf("expected sidecar %q to fail closed", contents)
		}
	}
}

func TestResolveStateRootEnvironmentSkipsInvalidSidecar(t *testing.T) {
	root, source, err := resolveStateRoot(`C:\Router`, testLookup(map[string]string{
		"CODEX_MUX_HOME": `D:\explicit`,
	}), func(string) ([]byte, bool, error) {
		return nil, true, errors.New("must not be called")
	})
	if err != nil {
		t.Fatal(err)
	}
	if root != `D:\explicit` || source != "environment:CODEX_MUX_HOME" {
		t.Fatalf("unexpected root/source: %q %q", root, source)
	}
}

func TestBuildLaunchPlanPreservesDeepLinksAndAddsProfile(t *testing.T) {
	seen := map[string]bool{}
	plan, err := buildLaunchPlan(
		`C:\Router\ChatGPT.exe`,
		[]string{`codex://thread/abc?value=two%20words`, `two words`, `quote"inside`},
		testLookup(map[string]string{"CODEX_MUX_HOME": `D:\Router State`}),
		schema2Sidecar(`D:\Router State`, 61234),
		func(path string) error {
			seen[path] = true
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	expectedArguments := []string{
		`--user-data-dir=D:\Router State\Profile`,
		`codex://thread/abc?value=two%20words`,
		`two words`,
		`quote"inside`,
	}
	if !reflect.DeepEqual(plan.Arguments, expectedArguments) {
		t.Fatalf("arguments differ:\n got: %#v\nwant: %#v", plan.Arguments, expectedArguments)
	}
	if plan.ControlPort != 61234 || plan.ConfigSchemaVersion != 2 {
		t.Fatalf("unexpected port contract: port=%d schema=%d", plan.ControlPort, plan.ConfigSchemaVersion)
	}
	if !seen[`C:\Router\ChatGPT.real.exe`] ||
		!seen[`C:\Router\resources\codex.exe`] ||
		!seen[`C:\Router\resources\codex.real.exe`] {
		t.Fatalf("did not validate every sibling executable: %#v", seen)
	}
}

func TestBuildLaunchPlanRejectsUserDataOverride(t *testing.T) {
	for _, argument := range []string{`--user-data-dir`, `--USER-DATA-DIR=D:\escape`} {
		_, err := buildLaunchPlan(
			`C:\Router\ChatGPT.exe`,
			[]string{argument},
			testLookup(map[string]string{"CODEX_MUX_HOME": `D:\root`}),
			missingSidecar,
			func(string) error { return nil },
		)
		if err == nil {
			t.Fatalf("expected %q to be rejected", argument)
		}
	}
	if hasUserDataOverride([]string{`codex://thread/x?value=--user-data-dir=D:\safe`}) {
		t.Fatal("deep link was mistaken for an override")
	}
}

func TestNormalizeRouterOpenURIToOneExistingLocalPath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "project % & Unicode-ñ")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := "codex-router://open?" + url.Values{"path": []string{target}}.Encode()
	got, err := normalizeRouterArguments([]string{link}, checkOpenTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{target}) {
		t.Fatalf("normalized arguments differ: got %#v, want %#v", got, []string{target})
	}
}

func TestNormalizeRouterArgumentsPreservesOfficialProtocol(t *testing.T) {
	arguments := []string{`codex://thread/abc?value=two%20words`, `argument & not shell text`}
	got, err := normalizeRouterArguments(arguments, func(string) error {
		return errors.New("must not validate an official link")
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, arguments) {
		t.Fatalf("official arguments changed: got %#v, want %#v", got, arguments)
	}
}

func TestParseRouterOpenURIRejectsUntrustedShapes(t *testing.T) {
	longURI := "codex-router://open?path=C%3A%5C" + strings.Repeat("a", maximumDeepLinkBytes)
	tests := map[string]string{
		"wrong scheme":      `https://open?path=C%3A%5Cproject`,
		"opaque":            `codex-router:open?path=C%3A%5Cproject`,
		"wrong action":      `codex-router://run?path=C%3A%5Cproject`,
		"credentials":       `codex-router://user@open?path=C%3A%5Cproject`,
		"port":              `codex-router://open:80?path=C%3A%5Cproject`,
		"path action":       `codex-router://open/other?path=C%3A%5Cproject`,
		"fragment":          `codex-router://open?path=C%3A%5Cproject#fragment`,
		"missing path":      `codex-router://open`,
		"duplicate path":    `codex-router://open?path=C%3A%5Cone&path=C%3A%5Ctwo`,
		"unexpected query":  `codex-router://open?path=C%3A%5Cone&command=calc.exe`,
		"relative target":   `codex-router://open?path=relative%5Cproject`,
		"UNC target":        `codex-router://open?path=%5C%5Cserver%5Cshare`,
		"device target":     `codex-router://open?path=%5C%5C%3F%5CC%3A%5Cproject`,
		"traversal target":  `codex-router://open?path=C%3A%5Cproject%5C..%5Csecret`,
		"alternate stream":  `codex-router://open?path=C%3A%5Cproject%5Cfile%3Astream`,
		"quote injection":   `codex-router://open?path=C%3A%5Cproject%22%20%26%20calc.exe`,
		"newline injection": "codex-router://open?path=C%3A%5Cproject%0A--flag",
		"overlength":        longURI,
	}
	for name, link := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRouterOpenURI(link); err == nil {
				t.Fatalf("expected URI to be rejected: %s", link)
			}
		})
	}
}

func TestNormalizeRouterArgumentsRejectsMissingTargetAndArgumentInjection(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	link := "codex-router://open?" + url.Values{"path": []string{missing}}.Encode()
	if _, err := normalizeRouterArguments([]string{link}, checkOpenTarget); err == nil {
		t.Fatal("expected missing target to be rejected")
	}
	if _, err := normalizeRouterArguments([]string{link, `--user-data-dir=C:\escape`}, func(string) error { return nil }); err == nil {
		t.Fatal("expected extra argument next to protocol URI to be rejected")
	}
}

func TestEnvironmentWithReplacesKeysCaseInsensitively(t *testing.T) {
	got := environmentWith([]string{
		`Path=C:\Windows`,
		`codex_mux_home=D:\old`,
		`CODEX_MUX_HOME=D:\ambiguous-duplicate`,
		`UNCHANGED=yes`,
	}, map[string]string{
		"CODEX_MUX_HOME":                `D:\new`,
		"CODEX_ELECTRON_USER_DATA_PATH": `D:\new\Profile`,
	})
	want := []string{
		`Path=C:\Windows`,
		`codex_mux_home=D:\new`,
		`UNCHANGED=yes`,
		`CODEX_ELECTRON_USER_DATA_PATH=D:\new\Profile`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment differs:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestEnvironmentWithoutRemovesSensitiveOverridesAndDuplicates(t *testing.T) {
	got := environmentWithout([]string{
		`Path=C:\Windows`,
		`CODEX_MUX_CONTROL_TOKEN=first-secret`,
		`codex_mux_control_token=second-secret`,
		`CODEX_MUX_UI_TESTS=1`,
		`codex_mux_ui_tests=true`,
		`UNCHANGED=yes`,
	}, strippedChildEnvironment)
	want := []string{`Path=C:\Windows`, `UNCHANGED=yes`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitized environment differs:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestChildEnvironmentSetsInterceptionAndPreservesPrimaryAccount(t *testing.T) {
	plan := launchPlan{
		StateRoot:   `D:\Router State`,
		Profile:     `D:\Router State\Profile`,
		Mux:         `C:\Router\resources\codex.exe`,
		RealCodex:   `C:\Router\resources\codex.real.exe`,
		ControlPort: 61234,
	}
	got := childEnvironment([]string{
		`CODEX_HOME=C:\Users\Test\.codex`,
		`CODEX_SQLITE_HOME=C:\Users\Test\.codex`,
		`codex_sparkle_enabled=true`,
		`CODEX_MUX_CONTROL_PORT=50000`,
		`CODEX_MUX_CONTROL_TOKEN=must-not-reach-child`,
		`codex_mux_control_token=duplicate-secret`,
		`CODEX_MUX_UI_TESTS=1`,
		`codex_mux_ui_tests=true`,
	}, plan)
	values := map[string]string{}
	for _, entry := range got {
		separator := strings.IndexByte(entry, '=')
		if separator > 0 {
			values[strings.ToUpper(entry[:separator])] = entry[separator+1:]
		}
	}
	expected := map[string]string{
		"CODEX_ROUTER_DATA_DIR":         plan.StateRoot,
		"CODEX_MUX_HOME":                plan.StateRoot,
		"CODEX_MUX_STATE_ROOT":          plan.StateRoot,
		"CODEX_ELECTRON_USER_DATA_PATH": plan.Profile,
		"CODEX_CLI_PATH":                plan.Mux,
		"CODEX_MUX_REAL_CODEX":          plan.RealCodex,
		"CODEX_SPARKLE_ENABLED":         "false",
		controlPortEnvironment:          "61234",
		appshotsEnvironment:             "0",
		"CODEX_HOME":                    `C:\Users\Test\.codex`,
		"CODEX_SQLITE_HOME":             `C:\Users\Test\.codex`,
	}
	if !reflect.DeepEqual(values, expected) {
		t.Fatalf("child environment differs:\n got: %#v\nwant: %#v", values, expected)
	}
	for _, forbidden := range strippedChildEnvironment {
		if _, exists := values[forbidden]; exists {
			t.Fatalf("sensitive override %s reached child environment", forbidden)
		}
	}
}

func TestChildEnvironmentRequiresExplicitAppshotsOptIn(t *testing.T) {
	for name, environment := range map[string][]string{
		"missing":     nil,
		"zero":        {appshotsEnvironment + "=0"},
		"other truth": {appshotsEnvironment + "=true"},
		"conflicting": {appshotsEnvironment + "=1", strings.ToLower(appshotsEnvironment) + "=0"},
	} {
		t.Run(name, func(t *testing.T) {
			if explicitEnvironmentOptIn(environment, appshotsEnvironment) {
				t.Fatal("experimental Appshots was enabled without an unambiguous =1 opt-in")
			}
		})
	}
	if !explicitEnvironmentOptIn([]string{appshotsEnvironment + "=1"}, appshotsEnvironment) {
		t.Fatal("explicit =1 Appshots opt-in was not honored")
	}

	got := childEnvironment([]string{appshotsEnvironment + "=1"}, launchPlan{})
	if !containsEnvironmentEntry(got, appshotsEnvironment+"=1") {
		t.Fatalf("child environment did not preserve explicit opt-in: %#v", got)
	}
}

func containsEnvironmentEntry(environment []string, expected string) bool {
	for _, entry := range environment {
		if strings.EqualFold(entry, expected) {
			return true
		}
	}
	return false
}

func TestReadOptionalFileLimitsInput(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "launcher-config.json")
	if err := os.WriteFile(path, make([]byte, maximumSidecarBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, exists, err := readOptionalFile(path)
	if !exists || err == nil {
		t.Fatalf("expected oversized existing file to fail, exists=%v err=%v", exists, err)
	}
}
