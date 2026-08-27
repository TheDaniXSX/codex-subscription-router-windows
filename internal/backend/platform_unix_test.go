//go:build !windows

package backend

import (
	"slices"
	"testing"
)

func TestWithEnvironmentKeepsDifferentlyCasedUnixKey(t *testing.T) {
	got := withEnvironment(
		[]string{"PATH=/usr/bin", "codex_home=/old", "OTHER=value"},
		"CODEX_HOME",
		"/new",
	)
	want := []string{"PATH=/usr/bin", "codex_home=/old", "OTHER=value", "CODEX_HOME=/new"}
	if !slices.Equal(got, want) {
		t.Fatalf("withEnvironment() = %q, want %q", got, want)
	}
}

func TestWithoutRouterEnvironmentUsesUnixKeyCase(t *testing.T) {
	got := withoutRouterEnvironment([]string{
		"PATH=/usr/bin",
		"CODEX_MUX_CONTROL_TOKEN=secret",
		"codex_mux_control_token=not-the-router-key-on-unix",
	})
	want := []string{"PATH=/usr/bin", "codex_mux_control_token=not-the-router-key-on-unix"}
	if !slices.Equal(got, want) {
		t.Fatalf("withoutRouterEnvironment() = %q, want %q", got, want)
	}
}
