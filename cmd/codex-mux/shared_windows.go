//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// The launcher explicitly allows this one long-lived service to leave its job.
// Native backends are still supervised by their own kill-on-close jobs.
func prepareBrokerCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000 | 0x01000000, // NO_WINDOW | BREAKAWAY_FROM_JOB
	}
}
