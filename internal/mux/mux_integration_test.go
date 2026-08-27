package mux

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

// These tests intentionally run the test binary as a real child app-server.
// That exercises the production stdin/stdout JSONL transport, process
// supervision, restart path and shutdown behavior without needing credentials.
const (
	fakeProcessEnvironment           = "MUXTEST_FAKE_APP_SERVER"
	fakeRunRootEnvironment           = "MUXTEST_RUN_ROOT"
	defaultIntegrationRequestTimeout = 5 * time.Second
)

type fakePage struct {
	Threads    []map[string]any `json:"threads,omitempty"`
	NextCursor *string          `json:"nextCursor,omitempty"`
	Error      string           `json:"error,omitempty"`
}

type fakeAppServerSpec struct {
	AccountID              string              `json:"accountId"`
	UsedPercent            float64             `json:"usedPercent"`
	Disconnected           bool                `json:"disconnected,omitempty"`
	InitializeError        bool                `json:"initializeError,omitempty"`
	NoResponseMethods      []string            `json:"noResponseMethods,omitempty"`
	CrashMethod            string              `json:"crashMethod,omitempty"`
	CrashLaunches          int                 `json:"crashLaunches,omitempty"`
	QuotaOnThreadStart     bool                `json:"quotaOnThreadStart,omitempty"`
	ThreadStartID          string              `json:"threadStartId,omitempty"`
	ThreadReadPath         string              `json:"threadReadPath,omitempty"`
	Pages                  map[string]fakePage `json:"pages,omitempty"`
	DelayMilliseconds      map[string]int      `json:"delayMilliseconds,omitempty"`
	EmitServerRequestOn    string              `json:"emitServerRequestOn,omitempty"`
	LoginID                string              `json:"loginId,omitempty"`
	EmitLoginCompletedOn   string              `json:"emitLoginCompletedOn,omitempty"`
	CompleteLoginOnCancel  bool                `json:"completeLoginOnCancel,omitempty"`
	LoginCompletionSuccess bool                `json:"loginCompletionSuccess,omitempty"`
	LoginCompletionError   string              `json:"loginCompletionError,omitempty"`
}

type fakeEvent struct {
	AccountID string           `json:"accountId"`
	Launch    int              `json:"launch"`
	Sequence  int              `json:"sequence"`
	Message   protocol.Message `json:"message"`
}

// TestMuxFakeAppServerProcess is entered only by backend.Start subprocesses.
// os.Exit prevents the Go test harness from writing PASS to the JSONL stream.
func TestMuxFakeAppServerProcess(t *testing.T) {
	if os.Getenv(fakeProcessEnvironment) != "1" {
		t.Skip("subprocess helper")
	}
	code := runFakeAppServer(os.Stdin, os.Stdout)
	os.Exit(code)
}

