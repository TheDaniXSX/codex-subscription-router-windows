package mux

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/securefs"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

// Explicit opt-in only: this test makes two small real inference requests using
// private credential copies, never existing conversations or their state files.
func TestNativeSpendingSameThread(t *testing.T) {
	sourceRoot, exe := os.Getenv("ROUTER_LIVE_SPEND_SOURCE"), os.Getenv("ROUTER_LIVE_SPEND_EXE")
	if sourceRoot == "" || exe == "" {
		t.Skip("live subscriptions not explicitly supplied")
	}
	raw, err := os.ReadFile(filepath.Join(sourceRoot, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Accounts []state.Account `json:"accounts"`
	}
	if err = json.Unmarshal(raw, &source); err != nil || len(source.Accounts) != 2 {
		t.Fatal("two source subscriptions required")
	}
	root := t.TempDir()
	if err = securefs.PrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	primaryHome := filepath.Join(root, "primary")
	if err = os.Mkdir(primaryHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err = securefs.PrivateDirectory(primaryHome); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(root, "router"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddAccount("Native smoke secondary"); err != nil {
		t.Fatal(err)
	}
	accounts := store.Accounts()
	for i, a := range accounts {
		data, readErr := os.ReadFile(filepath.Join(source.Accounts[i].CodexHome, "auth.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err = securefs.WritePrivateFileAtomic(filepath.Join(a.CodexHome, "auth.json"), data); err != nil {
			t.Fatal(err)
		}
	}
	output := &lockedOutput{}
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CODEX_") {
			env = append(env, entry)
		}
	}
	m, err := New(Options{RealExecutable: exe, RealArgs: []string{"app-server", "-c", "features.multi_agent_v2=true"}, Environment: env, Store: store, Output: output, RequestTimeout: 45 * time.Second, RequestSpending: true})
	if err != nil {
		t.Fatal(err)
	}
	fakeInference := os.Getenv("ROUTER_LIVE_SPEND_FAKE_INFERENCE") == "1"
	if fakeInference {
		var once sync.Once
		childSeen := make(chan struct{})
		first := true
		var requestMu sync.Mutex
		m.spendTransport = nativeSpendTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == "GET" {
				return http.DefaultTransport.RoundTrip(r)
			}
			var b struct {
				Metadata map[string]string `json:"client_metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				return nil, err
			}
			requestMu.Lock()
			spawn := first
			first = false
			requestMu.Unlock()
			items := []any{map[string]any{"id": "msg_smoke", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": "OK"}}}}
			if spawn {
				items = []any{map[string]any{"id": "fc_smoke", "type": "function_call", "namespace": "collaboration", "name": "spawn_agent", "call_id": "call_smoke", "arguments": `{"task_name":"smoke_child","message":"Reply OK without tools."}`}}
				if err := m.SetRoutingMode(state.RoutingMode{Mode: "account", AccountID: accounts[1].ID}); err != nil {
					return nil, err
				}
			} else if b.Metadata["x-openai-subagent"] != "" {
				once.Do(func() { close(childSeen) })
			} else {
				select {
				case <-childSeen:
				case <-time.After(10 * time.Second):
					return nil, fmt.Errorf("native subagent did not reach gateway")
				}
			}
			response := map[string]any{"id": "resp_smoke", "status": "completed", "output": items, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
			events := []any{map[string]any{"type": "response.created", "response": map[string]string{"id": "resp_smoke", "status": "in_progress"}}}
			for i, item := range items {
				events = append(events, map[string]any{"type": "response.output_item.done", "output_index": i, "item": item})
			}
			events = append(events, map[string]any{"type": "response.completed", "response": response})
			var stream strings.Builder
			for _, e := range events {
				data, _ := json.Marshal(e)
				fmt.Fprintf(&stream, "data: %s\n\n", data)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream.String()))}, nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	defer m.Close()
	if err = m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	request := func(id int, method string, p any) protocol.Message {
		t.Helper()
		encoded, _ := json.Marshal(p)
		m.HandleClient(protocol.Request(method, json.RawMessage(strconv.Itoa(id)), encoded))
		var result protocol.Message
		if !awaitCondition(50*time.Second, func() bool {
			for _, message := range output.messages() {
				if string(message.ID) == strconv.Itoa(id) && message.Method == "" {
					result = message
					return true
				}
			}
			return false
		}) {
			t.Fatalf("timeout %s", method)
		}
		if result.Error != nil {
			t.Fatalf("%s: %v", method, result.Error)
		}
		return result
	}
	request(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "codex_router_native_smoke", "version": "0.1"}, "capabilities": map[string]any{"experimentalApi": true}})
	m.HandleClient(protocol.Message{Method: "initialized", Params: json.RawMessage(`{}`)})
	thread := request(2, "thread/start", map[string]any{"cwd": root, "model": "gpt-6-astra", "approvalPolicy": "never", "sandbox": "read-only"})
	id := threadIDFromResult(thread.Result)
	if id == "" {
		t.Fatal("no thread ID")
	}
	for index, a := range accounts {
		if fakeInference && index > 0 {
			break
		}
		if err = m.SetRoutingMode(state.RoutingMode{Mode: "account", AccountID: a.ID}); err != nil {
			t.Fatal(err)
		}
		result := request(3+index, "turn/start", map[string]any{"threadId": id, "effort": "low", "input": []any{map[string]string{"type": "text", "text": "Standalone connectivity test: reply exactly OK. Do not use tools."}}})
		var started struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(result.Result, &started)
		if !awaitCondition(60*time.Second, func() bool {
			for _, message := range output.messages() {
				if message.Method == "turn/completed" && strings.Contains(string(message.Params), started.Turn.ID) {
					var completed struct {
						Turn struct {
							Status string `json:"status"`
							Error  any    `json:"error"`
						} `json:"turn"`
					}
					_ = json.Unmarshal(message.Params, &completed)
					if completed.Turn.Status != "completed" {
						t.Errorf("turn failed: %v", completed.Turn.Error)
					}
					return true
				}
			}
			return false
		}) {
			t.Fatal("turn did not complete")
		}
		m.spendRecordsMu.Lock()
		records := append([]string(nil), func() []string {
			r := []string{}
			for _, v := range m.spendRecords {
				r = append(r, v.AccountID+":"+v.Outcome)
			}
			return r
		}()...)
		m.spendRecordsMu.Unlock()
		if fakeInference {
			if !awaitCondition(5*time.Second, func() bool {
				m.spendRecordsMu.Lock()
				defer m.spendRecordsMu.Unlock()
				seenChild := false
				seenRoot := false
				for _, r := range m.spendRecords {
					if r.AccountID == accounts[1].ID && r.Outcome == "completed" {
						if r.Subagent {
							seenChild = true
						} else {
							seenRoot = true
						}
					}
				}
				return seenRoot && seenChild
			}) {
				t.Fatal("missing second-account root continuation or native child inference")
			}
			t.Log("native subagent and root continuation both obey changed spending instruction")
			continue
		}
		if len(records) != index+1 || records[index] != a.ID+":completed" {
			t.Fatalf("wrong billing route: %v", records)
		}
		t.Logf("same thread, inference %d: selected subscription used", index+1)
	}
}

type nativeSpendTransport func(*http.Request) (*http.Response, error)

func (f nativeSpendTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
