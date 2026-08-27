//go:build !windows

package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func pathsEqual(left, right string) bool {
	return left == right
}

func canonicalExistingPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func ensureNoReparsePath(base, target string) error {
	baseAbsolute, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	baseInfo, err := os.Lstat(baseAbsolute)
	if err != nil {
		return fmt.Errorf("inspect trusted root %q: %w", baseAbsolute, err)
	}
	if baseInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("trusted root %q is a symbolic link", baseAbsolute)
	}
	baseCanonical, err := canonicalExistingPath(base)
	if err != nil {
		return err
	}
	targetAbsolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	// Walk the caller-provided spelling first. On macOS, a trusted path under
	// /var commonly canonicalizes to /private/var; comparing that canonical base
	// with the original target would reject an identical path. The final
	// canonical containment check below still prevents symlink escapes.
	relative, err := filepath.Rel(baseAbsolute, targetAbsolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("path %q is outside trusted root %q", target, base)
	}
	current := baseAbsolute
	components := []string{"."}
	if relative != "." {
		components = strings.Split(relative, string(filepath.Separator))
	}
	for _, component := range components {
		if component != "." {
			current = filepath.Join(current, component)
		}
		info, statErr := os.Lstat(current)
		if statErr != nil {
			return fmt.Errorf("inspect path component %q: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symbolic link", current)
		}
	}
	targetCanonical, err := canonicalExistingPath(targetAbsolute)
	if err != nil {
		return err
	}
	canonicalRelative, err := filepath.Rel(baseCanonical, targetCanonical)
	if err != nil || canonicalRelative == ".." || strings.HasPrefix(canonicalRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(canonicalRelative) {
		return fmt.Errorf("canonical path %q escapes trusted root %q", targetCanonical, baseCanonical)
	}
	return nil
}
