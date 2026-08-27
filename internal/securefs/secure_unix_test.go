//go:build !windows

package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateObjectsRejectSymlinksAndSetExactModes(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDirectory(directory); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %04o", info.Mode().Perm())
	}
	path := filepath.Join(directory, "file")
	if err := os.WriteFile(path, []byte("secret"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := PrivateFile(path); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %04o", info.Mode().Perm())
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := PrivateFile(link); err == nil {
		t.Fatal("PrivateFile() accepted a symlink")
	}
}
