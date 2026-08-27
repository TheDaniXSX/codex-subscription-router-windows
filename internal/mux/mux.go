package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/backend"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

const requestTimeout = 30 * time.Second

const (
	maximumConcurrentChildren = 4
	restartFailureLimit       = 5
	restartBaseDelay          = time.Second
	restartMaximumDelay       = 30 * time.Second
	restartCircuitDelay       = time.Minute
)

type Options struct {
	RealExecutable string
	RealArgs       []string
	Environment    []string
	Store          *state.Store
	Output         io.Writer
	// RequestTimeout defaults to 30 seconds. A shorter value is useful for
	// deterministic integration tests and constrained hosts.
	RequestTimeout time.Duration
}

type externalRoute struct {
	accountID string
	method    string
	message   protocol.Message
	excluded  map[string]struct{}
	reason    *RouteReason
	expiresAt time.Time
}

type serverRequestRoute struct {
	accountID string
	original  json.RawMessage
	expiresAt time.Time
}

type accountRuntime struct {
	status       string
	err          string
	loginID      string
	loginCancel  bool
	failures     int
	circuitUntil time.Time
}

type Event struct {
	Type      string `json:"type"`
	AccountID string `json:"accountId,omitempty"`
	Message   string `json:"message,omitempty"`
	Data      any    `json:"data,omitempty"`
}

// Multiplexer presents one app-server connection to ChatGPT.app while owning
// one real app-server process per ChatGPT subscription.
type Multiplexer struct {
	realExecutable string
	realArgs       []string
	environment    []string
	store          *state.Store
	output         io.Writer

	childrenMu sync.RWMutex
	children   map[string]*backend.Child
	inbound    chan backend.Inbound
	childOps   sync.Map
	runtimeMu  sync.RWMutex
	runtime    map[string]accountRuntime

	lifecycleMu    sync.Mutex
	runCtx         context.Context
	runCancel      context.CancelFunc
	closeOnce      sync.Once
	closing        atomic.Bool
	requestTimeout time.Duration
	threadLocksMu  sync.Mutex
	threadLocks    map[string]*threadLock

	initializationMu sync.RWMutex
	initializeParams json.RawMessage
	initialized      bool

	externalMu     sync.Mutex
	externalRoutes map[string]externalRoute
	serverMu       sync.Mutex
	serverRoutes   map[string]serverRequestRoute
	serverSequence atomic.Uint64

	outputMu sync.Mutex
	eventsMu sync.RWMutex
	events   map[chan Event]struct{}

	profileMu     sync.Mutex
	profileClient *http.Client
	profileCache  map[string]profileCacheEntry
	now           func() time.Time

	resetCreditsMu       sync.Mutex
	resetCreditsCache    map[string]resetCreditsCacheEntry
	resetCreditsEndpoint string

	previewMu        sync.RWMutex
	rateLimitPreview *RateLimitPreview

	resetPreviewMu sync.RWMutex
	resetPreviews  map[string]ResetCreditsPreview
}

type threadLock struct {
	mu   sync.Mutex
	refs int
}

func New(options Options) (*Multiplexer, error) {
	if options.RealExecutable == "" || options.Store == nil || options.Output == nil {
		return nil, errors.New("real executable, store, and output are required")
	}
	timeout := options.RequestTimeout
	if timeout <= 0 {
		timeout = requestTimeout
	}
	return &Multiplexer{
		realExecutable:       options.RealExecutable,
		realArgs:             append([]string(nil), options.RealArgs...),
		environment:          append([]string(nil), options.Environment...),
		store:                options.Store,
		output:               options.Output,
		children:             make(map[string]*backend.Child),
		inbound:              make(chan backend.Inbound, 1024),
		runtime:              make(map[string]accountRuntime),
		requestTimeout:       timeout,
		threadLocks:          make(map[string]*threadLock),
		externalRoutes:       make(map[string]externalRoute),
		serverRoutes:         make(map[string]serverRequestRoute),
		events:               make(map[chan Event]struct{}),
		profileClient:        newProfileHTTPClient(),
		profileCache:         make(map[string]profileCacheEntry),
		now:                  time.Now,
		resetCreditsCache:    make(map[string]resetCreditsCacheEntry),
		resetCreditsEndpoint: rateLimitResetCreditsURL,
		resetPreviews:        make(map[string]ResetCreditsPreview),
	}, nil
}

func (m *Multiplexer) Start(ctx context.Context) error {
	if m.closing.Load() {
		return errors.New("multiplexer is closed")
	}
	m.lifecycleMu.Lock()
	if m.runCtx != nil {
		m.lifecycleMu.Unlock()
		return errors.New("multiplexer is already started")
	}
	m.runCtx, m.runCancel = context.WithCancel(ctx)
	runCtx := m.runCtx
	m.lifecycleMu.Unlock()

	accounts := m.store.Accounts()
	startErrors := m.forEachAccountBounded(runCtx, accounts, func(account state.Account) error {
		if !account.Enabled {
			m.setRuntime(account.ID, accountRuntime{status: "disabled"})
			return nil
		}
		m.setRuntime(account.ID, accountRuntime{status: "pending"})
		_, err := m.startChild(runCtx, account)
		return err
	})
	for accountID, err := range startErrors {
		m.setRuntimeFailure(accountID, "error", err)
		fmt.Fprintf(os.Stderr, "codex-mux: start account %s: %v\n", accountID, err)
	}
	if len(m.childEntries()) == 0 {
		m.runCancel()
		m.lifecycleMu.Lock()
		m.runCtx, m.runCancel = nil, nil
		m.lifecycleMu.Unlock()
		return errors.New("no Codex app-server process could be started")
	}
	go m.inboundLoop(runCtx)
	go m.syncManagedConfigLoop(runCtx)
	go m.routeExpiryLoop(runCtx)
	return nil
}

