package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/backend"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/usage"
)

// These are correlation bounds, not history expiry. The authoritative usage
// history stays in the ledger. Reaching a bound leaves new links unassociated.
const usageRelationLimit = 100000
const usageRelationsMaxBytes = 48 << 20

type usageThreadRelation struct {
	ParentThreadID string `json:"parentThreadId,omitempty"`
	ParentTurnID   string `json:"parentTurnId,omitempty"`
	Subagent       bool   `json:"subagent,omitempty"`
	Root           bool   `json:"root,omitempty"`
	Ambiguous      bool   `json:"ambiguous,omitempty"`
	Active         bool   `json:"active,omitempty"`
}

type usageRelationRecord struct {
	RequestID       string `json:"requestId"`
	ThreadID        string `json:"threadId,omitempty"`
	TurnID          string `json:"turnId,omitempty"`
	ParentThreadID  string `json:"parentThreadId,omitempty"`
	Subagent        bool   `json:"subagent,omitempty"`
	RootTurnID      string `json:"rootTurnId,omitempty"`
	ParentRequestID string `json:"parentRequestId,omitempty"`
	AgentID         string `json:"agentId,omitempty"`
	Finished        bool   `json:"finished,omitempty"`
	Linked          bool   `json:"linked,omitempty"`
}

type usageRelationsState struct {
	mu      sync.Mutex
	path    string
	Threads map[string]usageThreadRelation `json:"threads"`
	// A resolved child turn is immutable even if its agent is reused later.
	Turns           map[string]string              `json:"turns"`
	Pending         map[string]usageRelationRecord `json:"pending"`
	RootCompletions map[string]bool                `json:"rootCompletions"`
	// Native turn terminal states are immutable. Keeping them independently
	// of parent links handles completion-before-spawn and replay safely.
	TurnActivity   map[string]bool `json:"turnActivity"`
	SeenActivities map[string]bool `json:"seenActivities"`
	UserRoots      map[string]bool `json:"userRoots"`
}

