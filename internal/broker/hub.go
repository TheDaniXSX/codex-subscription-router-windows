// Package broker shares one native Multiplexer among desktop connections.
// It owns no account files, credentials, network listeners, or processes.
package broker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
)

const (
	ErrUnavailable                = -32080
	ErrIncompatibleInitialization = -32081
	ErrThreadBusy                 = -32082
	ErrApprovalOwnerGone          = -32083
	ErrUnknownApprovalOwner       = -32084
	ErrInvalidClientMessage       = -32085
	maxMessageBytes               = 64 << 20
	maxTrackedTurns               = 100000
	clientQueueSize               = 256
)

type Activity struct {
	Clients          int `json:"clients"`
	PendingRequests  int `json:"pendingRequests"`
	PendingApprovals int `json:"pendingApprovals"`
	ActiveThreads    int `json:"activeThreads"`
}

type pendingRequest struct {
	client     *Client
	original   json.RawMessage
	method     string
	threadID   string
	initialize bool
}

type pendingApproval struct {
	client    *Client
	original  json.RawMessage
	threadID  string
	turnID    string
	responded bool
}

// Hub is the Multiplexer Output writer. SetHandler connects its reverse side
// to Multiplexer.HandleClient before accepting desktop connections.
type Hub struct {
	mu                sync.Mutex
	writeMu           sync.Mutex
	input             []byte
	handler           func(protocol.Message)
	sequence          uint64
	clients           map[string]*Client
	pending           map[string]pendingRequest
	approvals         map[string]pendingApproval
	initFingerprint   string
	initRequest       string
	initResult        *protocol.Message
	initWaiters       map[*Client]json.RawMessage
	nativeInitialized bool
	leases            map[string]*threadLease
	turnOwners        map[string]*Client
	turnParents       map[string]parentEdge
	nativeActive      map[string]map[string]bool
	completedTurns    map[string]bool
	unavailableTurns  map[string]bool
	knownThreads      map[string]bool
	parents           map[string]parentEdge
	seenItems         map[string]bool
	pendingItems      map[string][]nativeItem
	pendingItemCount  int
}

// Client represents one connection, not merely a channel name. Reconnecting
// with the same visible ID never inherits execution or approval ownership.
type Client struct {
	hub              *Hub
	id               string
	output           io.Writer
	queue            chan []byte
	done             chan struct{}
	closeOnce        sync.Once
	initReady        bool // protected by Hub.mu
	initialized      bool
	initializedOrder uint64
	pendingOriginal  map[string]bool
}

func New() *Hub {
	return &Hub{clients: map[string]*Client{}, pending: map[string]pendingRequest{}, approvals: map[string]pendingApproval{}, initWaiters: map[*Client]json.RawMessage{}, leases: map[string]*threadLease{}, turnOwners: map[string]*Client{}, turnParents: map[string]parentEdge{}, nativeActive: map[string]map[string]bool{}, completedTurns: map[string]bool{}, unavailableTurns: map[string]bool{}, knownThreads: map[string]bool{}, parents: map[string]parentEdge{}, seenItems: map[string]bool{}, pendingItems: map[string][]nativeItem{}}
}

func (h *Hub) SetHandler(handler func(protocol.Message)) {
	h.mu.Lock()
	h.handler = handler
	h.mu.Unlock()
}

func (h *Hub) AddClient(id string, output io.Writer) (*Client, error) {
	if !identity(id) || output == nil {
		return nil, errors.New("broker client ID and output are required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[id] != nil {
		return nil, errors.New("broker client ID already connected")
	}
	c := &Client{hub: h, id: id, output: output, queue: make(chan []byte, clientQueueSize), done: make(chan struct{}), pendingOriginal: map[string]bool{}}
	h.clients[id] = c
	go c.writeLoop()
	return c, nil
}

func (c *Client) ID() string { return c.id }

// Done closes when this connection is removed, including output failures.
func (c *Client) Done() <-chan struct{} { return c.done }
func (c *Client) Close()                { c.hub.removeClient(c) }
func (h *Hub) RemoveClient(id string) {
	h.mu.Lock()
	c := h.clients[id]
	h.mu.Unlock()
	if c != nil {
		h.removeClient(c)
	}
}

func (c *Client) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case data := <-c.queue:
			if n, err := c.output.Write(data); err != nil || n != len(data) {
				c.Close()
				return
			}
		}
	}
}

