package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
)

type Inbound struct {
	AccountID string
	Message   protocol.Message
	Raw       []byte
}

type response struct {
	message protocol.Message
	err     error
}

type processSupervisor interface {
	Terminate() error
	Close() error
}

// Child owns one real Codex app-server process and one isolated CODEX_HOME.
type Child struct {
	accountID string
	exe       string
	args      []string
	env       []string
	inbound   chan<- Inbound

	command    *exec.Cmd
	supervisor processSupervisor
	stdin      io.WriteCloser
	writeMu    sync.Mutex
	pendingMu  sync.Mutex
	pending    map[string]chan response
	sequence   atomic.Uint64
	closing    atomic.Bool
	closed     chan struct{}
	closeOnce  sync.Once
	stopOnce   sync.Once
	stopErr    error
}

func Start(accountID, codexHome, executable string, args, baseEnv []string, inbound chan<- Inbound) (*Child, error) {
	env := withoutRouterEnvironment(baseEnv)
	env = withEnvironment(env, "CODEX_HOME", codexHome)
	env = withEnvironment(env, "CODEX_SQLITE_HOME", codexHome)
	command := exec.Command(executable, args...)
	command.Env = env
	prepareCommand(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex stdout: %w", err)
	}
	command.Stderr = os.Stderr

	child := &Child{
		accountID: accountID,
		exe:       executable,
		args:      append([]string(nil), args...),
		env:       env,
		inbound:   inbound,
		command:   command,
		stdin:     stdin,
		pending:   make(map[string]chan response),
		closed:    make(chan struct{}),
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Codex app-server for %s: %w", accountID, err)
	}
	supervisor, err := superviseProcess(command.Process)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, fmt.Errorf("supervise Codex app-server for %s: %w", accountID, err)
	}
	child.supervisor = supervisor
	go child.readLoop(stdout)
	go child.waitLoop()
	return child, nil
}

func (c *Child) AccountID() string {
	return c.accountID
}

// Done is closed when the owned app-server process exits. Callers may use it
// to remove stale children and schedule a supervised restart. The returned
// channel is receive-only and remains safe to observe after Close.
func (c *Child) Done() <-chan struct{} {
	return c.closed
}

func (c *Child) Send(message protocol.Message) error {
	encoded, err := protocol.Encode(message)
	if err != nil {
		return err
	}
	return c.SendRaw(encoded)
}

func (c *Child) SendRaw(encoded []byte) error {
	if c.closing.Load() {
		return errors.New("Codex app-server is closing")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closing.Load() {
		return errors.New("Codex app-server is closing")
	}
	select {
	case <-c.closed:
		return errors.New("Codex app-server is closed")
	default:
	}
	if _, err := c.stdin.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write Codex app-server request: %w", err)
	}
	return nil
}

func (c *Child) Request(ctx context.Context, method string, params json.RawMessage) (protocol.Message, error) {
	id := protocol.StringID("__codex_mux_" + strconv.FormatUint(c.sequence.Add(1), 10))
	key := protocol.RequestIDKey(id)
	responses := make(chan response, 1)
	c.pendingMu.Lock()
	c.pending[key] = responses
	c.pendingMu.Unlock()

	if err := c.Send(protocol.Request(method, id, params)); err != nil {
		c.removePending(key)
		return protocol.Message{}, err
	}
	select {
	case received := <-responses:
		if received.err != nil {
			return protocol.Message{}, received.err
		}
		if received.message.Error != nil {
			return received.message, fmt.Errorf("%s: %s", method, received.message.Error.Message)
		}
		return received.message, nil
	case <-ctx.Done():
		c.removePending(key)
		return protocol.Message{}, ctx.Err()
	case <-c.closed:
		c.removePending(key)
		return protocol.Message{}, errors.New("Codex app-server closed while awaiting response")
	}
}

func (c *Child) Close() error {
	if c.command.Process == nil {
		return nil
	}
	c.stopOnce.Do(func() {
		select {
		case <-c.closed:
			c.stopErr = c.supervisor.Close()
			return
		default:
		}
		c.closing.Store(true)
		// StdinPipe is backed by an *os.File, whose Close may run concurrently
		// with Write. Do not wait for writeMu here: a hung child can fill the
		// pipe while a writer owns that mutex.
		closeErr := c.stdin.Close()
		if waitForProcess(c.closed, 2*time.Second) {
			if err := c.supervisor.Close(); err != nil {
				c.stopErr = err
			} else {
				c.stopErr = closeErr
			}
			return
		}
		if err := c.supervisor.Terminate(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			c.stopErr = err
			return
		}
		if !waitForProcess(c.closed, 2*time.Second) {
			c.stopErr = errors.New("Codex app-server did not exit after termination")
			return
		}
		c.stopErr = closeErr
	})
	return c.stopErr
}

func waitForProcess(closed <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-closed:
		return true
	case <-timer.C:
		return false
	}
}

func (c *Child) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		message, err := protocol.Parse(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "codex-mux: %s emitted invalid JSON: %v\n", c.accountID, err)
			continue
		}
		if message.Method == "" && len(message.ID) > 0 {
			key := protocol.RequestIDKey(message.ID)
			c.pendingMu.Lock()
			responses := c.pending[key]
			if responses != nil {
				delete(c.pending, key)
			}
			c.pendingMu.Unlock()
			if responses != nil {
				responses <- response{message: message}
				continue
			}
		}
		c.inbound <- Inbound{AccountID: c.accountID, Message: message, Raw: raw}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "codex-mux: read %s app-server: %v\n", c.accountID, err)
	}
}

func (c *Child) waitLoop() {
	err := c.command.Wait()
	if c.supervisor != nil {
		_ = c.supervisor.Close()
	}
	c.closeOnce.Do(func() { close(c.closed) })
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for key, responses := range c.pending {
		responses <- response{err: fmt.Errorf("Codex app-server exited: %w", err)}
		delete(c.pending, key)
	}
}

func (c *Child) removePending(key string) {
	c.pendingMu.Lock()
	delete(c.pending, key)
	c.pendingMu.Unlock()
}

func withEnvironment(environment []string, key, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		separator := strings.IndexByte(entry, '=')
		if separator > 0 && environmentKeyEqual(entry[:separator], key) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, key+"="+value)
}

func withoutRouterEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		separator := strings.IndexByte(entry, '=')
		if separator > 0 {
			key := entry[:separator]
			if environmentKeyHasPrefix(key, "CODEX_MUX_") || environmentKeyHasPrefix(key, "CODEX_ROUTER_") {
				continue
			}
		}
		result = append(result, entry)
	}
	return result
}