func newUsageRelationsState(path string) (*usageRelationsState, error) {
	s := &usageRelationsState{path: path, Threads: map[string]usageThreadRelation{}, Turns: map[string]string{}, Pending: map[string]usageRelationRecord{}, RootCompletions: map[string]bool{}, TurnActivity: map[string]bool{}, SeenActivities: map[string]bool{}, UserRoots: map[string]bool{}}
	if path == "" {
		return s, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Size() > usageRelationsMaxBytes {
		return s, errors.New("usage relations file exceeds size bound or is unreadable")
	}
	var disk struct {
		Threads         map[string]usageThreadRelation `json:"threads"`
		Turns           map[string]string              `json:"turns"`
		Pending         map[string]usageRelationRecord `json:"pending"`
		RootCompletions map[string]bool                `json:"rootCompletions"`
		TurnActivity    map[string]bool                `json:"turnActivity"`
		SeenActivities  map[string]bool                `json:"seenActivities"`
		UserRoots       map[string]bool                `json:"userRoots"`
	}
	dec := json.NewDecoder(io.LimitReader(f, usageRelationsMaxBytes+1))
	if err := dec.Decode(&disk); err != nil {
		return s, fmt.Errorf("read usage relations: %w", err)
	}
	if dec.Decode(new(any)) != io.EOF || len(disk.Threads) > usageRelationLimit || len(disk.Turns) > usageRelationLimit || len(disk.Pending) > usageRelationLimit || len(disk.RootCompletions) > usageRelationLimit || len(disk.TurnActivity) > usageRelationLimit || len(disk.SeenActivities) > usageRelationLimit || len(disk.UserRoots) > usageRelationLimit {
		return s, errors.New("usage relations exceed bounds or have trailing data")
	}
	for id, edge := range disk.Threads {
		if !usageIdentity(id) || (edge.ParentThreadID != "" && !usageIdentity(edge.ParentThreadID)) || (edge.ParentTurnID != "" && !usageIdentity(edge.ParentTurnID)) {
			return s, errors.New("invalid usage thread relation")
		}
	}
	for id, root := range disk.Turns {
		if !usageRootIdentity(id) || !usageRootIdentity(root) {
			return s, errors.New("invalid usage turn relation")
		}
	}
	for id, record := range disk.Pending {
		if !usageIdentity(id) || id != record.RequestID || !record.valid() {
			return s, errors.New("invalid pending usage relation")
		}
	}
	for root := range disk.RootCompletions {
		if !usageRootIdentity(root) {
			return s, errors.New("invalid usage root completion")
		}
	}
	for key := range disk.TurnActivity {
		if !usageRootIdentity(key) {
			return s, errors.New("invalid usage turn activity")
		}
	}
	for key := range disk.UserRoots {
		if !usageRootIdentity(key) {
			return s, errors.New("invalid usage client turn root")
		}
	}
	for key := range disk.SeenActivities {
		parts := strings.Split(key, ":")
		if len(parts) != 5 {
			return s, errors.New("invalid usage activity identity")
		}
		for _, part := range parts {
			if !usageIdentity(part) {
				return s, errors.New("invalid usage activity identity")
			}
		}
	}
	if disk.Threads != nil {
		s.Threads = disk.Threads
	}
	if disk.Turns != nil {
		s.Turns = disk.Turns
	}
	if disk.Pending != nil {
		s.Pending = disk.Pending
	}
	if disk.RootCompletions != nil {
		s.RootCompletions = disk.RootCompletions
	}
	if disk.TurnActivity != nil {
		s.TurnActivity = disk.TurnActivity
	}
	if disk.SeenActivities != nil {
		s.SeenActivities = disk.SeenActivities
	}
	if disk.UserRoots != nil {
		s.UserRoots = disk.UserRoots
	}
	return s, nil
}

// Only identifiers are serialized. Native items also contain prompts, output,
// and model text; their wire structures below deliberately do not expose them.
func (s *usageRelationsState) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(data) > usageRelationsMaxBytes {
		return errors.New("usage relations exceed file bound")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".usage-relations-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func usageIdentity(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if c < 0x21 || c > 0x7e || strings.ContainsRune(":/\\", c) {
			return false
		}
	}
	return true
}

func usageRootIdentity(id string) bool {
	thread, turn, ok := strings.Cut(id, ":")
	return ok && usageIdentity(thread) && usageIdentity(turn)
}

func (r usageRelationRecord) valid() bool {
	if !usageIdentity(r.RequestID) {
		return false
	}
	for _, id := range []string{r.ThreadID, r.TurnID, r.ParentThreadID, r.ParentRequestID, r.AgentID} {
		if id != "" && !usageIdentity(id) {
			return false
		}
	}
	return r.RootTurnID == "" || usageRootIdentity(r.RootTurnID)
}

func (s *usageRelationsState) resolveLocked(threadID, turnID string, subagent bool, parentThreadID string, seen map[string]bool) string {
	if !usageIdentity(threadID) || !usageIdentity(turnID) {
		return ""
	}
	key := threadID + ":" + turnID
	if s.UserRoots[key] {
		return key
	}
	if seen[key] || len(seen) >= 256 {
		return ""
	}
	seen[key] = true
	edge := s.Threads[threadID]
	if edge.Ambiguous {
		return ""
	}
	if edge.ParentThreadID != "" && parentThreadID != "" && edge.ParentThreadID != parentThreadID {
		return ""
	}
	if edge.ParentThreadID != "" && edge.ParentTurnID != "" {
		root := s.resolveLocked(edge.ParentThreadID, edge.ParentTurnID, false, "", seen)
		if pinned := s.Turns[key]; pinned != "" && pinned != root {
			return ""
		}
		if root != "" && len(s.Turns) < usageRelationLimit {
			s.Turns[key] = root
		}
		return root
	}
	if subagent || edge.Subagent || parentThreadID != "" || edge.ParentThreadID != "" || !edge.Root {
		return ""
	}
	return key
}

