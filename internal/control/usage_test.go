package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/mux"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/usage"
)

func TestUsageRoutesRequireAuthenticationAndValidateShape(t *testing.T) {
	s, _ := newAccountActionTestServer(t)
	for _, tc := range []struct {
		method, path, body string
		auth               bool
		want               int
	}{
		{"GET", "/v1/usage/turn?threadId=a&turnId=b", "", false, 401},
		{"GET", "/v1/usage/status", "", false, 401},
		{"PATCH", "/v1/usage/calibration", `{"rootId":"a:b","included":true,"revision":0}`, false, 401},
		{"POST", "/v1/usage/turn?threadId=a&turnId=b", "", true, 405},
		{"GET", "/v1/usage/turn?threadId=a", "", true, 400},
		{"GET", "/v1/usage/turn?threadId=a&threadId=b&turnId=c", "", true, 400},
		{"GET", "/v1/usage/turn?threadId=%00&turnId=b", "", true, 400},
		{"GET", "/v1/usage/turn?threadId=a&turnId=b", "", true, 503},
		{"PATCH", "/v1/usage/calibration", `{"rootId":"a:b","included":true}`, true, 400},
		{"PATCH", "/v1/usage/calibration", `{"rootId":"a:b","revision":0}`, true, 400},
		{"PATCH", "/v1/usage/calibration", `{"rootId":"a:b","included":true,"revision":0,"unknown":1}`, true, 400},
		{"PATCH", "/v1/usage/calibration", `{"rootId":"a:b","included":true,"revision":0} {}`, true, 400},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.auth {
			r = authorizedAccountActionRequest(tc.method, tc.path)
			r.Body = io.NopCloser(strings.NewReader(tc.body))
		}
		w := httptest.NewRecorder()
		s.http.Handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s got %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	// Origin checks apply to the new write endpoint as well.
	r := authorizedAccountActionRequest(http.MethodPatch, "/v1/usage/calibration")
	r.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("foreign origin reached calibration")
	}
}

func TestUsageCalibrationHTTPPersistsAndRejectsStaleRevision(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "state"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := usage.Open(filepath.Join(store.Root(), "usage-ledger.sqlite"), usage.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Minute)
	input, cached, output, write := int64(100), int64(20), int64(30), int64(0)
	for _, id := range []string{"one", "two"} {
		record := usage.Record{RequestID: id, RootTurnID: id + ":turn", ThreadID: id, TurnID: "turn", AccountID: "primary", AccountPlan: "plus", ModelServed: "gpt-6-sol", TierServed: "default", StartedAt: start.Add(time.Second), FinishedAt: start.Add(2 * time.Second), Outcome: "completed", Usage: &usage.TokenUsage{InputTokens: &input, CachedInputTokens: &cached, OutputTokens: &output, CacheWriteTokens: &write}}
		if err = ledger.Begin(ctx, record); err != nil {
			t.Fatal(err)
		}
		if err = ledger.Finish(ctx, id, record); err != nil {
			t.Fatal(err)
		}
	}
	before, after, minutes, reset := 10.0, 12.0, int64(300), start.Add(time.Hour).Unix()
	if err = ledger.ObserveQuota(ctx, usage.QuotaObservation{ID: "shared", AccountID: "primary", Plan: "plus", Bucket: usage.BucketShort, CapacityMultiplier: 1, RequestIDs: []string{"one", "two"}, Before: usage.QuotaSnapshot{UsedPercent: &before, ObservedAt: start, WindowDurationMins: &minutes, ResetsAt: &reset}, After: usage.QuotaSnapshot{UsedPercent: &after, ObservedAt: start.Add(3 * time.Second), WindowDurationMins: &minutes, ResetsAt: &reset}}); err != nil {
		t.Fatal(err)
	}
	ledger.Close()
	m, err := mux.New(mux.Options{RealExecutable: "not-started", Store: store, Output: io.Discard, RequestSpending: true})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := New("127.0.0.1:0", "secret", m, false)
	get := func(id string) mux.UsageTurnView {
		t.Helper()
		w := httptest.NewRecorder()
		s.http.Handler.ServeHTTP(w, authorizedAccountActionRequest("GET", "/v1/usage/turn?threadId="+id+"&turnId=turn"))
		if w.Code != 200 {
			t.Fatalf("GET: %d %s", w.Code, w.Body.String())
		}
		var v mux.UsageTurnView
		if err = json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	initial := get("one")
	if initial.CalibrationEnabled || initial.Unit != "Pro20" || len(initial.Observations) != 1 {
		t.Fatalf("bad initial view: %+v", initial)
	}
	patch := func(revision uint64, related bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"rootId": "one:turn", "included": true, "revision": revision, "includeRelated": related})
		r := authorizedAccountActionRequest("PATCH", "/v1/usage/calibration")
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		s.http.Handler.ServeHTTP(w, r)
		return w
	}
	if w := patch(initial.Revision, true); w.Code != 200 {
		t.Fatalf("PATCH: %d %s", w.Code, w.Body.String())
	}
	if !get("one").CalibrationEnabled || !get("two").CalibrationEnabled {
		t.Fatal("bulk group toggle was not persisted")
	}
	if w := patch(initial.Revision, false); w.Code != 409 {
		t.Fatalf("stale revision accepted: %d %s", w.Code, w.Body.String())
	}
	missing := get("not-recorded")
	if len(missing.Requests) != 0 || missing.TotalAPICostUSD != nil {
		t.Fatal("unknown turn fabricated usage")
	}
}