func runFakeAppServer(input *os.File, output *os.File) int {
	home := os.Getenv("CODEX_HOME")
	runRoot := os.Getenv(fakeRunRootEnvironment)
	spec, err := readFakeSpec(home)
	if err != nil {
		return 91
	}
	launch, err := claimFakeLaunch(runRoot, spec.AccountID)
	if err != nil {
		return 92
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 93
	}
	defer listener.Close()
	if err := recordFakeLiveness(runRoot, spec.AccountID, launch, listener.Addr().String()); err != nil {
		return 94
	}
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = connection.Close()
		}
	}()

	writer := bufio.NewWriter(output)
	defer writer.Flush()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	sequence := 0
	for scanner.Scan() {
		message, parseErr := protocol.Parse(scanner.Bytes())
		if parseErr != nil {
			continue
		}
		sequence++
		_ = recordFakeEvent(runRoot, fakeEvent{
			AccountID: spec.AccountID,
			Launch:    launch,
			Sequence:  sequence,
			Message:   message,
		})
		if message.Method == "" {
			continue
		}

		// Reload the spec so a test can safely alter quota/delay behavior while
		// the process is alive. Specs are replaced atomically by the parent.
		if current, currentErr := readFakeSpec(home); currentErr == nil {
			spec = current
		}
		if delay := spec.DelayMilliseconds[message.Method]; delay > 0 {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
		if spec.CrashMethod == message.Method && launch <= spec.CrashLaunches {
			return 42
		}
		if stringInSlice(spec.NoResponseMethods, message.Method) {
			continue
		}

		response := fakeResponse(spec, message)
		if response != nil {
			encoded, encodeErr := protocol.Encode(*response)
			if encodeErr != nil {
				return 95
			}
			if _, err := writer.Write(append(encoded, '\n')); err != nil {
				return 0
			}
			if err := writer.Flush(); err != nil {
				return 0
			}
		}
		if spec.EmitServerRequestOn == message.Method {
			serverRequest := protocol.Request(
				"test/server/request",
				protocol.StringID("server-1"),
				json.RawMessage(`{"prompt":"approve"}`),
			)
			encoded, _ := protocol.Encode(serverRequest)
			if _, err := writer.Write(append(encoded, '\n')); err != nil {
				return 0
			}
			if err := writer.Flush(); err != nil {
				return 0
			}
		}
		if spec.EmitLoginCompletedOn == message.Method ||
			(spec.CompleteLoginOnCancel && message.Method == "account/login/cancel") {
			params := map[string]any{
				"loginId": spec.LoginID,
				"success": spec.LoginCompletionSuccess,
			}
			if spec.LoginCompletionError != "" {
				params["error"] = spec.LoginCompletionError
			}
			encodedParams, _ := json.Marshal(params)
			completed, _ := protocol.Encode(protocol.Message{
				Method: "account/login/completed",
				Params: encodedParams,
			})
			if _, err := writer.Write(append(completed, '\n')); err != nil {
				return 0
			}
			if err := writer.Flush(); err != nil {
				return 0
			}
		}
	}
	return 0
}

func fakeResponse(spec fakeAppServerSpec, message protocol.Message) *protocol.Message {
	success := func(value any) *protocol.Message {
		result, _ := json.Marshal(value)
		response := protocol.Success(message.ID, result)
		return &response
	}
	switch message.Method {
	case "initialize":
		if spec.InitializeError {
			response := protocol.Failure(message.ID, -32090, "synthetic initialize failure")
			return &response
		}
		return success(map[string]any{"serverInfo": map[string]any{"name": "fake-app-server"}})
	case "account/read":
		if spec.Disconnected {
			return success(map[string]any{"account": nil})
		}
		return success(map[string]any{"account": map[string]any{
			"type": "chatgpt", "email": spec.AccountID + "@example.test", "planType": "plus",
		}})
	case "account/rateLimits/read":
		shortMinutes, weeklyMinutes := int64(300), int64(10_080)
		return success(map[string]any{"rateLimits": map[string]any{
			"primary": map[string]any{
				"usedPercent": spec.UsedPercent, "windowDurationMins": shortMinutes,
			},
			"secondary": map[string]any{
				"usedPercent": spec.UsedPercent, "windowDurationMins": weeklyMinutes,
			},
		}})
	case "account/login/start":
		loginID := spec.LoginID
		if loginID == "" {
			loginID = "login-" + spec.AccountID
		}
		return success(map[string]any{
			"type": "deviceCode", "loginId": loginID,
			"verificationUrl": "https://example.test/device", "userCode": "TEST-CODE",
		})
	case "account/login/cancel":
		return success(map[string]any{})
	case "thread/start":
		if spec.QuotaOnThreadStart {
			response := protocol.Message{ID: message.ID, Error: &protocol.RPCError{
				Code: -32000, Message: "usage limit exceeded",
				Data: json.RawMessage(`{"codexErrorInfo":"usage_limit_exceeded"}`),
			}}
			return &response
		}
		threadID := spec.ThreadStartID
		if threadID == "" {
			threadID = "thread-" + spec.AccountID
		}
		return success(map[string]any{"thread": map[string]any{"id": threadID}})
	case "thread/list":
		cursor := ""
		var params struct {
			Cursor *string `json:"cursor"`
		}
		_ = json.Unmarshal(message.Params, &params)
		if params.Cursor != nil {
			cursor = *params.Cursor
		}
		page, ok := spec.Pages[cursor]
		if !ok {
			return success(map[string]any{"data": []any{}, "nextCursor": nil})
		}
		if page.Error != "" {
			response := protocol.Failure(message.ID, -32091, page.Error)
			return &response
		}
		return success(map[string]any{"data": page.Threads, "nextCursor": page.NextCursor})
	case "thread/read":
		path := spec.ThreadReadPath
		if path == "" {
			path = filepath.Join(os.TempDir(), spec.AccountID+".jsonl")
		}
		threadID := "migrating-thread"
		var params map[string]any
		if json.Unmarshal(message.Params, &params) == nil {
			if value, ok := params["threadId"].(string); ok {
				threadID = value
			}
		}
		return success(map[string]any{"thread": map[string]any{
			"id": threadID, "path": path, "cwd": os.TempDir(), "modelProvider": "openai",
		}})
	case "thread/resume":
		return success(map[string]any{"thread": map[string]any{"id": "migrating-thread"}})
	case "turn/start":
		return success(map[string]any{"turn": map[string]any{"id": "turn-" + spec.AccountID}})
	default:
		return success(map[string]any{})
	}
}

type lockedOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (output *lockedOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.data.Write(data)
}

func (output *lockedOutput) messages() []protocol.Message {
	output.mu.Lock()
	defer output.mu.Unlock()
	lines := bytes.Split(bytes.TrimSpace(output.data.Bytes()), []byte{'\n'})
	messages := make([]protocol.Message, 0, len(lines))
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		message, err := protocol.Parse(line)
		if err == nil {
			messages = append(messages, message)
		}
	}
	return messages
}

type muxIntegrationHarness struct {
	t       *testing.T
	root    string
	runRoot string
	store   *state.Store
	mux     *Multiplexer
	output  *lockedOutput
	cancel  context.CancelFunc
	closed  atomic.Bool
}

func newMuxIntegrationHarness(
	t *testing.T,
	specs []fakeAppServerSpec,
	timeout time.Duration,
) *muxIntegrationHarness {
	t.Helper()
	if len(specs) == 0 || specs[0].AccountID != "primary" {
		t.Fatal("first fake account must be primary")
	}
	root := t.TempDir()
	runRoot := filepath.Join(root, "fake-runtime")
	primaryHome := filepath.Join(root, "accounts", "primary", "codex-home")
	if err := os.MkdirAll(primaryHome, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(root, "state"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	accounts := []state.Account{store.Accounts()[0]}
	for index := 1; index < len(specs); index++ {
		account, addErr := store.AddAccount(fmt.Sprintf("Account %02d", index+1))
		if addErr != nil {
			t.Fatal(addErr)
		}
		accounts = append(accounts, account)
	}
	for index := range specs {
		specs[index].AccountID = accounts[index].ID
		if err := writeFakeSpec(accounts[index].CodexHome, specs[index]); err != nil {
			t.Fatal(err)
		}
	}
	output := &lockedOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	multiplexer, err := New(Options{
		RealExecutable: os.Args[0],
		RealArgs:       []string{"-test.run=^TestMuxFakeAppServerProcess$"},
		Environment: append(os.Environ(),
			fakeProcessEnvironment+"=1",
			fakeRunRootEnvironment+"="+runRoot,
		),
		Store:          store,
		Output:         output,
		RequestTimeout: timeout,
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	harness := &muxIntegrationHarness{
		t: t, root: root, runRoot: runRoot, store: store,
		mux: multiplexer, output: output, cancel: cancel,
	}
	if err := multiplexer.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(harness.closeAndAssert)
	return harness
}

func (h *muxIntegrationHarness) accounts() []state.Account {
	return h.store.Accounts()
}

func (h *muxIntegrationHarness) request(id int, method string, params any) protocol.Message {
	h.t.Helper()
	raw, err := protocol.MarshalParams(params)
	if err != nil {
		h.t.Fatal(err)
	}
	h.mux.HandleClient(protocol.Request(method, json.RawMessage(strconv.Itoa(id)), raw))
	return h.awaitResponse(id, 5*time.Second)
}

func (h *muxIntegrationHarness) awaitResponse(id int, timeout time.Duration) protocol.Message {
	h.t.Helper()
	key := strconv.Itoa(id)
	var found protocol.Message
	if !awaitCondition(timeout, func() bool {
		for _, message := range h.output.messages() {
			if protocol.RequestIDKey(message.ID) == key && message.Method == "" {
				found = message
				return true
			}
		}
		return false
	}) {
		h.t.Fatalf("timed out waiting for response %d; output=%#v", id, h.output.messages())
	}
	return found
}

func (h *muxIntegrationHarness) closeAndAssert() {
	if !h.closed.CompareAndSwap(false, true) {
		return
	}
	h.cancel()
	h.mux.Close()
	addresses := h.liveAddresses()
	if !awaitCondition(5*time.Second, func() bool {
		for _, address := range addresses {
			connection, err := net.DialTimeout("tcp", address, 25*time.Millisecond)
			if err == nil {
				_ = connection.Close()
				return false
			}
		}
		return true
	}) {
		h.t.Errorf("one or more fake app-server processes survived Multiplexer.Close: %v", addresses)
	}
}

func (h *muxIntegrationHarness) liveAddresses() []string {
	files, _ := filepath.Glob(filepath.Join(h.runRoot, "live", "*.json"))
	addresses := make([]string, 0, len(files))
	for _, path := range files {
		var value struct {
			Address string `json:"address"`
		}
		data, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(data, &value) == nil && value.Address != "" {
			addresses = append(addresses, value.Address)
		}
	}
	return addresses
}

func (h *muxIntegrationHarness) eventCount(accountID, method string) int {
	events := h.events(accountID)
	count := 0
	for _, event := range events {
		if event.Message.Method == method {
			count++
		}
	}
	return count
}

func (h *muxIntegrationHarness) events(accountID string) []fakeEvent {
	files, _ := filepath.Glob(filepath.Join(h.runRoot, "events", safeFilePart(accountID)+"-*.json"))
	events := make([]fakeEvent, 0, len(files))
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var event fakeEvent
		if json.Unmarshal(data, &event) == nil {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Launch != events[j].Launch {
			return events[i].Launch < events[j].Launch
		}
		return events[i].Sequence < events[j].Sequence
	})
	return events
}

func (h *muxIntegrationHarness) launchCount(accountID string) int {
	files, _ := filepath.Glob(filepath.Join(h.runRoot, "launches", safeFilePart(accountID)+"-*.claimed"))
	return len(files)
}

func TestMuxIntegrationThreadStartQuotaRetriesAnotherAccount(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{AccountID: "primary", UsedPercent: 5, QuotaOnThreadStart: true},
		{UsedPercent: 20, ThreadStartID: "fallback-thread"},
	}, defaultIntegrationRequestTimeout)
	accounts := h.accounts()

	response := h.request(1, "thread/start", map[string]any{"cwd": t.TempDir()})
	if response.Error != nil {
		t.Fatalf("thread/start failed instead of retrying: %#v", response.Error)
	}
	if got := threadIDFromResult(response.Result); got != "fallback-thread" {
		t.Fatalf("thread/start returned thread %q, want fallback-thread", got)
	}
	owner, ok := h.store.ThreadOwner("fallback-thread")
	if !ok || owner != accounts[1].ID {
		t.Fatalf("fallback thread owner = %q, %v; want %q", owner, ok, accounts[1].ID)
	}
	if got := h.eventCount(accounts[0].ID, "thread/start"); got != 1 {
		t.Fatalf("depleted account received %d thread/start calls, want 1", got)
	}
	if got := h.eventCount(accounts[1].ID, "thread/start"); got != 1 {
		t.Fatalf("fallback account received %d thread/start calls, want 1", got)
	}
}

func TestMuxIntegrationThreadListDeduplicatesPostFailoverInReverseOrder(t *testing.T) {
	cursor := ""
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{
			AccountID: "primary", UsedPercent: 10,
			DelayMilliseconds: map[string]int{"thread/list": 140},
			Pages: map[string]fakePage{"": {Threads: []map[string]any{{
				"id": "shared", "preview": "stale-source", "updatedAt": 300,
			}}, NextCursor: &cursor}},
		},
		{
			UsedPercent: 20,
			Pages: map[string]fakePage{"": {Threads: []map[string]any{{
				"id": "shared", "preview": "current-target", "updatedAt": 200,
			}}, NextCursor: &cursor}},
		},
	}, defaultIntegrationRequestTimeout)
	accounts := h.accounts()
	if err := h.store.SetThreadOwner("shared", accounts[1].ID); err != nil {
		t.Fatal(err)
	}

	response := h.request(2, "thread/list", map[string]any{"limit": 20})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	var result struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 {
		t.Fatalf("deduplicated list has %d entries, want 1: %#v", len(result.Data), result.Data)
	}
	if result.Data[0]["preview"] != "current-target" {
		t.Fatalf("dedup selected %#v, want current owner copy", result.Data[0])
	}
	owner, _ := h.store.ThreadOwner("shared")
	if owner != accounts[1].ID {
		t.Fatalf("thread/list reassigned post-failover owner to %q", owner)
	}
}