func (m *Multiplexer) syncManagedConfigLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.store.SyncManagedConfig(); err != nil {
				fmt.Fprintf(os.Stderr, "codex-mux: sync shared plugin config: %v\n", err)
			}
		}
	}
}

func (m *Multiplexer) Close() {
	m.closeOnce.Do(func() {
		m.closing.Store(true)
		m.lifecycleMu.Lock()
		if m.runCancel != nil {
			m.runCancel()
		}
		m.lifecycleMu.Unlock()

		entries := m.childEntries()
		m.expireAllRoutes()
		m.childrenMu.Lock()
		m.children = make(map[string]*backend.Child)
		m.childrenMu.Unlock()
		m.closeChildrenBounded(entries)
	})
}

func (m *Multiplexer) HandleClient(message protocol.Message) {
	if m.closing.Load() {
		if len(message.ID) > 0 {
			m.write(protocol.Failure(message.ID, -32034, "router is shutting down"))
		}
		return
	}
	if message.Method == "" && len(message.ID) > 0 {
		m.handleServerRequestResponse(message)
		return
	}
	if message.Method == "initialize" && len(message.ID) > 0 {
		go m.initialize(message)
		return
	}
	if len(message.ID) == 0 {
		m.handleClientNotification(message)
		return
	}

	switch message.Method {
	case "thread/list":
		go m.aggregateThreadList(message)
	case "thread/start":
		go m.routeNewThread(message)
	case "account/rateLimits/read":
		go m.routeAggregatedRateLimits(message)
	default:
		m.routeExistingRequest(message)
	}
}

func (m *Multiplexer) initialize(message protocol.Message) {
	m.initializationMu.Lock()
	m.initializeParams = append(json.RawMessage(nil), message.Params...)
	m.initializationMu.Unlock()

	entries := m.childEntries()
	type initializeResult struct {
		index     int
		accountID string
		child     *backend.Child
		result    json.RawMessage
		err       error
	}
	results := make(chan initializeResult, len(entries))
	semaphore := make(chan struct{}, maximumConcurrentChildren)
	var wait sync.WaitGroup
	for index, entry := range entries {
		wait.Add(1)
		go func(index int, entry childEntry) {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			ctx, cancel := context.WithTimeout(context.Background(), m.requestTimeout)
			defer cancel()
			response, err := entry.child.Request(ctx, "initialize", message.Params)
			results <- initializeResult{index: index, accountID: entry.account.ID, child: entry.child, result: response.Result, err: err}
		}(index, entry)
	}
	wait.Wait()
	close(results)
	ordered := make([]initializeResult, len(entries))
	for result := range results {
		ordered[result.index] = result
	}
	var firstResult json.RawMessage
	var firstErr error
	for _, result := range ordered {
		if result.err != nil {
			if m.removeChildIfCurrent(result.accountID, result.child) {
				_ = result.child.Close()
			}
			m.setRuntimeFailure(result.accountID, "error", fmt.Errorf("initialize app-server: %w", result.err))
			m.publish(Event{Type: "account-error", AccountID: result.accountID, Message: "App-server initialization failed"})
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		if firstResult == nil {
			firstResult = result.result
		}
	}
	if firstResult == nil {
		m.write(protocol.Failure(message.ID, -32000, fmt.Sprintf("failed to initialize account pool: %v", firstErr)))
		return
	}
	m.write(protocol.Success(message.ID, firstResult))
}

func (m *Multiplexer) handleClientNotification(message protocol.Message) {
	if message.Method == "initialized" {
		m.initializationMu.Lock()
		m.initialized = true
		m.initializationMu.Unlock()
		for _, entry := range m.childEntries() {
			_ = entry.child.Send(message)
		}
		return
	}
	if controller, ok := m.controllerChild(); ok {
		_ = controller.Send(message)
	}
}

func (m *Multiplexer) routeNewThread(message protocol.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), m.requestTimeout)
	defer cancel()
	m.routeNewThreadExcluding(ctx, message, nil)
}

func (m *Multiplexer) routeNewThreadExcluding(ctx context.Context, message protocol.Message, excluded map[string]struct{}) {
	account, reason, err := m.chooseAccountExcluding(ctx, excluded)
	if err != nil {
		if errors.Is(err, errNoSubscriptionCapacity) {
			m.write(m.allSubscriptionsDepleted(ctx, message.ID))
			return
		}
		m.write(protocol.Failure(message.ID, -32020, err.Error()))
		return
	}
	if err := m.forwardRoute(account.ID, message, excluded, &reason); err != nil {
		m.write(protocol.Failure(message.ID, -32021, err.Error()))
		return
	}
}

