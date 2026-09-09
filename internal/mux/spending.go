package mux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/spend"
)

const spendProvider = "codex_router_spend"

func (m *Multiplexer) startSpendGateway() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		_ = listener.Close()
		return err
	}
	m.spendToken = hex.EncodeToString(secret[:])
	m.spendURL = "http://" + listener.Addr().String() + "/v1"
	u, _ := url.Parse("https://chatgpt.com/backend-api/codex/responses")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	transport.MaxConnsPerHost = 32
	transport.MaxIdleConnsPerHost = 16
	g := &spend.Gateway{Policy: m.spendPolicy, Token: m.spendToken, Upstream: u, RoundTrip: transport, Candidates: m.spendCandidates, Credentials: m.spendCredentials, Observe: m.recordSpend}
	if m.spendTransport != nil {
		g.RoundTrip = m.spendTransport
	}
	m.spendServer = &http.Server{Handler: g, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 * 1024, BaseContext: func(net.Listener) context.Context { return m.runCtx }}
	m.updateSpendMode()
	go func() { _ = m.spendServer.Serve(listener); transport.CloseIdleConnections() }()
	return nil
}

func (m *Multiplexer) updateSpendMode() {
	mode := m.store.RoutingMode()
	id := ""
	if mode.Mode == "account" {
		id = mode.AccountID
	}
	m.spendPolicy.Set(spend.Mode{AccountID: id})
}

func (m *Multiplexer) spendCandidates(ctx context.Context) ([]spend.Candidate, error) {
	m.spendCacheMu.Lock()
	defer m.spendCacheMu.Unlock()
	now := m.now()
	if now.Sub(m.spendCacheAt) < 2*time.Second && len(m.spendCache) > 0 {
		return append([]spend.Candidate(nil), m.spendCache...), nil
	}
	ctx, cancel := context.WithTimeout(ctx, m.requestTimeout)
	defer cancel()
	snapshots := m.accountSnapshots(ctx, false)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	result := make([]spend.Candidate, 0, len(snapshots))
	for _, s := range snapshots {
		weekly, short := longestAndShortestWindow(s.RateLimits)
		remaining := 0.0
		known := weekly != nil
		if known {
			remaining = 100 - weekly.UsedPercent
			if short != nil {
				remaining = math.Min(remaining, 100-short.UsedPercent)
			}
		}
		credits := resetCreditMetadata{}
		if account, ok := m.store.Account(s.ID); ok && s.Connected {
			credits = m.routingResetCredits(ctx, account)
		}
		result = append(result, spend.Candidate{ID: s.ID, Enabled: s.Enabled, Connected: s.Connected && s.AuthType == "chatgpt", Known: known, Remaining: remaining, Urgency: routeUrgencyScore(now, weekly, credits), ObservedAt: now})
	}
	m.spendCache = result
	m.spendCacheAt = now
	return append([]spend.Candidate(nil), result...), nil
}

func (m *Multiplexer) spendCredentials(ctx context.Context, id string) (spend.Credentials, error) {
	account, ok := m.store.Account(id)
	if !ok || !account.Enabled {
		return spend.Credentials{}, errors.New("subscription unavailable")
	}
	child, ok := m.child(id)
	if !ok {
		return spend.Credentials{}, errors.New("subscription unavailable")
	}
	// Refresh through the official account client, never implement token refresh
	// independently or replace another account's auth file.
	refreshed, err := child.Request(ctx, "account/read", json.RawMessage(`{"refreshToken":true}`))
	if err != nil {
		return spend.Credentials{}, err
	}
	if refreshed.Error != nil {
		return spend.Credentials{}, errors.New("subscription refresh rejected")
	}
	credentials, err := readAuthFile(filepath.Join(account.CodexHome, "auth.json"))
	if err != nil {
		return spend.Credentials{}, err
	}
	return spend.Credentials{Bearer: credentials.Tokens.AccessToken, AccountID: credentials.Tokens.AccountID}, nil
}

func (m *Multiplexer) spendArgs() []string {
	args := append([]string(nil), m.realArgs...)
	if !m.requestSpending {
		return args
	}
	config := map[string]any{
		"model_provider": spendProvider,
		"model_providers." + spendProvider + ".name":                                 "Codex subscription spend gateway",
		"model_providers." + spendProvider + ".base_url":                             m.spendURL,
		"model_providers." + spendProvider + ".requires_openai_auth":                 true,
		"model_providers." + spendProvider + ".env_http_headers.X-Codex-Spend-Token": "CODEX_SPEND_GATEWAY_TOKEN",
		"model_providers." + spendProvider + ".wire_api":                             "responses",
		"model_providers." + spendProvider + ".supports_websockets":                  false,
		"model_providers." + spendProvider + ".request_max_retries":                  0,
		"model_providers." + spendProvider + ".stream_max_retries":                   0,
	}
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := config[key]
		encoded, _ := json.Marshal(value)
		args = append(args, "-c", key+"="+string(encoded))
	}
	return args
}

func (m *Multiplexer) spendEnvironment() []string {
	env := append([]string(nil), m.environment...)
	if m.requestSpending {
		filtered := env[:0]
		for _, entry := range env {
			key, _, _ := strings.Cut(entry, "=")
			if !strings.EqualFold(key, "CODEX_SPEND_GATEWAY_TOKEN") {
				filtered = append(filtered, entry)
			}
		}
		env = filtered
		env = append(env, "CODEX_SPEND_GATEWAY_TOKEN="+m.spendToken)
	}
	return env
}

// Bind resumed histories as well as new threads to the local transport. Thread
// ownership continues to route filesystem state and account-specific plugins.
func (m *Multiplexer) bindSpendProvider(method string, params json.RawMessage) json.RawMessage {
	if !m.requestSpending {
		return params
	}
	switch method {
	case "thread/start", "thread/resume", "thread/fork":
	default:
		return params
	}
	var p map[string]any
	if len(params) == 0 {
		p = make(map[string]any)
	} else if json.Unmarshal(params, &p) != nil || p == nil {
		return params
	}
	p["modelProvider"] = spendProvider
	encoded, _ := json.Marshal(p)
	return encoded
}

func (m *Multiplexer) recordSpend(record spend.Record) {
	m.spendRecordsMu.Lock()
	m.spendRecords = append(m.spendRecords, record)
	if len(m.spendRecords) > 100 {
		m.spendRecords = append([]spend.Record(nil), m.spendRecords[len(m.spendRecords)-100:]...)
	}
	m.spendRecordsMu.Unlock()
	m.publish(Event{Type: "inference-spent", AccountID: record.AccountID, Data: record})
}

func (m *Multiplexer) SpendingStatus() any {
	m.spendRecordsMu.Lock()
	defer m.spendRecordsMu.Unlock()
	return struct {
		Enabled bool           `json:"enabled"`
		Scope   string         `json:"scope"`
		Records []spend.Record `json:"records"`
	}{m.requestSpending, "http-inference", append([]spend.Record(nil), m.spendRecords...)}
}
