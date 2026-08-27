package mux

import (
	"encoding/json"
	"testing"
)

func TestHandleLoginCompletedPreservesStructuredFailure(t *testing.T) {
	multiplexer := &Multiplexer{runtime: map[string]accountRuntime{
		"account": {status: "pending", loginID: "login-1"},
	}}
	multiplexer.handleLoginCompleted("account", json.RawMessage(`{
		"loginId":"login-1","success":false,"error":"device code expired"
	}`))
	runtime := multiplexer.runtimeState("account")
	if runtime.loginID != "" || runtime.status != "error" || runtime.err != "device code expired" {
		t.Fatalf("failed login runtime = %#v", runtime)
	}
}

func TestHandleLoginCompletedIgnoresMismatchedLoginID(t *testing.T) {
	multiplexer := &Multiplexer{runtime: map[string]accountRuntime{
		"account": {status: "pending", loginID: "current-login"},
	}}
	multiplexer.handleLoginCompleted("account", json.RawMessage(`{
		"loginId":"stale-login","success":false,"error":"stale failure"
	}`))
	runtime := multiplexer.runtimeState("account")
	if runtime.loginID != "current-login" || runtime.status != "pending" || runtime.err != "" {
		t.Fatalf("stale completion mutated runtime: %#v", runtime)
	}
}

func TestHandleLoginCompletedTreatsCancelledLoginAsDisconnected(t *testing.T) {
	multiplexer := &Multiplexer{runtime: map[string]accountRuntime{
		"account": {status: "pending", loginID: "login-1", loginCancel: true},
	}}
	multiplexer.handleLoginCompleted("account", json.RawMessage(`{
		"loginId":"login-1","success":false,"error":"cancelled"
	}`))
	runtime := multiplexer.runtimeState("account")
	if runtime.loginID != "" || runtime.loginCancel || runtime.status != "disconnected" || runtime.err != "" {
		t.Fatalf("cancelled login runtime = %#v", runtime)
	}
}
