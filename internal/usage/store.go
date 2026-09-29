package usage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/securefs"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

func open(path string, opts Options) (*Manager, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("usage database path is empty")
	}
	if opts.MaxRootTurns <= 0 {
		opts.MaxRootTurns = DefaultMaxRootTurns
	}
	if opts.FastPrior <= 0 || !finite(opts.FastPrior) {
		opts.FastPrior = 2.5
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if len(opts.RateCards) == 0 {
		opts.RateCards = defaultRateCards()
	}
	for _, card := range opts.RateCards {
		if strings.TrimSpace(card.Model) == "" || strings.TrimSpace(card.Version) == "" ||
			!finite(card.InputUSDPerMillion) || !finite(card.CachedInputUSDPerMillion) || !finite(card.OutputUSDPerMillion) ||
			card.InputUSDPerMillion < 0 || card.CachedInputUSDPerMillion < 0 || card.OutputUSDPerMillion < 0 {
			return nil, errors.New("invalid versioned API rate card")
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve usage database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create usage directory: %w", err)
	}
	if err := securefs.PrivateDirectory(filepath.Dir(absolute)); err != nil {
		return nil, fmt.Errorf("restrict usage directory: %w", err)
	}
	if _, err := os.Lstat(absolute); err == nil {
		if err := securefs.PrivateFile(absolute); err != nil {
			return nil, fmt.Errorf("restrict existing usage database: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect usage database: %w", err)
	}
	// Encode the filename as a file URL so punctuation in a Windows path is
	// not interpreted as SQLite URI options.
	dsn := sqliteFileURL(absolute)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open usage database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	closeOnError := func(err error) (*Manager, error) { _ = db.Close(); return nil, err }
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000; PRAGMA journal_mode = DELETE; PRAGMA synchronous = FULL; PRAGMA trusted_schema = OFF;`); err != nil {
		return closeOnError(fmt.Errorf("configure usage database: %w", err))
	}
	if err := db.Ping(); err != nil {
		return closeOnError(fmt.Errorf("initialize usage database: %w", err))
	}
	if err := securefs.PrivateFile(absolute); err != nil {
		return closeOnError(fmt.Errorf("restrict usage database: %w", err))
	}
	if err := initializeSchema(db); err != nil {
		return closeOnError(err)
	}
	if err := recoverActive(db, opts.Now()); err != nil {
		return closeOnError(fmt.Errorf("recover interrupted usage requests: %w", err))
	}
	return &Manager{db: db, options: opts}, nil
}

func sqliteFileURL(path string) string {
	path = filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func initializeSchema(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read usage schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("usage database schema %d is newer than supported schema %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin usage schema migration: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value INTEGER NOT NULL)`,
		`INSERT INTO meta(key,value) VALUES ('revision',0)`,
		`INSERT INTO meta(key,value) VALUES ('calibration_revision',0)`,
		`CREATE TABLE root_turns (seq INTEGER PRIMARY KEY AUTOINCREMENT, root_turn_id TEXT NOT NULL UNIQUE, thread_id TEXT NOT NULL DEFAULT '', turn_id TEXT NOT NULL DEFAULT '', calibration_enabled INTEGER NOT NULL DEFAULT 0, completed INTEGER NOT NULL DEFAULT 0, created_ns INTEGER NOT NULL, updated_ns INTEGER NOT NULL)`,
		`CREATE TABLE requests (request_id TEXT PRIMARY KEY, root_turn_id TEXT NOT NULL DEFAULT '', parent_request_id TEXT NOT NULL DEFAULT '', account_id TEXT NOT NULL DEFAULT '', started_ns INTEGER NOT NULL DEFAULT 0, finished_ns INTEGER NOT NULL DEFAULT 0, outcome TEXT NOT NULL DEFAULT '', record_json TEXT NOT NULL, api_cost_json TEXT NOT NULL DEFAULT '{}', revision INTEGER NOT NULL)`,
		`CREATE INDEX requests_by_root ON requests(root_turn_id, started_ns, request_id)`,
		`CREATE INDEX requests_by_account_time ON requests(account_id, started_ns, finished_ns)`,
		`CREATE TABLE pending_relations (request_id TEXT PRIMARY KEY, root_turn_id TEXT NOT NULL DEFAULT '', parent_request_id TEXT NOT NULL DEFAULT '', parent_thread_id TEXT NOT NULL DEFAULT '', agent_id TEXT NOT NULL DEFAULT '', updated_ns INTEGER NOT NULL)`,
		`CREATE TABLE observations (id TEXT PRIMARY KEY, account_id TEXT NOT NULL, bucket TEXT NOT NULL, epoch_key TEXT NOT NULL, before_ns INTEGER NOT NULL, after_ns INTEGER NOT NULL, observation_json TEXT NOT NULL, revision INTEGER NOT NULL)`,
		`CREATE INDEX observations_by_bucket ON observations(bucket, account_id, before_ns, after_ns)`,
		`CREATE TABLE observation_members (observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE, request_id TEXT NOT NULL REFERENCES requests(request_id) ON DELETE CASCADE, root_turn_id TEXT NOT NULL DEFAULT '', PRIMARY KEY(observation_id, request_id))`,
		`CREATE INDEX observation_members_by_root ON observation_members(root_turn_id, observation_id)`,
		`CREATE TABLE frozen_estimates (request_id TEXT NOT NULL REFERENCES requests(request_id) ON DELETE CASCADE, bucket TEXT NOT NULL, estimate_json TEXT NOT NULL, PRIMARY KEY(request_id,bucket))`,
		`CREATE TABLE model_versions (id INTEGER PRIMARY KEY AUTOINCREMENT, bucket TEXT NOT NULL, calibration_revision INTEGER NOT NULL, model_version TEXT NOT NULL, fit_json TEXT NOT NULL, created_ns INTEGER NOT NULL, UNIQUE(bucket,calibration_revision))`,
		`PRAGMA user_version = 1`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("apply usage schema migration: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit usage schema migration: %w", err)
	}
	return nil
}

func recoverActive(db *sql.DB, now time.Time) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT request_id,record_json FROM requests WHERE finished_ns=0`)
	if err != nil {
		return err
	}
	type interrupted struct{ id, data string }
	var pending []interrupted
	for rows.Next() {
		var item interrupted
		if err := rows.Scan(&item.id, &item.data); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(pending) == 0 {
		return tx.Commit()
	}
	for _, item := range pending {
		var record Record
		if err := json.Unmarshal([]byte(item.data), &record); err != nil {
			return err
		}
		record.Outcome = "interrupted-restart"
		// FinishedAt remains zero: restart is not an upstream completion time.
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE requests SET outcome=?,finished_ns=?,record_json=? WHERE request_id=?`, record.Outcome, now.UnixNano(), string(encoded), item.id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE meta SET value=value+1 WHERE key='revision'`); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) close() error {
	if m == nil || m.db == nil {
		return nil
	}
	return m.db.Close()
}

func (m *Manager) completeTurn(ctx context.Context, rootTurnID string) error {
	if rootTurnID == "" {
		return errors.New("root turn id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	threadID, turnID := splitRootTurnID(rootTurnID)
	if err := ensureRoot(ctx, tx, rootTurnID, threadID, turnID, m.options.Now()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE root_turns SET completed=1,updated_ns=? WHERE root_turn_id=?`, m.options.Now().UnixNano(), rootTurnID); err != nil {
		return err
	}
	if _, err := bumpRevision(ctx, tx); err != nil {
		return err
	}
	if err := trimRoots(ctx, tx, m.options.MaxRootTurns); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) revision(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (uint64, error) {
	var revision int64
	if err := q.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='revision'`).Scan(&revision); err != nil {
		return 0, err
	}
	return uint64(revision), nil
}

func bumpRevision(ctx context.Context, tx *sql.Tx) (uint64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=value+1 WHERE key='revision'`); err != nil {
		return 0, err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='revision'`).Scan(&revision); err != nil {
		return 0, err
	}
	return uint64(revision), nil
}

func (m *Manager) begin(ctx context.Context, record Record) error {
	if strings.TrimSpace(record.RequestID) == "" {
		return errors.New("usage request id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if record.StartedAt.IsZero() {
		record.StartedAt = m.options.Now()
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM requests WHERE request_id=?`, record.RequestID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return tx.Commit()
	}
	if record.RootTurnID != "" {
		if err := ensureRoot(ctx, tx, record.RootTurnID, record.ThreadID, record.TurnID, record.StartedAt); err != nil {
			return err
		}
	}
	if relation, err := pendingRelation(ctx, tx, record.RequestID); err == nil {
		if record.RootTurnID == "" {
			record.RootTurnID = relation.rootID
		}
		if record.ParentRequestID == "" {
			record.ParentRequestID = relation.parentID
		}
		if record.ParentThreadID == "" {
			record.ParentThreadID = relation.parentThreadID
		}
		if record.AgentID == "" {
			record.AgentID = relation.agentID
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if record.RootTurnID != "" {
		if err := ensureRoot(ctx, tx, record.RootTurnID, record.ThreadID, record.TurnID, record.StartedAt); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	apiCost, _ := json.Marshal(APICost{UnavailableReason: "usage-not-final"})
	revision, err := bumpRevision(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO requests(request_id,root_turn_id,parent_request_id,account_id,started_ns,finished_ns,outcome,record_json,api_cost_json,revision)
VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(request_id) DO NOTHING`, record.RequestID, record.RootTurnID, record.ParentRequestID, record.AccountID, record.StartedAt.UnixNano(), int64(0), record.Outcome, string(encoded), string(apiCost), revision)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pending_relations WHERE request_id=?`, record.RequestID); err != nil {
		return err
	}
	if err := invalidateRootAttestation(ctx, tx, record.RootTurnID); err != nil {
		return err
	}
	// A late arrival can reveal a previously omitted concurrent request.
	if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=value+1 WHERE key='calibration_revision' AND EXISTS(SELECT 1 FROM observations WHERE account_id=? AND after_ns>?)`, record.AccountID, nanos(record.StartedAt)); err != nil {
		return err
	}
	if err := trimRoots(ctx, tx, m.options.MaxRootTurns); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) finish(ctx context.Context, requestID string, final Record) error {
	if strings.TrimSpace(requestID) == "" {
		return errors.New("usage request id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT record_json FROM requests WHERE request_id=?`, requestID).Scan(&raw)
	var record Record
	if errors.Is(err, sql.ErrNoRows) {
		record = Record{RequestID: requestID, StartedAt: m.options.Now()}
		if relation, relErr := pendingRelation(ctx, tx, requestID); relErr == nil {
			record.RootTurnID, record.ParentRequestID, record.ParentThreadID, record.AgentID = relation.rootID, relation.parentID, relation.parentThreadID, relation.agentID
		} else if !errors.Is(relErr, sql.ErrNoRows) {
			return relErr
		}
	} else if err != nil {
		return err
	} else if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return err
	}
	// The first final record, rate card and estimate form immutable evidence.
	// A replay must not silently replace historical usage or prices.
	if !record.FinishedAt.IsZero() {
		return tx.Commit()
	}
	previousRoot := record.RootTurnID
	mergeRecord(&record, final, requestID)
	if record.FinishedAt.IsZero() {
		record.FinishedAt = m.options.Now()
	}
	if record.RootTurnID != "" {
		if err := ensureRoot(ctx, tx, record.RootTurnID, record.ThreadID, record.TurnID, record.StartedAt); err != nil {
			return err
		}
	}
	if raw == "" || previousRoot != record.RootTurnID {
		if err := invalidateRootAttestation(ctx, tx, record.RootTurnID); err != nil {
			return err
		}
	}
	apiCost := calculateAPICost(record, m.options.RateCards)
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	costJSON, err := json.Marshal(apiCost)
	if err != nil {
		return err
	}
	revision, err := bumpRevision(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO requests(request_id,root_turn_id,parent_request_id,account_id,started_ns,finished_ns,outcome,record_json,api_cost_json,revision)
VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET root_turn_id=excluded.root_turn_id,parent_request_id=excluded.parent_request_id,account_id=excluded.account_id,started_ns=excluded.started_ns,finished_ns=excluded.finished_ns,outcome=excluded.outcome,record_json=excluded.record_json,api_cost_json=excluded.api_cost_json,revision=excluded.revision`, requestID, record.RootTurnID, record.ParentRequestID, record.AccountID, record.StartedAt.UnixNano(), record.FinishedAt.UnixNano(), record.Outcome, string(encoded), string(costJSON), revision)
	if err != nil {
		return err
	}
	if err := freezePredictions(ctx, tx, record, apiCost, m.options.FastPrior); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE observation_members SET root_turn_id=? WHERE request_id=?`, record.RootTurnID, requestID); err != nil {
		return err
	}
	if err := refreshObservationsForRequest(ctx, tx, requestID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=value+1 WHERE key='calibration_revision' AND EXISTS(SELECT 1 FROM observation_members WHERE request_id=?)`, requestID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pending_relations WHERE request_id=?`, requestID); err != nil {
		return err
	}
	if err := trimRoots(ctx, tx, m.options.MaxRootTurns); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) setRelation(ctx context.Context, requestID, rootTurnID, parentRequestID, agentID string) error {
	if requestID == "" || rootTurnID == "" {
		return errors.New("request and root turn ids are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureRoot(ctx, tx, rootTurnID, "", "", m.options.Now()); err != nil {
		return err
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT record_json FROM requests WHERE request_id=?`, requestID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		old, pendingErr := pendingRelation(ctx, tx, requestID)
		if pendingErr != nil && !errors.Is(pendingErr, sql.ErrNoRows) {
			return pendingErr
		}
		if old.rootID != rootTurnID {
			if err := invalidateRootAttestation(ctx, tx, rootTurnID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO pending_relations(request_id,root_turn_id,parent_request_id,agent_id,updated_ns) VALUES(?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET root_turn_id=excluded.root_turn_id,parent_request_id=CASE WHEN excluded.parent_request_id='' THEN pending_relations.parent_request_id ELSE excluded.parent_request_id END,agent_id=CASE WHEN excluded.agent_id='' THEN pending_relations.agent_id ELSE excluded.agent_id END,updated_ns=excluded.updated_ns`, requestID, rootTurnID, parentRequestID, agentID, m.options.Now().UnixNano())
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		var record Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return err
		}
		if record.RootTurnID != rootTurnID {
			if err := invalidateRootAttestation(ctx, tx, rootTurnID); err != nil {
				return err
			}
			if err := invalidateRootAttestation(ctx, tx, record.RootTurnID); err != nil {
				return err
			}
		}
		record.RootTurnID = rootTurnID
		if parentRequestID != "" {
			record.ParentRequestID = parentRequestID
		}
		if agentID != "" {
			record.AgentID = agentID
		}
		if record.ThreadID == "" || record.TurnID == "" {
			record.ThreadID, record.TurnID = splitRootTurnID(rootTurnID)
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE requests SET root_turn_id=?,parent_request_id=?,record_json=? WHERE request_id=?`, record.RootTurnID, record.ParentRequestID, string(encoded), requestID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE observation_members SET root_turn_id=? WHERE request_id=?`, rootTurnID, requestID); err != nil {
			return err
		}
		if err := refreshObservationsForRequest(ctx, tx, requestID); err != nil {
			return err
		}
	}
	if _, err := bumpRevision(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=value+1 WHERE key='calibration_revision'`); err != nil {
		return err
	}
	if err := trimRoots(ctx, tx, m.options.MaxRootTurns); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) setParentThread(ctx context.Context, requestID, parentThreadID string) error {
	if requestID == "" || parentThreadID == "" {
		return errors.New("request id and parent thread id are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT record_json FROM requests WHERE request_id=?`, requestID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO pending_relations(request_id,parent_thread_id,updated_ns) VALUES(?,?,?) ON CONFLICT(request_id) DO UPDATE SET parent_thread_id=excluded.parent_thread_id,updated_ns=excluded.updated_ns`, requestID, parentThreadID, m.options.Now().UnixNano())
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		var record Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return err
		}
		record.ParentThreadID = parentThreadID
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE requests SET record_json=? WHERE request_id=?`, string(encoded), requestID); err != nil {
			return err
		}
	}
	if _, err := bumpRevision(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) observeQuota(ctx context.Context, observation QuotaObservation) error {
	if observation.ID == "" {
		id, err := newID()
		if err != nil {
			return err
		}
		observation.ID = id
	}
	if observation.Bucket != BucketShort && observation.Bucket != BucketWeekly {
		return errors.New("quota bucket must be short or weekly")
	}
	if observation.AccountID == "" {
		observation.AccountID = ""
	}
	if observation.RequestIDs == nil {
		observation.RequestIDs = []string{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM observations WHERE id=?`, ""); err != nil {
		return err
	}
	var saved ObservationView
	var existingJSON string
	err = tx.QueryRowContext(ctx, `SELECT observation_json FROM observations WHERE id=?`, observation.ID).Scan(&existingJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	extending := err == nil
	if extending {
		if err = json.Unmarshal([]byte(existingJSON), &saved); err != nil {
			return err
		}
		var changed bool
		observation, changed, err = prepareObservationExtension(saved, observation)
		if err != nil {
			return err
		}
		if !changed {
			return tx.Commit()
		}
	}
	view := buildObservationView(observation)
	view.RootTurnIDs = append([]string{}, saved.RootTurnIDs...)
	epoch := quotaEpochKey(observation.Before)
	beforeNs, afterNs := nanos(observation.Before.ObservedAt), nanos(observation.After.ObservedAt)
	if beforeNs > 0 && afterNs > beforeNs && observation.AccountID != "" {
		var overlaps int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM observations WHERE id<>? AND account_id=? AND bucket=? AND epoch_key=? AND before_ns < ? AND after_ns > ?`, observation.ID, observation.AccountID, observation.Bucket, epoch, afterNs, beforeNs).Scan(&overlaps); err != nil {
			return err
		}
		if overlaps > 0 {
			view.EligibilityReason = "overlapping-quota-interval"
		}
	}
	revision, err := bumpRevision(ctx, tx)
	if err != nil {
		return err
	}
	view.Revision = revision
	encoded, err := json.Marshal(view)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO observations(id,account_id,bucket,epoch_key,before_ns,after_ns,observation_json,revision) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET after_ns=excluded.after_ns,observation_json=excluded.observation_json,revision=excluded.revision`, observation.ID, observation.AccountID, observation.Bucket, epoch, beforeNs, afterNs, string(encoded), revision); err != nil {
		return err
	}
	// Insert the observation before its members: foreign keys are enforced.
	for _, id := range observation.RequestIDs {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO observation_members(observation_id,request_id,root_turn_id) SELECT ?,request_id,root_turn_id FROM requests WHERE request_id=?`, observation.ID, id); err != nil {
			return err
		}
	}
	if extending && len(saved.RequestIDs) != len(observation.RequestIDs) {
		// A previous attestation did not cover the newly added work. All
		// contributing roots must review the expanded interval explicitly.
		rows, queryErr := tx.QueryContext(ctx, `SELECT DISTINCT root_turn_id FROM observation_members WHERE observation_id=?`, observation.ID)
		if queryErr != nil {
			return queryErr
		}
		var roots []string
		for rows.Next() {
			var root string
			if err = rows.Scan(&root); err != nil {
				rows.Close()
				return err
			}
			roots = append(roots, root)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, root := range roots {
			if err = invalidateRootAttestation(ctx, tx, root); err != nil {
				return err
			}
		}
	}
	view, err = refreshObservationView(ctx, tx, view)
	if err != nil {
		return err
	}
	encoded, err = json.Marshal(view)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE observations SET observation_json=? WHERE id=?`, string(encoded), observation.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE meta SET value=value+1 WHERE key='calibration_revision'`); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) setCalibration(ctx context.Context, rootTurnID string, enabled bool) error {
	var rev uint64
	if err := m.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='revision'`).Scan(&rev); err != nil {
		return err
	}
	return m.setCalibrationRevision(ctx, rootTurnID, enabled, rev)
}

func (m *Manager) setCalibrationRevision(ctx context.Context, rootTurnID string, enabled bool, expectedRevision uint64) error {
	return m.setCalibrationBulk(ctx, []string{rootTurnID}, enabled, expectedRevision)
}

func (m *Manager) setCalibrationBulk(ctx context.Context, rootTurnIDs []string, enabled bool, expectedRevision uint64) error {
	if len(rootTurnIDs) == 0 {
		return errors.New("at least one root turn id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='revision'`).Scan(&current); err != nil {
		return err
	}
	if uint64(current) != expectedRevision {
		return ErrRevisionConflict
	}
	seen := map[string]struct{}{}
	changed := false
	for _, rootID := range rootTurnIDs {
		if rootID == "" {
			return errors.New("root turn id is required")
		}
		if _, ok := seen[rootID]; ok {
			continue
		}
		seen[rootID] = struct{}{}
		var old int
		if err := tx.QueryRowContext(ctx, `SELECT calibration_enabled FROM root_turns WHERE root_turn_id=?`, rootID).Scan(&old); err != nil {
			return err
		}
		if old != boolInt(enabled) {
			if _, err := tx.ExecContext(ctx, `UPDATE root_turns SET calibration_enabled=?,updated_ns=? WHERE root_turn_id=?`, boolInt(enabled), m.options.Now().UnixNano(), rootID); err != nil {
				return err
			}
			changed = true
		}
		// Keep missing diagnostics visible rather than silently creating a turn
		// that has no recorded request or native relation.
	}
	if _, err := bumpRevision(ctx, tx); err != nil {
		return err
	}
	if changed {
		if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=value+1 WHERE key='calibration_revision'`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func invalidateRootAttestation(ctx context.Context, tx *sql.Tx, rootID string) error {
	if rootID == "" {
		return nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE root_turns SET calibration_enabled=0 WHERE root_turn_id=? AND calibration_enabled=1`, rootID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE meta SET value=value+1 WHERE key='calibration_revision'`)
	}
	return err
}

func ensureRoot(ctx context.Context, tx *sql.Tx, rootID, threadID, turnID string, at time.Time) error {
	if rootID == "" {
		return nil
	}
	// The canonical root key is threadId:turnId. Child request thread/turn
	// values must never replace the root identity in the UI index.
	if parsedThread, parsedTurn := splitRootTurnID(rootID); parsedThread != "" && parsedTurn != "" {
		threadID, turnID = parsedThread, parsedTurn
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO root_turns(root_turn_id,thread_id,turn_id,created_ns,updated_ns) VALUES(?,?,?,?,?) ON CONFLICT(root_turn_id) DO UPDATE SET thread_id=CASE WHEN excluded.thread_id='' THEN root_turns.thread_id ELSE excluded.thread_id END,turn_id=CASE WHEN excluded.turn_id='' THEN root_turns.turn_id ELSE excluded.turn_id END,updated_ns=excluded.updated_ns`, rootID, threadID, turnID, at.UnixNano(), at.UnixNano())
	return err
}

