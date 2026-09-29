package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/broker"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/control"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/mux"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/securefs"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
)

const sharedProtocol = 1
const sharedDescriptorName = "broker-session.json"

type sharedBinding struct {
	Root        string `json:"root"`
	PrimaryHome string `json:"primaryHome"`
	UsageRoot   string `json:"usageRoot"`
	RouterHash  string `json:"routerHash"`
	NativeHash  string `json:"nativeHash"`
}

// This file is private to the OS user. It is never returned by diagnostics/API.
type sharedDescriptor struct {
	Protocol       int           `json:"protocol"`
	Binding        sharedBinding `json:"binding"`
	RPCAddress     string        `json:"rpcAddress"`
	ControlAddress string        `json:"controlAddress"`
	Token          string        `json:"token"`
}

type sharedHello struct {
	Protocol int    `json:"protocol"`
	Token    string `json:"token"`
	Probe    bool   `json:"probe,omitempty"`
}

func sharedPath(value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) {
		return "", errors.New("shared installation paths must be absolute and nonempty")
	}
	path, err := filepath.EvalSymlinks(filepath.Clean(value))
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return "", errors.New("shared installation directory does not exist")
	}
	return path, nil
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readSharedBinding(realExecutable string) (sharedBinding, error) {
	var binding sharedBinding
	if runtime.GOOS != "windows" {
		return binding, errors.New("shared desktop service is currently qualified only on Windows")
	}
	if os.Getenv("CODEX_MUX_SHARED_PROTOCOL") != strconv.Itoa(sharedProtocol) {
		return binding, errors.New("unsupported shared router protocol; update both installations together")
	}
	var err error
	if binding.Root, err = sharedPath(os.Getenv("CODEX_MUX_SHARED_ROOT")); err != nil {
		return binding, err
	}
	if binding.PrimaryHome, err = sharedPath(os.Getenv("CODEX_HOME")); err != nil {
		return binding, err
	}
	if binding.UsageRoot, err = sharedPath(os.Getenv("CODEX_MUX_USAGE_ROOT")); err != nil {
		return binding, err
	}
	if strings.EqualFold(binding.Root, binding.UsageRoot) || strings.EqualFold(binding.PrimaryHome, binding.UsageRoot) {
		return binding, errors.New("calibration data requires its own development directory")
	}
	executable, err := os.Executable()
	if err != nil {
		return binding, err
	}
	if binding.RouterHash, err = fileDigest(executable); err != nil {
		return binding, err
	}
	if binding.NativeHash, err = fileDigest(realExecutable); err != nil {
		return binding, err
	}
	return binding, nil
}

func equalSharedBinding(a, b sharedBinding) bool {
	return strings.EqualFold(a.Root, b.Root) && strings.EqualFold(a.PrimaryHome, b.PrimaryHome) &&
		strings.EqualFold(a.UsageRoot, b.UsageRoot) && a.RouterHash == b.RouterHash && a.NativeHash == b.NativeHash
}

func loopbackAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	n, perr := strconv.Atoi(port)
	return err == nil && perr == nil && host == "127.0.0.1" && n > 0 && n <= 65535
}

