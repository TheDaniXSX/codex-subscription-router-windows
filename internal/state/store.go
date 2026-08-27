package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/securefs"
)

const stateVersion = 1

type Account struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	CodexHome  string `json:"codexHome"`
	Enabled    bool   `json:"enabled"`
	Controller bool   `json:"controller"`
	CreatedAt  int64  `json:"createdAt"`
}

type persistedState struct {
	Version     int               `json:"version"`
	Accounts    []Account         `json:"accounts"`
	ThreadOwner map[string]string `json:"threadOwner"`
}

// Store persists only routing metadata. OAuth credentials and conversation
// databases remain inside each account's isolated Codex home.
type Store struct {
	mu               sync.RWMutex
	root             string
	path             string
	primaryCodexHome string
	accounts         []Account
	owners           map[string]string
}

func Open(root, primaryCodexHome string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("state root is required")
	}
	normalizedRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve state root: %w", err)
	}
	normalizedRoot = filepath.Clean(normalizedRoot)
	if err := os.MkdirAll(normalizedRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create state root: %w", err)
	}
	if err := ensureNoReparsePath(normalizedRoot, normalizedRoot); err != nil {
		return nil, fmt.Errorf("validate state root: %w", err)
	}
	normalizedRoot, err = canonicalExistingPath(normalizedRoot)
	if err != nil {
		return nil, fmt.Errorf("canonicalize state root: %w", err)
	}
	if err := securefs.PrivateDirectory(normalizedRoot); err != nil {
		return nil, fmt.Errorf("secure state root: %w", err)
	}
	normalizedPrimaryHome, err := normalizeConfiguredPath(primaryCodexHome, "primary Codex home")
	if err != nil {
		return nil, err
	}

	store := &Store{
		root:             normalizedRoot,
		path:             filepath.Join(normalizedRoot, "state.json"),
		primaryCodexHome: normalizedPrimaryHome,
		owners:           make(map[string]string),
	}
	persisted, err := readPersistedState(store.path, store.root)
	switch {
	case err == nil:
		if err := securefs.PrivateFile(store.path); err != nil {
			return nil, fmt.Errorf("secure state: %w", err)
		}
		accounts, owners, validateErr := validatePersistedState(store.root, store.primaryCodexHome, persisted)
		if validateErr != nil {
			return nil, fmt.Errorf("validate state: %w", validateErr)
		}
		store.accounts = accounts
		store.owners = owners
	case errors.Is(err, os.ErrNotExist):
		store.accounts = []Account{{
			ID:         "primary",
			Label:      "Primary",
			CodexHome:  store.primaryCodexHome,
			Enabled:    true,
			Controller: true,
			CreatedAt:  time.Now().Unix(),
		}}
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("read state: %w", err)
	}
	for _, account := range store.accounts {
		if samePath(account.CodexHome, store.primaryCodexHome) {
			continue
		}
		if err := securefs.PrivateDirectory(account.CodexHome); err != nil {
			return nil, fmt.Errorf("secure account %q home: %w", account.ID, err)
		}
		configPath := filepath.Join(account.CodexHome, "config.toml")
		if _, err := os.Stat(configPath); err == nil {
			if err := securefs.PrivateFile(configPath); err != nil {
				return nil, fmt.Errorf("secure account %q config: %w", account.ID, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect account %q config: %w", account.ID, err)
		}
		if err := syncIsolatedConfig(store.primaryCodexHome, account.CodexHome); err != nil {
			return nil, fmt.Errorf("sync account %q config: %w", account.ID, err)
		}
	}
	return store, nil
}

func (s *Store) Root() string {
	return s.root
}

// SyncManagedConfig propagates desktop-managed configuration (including
// plugins, marketplaces, skills, and MCP server definitions) to every
// isolated subscription. Credential stores and project trust remain local to
// each account; syncIsolatedConfig deliberately excludes both.
func (s *Store) SyncManagedConfig() error {
	s.mu.RLock()
	accounts := slices.Clone(s.accounts)
	primaryCodexHome := s.primaryCodexHome
	s.mu.RUnlock()

	for _, account := range accounts {
		if samePath(account.CodexHome, primaryCodexHome) {
			continue
		}
		if err := syncIsolatedConfig(primaryCodexHome, account.CodexHome); err != nil {
			return fmt.Errorf("sync account %q config: %w", account.ID, err)
		}
	}
	return nil
}

func (s *Store) Accounts() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.accounts)
}

