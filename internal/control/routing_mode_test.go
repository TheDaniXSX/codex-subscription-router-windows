package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutingModeEndpoint(t *testing.T) {
	server, store := newAccountActionTestServer(t)
	for _, tc := range []struct {
		method, body, token string
		code                int
	}{
		{"GET", "", "", 401},
		{"PATCH", `{"mode":"account","accountId":"primary"}`, "", 401},
		{"PUT", `{}`, "secret", 405},
		{"PATCH", `{"mode":"invalid"}`, "secret", 400},
		{"PATCH", `{"mode":"account","accountId":"missing"}`, "secret", 400},
		{"PATCH", `{"mode":"auto","unknown":1}`, "secret", 400},
		{"PATCH", `{"mode":"auto","mode":"account","accountId":"primary"}`, "secret", 400},
		{"PATCH", `{"mode":"auto"} {}`, "secret", 400},
		{"PATCH", `{"mode":"account","accountId":"primary"}`, "secret", 200},
		{"GET", "", "secret", 200},
	} {
		request := httptest.NewRequest(tc.method, "/v1/routing-mode", strings.NewReader(tc.body))
		request.Header.Set("X-Codex-Mux-Token", tc.token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		before := store.RoutingMode()
		server.http.Handler.ServeHTTP(response, request)
		if tc.code >= 400 && store.RoutingMode() != before {
			t.Fatal("failed request mutated mode")
		}
		if response.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.method, tc.body, response.Code, response.Body.String())
		}
		if tc.code == http.StatusOK && !strings.Contains(response.Body.String(), `"routingMode":{"mode":"account","accountId":"primary"}`) {
			t.Fatal(response.Body.String())
		}
	}
	if store.RoutingMode().AccountID != "primary" {
		t.Fatal("preference not saved")
	}
	request := httptest.NewRequest("PATCH", "/v1/routing-mode", strings.NewReader(`{"mode":"auto"}`))
	request.Header.Set("X-Codex-Mux-Token", "secret")
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(response, request)
	if response.Code != 403 || store.RoutingMode().AccountID != "primary" {
		t.Fatal("foreign origin changed mode")
	}
}
