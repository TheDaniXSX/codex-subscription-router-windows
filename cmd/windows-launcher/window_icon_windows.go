//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	routerAppUserModelID      = "com.openai.codex.subscription-router"
	wmSetIcon                 = 0x0080
	iconSmall                 = 0
	iconBig                   = 1
	sendMessageAbortIfHung    = 0x0002
	windowIconPollInterval    = 500 * time.Millisecond
	windowBrandingIdlePeriod  = 5 * time.Second
	windowBrandingIdlePasses  = 20
	windowIconMessageTimeout  = 250
	windowBrandingApplyPasses = 4
	coinitMultithreaded       = 0x0
)

var (
	windowIconUser32         = syscall.NewLazyDLL("user32.dll")
	windowIconShell32        = syscall.NewLazyDLL("shell32.dll")
	windowIconOle32          = syscall.NewLazyDLL("ole32.dll")
	enumWindows              = windowIconUser32.NewProc("EnumWindows")
	getWindowThreadProcessID = windowIconUser32.NewProc("GetWindowThreadProcessId")
	sendMessageTimeoutW      = windowIconUser32.NewProc("SendMessageTimeoutW")
	destroyIcon              = windowIconUser32.NewProc("DestroyIcon")
	extractIconExW           = windowIconShell32.NewProc("ExtractIconExW")
	shGetPropertyStoreWindow = windowIconShell32.NewProc("SHGetPropertyStoreForWindow")
	propVariantClear         = windowIconOle32.NewProc("PropVariantClear")
	coInitializeEx           = windowIconOle32.NewProc("CoInitializeEx")
	coUninitialize           = windowIconOle32.NewProc("CoUninitialize")
)

var (
	propertyStoreInterfaceID = syscall.GUID{
		Data1: 0x886d8eeb,
		Data2: 0x8cf2,
		Data3: 0x4446,
		Data4: [8]byte{0x8d, 0x02, 0xcd, 0xba, 0x1d, 0xbd, 0xcf, 0x99},
	}
	appUserModelFormatID = syscall.GUID{
		Data1: 0x9f4c2855,
		Data2: 0x9f79,
		Data3: 0x4b39,
		Data4: [8]byte{0xa8, 0xd0, 0xe1, 0xd4, 0x2d, 0xe1, 0xd5, 0xf3},
	}
	appUserModelRelaunchIconKey = propertyKey{formatID: appUserModelFormatID, propertyID: 3}
	appUserModelIDKey           = propertyKey{formatID: appUserModelFormatID, propertyID: 5}
)

type launcherIcons struct {
	large uintptr
	small uintptr
}

type propertyKey struct {
	formatID   syscall.GUID
	propertyID uint32
}

// PROPVARIANT has a 16-byte value union following its 8-byte header on x64.
// PropVariantFromString and PropVariantClear own the union representation.
type propVariant struct {
	variantType uint16
	reserved1   uint16
	reserved2   uint16
	reserved3   uint16
	value       unsafe.Pointer
	value2      uintptr
}

type propertyStore struct {
	vtable *propertyStoreVTable
}

type propertyStoreVTable struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
	getCount       uintptr
	getAt          uintptr
	getValue       uintptr
	setValue       uintptr
	commit         uintptr
}

type windowBrandingApplier struct {
	processID   uint32
	icons       launcherIcons
	launcher    string
	callback    uintptr
	applyPasses map[uintptr]int
	applied     int
	attempted   bool
	firstError  error
}

func loadLauncherIcons(executable string) (launcherIcons, error) {
	path, err := syscall.UTF16PtrFromString(executable)
	if err != nil {
		return launcherIcons{}, fmt.Errorf("encode launcher icon path: %w", err)
	}

	icons := launcherIcons{}
	count, _, callErr := extractIconExW.Call(
		uintptr(unsafe.Pointer(path)),
		0,
		uintptr(unsafe.Pointer(&icons.large)),
		uintptr(unsafe.Pointer(&icons.small)),
		1,
	)
	if count == 0 || icons.large == 0 || icons.small == 0 {
		icons.close()
		return launcherIcons{}, windowsCallError("ExtractIconExW", callErr)
	}
	return icons, nil
}

