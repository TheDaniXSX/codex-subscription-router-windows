package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWritePrivateFileAtomicReplacesCompleteFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "private")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFileAtomic(path, []byte("new contents")); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "new contents" {
		t.Fatalf("contents = %q", contents)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".private-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("atomic write left temporary files: %v", matches)
	}
}