func (c *Client) emit(message protocol.Message) {
	data, err := protocol.Encode(message)
	if err != nil {
		c.Close()
		return
	}
	data = append(data, '\n')
	select {
	case <-c.done:
		return
	case c.queue <- data:
	default:
		c.Close() // A stalled interface cannot stall the other desktop.
	}
}

func (h *Hub) removeClient(c *Client) {
	h.mu.Lock()
	if h.clients[c.id] != c {
		h.mu.Unlock()
		return
	}
	delete(h.clients, c.id)
	delete(h.initWaiters, c)
	var failures []protocol.Message
	for id, approval := range h.approvals {
		if approval.client == c {
			if !approval.responded {
				failures = append(failures, protocol.Failure(approval.original, ErrApprovalOwnerGone, "desktop owning this approval disconnected"))
			}
			delete(h.approvals, id)
		}
	}
	handler := h.handler
	h.mu.Unlock()
	c.closeOnce.Do(func() {
		close(c.done)
		if closer, ok := c.output.(io.Closer); ok {
			_ = closer.Close()
		}
	})
	// Keep active leases until native completion. A new connection must not
	// silently take over an in-flight turn from a disconnected desktop.
	if handler != nil {
		for _, failure := range failures {
			handler(failure)
		}
	}
}

func (h *Hub) Activity() Activity {
	h.mu.Lock()
	defer h.mu.Unlock()
	pendingApprovals := 0
	for _, approval := range h.approvals {
		if !approval.responded {
			pendingApprovals++
		}
	}
	activeThreads := len(h.leases)
	for threadID, turns := range h.nativeActive {
		if len(turns) > 0 && h.leases[threadID] == nil {
			activeThreads++
		}
	}
	return Activity{Clients: len(h.clients), PendingRequests: len(h.pending), PendingApprovals: pendingApprovals, ActiveThreads: activeThreads}
}

// Idle describes work, independently of connection count. Daemon shutdown
// normally requires Idle() and Activity().Clients == 0 plus a grace period.
func (h *Hub) Idle() bool {
	a := h.Activity()
	return a.PendingRequests == 0 && a.PendingApprovals == 0 && a.ActiveThreads == 0
}

func (h *Hub) nextIDLocked(kind string) json.RawMessage {
	h.sequence++
	return protocol.StringID("csr-broker:" + kind + ":" + strconv.FormatUint(h.sequence, 10))
}