func TestMuxIntegrationChildCrashRestartsAndReinitializes(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{{
		AccountID: "primary", UsedPercent: 10,
		CrashMethod: "test/crash", CrashLaunches: 1,
	}}, 2*time.Second)

	if response := h.request(3, "initialize", map[string]any{"clientInfo": map[string]any{"name": "integration"}}); response.Error != nil {
		t.Fatal(response.Error)
	}
	h.mux.HandleClient(protocol.Message{Method: "initialized"})
	crashResponse := h.request(4, "test/crash", map[string]any{})
	if crashResponse.Error == nil {
		t.Fatalf("crashed route unexpectedly succeeded: %#v", crashResponse)
	}
	if !awaitCondition(6*time.Second, func() bool { return h.launchCount("primary") >= 2 }) {
		t.Fatalf("child did not restart; launches=%d", h.launchCount("primary"))
	}
	if !awaitCondition(6*time.Second, func() bool {
		return h.eventCount("primary", "initialize") >= 2 && h.eventCount("primary", "initialized") >= 2
	}) {
		t.Fatalf("restart did not restore initialization; events=%#v", h.events("primary"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshots := h.mux.Accounts(ctx)
	if len(snapshots) != 1 || !snapshots[0].Connected || snapshots[0].Error != "" {
		t.Fatalf("restarted account did not recover: %#v", snapshots)
	}
}

func TestMuxIntegrationInitializeFailureIsIsolated(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{AccountID: "primary", UsedPercent: 1, InitializeError: true},
		{UsedPercent: 30, ThreadStartID: "healthy-thread"},
	}, defaultIntegrationRequestTimeout)
	accounts := h.accounts()

	if response := h.request(5, "initialize", map[string]any{"capabilities": map[string]any{}}); response.Error != nil {
		t.Fatalf("one failed account prevented pool initialization: %#v", response.Error)
	}
	response := h.request(6, "thread/start", map[string]any{"cwd": t.TempDir()})
	if response.Error != nil || threadIDFromResult(response.Result) != "healthy-thread" {
		t.Fatalf("request routed to failed initializer: %#v", response)
	}
	if got := h.eventCount(accounts[0].ID, "thread/start"); got != 0 {
		t.Fatalf("failed initializer received %d routed requests", got)
	}
}

