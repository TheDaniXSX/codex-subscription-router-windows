package broker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
)

type messageWriter struct{ messages chan protocol.Message }

func (w *messageWriter) Write(data []byte) (int, error) {
	m, err := protocol.Parse(data)
	if err != nil {
		return 0, err
	}
	w.messages <- m
	return len(data), nil
}

type fixture struct {
	t      *testing.T
	h      *Hub
	native chan protocol.Message
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, h: New(), native: make(chan protocol.Message, 2000)}
	f.h.SetHandler(func(m protocol.Message) { f.native <- m })
	return f
}
func (f *fixture) client(id string) (*Client, chan protocol.Message) {
	f.t.Helper()
	w := &messageWriter{messages: make(chan protocol.Message, 2000)}
	c, err := f.h.AddClient(id, w)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(c.Close)
	return c, w.messages
}
func take(t *testing.T, messages <-chan protocol.Message) protocol.Message {
	t.Helper()
	select {
	case m := <-messages:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("message timed out")
		return protocol.Message{}
	}
}
func empty(t *testing.T, messages <-chan protocol.Message) {
	t.Helper()
	select {
	case m := <-messages:
		t.Fatalf("unexpected message: %+v", m)
	case <-time.After(15 * time.Millisecond):
	}
}
func send(t *testing.T, c *Client, m protocol.Message) {
	t.Helper()
	if err := c.Send(m); err != nil {
		t.Fatal(err)
	}
}
func request(method, id, params string) protocol.Message {
	return protocol.Request(method, json.RawMessage(id), json.RawMessage(params))
}
func (f *fixture) write(m protocol.Message) {
	f.t.Helper()
	data, err := protocol.Encode(m)
	if err != nil {
		f.t.Fatal(err)
	}
	data = append(data, '\n')
	if n, err := f.h.Write(data); err != nil || n != len(data) {
		f.t.Fatalf("Write = %d, %v", n, err)
	}
}
func (f *fixture) note(method, params string) {
	f.write(protocol.Message{Method: method, Params: json.RawMessage(params)})
}
func (f *fixture) ready(id string) (*Client, chan protocol.Message) {
	f.t.Helper()
	c, out := f.client(id)
	first := f.h.initResult == nil
	send(f.t, c, request("initialize", "0", `{"clientInfo":{"name":"test","version":"1"}}`))
	if first {
		req := take(f.t, f.native)
		f.write(protocol.Success(req.ID, json.RawMessage(`{"userAgent":"native"}`)))
	}
	if got := take(f.t, out); string(got.ID) != "0" || got.Error != nil {
		f.t.Fatalf("initialize: %+v", got)
	}
	send(f.t, c, protocol.Message{Method: "initialized"})
	if first {
		take(f.t, f.native)
	}
	return c, out
}
func (f *fixture) start(c *Client, out <-chan protocol.Message, thread, turn string) {
	f.t.Helper()
	send(f.t, c, request("turn/start", `"start"`, fmt.Sprintf(`{"threadId":%q}`, thread)))
	req := take(f.t, f.native)
	f.write(protocol.Success(req.ID, json.RawMessage(fmt.Sprintf(`{"turn":{"id":%q,"status":"inProgress"}}`, turn))))
	if m := take(f.t, out); m.Error != nil {
		f.t.Fatalf("start: %+v", m)
	}
}

func TestPrivateResponsesRestoreCollidingClientIDs(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	b, bo := f.ready("dev")
	send(t, a, request("thread/list", "7", `{}`))
	send(t, b, request("thread/list", "7", `{}`))
	ar, br := take(t, f.native), take(t, f.native)
	if string(ar.ID) == string(br.ID) {
		t.Fatal("request IDs collided")
	}
	f.write(protocol.Success(br.ID, json.RawMessage(`{"source":"dev"}`)))
	f.write(protocol.Success(ar.ID, json.RawMessage(`{"source":"prod"}`)))
	for _, tc := range []struct {
		out    <-chan protocol.Message
		source string
	}{{ao, "prod"}, {bo, "dev"}} {
		m := take(t, tc.out)
		if string(m.ID) != "7" || !strings.Contains(string(m.Result), tc.source) {
			t.Fatalf("crossed response: %+v", m)
		}
	}
	if !f.h.Idle() {
		t.Fatal(f.h.Activity())
	}
}