func (icons launcherIcons) close() {
	if icons.large != 0 {
		_, _, _ = destroyIcon.Call(icons.large)
	}
	if icons.small != 0 && icons.small != icons.large {
		_, _, _ = destroyIcon.Call(icons.small)
	}
}

func newWindowBrandingApplier(processID uint32, icons launcherIcons, launcher string) (*windowBrandingApplier, error) {
	if processID == 0 {
		return nil, fmt.Errorf("window branding target process ID is zero")
	}
	applier := &windowBrandingApplier{
		processID:   processID,
		icons:       icons,
		launcher:    launcher,
		applyPasses: make(map[uintptr]int),
	}
	applier.callback = syscall.NewCallback(applier.visitWindow)
	return applier, nil
}

func (applier *windowBrandingApplier) apply() (int, error) {
	applier.applied = 0
	applier.attempted = false
	applier.firstError = nil
	result, _, callErr := enumWindows.Call(applier.callback, 0)
	runtime.KeepAlive(applier)
	if result == 0 {
		return applier.applied, windowsCallError("EnumWindows", callErr)
	}
	return applier.applied, applier.firstError
}

func (applier *windowBrandingApplier) visitWindow(window uintptr, _ uintptr) uintptr {
	owner := uint32(0)
	_, _, _ = getWindowThreadProcessID.Call(
		window,
		uintptr(unsafe.Pointer(&owner)),
	)
	if owner != applier.processID || applier.applyPasses[window] >= windowBrandingApplyPasses {
		return 1
	}
	applier.applyPasses[window]++
	applier.attempted = true

	identityErr := setWindowIdentity(window, applier.launcher)
	if identityErr != nil {
		if applier.firstError == nil {
			applier.firstError = identityErr
		}
	}
	iconApplied := setWindowIcon(window, iconBig, applier.icons.large) &&
		setWindowIcon(window, iconSmall, applier.icons.small)
	if !iconApplied {
		if applier.firstError == nil {
			applier.firstError = fmt.Errorf("apply icon to window 0x%x", window)
		}
	}

	if identityErr == nil && iconApplied {
		applier.applied++
	}
	return 1
}

func setWindowIcon(window uintptr, kind uintptr, icon uintptr) bool {
	if window == 0 || icon == 0 {
		return false
	}
	messageResult := uintptr(0)
	result, _, _ := sendMessageTimeoutW.Call(
		window,
		wmSetIcon,
		kind,
		icon,
		sendMessageAbortIfHung,
		windowIconMessageTimeout,
		uintptr(unsafe.Pointer(&messageResult)),
	)
	return result != 0
}

func setWindowIdentity(window uintptr, launcherPath string) error {
	if window == 0 {
		return fmt.Errorf("window identity target is zero")
	}
	store, err := getWindowPropertyStore(window)
	if err != nil {
		return err
	}
	defer store.close()

	// The AppUserModelID is committed last. Windows uses it to refresh taskbar
	// grouping and to find the Start Menu shortcut carrying the same identity.
	if err := store.setString(appUserModelRelaunchIconKey, launcherPath+",0"); err != nil {
		return fmt.Errorf("set taskbar relaunch icon: %w", err)
	}
	if err := store.setString(appUserModelIDKey, routerAppUserModelID); err != nil {
		return fmt.Errorf("set taskbar AppUserModelID: %w", err)
	}
	if result, _, _ := syscall.SyscallN(store.vtable.commit, uintptr(unsafe.Pointer(store))); hresultFailed(result) {
		return hresultError("IPropertyStore.Commit", result)
	}
	return nil
}

