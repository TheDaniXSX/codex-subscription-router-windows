//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

func main() {
	arguments := os.Args[1:]
	selfTest := len(arguments) == 1 && arguments[0] == selfTestArgument
	diagnostics := len(arguments) == 1 && arguments[0] == diagnosticsArgument
	executable, err := os.Executable()
	if err != nil {
		exitWithError(selfTest || diagnostics, fmt.Errorf("resolve launcher executable: %w", err))
	}
	plan, err := buildLaunchPlan(
		executable,
		arguments,
		os.LookupEnv,
		readOptionalFile,
		checkRegularFile,
	)
	if err != nil {
		exitWithError(selfTest || diagnostics, err)
	}
	if err := checkDistinctFiles(executable, plan.RealApp); err != nil {
		exitWithError(selfTest || diagnostics, fmt.Errorf("validate real desktop executable: %w", err))
	}
	if err := checkDistinctFiles(plan.Mux, plan.RealCodex); err != nil {
		exitWithError(selfTest || diagnostics, fmt.Errorf("validate real Codex executable: %w", err))
	}
	if selfTest || diagnostics {
		fmt.Fprintln(os.Stdout, "status=ok")
		fmt.Fprintf(os.Stdout, "root_source=%s\n", plan.RootSource)
		fmt.Fprintf(os.Stdout, "state_root=%s\n", plan.StateRoot)
		fmt.Fprintf(os.Stdout, "profile=%s\n", plan.Profile)
		fmt.Fprintf(os.Stdout, "real_app=%s\n", plan.RealApp)
		fmt.Fprintf(os.Stdout, "mux=%s\n", plan.Mux)
		fmt.Fprintf(os.Stdout, "real_codex=%s\n", plan.RealCodex)
		fmt.Fprintf(os.Stdout, "control_port=%d\n", plan.ControlPort)
		fmt.Fprintf(os.Stdout, "launcher_config_schema=%d\n", plan.ConfigSchemaVersion)
		if diagnostics {
			fmt.Fprintln(os.Stdout, "process_supervision=windows-job-object")
			if plan.ConfigSchemaVersion == 1 {
				fmt.Fprintln(os.Stdout, "warning=legacy-control-port-48123-upgrade-required")
			}
			if explicitEnvironmentOptIn(os.Environ(), appshotsEnvironment) {
				fmt.Fprintln(os.Stdout, "appshots=enabled-opt-in")
			} else {
				fmt.Fprintln(os.Stdout, "appshots=disabled-default")
			}
		}
		return
	}
	err = runReleaseLaunch(plan, func() error {
		if err := os.MkdirAll(plan.Profile, 0o700); err != nil {
			return fmt.Errorf("create isolated profile %s: %w", plan.Profile, err)
		}
		if err := superviseDescendants(); err != nil {
			return fmt.Errorf("enable process-tree supervision: %w", err)
		}

		command := exec.Command(plan.RealApp, plan.Arguments...)
		command.Dir = plan.AppDirectory
		command.Env = childEnvironment(os.Environ(), plan)
		command.Stdin = os.Stdin
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			return fmt.Errorf("start %s: %w", plan.RealApp, err)
		}
		stopBrandingSync, brandingErr := startWindowBrandingSync(command.Process.Pid, executable)
		if brandingErr != nil {
			fmt.Fprintf(os.Stderr, "Codex Subscription Router launcher: window branding warning: %v\n", brandingErr)
		}
		err := command.Wait()
		stopBrandingSync()
		if err != nil {
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				return exitError
			}
			return fmt.Errorf("wait for %s: %w", plan.RealApp, err)
		}
		return nil
	})
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.ExitCode())
		}
		exitWithError(false, err)
	}
}

func exitWithError(selfTest bool, err error) {
	message := "Codex Subscription Router launcher: " + err.Error()
	fmt.Fprintln(os.Stderr, message)
	if !selfTest {
		showErrorMessage(message)
	}
	os.Exit(1)
}

func showErrorMessage(message string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	text, textErr := syscall.UTF16PtrFromString(message)
	title, titleErr := syscall.UTF16PtrFromString(productDirectoryName)
	if textErr != nil || titleErr != nil {
		return
	}
	const mbOK = 0x00000000
	const mbIconError = 0x00000010
	const mbSetForeground = 0x00010000
	_, _, _ = messageBox.Call(
		0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(title)),
		mbOK|mbIconError|mbSetForeground,
	)
}
