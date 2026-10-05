package mux

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
)

// Explicit local-CLI opt-in; every inference destination is an HTTP fixture.
// No credentials, copied homes, remote catalogue, login, or real inference.
func TestNativeResponsesProtocolOffline(t *testing.T) {
	executable := os.Getenv("ROUTER_PROTOCOL_CLI")
	if executable == "" {
		t.Skip("official CLI path not explicitly supplied")
	}
	root := t.TempDir()
	home := filepath.Join(root, "fresh-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	type capture struct {
		Path   string
		Header http.Header
		Body   map[string]json.RawMessage
	}
	requests := make(chan capture, 8)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || (r.URL.Path != "/v1/responses" && r.URL.Path != "/v1/responses/compact") {
			http.Error(w, "unsupported fixture operation", http.StatusNotFound)
			return
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		requests <- capture{r.URL.Path, r.Header.Clone(), body}
		if r.URL.Path == "/v1/responses/compact" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"output":[]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\",\"status\":\"in_progress\"}}\n\n"+
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"msg_fixture\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	}))
	defer fixture.Close()
	args := []string{"app-server"}
	for _, override := range []string{
		`model_provider="fixture"`, `model="router-fixture"`,
		`model_providers.fixture.name="Offline protocol fixture"`,
		`model_providers.fixture.base_url=` + strconv.Quote(fixture.URL+"/v1"),
		`model_providers.fixture.wire_api="responses"`,
		`model_providers.fixture.requires_openai_auth=false`,
		`model_providers.fixture.supports_websockets=false`,
		`model_providers.fixture.request_max_retries=0`,
		`model_providers.fixture.stream_max_retries=0`,
		`model_reasoning_effort="low"`,
	} {
		args = append(args, "-c", override)
	}
	command := exec.Command(executable, args...)
	command.Dir = root
	// Deliberate environment allowlist rather than filtering known secret names.
	for _, key := range []string{"SystemRoot", "windir", "SystemDrive", "PATH", "PATHEXT", "COMSPEC", "OS", "PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS"} {
		if value, ok := os.LookupEnv(key); ok {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	for _, key := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "TEMP", "TMP", "CODEX_HOME", "CODEX_SQLITE_HOME"} {
		command.Env = append(command.Env, key+"="+home)
	}
	command.Stderr = io.Discard
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = input.Close()
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill() // Only the fresh process owned by this test.
			<-done
			t.Error("native fixture needed forced shutdown")
		}
	}()
	messages := make(chan protocol.Message, 512)
	go func() {
		defer close(messages)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 32*1024), 4<<20)
		for scanner.Scan() {
			var message protocol.Message
			if json.Unmarshal(scanner.Bytes(), &message) == nil {
				messages <- message
			}
		}
	}()
	send := func(id int, method string, params any) protocol.Message {
		t.Helper()
		encoded, _ := json.Marshal(params)
		request, _ := protocol.Encode(protocol.Request(method, json.RawMessage(strconv.Itoa(id)), encoded))
		if _, err := fmt.Fprintf(input, "%s\n", request); err != nil {
			t.Fatal(err)
		}
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		for {
			select {
			case message, ok := <-messages:
				if !ok {
					t.Fatalf("native CLI exited during %s", method)
				}
				if string(message.ID) == strconv.Itoa(id) && message.Method == "" {
					if message.Error != nil {
						t.Fatalf("%s rejected with code %d", method, message.Error.Code)
					}
					return message
				}
			case <-timer.C:
				t.Fatalf("timeout waiting for %s", method)
			}
		}
	}
	send(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "router_offline_protocol", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}})
	_, _ = io.WriteString(input, "{\"method\":\"initialized\",\"params\":{}}\n")
	account := send(2, "account/read", map[string]bool{"refreshToken": false})
	var accountResult struct {
		Account json.RawMessage `json:"account"`
	}
	if json.Unmarshal(account.Result, &accountResult) != nil || string(accountResult.Account) != "null" {
		t.Fatal("fresh CLI unexpectedly has an account")
	}
	started := send(3, "thread/start", map[string]any{"cwd": root, "approvalPolicy": "never", "sandbox": "read-only", "ephemeral": true, "allowProviderModelFallback": false})
	threadID := threadIDFromResult(started.Result)
	if threadID == "" {
		t.Fatal("native CLI omitted thread ID")
	}
	send(4, "turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]string{"type": "text", "text": "Offline fixture."}}, "responsesapiClientMetadata": map[string]string{"fixture": "local"}})
	var captured capture
	select {
	case captured = <-requests:
	case <-time.After(20 * time.Second):
		t.Fatal("native CLI did not reach local Responses fixture")
	}
	if captured.Header.Get("Content-Encoding") != "" || captured.Body["previous_response_id"] != nil || captured.Body["conversation"] != nil {
		t.Fatal("native CLI changed the stateless HTTP request contract")
	}
	var metadata map[string]string
	_ = json.Unmarshal(captured.Body["client_metadata"], &metadata)
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var nested map[string]string
	_ = json.Unmarshal([]byte(metadata["x-codex-turn-metadata"]), &nested)
	t.Logf("native metadata keys=%s directThread=%t nestedThread=%t sessionHeader=%t", strings.Join(keys, ","), metadata["thread_id"] == threadID, nested["thread_id"] == threadID, captured.Header.Get("X-Codex-Session-Id") == threadID)
	if metadata["thread_id"] != threadID || metadata["turn_id"] == "" || len(metadata["turn_id"]) > 64 || nested["fixture"] != "local" {
		t.Fatal("native metadata no longer supports baseline spending attribution")
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	completed := false
	for !completed {
		select {
		case message, ok := <-messages:
			if !ok {
				t.Fatal("native CLI exited before completing fixture turn")
			}
			if message.Method == "turn/completed" {
				var params struct {
					Turn struct {
						Status string `json:"status"`
					} `json:"turn"`
				}
				if json.Unmarshal(message.Params, &params) != nil || params.Turn.Status != "completed" {
					t.Fatal("native CLI did not accept fixture Responses stream")
				}
				completed = true
			}
		case <-timer.C:
			t.Fatal("native CLI did not finish fixture turn")
		}
	}
	send(5, "thread/compact/start", map[string]string{"threadId": threadID})
	select {
	case compact := <-requests:
		compactKeys := make([]string, 0, len(compact.Body))
		for key := range compact.Body {
			compactKeys = append(compactKeys, key)
		}
		sort.Strings(compactKeys)
		t.Logf("native compaction path=%s keys=%s encoded=%t previous=%t", compact.Path, strings.Join(compactKeys, ","), compact.Header.Get("Content-Encoding") != "", compact.Body["previous_response_id"] != nil)
		// The router's custom-provider name uses native local compaction through
		// Responses. Remote compaction, if selected by native Codex, has its own
		// /compact boundary; both existing gateway paths accept full input.
		if (compact.Path != "/v1/responses/compact" && compact.Path != "/v1/responses") || compact.Body["input"] == nil || compact.Body["previous_response_id"] != nil || compact.Header.Get("Content-Encoding") != "" {
			t.Fatal("native CLI changed the full-context HTTP compaction contract")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("native CLI did not reach local compaction fixture")
	}
}
