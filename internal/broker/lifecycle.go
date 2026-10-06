package broker

import (
	"encoding/json"
	"strings"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
)

// BackendUnavailable settles activity only after the caller has confirmed a
// native backend process stopped. matches must identify that backend's threads;
// a transient account error or quota snapshot failure is not sufficient proof.
// The predicate runs under the Hub lock and must not reenter the Hub.
//
// Desktop connections remain open because they may own healthy turns on other
// backends. Dead turns get an explicit native error, never a fabricated success
// or turn/completed. Pending replies from the stopped process are discarded.
func (h *Hub) BackendUnavailable(matches func(threadID string) bool) {
	if matches == nil {
		return
	}
	type delivery struct {
		client  *Client
		message protocol.Message
	}
	var deliveries []delivery
	var failures []protocol.Message
	h.mu.Lock()
	matched := map[string]bool{}
	checked := map[string]bool{}
	affected := func(threadID string) bool {
		if !identity(threadID) {
			return false
		}
		if !checked[threadID] {
			checked[threadID] = true
			matched[threadID] = matches(threadID)
		}
		return matched[threadID]
	}
	for threadID, turns := range h.nativeActive {
		if !affected(threadID) {
			continue
		}
		for turnID := range turns {
			key := threadID + ":" + turnID
			h.rememberUnavailableTurnLocked(key)
			params, _ := json.Marshal(map[string]any{
				"threadId": threadID, "turnId": turnID, "willRetry": false,
				"error": map[string]any{"message": "Codex app-server stopped before this turn finished. Resume the chat to continue.", "codexErrorInfo": "internalServerError"},
			})
			message := protocol.Message{Method: "error", Params: params}
			for _, client := range h.clients {
				if client.initialized {
					deliveries = append(deliveries, delivery{client, message})
				}
			}
		}
		delete(h.nativeActive, threadID)
	}
	for threadID := range h.leases {
		if affected(threadID) {
			delete(h.leases, threadID)
		}
	}
	for key := range h.turnOwners {
		threadID, _, _ := strings.Cut(key, ":")
		if affected(threadID) {
			h.rememberUnavailableTurnLocked(key)
			delete(h.turnOwners, key)
		}
	}
	for key, pending := range h.pending {
		if pending.initialize || !affected(pending.threadID) {
			continue
		}
		delete(h.pending, key)
		delete(pending.client.pendingOriginal, protocol.RequestIDKey(pending.original))
		deliveries = append(deliveries, delivery{pending.client, protocol.Failure(pending.original, ErrUnavailable, "app-server stopped before replying to this request")})
	}
	for key, approval := range h.approvals {
		if !affected(approval.threadID) {
			continue
		}
		delete(h.approvals, key)
		if !approval.responded {
			failures = append(failures, protocol.Failure(approval.original, ErrUnavailable, "app-server stopped while this approval was pending"))
		}
		params, _ := json.Marshal(map[string]any{"requestId": json.RawMessage(key), "threadId": approval.threadID})
		deliveries = append(deliveries, delivery{approval.client, protocol.Message{Method: "serverRequest/resolved", Params: params}})
	}
	for key, items := range h.pendingItems {
		threadID, _, _ := strings.Cut(key, ":")
		if affected(threadID) {
			h.pendingItemCount -= len(items)
			delete(h.pendingItems, key)
		}
	}
	for threadID, parent := range h.parents {
		if affected(threadID) || affected(parent.threadID) {
			delete(h.parents, threadID)
		}
	}
	for key, parent := range h.turnParents {
		threadID, _, _ := strings.Cut(key, ":")
		if affected(threadID) || affected(parent.threadID) {
			delete(h.turnParents, key)
		}
	}
	for threadID := range h.knownThreads {
		if affected(threadID) {
			delete(h.knownThreads, threadID)
		}
	}
	handler := h.handler
	h.mu.Unlock()
	for _, item := range deliveries {
		item.client.emit(item.message)
	}
	if handler != nil {
		for _, failure := range failures {
			handler(failure)
		}
	}
}

func (h *Hub) rememberUnavailableTurnLocked(key string) {
	if len(h.completedTurns) < maxTrackedTurns || h.completedTurns[key] {
		h.completedTurns[key] = true
		h.unavailableTurns[key] = true
	}
}

func (h *Hub) unavailableNotificationLocked(message protocol.Message) bool {
	if len(h.unavailableTurns) == 0 {
		return false
	}
	var note nativeNotification
	if json.Unmarshal(message.Params, &note) != nil {
		return false
	}
	turnID := note.TurnID
	if turnID == "" {
		turnID = note.Turn.ID
	}
	return h.unavailableTurns[note.ThreadID+":"+turnID]
}