func (m *Multiplexer) associateUsageRecord(rec *usage.Record, parentThreadID string) {
	if rec == nil || m.usageRelations == nil {
		return
	}
	if parentThreadID == "" {
		parentThreadID = rec.ParentThreadID
	}
	r := usageRelationRecord{RequestID: rec.RequestID, ThreadID: rec.ThreadID, TurnID: rec.TurnID, ParentThreadID: parentThreadID, Subagent: rec.Subagent, RootTurnID: rec.RootTurnID, ParentRequestID: rec.ParentRequestID, AgentID: rec.AgentID, Finished: !rec.FinishedAt.IsZero()}
	if !r.valid() {
		rec.RootTurnID = ""
		return
	}
	s := m.usageRelations
	s.mu.Lock()
	defer s.mu.Unlock()
	oldTurns := len(s.Turns)
	changed := false
	// Native events can bind an in-flight request after Begin. Retain that
	// exact result until Finish consumes it, even if the agent is later reused
	// and new requests have become ambiguous.
	if prior := s.Pending[r.RequestID]; r.RootTurnID == "" && prior.RootTurnID != "" {
		r.RootTurnID = prior.RootTurnID
		if r.ParentThreadID == "" {
			r.ParentThreadID = prior.ParentThreadID
		}
		if r.ParentRequestID == "" {
			r.ParentRequestID = prior.ParentRequestID
		}
		if r.AgentID == "" {
			r.AgentID = prior.AgentID
		}
	}
	if r.ParentThreadID != "" {
		changed = s.rememberParentLocked(r.ThreadID, r.ParentThreadID, "", false)
	} else if r.Subagent && usageIdentity(r.ThreadID) {
		edge, exists := s.Threads[r.ThreadID]
		if !exists && len(s.Threads) < usageRelationLimit {
			s.Threads[r.ThreadID] = usageThreadRelation{Subagent: true, Active: true}
			changed = true
		} else if exists && !edge.Subagent {
			edge.Subagent, edge.Root, edge.Active = true, false, true
			s.Threads[r.ThreadID] = edge
			changed = true
		}
	}
	if r.RootTurnID == "" {
		r.RootTurnID = s.resolveLocked(r.ThreadID, r.TurnID, r.Subagent, r.ParentThreadID, map[string]bool{})
	}
	if r.ParentThreadID == "" {
		r.ParentThreadID = s.Threads[r.ThreadID].ParentThreadID
	}
	if r.AgentID == "" && usageIdentity(r.ThreadID) {
		r.AgentID = r.ThreadID
	}
	rec.RootTurnID, rec.AgentID, rec.ParentThreadID, rec.ParentRequestID = r.RootTurnID, r.AgentID, r.ParentThreadID, r.ParentRequestID
	changed = changed || oldTurns != len(s.Turns)
	if r.RootTurnID == "" {
		if old, ok := s.Pending[r.RequestID]; (ok || len(s.Pending) < usageRelationLimit) && (!ok || old != r) {
			s.Pending[r.RequestID] = r
			changed = true
		}
	} else if _, ok := s.Pending[r.RequestID]; ok {
		delete(s.Pending, r.RequestID)
		changed = true
	}
	if changed {
		m.usageError(s.saveLocked())
	}
}

type usageNativeItem struct {
	ID                string   `json:"id"`
	Type              string   `json:"type"`
	Tool              string   `json:"tool"`
	Kind              string   `json:"kind"`
	SenderThreadID    string   `json:"senderThreadId"`
	ReceiverThreadIDs []string `json:"receiverThreadIds"`
	AgentThreadID     string   `json:"agentThreadId"`
}

type usageNativeTurn struct {
	ID     string            `json:"id"`
	Status string            `json:"status"`
	Items  []usageNativeItem `json:"items"`
}