func readSharedDescriptor(binding sharedBinding) (sharedDescriptor, error) {
	var descriptor sharedDescriptor
	path := filepath.Join(binding.Root, sharedDescriptorName)
	info, err := os.Lstat(path)
	if err != nil {
		return descriptor, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16*1024 {
		return descriptor, errors.New("invalid broker descriptor")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return descriptor, err
	}
	if err = json.Unmarshal(data, &descriptor); err != nil {
		return descriptor, errors.New("invalid broker descriptor JSON")
	}
	if descriptor.Protocol != sharedProtocol || !equalSharedBinding(binding, descriptor.Binding) {
		return descriptor, errors.New("shared broker belongs to another installation revision or data binding; close both apps before upgrading")
	}
	if !loopbackAddress(descriptor.RPCAddress) || !loopbackAddress(descriptor.ControlAddress) {
		return descriptor, errors.New("broker endpoints must be IPv4 loopback")
	}
	if normalized, tokenErr := validateControlToken(descriptor.Token); tokenErr != nil || normalized != descriptor.Token {
		return descriptor, errors.New("invalid broker authentication")
	}
	return descriptor, nil
}

func dialShared(descriptor sharedDescriptor, probe bool) (net.Conn, *bufio.Reader, error) {
	conn, err := net.DialTimeout("tcp", descriptor.RPCAddress, time.Second)
	if err != nil {
		return nil, nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err = json.NewEncoder(conn).Encode(sharedHello{Protocol: sharedProtocol, Token: descriptor.Token, Probe: probe}); err != nil {
		conn.Close()
		return nil, nil, err
	}
	reader := bufio.NewReader(conn)
	// The peer must acknowledge before we expose any application traffic.
	ack, err := reader.ReadSlice('\n')
	if err != nil || string(ack) != "{\"ok\":true}\n" {
		conn.Close()
		return nil, nil, errors.New("shared broker handshake failed")
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, reader, nil
}

func spawnSharedBroker(args []string, binding sharedBinding) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logDirectory := filepath.Join(binding.Root, "logs")
	if err = os.MkdirAll(logDirectory, 0700); err != nil {
		return err
	}
	if err = securefs.PrivateDirectory(logDirectory); err != nil {
		return err
	}
	logPath := filepath.Join(logDirectory, "shared-broker.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	if err = securefs.PrivateFile(logPath); err != nil {
		return err
	}
	command := exec.Command(executable, append([]string{"--router-broker"}, args...)...)
	command.Env = os.Environ()
	command.Stdout = log
	command.Stderr = log
	prepareBrokerCommand(command)
	if err = command.Start(); err != nil {
		return fmt.Errorf("start shared service outside desktop process lifetime: %w", err)
	}
	go func() { _ = command.Wait() }()
	return nil
}

func connectSharedBroker(binding sharedBinding, args []string) (sharedDescriptor, net.Conn, *bufio.Reader, error) {
	// A legacy router holds the same shared-root mutex. We never terminate it.
	// A competing new launcher may also start a candidate; only the winner opens
	// the Store and publishes a descriptor.
	var last error
	started := false
	for until := time.Now().Add(45 * time.Second); time.Now().Before(until); {
		descriptor, err := readSharedDescriptor(binding)
		if err == nil {
			conn, reader, dialErr := dialShared(descriptor, false)
			if dialErr == nil {
				return descriptor, conn, reader, nil
			}
			last = dialErr
		} else {
			last = err
		}
		if !started {
			if err = spawnSharedBroker(args, binding); err != nil {
				return sharedDescriptor{}, nil, nil, err
			}
			started = true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return sharedDescriptor{}, nil, nil, fmt.Errorf("shared service unavailable; close legacy PROD and DEV before activating the paired update (%v)", last)
}

func sharedControlHandler(descriptor sharedDescriptor, localToken, channel string) http.Handler {
	target, _ := url.Parse("http://" + descriptor.ControlAddress)
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Header.Set("X-Codex-Mux-Token", descriptor.Token)
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "shared router unavailable", http.StatusBadGateway)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "app://-" {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Codex-Mux-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/v1/health" {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Codex-Mux-Token")), []byte(localToken)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/v1/usage/") && channel != "development" {
			http.Error(w, "calibration is available in DEV", http.StatusNotFound)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func runSharedProxy(realExecutable string, args []string) error {
	binding, err := readSharedBinding(realExecutable)
	if err != nil {
		return err
	}
	root, err := sharedPath(os.Getenv("CODEX_MUX_HOME"))
	if err != nil {
		return err
	}
	channel := os.Getenv("CODEX_MUX_INSTALL_CHANNEL")
	if channel != "production" && channel != "development" {
		return errors.New("shared client requires an installation channel")
	}
	release, err := acquireInstanceLock(filepath.Join(root, "desktop-client"))
	if err != nil {
		return err
	}
	defer release()
	port, err := parseControlPort(os.Getenv("CODEX_MUX_CONTROL_PORT"))
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	defer listener.Close()
	token, err := loadOrCreateToken(root)
	if err != nil {
		return err
	}
	descriptor, conn, reader, err := connectSharedBroker(binding, args)
	if err != nil {
		return err
	}
	defer conn.Close()
	server := &http.Server{Handler: sharedControlHandler(descriptor, token, channel), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	done := make(chan error, 2)
	go func() { _, err := io.Copy(os.Stdout, reader); done <- err }()
	go func() { _, err := io.Copy(conn, os.Stdin); done <- err }()
	return <-done
}

func runSharedBroker(realExecutable string, args []string) error {
	if !isInteractiveAppServer(args) {
		return errors.New("shared broker requires interactive app-server arguments")
	}
	binding, err := readSharedBinding(realExecutable)
	if err != nil {
		return err
	}
	release, err := acquireInstanceLock(binding.Root)
	if err != nil {
		return err
	}
	defer release()
	store, err := state.Open(binding.Root, binding.PrimaryHome)
	if err != nil {
		return err
	}
	rpc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer rpc.Close()
	controlListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer controlListener.Close()
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return err
	}
	descriptor := sharedDescriptor{Protocol: sharedProtocol, Binding: binding, RPCAddress: rpc.Addr().String(), ControlAddress: controlListener.Addr().String(), Token: hex.EncodeToString(secret[:])}
	hub := broker.New()
	ctx, cancel := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer cancel()
	multiplexer, err := mux.New(mux.Options{RealExecutable: realExecutable, RealArgs: args, Environment: os.Environ(), Store: store, Output: hub, RequestSpending: true, UsageRoot: binding.UsageRoot,
		OnBackendStopped: func(accountID string) {
			hub.BackendUnavailable(func(threadID string) bool {
				owner, known := store.ThreadOwner(threadID)
				if !known {
					if account, ok := store.Controller(); ok {
						owner = account.ID
					}
				}
				return owner == accountID
			})
		},
	})
	if err != nil {
		return err
	}
	if err = multiplexer.Start(ctx); err != nil {
		return err
	}
	defer multiplexer.Close()
	hub.SetHandler(multiplexer.HandleClient)
	api := control.New(descriptor.ControlAddress, descriptor.Token, multiplexer, false)
	go func() { _ = api.Serve(controlListener) }()
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = api.Shutdown(c)
	}()
	data, _ := json.Marshal(descriptor)
	descriptorPath := filepath.Join(binding.Root, sharedDescriptorName)
	if err = securefs.WritePrivateFileAtomic(descriptorPath, data); err != nil {
		return err
	}
	defer func() { _ = os.Remove(descriptorPath) }()
	var sequence atomic.Uint64
	go func() {
		for {
			conn, acceptErr := rpc.Accept()
			if acceptErr != nil {
				return
			}
			go serveSharedConnection(conn, descriptor, hub, sequence.Add(1))
		}
	}()
	// The service outlives either window, but exits after the last client and
	// in-flight task have gone. Closing DEV never kills work in PROD.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	idleSince := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if hub.Activity().Clients != 0 || !hub.Idle() {
				idleSince = now
				continue
			}
			if now.Sub(idleSince) >= time.Minute {
				return nil
			}
		}
	}
}