func (c *Client) Send(message protocol.Message) error {
	h := c.hub
	// Own copies: callers may reuse decoder buffers after Send returns.
	message = cloneMessage(message)
	h.mu.Lock()
	if h.clients[c.id] != c {
		h.mu.Unlock()
		return errors.New("broker client disconnected")
	}
	if h.handler == nil {
		h.mu.Unlock()
		return errors.New("broker handler is not configured")
	}
	handler := h.handler
	if message.Method == "" {
		key := protocol.RequestIDKey(message.ID)
		approval, ok := h.approvals[key]
		if !ok || approval.client != c || approval.responded {
			h.mu.Unlock()
			return errors.New("response does not belong to this desktop")
		}
		approval.responded = true
		h.approvals[key] = approval
		message.ID = append(json.RawMessage(nil), approval.original...)
		h.mu.Unlock()
		handler(message)
		return nil
	}
	if len(message.ID) == 0 {
		if message.Method == "initialized" {
			if !c.initReady {
				h.mu.Unlock()
				return errors.New("desktop has not initialized")
			}
			c.initialized = true
			if c.initializedOrder == 0 {
				h.sequence++
				c.initializedOrder = h.sequence
			}
			forward := !h.nativeInitialized
			h.nativeInitialized = true
			h.mu.Unlock()
			if forward {
				handler(message)
			}
			return nil
		}
		if !c.initialized {
			h.mu.Unlock()
			return errors.New("desktop has not initialized")
		}
		if isExecutionMutation(message.Method) {
			h.mu.Unlock()
			return errors.New("execution mutations require a request ID")
		}
		h.mu.Unlock()
		handler(message)
		return nil
	}
	originalKey := protocol.RequestIDKey(message.ID)
	if !validRequestID(message.ID) || c.pendingOriginal[originalKey] {
		h.mu.Unlock()
		c.emit(protocol.Failure(message.ID, ErrInvalidClientMessage, "invalid or duplicate desktop request ID"))
		return nil
	}
	if len(h.pending) >= maxTrackedTurns {
		h.mu.Unlock()
		c.emit(protocol.Failure(message.ID, ErrUnavailable, "shared app-server pending request limit reached"))
		return nil
	}
	if message.Method == "initialize" {
		fingerprint, err := initializationFingerprint(message.Params)
		if err != nil {
			h.mu.Unlock()
			c.emit(protocol.Failure(message.ID, ErrInvalidClientMessage, "invalid initialize capabilities"))
			return nil
		}
		if h.initFingerprint != "" && h.initFingerprint != fingerprint {
			h.mu.Unlock()
			c.emit(protocol.Failure(message.ID, ErrIncompatibleInitialization, "desktop capabilities do not match the shared app-server"))
			return nil
		}
		if h.initResult != nil {
			response := cloneMessage(*h.initResult)
			response.ID = message.ID
			c.initReady = true
			h.mu.Unlock()
			c.emit(response)
			return nil
		}
		if _, waiting := h.initWaiters[c]; waiting {
			h.mu.Unlock()
			c.emit(protocol.Failure(message.ID, ErrInvalidClientMessage, "desktop initialization is already pending"))
			return nil
		}
		h.initFingerprint = fingerprint
		h.initWaiters[c] = message.ID
		c.pendingOriginal[originalKey] = true
		if h.initRequest != "" {
			h.mu.Unlock()
			return nil
		}
		message.ID = h.nextIDLocked("initialize")
		h.initRequest = protocol.RequestIDKey(message.ID)
		h.pending[h.initRequest] = pendingRequest{initialize: true, method: "initialize"}
		h.mu.Unlock()
		handler(message)
		return nil
	}
	if !c.initialized {
		h.mu.Unlock()
		c.emit(protocol.Failure(message.ID, ErrUnavailable, "desktop has not initialized"))
		return nil
	}
	threadID := threadFromParams(message.Params)
	if isExecutionMutation(message.Method) && threadID != "" {
		if owner := h.ownerLocked(threadID, ""); (owner != nil && owner != c) || (owner == nil && len(h.nativeActive[threadID]) > 0) {
			h.mu.Unlock()
			c.emit(protocol.Failure(message.ID, ErrThreadBusy, "another desktop owns the active chat turn"))
			return nil
		}
	}
	if message.Method == "turn/start" {
		if !identity(threadID) {
			h.mu.Unlock()
			c.emit(protocol.Failure(message.ID, ErrInvalidClientMessage, "turn/start requires a thread ID"))
			return nil
		}
		if len(h.turnOwners) >= maxTrackedTurns || len(h.completedTurns) >= maxTrackedTurns || len(h.leases) >= maxTrackedTurns {
			h.mu.Unlock()
			c.emit(protocol.Failure(message.ID, ErrUnavailable, "shared app-server ownership limit reached; reconnect after the broker is idle"))
			return nil
		}
		lease := h.leases[threadID]
		if lease == nil {
			lease = &threadLease{owner: c}
			h.leases[threadID] = lease
		}
		lease.pending++
		// An explicit desktop start is a new root, even in a former agent chat.
		lease.parent = parentEdge{}
	}
	original := message.ID
	message.ID = h.nextIDLocked("request")
	h.pending[protocol.RequestIDKey(message.ID)] = pendingRequest{client: c, original: original, method: message.Method, threadID: threadID}
	c.pendingOriginal[originalKey] = true
	h.mu.Unlock()
	handler(message)
	return nil
}

