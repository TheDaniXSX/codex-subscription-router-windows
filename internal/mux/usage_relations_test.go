package mux

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/backend"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/usage"
)

func usageRelationsTestMux(t *testing.T, path string) *Multiplexer {
	t.Helper()
	s, err := newUsageRelationsState(path)
	if err != nil {
		t.Fatal(err)
	}
	return &Multiplexer{usageRelations: s}
}

func usageRelationsNotify(m *Multiplexer, method, params string) {
	m.trackUsageNotification(backend.Inbound{AccountID: "account", Message: protocol.Message{Method: method, Params: json.RawMessage(params)}})
}

func usageRelationsRoot(m *Multiplexer) {
	usageRelationsNotify(m, "thread/started", `{"thread":{"id":"root","source":"appServer"}}`)
}

func usageRelationsClientTurn(m *Multiplexer, threadID, turnID string) {
	params, _ := json.Marshal(map[string]string{"threadId": threadID})
	result, _ := json.Marshal(map[string]any{"turn": map[string]string{"id": turnID, "status": "inProgress"}})
	m.trackUsageClientResponse(protocol.Message{Method: "turn/start", Params: params}, protocol.Message{Result: result})
}

func TestUsageRelationsRootRequiresExactTurnIdentity(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	usageRelationsNotify(m, "turn/started", `{"threadId":"root","turn":{"id":"recent-turn"}}`)
	cases := []struct {
		name         string
		record       usage.Record
		parent, want string
	}{
		{"main", usage.Record{RequestID: "r1", ThreadID: "root", TurnID: "explicit"}, "", "root:explicit"},
		{"no recent-turn fallback", usage.Record{RequestID: "r2", ThreadID: "root"}, "", ""},
		{"parent thread alone", usage.Record{RequestID: "r3", ThreadID: "child", TurnID: "t"}, "root", ""},
		{"subagent flag alone", usage.Record{RequestID: "r4", ThreadID: "child2", TurnID: "t", Subagent: true}, "", ""},
		{"invalid identity", usage.Record{RequestID: "r5", ThreadID: "root:other", TurnID: "t"}, "", ""},
		{"missing subagent flag is not root proof", usage.Record{RequestID: "r6", ThreadID: "unseen", TurnID: "t"}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m.associateUsageRecord(&tc.record, tc.parent)
			if tc.record.RootTurnID != tc.want {
				t.Fatalf("root = %q; want %q", tc.record.RootTurnID, tc.want)
			}
		})
	}
}

func TestUsageRelationsNestedOutOfOrderExplicitItems(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	r := usage.Record{RequestID: "request-c", ThreadID: "child-c", TurnID: "tc", ParentThreadID: "child-b", Subagent: true}
	m.associateUsageRecord(&r, "")
	usageRelationsNotify(m, "item/completed", `{"threadId":"child-b","turnId":"tb","item":{"type":"collabAgentToolCall","tool":"spawnAgent","senderThreadId":"child-b","receiverThreadIds":["child-c"]}}`)
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "" {
		t.Fatalf("unqualified ancestor was accepted: %q", r.RootTurnID)
	}
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"child-b","prompt":"must-not-be-saved"}}`)
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "root:tr" || r.AgentID != "child-c" {
		t.Fatalf("nested relation = %+v", r)
	}
	if len(m.usageRelations.Turns) != 2 {
		t.Fatalf("expected one stable link per child turn: %+v", m.usageRelations.Turns)
	}
}

func TestUsageRelationsNativeThreadSourceDoesNotInventParentTurn(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	usageRelationsNotify(m, "thread/started", `{"thread":{"id":"agent","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"root","depth":1}}}}}`)
	r := usage.Record{RequestID: "request", ThreadID: "agent", TurnID: "child-turn"}
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "" {
		t.Fatalf("source with parent thread cannot identify parent turn: %+v", r)
	}
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"parent-turn","item":{"type":"collabAgentToolCall","tool":"spawnAgent","senderThreadId":"different-thread","receiverThreadIds":["agent"]}}`)
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "" {
		t.Fatal("accepted mismatched sender")
	}
	usageRelationsNotify(m, "turn/completed", `{"threadId":"root","turn":{"id":"parent-turn","items":[{"type":"collabAgentToolCall","tool":"spawnAgent","senderThreadId":"root","receiverThreadIds":["agent"]}]}}`)
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "root:parent-turn" {
		t.Fatalf("explicit completed-turn item did not resolve: %+v", r)
	}
}

func TestUsageRelationsReusedAgentBecomesUnassociated(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"first","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"agent"}}`)
	first := usage.Record{RequestID: "first-request", ThreadID: "agent", TurnID: "t", Subagent: true}
	m.associateUsageRecord(&first, "root")
	if first.RootTurnID != "root:first" {
		t.Fatal(first.RootTurnID)
	}
	usageRelationsNotify(m, "item/started", `{"threadId":"root","turnId":"second","item":{"type":"collabAgentToolCall","tool":"sendInput","senderThreadId":"root","receiverThreadIds":["agent"]}}`)
	next := usage.Record{RequestID: "next-request", ThreadID: "agent", TurnID: "t", Subagent: true}
	m.associateUsageRecord(&next, "root")
	if next.RootTurnID != "" {
		t.Fatal("new operation of reused agent was assigned to old root", next.RootTurnID)
	}
	m.associateUsageRecord(&first, "root")
	if first.RootTurnID != "root:first" {
		t.Fatal("already attributed request was rewritten")
	}
}

