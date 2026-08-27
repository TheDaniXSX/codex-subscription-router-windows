//go:build !windows

package main

import "testing"

func TestUnixRealExecutableName(t *testing.T) {
	if got, want := realExecutableName(), "codex.real"; got != want {
		t.Fatalf("realExecutableName() = %q, want %q", got, want)
	}
}