func (s *Store) Account(id string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.accounts {
		if account.ID == id {
			return account, true
		}
	}
	return Account{}, false
}

func (s *Store) Controller() (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.accounts {
		if account.Controller {
			return account, true
		}
	}
	if len(s.accounts) == 0 {
		return Account{}, false
	}
	return s.accounts[0], true
}

func (s *Store) AddAccount(label string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	label = strings.TrimSpace(label)
	if label == "" {
		label = fmt.Sprintf("Subscription %d", len(s.accounts)+1)
	}
	if len(label) > maxAccountLabel || strings.ContainsRune(label, 0) {
		return Account{}, errors.New("account label is invalid")
	}
	var id string
	for attempts := 0; attempts < 8; attempts++ {
		candidate, err := randomID()
		if err != nil {
			return Account{}, err
		}
		if !s.accountExistsLocked(candidate) {
			id = candidate
			break
		}
	}
	if id == "" {
		return Account{}, errors.New("could not generate a unique account ID")
	}
	codexHome := filepath.Join(s.root, "accounts", id, "codex-home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return Account{}, fmt.Errorf("create account home: %w", err)
	}
	accountRoot := filepath.Join(s.root, "accounts", id)
	cleanup := func() { _ = os.RemoveAll(accountRoot) }
	if err := ensureNoReparsePath(s.root, codexHome); err != nil {
		cleanup()
		return Account{}, fmt.Errorf("validate account home: %w", err)
	}
	for _, directory := range []string{filepath.Join(s.root, "accounts"), accountRoot, codexHome} {
		if err := securefs.PrivateDirectory(directory); err != nil {
			cleanup()
			return Account{}, fmt.Errorf("secure account directory %q: %w", directory, err)
		}
	}
	if err := syncIsolatedConfig(s.primaryCodexHome, codexHome); err != nil {
		cleanup()
		return Account{}, fmt.Errorf("write account config: %w", err)
	}
	canonicalHome, err := canonicalExistingPath(codexHome)
	if err != nil {
		cleanup()
		return Account{}, fmt.Errorf("canonicalize account home: %w", err)
	}

	account := Account{
		ID:        id,
		Label:     label,
		CodexHome: canonicalHome,
		Enabled:   true,
		CreatedAt: time.Now().Unix(),
	}
	previousLength := len(s.accounts)
	s.accounts = append(s.accounts, account)
	if err := s.saveLocked(); err != nil {
		s.accounts = s.accounts[:previousLength]
		cleanup()
		return Account{}, err
	}
	return account, nil
}

func (s *Store) UpdateAccount(id string, label *string, enabled *bool) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.accounts {
		if s.accounts[index].ID != id {
			continue
		}
		previous := s.accounts[index]
		if enabled != nil && s.accounts[index].Controller && !*enabled {
			return Account{}, errors.New("controller account cannot be disabled")
		}
		if label != nil {
			trimmed := strings.TrimSpace(*label)
			if trimmed == "" {
				return Account{}, errors.New("account label cannot be empty")
			}
			if len(trimmed) > maxAccountLabel || strings.ContainsRune(trimmed, 0) {
				return Account{}, errors.New("account label is invalid")
			}
			s.accounts[index].Label = trimmed
		}
		if enabled != nil {
			s.accounts[index].Enabled = *enabled
		}
		if err := s.saveLocked(); err != nil {
			s.accounts[index] = previous
			return Account{}, err
		}
		return s.accounts[index], nil
	}
	return Account{}, fmt.Errorf("account %q not found", id)
}

func (s *Store) ThreadOwner(threadID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	owner, ok := s.owners[threadID]
	return owner, ok
}

func (s *Store) SetThreadOwner(threadID, accountID string) error {
	return s.SetThreadOwners(map[string]string{threadID: accountID})
}