func TestInitializeCoalescingCapabilitiesAndRecovery(t *testing.T) {
	f := newFixture(t)
	a, ao := f.client("prod")
	b, bo := f.client("dev")
	send(t, a, request("initialize", "1", `{"clientInfo":{"name":"a"},"capabilities":{"optOutNotificationMethods":["b","a","a"]}}`))
	ar := take(t, f.native)
	send(t, b, request("initialize", "2", `{"clientInfo":{"name":"b"},"capabilities":{"experimentalApi":false,"optOutNotificationMethods":["a","b"]}}`))
	empty(t, f.native)
	f.write(protocol.Failure(ar.ID, -1, "native unavailable"))
	if take(t, ao).Error == nil || take(t, bo).Error == nil {
		t.Fatal("initialize failure lost")
	}
	send(t, a, request("initialize", "3", `{"capabilities":{"experimentalApi":true}}`))
	ar = take(t, f.native)
	f.write(protocol.Success(ar.ID, json.RawMessage(`{"userAgent":"ready"}`)))
	take(t, ao)
	send(t, b, request("initialize", "4", `{"capabilities":{"experimentalApi":false}}`))
	if got := take(t, bo); got.Error == nil || got.Error.Code != ErrIncompatibleInitialization {
		t.Fatalf("capabilities accepted: %+v", got)
	}
	send(t, b, request("initialize", "5", `{"capabilities":{"experimentalApi":true}}`))
	if got := take(t, bo); got.Error != nil || string(got.ID) != "5" {
		t.Fatalf("cached initialization: %+v", got)
	}
	send(t, a, protocol.Message{Method: "initialized"})
	send(t, b, protocol.Message{Method: "initialized"})
	if take(t, f.native).Method != "initialized" {
		t.Fatal("missing initialized")
	}
	empty(t, f.native)
}

func TestNotificationsBroadcastOnlyToInitializedClients(t *testing.T) {
	f := newFixture(t)
	_, ao := f.ready("prod")
	_, bo := f.ready("dev")
	_, cold := f.client("cold")
	f.note("account/updated", `{"plan":"pro"}`)
	if take(t, ao).Method != "account/updated" || take(t, bo).Method != "account/updated" {
		t.Fatal("missing broadcast")
	}
	empty(t, cold)
}

func TestApprovalOwnerSpoofAndResolution(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	b, bo := f.ready("dev")
	f.start(a, ao, "chat", "turn")
	f.write(request("item/commandExecution/requestApproval", `"mux:approval"`, `{"threadId":"chat","turnId":"turn"}`))
	approval := take(t, ao)
	empty(t, bo)
	if string(approval.ID) == `"mux:approval"` {
		t.Fatal("server ID not isolated")
	}
	if err := b.Send(protocol.Success(approval.ID, nil)); err == nil {
		t.Fatal("other client answered approval")
	}
	send(t, a, protocol.Success(approval.ID, json.RawMessage(`{"decision":"accept"}`)))
	if got := take(t, f.native); string(got.ID) != `"mux:approval"` {
		t.Fatalf("wrong upstream ID: %+v", got)
	}
	if f.h.Activity().PendingApprovals != 0 {
		t.Fatal("answered approval counted pending")
	}
	if err := a.Send(protocol.Success(approval.ID, nil)); err == nil {
		t.Fatal("duplicate approval accepted")
	}
	f.note("serverRequest/resolved", `{"threadId":"chat","requestId":"mux:approval","extra":true}`)
	resolved := take(t, ao)
	var params map[string]json.RawMessage
	_ = json.Unmarshal(resolved.Params, &params)
	if string(params["requestId"]) != string(approval.ID) || string(params["extra"]) != "true" {
		t.Fatalf("resolved mapping: %s", resolved.Params)
	}
	empty(t, bo)
}

