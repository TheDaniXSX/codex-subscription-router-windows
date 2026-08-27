package backend

import (
	"os/exec"
	"slices"
	"testing"
)

func TestPrepareCommandHidesChildConsole(t *testing.T) {
	command := exec.Command("codex.real.exe", "app-server")
	prepareCommand(command)
	attributes := command.SysProcAttr
	if attributes == nil {
		t.Fatal("prepareCommand() did not configure Windows process attributes")
	}
	if !attributes.HideWindow {
		t.Fatal("prepareCommand() did not hide the child window")
	}
	if attributes.CreationFlags&createNoWindow == 0 {
		t.Fatal("prepareCommand() did not set CREATE_NO_WINDOW")
	}
}

func TestWithEnvironmentReplacesWindowsKeyCaseInsensitively(t *testing.T) {
	got := withEnvironment(
		[]string{"Path=C:\\Windows", "codex_home=C:\\old", "OTHER=value"},
		"CODEX_HOME",
		"C:\\new",
	)
	want := []string{"Path=C:\\Windows", "OTHER=value", "CODEX_HOME=C:\\new"}
	if !slices.Equal(got, want) {
		t.Fatalf("withEnvironment() = %q, want %q", got, want)
	}
}

func TestWithoutRouterEnvironmentIsCaseInsensitiveOnWindows(t *testing.T) {
	got := withoutRouterEnvironment([]string{
		"Path=C:\\Windows",
		"codex_mux_control_token=secret",
		"CODEX_MUX_UI_TESTS=1",
		"Codex_Router_Internal=secret",
		"CODEX_HOME=C:\\primary",
	})
	want := []string{"Path=C:\\Windows", "CODEX_HOME=C:\\primary"}
	if !slices.Equal(got, want) {
		t.Fatalf("withoutRouterEnvironment() = %q, want %q", got, want)
	}
}