func TestUsageRelationsIgnoreWaitCompletionAndCycles(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"t","item":{"type":"collabAgentToolCall","tool":"wait","senderThreadId":"root","receiverThreadIds":["a"]}}`)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"t","item":{"type":"subAgentActivity","kind":"completed","agentThreadId":"a"}}`)
	if len(m.usageRelations.Threads) != 1 {
		t.Fatal("wait/completion created parent links")
	}
	usageRelationsNotify(m, "item/completed", `{"threadId":"a","turnId":"ta","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"b"}}`)
	usageRelationsNotify(m, "item/completed", `{"threadId":"b","turnId":"tb","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"a"}}`)
	r := usage.Record{RequestID: "request", ThreadID: "a", TurnID: "ta", Subagent: true}
	m.associateUsageRecord(&r, "b")
	if r.RootTurnID != "" {
		t.Fatal("cyclic relation resolved")
	}
}

func TestUsageRelationsAmbiguousAncestorInvalidatesNewDescendantRequests(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"child"}}`)
	usageRelationsNotify(m, "item/completed", `{"threadId":"child","turnId":"tc","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"grandchild"}}`)
	r := usage.Record{RequestID: "one", ThreadID: "grandchild", TurnID: "tg", Subagent: true}
	m.associateUsageRecord(&r, "child")
	if r.RootTurnID != "root:tr" {
		t.Fatal(r.RootTurnID)
	}
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"another-turn","item":{"type":"collabAgentToolCall","tool":"sendInput","senderThreadId":"root","receiverThreadIds":["child"]}}`)
	next := usage.Record{RequestID: "two", ThreadID: "grandchild", TurnID: "tg", Subagent: true}
	m.associateUsageRecord(&next, "child")
	if next.RootTurnID != "" {
		t.Fatalf("cached child turn hid ambiguous ancestor: %q", next.RootTurnID)
	}
}

func TestUsageRelationsFinishPreservesAsyncBindingAfterAgentReuse(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	inflight := usage.Record{RequestID: "inflight", ThreadID: "child", TurnID: "tc", Subagent: true}
	m.associateUsageRecord(&inflight, "root")
	if inflight.RootTurnID != "" {
		t.Fatal("request should begin pending")
	}
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"first","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"child"}}`)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"second","item":{"type":"collabAgentToolCall","tool":"sendInput","senderThreadId":"root","receiverThreadIds":["child"]}}`)
	inflight.FinishedAt = time.Now()
	m.associateUsageRecord(&inflight, "root")
	if inflight.RootTurnID != "root:first" {
		t.Fatalf("Finish erased an asynchronous explicit binding: %q", inflight.RootTurnID)
	}
	if _, ok := m.usageRelations.Pending[inflight.RequestID]; ok {
		t.Fatal("finished resolved request remains pending")
	}
}

func TestUsageRelationsPersistIDsAndPendingAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage-relations.json")
	m := usageRelationsTestMux(t, path)
	usageRelationsRoot(m)
	r := usage.Record{RequestID: "pending", ThreadID: "child", TurnID: "tc", ParentThreadID: "root", Subagent: true}
	m.associateUsageRecord(&r, "")
	m = usageRelationsTestMux(t, path)
	if len(m.usageRelations.Pending) != 1 {
		t.Fatal("pending relation not retained")
	}
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"child","agentPath":"/root/secret-name","prompt":"secret-prompt","response":"secret-response"}}`)
	m = usageRelationsTestMux(t, path)
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "root:tr" {
		t.Fatal(r.RootTurnID)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatalf("private content in sidecar: %s", data)
	}
}

