package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/control"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/mux"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/securefs"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

const (
	minimumControlPort = 49152
	maximumControlPort = 65535
)

var controlTokenMu sync.Mutex

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "codex-mux: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	realExecutable, err := resolveRealExecutable()
	if err != nil {
		return err
	}
	args := os.Args[1:]
	if !isInteractiveAppServer(args) {
		return passthrough(realExecutable, args)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	root := os.Getenv("CODEX_MUX_HOME")
	if root == "" {
		root = filepath.Join(home, ".codex-mux")
	}
	primaryCodexHome := os.Getenv("CODEX_HOME")
	if primaryCodexHome == "" {
		primaryCodexHome = filepath.Join(home, ".codex")
	}
	releaseInstance, err := acquireInstanceLock(root)
	if err != nil {
		return err
	}
	defer releaseInstance()

	port, err := parseControlPort(os.Getenv("CODEX_MUX_CONTROL_PORT"))
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("reserve local control endpoint: %w", err)
	}
	defer listener.Close()

	store, err := state.Open(root, primaryCodexHome)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer cancel()
	multiplexer, err := mux.New(mux.Options{
		RealExecutable: realExecutable,
		RealArgs:       args,
		Environment:    os.Environ(),
		Store:          store,
		Output:         os.Stdout,
	})
	if err != nil {
		return err
	}
	if err := multiplexer.Start(ctx); err != nil {
		return err
	}
	defer multiplexer.Close()

	token, err := loadOrCreateToken(root)
	if err != nil {
		return err
	}
	controlServer := control.New(
		listener.Addr().String(),
		token,
		multiplexer,
		os.Getenv("CODEX_MUX_UI_TESTS") == "1",
	)
	go func() {
		if serveErr := controlServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "codex-mux: control server: %v\n", serveErr)
		}
	}()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = controlServer.Shutdown(shutdownCtx)
	}()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		message, parseErr := protocol.Parse(scanner.Bytes())
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "codex-mux: ignore invalid client JSON: %v\n", parseErr)
			continue
		}
		multiplexer.HandleClient(message)
	}
	cancel()
	return scanner.Err()
}

func parseControlPort(value string) (int, error) {
	if value == "" {
		return 0, errors.New("CODEX_MUX_CONTROL_PORT is required for interactive app-server mode")
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < minimumControlPort || port > maximumControlPort {
		return 0, fmt.Errorf(
			"CODEX_MUX_CONTROL_PORT must be a decimal port between %d and %d",
			minimumControlPort,
			maximumControlPort,
		)
	}
	return port, nil
}

func resolveRealExecutable() (string, error) {
	if configured := os.Getenv("CODEX_MUX_REAL_CODEX"); configured != "" {
		return configured, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve wrapper executable: %w", err)
	}
	realExecutable := filepath.Join(filepath.Dir(executable), realExecutableName())
	if _, err := os.Stat(realExecutable); err != nil {
		return "", fmt.Errorf("find bundled codex.real: %w", err)
	}
	return realExecutable, nil
}

func isInteractiveAppServer(args []string) bool {
	for index, argument := range args {
		if argument != "app-server" {
			continue
		}
		if index+1 < len(args) {
			switch args[index+1] {
			case "daemon", "proxy", "generate-ts", "generate-json-schema", "help":
				return false
			}
		}
		return true
	}
	return false
}

func passthrough(realExecutable string, args []string) error {
	command := exec.Command(realExecutable, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = os.Environ()
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.ExitCode())
		}
		return err
	}
	return nil
}

func loadOrCreateToken(root string) (string, error) {
	controlTokenMu.Lock()
	defer controlTokenMu.Unlock()
	if configured := os.Getenv("CODEX_MUX_CONTROL_TOKEN"); configured != "" {
		return validateControlToken(configured)
	}
	path := filepath.Join(root, "control-token")
	if _, err := os.Lstat(path); err == nil {
		if chmodErr := securefs.PrivateFile(path); chmodErr != nil {
			return "", fmt.Errorf("secure control token: %w", chmodErr)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read control token: %w", readErr)
		}
		token, validateErr := validateControlToken(string(data))
		if validateErr != nil {
			return "", fmt.Errorf("read control token: %w", validateErr)
		}
		return token, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read control token: %w", err)
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate control token: %w", err)
	}
	token := hex.EncodeToString(bytes)
	if err := securefs.WritePrivateFileAtomic(path, []byte(token)); err != nil {
		return "", fmt.Errorf("write private control token: %w", err)
	}
	return token, nil
}

func validateControlToken(value string) (string, error) {
	token := strings.TrimSpace(value)
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("control token must be exactly 32 random bytes encoded as hexadecimal")
	}
	return token, nil
}