func TestDisconnectFailsApprovalAndReconnectDoesNotInherit(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	f.start(a, ao, "chat", "turn")
	f.write(request("approval", `1`, `{"threadId":"chat","turnId":"turn"}`))
	take(t, ao)
	a.Close()
	select {
	case <-a.Done():
	default:
		t.Fatal("Done not closed")
	}
	if got := take(t, f.native); got.Error == nil || got.Error.Code != ErrApprovalOwnerGone || string(got.ID) != "1" {
		t.Fatalf("disconnect: %+v", got)
	}
	b, bo := f.ready("prod")
	send(t, b, request("turn/start", "2", `{"threadId":"chat"}`))
	if got := take(t, bo); got.Error == nil || got.Error.Code != ErrThreadBusy {
		t.Fatalf("reconnect stole turn: %+v", got)
	}
	f.write(request("approval", `3`, `{"threadId":"chat","turnId":"turn"}`))
	if got := take(t, f.native); got.Error == nil || got.Error.Code != ErrApprovalOwnerGone {
		t.Fatalf("approval inherited: %+v", got)
	}
	f.note("turn/completed", `{"threadId":"chat","turn":{"id":"turn"}}`)
	take(t, bo)
	f.start(b, bo, "chat", "new")
}

func TestBusyMutationsReadDoesNotStealAndStaleCompletion(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	b, bo := f.ready("dev")
	f.start(a, ao, "chat", "old")
	for _, method := range []string{"turn/start", "turn/steer", "turn/interrupt", "thread/archive", "thread/rollback", "thread/compact/start"} {
		send(t, b, request(method, "2", `{"threadId":"chat"}`))
		if got := take(t, bo); got.Error == nil || got.Error.Code != ErrThreadBusy {
			t.Fatalf("%s not blocked: %+v", method, got)
		}
	}
	if err := b.Send(protocol.Message{Method: "turn/interrupt", Params: json.RawMessage(`{"threadId":"chat"}`)}); err == nil {
		t.Fatal("notification bypassed lease")
	}
	send(t, b, request("thread/resume", "3", `{"threadId":"chat"}`))
	req := take(t, f.native)
	f.write(protocol.Success(req.ID, json.RawMessage(`{"thread":{"id":"chat"}}`)))
	take(t, bo)
	f.write(request("approval", "9", `{"threadId":"chat","turnId":"old"}`))
	take(t, ao)
	f.note("turn/completed", `{"threadId":"chat","turn":{"id":"old"}}`)
	take(t, ao)
	take(t, bo)
	f.start(b, bo, "chat", "new")
	f.note("turn/started", `{"threadId":"chat","turn":{"id":"old"}}`)
	take(t, ao)
	take(t, bo)
	f.note("turn/completed", `{"threadId":"chat","turn":{"id":"old"}}`)
	take(t, ao)
	take(t, bo)
	f.write(request("approval", "10", `{"threadId":"chat","turnId":"new"}`))
	take(t, bo)
	empty(t, ao)
	if f.h.Activity().ActiveThreads != 1 {
		t.Fatal(f.h.Activity())
	}
}

func TestUnknownTurnApprovalFailsClosedAndBlocksMutation(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	f.note("turn/started", `{"threadId":"external","turn":{"id":"unknown"}}`)
	take(t, ao)
	if f.h.Idle() || f.h.Activity().ActiveThreads != 1 {
		t.Fatal("unknown live activity reported idle")
	}
	f.write(request("approval", "1", `{"threadId":"external","turnId":"unknown"}`))
	if got := take(t, f.native); got.Error == nil || got.Error.Code != ErrUnknownApprovalOwner {
		t.Fatalf("unknown owner: %+v", got)
	}
	send(t, a, request("turn/start", "2", `{"threadId":"external"}`))
	if got := take(t, ao); got.Error == nil || got.Error.Code != ErrThreadBusy {
		t.Fatalf("unknown live turn mutated: %+v", got)
	}
}