func TestUsageRelationsBadSidecarReturnsUsableEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage-relations.json")
	if err := os.WriteFile(path, []byte(`{"threads":`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newUsageRelationsState(path)
	if err == nil || s == nil || s.Threads == nil {
		t.Fatalf("bad sidecar should not disable inference: %v %+v", err, s)
	}
	m := &Multiplexer{usageRelations: s}
	usageRelationsClientTurn(m, "main", "t")
	r := usage.Record{RequestID: "r", ThreadID: "main", TurnID: "t"}
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "main:t" {
		t.Fatal(r.RootTurnID)
	}
}

func TestUsageRelationsNilHooksAndConcurrentCalls(t *testing.T) {
	(&Multiplexer{}).associateUsageRecord(nil, "")
	usageRelationsNotify(&Multiplexer{}, "turn/started", `{}`)
	m := usageRelationsTestMux(t, "")
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			usageRelationsRoot(m)
			r := usage.Record{RequestID: "r", ThreadID: "root", TurnID: "t"}
			m.associateUsageRecord(&r, "")
		}()
	}
	group.Wait()
}

func TestUsageRelationsRootWaitsForDetachedDescendantAfterParentCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage-relations.json")
	m := usageRelationsTestMux(t, path)
	usageRelationsRoot(m)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"child"}}`)
	usageRelationsNotify(m, "item/completed", `{"threadId":"child","turnId":"tc","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"detached-grandchild"}}`)
	usageRelationsNotify(m, "turn/completed", `{"threadId":"root","turn":{"id":"tr"}}`)
	if !m.usageRelations.RootCompletions["root:tr"] || len(m.usageRelations.readyCompletionsLocked()) != 0 {
		t.Fatal("root finalized while descendants active")
	}
	// Closing the immediate child is insufficient when its own descendant
	// continues, including after the router restarts.
	usageRelationsNotify(m, "turn/completed", `{"threadId":"child","turn":{"id":"tc"}}`)
	m = usageRelationsTestMux(t, path)
	if len(m.usageRelations.readyCompletionsLocked()) != 0 {
		t.Fatal("detached descendant was lost across restart")
	}
	usageRelationsNotify(m, "turn/completed", `{"threadId":"detached-grandchild","turn":{"id":"tg"}}`)
	ready := m.usageRelations.readyCompletionsLocked()
	if len(ready) != 1 || ready[0] != "root:tr" {
		t.Fatalf("ready roots = %v", ready)
	}
	if m.usageRelations.RootCompletions["child:tc"] {
		t.Fatal("child completion created an independent root completion")
	}
}

func TestUsageRelationsReplayDoesNotReopenCompletedChild(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"collabAgentToolCall","tool":"spawnAgent","senderThreadId":"root","receiverThreadIds":["child"]}}`)
	usageRelationsNotify(m, "turn/completed", `{"threadId":"child","turn":{"id":"tc"}}`)
	usageRelationsNotify(m, "turn/completed", `{"threadId":"root","turn":{"id":"tr","items":[{"type":"collabAgentToolCall","tool":"spawnAgent","senderThreadId":"root","receiverThreadIds":["child"]}]}}`)
	if len(m.usageRelations.readyCompletionsLocked()) != 1 {
		t.Fatal("replayed spawn reopened child")
	}
	usageRelationsNotify(m, "turn/started", `{"threadId":"child","turn":{"id":"later"}}`)
	if len(m.usageRelations.readyCompletionsLocked()) != 0 {
		t.Fatal("explicit later child turn did not hold root provisional")
	}
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","kind":"interrupted","agentThreadId":"child"}}`)
	if len(m.usageRelations.readyCompletionsLocked()) != 1 {
		t.Fatal("explicit interruption did not settle descendant")
	}
}