func getWindowPropertyStore(window uintptr) (*propertyStore, error) {
	store := (*propertyStore)(nil)
	result, _, _ := shGetPropertyStoreWindow.Call(
		window,
		uintptr(unsafe.Pointer(&propertyStoreInterfaceID)),
		uintptr(unsafe.Pointer(&store)),
	)
	if hresultFailed(result) || store == nil {
		return nil, hresultError("SHGetPropertyStoreForWindow", result)
	}
	return store, nil
}

func (store *propertyStore) setString(key propertyKey, value string) error {
	text, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		return fmt.Errorf("encode taskbar property: %w", err)
	}
	// IPropertyStore.SetValue copies the VT_LPWSTR synchronously. The backing
	// UTF-16 data is Go-owned, so this input-only variant must not be cleared.
	variant := propVariant{variantType: 31, value: unsafe.Pointer(text)} // VT_LPWSTR

	result, _, _ := syscall.SyscallN(
		store.vtable.setValue,
		uintptr(unsafe.Pointer(store)),
		uintptr(unsafe.Pointer(&key)),
		uintptr(unsafe.Pointer(&variant)),
	)
	runtime.KeepAlive(text)
	if hresultFailed(result) {
		return hresultError("IPropertyStore.SetValue", result)
	}
	return nil
}

func (store *propertyStore) close() {
	if store != nil && store.vtable != nil && store.vtable.release != 0 {
		_, _, _ = syscall.SyscallN(store.vtable.release, uintptr(unsafe.Pointer(store)))
	}
}

func hresultFailed(result uintptr) bool {
	return int32(result) < 0
}

func hresultError(operation string, result uintptr) error {
	return fmt.Errorf("%s failed with HRESULT 0x%08x", operation, uint32(result))
}

func initializeWindowBrandingCOM() func() {
	result, _, _ := coInitializeEx.Call(0, coinitMultithreaded)
	if result == 0 || result == 1 {
		return func() { _, _, _ = coUninitialize.Call() }
	}
	// RPC_E_CHANGED_MODE means COM is already initialized in another apartment;
	// the property-store calls remain available and must not be uninitialized here.
	return func() {}
}

// startWindowBrandingSync keeps the visible Electron window aligned with the
// independent launcher. The top-level window belongs to ChatGPT.real.exe, so
// the launcher supplies both its icon and the explicit AppUserModelID used to
// merge the running window with the Start Menu/taskbar shortcut. Handles stay
// owned by the launcher until the child exits. A bounded number of passes per
// window covers Electron applying its own late startup branding without
// continuous window-message traffic once the window is stable. Enumeration
// also backs off from 500 ms to 5 seconds after startup settles.
func startWindowBrandingSync(processID int, launcherPath string) (func(), error) {
	if processID <= 0 {
		return func() {}, fmt.Errorf("window branding target process ID is invalid: %d", processID)
	}
	icons, err := loadLauncherIcons(launcherPath)
	if err != nil {
		return func() {}, err
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		defer close(done)
		defer icons.close()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		uninitializeCOM := initializeWindowBrandingCOM()
		defer uninitializeCOM()

		applier, applierErr := newWindowBrandingApplier(uint32(processID), icons, launcherPath)
		if applierErr != nil {
			return
		}
		ticker := time.NewTicker(windowIconPollInterval)
		defer ticker.Stop()
		idlePasses := 0
		applyBranding := func() {
			_, _ = applier.apply()
			if applier.attempted {
				idlePasses = 0
				ticker.Reset(windowIconPollInterval)
				return
			}
			idlePasses++
			if idlePasses == windowBrandingIdlePasses {
				ticker.Reset(windowBrandingIdlePeriod)
			}
		}
		applyBranding()
		for {
			select {
			case <-ticker.C:
				applyBranding()
			case <-stop:
				return
			}
		}
	}()

	return func() {
		stopOnce.Do(func() { close(stop) })
		<-done
	}, nil
}
