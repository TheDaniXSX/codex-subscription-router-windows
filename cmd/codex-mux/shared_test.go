package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/broker"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
)

const sharedTestLocalToken = "1111111111111111111111111111111111111111111111111111111111111111"
const sharedTestBrokerToken = "2222222222222222222222222222222222222222222222222222222222222222"

func TestSharedControlTranslatesAuthenticationAndPreservesRequest(t *testing.T) {
	requests := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	d := sharedDescriptor{ControlAddress: strings.TrimPrefix(upstream.URL, "http://"), Token: sharedTestBrokerToken}
	request := httptest.NewRequest(http.MethodPatch, "http://127.0.0.1/v1/usage/calibration?request=a", strings.NewReader(`{"included":true}`))
	request.Header.Set("X-Codex-Mux-Token", sharedTestLocalToken)
	request.Header.Set("Origin", "app://-")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	sharedControlHandler(d, sharedTestLocalToken, "development").ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("DEV proxy failed: %d %s", response.Code, response.Body.String())
	}
	received := <-requests
	if received.Header.Get("X-Codex-Mux-Token") != sharedTestBrokerToken || received.Method != http.MethodPatch || received.URL.Path != "/v1/usage/calibration" || received.URL.RawQuery != "request=a" {
		t.Fatal("broker authentication or request changed incorrectly")
	}
	if received.Header.Get("Origin") != "app://-" || response.Header().Get("Access-Control-Allow-Origin") != "app://-" {
		t.Fatal("desktop origin lost")
	}
	if strings.Contains(response.Body.String(), sharedTestBrokerToken) || strings.Contains(response.Body.String(), sharedTestLocalToken) {
		t.Fatal("proxy exposed a token")
	}
}

func TestSharedControlPreflightOriginsAndChannelRestrictions(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	d := sharedDescriptor{ControlAddress: strings.TrimPrefix(upstream.URL, "http://"), Token: sharedTestBrokerToken}
	for _, tc := range []struct {
		name, method, path, origin, token, channel string
		want                                       int
		forward                                    bool
	}{
		{"preflight", http.MethodOptions, "/v1/usage/calibration", "app://-", "", "development", http.StatusNoContent, false},
		{"missing token", http.MethodGet, "/v1/accounts", "app://-", "", "development", http.StatusUnauthorized, false},
		{"wrong token", http.MethodGet, "/v1/accounts", "app://-", sharedTestBrokerToken, "development", http.StatusUnauthorized, false},
		{"evil origin", http.MethodGet, "/v1/accounts", "https://example.invalid", sharedTestLocalToken, "development", http.StatusForbidden, false},
		{"null origin", http.MethodOptions, "/v1/usage/status", "null", "", "development", http.StatusForbidden, false},
		{"production usage", http.MethodGet, "/v1/usage/status", "app://-", sharedTestLocalToken, "production", http.StatusNotFound, false},
		{"production mutation", http.MethodPatch, "/v1/usage/calibration", "app://-", sharedTestLocalToken, "production", http.StatusNotFound, false},
		{"production encoded usage", http.MethodGet, "/v1/%75sage/status", "app://-", sharedTestLocalToken, "production", http.StatusNotFound, false},
		{"production accounts", http.MethodGet, "/v1/accounts", "app://-", sharedTestLocalToken, "production", http.StatusOK, true},
		{"DEV usage", http.MethodGet, "/v1/usage/status", "app://-", sharedTestLocalToken, "development", http.StatusOK, true},
		{"local CLI", http.MethodGet, "/v1/accounts", "", sharedTestLocalToken, "development", http.StatusOK, true},
		{"health", http.MethodGet, "/v1/health", "", "", "production", http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			request := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, nil)
			request.Header.Set("Origin", tc.origin)
			request.Header.Set("X-Codex-Mux-Token", tc.token)
			if tc.method == http.MethodOptions {
				request.Header.Set("Access-Control-Request-Method", http.MethodPatch)
				request.Header.Set("Access-Control-Request-Headers", "content-type,x-codex-mux-token")
			}
			response := httptest.NewRecorder()
			sharedControlHandler(d, sharedTestLocalToken, tc.channel).ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status=%d want%d: %s", response.Code, tc.want, response.Body.String())
			}
			if (calls.Load() > before) != tc.forward {
				t.Fatal("unexpected upstream request")
			}
			if tc.origin == "app://-" && response.Header().Get("Access-Control-Allow-Origin") != "app://-" {
				t.Fatal("desktop response/error lacks CORS")
			}
			if tc.want == http.StatusForbidden && response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("untrusted origin allowed")
			}
			if tc.name == "preflight" {
				if !strings.Contains(response.Header().Get("Access-Control-Allow-Methods"), "PATCH") || !strings.Contains(strings.ToLower(response.Header().Get("Access-Control-Allow-Headers")), "x-codex-mux-token") {
					t.Fatal("preflight does not allow required mutation headers")
				}
			}
		})
	}
	// Authorization is not the desktop API's authentication header.
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/accounts", nil)
	request.Header.Set("Authorization", "Bearer "+sharedTestLocalToken)
	response := httptest.NewRecorder()
	sharedControlHandler(d, sharedTestLocalToken, "development").ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("unsupported authentication header accepted")
	}
}

