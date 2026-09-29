package mux

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/backend"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

func serverResolutionMux(t *testing.T) (*Multiplexer, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "state"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	output := new(bytes.Buffer)
	m, err := New(Options{RealExecutable: "not-started", Store: store, Output: output})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, output
}

func serverResolutionInbound(account string, message protocol.Message) backend.Inbound {
	raw, _ := protocol.Encode(message)
	return backend.Inbound{AccountID: account, Message: message, Raw: raw}
}

func serverResolutionMessages(t *testing.T, b *bytes.Buffer) []protocol.Message {
	t.Helper()
	var result []protocol.Message
	for _, line := range bytes.Split(bytes.TrimSpace(b.Bytes()), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		m, err := protocol.Parse(line)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, m)
	}
	return result
}

func resolutionParams(t *testing.T, message protocol.Message) map[string]json.RawMessage {
	t.Helper()
	var params map[string]json.RawMessage
	if err := json.Unmarshal(message.Params, &params); err != nil {
		t.Fatal(err)
	}
	return params
}

func TestServerResolutionRemapsAccountScopedIDsAndPreservesMetadata(t *testing.T) {
	m, out := serverResolutionMux(t)
	m.handleInbound(serverResolutionInbound("primary", protocol.Request("item/commandExecution/requestApproval", json.RawMessage(`7`), json.RawMessage(`{"threadId":"chat-a"}`))))
	m.handleInbound(serverResolutionInbound("second-account", protocol.Request("item/commandExecution/requestApproval", json.RawMessage(`7`), json.RawMessage(`{"threadId":"chat-b"}`))))
	requests := serverResolutionMessages(t, out)
	if len(requests) != 2 || bytes.Equal(requests[0].ID, requests[1].ID) {
		t.Fatal("server IDs were not made account-specific")
	}
	resolved := serverResolutionInbound("second-account", protocol.Message{Method: "serverRequest/resolved", Params: json.RawMessage(`{"requestId":7,"threadId":"chat-b","turnId":"turn-b","reason":"cancelled"}`)})
	m.handleInbound(resolved)
	messages := serverResolutionMessages(t, out)
	if len(messages) != 3 {
		t.Fatal("non-controller resolution was not forwarded")
	}
	params := resolutionParams(t, messages[2])
	if !bytes.Equal(params["requestId"], requests[1].ID) || string(params["threadId"]) != `"chat-b"` || string(params["turnId"]) != `"turn-b"` || string(params["reason"]) != `"cancelled"` {
		t.Fatalf("resolution mapping lost metadata: %s", messages[2].Params)
	}
	if len(m.serverRoutes) != 1 {
		t.Fatal("native resolution removed another account's route")
	}
	m.handleInbound(resolved)
	if len(serverResolutionMessages(t, out)) != 3 {
		t.Fatal("replayed native resolution leaked unmapped ID")
	}
	m.handleInbound(serverResolutionInbound("primary", protocol.Message{Method: "serverRequest/resolved", Params: json.RawMessage(`{"requestId":7,"threadId":"wrong-chat"}`)}))
	if len(serverResolutionMessages(t, out)) != 3 {
		t.Fatal("wrong-thread resolution was forwarded")
	}
	m.handleInbound(serverResolutionInbound("primary", protocol.Message{Method: "serverRequest/resolved", Params: json.RawMessage(`{"requestId":7}`)}))
	messages = serverResolutionMessages(t, out)
	params = resolutionParams(t, messages[3])
	if !bytes.Equal(params["requestId"], requests[0].ID) || string(params["threadId"]) != `"chat-a"` || len(m.serverRoutes) != 0 {
		t.Fatal("missing native thread metadata did not use recorded route")
	}
}

func TestServerResolutionRetainsMappingAfterDesktopResponse(t *testing.T) {
	m, out := serverResolutionMux(t)
	m.forwardServerRequest(serverResolutionInbound("primary", protocol.Request("item/fileChange/requestApproval", protocol.StringID("approval"), json.RawMessage(`{"threadId":"chat"}`))))
	request := serverResolutionMessages(t, out)[0]
	m.handleServerRequestResponse(protocol.Success(request.ID, json.RawMessage(`{"decision":"accept"}`)))
	route, ok := m.serverRoutes[protocol.RequestIDKey(request.ID)]
	if !ok || !route.responded {
		t.Fatal("answer discarded native resolution mapping")
	}
	m.handleServerRequestResponse(protocol.Success(request.ID, json.RawMessage(`{"decision":"accept"}`)))
	m.handleInbound(serverResolutionInbound("primary", protocol.Message{Method: "serverRequest/resolved", Params: json.RawMessage(`{"requestId":"approval","threadId":"chat"}`)}))
	messages := serverResolutionMessages(t, out)
	if len(messages) != 2 || !bytes.Equal(resolutionParams(t, messages[1])["requestId"], request.ID) || len(m.serverRoutes) != 0 {
		t.Fatal("resolved notification after answer was not remapped")
	}
}