// SetThreadOwners commits a complete batch of newly learned affinities with a
// single atomic state-file replacement. This keeps large thread/list results
// from rewriting state.json once per thread.
func (s *Store) SetThreadOwners(assignments map[string]string) error {
	if len(assignments) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for threadID, accountID := range assignments {
		if err := validateThreadID(threadID); err != nil {
			return err
		}
		if !s.accountExistsLocked(accountID) {
			return fmt.Errorf("account %q not found", accountID)
		}
	}
	previous := s.owners
	next := make(map[string]string, len(s.owners)+len(assignments))
	for threadID, accountID := range s.owners {
		next[threadID] = accountID
	}
	changed := false
	for threadID, accountID := range assignments {
		if next[threadID] != accountID {
			next[threadID] = accountID
			changed = true
		}
	}
	if !changed {
		return nil
	}
	s.owners = next
	if err := s.saveLocked(); err != nil {
		s.owners = previous
		return err
	}
	return nil
}

// RemoveAccount removes a non-controller account and every affinity pointing
// to it in one state commit. Only the exact account directory managed by this
// Store can be removed; persisted paths are revalidated immediately before the
// filesystem operation.
func (s *Store) RemoveAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// RemoveAccount is reachable from the local control API. Keep the supplied
	// identifier as a single path-free component even though persisted account
	// IDs are validated when the store opens.
	if strings.Contains(id, "/") || strings.Contains(id, `\`) || strings.Contains(id, "..") {
		return fmt.Errorf("invalid account ID %q", id)
	}

	index := -1
	var account Account
	for candidate := range s.accounts {
		if s.accounts[candidate].ID == id {
			index = candidate
			account = s.accounts[candidate]
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("account %q not found", id)
	}
	if account.ID == "primary" || account.Controller {
		return errors.New("primary/controller account cannot be removed")
	}
	canonicalHome, err := validateSecondaryHome(s.root, account)
	if err != nil {
		return fmt.Errorf("refuse to remove untrusted account home: %w", err)
	}
	accountRoot := filepath.Dir(canonicalHome)

	previousAccounts := s.accounts
	previousOwners := s.owners
	nextAccounts := make([]Account, 0, len(s.accounts)-1)
	nextAccounts = append(nextAccounts, s.accounts[:index]...)
	nextAccounts = append(nextAccounts, s.accounts[index+1:]...)
	nextOwners := make(map[string]string, len(s.owners))
	for threadID, owner := range s.owners {
		if owner != id {
			nextOwners[threadID] = owner
		}
	}
	s.accounts = nextAccounts
	s.owners = nextOwners
	if err := s.saveLocked(); err != nil {
		s.accounts = previousAccounts
		s.owners = previousOwners
		return err
	}

	quarantineID, err := randomID()
	if err != nil {
		return s.rollbackRemovalLocked(previousAccounts, previousOwners, fmt.Errorf("generate cleanup ID: %w", err))
	}
	quarantine := filepath.Join(s.root, "accounts", ".removed-"+quarantineID)
	if err := os.Rename(accountRoot, quarantine); err != nil {
		return s.rollbackRemovalLocked(previousAccounts, previousOwners, fmt.Errorf("quarantine account directory: %w", err))
	}
	if err := os.RemoveAll(quarantine); err != nil {
		return fmt.Errorf("account metadata removed but quarantined files at %q could not be deleted: %w", quarantine, err)
	}
	return nil
}

func (s *Store) rollbackRemovalLocked(accounts []Account, owners map[string]string, cause error) error {
	s.accounts = accounts
	s.owners = owners
	if err := s.saveLocked(); err != nil {
		return errors.Join(cause, fmt.Errorf("rollback account metadata: %w", err))
	}
	return cause
}

func (s *Store) ThreadCounts() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := make(map[string]int)
	for _, accountID := range s.owners {
		counts[accountID]++
	}
	return counts
}

func (s *Store) saveLocked() error {
	persisted := persistedState{
		Version:     stateVersion,
		Accounts:    s.accounts,
		ThreadOwner: s.owners,
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := atomicWriteFile(s.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("commit state: %w", err)
	}
	return nil
}

func (s *Store) accountExistsLocked(id string) bool {
	for _, account := range s.accounts {
		if account.ID == id {
			return true
		}
	}
	return false
}

func randomID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate account ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
