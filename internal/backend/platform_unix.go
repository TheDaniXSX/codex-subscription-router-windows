//go:build !windows

package backend

import (
	"os"
	"os/exec"
	"strings"
)

func prepareCommand(*exec.Cmd) {}

type unixProcessSupervisor struct {
	process *os.Process
}

func superviseProcess(process *os.Process) (processSupervisor, error) {
	return &unixProcessSupervisor{process: process}, nil
}

func (s *unixProcessSupervisor) Terminate() error {
	return s.process.Signal(os.Interrupt)
}

func (*unixProcessSupervisor) Close() error {
	return nil
}

func environmentKeyEqual(left, right string) bool {
	return left == right
}

func environmentKeyHasPrefix(key, prefix string) bool {
	return strings.HasPrefix(key, prefix)
}