func TestMuxIntegrationLoginReturnsIDAndRejectsConcurrentStarts(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{
			AccountID: "primary", UsedPercent: 10, Disconnected: true,
			LoginID: "login-primary", DelayMilliseconds: map[string]int{"account/login/start": 150},
		},
		{UsedPercent: 20, Disconnected: true, LoginID: "login-secondary"},
	}, defaultIntegrationRequestTimeout)
	accounts := h.accounts()
	type loginResult struct {
		result json.RawMessage
		err    error
	}
	first := make(chan loginResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		result, err := h.mux.StartLogin(ctx, accounts[0].ID, "chatgptDeviceCode")
		first <- loginResult{result: result, err: err}
	}()
	if !awaitCondition(time.Second, func() bool {
		return h.eventCount(accounts[0].ID, "account/login/start") == 1
	}) {
		t.Fatal("first login did not reach its app-server")
	}
	if _, err := h.mux.StartLogin(ctx, accounts[1].ID, "chatgptDeviceCode"); err == nil ||
		!strings.Contains(err.Error(), "already pending") {
		t.Fatalf("concurrent login for another account was not rejected: %v", err)
	}
	if _, err := h.mux.StartLogin(ctx, accounts[0].ID, "chatgptDeviceCode"); err == nil ||
		!strings.Contains(err.Error(), "already pending") {
		t.Fatalf("second login for same account was not rejected: %v", err)
	}
	received := <-first
	if received.err != nil {
		t.Fatal(received.err)
	}
	var login struct {
		LoginID string `json:"loginId"`
	}
	if err := json.Unmarshal(received.result, &login); err != nil {
		t.Fatal(err)
	}
	if login.LoginID != "login-primary" {
		t.Fatalf("StartLogin returned loginId %q, want login-primary", login.LoginID)
	}
	runtime := h.mux.runtimeState(accounts[0].ID)
	if runtime.loginID != "login-primary" || runtime.status != "pending" {
		t.Fatalf("pending login correlation was not retained: %#v", runtime)
	}
	if got := h.eventCount(accounts[1].ID, "account/login/start"); got != 0 {
		t.Fatalf("rejected account received %d login requests", got)
	}
	if err := h.mux.CancelLogin(ctx, accounts[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestMuxIntegrationCancelLoginSendsExactIDAndEndsDisconnected(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{{
		AccountID: "primary", UsedPercent: 10, Disconnected: true,
		LoginID: "cancel-me", CompleteLoginOnCancel: true,
		LoginCompletionError: "device authorization cancelled",
	}}, defaultIntegrationRequestTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := h.mux.StartLogin(ctx, "primary", "chatgptDeviceCode"); err != nil {
		t.Fatal(err)
	}
	if err := h.mux.CancelLogin(ctx, "primary"); err != nil {
		t.Fatal(err)
	}
	if !awaitCondition(time.Second, func() bool {
		runtime := h.mux.runtimeState("primary")
		return runtime.loginID == "" && runtime.status == "disconnected" && runtime.err == ""
	}) {
		t.Fatalf("voluntary cancellation did not settle disconnected: %#v", h.mux.runtimeState("primary"))
	}
	cancelEvents := make([]fakeEvent, 0, 1)
	for _, event := range h.events("primary") {
		if event.Message.Method == "account/login/cancel" {
			cancelEvents = append(cancelEvents, event)
		}
	}
	if len(cancelEvents) != 1 {
		t.Fatalf("account/login/cancel calls = %d, want exactly 1", len(cancelEvents))
	}
	if got := string(cancelEvents[0].Message.Params); got != `{"loginId":"cancel-me"}` {
		t.Fatalf("cancel params = %s, want exact loginId payload", got)
	}
}

func TestMuxIntegrationFailedLoginCompletionPreservesError(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{{
		AccountID: "primary", UsedPercent: 10, Disconnected: true,
		LoginID: "failed-login", EmitLoginCompletedOn: "test/complete-login",
		LoginCompletionError: "access denied by account policy",
	}}, defaultIntegrationRequestTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := h.mux.StartLogin(ctx, "primary", "chatgptDeviceCode"); err != nil {
		t.Fatal(err)
	}
	if response := h.request(13, "test/complete-login", map[string]any{}); response.Error != nil {
		t.Fatal(response.Error)
	}
	if !awaitCondition(2*time.Second, func() bool {
		runtime := h.mux.runtimeState("primary")
		return runtime.loginID == "" && runtime.status == "error" &&
			runtime.err == "access denied by account policy"
	}) {
		t.Fatalf("failed login error was not preserved: %#v", h.mux.runtimeState("primary"))
	}
}

func TestMuxIntegrationSerializesConcurrentFailoverForOneThread(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{AccountID: "primary", UsedPercent: 100, ThreadReadPath: filepath.Join(t.TempDir(), "thread.jsonl")},
		{UsedPercent: 10, DelayMilliseconds: map[string]int{"thread/resume": 100}},
	}, defaultIntegrationRequestTimeout)
	accounts := h.accounts()
	if err := h.store.SetThreadOwner("migrating-thread", accounts[0].ID); err != nil {
		t.Fatal(err)
	}

	params := map[string]any{"threadId": "migrating-thread", "input": []any{}}
	h.mux.HandleClient(protocol.Request("turn/start", json.RawMessage(`7`), mustJSON(t, params)))
	h.mux.HandleClient(protocol.Request("turn/start", json.RawMessage(`8`), mustJSON(t, params)))
	for _, id := range []int{7, 8} {
		if response := h.awaitResponse(id, 5*time.Second); response.Error != nil {
			t.Fatalf("turn/start %d failed: %#v", id, response.Error)
		}
	}
	if got := h.eventCount(accounts[1].ID, "thread/resume"); got != 1 {
		t.Fatalf("concurrent failover resumed history %d times, want 1", got)
	}
	if got := h.eventCount(accounts[1].ID, "turn/start"); got != 2 {
		t.Fatalf("fallback received %d turns, want 2", got)
	}
	owner, _ := h.store.ThreadOwner("migrating-thread")
	if owner != accounts[1].ID {
		t.Fatalf("thread owner = %q, want %q", owner, accounts[1].ID)
	}
}

