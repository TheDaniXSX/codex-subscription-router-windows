package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/mux"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

func newAccountActionTestServer(t *testing.T) (*Server, *state.Store) {
	t.Helper()
	root := t.TempDir()
	primaryHome := filepath.Join(root, "primary")
	if err := os.MkdirAll(primaryHome, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(root, "state"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	multiplexer, err := mux.New(mux.Options{
		RealExecutable: "not-started-in-control-tests",
		Store:          store,
		Output:         io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	return New("127.0.0.1:0", "secret", multiplexer, false), store
}

func authorizedAccountActionRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("X-Codex-Mux-Token", "secret")
	request.Header.Set("Origin", desktopAppOrigin)
	return request
}

func TestDeleteAccountEndpointRequiresAuthorizationAndDeletesSecondary(t *testing.T) {
	server, store := newAccountActionTestServer(t)
	account, err := store.AddAccount("Temporary")
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/accounts/" + account.ID

	unauthorized := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodDelete, path, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized DELETE status = %d", unauthorized.Code)
	}
	if _, ok := store.Account(account.ID); !ok {
		t.Fatal("unauthorized DELETE removed the account")
	}

	response := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(response, authorizedAccountActionRequest(http.MethodDelete, path))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("DELETE response = %d %s", response.Code, response.Body.String())
	}
	if _, ok := store.Account(account.ID); ok {
		t.Fatal("secondary account still exists after DELETE")
	}
}

func TestDeleteAccountEndpointRejectsPrimary(t *testing.T) {
	server, store := newAccountActionTestServer(t)
	response := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(response, authorizedAccountActionRequest(http.MethodDelete, "/v1/accounts/primary"))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("primary DELETE status = %d body=%s", response.Code, response.Body.String())
	}
	if _, ok := store.Account("primary"); !ok {
		t.Fatal("primary account was removed")
	}
}

func TestCancelLoginEndpointIsRoutedAndRequiresPendingLogin(t *testing.T) {
	server, _ := newAccountActionTestServer(t)
	response := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(response, authorizedAccountActionRequest(
		http.MethodPost,
		"/v1/accounts/primary/login/cancel",
	))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("login cancel status = %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "unavailable") {
		t.Fatalf("login cancel route did not reach mux: %s", response.Body.String())
	}
}

func TestCORSAdvertisesDelete(t *testing.T) {
	server, _ := newAccountActionTestServer(t)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/v1/accounts/example", nil)
	request.Header.Set("Origin", desktopAppOrigin)
	server.http.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d", response.Code)
	}
	if methods := response.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "DELETE") {
		t.Fatalf("DELETE missing from allow methods: %q", methods)
	}
}