func sharedDescriptorFixture(t *testing.T) (sharedBinding, sharedDescriptor) {
	t.Helper()
	b := sharedBinding{Root: t.TempDir(), PrimaryHome: t.TempDir(), UsageRoot: t.TempDir(), RouterHash: strings.Repeat("a", 64), NativeHash: strings.Repeat("b", 64)}
	return b, sharedDescriptor{Protocol: sharedProtocol, Binding: b, RPCAddress: "127.0.0.1:50001", ControlAddress: "127.0.0.1:50002", Token: sharedTestBrokerToken}
}

func writeSharedDescriptorFixture(t *testing.T, b sharedBinding, d sharedDescriptor) {
	t.Helper()
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(b.Root, sharedDescriptorName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSharedDescriptorRejectsInvalidAddressBindingAndToken(t *testing.T) {
	b, d := sharedDescriptorFixture(t)
	writeSharedDescriptorFixture(t, b, d)
	if got, err := readSharedDescriptor(b); err != nil || !equalSharedBinding(got.Binding, b) {
		t.Fatalf("valid descriptor failed: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*sharedDescriptor)
	}{
		{"protocol", func(d *sharedDescriptor) { d.Protocol++ }},
		{"root", func(d *sharedDescriptor) { d.Binding.Root = filepath.Join(b.Root, "other") }},
		{"primary home", func(d *sharedDescriptor) { d.Binding.PrimaryHome = b.Root }},
		{"usage root", func(d *sharedDescriptor) { d.Binding.UsageRoot = b.Root }},
		{"router binary", func(d *sharedDescriptor) { d.Binding.RouterHash = strings.Repeat("c", 64) }},
		{"native binary", func(d *sharedDescriptor) { d.Binding.NativeHash = strings.Repeat("c", 64) }},
		{"external RPC", func(d *sharedDescriptor) { d.RPCAddress = "192.0.2.1:50001" }},
		{"external API", func(d *sharedDescriptor) { d.ControlAddress = "192.0.2.1:50002" }},
		{"DNS RPC", func(d *sharedDescriptor) { d.RPCAddress = "localhost:50001" }},
		{"IPv6", func(d *sharedDescriptor) { d.RPCAddress = "[::1]:50001" }},
		{"bad port", func(d *sharedDescriptor) { d.RPCAddress = "127.0.0.1:65536" }},
		{"no token", func(d *sharedDescriptor) { d.Token = "" }},
		{"short token", func(d *sharedDescriptor) { d.Token = "abcd" }},
		{"nonhex token", func(d *sharedDescriptor) { d.Token = strings.Repeat("z", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := d
			tc.change(&copy)
			writeSharedDescriptorFixture(t, b, copy)
			if _, err := readSharedDescriptor(b); err == nil {
				t.Fatal("invalid descriptor accepted")
			}
		})
	}
	for _, data := range []string{"not-json", strings.Repeat(" ", 17*1024)} {
		if err := os.WriteFile(filepath.Join(b.Root, sharedDescriptorName), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readSharedDescriptor(b); err == nil {
			t.Fatal("invalid descriptor file accepted")
		}
	}
}

func TestSharedPathAndAddressValidation(t *testing.T) {
	for _, path := range []string{"", "relative-directory", filepath.Join(t.TempDir(), "missing")} {
		if _, err := sharedPath(path); err == nil {
			t.Fatalf("invalid shared path accepted: %q", path)
		}
	}
	dir := t.TempDir()
	got, err := sharedPath(dir)
	if err != nil || got == "" {
		t.Fatal("existing absolute directory rejected")
	}
	file := filepath.Join(dir, "file")
	if err = os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = sharedPath(file); err == nil {
		t.Fatal("regular file accepted as data directory")
	}
	for _, address := range []string{"127.0.0.1:0", "127.0.0.1:-1", "127.0.0.1:65536", "localhost:5000", "[::1]:5000", "0.0.0.0:5000", "127.0.0.1:abc", "127.0.0.1"} {
		if loopbackAddress(address) {
			t.Fatalf("invalid address accepted: %q", address)
		}
	}
	if !loopbackAddress("127.0.0.1:65535") {
		t.Fatal("valid loopback address rejected")
	}
}

func TestSharedBindingRequiresIndependentUsageDirectory(t *testing.T) {
	b, _ := sharedDescriptorFixture(t)
	native := filepath.Join(t.TempDir(), "native-fixture")
	if err := os.WriteFile(native, []byte("native"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_MUX_SHARED_PROTOCOL", "1")
	t.Setenv("CODEX_MUX_SHARED_ROOT", b.Root)
	t.Setenv("CODEX_HOME", b.PrimaryHome)
	t.Setenv("CODEX_MUX_USAGE_ROOT", b.UsageRoot)
	if runtime.GOOS != "windows" {
		if _, err := readSharedBinding(native); err == nil || !strings.Contains(err.Error(), "only on Windows") {
			t.Fatalf("shared desktop mode must fail closed on unsupported platforms: %v", err)
		}
		return
	}
	if binding, err := readSharedBinding(native); err != nil || binding.NativeHash == "" || binding.RouterHash == "" {
		t.Fatalf("valid binding failed: %v", err)
	}
	for _, root := range []string{b.Root, b.PrimaryHome} {
		t.Setenv("CODEX_MUX_USAGE_ROOT", root)
		if _, err := readSharedBinding(native); err == nil {
			t.Fatal("usage shares account/primary directory")
		}
	}
}

func TestSharedConnectionRejectsInvalidHelloAndProbeDoesNotRegister(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		probe      bool
	}{
		{"wrong token", `{"protocol":1,"token":"bad"}` + "\n", false},
		{"wrong protocol", `{"protocol":99,"token":"` + sharedTestBrokerToken + `"}` + "\n", false},
		{"invalid JSON", "bad\n", false},
		{"oversized", strings.Repeat("a", 8192) + "\n", false},
		{"probe", `{"protocol":1,"token":"` + sharedTestBrokerToken + `","probe":true}` + "\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := broker.New()
			server, client := net.Pipe()
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(2 * time.Second))
			done := make(chan struct{})
			go func() {
				serveSharedConnection(server, sharedDescriptor{Token: sharedTestBrokerToken}, hub, 1)
				close(done)
			}()
			_, _ = io.WriteString(client, tc.line)
			line, err := bufio.NewReader(client).ReadString('\n')
			if tc.probe {
				if err != nil || line != "{\"ok\":true}\n" {
					t.Fatal("authenticated probe rejected")
				}
			} else if err == nil || line != "" {
				t.Fatal("invalid handshake acknowledged")
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("handshake did not finish")
			}
			if hub.Activity().Clients != 0 {
				t.Fatal("probe or invalid handshake registered a client")
			}
		})
	}
}

func TestSharedConnectionRoutesRPCAndRestoresDesktopRequestID(t *testing.T) {
	hub := broker.New()
	messages := make(chan protocol.Message, 4)
	hub.SetHandler(func(m protocol.Message) { messages <- m })
	server, client := net.Pipe()
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan struct{})
	go func() {
		serveSharedConnection(server, sharedDescriptor{Token: sharedTestBrokerToken}, hub, 7)
		close(done)
	}()
	if err := json.NewEncoder(client).Encode(sharedHello{Protocol: sharedProtocol, Token: sharedTestBrokerToken}); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(client)
	if ack, err := reader.ReadString('\n'); err != nil || ack != "{\"ok\":true}\n" {
		t.Fatal("handshake failed")
	}
	if _, err := io.WriteString(client, "{\"id\":42,\"method\":\"initialize\",\"params\":{\"capabilities\":{}}}\n"); err != nil {
		t.Fatal(err)
	}
	var initialized protocol.Message
	select {
	case initialized = <-messages:
	case <-time.After(time.Second):
		t.Fatal("RPC did not reach hub")
	}
	if initialized.Method != "initialize" || string(initialized.ID) == "42" {
		t.Fatal("hub did not isolate desktop RPC ID")
	}
	encoded, err := protocol.Encode(protocol.Message{ID: initialized.ID, Result: json.RawMessage(`{"userAgent":"fixture"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = hub.Write(append(encoded, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	reply, err := protocol.Parse([]byte(line))
	if err != nil || string(reply.ID) != "42" || reply.Error != nil {
		t.Fatalf("desktop response changed: %s %v", line, err)
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not close broker client")
	}
	if hub.Activity().Clients != 0 {
		t.Fatal("disconnected client retained")
	}
}

func TestSharedBrokerUsesLegacyRootLockAndIndependentClientLocks(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows named mutex contract")
	}
	root := t.TempDir()
	release, err := acquireInstanceLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if second, err := acquireInstanceLock(root); err == nil {
		second()
		t.Fatal("shared broker can coexist with legacy owner of same store")
	}
	clientRelease, err := acquireInstanceLock(filepath.Join(root, "desktop-client"))
	if err != nil {
		t.Fatal("desktop proxy conflicts with broker owner", err)
	}
	defer clientRelease()
	if second, err := acquireInstanceLock(filepath.Join(root, "desktop-client")); err == nil {
		second()
		t.Fatal("two proxies can own same desktop control identity")
	}
	devRelease, err := acquireInstanceLock(filepath.Join(t.TempDir(), "desktop-client"))
	if err != nil {
		t.Fatal("independent DEV proxy lock conflicts", err)
	}
	devRelease()
}
