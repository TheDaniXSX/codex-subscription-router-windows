package securefs

import (
	"errors"
	"os"
	"syscall"
	"time"
)

func replacePrivateFile(source, destination string) error {
	delays := [...]time.Duration{0, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond}
	var err error
	for _, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		err = os.Rename(source, destination)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.Errno(32)) && !errors.Is(err, syscall.Errno(33)) {
			return err
		}
	}
	return err
}

func syncPrivateDirectory(string) error {
	return nil
}
