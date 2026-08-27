//go:build !windows

package securefs

import "os"

func replacePrivateFile(source, destination string) error {
	return os.Rename(source, destination)
}

func syncPrivateDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
