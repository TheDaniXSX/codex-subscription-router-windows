package securefs

import (
	"fmt"
	"os"
	"path/filepath"
)

// WritePrivateFileAtomic publishes a complete private file in one rename. The
// temporary file is restricted before any caller-supplied bytes are written,
// so credentials and control material are never exposed through an inherited
// ACL while the write is in progress.
func WritePrivateFileAtomic(path string, contents []byte) (result error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if temporary != nil {
			_ = temporary.Close()
		}
		if result != nil {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if err := PrivateFile(temporaryPath); err != nil {
		return fmt.Errorf("restrict temporary file access: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	temporary = nil
	if err := replacePrivateFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace destination: %w", err)
	}
	if err := syncPrivateDirectory(directory); err != nil {
		return fmt.Errorf("flush destination directory: %w", err)
	}
	return nil
}