func (h *Hub) Write(data []byte) (int, error) {
	h.writeMu.Lock()
	// Parse under the framing lock, then release it before invoking handlers:
	// a synchronous handler may itself write a response into this Hub.
	var messages []protocol.Message
	if len(h.input)+len(data) > maxMessageBytes {
		h.input = nil
		h.writeMu.Unlock()
		return 0, errors.New("broker message exceeds size limit")
	}
	h.input = append(h.input, data...)
	for {
		i := bytes.IndexByte(h.input, '\n')
		if i < 0 {
			break
		}
		line := append([]byte(nil), h.input[:i]...)
		h.input = h.input[i+1:]
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		message, err := protocol.Parse(line)
		if err != nil {
			h.input = nil
			h.writeMu.Unlock()
			return 0, errors.New("invalid app-server message")
		}
		messages = append(messages, message)
	}
	if len(h.input) == 0 {
		h.input = nil
	}
	h.writeMu.Unlock()
	for _, message := range messages {
		h.receive(message)
	}
	return len(data), nil
}

func (h *Hub) receive(message protocol.Message) {
	h.mu.Lock()
	if message.Method == "" && len(message.ID) > 0 {
		key := protocol.RequestIDKey(message.ID)
		pending, ok := h.pending[key]
		if !ok {
			h.mu.Unlock()
			return
		}
		delete(h.pending, key)
		if pending.initialize {
			waiters := h.initWaiters
			h.initWaiters = map[*Client]json.RawMessage{}
			h.initRequest = ""
			if message.Error == nil {
				cached := cloneMessage(message)
				cached.ID = nil
				h.initResult = &cached
			} else {
				h.initFingerprint = ""
			}
			for client, id := range waiters {
				delete(client.pendingOriginal, protocol.RequestIDKey(id))
				client.initReady = message.Error == nil
			}
			h.mu.Unlock()
			for client, id := range waiters {
				result := cloneMessage(message)
				result.ID = id
				client.emit(result)
			}
			return
		}
		delete(pending.client.pendingOriginal, protocol.RequestIDKey(pending.original))
		h.resultLocked(pending, message)
		message.ID = pending.original
		client := pending.client
		h.mu.Unlock()
		client.emit(message)
		return
	}
	if message.Method != "" && len(message.ID) > 0 {
		threadID, turnID := identitiesFromParams(message.Params)
		owner := h.ownerLocked(threadID, turnID)
		if threadID == "" && isSessionRequest(message.Method) {
			owner = h.sessionOwnerLocked()
		}
		handler := h.handler
		if len(h.approvals) >= maxTrackedTurns {
			h.mu.Unlock()
			if handler != nil {
				handler(protocol.Failure(message.ID, ErrUnavailable, "shared app-server pending approval limit reached"))
			}
			return
		}
		if owner == nil || h.clients[owner.id] != owner || !owner.initialized {
			code, reason := ErrUnknownApprovalOwner, "no desktop owns the requested active chat turn"
			if owner != nil {
				code, reason = ErrApprovalOwnerGone, "desktop owning this approval disconnected"
			}
			h.mu.Unlock()
			if handler != nil {
				handler(protocol.Failure(message.ID, code, reason))
			}
			return
		}
		original := append(json.RawMessage(nil), message.ID...)
		message.ID = h.nextIDLocked("server")
		h.approvals[protocol.RequestIDKey(message.ID)] = pendingApproval{client: owner, original: original, threadID: threadID, turnID: turnID}
		h.mu.Unlock()
		owner.emit(message)
		return
	}
	if message.Method == "serverRequest/resolved" {
		var params struct {
			RequestID json.RawMessage `json:"requestId"`
			ThreadID  string          `json:"threadId"`
		}
		if json.Unmarshal(message.Params, &params) == nil {
			for key, approval := range h.approvals {
				if protocol.RequestIDKey(approval.original) == protocol.RequestIDKey(params.RequestID) && approval.threadID == params.ThreadID {
					delete(h.approvals, key)
					var fields map[string]json.RawMessage
					_ = json.Unmarshal(message.Params, &fields)
					fields["requestId"] = json.RawMessage(key)
					message.Params, _ = json.Marshal(fields)
					owner := approval.client
					h.mu.Unlock()
					owner.emit(message)
					return
				}
			}
		}
		h.mu.Unlock()
		return
	}
	if h.unavailableNotificationLocked(message) {
		h.mu.Unlock()
		return
	}
	h.notificationLocked(message)
	clients := make([]*Client, 0, len(h.clients))
	for _, client := range h.clients {
		if client.initialized {
			clients = append(clients, client)
		}
	}
	h.mu.Unlock()
	for _, client := range clients {
		client.emit(message)
	}
}

