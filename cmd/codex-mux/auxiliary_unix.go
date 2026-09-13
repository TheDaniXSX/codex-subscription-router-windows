//go:build !windows

package main

func isComputerUseAuxiliary(args []string) bool { return false }
