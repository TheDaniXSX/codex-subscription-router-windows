//go:build !windows

package main

import "os/exec"

func prepareBrokerCommand(command *exec.Cmd) {}
