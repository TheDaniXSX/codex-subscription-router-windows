//go:build windows

package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
)

func TestChildWindowsPropagatesIsolatedHomesAndExchangesJSONL(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_CHILD_HELPER") == "1" {
		runWindowsChildHelper()
		return
	}

	inbound := make(chan Inbound, 1)
	home := t.TempDir()
	child, err := Start(
		"windows-account",
		home,
		os.Args[0],
		[]string{"-test.run=^TestChildWindowsPropagatesIsolatedHomesAndExchangesJSONL$"},
		append(os.Environ(), "GO_WANT_CODEX_CHILD_HELPER=1"),
		inbound,
	)
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-child.closed:
		default:
			_ = child.Close()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := child.Request(ctx, "environment/read", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Request() error: %v", err)
	}
	var result struct {
		CodexHome       string `json:"codexHome"`
		CodexSQLiteHome string `json:"codexSqliteHome"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.CodexHome != home || result.CodexSQLiteHome != home {
		t.Fatalf("isolated homes = %#v, want both %q", result, home)
	}

	select {
	case event := <-inbound:
		if event.AccountID != "windows-account" || event.Message.Method != "helper/ready" {
			t.Fatalf("unexpected inbound event: %#v", event)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for child notification")
	}
}

func TestChildCloseWindowsTerminatesProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_CHILD_CLOSE_HELPER") == "1" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}

	child, err := Start(
		"windows-close-account",
		t.TempDir(),
		os.Args[0],
		[]string{"-test.run=^TestChildCloseWindowsTerminatesProcess$"},
		append(os.Environ(), "GO_WANT_CODEX_CHILD_CLOSE_HELPER=1"),
		make(chan Inbound, 1),
	)
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if err := child.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	select {
	case <-child.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("child process did not terminate after Close")
	}
}

func runWindowsChildHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(2)
	}
	request, err := protocol.Parse(scanner.Bytes())
	if err != nil {
		os.Exit(3)
	}
	result, err := json.Marshal(map[string]string{
		"codexHome":       os.Getenv("CODEX_HOME"),
		"codexSqliteHome": os.Getenv("CODEX_SQLITE_HOME"),
	})
	if err != nil {
		os.Exit(4)
	}
	response, err := protocol.Encode(protocol.Success(request.ID, result))
	if err != nil {
		os.Exit(5)
	}
	fmt.Println(string(response))
	fmt.Println(`{"method":"helper/ready","params":{}}`)
	time.Sleep(250 * time.Millisecond)
	os.Exit(0)
}
