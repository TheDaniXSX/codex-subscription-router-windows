package mux

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchProfileImageURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		if got := request.Header.Get("ChatGPT-Account-ID"); got != "account-123" {
			t.Fatalf("unexpected account header %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"profile":{"profile_picture_url":"https://example.com/wanted.png"}
		}`))
	}))
	defer server.Close()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{
		"tokens":{"access_token":"secret-token","account_id":"account-123"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	imageURL, err := fetchProfileImageURL(
		context.Background(),
		server.Client(),
		server.URL,
		authPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	if imageURL != "https://example.com/wanted.png" {
		t.Fatalf("unexpected image URL %q", imageURL)
	}
}

func TestValidatedProfileImageURLRejectsNonHTTPS(t *testing.T) {
	if _, err := validatedProfileImageURL("http://example.com/avatar.png"); err == nil {
		t.Fatal("expected an insecure profile URL to be rejected")
	}
}

func TestReadAuthFileRejectsTrailingJSON(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	contents := `{"tokens":{"access_token":"secret-token"}} {"tokens":{"access_token":"injected"}}`
	if err := os.WriteFile(authPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuthFile(authPath); err == nil {
		t.Fatal("readAuthFile() unexpectedly accepted trailing JSON")
	}
}

func TestFetchProfileImageURLRejectsTrailingJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"profile":{"profile_picture_url":"https://example.com/wanted.png"}} {}`))
	}))
	defer server.Close()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchProfileImageURL(context.Background(), server.Client(), server.URL, authPath); err == nil {
		t.Fatal("fetchProfileImageURL() unexpectedly accepted trailing JSON")
	}
}

func TestReadAuthFileReportsUnavailableCredentials(t *testing.T) {
	if _, err := readAuthFile(filepath.Join(t.TempDir(), "missing-auth.json")); !errors.Is(err, ErrCredentialsUnavailable) {
		t.Fatalf("readAuthFile() error = %v, want ErrCredentialsUnavailable", err)
	}

	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuthFile(authPath); !errors.Is(err, ErrCredentialsUnavailable) {
		t.Fatalf("readAuthFile() error = %v, want ErrCredentialsUnavailable", err)
	}
}

func TestProfileClientDoesNotForwardBearerAcrossRedirect(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalled = true
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchProfileImageURL(context.Background(), newProfileHTTPClient(), redirect.URL, authPath); err == nil {
		t.Fatal("fetchProfileImageURL() unexpectedly accepted a redirect")
	}
	if targetCalled {
		t.Fatal("profile client followed a redirect carrying bearer credentials")
	}
}
