package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

const (
	profileURL      = "https://chatgpt.com/backend-api/wham/profiles/me"
	profileCacheTTL = 10 * time.Minute
	profileMaxBytes = 1 << 20
)

// ErrCredentialsUnavailable means an account's credential store cannot be
// consumed by the router. Callers must not fall back to another account or
// attempt to extract credentials from an operating-system credential store.
var ErrCredentialsUnavailable = errors.New("account credentials are unavailable to the router")

func newProfileHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			// These requests carry an OAuth bearer. Do not rely on the standard
			// client's host/subdomain forwarding rules for sensitive headers.
			return http.ErrUseLastResponse
		},
	}
}

type profileCacheEntry struct {
	imageURL  string
	expiresAt time.Time
}

type authFile struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

type profileResponse struct {
	Profile struct {
		ProfilePictureURL string `json:"profile_picture_url"`
	} `json:"profile"`
}

func (m *Multiplexer) profileImageURL(ctx context.Context, account state.Account) string {
	now := time.Now()
	m.profileMu.Lock()
	cached, ok := m.profileCache[account.ID]
	m.profileMu.Unlock()
	if ok && now.Before(cached.expiresAt) {
		return cached.imageURL
	}

	imageURL, err := fetchProfileImageURL(
		ctx,
		m.profileClient,
		profileURL,
		filepath.Join(account.CodexHome, "auth.json"),
	)
	if err != nil {
		return ""
	}
	m.profileMu.Lock()
	m.profileCache[account.ID] = profileCacheEntry{
		imageURL:  imageURL,
		expiresAt: now.Add(profileCacheTTL),
	}
	m.profileMu.Unlock()
	return imageURL
}

func fetchProfileImageURL(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	authPath string,
) (string, error) {
	credentials, err := readAuthFile(authPath)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create profile request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credentials.Tokens.AccessToken)
	if credentials.Tokens.AccountID != "" {
		request.Header.Set("ChatGPT-Account-ID", credentials.Tokens.AccountID)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Codex Subscription Router")

	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetch profile: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, profileMaxBytes))
		return "", fmt.Errorf("fetch profile: status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, profileMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read profile: %w", err)
	}
	if len(data) > profileMaxBytes {
		return "", errors.New("profile response is too large")
	}
	var profile profileResponse
	if err := decodeSingleJSON(data, &profile); err != nil {
		return "", fmt.Errorf("decode profile: %w", err)
	}
	return validatedProfileImageURL(profile.Profile.ProfilePictureURL)
}

func readAuthFile(path string) (authFile, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return authFile{}, fmt.Errorf("%w: auth.json does not exist", ErrCredentialsUnavailable)
		}
		return authFile{}, fmt.Errorf("open account credentials: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, profileMaxBytes+1))
	if err != nil {
		return authFile{}, fmt.Errorf("read account credentials: %w", err)
	}
	if len(data) > profileMaxBytes {
		return authFile{}, errors.New("account credentials are too large")
	}
	var credentials authFile
	if err := decodeSingleJSON(data, &credentials); err != nil {
		return authFile{}, fmt.Errorf("decode account credentials: %w", err)
	}
	if credentials.Tokens.AccessToken == "" {
		return authFile{}, fmt.Errorf("%w: access token is absent", ErrCredentialsUnavailable)
	}
	return credentials, nil
}

func decodeSingleJSON(contents []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON data")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func validatedProfileImageURL(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("profile image URL is not HTTPS")
	}
	return parsed.String(), nil
}
