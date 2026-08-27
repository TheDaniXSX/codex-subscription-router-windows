//go:build !windows

package securefs

import (
	"fmt"
	"os"
)

func PrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private object %q is not a real directory", path)
	}
	return os.Chmod(path, 0o700)
}

func PrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private object %q is not a regular file", path)
	}
	return os.Chmod(path, 0o600)
}
