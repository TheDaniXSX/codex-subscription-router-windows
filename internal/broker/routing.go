package broker

import (
	"encoding/json"
	"strings"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
)

type threadLease struct {
	owner       *Client
	pending     int
	spawnActive bool
	parent      parentEdge
}

type parentEdge struct {
	threadID  string
	turnID    string
	ambiguous bool
}

type nativeItem struct {
	ID                string   `json:"id"`
	Type              string   `json:"type"`
	Kind              string   `json:"kind"`
	Tool              string   `json:"tool"`
	AgentThreadID     string   `json:"agentThreadId"`
	SenderThreadID    string   `json:"senderThreadId"`
	ReceiverThreadIDs []string `json:"receiverThreadIds"`
}

type nativeTurn struct {
	ID     string       `json:"id"`
	Status string       `json:"status"`
	Items  []nativeItem `json:"items"`
}

type nativeThread struct {
	ID             string          `json:"id"`
	ParentThreadID string          `json:"parentThreadId"`
	Source         json.RawMessage `json:"source"`
	Turns          []nativeTurn    `json:"turns"`
}

type nativeNotification struct {
	ThreadID string       `json:"threadId"`
	TurnID   string       `json:"turnId"`
	Thread   nativeThread `json:"thread"`
	Turn     nativeTurn   `json:"turn"`
	Item     nativeItem   `json:"item"`
}

func identitiesFromParams(params json.RawMessage) (string, string) {
	if len(params) > maxMessageBytes {
		return "", ""
	}
	var fields struct {
		ThreadID       string `json:"threadId"`
		LegacyThreadID string `json:"thread_id"`
		ConversationID string `json:"conversationId"`
		TurnID         string `json:"turnId"`
		LegacyTurnID   string `json:"turn_id"`
	}
	if json.Unmarshal(params, &fields) != nil {
		return "", ""
	}
	if fields.ThreadID == "" {
		fields.ThreadID = fields.LegacyThreadID
	}
	if fields.ThreadID == "" {
		fields.ThreadID = fields.ConversationID
	}
	if fields.TurnID == "" {
		fields.TurnID = fields.LegacyTurnID
	}
	if !identity(fields.ThreadID) || (fields.TurnID != "" && !identity(fields.TurnID)) {
		return "", ""
	}
	return fields.ThreadID, fields.TurnID
}

func threadFromParams(params json.RawMessage) string {
	thread, _ := identitiesFromParams(params)
	return thread
}

func (h *Hub) ownerLocked(threadID, turnID string) *Client {
	if !identity(threadID) {
		return nil
	}
	if turnID != "" {
		if !h.nativeActive[threadID][turnID] {
			return nil
		}
		return h.turnOwners[threadID+":"+turnID]
	}
	if lease := h.leases[threadID]; lease != nil {
		return lease.owner
	}
	return nil
}

func (h *Hub) resultLocked(request pendingRequest, response protocol.Message) {
	if request.method == "turn/start" {
		lease := h.leases[request.threadID]
		if lease != nil && lease.owner == request.client && lease.pending > 0 {
			lease.pending--
		}
		if response.Error == nil {
			var result struct {
				Turn nativeTurn `json:"turn"`
			}
			if json.Unmarshal(response.Result, &result) == nil && identity(result.Turn.ID) {
				h.bindTurnOwnerLocked(request.threadID, result.Turn.ID, request.client)
				if terminalStatus(result.Turn.Status) {
					h.turnCompletedLocked(request.threadID, result.Turn.ID)
				} else {
					h.turnStartedLocked(request.threadID, result.Turn.ID, request.client)
				}
			}
		}
		h.releaseIdleLeaseLocked(request.threadID)
	}
	if response.Error == nil && (request.method == "thread/start" || request.method == "thread/resume" || request.method == "thread/fork" || request.method == "thread/read") {
		var result struct {
			Thread nativeThread `json:"thread"`
		}
		if json.Unmarshal(response.Result, &result) == nil {
			// Opening or reading a chat does not acquire execution ownership.
			h.rememberThreadLocked(result.Thread)
		}
	}
}