func TestUsageRelationsCoverageWarnsWithoutAssigningPossibleOrphans(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	r := usage.Record{RequestID: "orphan", ThreadID: "child", TurnID: "tc", ParentThreadID: "root", Subagent: true}
	m.associateUsageRecord(&r, "")
	for _, root := range []string{"root:first", "root:second"} {
		pending, active := m.usageRelationCoverage(root)
		if pending != 1 || active != 1 {
			t.Fatalf("%s coverage = %d/%d", root, pending, active)
		}
	}
	if pending, active := m.usageRelationCoverage("unrelated:t"); pending != 0 || active != 0 {
		t.Fatal("unrelated thread inherited orphan")
	}
	if r.RootTurnID != "" {
		t.Fatal("coverage warning assigned tokens to a root")
	}
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"first","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"child"}}`)
	if pending, active := m.usageRelationCoverage("root:first"); pending != 0 || active != 1 {
		t.Fatalf("resolved coverage = %d/%d", pending, active)
	}
	if pending, active := m.usageRelationCoverage("root:second"); pending != 0 || active != 0 {
		t.Fatal("qualified edge still contaminated a different root")
	}
	usageRelationsNotify(m, "turn/completed", `{"threadId":"child","turn":{"id":"tc"}}`)
	if pending, active := m.usageRelationCoverage("root:first"); pending != 0 || active != 0 {
		t.Fatal("completed child remained active")
	}
}

func TestUsageRelationsLedgerLateBindingAndNativeCompletion(t *testing.T) {
	m := usageRelationsTestMux(t, filepath.Join(t.TempDir(), "relations.json"))
	ledger, err := usage.Open(filepath.Join(t.TempDir(), "usage.sqlite"), usage.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	m.usageLedger = ledger
	usageRelationsRoot(m)
	r := usage.Record{RequestID: "request", ThreadID: "child", TurnID: "tc", Subagent: true, StartedAt: time.Now(), Outcome: "running"}
	m.associateUsageRecord(&r, "")
	if err := ledger.Begin(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","kind":"started","agentThreadId":"child"}}`)
	view, err := ledger.ViewTurn(context.Background(), "root:tr")
	if err != nil || len(view.Requests) != 1 {
		t.Fatalf("late native binding missing from ledger: %+v %v", view, err)
	}
	usageRelationsNotify(m, "turn/completed", `{"threadId":"root","turn":{"id":"tr"}}`)
	view, err = ledger.ViewTurn(context.Background(), "root:tr")
	if err != nil || view.Completed {
		t.Fatalf("parent completion finalized live child: %+v %v", view, err)
	}
	r.FinishedAt, r.Outcome = time.Now(), "completed"
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "root:tr" || r.ParentThreadID != "root" {
		t.Fatalf("Finish missed async root/parent: %+v", r)
	}
	if err := ledger.Finish(context.Background(), r.RequestID, r); err != nil {
		t.Fatal(err)
	}
	usageRelationsNotify(m, "turn/completed", `{"threadId":"child","turn":{"id":"tc"}}`)
	view, err = ledger.ViewTurn(context.Background(), "root:tr")
	if err != nil || !view.Completed || len(view.Requests) != 1 {
		t.Fatalf("root did not finalize after last descendant: %+v %v", view, err)
	}
	if _, exists := m.usageRelations.Pending[r.RequestID]; exists {
		t.Fatal("finished bound request still pending")
	}
}

func TestUsageRelationsOutOfOrderCompletionBeforeParentRelation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relations.json")
	m := usageRelationsTestMux(t, path)
	usageRelationsRoot(m)
	usageRelationsNotify(m, "turn/completed", `{"threadId":"child","turn":{"id":"tc"}}`)
	m = usageRelationsTestMux(t, path)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","id":"spawn","kind":"started","agentThreadId":"child"}}`)
	usageRelationsNotify(m, "turn/completed", `{"threadId":"root","turn":{"id":"tr"}}`)
	if active := m.usageRelations.Threads["child"].Active; active {
		t.Fatal("late parent relation forgot already completed child")
	}
	if ready := m.usageRelations.readyCompletionsLocked(); len(ready) != 1 || ready[0] != "root:tr" {
		t.Fatalf("root remained provisional after late relation: %v", ready)
	}
}

func TestUsageRelationsTerminalReplayCannotCloseLaterTurn(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","id":"spawn","kind":"started","agentThreadId":"child"}}`)
	usageRelationsNotify(m, "turn/started", `{"threadId":"child","turn":{"id":"first"}}`)
	usageRelationsNotify(m, "turn/completed", `{"threadId":"child","turn":{"id":"first"}}`)
	completed := `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","id":"completed","kind":"completed","agentThreadId":"child"}}`
	usageRelationsNotify(m, "item/completed", completed)
	usageRelationsNotify(m, "turn/started", `{"threadId":"child","turn":{"id":"second"}}`)
	usageRelationsNotify(m, "turn/completed", `{"threadId":"child","turn":{"id":"first"}}`)
	usageRelationsNotify(m, "item/completed", completed)
	if !m.usageRelations.Threads["child"].Active {
		t.Fatal("old native terminal replay closed the later turn")
	}
	usageRelationsNotify(m, "turn/completed", `{"threadId":"child","turn":{"id":"second"}}`)
	usageRelationsNotify(m, "turn/started", `{"threadId":"child","turn":{"id":"second"}}`)
	if m.usageRelations.Threads["child"].Active {
		t.Fatal("old native start replay reopened a completed turn")
	}
}

func TestUsageRelationsThreadHistoryPreservesCompletedNativeTurns(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	usageRelationsNotify(m, "thread/started", `{"thread":{"id":"child","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"root"}}},"turns":[{"id":"tc","status":"completed"}]}}`)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","id":"spawn","kind":"started","agentThreadId":"child"}}`)
	usageRelationsNotify(m, "turn/completed", `{"threadId":"root","turn":{"id":"tr"}}`)
	if active := m.usageRelations.Threads["child"].Active; active {
		t.Fatal("completed restored child history was reopened")
	}
	if ready := m.usageRelations.readyCompletionsLocked(); len(ready) != 1 {
		t.Fatalf("completed restored child prevented root completion: %v", ready)
	}
}

func TestUsageRelationsExactUserTurnResponseBindsResumedChat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relations.json")
	m := usageRelationsTestMux(t, path)
	r := usage.Record{RequestID: "request", ThreadID: "resumed", TurnID: "turn"}
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "" {
		t.Fatal("unknown parentlessness classified before native proof")
	}
	usageRelationsNotify(m, "turn/completed", `{"threadId":"resumed","turn":{"id":"turn"}}`)
	usageRelationsClientTurn(m, "resumed", "turn")
	m = usageRelationsTestMux(t, path)
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "resumed:turn" || !m.usageRelations.RootCompletions["resumed:turn"] {
		t.Fatalf("exact client response did not retain earlier usage/completion: %+v", r)
	}
	later := usage.Record{RequestID: "later", ThreadID: "resumed", TurnID: "unproven"}
	m.associateUsageRecord(&later, "")
	if later.RootTurnID != "" {
		t.Fatal("one successful turn response proved unrelated later turn")
	}
}

