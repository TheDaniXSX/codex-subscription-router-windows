package broker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
)

func TestBackendUnavailableOnlySettlesStoppedThreads(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	b, bo := f.ready("dev")
	f.start(a, ao, "dead", "dt")
	f.start(a, ao, "live", "lt")
	f.write(request("approval", `"dead-approval"`, `{"threadId":"dead","turnId":"dt"}`))
	deadApproval := take(t, ao)
	f.write(request("approval", `"live-approval"`, `{"threadId":"live","turnId":"lt"}`))
	liveApproval := take(t, ao)
	send(t, a, request("thread/read", "10", `{"threadId":"dead"}`))
	deadRequest := take(t, f.native)
	send(t, b, request("thread/read", "11", `{"threadId":"live"}`))
	liveRequest := take(t, f.native)
	f.h.BackendUnavailable(func(thread string) bool { return thread == "dead" })
	seenError, seenFailure, seenResolved := false, false, false
	for i := 0; i < 3; i++ {
		m := take(t, ao)
		switch m.Method {
		case "error":
			var params struct {
				ThreadID  string `json:"threadId"`
				TurnID    string `json:"turnId"`
				WillRetry bool   `json:"willRetry"`
			}
			if err := json.Unmarshal(m.Params, &params); err != nil {
				t.Fatal(err)
			}
			if params.ThreadID != "dead" || params.TurnID != "dt" || params.WillRetry {
				t.Fatalf("wrong stopped error: %s", m.Params)
			}
			seenError = true
		case "serverRequest/resolved":
			if !strings.Contains(string(m.Params), string(deadApproval.ID)) {
				t.Fatalf("resolved wrong approval: %s", m.Params)
			}
			seenResolved = true
		case "":
			if string(m.ID) != "10" || m.Error == nil || m.Error.Code != ErrUnavailable {
				t.Fatalf("pending request: %+v", m)
			}
			seenFailure = true
		default:
			t.Fatalf("fabricated turn event: %+v", m)
		}
	}
	if !seenError || !seenFailure || !seenResolved {
		t.Fatal("missing stopped notifications")
	}
	if got := take(t, bo); got.Method != "error" {
		t.Fatalf("observer notification: %+v", got)
	}
	if got := take(t, f.native); got.Error == nil || got.Error.Code != ErrUnavailable || string(got.ID) != `"dead-approval"` {
		t.Fatalf("approval not failed: %+v", got)
	}
	if state := f.h.Activity(); state.Clients != 2 || state.ActiveThreads != 1 || state.PendingRequests != 1 || state.PendingApprovals != 1 {
		t.Fatal(state)
	}
	select {
	case <-a.Done():
		t.Fatal("desktop with healthy work disconnected")
	default:
	}
	if err := a.Send(protocol.Success(deadApproval.ID, nil)); err == nil {
		t.Fatal("dead approval accepted")
	}
	send(t, a, protocol.Success(liveApproval.ID, nil))
	take(t, f.native)
	f.write(protocol.Success(liveRequest.ID, nil))
	take(t, bo)
	// A delayed old result/notification cannot resurrect stopped activity.
	f.write(protocol.Success(deadRequest.ID, nil))
	f.note("turn/started", `{"threadId":"dead","turn":{"id":"dt"}}`)
	empty(t, ao)
	empty(t, bo)
	f.h.BackendUnavailable(func(thread string) bool { return thread == "dead" })
	empty(t, ao)
	empty(t, bo)
	empty(t, f.native)
	f.note("turn/completed", `{"threadId":"live","turn":{"id":"lt"}}`)
	take(t, ao)
	take(t, bo)
	if !f.h.Idle() {
		t.Fatal(f.h.Activity())
	}
	f.start(b, bo, "dead", "recovered")
}

func TestBackendUnavailableClearsUnacknowledgedAndOwnerlessActivity(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	send(t, a, request("turn/start", "1", `{"threadId":"dead-pending"}`))
	req := take(t, f.native)
	f.note("turn/started", `{"threadId":"dead-unknown","turn":{"id":"unowned"}}`)
	take(t, ao)
	f.h.BackendUnavailable(func(thread string) bool { return strings.HasPrefix(thread, "dead-") })
	for i := 0; i < 2; i++ {
		m := take(t, ao)
		if m.Method != "error" && (m.Error == nil || m.Error.Code != ErrUnavailable) {
			t.Fatalf("unexpected event: %+v", m)
		}
	}
	if !f.h.Idle() {
		t.Fatal(f.h.Activity())
	}
	f.write(protocol.Success(req.ID, json.RawMessage(`{"turn":{"id":"late","status":"inProgress"}}`)))
	empty(t, ao)
	if !f.h.Idle() {
		t.Fatal("late success reopened stopped turn")
	}
	// The old original ID was released, allowing a fresh request after restart.
	send(t, a, request("turn/start", "1", `{"threadId":"dead-pending"}`))
	current := take(t, f.native)
	f.write(protocol.Success(current.ID, json.RawMessage(`{"turn":{"id":"new","status":"inProgress"}}`)))
	take(t, ao)
}

func TestBackendUnavailablePreservesLiveDescendant(t *testing.T) {
	f := newFixture(t)
	a, ao := f.ready("prod")
	f.start(a, ao, "dead-root", "rt")
	f.note("item/completed", `{"threadId":"dead-root","turnId":"rt","item":{"id":"spawn","type":"subAgentActivity","kind":"started","agentThreadId":"live-child"}}`)
	take(t, ao)
	f.note("turn/started", `{"threadId":"live-child","turn":{"id":"ct"}}`)
	take(t, ao)
	f.h.BackendUnavailable(func(thread string) bool { return thread == "dead-root" })
	if got := take(t, ao); got.Method != "error" {
		t.Fatal(got)
	}
	if state := f.h.Activity(); state.ActiveThreads != 1 {
		t.Fatal(state)
	}
	f.write(request("approval", "4", `{"threadId":"live-child","turnId":"ct"}`))
	if got := take(t, ao); got.Method != "approval" {
		t.Fatalf("live descendant lost owner: %+v", got)
	}
	f.note("turn/completed", `{"threadId":"live-child","turn":{"id":"ct"}}`)
	take(t, ao)
	if !f.h.Idle() {
		t.Fatal(f.h.Activity())
	}
}
