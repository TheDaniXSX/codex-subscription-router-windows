package backend

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestChildCloseInterruptsBlockedWriter(t *testing.T) {
	if os.Getenv("BACKEND_BLOCKING_CHILD") == "1" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}

	inbound := make(chan Inbound, 1)
	child, err := Start(
		"blocked",
		t.TempDir(),
		os.Args[0],
		[]string{"-test.run=TestChildCloseInterruptsBlockedWriter"},
		append(os.Environ(), "BACKEND_BLOCKING_CHILD=1"),
		inbound,
	)
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- child.SendRaw([]byte(strings.Repeat("x", 8*1024*1024)))
	}()
	time.Sleep(100 * time.Millisecond)

	started := time.Now()
	if err := child.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("Close() took %v with a blocked writer", elapsed)
	}
	select {
	case <-writeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked SendRaw did not return after Close")
	}
}
