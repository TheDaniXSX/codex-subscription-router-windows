package state

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	fileAttributeReparsePoint = 0x00000400
	fileFlagBackupSemantics   = 0x02000000
	openExisting              = 3
	invalidHandleValue        = ^uintptr(0)
)

var (
	pathKernel32              = syscall.NewLazyDLL("kernel32.dll")
	createFileW               = pathKernel32.NewProc("CreateFileW")
	closeHandle               = pathKernel32.NewProc("CloseHandle")
	getFinalPathNameByHandleW = pathKernel32.NewProc("GetFinalPathNameByHandleW")
	getFileAttributesW        = pathKernel32.NewProc("GetFileAttributesW")
)

func pathsEqual(left, right string) bool {
	return strings.EqualFold(left, right)
}

// canonicalExistingPath resolves the path through an object handle rather
// than by string normalization. This catches junctions, symlinks, 8.3 aliases,
// subst drives, and case aliases before a persisted path is trusted.
func canonicalExistingPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	pointer, err := syscall.UTF16PtrFromString(absolute)
	if err != nil {
		return "", err
	}
	handle, _, callErr := createFileW.Call(
		uintptr(unsafe.Pointer(pointer)),
		0,
		uintptr(syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE),
		0,
		openExisting,
		fileFlagBackupSemantics,
		0,
	)
	if handle == invalidHandleValue {
		return "", fmt.Errorf("open canonical path %q: %w", absolute, callErr)
	}
	defer closeHandle.Call(handle)

	required, _, callErr := getFinalPathNameByHandleW.Call(handle, 0, 0, 0)
	if required == 0 {
		return "", fmt.Errorf("size canonical path %q: %w", absolute, callErr)
	}
	buffer := make([]uint16, required+1)
	written, _, callErr := getFinalPathNameByHandleW.Call(
		handle,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		0,
	)
	if written == 0 || written >= uintptr(len(buffer)) {
		return "", fmt.Errorf("read canonical path %q: %w", absolute, callErr)
	}
	canonical := syscall.UTF16ToString(buffer[:written])
	if strings.HasPrefix(canonical, `\\?\UNC\`) {
		canonical = `\\` + strings.TrimPrefix(canonical, `\\?\UNC\`)
	} else {
		canonical = strings.TrimPrefix(canonical, `\\?\`)
	}
	return filepath.Clean(canonical), nil
}

func ensureNoReparsePath(base, target string) error {
	baseAbsolute, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	basePointer, err := syscall.UTF16PtrFromString(baseAbsolute)
	if err != nil {
		return err
	}
	baseAttributes, _, callErr := getFileAttributesW.Call(uintptr(unsafe.Pointer(basePointer)))
	if baseAttributes == 0xffffffff {
		return fmt.Errorf("inspect trusted root %q: %w", baseAbsolute, callErr)
	}
	if baseAttributes&fileAttributeReparsePoint != 0 {
		return fmt.Errorf("trusted root %q is a reparse point", baseAbsolute)
	}
	baseCanonical, err := canonicalExistingPath(base)
	if err != nil {
		return err
	}
	targetAbsolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	// Do the lexical containment and component walk using the spelling supplied
	// by the caller. The canonical handle path may expand an 8.3 alias (as it
	// does for RUNNER~1 on GitHub-hosted Windows), which would make an identical
	// base and target appear unrelated before the final canonical check.
	relative, err := filepath.Rel(baseAbsolute, targetAbsolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("path %q is outside trusted root %q", target, base)
	}
	current := baseAbsolute
	components := []string{"."}
	if relative != "." {
		components = strings.Split(relative, string(filepath.Separator))
	}
	for _, component := range components {
		if component != "." {
			current = filepath.Join(current, component)
		}
		pointer, pointerErr := syscall.UTF16PtrFromString(current)
		if pointerErr != nil {
			return pointerErr
		}
		attributes, _, callErr := getFileAttributesW.Call(uintptr(unsafe.Pointer(pointer)))
		if attributes == 0xffffffff {
			return fmt.Errorf("inspect path component %q: %w", current, callErr)
		}
		if attributes&fileAttributeReparsePoint != 0 {
			return fmt.Errorf("path component %q is a reparse point", current)
		}
	}
	targetCanonical, err := canonicalExistingPath(targetAbsolute)
	if err != nil {
		return err
	}
	canonicalRelative, err := filepath.Rel(baseCanonical, targetCanonical)
	if err != nil || canonicalRelative == ".." || strings.HasPrefix(canonicalRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(canonicalRelative) {
		return fmt.Errorf("canonical path %q escapes trusted root %q", targetCanonical, baseCanonical)
	}
	return nil
}
