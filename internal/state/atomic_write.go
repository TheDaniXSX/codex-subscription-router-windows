package state

import (
	"fmt"
	"io/fs"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/securefs"
)

func atomicWriteFile(path string, contents []byte, mode fs.FileMode) error {
	if mode.Perm() != 0o600 {
		return fmt.Errorf("private atomic writes require mode 0600, got %04o", mode.Perm())
	}
	return securefs.WritePrivateFileAtomic(path, contents)
}