func (m *Multiplexer) routeExistingRequest(message protocol.Message) {
	accountID := ""
	if scopedAccountID, cleanedParams, ok := scopedPluginRequest(message.Method, message.Params); ok {
		if account, exists := m.store.Account(scopedAccountID); exists && account.Enabled {
			message.Params = cleanedParams
			if err := m.forward(scopedAccountID, message); err != nil {
				m.write(protocol.Failure(message.ID, -32023, err.Error()))
			}
			return
		}
	}
	threadID := threadIDFromParams(message.Params)
	if threadID != "" {
		accountID, _ = m.store.ThreadOwner(threadID)
	}
	if accountID == "" {
		if controller, ok := m.store.Controller(); ok {
			accountID = controller.ID
		}
	}
	if accountID == "" {
		m.write(protocol.Failure(message.ID, -32022, "no controller account is configured"))
		return
	}
	if message.Method == "turn/start" && threadID != "" {
		go m.routeTurnStart(message, threadID, accountID)
		return
	}
	if err := m.forward(accountID, message); err != nil {
		m.write(protocol.Failure(message.ID, -32023, err.Error()))
	}
}

func (m *Multiplexer) forward(accountID string, message protocol.Message) error {
	return m.forwardWithExclusions(accountID, message, nil)
}

func (m *Multiplexer) forwardWithExclusions(accountID string, message protocol.Message, excluded map[string]struct{}) error {
	return m.forwardRoute(accountID, message, excluded, nil)
}

func (m *Multiplexer) forwardRoute(accountID string, message protocol.Message, excluded map[string]struct{}, reason *RouteReason) error {
	if m.closing.Load() {
		return errors.New("router is shutting down")
	}
	child, ok := m.child(accountID)
	if !ok {
		return fmt.Errorf("account %s is unavailable", accountID)
	}
	key := protocol.RequestIDKey(message.ID)
	m.externalMu.Lock()
	if _, exists := m.externalRoutes[key]; exists {
		m.externalMu.Unlock()
		return fmt.Errorf("request ID %s is already pending", key)
	}
	m.externalRoutes[key] = externalRoute{
		accountID: accountID,
		method:    message.Method,
		message:   message,
		excluded:  cloneAccountSet(excluded),
		reason:    reason,
		expiresAt: m.now().Add(m.requestTimeout),
	}
	m.externalMu.Unlock()
	if err := child.Send(message); err != nil {
		m.externalMu.Lock()
		delete(m.externalRoutes, key)
		m.externalMu.Unlock()
		return err
	}
	return nil
}

func (m *Multiplexer) routeAggregatedRateLimits(message protocol.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), m.requestTimeout)
	defer cancel()
	rateLimits, err := m.AggregatedRateLimits(ctx)
	if err != nil {
		m.write(protocol.Failure(message.ID, -32024, err.Error()))
		return
	}
	result, err := json.Marshal(map[string]any{"rateLimits": rateLimits})
	if err != nil {
		m.write(protocol.Failure(message.ID, -32025, err.Error()))
		return
	}
	m.write(protocol.Success(message.ID, result))
}

func (m *Multiplexer) routeTurnStart(message protocol.Message, threadID, ownerID string) {
	release := m.lockThread(threadID)
	defer release()
	if currentOwner, ok := m.store.ThreadOwner(threadID); ok {
		ownerID = currentOwner
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*m.requestTimeout)
	defer cancel()
	snapshot, err := m.accountSnapshotWithProfile(ctx, ownerID, false)
	if err != nil || accountHasCapacity(snapshot) {
		if err := m.forward(ownerID, message); err != nil {
			m.write(protocol.Failure(message.ID, -32023, err.Error()))
		}
		return
	}
	excluded := map[string]struct{}{ownerID: {}}
	m.failoverTurn(ctx, message, threadID, ownerID, excluded)
}

func (m *Multiplexer) failoverTurn(
	ctx context.Context,
	message protocol.Message,
	threadID string,
	sourceAccountID string,
	excluded map[string]struct{},
) {
	fallback, _, err := m.chooseAccountExcluding(ctx, excluded)
	if err != nil {
		m.write(m.allSubscriptionsDepleted(ctx, message.ID))
		return
	}
	if err := m.resumeThreadOnAccount(ctx, threadID, sourceAccountID, fallback.ID); err != nil {
		m.write(protocol.Failure(message.ID, -32027, fmt.Sprintf("move chat to %s: %v", fallback.Label, err)))
		return
	}
	if err := m.store.SetThreadOwner(threadID, fallback.ID); err != nil {
		m.write(protocol.Failure(message.ID, -32028, err.Error()))
		return
	}
	if err := m.forwardWithExclusions(fallback.ID, message, excluded); err != nil {
		m.write(protocol.Failure(message.ID, -32023, err.Error()))
		return
	}
	m.publish(Event{
		Type:      "thread-failed-over",
		AccountID: fallback.ID,
		Message:   fmt.Sprintf("Chat continued with %s", fallback.Label),
		Data:      map[string]any{"threadId": threadID, "previousAccountId": sourceAccountID},
	})
}