type usageNativeNotification struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
	Turn     usageNativeTurn `json:"turn"`
	Item     usageNativeItem `json:"item"`
	Thread   struct {
		ID             string            `json:"id"`
		ParentThreadID string            `json:"parentThreadId"`
		Source         json.RawMessage   `json:"source"`
		Turns          []usageNativeTurn `json:"turns"`
	} `json:"thread"`
}

func (s *usageRelationsState) rememberParentLocked(child, parent, turn string, invalidates bool) bool {
	if !usageIdentity(child) || !usageIdentity(parent) || child == parent || (turn != "" && !usageIdentity(turn)) {
		return false
	}
	edge, exists := s.Threads[child]
	if !exists && len(s.Threads) >= usageRelationLimit {
		return false
	}
	before := edge
	edge.Subagent = true
	if edge.Root {
		edge.Ambiguous = true
	}
	edge.Root = false
	// Replayed spawn items in the parent's completed turn must not reopen a
	// child whose own completion was already observed.
	if !exists || (edge.ParentTurnID == "" && turn != "") {
		if active, known := s.threadActivityLocked(child); known {
			edge.Active = active
		} else {
			edge.Active = true
		}
	}
	if edge.ParentThreadID != "" && edge.ParentThreadID != parent {
		edge.Ambiguous = true
	}
	if edge.ParentTurnID != "" && turn != "" && edge.ParentTurnID != turn {
		edge.Ambiguous = true
	}
	if invalidates && edge.ParentTurnID != turn {
		edge.Ambiguous = true
	}
	if edge.ParentThreadID == "" {
		edge.ParentThreadID = parent
	}
	if edge.ParentTurnID == "" && !invalidates {
		edge.ParentTurnID = turn
	}
	if edge != before || !exists {
		s.Threads[child] = edge
		return true
	}
	return false
}

func (s *usageRelationsState) itemLocked(threadID, turnID string, item usageNativeItem) bool {
	if !usageIdentity(threadID) || !usageIdentity(turnID) {
		return false
	}
	changed := false
	switch item.Type {
	case "subAgentActivity":
		if usageIdentity(item.ID) && usageIdentity(item.Kind) && usageIdentity(item.AgentThreadID) {
			key := threadID + ":" + turnID + ":" + item.ID + ":" + item.Kind + ":" + item.AgentThreadID
			if s.SeenActivities[key] {
				return false
			}
			if len(s.SeenActivities) >= usageRelationLimit {
				return false
			}
			s.SeenActivities[key] = true
			changed = true
		}
		// A completion/wait event is not proof that the agent was started by
		// this turn. started is the explicit creation relation in 26.924.
		if item.Kind == "started" || item.Kind == "interacted" {
			changed = s.rememberParentLocked(item.AgentThreadID, threadID, turnID, item.Kind == "interacted") || changed
		}
		if item.Kind == "completed" || item.Kind == "interrupted" {
			for key, active := range s.TurnActivity {
				if active && strings.HasPrefix(key, item.AgentThreadID+":") {
					s.TurnActivity[key] = false
					changed = true
				}
			}
			changed = s.markActiveLocked(item.AgentThreadID, false) || changed
		}
	case "collabAgentToolCall":
		if item.SenderThreadID != threadID {
			return false
		}
		if item.Tool != "spawnAgent" && item.Tool != "sendInput" && item.Tool != "resumeAgent" {
			return false
		}
		for _, child := range item.ReceiverThreadIDs {
			changed = s.rememberParentLocked(child, threadID, turnID, item.Tool != "spawnAgent") || changed
		}
	}
	return changed
}

func (s *usageRelationsState) markActiveLocked(threadID string, active bool) bool {
	edge, ok := s.Threads[threadID]
	if !ok || !edge.Subagent || edge.Active == active {
		return false
	}
	edge.Active = active
	s.Threads[threadID] = edge
	return true
}

