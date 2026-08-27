package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthorizedRequiresHeaderToken(t *testing.T) {
	server := &Server{token: "secret"}

	headerRequest := httptest.NewRequest(http.MethodGet, "/v1/accounts", nil)
	headerRequest.Header.Set("X-Codex-Mux-Token", "secret")
	if !server.authorized(headerRequest) {
		t.Fatal("header token should authorize request")
	}

	queryRequest := httptest.NewRequest(http.MethodGet, "/v1/accounts?token=secret", nil)
	if server.authorized(queryRequest) {
		t.Fatal("query-string token must not authorize request")
	}
}

func TestSecurityHeadersRejectUnknownOrigin(t *testing.T) {
	server := &Server{}
	called := false
	handler := server.securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusForbidden; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if called {
		t.Fatal("rejected origin reached downstream handler")
	}
	if origin := response.Header().Get("Access-Control-Allow-Origin"); origin != "" {
		t.Fatalf("unexpected allow-origin header %q", origin)
	}
}

func TestSecurityHeadersAllowDesktopOrigin(t *testing.T) {
	server := &Server{}
	handler := server.securityHeaders(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodOptions, "/v1/accounts", nil)
	request.Header.Set("Origin", desktopAppOrigin)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusNoContent; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != desktopAppOrigin {
		t.Fatalf("allow-origin = %q, want %q", got, desktopAppOrigin)
	}
}

func TestSecurityHeadersAllowRequestsWithoutOrigin(t *testing.T) {
	server := &Server{}
	called := false
	handler := server.securityHeaders(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		called = true
		response.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if !called {
		t.Fatal("request without Origin should reach downstream handler")
	}
}

func TestDecodeJSONRequiresExactlyOneValue(t *testing.T) {
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{name: "single value", body: `{"label":"Primary"}`, ok: true},
		{name: "trailing whitespace", body: "{\"label\":\"Primary\"}\r\n\t", ok: true},
		{name: "second object", body: `{"label":"Primary"}{"label":"Injected"}`},
		{name: "trailing token", body: `{"label":"Primary"} true`},
		{name: "unknown field", body: `{"label":"Primary","token":"do-not-accept"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/accounts", strings.NewReader(test.body))
			var input struct {
				Label string `json:"label"`
			}
			err := decodeJSON(request, &input)
			if test.ok && err != nil {
				t.Fatalf("decodeJSON() error = %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("decodeJSON() unexpectedly accepted the body")
			}
		})
	}
}

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	body := `{"label":"` + strings.Repeat("x", 64*1024) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/accounts", strings.NewReader(body))
	var input struct {
		Label string `json:"label"`
	}
	if err := decodeJSON(request, &input); err == nil {
		t.Fatal("decodeJSON() unexpectedly accepted an oversized body")
	}
}

func TestWriteCredentialsUnavailableUsesStableMachineCode(t *testing.T) {
	response := httptest.NewRecorder()
	writeCredentialsUnavailable(response, errors.New("details for the local operator"))
	if got, want := response.Code, http.StatusConflict; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "credentials_unavailable" {
		t.Fatalf("error code = %q, want credentials_unavailable", body.Error)
	}
}
