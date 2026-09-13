package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// The native CUA helper inherits the desktop's router context but starts its
// own bare app-server for local operations. Never start a second mux for it.
// Match the exact bundled parent, not an environment flag or just a basename.
func isComputerUseAuxiliary(args []string) bool {
	if len(args) != 1 || args[0] != "app-server" {
		return false
	}
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	handle, err := syscall.OpenProcess(0x1000, false, uint32(os.Getppid()))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	query := syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
	ok, _, _ := query.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return false
	}
	return matchesComputerUseParent(executable, syscall.UTF16ToString(buffer[:size]))
}

func matchesComputerUseParent(executable, parent string) bool {
	expected := filepath.Join(filepath.Dir(executable), "cua_node", "bin", "node_modules", "@oai", "sky", "bin", "windows", "codex-computer-use.exe")
	return strings.EqualFold(filepath.Clean(parent), expected)
}