func (s *usageRelationsState) threadActivityLocked(threadID string) (active, known bool) {
	prefix := threadID + ":"
	for key, running := range s.TurnActivity {
		if strings.HasPrefix(key, prefix) {
			known = true
			active = active || running
		}
	}
	return active, known
}

func (s *usageRelationsState) turnActivityLocked(threadID, turnID string, active bool) bool {
	if !usageIdentity(threadID) || !usageIdentity(turnID) {
		return false
	}
	key := threadID + ":" + turnID
	previous, known := s.TurnActivity[key]
	// A native turn identity cannot be started twice after it has ended.
	if known && !previous && active {
		return false
	}
	if !known && len(s.TurnActivity) >= usageRelationLimit {
		return false
	}
	changed := !known || previous != active
	s.TurnActivity[key] = active
	threadActive, _ := s.threadActivityLocked(threadID)
	return s.markActiveLocked(threadID, threadActive) || changed
}

// A completed root remains provisional while a known descendant is active,
// including the gap between that descendant's HTTP inference operations.
func (s *usageRelationsState) hasActiveDescendantLocked(root string) bool {
	for threadID, edge := range s.Threads {
		if !edge.Subagent || !edge.Active {
			continue
		}
		if s.activeDescendantLocked(root, threadID, edge) {
			return true
		}
	}
	return false
}

func (s *usageRelationsState) activeDescendantLocked(root, threadID string, edge usageThreadRelation) bool {
	knownActive := false
	for key, active := range s.TurnActivity {
		if !active || !strings.HasPrefix(key, threadID+":") {
			continue
		}
		knownActive = true
		_, turnID, _ := strings.Cut(key, ":")
		resolved := s.resolveLocked(threadID, turnID, edge.Subagent, "", map[string]bool{})
		if key != root && (resolved == root || (resolved == "" && s.possibleDescendantLocked(root, edge))) {
			return true
		}
	}
	// A spawned agent may not have emitted its first turn/started yet. Once
	// exact active turns exist, a user turn in that thread owns its own work.
	return !knownActive && s.possibleDescendantLocked(root, edge)
}

func (s *usageRelationsState) possibleDescendantLocked(root string, edge usageThreadRelation) bool {
	rootThread, rootTurn, _ := strings.Cut(root, ":")
	seen := map[string]bool{}
	for edge.ParentThreadID != "" && !seen[edge.ParentThreadID] && len(seen) < 256 {
		if edge.ParentThreadID == rootThread && (edge.ParentTurnID == "" || edge.ParentTurnID == rootTurn || edge.Ambiguous) {
			return true
		}
		seen[edge.ParentThreadID] = true
		edge = s.Threads[edge.ParentThreadID]
	}
	return false
}

// Pending means possible unassociated requests from this known thread tree.
// A parent-thread-only hint can conservatively appear on several root turns;
// these counts are coverage warnings and never add tokens or observed quota.
func (m *Multiplexer) usageRelationCoverage(rootID string) (pending int, activeDescendants int) {
	if m.usageRelations == nil || !usageRootIdentity(rootID) {
		return 0, 0
	}
	s := m.usageRelations
	s.mu.Lock()
	defer s.mu.Unlock()
	for threadID, edge := range s.Threads {
		if edge.Subagent && edge.Active && s.activeDescendantLocked(rootID, threadID, edge) {
			activeDescendants++
		}
	}
	for _, rec := range s.Pending {
		if rec.RootTurnID != "" {
			continue
		}
		edge := s.Threads[rec.ThreadID]
		if edge.ParentThreadID == "" {
			edge.ParentThreadID = rec.ParentThreadID
		}
		if s.possibleDescendantLocked(rootID, edge) {
			pending++
		}
	}
	return pending, activeDescendants
}

func (s *usageRelationsState) readyCompletionsLocked() []string {
	var roots []string
	for root := range s.RootCompletions {
		if !s.hasActiveDescendantLocked(root) {
			roots = append(roots, root)
		}
	}
	return roots
}

