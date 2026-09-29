//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sharedTestBinding(root string) sharedBinding {
	return sharedBinding{
		SharedStateRoot:   filepath.Join(root, "production-state"),
		SharedPrimaryHome: filepath.Join(root, "native-home"),
		UsageDataRoot:     filepath.Join(root, "dev-data"),
		SharedProtocol:    1, ActivationPairID: "shared-test-pair",
	}
}

func TestSharedLaunchPlanBindsHomesAndPrivateRuntime(t *testing.T) {
	for _, channel := range []string{productionChannel, developmentChannel} {
		t.Run(channel, func(t *testing.T) {
			root := t.TempDir()
			stateRoot := filepath.Join(root, "runtime")
			binding := sharedTestBinding(root)
			port := 61234
			configuration := sidecarConfiguration{sharedBinding: binding, SchemaVersion: 3,
				StateRoot: stateRoot, ControlPort: &port, InstallChannel: channel,
				PrimaryCodexHome: binding.SharedPrimaryHome, PrimarySQLiteHome: binding.SharedPrimaryHome}
			data, _ := json.Marshal(configuration)
			appID, displayName := appIdentity(channel)
			manifest := buildIdentity{sharedBinding: binding, SchemaVersion: 3, ProfilePath: filepath.Join(stateRoot, "Profile"),
				ControlPort: port, InstallChannel: channel, PrimaryCodexHome: binding.SharedPrimaryHome, PrimarySQLiteHome: binding.SharedPrimaryHome}
			manifest.WindowsIntegrationIsolation.AppUserModelID = appID
			manifest.WindowsIntegrationIsolation.DisplayName = displayName
			encoded, _ := json.Marshal(manifest)
			if err := os.WriteFile(filepath.Join(root, "codex-mux-build.json"), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			plan, err := buildLaunchPlan(filepath.Join(root, "ChatGPT.exe"), nil,
				testLookup(map[string]string{"CODEX_MUX_HOME": `D:\inherited`}),
				func(string) ([]byte, bool, error) { return data, true, nil }, func(string) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if plan.StateRoot != stateRoot || plan.Profile != filepath.Join(stateRoot, "Profile") || plan.PrimaryCodexHome != binding.SharedPrimaryHome || plan.PrimarySQLiteHome != binding.SharedPrimaryHome {
				t.Fatalf("incorrect shared plan: %+v", plan)
			}
			if err := validateReleaseLaunchPlan(plan); err != nil {
				t.Fatal(err)
			}
			environment := strings.Join(childEnvironment([]string{"CODEX_HOME=bad", "CODEX_SQLITE_HOME=bad", "CODEX_MUX_SHARED_ROOT=bad", "codex_mux_usage_root=bad", "CODEX_MUX_SHARED_PROTOCOL=99", "CODEX_MUX_ACTIVATION_PAIR_ID=bad", "CODEX_MUX_INSTALL_CHANNEL=bad"}, plan), "\n")
			for _, expected := range []string{"CODEX_HOME=" + binding.SharedPrimaryHome, "CODEX_SQLITE_HOME=" + binding.SharedPrimaryHome,
				"CODEX_MUX_HOME=" + stateRoot, "CODEX_MUX_SHARED_ROOT=" + binding.SharedStateRoot,
				"CODEX_MUX_USAGE_ROOT=" + binding.UsageDataRoot, "CODEX_MUX_SHARED_PROTOCOL=1", "CODEX_MUX_INSTALL_CHANNEL=" + channel} {
				if !strings.Contains(environment, expected+"\n") && !strings.HasSuffix(environment, expected) {
					t.Fatalf("missing %s", expected)
				}
			}
			if strings.Contains(environment, "=bad") || strings.Contains(environment, "=99") {
				t.Fatal(environment)
			}
			manifest.ActivationPairID = "different-pair"
			encoded, _ = json.Marshal(manifest)
			os.WriteFile(filepath.Join(root, "codex-mux-build.json"), encoded, 0600)
			if _, err := buildLaunchPlan(filepath.Join(root, "ChatGPT.exe"), nil, testLookup(nil),
				func(string) ([]byte, bool, error) { return data, true, nil }, func(string) error { return nil }); err == nil {
				t.Fatal("mismatched activation pair accepted")
			}
		})
	}
}

func TestSharedSidecarRejectsPartialBindingAndLegacyOptIn(t *testing.T) {
	port := 61234
	good := sidecarConfiguration{sharedBinding: sharedTestBinding(t.TempDir()), SchemaVersion: 3, StateRoot: `D:\runtime`, ControlPort: &port}
	for _, mutate := range []func(*sidecarConfiguration){
		func(c *sidecarConfiguration) { c.SchemaVersion = 2 },
		func(c *sidecarConfiguration) { c.SharedProtocol = 2 },
		func(c *sidecarConfiguration) { c.SharedStateRoot = "" },
		func(c *sidecarConfiguration) { c.UsageDataRoot = "relative" },
		func(c *sidecarConfiguration) { c.ActivationPairID = "bad id" },
	} {
		bad := good
		mutate(&bad)
		data, _ := json.Marshal(bad)
		if _, err := decodeSidecar(data); err == nil {
			t.Fatalf("accepted invalid config: %s", data)
		}
	}
}

func TestSharedActivationMustBeCommittedBeforeLaunch(t *testing.T) {
	plan := validProductionLaunchPlan(61234)
	plan.ConfigSchemaVersion = 3
	plan.sharedBinding = sharedTestBinding(t.TempDir())
	plan.PrimaryCodexHome, plan.PrimarySQLiteHome = plan.SharedPrimaryHome, plan.SharedPrimaryHome
	called := false
	if err := runReleaseLaunch(plan, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("missing activation allowed launch")
	}
	for _, data := range []string{`{}`, `{"pairId":"shared-test-pair","state":"publishing"}`, `{"pairId":"other-pair","state":"committed"}`, `{"pairId":"shared-test-pair","state":"failed"}`, "invalid"} {
		if err := validateSharedActivation(plan, func(string) ([]byte, bool, error) { return []byte(data), true, nil }); err == nil {
			t.Fatal(data)
		}
	}
	if err := validateSharedActivation(plan, func(string) ([]byte, bool, error) {
		return []byte(`{"pairId":"shared-test-pair","state":"committed"}`), true, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyLaunchStripsInheritedSharedEnvironment(t *testing.T) {
	plan := validProductionLaunchPlan(61234)
	environment := strings.Join(childEnvironment([]string{"CODEX_MUX_SHARED_ROOT=bad", "CODEX_MUX_SHARED_PROTOCOL=1", "CODEX_MUX_USAGE_ROOT=bad", "CODEX_MUX_INSTALL_CHANNEL=development"}, plan), "\n")
	if strings.Contains(environment, "CODEX_MUX_SHARED") || strings.Contains(environment, "CODEX_MUX_USAGE") || strings.Contains(environment, "CODEX_MUX_INSTALL_CHANNEL") {
		t.Fatal(environment)
	}
}
