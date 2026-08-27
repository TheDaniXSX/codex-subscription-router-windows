package securefs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPrivateWindowsPaths(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDirectory(directory); err != nil {
		t.Fatalf("PrivateDirectory() error = %v", err)
	}
	path := filepath.Join(directory, "secret")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrivateFile(path); err != nil {
		t.Fatalf("PrivateFile() error = %v", err)
	}
}

func TestPrivateDirectoryRemovesExplicitAdministratorGrant(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "explicit-administrators")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	command := exec.Command(icacls, directory, "/grant", "*S-1-5-32-544:(OI)(CI)F")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("inject explicit Administrators ACE: %v: %s", err, output)
	}
	if err := PrivateDirectory(directory); err != nil {
		t.Fatalf("PrivateDirectory() error = %v", err)
	}
}

func TestPrivateFileProducesExactOwnerAndACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-file")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrivateFile(path); err != nil {
		t.Fatal(err)
	}
	sid, err := currentSID()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyRestrictedACL(path, sid, 0); err != nil {
		t.Fatalf("exact file ACL verification: %v", err)
	}
}

func TestPrivateDirectoryProducesExactInheritedACL(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private-directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDirectory(directory); err != nil {
		t.Fatal(err)
	}
	sid, err := currentSID()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyRestrictedACL(directory, sid, objectInheritACE|containerInheritACE); err != nil {
		t.Fatalf("exact directory ACL verification: %v", err)
	}
}

func TestPrivateFileRejectsReparsePoint(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	link := filepath.Join(directory, "link")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("junction creation is unavailable: %v: %s", err, output)
	}
	if err := PrivateFile(link); err == nil {
		t.Fatal("PrivateFile() accepted a reparse point")
	}
}
