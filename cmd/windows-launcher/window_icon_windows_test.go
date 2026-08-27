//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

const (
	wmGetIcon = 0x007F
)

var (
	createWindowExW = windowIconUser32.NewProc("CreateWindowExW")
	destroyWindow   = windowIconUser32.NewProc("DestroyWindow")
	sendMessageW    = windowIconUser32.NewProc("SendMessageW")
)

func TestLauncherIconAppliesToOwnedWindow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	uninitializeCOM := initializeWindowBrandingCOM()
	defer uninitializeCOM()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	icons, err := loadLauncherIcons(executable)
	if err != nil {
		t.Fatalf("load launcher icons: %v", err)
	}
	defer icons.close()

	className, err := syscall.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}
	title, err := syscall.UTF16PtrFromString("Codex Router Icon Test")
	if err != nil {
		t.Fatal(err)
	}
	window, _, callErr := createWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		0,
		0,
		0,
		64,
		64,
		0,
		0,
		0,
		0,
	)
	if window == 0 {
		t.Fatalf("CreateWindowExW: %v", callErr)
	}
	defer func() { _, _, _ = destroyWindow.Call(window) }()

	applier, err := newWindowBrandingApplier(uint32(os.Getpid()), icons, executable)
	if err != nil {
		t.Fatalf("create window branding applier: %v", err)
	}
	applied, err := applier.apply()
	if err != nil {
		t.Fatalf("apply launcher branding: %v", err)
	}
	if applied < 1 {
		t.Fatal("launcher icon was not applied to the owned top-level window")
	}

	large, _, _ := sendMessageW.Call(window, wmGetIcon, iconBig, 0)
	if large == 0 {
		t.Fatal("large window icon is unset after launcher branding")
	}
	small, _, _ := sendMessageW.Call(window, wmGetIcon, iconSmall, 0)
	if small == 0 {
		t.Fatal("small window icon is unset after launcher branding")
	}
	appID, err := readWindowStringProperty(window, appUserModelIDKey)
	if err != nil {
		t.Fatalf("read window AppUserModelID: %v", err)
	}
	if appID != routerAppUserModelID {
		t.Fatalf("window AppUserModelID mismatch: got %q want %q", appID, routerAppUserModelID)
	}
}

func TestApplyLauncherIconRejectsZeroPID(t *testing.T) {
	if _, err := newWindowBrandingApplier(0, launcherIcons{large: 1, small: 1}, "launcher.exe"); err == nil {
		t.Fatal("expected zero process ID to be rejected")
	}
}

func readWindowStringProperty(window uintptr, key propertyKey) (string, error) {
	store, err := getWindowPropertyStore(window)
	if err != nil {
		return "", err
	}
	defer store.close()

	variant := propVariant{}
	result, _, _ := syscall.SyscallN(
		store.vtable.getValue,
		uintptr(unsafe.Pointer(store)),
		uintptr(unsafe.Pointer(&key)),
		uintptr(unsafe.Pointer(&variant)),
	)
	if hresultFailed(result) {
		return "", hresultError("IPropertyStore.GetValue", result)
	}
	defer func() { _, _, _ = propVariantClear.Call(uintptr(unsafe.Pointer(&variant))) }()

	if variant.variantType == 0 || variant.value == nil {
		return "", nil
	}
	text := unsafe.Slice((*uint16)(variant.value), 32768)
	length := 0
	for length < len(text) && text[length] != 0 {
		length++
	}
	if length == len(text) {
		return "", fmt.Errorf("window property string is not null terminated")
	}
	return syscall.UTF16ToString(text[:length]), nil
}
