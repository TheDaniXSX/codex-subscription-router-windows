package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type accountConfig struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	UsedPercent float64 `json:"usedPercent"`
}

type request struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type response struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Result any             `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type event struct {
	AccountID         string          `json:"accountId"`
	Method            string          `json:"method"`
	Params            json.RawMessage `json:"params,omitempty"`
	CodexHome         string          `json:"codexHome"`
	SQLite            string          `json:"sqliteHome"`
	ControlTokenInEnv bool            `json:"controlTokenInEnv"`
	UITestFlagInEnv   bool            `json:"uiTestFlagInEnv"`
}

func main() {
	if !hasArgument("app-server") {
		passthroughProbe()
		return
	}

	home := os.Getenv("CODEX_HOME")
	config, err := readConfig(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var incoming request
		if err := json.Unmarshal(scanner.Bytes(), &incoming); err != nil {
			continue
		}
		config, err = readConfig(home)
		if err != nil {
			write(response{ID: incoming.ID, Error: &rpcError{Code: -32603, Message: err.Error()}})
			continue
		}
		appendEvent(home, event{
			AccountID:         config.ID,
			Method:            incoming.Method,
			Params:            incoming.Params,
			CodexHome:         home,
			SQLite:            os.Getenv("CODEX_SQLITE_HOME"),
			ControlTokenInEnv: os.Getenv("CODEX_MUX_CONTROL_TOKEN") != "",
			UITestFlagInEnv:   os.Getenv("CODEX_MUX_UI_TESTS") != "",
		})
		if len(incoming.ID) == 0 {
			continue
		}
		handle(incoming, config, home)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
}

func passthroughProbe() {
	exitCode, _ := strconv.Atoi(os.Getenv("MOCK_CODEX_EXIT_CODE"))
	write(map[string]any{
		"args":        os.Args[1:],
		"codexHome":   os.Getenv("CODEX_HOME"),
		"muxHome":     os.Getenv("CODEX_MUX_HOME"),
		"probeMarker": os.Getenv("MOCK_CODEX_MARKER"),
	})
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func handle(incoming request, config accountConfig, home string) {
	switch incoming.Method {
	case "initialize":
		write(response{ID: incoming.ID, Result: map[string]any{"server": "codex-mux-smoke-mock"}})
	case "account/read":
		write(response{ID: incoming.ID, Result: map[string]any{
			"account": map[string]any{
				"type": "chatgpt", "email": config.Email, "planType": "plus",
			},
		}})
	case "account/rateLimits/read":
		duration := int64(7 * 24 * 60)
		write(response{ID: incoming.ID, Result: map[string]any{
			"rateLimits": map[string]any{
				"primary": map[string]any{
					"usedPercent": config.UsedPercent, "windowDurationMins": duration,
				},
			},
		}})
	case "thread/list":
		write(response{ID: incoming.ID, Result: map[string]any{"data": []any{}, "nextCursor": nil}})
	case "thread/start":
		threadID := "thread-" + config.ID
		write(response{ID: incoming.ID, Result: map[string]any{
			"thread": map[string]any{
				"id": threadID, "path": filepath.Join(home, threadID+".jsonl"),
				"cwd": home, "modelProvider": "openai",
			},
			"accountId": config.ID,
		}})
	case "thread/read":
		threadID := stringParam(incoming.Params, "threadId")
		write(response{ID: incoming.ID, Result: map[string]any{
			"thread": map[string]any{
				"id": threadID, "path": filepath.Join(home, threadID+".jsonl"),
				"cwd": home, "modelProvider": "openai",
			},
		}})
	case "thread/resume":
		threadID := stringParam(incoming.Params, "threadId")
		write(response{ID: incoming.ID, Result: map[string]any{
			"thread": map[string]any{"id": threadID}, "accountId": config.ID,
		}})
	case "turn/start":
		write(response{ID: incoming.ID, Result: map[string]any{
			"turn": map[string]any{"id": "turn-" + config.ID}, "accountId": config.ID,
		}})
	case "app/installed", "app/list", "mcpServerStatus/list", "mcpServer/oauth/login":
		var cleaned any
		_ = json.Unmarshal(incoming.Params, &cleaned)
		write(response{ID: incoming.ID, Result: map[string]any{
			"accountId": config.ID, "params": cleaned,
		}})
	default:
		write(response{ID: incoming.ID, Result: map[string]any{"accountId": config.ID}})
	}
}

func readConfig(home string) (accountConfig, error) {
	data, err := os.ReadFile(filepath.Join(home, "mock-account.json"))
	if err != nil {
		return accountConfig{}, fmt.Errorf("read mock account config: %w", err)
	}
	var config accountConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return accountConfig{}, fmt.Errorf("decode mock account config: %w", err)
	}
	if config.ID == "" {
		return accountConfig{}, fmt.Errorf("mock account ID is required")
	}
	return config, nil
}

func appendEvent(home string, value event) {
	file, err := os.OpenFile(filepath.Join(home, "mock-events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_ = json.NewEncoder(file).Encode(value)
}

func write(value any) {
	_ = json.NewEncoder(os.Stdout).Encode(value)
}

func hasArgument(expected string) bool {
	for _, argument := range os.Args[1:] {
		if argument == expected {
			return true
		}
	}
	return false
}

func stringParam(raw json.RawMessage, name string) string {
	var params map[string]any
	if json.Unmarshal(raw, &params) == nil {
		if value, ok := params[name].(string); ok {
			return value
		}
	}
	return ""
}
