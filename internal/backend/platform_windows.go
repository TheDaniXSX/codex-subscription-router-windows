package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	createNoWindow                    = 0x08000000
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	processTerminate                  = 0x0001
	processSetQuota                   = 0x0100
	processSynchronize                = 0x00100000
	waitObject0                       = 0x00000000
	waitTimeout                       = 0x00000102
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	openProcess              = kernel32.NewProc("OpenProcess")
	waitForSingleObject      = kernel32.NewProc("WaitForSingleObject")
)

type basicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type extendedLimitInformation struct {
	BasicLimitInformation basicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type windowsJobSupervisor struct {
	handle syscall.Handle
	once   sync.Once
	err    error
}

func prepareCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

func superviseProcess(process *os.Process) (processSupervisor, error) {
	job, _, callErr := createJobObjectW.Call(0, 0)
	if job == 0 {
		return nil, fmt.Errorf("create Windows Job Object: %w", callErr)
	}
	supervisor := &windowsJobSupervisor{handle: syscall.Handle(job)}
	limits := extendedLimitInformation{}
	limits.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	configured, _, configureErr := setInformationJobObject.Call(
		job,
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		unsafe.Sizeof(limits),
	)
	if configured == 0 {
		_ = supervisor.Close()
		return nil, fmt.Errorf("configure Windows Job Object: %w", configureErr)
	}
	processHandle, _, openErr := openProcess.Call(
		processTerminate|processSetQuota,
		0,
		uintptr(uint32(process.Pid)),
	)
	if processHandle == 0 {
		_ = supervisor.Close()
		return nil, fmt.Errorf("open child process for Job Object: %w", openErr)
	}
	defer syscall.CloseHandle(syscall.Handle(processHandle))
	assigned, _, assignErr := assignProcessToJobObject.Call(job, processHandle)
	if assigned == 0 {
		_ = supervisor.Close()
		return nil, fmt.Errorf("assign child to Windows Job Object: %w", assignErr)
	}
	return supervisor, nil
}

func (s *windowsJobSupervisor) Terminate() error {
	return s.Close()
}

func (s *windowsJobSupervisor) Close() error {
	s.once.Do(func() {
		if err := syscall.CloseHandle(s.handle); err != nil && !errors.Is(err, syscall.Errno(6)) {
			s.err = err
		}
	})
	return s.err
}

func environmentKeyEqual(left, right string) bool {
	return strings.EqualFold(left, right)
}

func environmentKeyHasPrefix(key, prefix string) bool {
	return len(key) >= len(prefix) && strings.EqualFold(key[:len(prefix)], prefix)
}