type relation struct{ rootID, parentID, parentThreadID, agentID string }

func pendingRelation(ctx context.Context, tx *sql.Tx, requestID string) (relation, error) {
	var r relation
	err := tx.QueryRowContext(ctx, `SELECT root_turn_id,parent_request_id,parent_thread_id,agent_id FROM pending_relations WHERE request_id=?`, requestID).Scan(&r.rootID, &r.parentID, &r.parentThreadID, &r.agentID)
	return r, err
}

func trimRoots(ctx context.Context, tx *sql.Tx, max int) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM root_turns r WHERE completed=1 AND NOT EXISTS (SELECT 1 FROM requests q WHERE q.root_turn_id=r.root_turn_id AND q.finished_ns=0)`).Scan(&count); err != nil {
		return err
	}
	if count <= max {
		return nil
	}
	limit := count - max
	rows, err := tx.QueryContext(ctx, `SELECT root_turn_id FROM root_turns r WHERE completed=1 AND NOT EXISTS (SELECT 1 FROM requests q WHERE q.root_turn_id=r.root_turn_id AND q.finished_ns=0) ORDER BY seq ASC LIMIT ?`, limit)
	if err != nil {
		return err
	}
	var evict []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		evict = append(evict, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	affectedSamples := false
	for _, rootID := range evict {
		var linked int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM observation_members WHERE root_turn_id=?`, rootID).Scan(&linked); err != nil {
			return err
		}
		if linked > 0 {
			affectedSamples = true
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM requests WHERE root_turn_id=?`, rootID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM root_turns WHERE root_turn_id=?`, rootID); err != nil {
			return err
		}
	}
	// Preserve the original shared observation JSON/raw quota evidence while
	// at least one member root remains. The missing request makes it unusable
	// for fitting and will be reported as member-history-evicted.
	if _, err := tx.ExecContext(ctx, `DELETE FROM observations WHERE NOT EXISTS (SELECT 1 FROM observation_members m WHERE m.observation_id=observations.id)`); err != nil {
		return err
	}
	if affectedSamples {
		if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=value+1 WHERE key='calibration_revision'`); err != nil {
			return err
		}
	}
	return nil
}

func mergeRecord(dst *Record, src Record, requestID string) {
	dst.RequestID = requestID
	mergeString := func(target *string, value string) {
		if value != "" {
			*target = value
		}
	}
	mergeString(&dst.RootTurnID, src.RootTurnID)
	mergeString(&dst.ParentRequestID, src.ParentRequestID)
	mergeString(&dst.AgentID, src.AgentID)
	mergeString(&dst.AccountID, src.AccountID)
	mergeString(&dst.AccountPlan, src.AccountPlan)
	mergeString(&dst.ModelRequested, src.ModelRequested)
	mergeString(&dst.ModelServed, src.ModelServed)
	mergeString(&dst.TierRequested, src.TierRequested)
	mergeString(&dst.TierServed, src.TierServed)
	mergeString(&dst.ReasoningEffort, src.ReasoningEffort)
	mergeString(&dst.ThreadID, src.ThreadID)
	mergeString(&dst.ParentThreadID, src.ParentThreadID)
	mergeString(&dst.TurnID, src.TurnID)
	mergeString(&dst.Outcome, src.Outcome)
	if src.Subagent {
		dst.Subagent = true
	}
	if !src.StartedAt.IsZero() {
		dst.StartedAt = src.StartedAt
	}
	if !src.FinishedAt.IsZero() {
		dst.FinishedAt = src.FinishedAt
	}
	if src.Usage != nil {
		dst.Usage = src.Usage
	}
	if src.Fast != nil {
		dst.Fast = src.Fast
	}
}

func splitRootTurnID(rootID string) (string, string) {
	i := strings.LastIndex(rootID, ":")
	if i <= 0 || i == len(rootID)-1 {
		return "", ""
	}
	return rootID[:i], rootID[i+1:]
}

func newID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func nanos(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixNano()
}
func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func first(current, fallback string) string {
	if current != "" {
		return current
	}
	return fallback
}
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
