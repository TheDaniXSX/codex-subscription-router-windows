//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type observation struct {
	Arguments   []string          `json:"arguments"`
	Environment map[string]string `json:"environment"`
}

func main() {
	ready := os.Getenv("CODEX_ROUTER_TEST_TREE_READY")
	survived := os.Getenv("CODEX_ROUTER_TEST_TREE_SURVIVED")
	if len(os.Args) == 2 && os.Args[1] == "--router-test-grandchild" {
		if ready == "" || survived == "" {
			os.Exit(93)
		}
		if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
			os.Exit(94)
		}
		time.Sleep(2 * time.Second)
		if err := os.WriteFile(survived, []byte("orphaned"), 0o600); err != nil {
			os.Exit(95)
		}
		return
	}
	if ready != "" || survived != "" {
		if ready == "" || survived == "" {
			os.Exit(96)
		}
		grandchild := exec.Command(os.Args[0], "--router-test-grandchild")
		grandchild.Env = os.Environ()
		if err := grandchild.Start(); err != nil {
			os.Exit(97)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(98)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if os.Getenv("CODEX_ROUTER_TEST_STDIO") == "1" {
		fmt.Fprintln(os.Stdout, "synthetic-child-stdout")
		fmt.Fprintln(os.Stderr, "synthetic-child-stderr")
	}
	output := os.Getenv("CODEX_ROUTER_TEST_OUTPUT")
	if output == "" {
		os.Exit(90)
	}
	observed := observation{
		Arguments: os.Args[1:],
		Environment: map[string]string{
			"CODEX_ROUTER_DATA_DIR":         os.Getenv("CODEX_ROUTER_DATA_DIR"),
			"CODEX_MUX_HOME":                os.Getenv("CODEX_MUX_HOME"),
			"CODEX_MUX_STATE_ROOT":          os.Getenv("CODEX_MUX_STATE_ROOT"),
			"CODEX_ELECTRON_USER_DATA_PATH": os.Getenv("CODEX_ELECTRON_USER_DATA_PATH"),
			"CODEX_CLI_PATH":                os.Getenv("CODEX_CLI_PATH"),
			"CODEX_MUX_REAL_CODEX":          os.Getenv("CODEX_MUX_REAL_CODEX"),
			"CODEX_SPARKLE_ENABLED":         os.Getenv("CODEX_SPARKLE_ENABLED"),
			"CODEX_ROUTER_ENABLE_APPSHOTS":  os.Getenv("CODEX_ROUTER_ENABLE_APPSHOTS"),
			"CODEX_MUX_CONTROL_PORT":        os.Getenv("CODEX_MUX_CONTROL_PORT"),
			"CODEX_MUX_CONTROL_TOKEN":       os.Getenv("CODEX_MUX_CONTROL_TOKEN"),
			"CODEX_MUX_UI_TESTS":            os.Getenv("CODEX_MUX_UI_TESTS"),
			"CODEX_HOME":                    os.Getenv("CODEX_HOME"),
			"CODEX_SQLITE_HOME":             os.Getenv("CODEX_SQLITE_HOME"),
		},
	}
	data, err := json.Marshal(observed)
	if err != nil {
		os.Exit(91)
	}
	if err := os.WriteFile(output, data, 0o600); err != nil {
		os.Exit(92)
	}
	os.Exit(37)
}