func TestMuxIntegrationThreadListHandlesInfiniteSlowAndPartialCursors(t *testing.T) {
	repeat, bad := "repeat", "bad"
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{
			AccountID: "primary", UsedPercent: 10,
			Pages: map[string]fakePage{
				"":       {Threads: []map[string]any{{"id": "infinite-a", "updatedAt": 4}}, NextCursor: &repeat},
				"repeat": {Threads: []map[string]any{{"id": "infinite-b", "updatedAt": 3}}, NextCursor: &repeat},
			},
		},
		{
			UsedPercent: 20, DelayMilliseconds: map[string]int{"thread/list": 120},
			Pages: map[string]fakePage{"": {Threads: []map[string]any{{"id": "slow", "updatedAt": 2}}}},
		},
		{
			UsedPercent: 30,
			Pages: map[string]fakePage{
				"":    {Threads: []map[string]any{{"id": "partial", "updatedAt": 1}}, NextCursor: &bad},
				"bad": {Error: "synthetic second-page failure"},
			},
		},
	}, defaultIntegrationRequestTimeout)
	started := time.Now()
	response := h.request(9, "thread/list", map[string]any{"limit": 20})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("adversarial cursors blocked aggregation for %v", elapsed)
	}
	var result struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(result.Data))
	for _, thread := range result.Data {
		ids = append(ids, thread["id"].(string))
	}
	sort.Strings(ids)
	want := []string{"infinite-a", "infinite-b", "partial", "slow"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("merged thread ids = %v, want %v", ids, want)
	}
	if got := h.eventCount("primary", "thread/list"); got != 2 {
		t.Fatalf("repeated cursor caused %d page requests, want 2", got)
	}
}

