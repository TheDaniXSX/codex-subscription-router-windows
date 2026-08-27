package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var createMutexW = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")

func realExecutableName() string {
	return "codex.real.exe"
}

// Windows does not support delivering SIGTERM to arbitrary processes. The
// console interrupt is the only portable notification exposed by os/signal.
func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func acquireInstanceLock(root string) (func(), error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve state root for instance lock: %w", err)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(absolute))))
	name := "Local\\CodexSubscriptionRouter-" + hex.EncodeToString(sum[:16])
	namePointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("encode instance lock name: %w", err)
	}
	handle, _, callErr := createMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePointer)))
	if handle == 0 {
		return nil, fmt.Errorf("create instance lock: %w", callErr)
	}
	if errors.Is(callErr, syscall.ERROR_ALREADY_EXISTS) {
		_ = syscall.CloseHandle(syscall.Handle(handle))
		return nil, fmt.Errorf("another Codex Subscription Router already uses %q", absolute)
	}
	var once sync.Once
	return func() {
		once.Do(func() { _ = syscall.CloseHandle(syscall.Handle(handle)) })
	}, nil
}