func TestServerResolutionExpiryAndShutdownClearMappedApprovals(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "expiry", true: "shutdown"}[shutdown], func(t *testing.T) {
			m, out := serverResolutionMux(t)
			at := time.Now()
			m.now = func() time.Time { return at }
			for _, id := range []string{"waiting", "answered"} {
				m.forwardServerRequest(serverResolutionInbound("primary", protocol.Request("item/commandExecution/requestApproval", protocol.StringID(id), json.RawMessage(`{"threadId":"chat"}`))))
			}
			requests := serverResolutionMessages(t, out)
			m.handleServerRequestResponse(protocol.Success(requests[1].ID, json.RawMessage(`{}`)))
			if shutdown {
				m.expireAllRoutes()
			} else {
				m.expireRoutes(at.Add(m.requestTimeout + time.Second))
			}
			messages := serverResolutionMessages(t, out)
			if len(messages) != 4 || len(m.serverRoutes) != 0 {
				t.Fatalf("routes not fully resolved: %d messages, %d routes", len(messages), len(m.serverRoutes))
			}
			seen := map[string]bool{}
			for _, message := range messages[2:] {
				if message.Method != "serverRequest/resolved" {
					t.Fatal("unexpected expiration message")
				}
				params := resolutionParams(t, message)
				seen[protocol.RequestIDKey(params["requestId"])] = true
				if string(params["threadId"]) != `"chat"` {
					t.Fatal("expiry lost thread")
				}
			}
			for _, request := range requests {
				if !seen[protocol.RequestIDKey(request.ID)] {
					t.Fatal("expiry did not emit original mapped ID")
				}
			}
			m.expireAllRoutes()
			if len(serverResolutionMessages(t, out)) != 4 {
				t.Fatal("second cleanup repeated resolution")
			}
		})
	}
}

func TestMuxIntegrationAnsweredServerRequestIsNotFailedAgainOnExpiry(t *testing.T) {
	h := newMuxIntegrationHarness(t, []fakeAppServerSpec{{AccountID: "primary", UsedPercent: 10, EmitServerRequestOn: "test/emit-server-request"}}, time.Second)
	if reply := h.request(800, "test/emit-server-request", map[string]any{}); reply.Error != nil {
		t.Fatal(reply.Error)
	}
	var approval protocol.Message
	if !awaitCondition(time.Second, func() bool {
		for _, message := range h.output.messages() {
			if message.Method == "test/server/request" {
				approval = message
				return true
			}
		}
		return false
	}) {
		t.Fatal("server approval not forwarded")
	}
	h.mux.handleServerRequestResponse(protocol.Success(approval.ID, json.RawMessage(`{"decision":"accept"}`)))
	h.mux.handleServerRequestResponse(protocol.Success(approval.ID, json.RawMessage(`{"decision":"accept"}`)))
	if !awaitCondition(time.Second, func() bool {
		for _, event := range h.events("primary") {
			if event.Message.Method == "" && event.Message.Error == nil && string(event.Message.ID) == `"server-1"` {
				return true
			}
		}
		return false
	}) {
		t.Fatal("answer did not reach native app-server")
	}
	h.mux.expireRoutes(time.Now().Add(2 * time.Second))
	// The fake app-server processes JSONL sequentially. Its reply fences all
	// earlier writes so an erroneous duplicate failure cannot escape the check.
	if reply := h.request(801, "test/ordering-fence", map[string]any{}); reply.Error != nil {
		t.Fatal(reply.Error)
	}
	count := 0
	for _, event := range h.events("primary") {
		if event.Message.Method == "" && string(event.Message.ID) == `"server-1"` {
			count++
			if event.Message.Error != nil {
				t.Fatal("answered approval failed a second time")
			}
		}
	}
	if count != 1 {
		t.Fatalf("native request received %d answers", count)
	}
	found := false
	for _, message := range h.output.messages() {
		if message.Method == "serverRequest/resolved" && bytes.Equal(resolutionParams(t, message)["requestId"], approval.ID) {
			found = true
		}
	}
	if !found {
		t.Fatal("expiry failed to close mapped frontend approval")
	}
}