func TestMuxIntegrationExpiresUnansweredExternalAndServerRoutes(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{{
		AccountID: "primary", UsedPercent: 10,
		NoResponseMethods:   []string{"test/no-response"},
		EmitServerRequestOn: "test/emit-server-request",
	}}, 100*time.Millisecond)

	response := h.request(10, "test/no-response", map[string]any{})
	if response.Error == nil || response.Error.Code != -32030 {
		t.Fatalf("unanswered external route = %#v, want -32030", response)
	}
	if !awaitCondition(time.Second, func() bool {
		h.mux.externalMu.Lock()
		defer h.mux.externalMu.Unlock()
		return len(h.mux.externalRoutes) == 0
	}) {
		t.Fatal("expired external route remained registered")
	}

	if response := h.request(11, "test/emit-server-request", map[string]any{}); response.Error != nil {
		t.Fatal(response.Error)
	}
	if !awaitCondition(time.Second, func() bool {
		for _, message := range h.output.messages() {
			if message.Method == "test/server/request" && len(message.ID) > 0 {
				return true
			}
		}
		return false
	}) {
		t.Fatal("fake server request was not forwarded")
	}
	if !awaitCondition(2*time.Second, func() bool {
		for _, event := range h.events("primary") {
			if event.Message.Method == "" && event.Message.Error != nil && event.Message.Error.Code == -32031 {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("expired server request was not failed back to child: %#v", h.events("primary"))
	}
	h.mux.serverMu.Lock()
	defer h.mux.serverMu.Unlock()
	if len(h.mux.serverRoutes) != 0 {
		t.Fatalf("expired server routes remain registered: %d", len(h.mux.serverRoutes))
	}
}

func TestMuxIntegrationTwentyAccountsInitializeCloseWithoutLeaks(t *testing.T) {
	baselineGoroutines := runtime.NumGoroutine()
	specs := make([]fakeAppServerSpec, 20)
	for index := range specs {
		specs[index] = fakeAppServerSpec{UsedPercent: float64(index + 1)}
	}
	specs[0].AccountID = "primary"
	h := newMuxIntegrationHarness(t, specs, defaultIntegrationRequestTimeout)

	if response := h.request(12, "initialize", map[string]any{"clientInfo": map[string]any{"name": "scale-test"}}); response.Error != nil {
		t.Fatal(response.Error)
	}
	h.mux.HandleClient(protocol.Message{Method: "initialized"})
	if !awaitCondition(5*time.Second, func() bool {
		for _, account := range h.accounts() {
			if h.eventCount(account.ID, "initialize") != 1 || h.eventCount(account.ID, "initialized") != 1 {
				return false
			}
		}
		return true
	}) {
		t.Fatal("not all 20 accounts completed initialization")
	}
	h.closeAndAssert()
	runtime.GC()
	if !awaitCondition(5*time.Second, func() bool {
		return runtime.NumGoroutine() <= baselineGoroutines+6
	}) {
		t.Fatalf("goroutines did not settle after close: baseline=%d current=%d", baselineGoroutines, runtime.NumGoroutine())
	}
}

func TestMuxIntegrationConcurrentConfigAndRoutingState(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{
		{AccountID: "primary", UsedPercent: 10},
		{UsedPercent: 20},
	}, defaultIntegrationRequestTimeout)
	accounts := h.accounts()
	var wait sync.WaitGroup
	errCh := make(chan error, 200)
	for worker := 0; worker < 8; worker++ {
		worker := worker
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 25; iteration++ {
				label := fmt.Sprintf("Concurrent-%02d-%02d", worker, iteration)
				if _, err := h.store.UpdateAccount(accounts[1].ID, &label, nil); err != nil {
					errCh <- err
				}
				if err := h.store.SetThreadOwner(fmt.Sprintf("thread-%02d-%02d", worker, iteration), accounts[worker%2].ID); err != nil {
					errCh <- err
				}
				if err := h.store.SyncManagedConfig(); err != nil {
					errCh <- err
				}
			}
		}()
	}
	wait.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent state/config operation: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(h.store.Root(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("concurrent writes corrupted state.json: %q", data)
	}
	reopened, err := state.Open(h.store.Root(), accounts[0].CodexHome)
	if err != nil {
		t.Fatalf("persisted concurrent state cannot be reopened: %v", err)
	}
	if got := len(reopened.ThreadCounts()); got != 2 {
		t.Fatalf("thread ownership was lost for one account: %#v", reopened.ThreadCounts())
	}
}

func readFakeSpec(home string) (fakeAppServerSpec, error) {
	data, err := os.ReadFile(filepath.Join(home, "fake-app-server.json"))
	if err != nil {
		return fakeAppServerSpec{}, err
	}
	var spec fakeAppServerSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return fakeAppServerSpec{}, err
	}
	return spec, nil
}