// trackUsageClientResponse proves an exact user turn through its successful
// native RPC response. The matching external route supplies the thread ID;
// a rejected turn/start therefore cannot classify a later agent turn as root.
func (m *Multiplexer) trackUsageClientResponse(request, response protocol.Message) {
	s := m.usageRelations
	if s == nil || request.Method != "turn/start" || response.Error != nil || len(request.Params) > 8<<20 || len(response.Result) > 8<<20 {
		return
	}
	var params struct {
		ThreadID string `json:"threadId"`
	}
	var result struct {
		Turn usageNativeTurn `json:"turn"`
	}
	if json.Unmarshal(request.Params, &params) != nil || json.Unmarshal(response.Result, &result) != nil || !usageIdentity(params.ThreadID) || !usageIdentity(result.Turn.ID) {
		return
	}
	key := params.ThreadID + ":" + result.Turn.ID
	s.mu.Lock()
	if !s.UserRoots[key] && len(s.UserRoots) >= usageRelationLimit {
		s.mu.Unlock()
		return
	}
	s.UserRoots[key] = true
	if active, known := s.TurnActivity[key]; known && !active && len(s.RootCompletions) < usageRelationLimit {
		s.RootCompletions[key] = true
	}
	m.usageError(s.saveLocked())
	s.mu.Unlock()
	method := "turn/started"
	if result.Turn.Status == "completed" || result.Turn.Status == "interrupted" || result.Turn.Status == "failed" {
		method = "turn/completed"
	}
	encoded, err := json.Marshal(usageNativeNotification{ThreadID: params.ThreadID, Turn: result.Turn})
	if err == nil {
		m.trackUsageNotification(backend.Inbound{Message: protocol.Message{Method: method, Params: encoded}})
	}
}

