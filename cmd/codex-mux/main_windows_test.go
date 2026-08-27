//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestShutdownSignalsWindowsUsesConsoleInterrupt(t *testing.T) {
	signals := shutdownSignals()
	if len(signals) != 1 || signals[0] != os.Interrupt {
		t.Fatalf("shutdownSignals() = %v, want [%v]", signals, os.Interrupt)
	}
}

func TestLoadOrCreateTokenWindowsIsConsistentAcrossConcurrentCalls(t *testing.T) {
	root := t.TempDir()
	const calls = 16
	results := make(chan string, calls)
	errors := make(chan error, calls)
	var wait sync.WaitGroup
	for range calls {
		wait.Add(1)
		go func() {
			defer wait.Done()
			token, err := loadOrCreateToken(root)
			results <- token
			errors <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var expected string
	for token := range results {
		if expected == "" {
			expected = token
		}
		if token != expected {
			t.Fatalf("concurrent callers received %q and %q", expected, token)
		}
	}
}

func TestLoadOrCreateTokenWindowsRejectsReparsePoint(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "control-token")
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("junction creation is unavailable: %v: %s", err, output)
	}
	if _, err := loadOrCreateToken(root); err == nil {
		t.Fatal("loadOrCreateToken() accepted a reparse point")
	}
}

func TestLoadOrCreateTokenWindowsPersistsToken(t *testing.T) {
	root := t.TempDir()
	first, err := loadOrCreateToken(root)
	if err != nil {
		t.Fatalf("first loadOrCreateToken() error: %v", err)
	}
	second, err := loadOrCreateToken(root)
	if err != nil {
		t.Fatalf("second loadOrCreateToken() error: %v", err)
	}
	if first != second {
		t.Fatalf("control token changed across reads: %q != %q", first, second)
	}
	if _, err := validateControlToken(first); err != nil {
		t.Fatalf("generated token is invalid: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "control-token"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != first {
		t.Fatalf("persisted token = %q, want %q", contents, first)
	}
}