func writeFakeSpec(home string, spec fakeAppServerSpec) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	path := filepath.Join(home, "fake-app-server.json")
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func claimFakeLaunch(runRoot, accountID string) (int, error) {
	directory := filepath.Join(runRoot, "launches")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return 0, err
	}
	for launch := 1; launch <= 1_000; launch++ {
		path := filepath.Join(directory, fmt.Sprintf("%s-%06d.claimed", safeFilePart(accountID), launch))
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_ = file.Close()
			return launch, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return 0, err
		}
	}
	return 0, errors.New("too many fake app-server launches")
}

func recordFakeLiveness(runRoot, accountID string, launch int, address string) error {
	directory := filepath.Join(runRoot, "live")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{
		"accountId": accountID, "launch": launch, "pid": os.Getpid(), "address": address,
	})
	return os.WriteFile(filepath.Join(directory, fmt.Sprintf(
		"%s-%06d.json", safeFilePart(accountID), launch,
	)), data, 0o600)
}

func recordFakeEvent(runRoot string, event fakeEvent) error {
	directory := filepath.Join(runRoot, "events")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	path := filepath.Join(directory, fmt.Sprintf(
		"%s-%06d-%06d-%d.json",
		safeFilePart(event.AccountID), event.Launch, event.Sequence, os.Getpid(),
	))
	return os.WriteFile(path, data, 0o600)
}

func safeFilePart(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func stringInSlice(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func awaitCondition(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return condition()
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
