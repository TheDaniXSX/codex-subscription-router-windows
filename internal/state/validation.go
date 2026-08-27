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
)

const (
	maxStateFileSize = 64 << 20
	maxAccounts      = 64
	maxAccountID     = 64
	maxAccountLabel  = 256
	maxThreadID      = 4096
)

func normalizeConfiguredPath(path, description string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s is required", description)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", description, err)
	}
	if _, err := os.Stat(absolute); err == nil {
		canonical, canonicalErr := canonicalExistingPath(absolute)
		if canonicalErr != nil {
			return "", fmt.Errorf("canonicalize %s: %w", description, canonicalErr)
		}
		return canonical, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect %s: %w", description, err)
	}
	return filepath.Clean(absolute), nil
}

func readPersistedState(path, root string) (persistedState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return persistedState{}, err
	}
	if !info.Mode().IsRegular() {
		return persistedState{}, fmt.Errorf("state path is not a regular file")
	}
	if err := ensureNoReparsePath(root, path); err != nil {
		return persistedState{}, fmt.Errorf("state path is not trusted: %w", err)
	}
	if info.Size() > maxStateFileSize {
		return persistedState{}, fmt.Errorf("state file is %d bytes, limit is %d", info.Size(), maxStateFileSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return persistedState{}, err
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return persistedState{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var persisted persistedState
	if err := decoder.Decode(&persisted); err != nil {
		return persistedState{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return persistedState{}, fmt.Errorf("state contains multiple JSON values")
		}
		return persistedState{}, fmt.Errorf("read trailing state data: %w", err)
	}
	return persisted, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("state contains duplicate JSON key %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			closing, err := decoder.Token()
			if err != nil {
				return err
			}
			if closing != json.Delim('}') {
				return errors.New("state JSON object is not closed")
			}
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			closing, err := decoder.Token()
			if err != nil {
				return err
			}
			if closing != json.Delim(']') {
				return errors.New("state JSON array is not closed")
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("state contains multiple JSON values")
		}
		return err
	}
	return nil
}

func validatePersistedState(root, primaryCodexHome string, persisted persistedState) ([]Account, map[string]string, error) {
	if persisted.Version != stateVersion {
		return nil, nil, fmt.Errorf("unsupported state version %d", persisted.Version)
	}
	if len(persisted.Accounts) == 0 {
		return nil, nil, errors.New("state must contain a primary account")
	}
	if len(persisted.Accounts) > maxAccounts {
		return nil, nil, fmt.Errorf("state contains %d accounts, limit is %d", len(persisted.Accounts), maxAccounts)
	}

	accounts := make([]Account, len(persisted.Accounts))
	copy(accounts, persisted.Accounts)
	ids := make(map[string]struct{}, len(accounts))
	controllerCount := 0
	primaryCount := 0
	for index := range accounts {
		account := &accounts[index]
		if err := validateAccountID(account.ID); err != nil {
			return nil, nil, fmt.Errorf("account %d: %w", index, err)
		}
		foldedID := strings.ToLower(account.ID)
		if _, exists := ids[foldedID]; exists {
			return nil, nil, fmt.Errorf("duplicate account ID %q", account.ID)
		}
		ids[foldedID] = struct{}{}
		if strings.TrimSpace(account.Label) == "" || account.Label != strings.TrimSpace(account.Label) {
			return nil, nil, fmt.Errorf("account %q has an empty or untrimmed label", account.ID)
		}
		if len(account.Label) > maxAccountLabel || strings.ContainsRune(account.Label, 0) {
			return nil, nil, fmt.Errorf("account %q label is invalid", account.ID)
		}
		if account.CreatedAt < 0 {
			return nil, nil, fmt.Errorf("account %q has a negative creation time", account.ID)
		}
		if account.Controller {
			controllerCount++
			if !account.Enabled {
				return nil, nil, fmt.Errorf("controller account %q must be enabled", account.ID)
			}
		}

		if account.ID == "primary" {
			primaryCount++
			if !account.Controller {
				return nil, nil, errors.New("primary account must be the controller")
			}
			if !samePath(account.CodexHome, primaryCodexHome) {
				return nil, nil, fmt.Errorf("primary Codex home %q does not match configured home %q", account.CodexHome, primaryCodexHome)
			}
			account.CodexHome = primaryCodexHome
			continue
		}
		canonicalHome, err := validateSecondaryHome(root, *account)
		if err != nil {
			return nil, nil, err
		}
		account.CodexHome = canonicalHome
	}
	if primaryCount != 1 {
		return nil, nil, fmt.Errorf("state has %d primary accounts, want exactly 1", primaryCount)
	}
	if controllerCount != 1 {
		return nil, nil, fmt.Errorf("state has %d controller accounts, want exactly 1", controllerCount)
	}
	for left := range accounts {
		for right := left + 1; right < len(accounts); right++ {
			if samePath(accounts[left].CodexHome, accounts[right].CodexHome) {
				return nil, nil, fmt.Errorf("accounts %q and %q share a Codex home", accounts[left].ID, accounts[right].ID)
			}
		}
	}

	owners := make(map[string]string, len(persisted.ThreadOwner))
	validIDs := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		validIDs[account.ID] = struct{}{}
	}
	for threadID, accountID := range persisted.ThreadOwner {
		if err := validateThreadID(threadID); err != nil {
			return nil, nil, err
		}
		if _, exists := validIDs[accountID]; !exists {
			return nil, nil, fmt.Errorf("thread %q references unknown account %q", threadID, accountID)
		}
		owners[threadID] = accountID
	}
	return accounts, owners, nil
}

func validateAccountID(id string) error {
	if id == "" || len(id) > maxAccountID || id == "." || id == ".." {
		return fmt.Errorf("invalid account ID %q", id)
	}
	for _, character := range id {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return fmt.Errorf("invalid account ID %q", id)
	}
	return nil
}

func validateThreadID(id string) error {
	if id == "" || id != strings.TrimSpace(id) || len(id) > maxThreadID || strings.ContainsRune(id, 0) {
		return fmt.Errorf("invalid thread ID %q", id)
	}
	return nil
}

func validateSecondaryHome(root string, account Account) (string, error) {
	expected := filepath.Join(root, "accounts", account.ID, "codex-home")
	accountAbsolute, err := filepath.Abs(account.CodexHome)
	if err != nil {
		return "", fmt.Errorf("resolve account %q home: %w", account.ID, err)
	}
	if !pathsEqual(filepath.Clean(accountAbsolute), filepath.Clean(expected)) {
		return "", fmt.Errorf("account %q home %q is outside its managed location %q", account.ID, account.CodexHome, expected)
	}
	info, err := os.Lstat(expected)
	if err != nil {
		return "", fmt.Errorf("inspect account %q home: %w", account.ID, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("account %q home is not a directory", account.ID)
	}
	if err := ensureNoReparsePath(root, expected); err != nil {
		return "", fmt.Errorf("account %q home is not trusted: %w", account.ID, err)
	}
	canonical, err := canonicalExistingPath(expected)
	if err != nil {
		return "", fmt.Errorf("canonicalize account %q home: %w", account.ID, err)
	}
	return canonical, nil
}
