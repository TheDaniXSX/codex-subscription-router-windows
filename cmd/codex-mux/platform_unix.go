//go:build !windows

package main

import (
	"os"
	"syscall"
)

func realExecutableName() string {
	return "codex.real"
}

func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func acquireInstanceLock(string) (func(), error) {
	return func() {}, nil
}