func initializationFingerprint(raw json.RawMessage) (string, error) {
	var params map[string]json.RawMessage
	if len(raw) > 1<<20 || json.Unmarshal(raw, &params) != nil || params == nil {
		return "", errors.New("invalid initialize")
	}
	caps := map[string]any{}
	if raw, ok := params["capabilities"]; ok && string(raw) != "null" {
		if json.Unmarshal(raw, &caps) != nil || caps == nil {
			return "", errors.New("invalid capabilities")
		}
	}
	for _, name := range []string{"experimentalApi", "requestAttestation", "explicitGatewayOauth", "mcpServerOpenaiFormElicitation"} {
		if value, ok := caps[name]; ok {
			if _, ok := value.(bool); !ok {
				return "", errors.New("invalid boolean capability")
			}
		} else {
			caps[name] = false
		}
	}
	if raw, ok := caps["optOutNotificationMethods"]; ok && raw != nil {
		array, ok := raw.([]any)
		if !ok {
			return "", errors.New("invalid notification capability")
		}
		methods := make([]string, 0, len(array))
		seen := map[string]bool{}
		for _, entry := range array {
			method, ok := entry.(string)
			if !ok {
				return "", errors.New("invalid notification capability")
			}
			if !seen[method] {
				seen[method] = true
				methods = append(methods, method)
			}
		}
		sort.Strings(methods)
		caps["optOutNotificationMethods"] = methods
	} else {
		caps["optOutNotificationMethods"] = []string{}
	}
	encoded, err := json.Marshal(caps)
	return string(encoded), err
}

func validRequestID(raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > 1024 {
		return false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		return false
	}
	switch v := value.(type) {
	case string:
		return true
	case json.Number:
		_, err := v.Int64()
		return err == nil
	default:
		return false
	}
}

func cloneMessage(m protocol.Message) protocol.Message {
	m.ID = append(json.RawMessage(nil), m.ID...)
	m.Params = append(json.RawMessage(nil), m.Params...)
	m.Result = append(json.RawMessage(nil), m.Result...)
	if m.Error != nil {
		e := *m.Error
		e.Data = append(json.RawMessage(nil), e.Data...)
		m.Error = &e
	}
	return m
}

func identity(id string) bool {
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

func isExecutionMutation(method string) bool {
	return method == "turn/start" || method == "turn/steer" || method == "turn/interrupt" || method == "thread/rollback" || method == "thread/archive" || method == "thread/compact/start"
}

func isSessionRequest(method string) bool {
	return method == "attestation/generate" || method == "account/chatgptAuthTokens/refresh"
}

// Global authentication requests have no turn. Elect the oldest initialized
// connection only at request arrival; the approval record fixes that owner.
func (h *Hub) sessionOwnerLocked() *Client {
	var owner *Client
	for _, client := range h.clients {
		if client.initialized && (owner == nil || client.initializedOrder < owner.initializedOrder) {
			owner = client
		}
	}
	return owner
}

func (a Activity) String() string {
	return fmt.Sprintf("clients=%d requests=%d approvals=%d activeThreads=%d", a.Clients, a.PendingRequests, a.PendingApprovals, a.ActiveThreads)
}