func TestNestedOutOfOrderDescendantsKeepDetachedOwnership(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	_, bo := f.ready("dev")
	send(t, a, request("turn/start", "1", `{"threadId":"root"}`))
	rootReq := take(t, f.native)
	for _, n := range []struct{ method, params string }{
		{"turn/started", `{"threadId":"grand","turn":{"id":"gt"}}`},
		{"turn/started", `{"threadId":"child","turn":{"id":"ct"}}`},
		{"item/completed", `{"threadId":"child","turnId":"ct","item":{"id":"nested","type":"subAgentActivity","kind":"started","agentThreadId":"grand"}}`},
		{"turn/completed", `{"threadId":"child","turn":{"id":"ct"}}`},
		{"item/started", `{"threadId":"root","turnId":"rt","item":{"id":"spawn","type":"collabAgentToolCall","tool":"spawnAgent","senderThreadId":"root","receiverThreadIds":[]}}`},
		{"item/completed", `{"threadId":"root","turnId":"rt","item":{"id":"spawn","type":"collabAgentToolCall","tool":"spawnAgent","senderThreadId":"root","receiverThreadIds":["child"]}}`},
		{"turn/completed", `{"threadId":"root","turn":{"id":"rt"}}`},
	} {
		f.note(n.method, n.params)
		take(t, ao)
		take(t, bo)
	}
	f.write(protocol.Success(rootReq.ID, json.RawMessage(`{"turn":{"id":"rt","status":"completed"}}`)))
	take(t, ao)
	for _, pair := range [][2]string{{"grand", "gt"}} {
		f.write(request("approval", `"native"`, fmt.Sprintf(`{"threadId":%q,"turnId":%q}`, pair[0], pair[1])))
		approval := take(t, ao)
		send(t, a, protocol.Success(approval.ID, nil))
		take(t, f.native)
	}
	empty(t, bo)
	if f.h.Activity().ActiveThreads != 1 {
		t.Fatal(f.h.Activity())
	}
}

func TestSourceOnlyDoesNotGiveSubagentOwnership(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	f.start(a, ao, "root", "rt")
	f.note("thread/started", `{"thread":{"id":"child","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"root"}}}}}`)
	take(t, ao)
	f.note("turn/started", `{"threadId":"child","turn":{"id":"ct"}}`)
	take(t, ao)
	f.write(request("approval", "1", `{"threadId":"child","turnId":"ct"}`))
	if got := take(t, f.native); got.Error == nil || got.Error.Code != ErrUnknownApprovalOwner {
		t.Fatalf("parent-only edge stole ownership: %+v", got)
	}
}

func TestOldSubagentCompletionCannotCloseLaterUserTurn(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	b, bo := f.ready("dev")
	f.start(a, ao, "root", "rt")
	for _, n := range []struct{ method, params string }{
		{"item/completed", `{"threadId":"root","turnId":"rt","item":{"id":"spawn","type":"subAgentActivity","kind":"started","agentThreadId":"child"}}`},
		{"turn/started", `{"threadId":"child","turn":{"id":"ct"}}`},
		{"turn/completed", `{"threadId":"child","turn":{"id":"ct"}}`},
	} {
		f.note(n.method, n.params)
		take(t, ao)
		take(t, bo)
	}
	f.start(b, bo, "child", "user")
	f.note("item/completed", `{"threadId":"root","turnId":"rt","item":{"id":"late-completed","type":"subAgentActivity","kind":"completed","agentThreadId":"child"}}`)
	take(t, ao)
	take(t, bo)
	f.write(request("approval", "7", `{"threadId":"child","turnId":"user"}`))
	take(t, bo)
	empty(t, ao)
}