// trackUsageNotification performs no account or app-server RPC. The source
// schemas were generated from the exact official 26.924 runtime. UI-only
// parentTurnKey fallbacks based on creation time are deliberately excluded.
func (m *Multiplexer) trackUsageNotification(inbound backend.Inbound) {
	s := m.usageRelations
	if s == nil {
		return
	}
	switch inbound.Message.Method {
	case "thread/started", "turn/started", "turn/completed", "item/started", "item/completed":
	default:
		return
	}
	if len(inbound.Message.Params) > 8<<20 {
		return
	}
	var note usageNativeNotification
	if json.Unmarshal(inbound.Message.Params, &note) != nil {
		return
	}
	s.mu.Lock()
	changed := false
	if inbound.Message.Method == "thread/started" {
		thread := note.Thread
		var source struct {
			SubAgent json.RawMessage `json:"subAgent"`
		}
		_ = json.Unmarshal(thread.Source, &source)
		parent := thread.ParentThreadID
		if parent == "" && len(source.SubAgent) > 0 {
			var agent struct {
				Spawn struct {
					Parent string `json:"parent_thread_id"`
				} `json:"thread_spawn"`
			}
			_ = json.Unmarshal(source.SubAgent, &agent)
			parent = agent.Spawn.Parent
		}
		if parent != "" {
			changed = s.rememberParentLocked(thread.ID, parent, "", false)
		}
		if usageIdentity(thread.ID) && len(source.SubAgent) > 0 && string(source.SubAgent) != "null" {
			edge, exists := s.Threads[thread.ID]
			if !edge.Subagent && (exists || len(s.Threads) < usageRelationLimit) {
				edge.Subagent = true
				edge.Root = false
				s.Threads[thread.ID] = edge
				changed = true
			}
		}
		var sourceName string
		_ = json.Unmarshal(thread.Source, &sourceName)
		if usageIdentity(thread.ID) && parent == "" && (sourceName == "cli" || sourceName == "vscode" || sourceName == "exec" || sourceName == "appServer") {
			edge, exists := s.Threads[thread.ID]
			if !edge.Subagent && !edge.Root && (exists || len(s.Threads) < usageRelationLimit) {
				edge.Root = true
				s.Threads[thread.ID] = edge
				changed = true
			}
		}
		for _, turn := range thread.Turns {
			switch turn.Status {
			case "inProgress":
				changed = s.turnActivityLocked(thread.ID, turn.ID, true) || changed
			case "completed", "interrupted", "failed":
				changed = s.turnActivityLocked(thread.ID, turn.ID, false) || changed
			}
			for _, item := range turn.Items {
				changed = s.itemLocked(thread.ID, turn.ID, item) || changed
			}
		}
	} else {
		if note.TurnID == "" {
			note.TurnID = note.Turn.ID
		}
		changed = s.itemLocked(note.ThreadID, note.TurnID, note.Item)
		for _, item := range note.Turn.Items {
			changed = s.itemLocked(note.ThreadID, note.TurnID, item) || changed
		}
	}
	var resolved []usageRelationRecord
	if inbound.Message.Method == "turn/started" {
		changed = s.turnActivityLocked(note.ThreadID, note.TurnID, true) || changed
	}
	if inbound.Message.Method == "turn/completed" && usageIdentity(note.ThreadID) && usageIdentity(note.TurnID) {
		changed = s.turnActivityLocked(note.ThreadID, note.TurnID, false) || changed
		key := note.ThreadID + ":" + note.TurnID
		if s.resolveLocked(note.ThreadID, note.TurnID, false, "", map[string]bool{}) == key {
			if !s.RootCompletions[key] && len(s.RootCompletions) < usageRelationLimit {
				s.RootCompletions[key] = true
				changed = true
			}
		}
	}
	for id, rec := range s.Pending {
		if rec.Linked {
			continue
		}
		root := rec.RootTurnID
		if root == "" {
			root = s.resolveLocked(rec.ThreadID, rec.TurnID, rec.Subagent, rec.ParentThreadID, map[string]bool{})
		}
		if root == "" {
			continue
		}
		rec.RootTurnID = root
		if rec.AgentID == "" {
			rec.AgentID = rec.ThreadID
		}
		if rec.ParentThreadID == "" {
			rec.ParentThreadID = s.Threads[rec.ThreadID].ParentThreadID
		}
		s.Pending[id] = rec
		resolved = append(resolved, rec)
	}
	if changed || len(resolved) > 0 {
		m.usageError(s.saveLocked())
	}
	completedRoots := s.readyCompletionsLocked()
	s.mu.Unlock()
	if m.usageLedger == nil {
		return
	}
	var linked []string
	for _, rec := range resolved {
		if rec.ParentThreadID != "" {
			if err := m.usageLedger.SetParentThread(context.Background(), rec.RequestID, rec.ParentThreadID); err != nil {
				m.usageError(err)
				continue
			}
		}
		if err := m.usageLedger.SetRelation(context.Background(), rec.RequestID, rec.RootTurnID, rec.ParentRequestID, rec.AgentID); err != nil {
			m.usageError(err)
			continue
		}
		linked = append(linked, rec.RequestID)
	}
	if len(linked) > 0 {
		s.mu.Lock()
		for _, requestID := range linked {
			pending, exists := s.Pending[requestID]
			if !exists {
				continue
			}
			if pending.Finished {
				delete(s.Pending, requestID)
			} else {
				pending.Linked = true
				s.Pending[requestID] = pending
			}
		}
		m.usageError(s.saveLocked())
		s.mu.Unlock()
	}
	for _, root := range completedRoots {
		s.mu.Lock()
		if !s.RootCompletions[root] || s.hasActiveDescendantLocked(root) {
			s.mu.Unlock()
			continue
		}
		if err := m.usageLedger.CompleteTurn(context.Background(), root); err != nil {
			m.usageError(err)
			s.mu.Unlock()
			continue
		}
		delete(s.RootCompletions, root)
		m.usageError(s.saveLocked())
		s.mu.Unlock()
	}
}