func (h *Hub) notificationLocked(message protocol.Message) {
	switch message.Method {
	case "thread/started", "turn/started", "turn/completed", "item/started", "item/completed":
	default:
		return
	}
	var note nativeNotification
	if json.Unmarshal(message.Params, &note) != nil {
		return
	}
	if message.Method == "thread/started" {
		h.rememberThreadLocked(note.Thread)
		return
	}
	if !identity(note.ThreadID) {
		return
	}
	if note.TurnID == "" {
		note.TurnID = note.Turn.ID
	}
	if !identity(note.TurnID) {
		return
	}
	if message.Method == "turn/started" {
		h.turnStartedLocked(note.ThreadID, note.TurnID, nil)
	}
	h.itemLocked(note.ThreadID, note.TurnID, note.Item)
	for _, item := range note.Turn.Items {
		h.itemLocked(note.ThreadID, note.TurnID, item)
	}
	if message.Method == "turn/completed" {
		h.turnCompletedLocked(note.ThreadID, note.TurnID)
	}
}

func (h *Hub) rememberThreadLocked(thread nativeThread) {
	if !identity(thread.ID) {
		return
	}
	if len(h.knownThreads) < maxTrackedTurns {
		h.knownThreads[thread.ID] = true
	}
	parent := thread.ParentThreadID
	if parent == "" {
		var source struct {
			SubAgent struct {
				Spawn struct {
					Parent string `json:"parent_thread_id"`
				} `json:"thread_spawn"`
			} `json:"subAgent"`
		}
		if json.Unmarshal(thread.Source, &source) == nil {
			parent = source.SubAgent.Spawn.Parent
		}
	}
	if identity(parent) && parent != thread.ID {
		edge, known := h.parents[thread.ID]
		if !known && len(h.parents) < maxTrackedTurns {
			h.parents[thread.ID] = parentEdge{threadID: parent}
		}
		if known && edge.threadID != parent {
			edge.ambiguous = true
			h.parents[thread.ID] = edge
		}
	}
	// Resumed history is deliberately not replayed as live activity. Native
	// turn notifications establish activity; source alone proves no turn owner.
}

func (h *Hub) turnStartedLocked(threadID, turnID string, explicitOwner *Client) {
	key := threadID + ":" + turnID
	if h.completedTurns[key] {
		if explicitOwner != nil {
			h.bindTurnOwnerLocked(threadID, turnID, explicitOwner)
		}
		return
	}
	if h.nativeActive[threadID] == nil {
		if len(h.nativeActive) >= maxTrackedTurns {
			return
		}
		h.nativeActive[threadID] = map[string]bool{}
	}
	h.nativeActive[threadID][turnID] = true
	owner := explicitOwner
	var parent parentEdge
	if owner == nil {
		if lease := h.leases[threadID]; lease != nil {
			owner = lease.owner
			parent = lease.parent
		}
	}
	if owner == nil {
		edge := h.parents[threadID]
		if !edge.ambiguous && edge.turnID != "" {
			owner = h.turnOwners[edge.threadID+":"+edge.turnID]
			parent = edge
		}
	}
	if owner != nil {
		if parent.turnID != "" && h.turnOwners[key] == nil {
			h.turnParents[key] = parent
		}
		if !h.bindTurnOwnerLocked(threadID, turnID, owner) {
			return
		}
		lease := h.leases[threadID]
		if lease == nil {
			lease = &threadLease{owner: owner, parent: parent}
			h.leases[threadID] = lease
		}
		if lease.owner == owner {
			lease.spawnActive = false
		}
	}
}

func (h *Hub) bindTurnOwnerLocked(threadID, turnID string, owner *Client) bool {
	key := threadID + ":" + turnID
	if previous := h.turnOwners[key]; previous != nil && previous != owner {
		return false
	}
	if lease := h.leases[threadID]; lease != nil && lease.owner != owner {
		return false
	}
	if h.turnOwners[key] == nil && len(h.turnOwners) >= maxTrackedTurns {
		return false
	}
	h.turnOwners[key] = owner
	items := h.pendingItems[key]
	delete(h.pendingItems, key)
	h.pendingItemCount -= len(items)
	for _, item := range items {
		h.applyItemLocked(threadID, turnID, item)
	}
	return true
}

func (h *Hub) turnCompletedLocked(threadID, turnID string) {
	key := threadID + ":" + turnID
	wasActive := h.nativeActive[threadID][turnID]
	if len(h.completedTurns) < maxTrackedTurns {
		h.completedTurns[key] = true
	}
	delete(h.nativeActive[threadID], turnID)
	if len(h.nativeActive[threadID]) == 0 {
		delete(h.nativeActive, threadID)
	}
	if lease := h.leases[threadID]; lease != nil && wasActive {
		lease.spawnActive = false
	}
	for key, approval := range h.approvals {
		if approval.threadID == threadID && approval.turnID == turnID {
			delete(h.approvals, key)
		}
	}
	h.releaseIdleLeaseLocked(threadID)
}