func TestFragmentedOutputAndReentrantHandler(t *testing.T) {
	f := newFixture(t)
	_, ao := f.ready("prod")
	for _, part := range []string{"{\"method\":\"one\",", "\"params\":{}}\n{\"method\":\"two\"}\n"} {
		if _, err := f.h.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if take(t, ao).Method != "one" || take(t, ao).Method != "two" {
		t.Fatal("fragment framing")
	}
	f.h.SetHandler(func(m protocol.Message) { f.h.Write([]byte("{\"method\":\"reentrant\"}\n")) })
	done := make(chan struct{})
	go func() {
		f.h.Write([]byte("{\"id\":1,\"method\":\"approval\",\"params\":{\"threadId\":\"unknown\"}}\n"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reentrant Write deadlock")
	}
	if take(t, ao).Method != "reentrant" {
		t.Fatal("reentrant response lost")
	}
}

type blockedWriter struct {
	closed chan struct{}
	once   sync.Once
}

func (w *blockedWriter) Write(data []byte) (int, error) { <-w.closed; return 0, io.ErrClosedPipe }
func (w *blockedWriter) Close() error                   { w.once.Do(func() { close(w.closed) }); return nil }

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("failed") }

func TestOutputFailureAndBackpressureCloseOnlyAffectedClient(t *testing.T) {
	f := newFixture(t)
	_, ao := f.ready("healthy")
	bad, err := f.h.AddClient("failed", failedWriter{})
	if err != nil {
		t.Fatal(err)
	}
	bad.emit(protocol.Success(json.RawMessage("1"), nil))
	select {
	case <-bad.Done():
	case <-time.After(time.Second):
		t.Fatal("write error did not disconnect")
	}
	w := &blockedWriter{closed: make(chan struct{})}
	blocked, err := f.h.AddClient("blocked", w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(blocked.Close)
	for i := 0; i < clientQueueSize+2; i++ {
		blocked.emit(protocol.Success(json.RawMessage("1"), nil))
	}
	select {
	case <-blocked.Done():
	case <-time.After(time.Second):
		t.Fatal("overflow did not disconnect")
	}
	f.note("healthy", `{}`)
	if take(t, ao).Method != "healthy" {
		t.Fatal("healthy client blocked")
	}
}

func TestConcurrentClientRequestsAndDisconnects(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	b, bo := f.ready("dev")
	var wg sync.WaitGroup
	for _, c := range []*Client{a, b} {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = c.Send(request("thread/list", fmt.Sprint(i+1), `{}`))
			}
		}(c)
	}
	wg.Wait()
	for i := 0; i < 100; i++ {
		req := take(t, f.native)
		f.write(protocol.Success(req.ID, nil))
	}
	for i := 0; i < 50; i++ {
		take(t, ao)
		take(t, bo)
	}
	wg.Add(2)
	go func() { defer wg.Done(); a.Close() }()
	go func() { defer wg.Done(); b.Close() }()
	wg.Wait()
	if !f.h.Idle() || f.h.Activity().Clients != 0 {
		t.Fatal(f.h.Activity())
	}
}

func TestInputValidation(t *testing.T) {
	for _, raw := range []string{"null", "true", "1.5", "1 2", `{}","`, "9223372036854775808"} {
		if validRequestID(json.RawMessage(raw)) {
			t.Fatalf("accepted ID %q", raw)
		}
	}
	for _, raw := range []string{"1", `"abc"`} {
		if !validRequestID(json.RawMessage(raw)) {
			t.Fatalf("rejected ID %q", raw)
		}
	}
	for _, raw := range []string{`{"capabilities":{"experimentalApi":"yes"}}`, `{"capabilities":{"optOutNotificationMethods":[1]}}`} {
		if _, err := initializationFingerprint(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted capabilities %s", raw)
		}
	}
	a, err := initializationFingerprint(json.RawMessage(`{"capabilities":{"optOutNotificationMethods":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := initializationFingerprint(json.RawMessage(`{}`))
	if err != nil || a != b {
		t.Fatal("default/null capabilities differ")
	}
}

func TestDuplicateIDAndFailedSecondStartPreserveFirstTurn(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	f.start(a, ao, "chat", "first")
	send(t, a, request("turn/start", "2", `{"threadId":"chat"}`))
	req := take(t, f.native)
	send(t, a, request("thread/list", "2", `{}`))
	if got := take(t, ao); got.Error == nil || got.Error.Code != ErrInvalidClientMessage {
		t.Fatalf("duplicate request: %+v", got)
	}
	f.write(protocol.Failure(req.ID, -1, "already active"))
	take(t, ao)
	f.write(request("approval", "4", `{"threadId":"chat","turnId":"first"}`))
	approval := take(t, ao)
	f.note("serverRequest/resolved", `{"threadId":"chat","requestId":4}`)
	take(t, ao)
	if err := a.Send(protocol.Success(approval.ID, nil)); err == nil {
		t.Fatal("resolved approval accepted late answer")
	}
	if f.h.Activity().ActiveThreads != 1 || f.h.Activity().PendingApprovals != 0 {
		t.Fatal(f.h.Activity())
	}
}

func TestDirectTurnInAgentChatRemainsRootForSameClient(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	f.start(a, ao, "root", "rt")
	f.note("item/completed", `{"threadId":"root","turnId":"rt","item":{"id":"spawn","type":"subAgentActivity","kind":"started","agentThreadId":"child"}}`)
	take(t, ao)
	// The desktop explicitly starts work while the child's activity hint exists.
	send(t, a, request("turn/start", "9", `{"threadId":"child"}`))
	req := take(t, f.native)
	f.note("turn/started", `{"threadId":"child","turn":{"id":"direct"}}`)
	take(t, ao)
	f.write(protocol.Success(req.ID, json.RawMessage(`{"turn":{"id":"direct","status":"inProgress"}}`)))
	take(t, ao)
	f.note("item/completed", `{"threadId":"root","turnId":"rt","item":{"id":"late","type":"subAgentActivity","kind":"completed","agentThreadId":"child"}}`)
	take(t, ao)
	f.write(request("approval", "10", `{"threadId":"child","turnId":"direct"}`))
	if got := take(t, ao); got.Method != "approval" {
		t.Fatalf("explicit root closed: %+v", got)
	}
}

func TestSessionRequestsElectOnlyForNewRequests(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	b, bo := f.ready("dev")
	f.write(request("attestation/generate", `"attest-old"`, `{}`))
	old := take(t, ao)
	empty(t, bo)
	if err := b.Send(protocol.Success(old.ID, nil)); err == nil {
		t.Fatal("global request answered by other desktop")
	}
	a.Close()
	if got := take(t, f.native); got.Error == nil || got.Error.Code != ErrApprovalOwnerGone || string(got.ID) != `"attest-old"` {
		t.Fatalf("old request was transferred: %+v", got)
	}
	empty(t, bo)
	f.write(request("attestation/generate", `"attest-new"`, `{}`))
	current := take(t, bo)
	send(t, b, protocol.Success(current.ID, json.RawMessage(`{"token":"test"}`)))
	if got := take(t, f.native); string(got.ID) != `"attest-new"` {
		t.Fatalf("new request did not elect connected client: %+v", got)
	}
	f.write(request("account/chatgptAuthTokens/refresh", `"refresh"`, `{"reason":"unauthorized"}`))
	if got := take(t, bo); got.Method != "account/chatgptAuthTokens/refresh" {
		t.Fatal(got)
	}
	f.write(request("unknown/globalApproval", "99", `{}`))
	if got := take(t, f.native); got.Error == nil || got.Error.Code != ErrUnknownApprovalOwner {
		t.Fatalf("unknown global RPC got an owner: %+v", got)
	}
}

func TestLegacyConversationApprovalUsesActiveOwner(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	_, bo := f.ready("dev")
	f.start(a, ao, "chat", "turn")
	f.write(request("applyPatchApproval", "1", `{"conversationId":"chat","callId":"call"}`))
	if got := take(t, ao); got.Method != "applyPatchApproval" {
		t.Fatal(got)
	}
	empty(t, bo)
}
