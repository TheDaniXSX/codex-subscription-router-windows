package chromenative

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/securefs"
)

type Bridge struct {
	config Config
	client *http.Client
	now    func() time.Time
}

func NewBridge(config Config) (*Bridge, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Bridge{
		config: config,
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: time.Now,
	}, nil
}

func (bridge *Bridge) Handle(ctx context.Context, request Request) Response {
	switch request.Type {
	case "hello":
		return Response{Protocol: ProtocolVersion, ID: request.ID, OK: true, Result: map[string]any{
			"hostName":     HostName,
			"capabilities": []string{"router.health", "router.accounts", "router.profile", "browser.context.publish"},
		}}
	case "router.health":
		return bridge.routerRequest(ctx, request.ID, http.MethodGet, "/v1/health")
	case "router.accounts":
		return bridge.routerRequest(ctx, request.ID, http.MethodGet, "/v1/accounts")
	case "router.profile":
		return bridge.routerRequest(ctx, request.ID, http.MethodGet, "/v1/profile/combined")
	case "browser.context.publish":
		browserContext, err := ParseBrowserContext(request.Payload)
		if err != nil {
			return Failure(request.ID, "invalid_payload", err.Error())
		}
		contextID, err := bridge.writeContext(browserContext)
		if err != nil {
			return Failure(request.ID, "context_write_failed", err.Error())
		}
		return Response{Protocol: ProtocolVersion, ID: request.ID, OK: true, Result: map[string]any{"contextId": contextID}}
	default:
		return Failure(request.ID, "unsupported_type", "request type is not supported")
	}
}

func (bridge *Bridge) routerRequest(ctx context.Context, id, method, path string) Response {
	request, err := http.NewRequestWithContext(ctx, method, bridge.config.ControlURL()+path, nil)
	if err != nil {
		return Failure(id, "router_request_failed", err.Error())
	}
	if path != "/v1/health" {
		token, err := bridge.readControlToken()
		if err != nil {
			return Failure(id, "router_unavailable", err.Error())
		}
		request.Header.Set("X-Codex-Mux-Token", token)
	}
	client := *bridge.client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(request)
	if err != nil {
		return Failure(id, "router_unavailable", "router control endpoint is unavailable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxMessageSize+1))
	if err != nil {
		return Failure(id, "router_response_failed", err.Error())
	}
	if len(body) > MaxMessageSize {
		return Failure(id, "router_response_failed", "router response exceeds the native message limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Failure(id, "router_response_failed", fmt.Sprintf("router returned HTTP %d", response.StatusCode))
	}
	var result any
	if err := json.Unmarshal(body, &result); err != nil {
		return Failure(id, "router_response_failed", "router returned invalid JSON")
	}
	return Response{Protocol: ProtocolVersion, ID: id, OK: true, Result: result}
}

func (bridge *Bridge) readControlToken() (string, error) {
	contents, err := os.ReadFile(filepath.Join(bridge.config.StateRoot, "control-token"))
	if err != nil {
		return "", fmt.Errorf("read router control token: %w", err)
	}
	token := strings.TrimSpace(string(contents))
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("router control token has an invalid format")
	}
	return token, nil
}

func (bridge *Bridge) writeContext(browserContext BrowserContext) (string, error) {
	root := filepath.Join(bridge.config.StateRoot, "chrome-connector", "contexts")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create browser context directory: %w", err)
	}
	if err := securefs.PrivateDirectory(root); err != nil {
		return "", fmt.Errorf("secure browser context directory: %w", err)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate context id: %w", err)
	}
	contextID := hex.EncodeToString(random)
	record := struct {
		Schema     int            `json:"schema"`
		ContextID  string         `json:"contextId"`
		CapturedAt string         `json:"capturedAt"`
		Context    BrowserContext `json:"context"`
	}{1, contextID, bridge.now().UTC().Format(time.RFC3339Nano), browserContext}
	contents, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(root, ".context-*.tmp")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, bytes.NewReader(contents)); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := securefs.PrivateFile(temporaryPath); err != nil {
		return "", err
	}
	destination := filepath.Join(root, contextID+".json")
	if err := os.Rename(temporaryPath, destination); err != nil {
		return "", err
	}
	bridge.pruneContexts(root, destination)
	return contextID, nil
}

// pruneContexts bounds private browser data without ever following links or
// touching files outside the connector's exact contexts directory.
func (bridge *Bridge) pruneContexts(root, keep string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type candidate struct {
		path string
		time time.Time
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if path == keep {
			continue
		}
		info, statErr := entry.Info()
		if statErr == nil {
			candidates = append(candidates, candidate{path: path, time: info.ModTime()})
		}
	}
	for len(candidates) > 19 {
		oldest := 0
		for index := 1; index < len(candidates); index++ {
			if candidates[index].time.Before(candidates[oldest].time) {
				oldest = index
			}
		}
		_ = os.Remove(candidates[oldest].path)
		candidates = append(candidates[:oldest], candidates[oldest+1:]...)
	}
}
