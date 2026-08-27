package backend

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWindowsJobKillsDescendants(t *testing.T) {
	switch os.Getenv("BACKEND_JOB_HELPER") {
	case "grandchild":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "parent":
		command := exec.Command(os.Args[0], "-test.run=TestWindowsJobKillsDescendants")
		command.Env = append(os.Environ(), "BACKEND_JOB_HELPER=grandchild")
		prepareCommand(command)
		if err := command.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("BACKEND_JOB_PID_FILE"), []byte(strconv.Itoa(command.Process.Pid)), 0o600); err != nil {
			os.Exit(3)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}

	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	child, err := Start(
		"tree",
		t.TempDir(),
		os.Args[0],
		[]string{"-test.run=TestWindowsJobKillsDescendants"},
		append(os.Environ(), "BACKEND_JOB_HELPER=parent", "BACKEND_JOB_PID_FILE="+pidFile),
		make(chan Inbound, 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	grandchildPID := waitForPIDFile(t, pidFile)
	if err := child.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		exited, err := windowsProcessExited(grandchildPID)
		if err != nil {
			t.Fatal(err)
		}
		if exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant process %d survived Job Object close", grandchildPID)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents)))
			if parseErr != nil {
				t.Fatalf("invalid helper PID: %v", parseErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for helper PID")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func windowsProcessExited(pid int) (bool, error) {
	handle, _, callErr := openProcess.Call(processSynchronize, 0, uintptr(uint32(pid)))
	if handle == 0 {
		if errors.Is(callErr, syscall.Errno(87)) {
			return true, nil
		}
		return false, callErr
	}
	defer syscall.CloseHandle(syscall.Handle(handle))
	result, _, waitErr := waitForSingleObject.Call(handle, 0)
	switch result {
	case waitObject0:
		return true, nil
	case waitTimeout:
		return false, nil
	default:
		return false, waitErr
	}
}