func (h *Hub) releaseIdleLeaseLocked(threadID string) {
	lease := h.leases[threadID]
	if lease != nil && lease.pending == 0 && !lease.spawnActive && len(h.nativeActive[threadID]) == 0 {
		delete(h.leases, threadID)
	}
}

func (h *Hub) itemLocked(threadID, turnID string, item nativeItem) {
	if item.Type != "subAgentActivity" && item.Type != "collabAgentToolCall" {
		return
	}
	if item.Type == "subAgentActivity" && item.Kind != "started" && item.Kind != "interacted" && item.Kind != "completed" && item.Kind != "interrupted" {
		return
	}
	if item.Type == "collabAgentToolCall" && (item.SenderThreadID != threadID || (item.Tool != "spawnAgent" && item.Tool != "sendInput" && item.Tool != "resumeAgent")) {
		return
	}
	if !identity(item.ID) {
		return
	}
	children := item.ReceiverThreadIDs
	if item.Type == "subAgentActivity" {
		children = []string{item.AgentThreadID}
	}
	if len(children) == 0 {
		return
	}
	key := threadID + ":" + turnID + ":" + item.ID + ":" + item.Type + ":" + item.Kind + ":" + item.Tool + ":" + strings.Join(children, ":")
	if h.seenItems[key] || len(h.seenItems) >= maxTrackedTurns {
		return
	}
	if h.turnOwners[threadID+":"+turnID] == nil {
		if h.pendingItemCount >= maxTrackedTurns {
			return
		}
		h.seenItems[key] = true
		h.pendingItems[threadID+":"+turnID] = append(h.pendingItems[threadID+":"+turnID], item)
		h.pendingItemCount++
		return
	}
	h.seenItems[key] = true
	h.applyItemLocked(threadID, turnID, item)
}

func (h *Hub) applyItemLocked(threadID, turnID string, item nativeItem) {
	children := item.ReceiverThreadIDs
	if item.Type == "subAgentActivity" {
		children = []string{item.AgentThreadID}
	}
	for _, child := range children {
		if !identity(child) || child == threadID {
			continue
		}
		if item.Kind == "completed" || item.Kind == "interrupted" {
			// Only a proved parent can close a child's activity hint.
			edge := h.parents[child]
			if edge.threadID == threadID && edge.turnID == turnID {
				for childTurn := range h.nativeActive[child] {
					if h.turnParents[child+":"+childTurn] == edge {
						h.turnCompletedLocked(child, childTurn)
					}
				}
				if lease := h.leases[child]; lease != nil && lease.parent == edge {
					lease.spawnActive = false
				}
				h.releaseIdleLeaseLocked(child)
			}
			continue
		}
		owner := h.turnOwners[threadID+":"+turnID]
		if owner == nil {
			continue
		}
		edge, known := h.parents[child]
		creation := item.Kind == "started" || item.Tool == "spawnAgent"
		if known && creation && edge.turnID != "" && (edge.threadID != threadID || edge.turnID != turnID) {
			edge.ambiguous = true
			h.parents[child] = edge
			continue
		}
		if !known && len(h.parents) >= maxTrackedTurns {
			continue
		}
		lease := h.leases[child]
		if lease != nil && lease.owner != owner {
			continue
		} // Never transfer live work.
		if known && edge.ambiguous {
			continue
		}
		h.parents[child] = parentEdge{threadID: threadID, turnID: turnID}
		if lease == nil {
			lease = &threadLease{owner: owner, spawnActive: true, parent: h.parents[child]}
			h.leases[child] = lease
		}
		for activeTurn := range h.nativeActive[child] {
			if h.turnOwners[child+":"+activeTurn] == nil {
				h.turnParents[child+":"+activeTurn] = h.parents[child]
			}
			h.bindTurnOwnerLocked(child, activeTurn, owner)
			lease.spawnActive = false
		}
		if creation && len(h.nativeActive[child]) == 0 {
			for key := range h.completedTurns {
				if strings.HasPrefix(key, child+":") {
					// Explicit creation can arrive after the new child's own
					// completion. Bind that finished turn to resolve any still
					// running grandchildren recorded before their parent.
					if h.turnOwners[key] == nil {
						h.turnParents[key] = h.parents[child]
						h.bindTurnOwnerLocked(child, strings.TrimPrefix(key, child+":"), owner)
					}
					lease.spawnActive = false
				}
			}
		}
		h.releaseIdleLeaseLocked(child)
	}
}

func terminalStatus(status string) bool {
	return status == "completed" || status == "interrupted" || status == "failed"
}
