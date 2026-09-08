package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/securefs"
)

// RoutingMode is separate from account enablement and thread ownership.
// The sidecar deliberately leaves state.json readable by older router versions.
type RoutingMode struct {
	Mode      string `json:"mode"`
	AccountID string `json:"accountId,omitempty"`
}

func ParseRoutingMode(data []byte) (RoutingMode, error) {
	var mode RoutingMode
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return mode, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&mode); err != nil {
		return mode, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return mode, errors.New("trailing routing mode data")
	}
	return mode, mode.Validate()
}

func (r RoutingMode) Validate() error {
	if r.Mode == "auto" && r.AccountID == "" {
		return nil
	}
	if r.Mode == "account" && r.AccountID != "" && len(r.AccountID) <= maxAccountID && !strings.ContainsAny(r.AccountID, "/\\\x00") && !strings.Contains(r.AccountID, "..") {
		return nil
	}
	return errors.New("routing mode must be auto, or account with a valid accountId")
}

func (s *Store) RoutingMode() RoutingMode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.routingMode
}

func (s *Store) SetRoutingMode(mode RoutingMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := mode.Validate(); err != nil {
		return err
	}
	if mode.Mode == "account" {
		found := false
		for _, account := range s.accounts {
			if account.ID == mode.AccountID && account.Enabled {
				found = true
				break
			}
		}
		if !found {
			return errors.New("routing account must exist and be enabled")
		}
	}
	return s.saveRoutingModeLocked(mode)
}

func (s *Store) saveRoutingModeLocked(mode RoutingMode) error {
	data, err := json.Marshal(struct {
		Version int `json:"version"`
		RoutingMode
	}{1, mode})
	if err != nil {
		return err
	}
	path := filepath.Join(s.root, "routing-mode.json")
	if err := ensureNoReparsePath(s.root, path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := atomicWriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("commit routing mode: %w", err)
	}
	s.routingMode = mode
	return nil
}

func (s *Store) loadRoutingMode() error {
	s.routingMode = RoutingMode{Mode: "auto"}
	path := filepath.Join(s.root, "routing-mode.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return errors.New("invalid routing mode file")
	}
	if err := ensureNoReparsePath(s.root, path); err != nil {
		return err
	}
	if err := securefs.PrivateFile(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var input struct {
		Version int `json:"version"`
		RoutingMode
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing routing mode data")
	}
	if input.Version != 1 {
		return errors.New("unsupported routing mode version")
	}
	if err := input.RoutingMode.Validate(); err != nil {
		return err
	}
	if input.Mode == "account" {
		for _, account := range s.accounts {
			if account.ID == input.AccountID && account.Enabled {
				s.routingMode = input.RoutingMode
				return nil
			}
		}
		// Older binaries can remove/disable accounts without knowing this sidecar.
		return s.saveRoutingModeLocked(RoutingMode{Mode: "auto"})
	}
	s.routingMode = input.RoutingMode
	return nil
}
