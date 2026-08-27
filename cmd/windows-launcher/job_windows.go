//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x00002000
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	closeHandle              = kernel32.NewProc("CloseHandle")
)

// These layouts match JOBOBJECT_EXTENDED_LIMIT_INFORMATION on 64-bit Windows.
// The release launcher is x64-only; keeping them local avoids a runtime
// dependency solely for four stable kernel32 calls.
type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	_                       uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	_                       uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobObjectIOCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IOInfo                jobObjectIOCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// superviseDescendants creates an unnamed, handle-only Job Object and assigns
// this launcher before Electron starts. All descendants therefore join the job
// as they are created. The handle is intentionally retained until process
// teardown: closing its last handle terminates any surviving descendants.
func superviseDescendants() error {
	job, _, callErr := createJobObjectW.Call(0, 0)
	if job == 0 {
		return windowsCallError("CreateJobObjectW", callErr)
	}
	configured := false
	defer func() {
		if !configured {
			_, _, _ = closeHandle.Call(job)
		}
	}()

	limits := jobObjectExtendedLimitInformation{}
	limits.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	result, _, callErr := setInformationJobObject.Call(
		job,
		jobObjectExtendedLimitInformationClass,
		uintptr(unsafe.Pointer(&limits)),
		unsafe.Sizeof(limits),
	)
	if result == 0 {
		return windowsCallError("SetInformationJobObject", callErr)
	}

	currentProcess, err := syscall.GetCurrentProcess()
	if err != nil {
		return fmt.Errorf("GetCurrentProcess: %w", err)
	}
	result, _, callErr = assignProcessToJobObject.Call(job, uintptr(currentProcess))
	if result == 0 {
		return windowsCallError("AssignProcessToJobObject", callErr)
	}

	configured = true
	return nil
}

func windowsCallError(operation string, err error) error {
	if errno, ok := err.(syscall.Errno); ok && errno == 0 {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