func serveSharedConnection(conn net.Conn, descriptor sharedDescriptor, hub *broker.Hub, id uint64) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadSlice('\n') // 4 KiB authentication envelope limit
	if err != nil {
		return
	}
	var hello sharedHello
	if json.Unmarshal(line, &hello) != nil || hello.Protocol != sharedProtocol ||
		subtle.ConstantTimeCompare([]byte(hello.Token), []byte(descriptor.Token)) != 1 {
		return
	}
	if _, err = io.WriteString(conn, "{\"ok\":true}\n"); err != nil {
		return
	}
	if hello.Probe {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	client, err := hub.AddClient(strconv.FormatUint(id, 10), sharedConnectionWriter{conn})
	if err != nil {
		return
	}
	defer client.Close()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		message, parseErr := protocol.Parse(scanner.Bytes())
		if parseErr != nil {
			return
		}
		if err := client.Send(message); err != nil {
			return
		}
	}
}

// A stalled desktop cannot pin a writer forever or starve other clients.
type sharedConnectionWriter struct{ net.Conn }

func (w sharedConnectionWriter) Write(data []byte) (int, error) {
	_ = w.SetWriteDeadline(time.Now().Add(10 * time.Second))
	n, err := w.Conn.Write(data)
	_ = w.SetWriteDeadline(time.Time{})
	return n, err
}