func (m *Multiplexer) resumeThreadOnAccount(ctx context.Context, threadID, sourceAccountID, targetAccountID string) error {
	source, ok := m.child(sourceAccountID)
	if !ok {
		return fmt.Errorf("source subscription is unavailable")
	}
	target, ok := m.child(targetAccountID)
	if !ok {
		return fmt.Errorf("target subscription is unavailable")
	}
	readParams, _ := json.Marshal(map[string]any{"threadId": threadID, "includeTurns": true})
	readResponse, err := source.Request(ctx, "thread/read", readParams)
	if err != nil {
		return fmt.Errorf("read existing chat: %w", err)
	}
	var readResult struct {
		Thread struct {
			ID            string `json:"id"`
			Path          string `json:"path"`
			CWD           string `json:"cwd"`
			ModelProvider string `json:"modelProvider"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(readResponse.Result, &readResult); err != nil {
		return fmt.Errorf("decode existing chat: %w", err)
	}
	if readResult.Thread.ID == "" || readResult.Thread.Path == "" {
		return errors.New("existing chat has no resumable history path")
	}
	resumeParams, _ := json.Marshal(map[string]any{
		"threadId":      threadID,
		"history":       nil,
		"path":          readResult.Thread.Path,
		"cwd":           readResult.Thread.CWD,
		"model":         nil,
		"modelProvider": readResult.Thread.ModelProvider,
	})
	if _, err := target.Request(ctx, "thread/resume", resumeParams); err != nil {
		return fmt.Errorf("resume existing chat: %w", err)
	}
	return nil
}

func (m *Multiplexer) handleServerRequestResponse(message protocol.Message) {
	key := protocol.RequestIDKey(message.ID)
	m.serverMu.Lock()
	route, ok := m.serverRoutes[key]
	if ok {
		delete(m.serverRoutes, key)
	}
	m.serverMu.Unlock()
	if !ok {
		return
	}
	message.ID = route.original
	if child, exists := m.child(route.accountID); exists {
		_ = child.Send(message)
	}
}

func (m *Multiplexer) inboundLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case inbound := <-m.inbound:
			m.handleInbound(inbound)
		}
	}
}

func (m *Multiplexer) handleInbound(inbound backend.Inbound) {
	message := inbound.Message
	if message.Method == "" && len(message.ID) > 0 {
		key := protocol.RequestIDKey(message.ID)
		m.externalMu.Lock()
		route, ok := m.externalRoutes[key]
		if ok && route.accountID == inbound.AccountID {
			delete(m.externalRoutes, key)
		} else if ok {
			ok = false
		}
		m.externalMu.Unlock()
		if ok {
			if isUsageLimitResponse(message) {
				switch route.method {
				case "thread/start":
					m.publish(Event{Type: "thread-route-retrying", AccountID: inbound.AccountID, Message: "Selected subscription reported no capacity; trying another"})
					go m.retryNewThreadAfterUsageLimit(route, inbound.AccountID)
					return
				case "turn/start":
					go m.retryTurnAfterUsageLimit(route, inbound.AccountID)
					return
				}
			}
			m.learnThreadOwner(route, inbound.AccountID, message.Result)
			if route.method == "thread/start" && message.Error == nil {
				label := inbound.AccountID
				if account, exists := m.store.Account(inbound.AccountID); exists {
					label = account.Label
				}
				m.publish(Event{Type: "thread-routed", AccountID: inbound.AccountID, Message: fmt.Sprintf("New chat pinned to %s", label), Data: route.reason})
			}
			m.writeRaw(inbound.Raw)
		}
		return
	}
	if message.Method != "" && len(message.ID) > 0 {
		m.forwardServerRequest(inbound)
		return
	}
	if message.Method == "account/rateLimits/updated" {
		go m.forwardAggregatedRateLimitNotification(inbound.Raw)
		return
	}
	if message.Method == "thread/started" {
		if threadID := threadIDFromNotification(message.Params); threadID != "" {
			_ = m.store.SetThreadOwner(threadID, inbound.AccountID)
		}
	}
	if message.Method == "turn/completed" ||
		message.Method == "account/login/completed" ||
		message.Method == "account/updated" {
		if message.Method == "account/login/completed" {
			m.handleLoginCompleted(inbound.AccountID, message.Params)
		}
		go m.publishAccountRefresh(inbound.AccountID)
	}
	if m.shouldForwardNotification(inbound.AccountID, message.Method) {
		m.writeRaw(inbound.Raw)
	}
}

func (m *Multiplexer) handleLoginCompleted(accountID string, params json.RawMessage) {
	var completed struct {
		LoginID string  `json:"loginId"`
		Success bool    `json:"success"`
		Error   *string `json:"error"`
	}
	if json.Unmarshal(params, &completed) != nil || completed.LoginID == "" {
		return
	}
	m.updateRuntime(accountID, func(runtime *accountRuntime) {
		if runtime.loginID != completed.LoginID {
			return
		}
		cancelled := runtime.loginCancel
		runtime.loginID = ""
		runtime.loginCancel = false
		if completed.Success {
			runtime.status = "disconnected"
			runtime.err = ""
			return
		}
		if cancelled {
			runtime.status = "disconnected"
			runtime.err = ""
			return
		}
		runtime.status = "error"
		if completed.Error != nil && strings.TrimSpace(*completed.Error) != "" {
			runtime.err = *completed.Error
		} else {
			runtime.err = "Sign-in failed"
		}
	})
}

func (m *Multiplexer) forwardAggregatedRateLimitNotification(fallback []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), m.requestTimeout)
	defer cancel()
	rateLimits, err := m.AggregatedRateLimits(ctx)
	if err != nil {
		m.writeRaw(fallback)
		return
	}
	params, err := json.Marshal(map[string]any{"rateLimits": rateLimits})
	if err != nil {
		m.writeRaw(fallback)
		return
	}
	m.write(protocol.Message{Method: "account/rateLimits/updated", Params: params})
}

func (m *Multiplexer) retryTurnAfterUsageLimit(route externalRoute, exhaustedAccountID string) {
	threadID := threadIDFromParams(route.message.Params)
	if threadID == "" {
		ctx, cancel := context.WithTimeout(context.Background(), m.requestTimeout)
		defer cancel()
		m.write(m.allSubscriptionsDepleted(ctx, route.message.ID))
		return
	}
	excluded := cloneAccountSet(route.excluded)
	if excluded == nil {
		excluded = make(map[string]struct{})
	}
	excluded[exhaustedAccountID] = struct{}{}
	release := m.lockThread(threadID)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*m.requestTimeout)
	defer cancel()
	m.failoverTurn(ctx, route.message, threadID, exhaustedAccountID, excluded)
}

func (m *Multiplexer) retryNewThreadAfterUsageLimit(route externalRoute, exhaustedAccountID string) {
	excluded := cloneAccountSet(route.excluded)
	if excluded == nil {
		excluded = make(map[string]struct{})
	}
	excluded[exhaustedAccountID] = struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), m.requestTimeout)
	defer cancel()
	m.routeNewThreadExcluding(ctx, route.message, excluded)
}

func (m *Multiplexer) forwardServerRequest(inbound backend.Inbound) {
	if m.closing.Load() {
		if child, ok := m.child(inbound.AccountID); ok {
			_ = child.Send(protocol.Failure(inbound.Message.ID, -32031, "router is shutting down"))
		}
		return
	}
	sequence := m.serverSequence.Add(1)
	newID := protocol.StringID(fmt.Sprintf("codex-mux:%s:%d", inbound.AccountID, sequence))
	key := protocol.RequestIDKey(newID)
	m.serverMu.Lock()
	m.serverRoutes[key] = serverRequestRoute{
		accountID: inbound.AccountID,
		original:  append(json.RawMessage(nil), inbound.Message.ID...),
		expiresAt: m.now().Add(m.requestTimeout),
	}
	m.serverMu.Unlock()
	inbound.Message.ID = newID
	m.write(inbound.Message)
}

func (m *Multiplexer) shouldForwardNotification(accountID, method string) bool {
	controller, ok := m.store.Controller()
	if ok && controller.ID == accountID {
		return true
	}
	return strings.HasPrefix(method, "thread/") ||
		strings.HasPrefix(method, "turn/") ||
		strings.HasPrefix(method, "item/") ||
		strings.HasPrefix(method, "hook/") ||
		strings.HasPrefix(method, "rawResponse")
}

func (m *Multiplexer) learnThreadOwner(route externalRoute, accountID string, result json.RawMessage) {
	switch route.method {
	case "thread/start", "thread/fork", "thread/resume", "thread/unarchive":
		if threadID := threadIDFromResult(result); threadID != "" {
			_ = m.store.SetThreadOwner(threadID, accountID)
		}
	}
}

func (m *Multiplexer) write(message protocol.Message) {
	encoded, err := protocol.Encode(message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex-mux: encode response: %v\n", err)
		return
	}
	m.writeRaw(encoded)
}

func (m *Multiplexer) writeRaw(encoded []byte) {
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	_, _ = m.output.Write(append(encoded, '\n'))
}

type childEntry struct {
	account state.Account
	child   *backend.Child
}

func (m *Multiplexer) childEntries() []childEntry {
	accounts := m.store.Accounts()
	m.childrenMu.RLock()
	defer m.childrenMu.RUnlock()
	entries := make([]childEntry, 0, len(accounts))
	for _, account := range accounts {
		if child := m.children[account.ID]; child != nil {
			entries = append(entries, childEntry{account: account, child: child})
		}
	}
	return entries
}

func (m *Multiplexer) child(accountID string) (*backend.Child, bool) {
	m.childrenMu.RLock()
	defer m.childrenMu.RUnlock()
	child, ok := m.children[accountID]
	return child, ok
}

func (m *Multiplexer) controllerChild() (*backend.Child, bool) {
	controller, ok := m.store.Controller()
	if !ok {
		return nil, false
	}
	return m.child(controller.ID)
}

func (m *Multiplexer) startChild(ctx context.Context, account state.Account) (*backend.Child, error) {
	operation := m.childOperationLock(account.ID)
	operation.Lock()
	defer operation.Unlock()
	return m.startChildLocked(ctx, account)
}

// startChildLocked requires the per-account child operation lock.
func (m *Multiplexer) startChildLocked(ctx context.Context, account state.Account) (*backend.Child, error) {
	if m.closing.Load() {
		return nil, errors.New("router is shutting down")
	}
	if !account.Enabled {
		return nil, fmt.Errorf("account %s is disabled", account.ID)
	}
	if child, ok := m.child(account.ID); ok {
		select {
		case <-child.Done():
			m.removeChildIfCurrent(account.ID, child)
		default:
			return child, nil
		}
	}
	if runtime := m.runtimeState(account.ID); !runtime.circuitUntil.IsZero() && m.now().Before(runtime.circuitUntil) {
		return nil, fmt.Errorf("account %s restart circuit is open until %s", account.ID, runtime.circuitUntil.Format(time.RFC3339))
	}
	child, err := backend.Start(
		account.ID,
		account.CodexHome,
		m.realExecutable,
		m.realArgs,
		m.environment,
		m.inbound,
	)
	if err != nil {
		return nil, err
	}
	m.childrenMu.Lock()
	m.children[account.ID] = child
	m.childrenMu.Unlock()

	m.initializationMu.RLock()
	params := append(json.RawMessage(nil), m.initializeParams...)
	initialized := m.initialized
	m.initializationMu.RUnlock()
	if len(params) > 0 {
		requestCtx, cancel := context.WithTimeout(ctx, m.requestTimeout)
		_, err := child.Request(requestCtx, "initialize", params)
		cancel()
		if err != nil {
			m.removeChildIfCurrent(account.ID, child)
			_ = child.Close()
			return nil, err
		}
		if initialized {
			_ = child.Send(protocol.Message{Method: "initialized"})
		}
	}
	m.updateRuntime(account.ID, func(runtime *accountRuntime) {
		runtime.status = "disconnected"
		runtime.err = ""
	})
	go m.monitorChild(account.ID, child)
	return child, nil
}

func (m *Multiplexer) stopChild(accountID, status string) error {
	operation := m.childOperationLock(accountID)
	operation.Lock()
	defer operation.Unlock()
	return m.stopChildLocked(accountID, status)
}

// stopChildLocked requires the per-account child operation lock.
func (m *Multiplexer) stopChildLocked(accountID, status string) error {
	m.childrenMu.Lock()
	child := m.children[accountID]
	delete(m.children, accountID)
	m.childrenMu.Unlock()
	if status != "" {
		m.setRuntime(accountID, accountRuntime{status: status})
	}
	if child == nil {
		return nil
	}
	return child.Close()
}

func (m *Multiplexer) removeChildIfCurrent(accountID string, child *backend.Child) bool {
	m.childrenMu.Lock()
	defer m.childrenMu.Unlock()
	if m.children[accountID] != child {
		return false
	}
	delete(m.children, accountID)
	return true
}

func (m *Multiplexer) childOperationLock(accountID string) *sync.Mutex {
	value, _ := m.childOps.LoadOrStore(accountID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (m *Multiplexer) monitorChild(accountID string, child *backend.Child) {
	<-child.Done()
	if !m.removeChildIfCurrent(accountID, child) {
		return
	}
	m.lifecycleMu.Lock()
	runCtx := m.runCtx
	m.lifecycleMu.Unlock()
	if runCtx == nil || runCtx.Err() != nil {
		return
	}
	account, ok := m.store.Account(accountID)
	if !ok || !account.Enabled {
		return
	}

	runtime := m.recordChildFailure(accountID, errors.New("Codex app-server exited unexpectedly"))
	delay := restartDelay(runtime.failures)
	if runtime.failures >= restartFailureLimit {
		delay = restartCircuitDelay
		m.updateRuntime(accountID, func(current *accountRuntime) {
			current.status = "error"
			current.circuitUntil = m.now().Add(delay)
		})
		m.publish(Event{Type: "account-error", AccountID: accountID, Message: "App-server restart circuit opened", Data: map[string]any{"retryInSeconds": int(delay.Seconds())}})
	} else {
		m.publish(Event{Type: "account-restarting", AccountID: accountID, Message: "App-server stopped; restarting", Data: map[string]any{"retryInSeconds": int(delay.Seconds())}})
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-runCtx.Done():
		return
	case <-timer.C:
	}
	account, ok = m.store.Account(accountID)
	if !ok || !account.Enabled {
		return
	}
	// The circuit delay has elapsed, so allow one supervised probe.
	m.updateRuntime(accountID, func(current *accountRuntime) { current.circuitUntil = time.Time{} })
	if _, err := m.startChild(runCtx, account); err != nil {
		m.setRuntimeFailure(accountID, "error", err)
		m.publish(Event{Type: "account-error", AccountID: accountID, Message: err.Error()})
		m.scheduleRestart(accountID)
		return
	}
	m.publish(Event{Type: "account-restarted", AccountID: accountID, Message: "App-server restarted"})
}

func (m *Multiplexer) scheduleRestart(accountID string) {
	m.lifecycleMu.Lock()
	runCtx := m.runCtx
	m.lifecycleMu.Unlock()
	if runCtx == nil || runCtx.Err() != nil {
		return
	}
	runtime := m.recordChildFailure(accountID, errors.New("app-server restart failed"))
	delay := restartDelay(runtime.failures)
	if runtime.failures >= restartFailureLimit {
		delay = restartCircuitDelay
		m.updateRuntime(accountID, func(current *accountRuntime) {
			current.status = "error"
			current.circuitUntil = m.now().Add(delay)
		})
		m.publish(Event{Type: "account-error", AccountID: accountID, Message: "App-server restart circuit opened", Data: map[string]any{"retryInSeconds": int(delay.Seconds())}})
	} else {
		m.publish(Event{Type: "account-restarting", AccountID: accountID, Message: "Retrying app-server start", Data: map[string]any{"retryInSeconds": int(delay.Seconds())}})
	}
	timer := time.NewTimer(delay)
	go func() {
		defer timer.Stop()
		select {
		case <-runCtx.Done():
			return
		case <-timer.C:
		}
		account, ok := m.store.Account(accountID)
		if !ok || !account.Enabled {
			return
		}
		m.updateRuntime(accountID, func(current *accountRuntime) { current.circuitUntil = time.Time{} })
		if _, err := m.startChild(runCtx, account); err != nil {
			m.setRuntimeFailure(accountID, "error", err)
			m.scheduleRestart(accountID)
		}
	}()
}

func restartDelay(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := restartBaseDelay
	for index := 1; index < failures && delay < restartMaximumDelay; index++ {
		delay *= 2
	}
	if delay > restartMaximumDelay {
		return restartMaximumDelay
	}
	return delay
}

func (m *Multiplexer) SubscribeEvents() (<-chan Event, func()) {
	channel := make(chan Event, 32)
	m.eventsMu.Lock()
	m.events[channel] = struct{}{}
	m.eventsMu.Unlock()
	return channel, func() {
		m.eventsMu.Lock()
		if _, ok := m.events[channel]; ok {
			delete(m.events, channel)
			close(channel)
		}
		m.eventsMu.Unlock()
	}
}

func (m *Multiplexer) publish(event Event) {
	m.eventsMu.RLock()
	defer m.eventsMu.RUnlock()
	for channel := range m.events {
		select {
		case channel <- event:
		default:
		}
	}
}

func (m *Multiplexer) publishAccountRefresh(accountID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := m.accountSnapshot(ctx, accountID)
	if err == nil {
		m.publish(Event{Type: "account-updated", AccountID: accountID, Data: snapshot})
	} else {
		m.setRuntimeFailure(accountID, "error", err)
		m.publish(Event{Type: "account-error", AccountID: accountID, Message: err.Error()})
	}
}

func (m *Multiplexer) setRuntime(accountID string, runtime accountRuntime) {
	m.runtimeMu.Lock()
	m.runtime[accountID] = runtime
	m.runtimeMu.Unlock()
}

func (m *Multiplexer) runtimeState(accountID string) accountRuntime {
	m.runtimeMu.RLock()
	defer m.runtimeMu.RUnlock()
	return m.runtime[accountID]
}

func (m *Multiplexer) updateRuntime(accountID string, update func(*accountRuntime)) accountRuntime {
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	runtime := m.runtime[accountID]
	update(&runtime)
	m.runtime[accountID] = runtime
	return runtime
}

func (m *Multiplexer) setRuntimeFailure(accountID, status string, err error) accountRuntime {
	return m.updateRuntime(accountID, func(runtime *accountRuntime) {
		runtime.status = status
		if err != nil {
			runtime.err = err.Error()
		}
	})
}

func (m *Multiplexer) recordChildFailure(accountID string, err error) accountRuntime {
	return m.updateRuntime(accountID, func(runtime *accountRuntime) {
		runtime.status = "restarting"
		runtime.loginID = ""
		runtime.loginCancel = false
		runtime.failures++
		if err != nil {
			runtime.err = err.Error()
		}
	})
}

func (m *Multiplexer) markRuntimeHealthy(accountID, status string) {
	m.updateRuntime(accountID, func(runtime *accountRuntime) {
		runtime.status = status
		runtime.err = ""
		runtime.loginID = ""
		runtime.loginCancel = false
		runtime.failures = 0
		runtime.circuitUntil = time.Time{}
	})
}

func (m *Multiplexer) lockThread(threadID string) func() {
	m.threadLocksMu.Lock()
	lock := m.threadLocks[threadID]
	if lock == nil {
		lock = &threadLock{}
		m.threadLocks[threadID] = lock
	}
	lock.refs++
	m.threadLocksMu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		m.threadLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(m.threadLocks, threadID)
		}
		m.threadLocksMu.Unlock()
	}
}

func (m *Multiplexer) forEachAccountBounded(ctx context.Context, accounts []state.Account, operation func(state.Account) error) map[string]error {
	type result struct {
		accountID string
		err       error
	}
	results := make(chan result, len(accounts))
	semaphore := make(chan struct{}, maximumConcurrentChildren)
	var wait sync.WaitGroup
	for _, account := range accounts {
		account := account
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- result{accountID: account.ID, err: ctx.Err()}
				return
			}
			results <- result{accountID: account.ID, err: operation(account)}
		}()
	}
	wait.Wait()
	close(results)
	errorsByAccount := make(map[string]error)
	for result := range results {
		if result.err != nil {
			errorsByAccount[result.accountID] = result.err
		}
	}
	return errorsByAccount
}

func (m *Multiplexer) closeChildrenBounded(entries []childEntry) {
	semaphore := make(chan struct{}, maximumConcurrentChildren)
	var wait sync.WaitGroup
	for _, entry := range entries {
		entry := entry
		wait.Add(1)
		go func() {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			if err := entry.child.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "codex-mux: close account %s: %v\n", entry.account.ID, err)
			}
		}()
	}
	wait.Wait()
}

func (m *Multiplexer) routeExpiryLoop(ctx context.Context) {
	interval := m.requestTimeout / 4
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	if interval > time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.expireRoutes(now)
		}
	}
}

func (m *Multiplexer) expireRoutes(now time.Time) {
	expiredExternal := make([]externalRoute, 0)
	m.externalMu.Lock()
	for key, route := range m.externalRoutes {
		if !route.expiresAt.After(now) {
			delete(m.externalRoutes, key)
			expiredExternal = append(expiredExternal, route)
		}
	}
	m.externalMu.Unlock()
	for _, route := range expiredExternal {
		m.write(protocol.Failure(route.message.ID, -32030, fmt.Sprintf("%s timed out waiting for an app-server response", route.method)))
	}

	expiredServer := make([]serverRequestRoute, 0)
	m.serverMu.Lock()
	for key, route := range m.serverRoutes {
		if !route.expiresAt.After(now) {
			delete(m.serverRoutes, key)
			expiredServer = append(expiredServer, route)
		}
	}
	m.serverMu.Unlock()
	for _, route := range expiredServer {
		if child, ok := m.child(route.accountID); ok {
			_ = child.Send(protocol.Failure(route.original, -32031, "desktop client timed out responding to app-server request"))
		}
	}
}

func (m *Multiplexer) expireAllRoutes() {
	m.externalMu.Lock()
	external := make([]externalRoute, 0, len(m.externalRoutes))
	for key, route := range m.externalRoutes {
		external = append(external, route)
		delete(m.externalRoutes, key)
	}
	m.externalMu.Unlock()
	for _, route := range external {
		m.write(protocol.Failure(route.message.ID, -32030, "router shut down before app-server responded"))
	}

	m.serverMu.Lock()
	server := make([]serverRequestRoute, 0, len(m.serverRoutes))
	for key, route := range m.serverRoutes {
		server = append(server, route)
		delete(m.serverRoutes, key)
	}
	m.serverMu.Unlock()
	for _, route := range server {
		if child, ok := m.child(route.accountID); ok {
			_ = child.Send(protocol.Failure(route.original, -32031, "router shut down before desktop client responded"))
		}
	}
}

func threadIDFromParams(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var decoded map[string]any
	if json.Unmarshal(params, &decoded) != nil {
		return ""
	}
	for _, key := range []string{"threadId", "thread_id"} {
		if value, ok := decoded[key].(string); ok {
			return value
		}
	}
	return ""
}

func threadIDFromResult(result json.RawMessage) string {
	var decoded struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &decoded) != nil {
		return ""
	}
	return decoded.Thread.ID
}

func threadIDFromNotification(params json.RawMessage) string {
	return threadIDFromResult(params)
}

func accountHasCapacity(snapshot AccountSnapshot) bool {
	if !snapshot.Enabled || !snapshot.Connected || snapshot.AuthType != "chatgpt" {
		return false
	}
	weekly, _ := longestAndShortestWindow(snapshot.RateLimits)
	return weekly == nil || weekly.UsedPercent < 100
}

func isUsageLimitResponse(message protocol.Message) bool {
	if message.Error == nil {
		return false
	}
	if len(message.Error.Data) > 0 {
		var structured any
		if json.Unmarshal(message.Error.Data, &structured) == nil && structuredContainsUsageLimit(structured) {
			return true
		}
	}
	text := strings.ToLower(message.Error.Message)
	return strings.Contains(text, "usage limit") ||
		strings.Contains(text, "usage_limit_exceeded") ||
		strings.Contains(text, "rate limit exceeded") ||
		strings.Contains(text, "rate_limit_exceeded") ||
		strings.Contains(text, "quota exceeded")
}

func structuredContainsUsageLimit(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalizedKey := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			switch normalizedKey {
			case "codexerrorinfo", "code", "type", "error", "reason":
				if text, ok := nested.(string); ok && usageLimitCode(text) {
					return true
				}
			}
			if structuredContainsUsageLimit(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if structuredContainsUsageLimit(nested) {
				return true
			}
		}
	}
	return false
}

func usageLimitCode(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "usage_limit_exceeded", "rate_limit_exceeded", "quota_exceeded", "usage_limit_reached", "rate_limit_reached":
		return true
	default:
		return false
	}
}

func (m *Multiplexer) allSubscriptionsDepleted(ctx context.Context, id json.RawMessage) protocol.Message {
	var resetsAt *int64
	if preview := m.currentRateLimitPreview(); preview != nil && preview.Mode.isAllDepleted() {
		resetsAt = preview.ResetsAt
	} else if limits, err := m.AggregatedRateLimits(ctx); err == nil {
		weekly, _ := longestAndShortestWindow(limits)
		if weekly != nil {
			resetsAt = weekly.ResetsAt
		}
	}
	return allSubscriptionsDepleted(id, resetsAt)
}

func allSubscriptionsDepleted(id json.RawMessage, resetsAt *int64) protocol.Message {
	message := "All connected subscriptions are depleted. Add another subscription or wait for usage to reset."
	if resetsAt != nil {
		reset := time.Unix(*resetsAt, 0).In(time.Local)
		message = fmt.Sprintf(
			"All connected subscriptions are depleted. Usage resets on %s.",
			reset.Format("Monday, 2 January at 3:04 PM"),
		)
	}
	return protocol.Failure(
		id,
		-32026,
		message,
	)
}

func cloneAccountSet(source map[string]struct{}) map[string]struct{} {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string]struct{}, len(source))
	for accountID := range source {
		clone[accountID] = struct{}{}
	}
	return clone
}

func sortThreads(threads []map[string]any) {
	sort.SliceStable(threads, func(i, j int) bool {
		left := numericField(threads[i], "updatedAt", "createdAt")
		right := numericField(threads[j], "updatedAt", "createdAt")
		if left != right {
			return left > right
		}
		return stringField(threads[i], "id") < stringField(threads[j], "id")
	})
}

func numericField(value map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if number, ok := value[key].(float64); ok {
			return number
		}
	}
	return 0
}

func stringField(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}
