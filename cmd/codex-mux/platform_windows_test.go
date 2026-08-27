package main

import "testing"

func TestWindowsRealExecutableName(t *testing.T) {
	if got, want := realExecutableName(), "codex.real.exe"; got != want {
		t.Fatalf("realExecutableName() = %q, want %q", got, want)
	}
}

func TestWindowsInstanceLockIsExclusiveAndReleasable(t *testing.T) {
	root := t.TempDir()
	release, err := acquireInstanceLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireInstanceLock(root); err == nil {
		release()
		t.Fatal("second acquireInstanceLock() unexpectedly succeeded")
	}
	release()
	releaseAgain, err := acquireInstanceLock(root)
	if err != nil {
		t.Fatalf("acquireInstanceLock() after release error = %v", err)
	}
	releaseAgain()
}