func TestUsageRelationsRejectedTurnStartCannotClassifyLaterAgentTurn(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	m.trackUsageClientResponse(protocol.Message{Method: "turn/start", Params: json.RawMessage(`{"threadId":"agent"}`)}, protocol.Message{Error: &protocol.RPCError{Code: 400}, Result: json.RawMessage(`{"turn":{"id":"turn"}}`)})
	usageRelationsNotify(m, "turn/started", `{"threadId":"agent","turn":{"id":"turn"}}`)
	r := usage.Record{RequestID: "request", ThreadID: "agent", TurnID: "turn"}
	m.associateUsageRecord(&r, "")
	if r.RootTurnID != "" || len(m.usageRelations.UserRoots) != 0 {
		t.Fatal("rejected start established a user root")
	}
}

func TestUsageRelationsDirectUserTurnInAgentThreadKeepsEarlierParent(t *testing.T) {
	m := usageRelationsTestMux(t, "")
	usageRelationsRoot(m)
	usageRelationsNotify(m, "item/completed", `{"threadId":"root","turnId":"tr","item":{"type":"subAgentActivity","id":"spawn","kind":"started","agentThreadId":"child"}}`)
	old := usage.Record{RequestID: "old", ThreadID: "child", TurnID: "old", Subagent: true}
	m.associateUsageRecord(&old, "root")
	usageRelationsClientTurn(m, "child", "user")
	current := usage.Record{RequestID: "current", ThreadID: "child", TurnID: "user"}
	m.associateUsageRecord(&current, "")
	m.associateUsageRecord(&old, "root")
	if current.RootTurnID != "child:user" || old.RootTurnID != "root:tr" {
		t.Fatalf("direct user turn rewrote historical parent: current=%+v old=%+v", current, old)
	}
	if _, active := m.usageRelationCoverage("root:tr"); active != 0 {
		t.Fatal("new direct user turn held the historical parent root active")
	}
}
