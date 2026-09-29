package mux

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/spend"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/usage"
)

func newUsageTestMux(t *testing.T) *Multiplexer {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "state"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(Options{RealExecutable: "not-started", Store: store, Output: io.Discard, RequestSpending: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.usageLedger == nil {
		t.Fatal("usage ledger not initialized", m.usageTelemetry.err)
	}
	m.runCtx, m.runCancel = context.WithCancel(context.Background())
	t.Cleanup(m.Close)
	return m
}

func usageTestSnapshot(at time.Time, percent float64) AccountSnapshot {
	short, weekly, reset := int64(300), int64(10080), at.Add(7*24*time.Hour).Unix()
	return AccountSnapshot{ID: "primary", PlanType: "plus", observedAt: at, RateLimits: &RateLimits{
		Primary:   &RateLimitWindow{UsedPercent: percent, WindowDurationMins: &short, ResetsAt: &reset},
		Secondary: &RateLimitWindow{UsedPercent: percent, WindowDurationMins: &weekly, ResetsAt: &reset},
	}}
}

func TestUsageCaptureGroupsKnownConcurrencyAndNormalizesPro20(t *testing.T) {
	m := newUsageTestMux(t)
	m.usageTelemetry.delays = []time.Duration{time.Millisecond}
	start := time.Now().UTC()
	before, after := usageTestSnapshot(start, 10), usageTestSnapshot(start.Add(time.Second), 12)
	// Both samples belong to the same reset epoch.
	after.RateLimits.Primary.ResetsAt = before.RateLimits.Primary.ResetsAt
	after.RateLimits.Secondary.ResetsAt = before.RateLimits.Secondary.ResetsAt
	var reads atomic.Int32
	m.usageTelemetry.read = func(context.Context, string) (AccountSnapshot, error) {
		if reads.Add(1) == 1 {
			return before, nil
		}
		return after, nil
	}
	in, out, cached := int64(100), int64(10), int64(0)
	newRecord := func(id, thread string) spend.Record {
		usageRelationsClientTurn(m, thread, "turn")
		return spend.Record{Telemetry: &usage.Record{RequestID: id, ThreadID: thread, TurnID: "turn", AccountID: "primary", ModelRequested: "gpt-6-sol", TierRequested: "default", StartedAt: start.Add(10 * time.Millisecond), Usage: &usage.TokenUsage{InputTokens: &in, OutputTokens: &out, CachedInputTokens: &cached}}}
	}
	a, b := newRecord("a", "one"), newRecord("b", "two")
	m.beginUsage(context.Background(), &a)
	m.beginUsage(context.Background(), &b)
	for _, r := range []spend.Record{a, b} {
		r.Telemetry.FinishedAt = start.Add(500 * time.Millisecond)
		r.Telemetry.Outcome = "completed"
		m.finishUsage(r)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		v, err := m.UsageTurn(context.Background(), "one", "turn")
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Observations) == 2 {
			for _, o := range v.Observations {
				if len(o.RequestIDs) != 2 || len(o.RootTurnIDs) != 2 {
					t.Fatalf("incomplete group: %+v", o)
				}
				if o.ObservedPercentX20 == nil || *o.ObservedPercentX20 != 0.1 {
					t.Fatalf("expected 2pp / 20, got %+v", o)
				}
				if o.CalibrationEnabled {
					t.Fatal("calibration enabled by default")
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("quota was not recorded: %+v", v)
		}
		time.Sleep(time.Millisecond)
	}
	if reads.Load() != 2 {
		t.Fatalf("expected two fresh reads for whole group, got %d", reads.Load())
	}
}

func TestUsageWindowClassificationDoesNotDuplicateSingleWindow(t *testing.T) {
	s := usageTestSnapshot(time.Now(), 7)
	s.RateLimits.Primary = nil
	if usageQuotaSnapshot(s, usage.BucketShort).UsedPercent != nil {
		t.Fatal("weekly reused as short")
	}
	if usageQuotaSnapshot(s, usage.BucketWeekly).UsedPercent == nil {
		t.Fatal("weekly lost")
	}
	s.RateLimits.Secondary.WindowDurationMins = nil
	if usageQuotaSnapshot(s, usage.BucketWeekly).UsedPercent != nil {
		t.Fatal("unknown window classified")
	}
}

func TestUsageCapacityDoesNotGuessUnknownPlans(t *testing.T) {
	for plan, want := range map[string]float64{"plus": 1, "prolite": 5, "pro": 20, "free": 0, "business": 0, "": 0} {
		if got := usagePlanCapacity(plan); got != want {
			t.Fatalf("%s: %v", plan, got)
		}
	}
}

func TestUsageUsesCommonCodexPoolAndDoesNotSumModelQuotas(t *testing.T) {
	s := usageTestSnapshot(time.Now(), 7)
	s.RateLimitsByLimitID = map[string]*RateLimits{"other-model": s.RateLimits}
	if usageQuotaSnapshot(s, usage.BucketShort).UsedPercent != nil {
		t.Fatal("unknown quota replaced common pool")
	}
	s.RateLimitsByLimitID["codex"] = s.RateLimits
	got := usageQuotaSnapshot(s, usage.BucketShort)
	if got.UsedPercent == nil || *got.UsedPercent != 7 {
		t.Fatal("codex pool lost")
	}
	s.RateLimitsByLimitID = nil
	s.RateLimits.LimitID = "other-model"
	if usageQuotaSnapshot(s, usage.BucketShort).UsedPercent != nil {
		t.Fatal("unsupported legacy pool accepted")
	}
}

func TestUsageRequestStartingDuringQuotaReadJoinsWithoutBlockingDispatch(t *testing.T) {
	m := newUsageTestMux(t)
	m.usageTelemetry.delays = []time.Duration{0}
	start := time.Now().UTC()
	before := usageTestSnapshot(start, 10)
	after := usageTestSnapshot(start.Add(time.Minute), 12)
	after.RateLimits.Primary.ResetsAt = before.RateLimits.Primary.ResetsAt
	after.RateLimits.Secondary.ResetsAt = before.RateLimits.Secondary.ResetsAt
	reading, release := make(chan struct{}), make(chan struct{})
	var closeOnce sync.Once
	t.Cleanup(func() { closeOnce.Do(func() { close(release) }) })
	var reads atomic.Int32
	m.usageTelemetry.read = func(ctx context.Context, _ string) (AccountSnapshot, error) {
		switch reads.Add(1) {
		case 1:
			return before, nil
		case 2:
			close(reading)
			select {
			case <-release:
			case <-ctx.Done():
				return AccountSnapshot{}, ctx.Err()
			}
		}
		return after, nil
	}
	input, output, cached := int64(100), int64(10), int64(0)
	makeRecord := func(id string) spend.Record {
		usageRelationsClientTurn(m, id, "turn")
		return spend.Record{Telemetry: &usage.Record{RequestID: id, ThreadID: id, TurnID: "turn", AccountID: "primary", ModelServed: "gpt-6-sol", TierServed: "default", Usage: &usage.TokenUsage{InputTokens: &input, OutputTokens: &output, CachedInputTokens: &cached}}}
	}
	a, b := makeRecord("a"), makeRecord("b")
	m.beginUsage(context.Background(), &a)
	a.Telemetry.FinishedAt = time.Now().UTC()
	m.finishUsage(a)
	select {
	case <-reading:
	case <-time.After(time.Second):
		t.Fatal("quota reader did not start")
	}
	started := make(chan struct{})
	go func() { m.beginUsage(context.Background(), &b); close(started) }()
	select {
	case <-started:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("quota read blocked dispatch")
	}
	closeOnce.Do(func() { close(release) })
	b.Telemetry.FinishedAt = time.Now().UTC()
	m.finishUsage(b)
	deadline := time.Now().Add(3 * time.Second)
	for {
		view, err := m.UsageTurn(context.Background(), "a", "turn")
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Observations) > 0 {
			if len(view.Observations) != 2 || len(view.Observations[0].RequestIDs) != 2 {
				t.Fatalf("incomplete or duplicate overlap group: %+v", view.Observations)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("joined observation never settled")
		}
		time.Sleep(time.Millisecond)
	}
}

func usageTestReadSequence(t *testing.T, m *Multiplexer, snapshots ...AccountSnapshot) *atomic.Int32 {
	t.Helper()
	var reads atomic.Int32
	m.usageTelemetry.read = func(context.Context, string) (AccountSnapshot, error) {
		i := int(reads.Add(1)) - 1
		if i >= len(snapshots) {
			t.Errorf("unexpected quota read %d (sequence has %d)", i+1, len(snapshots))
			return AccountSnapshot{}, errors.New("no synthetic reading")
		}
		return snapshots[i], nil
	}
	return &reads
}

func usageTestFinishedRequest(t *testing.T, m *Multiplexer, id string, start, end time.Time, input int64) {
	t.Helper()
	output, cached, cacheWrite := int64(10), int64(0), int64(0)
	usageRelationsClientTurn(m, id, "turn")
	m.now = func() time.Time { return start }
	r := spend.Record{Telemetry: &usage.Record{RequestID: id, ThreadID: id, TurnID: "turn", AccountID: "primary", ModelServed: "gpt-6-sol", TierServed: "default", Usage: &usage.TokenUsage{InputTokens: &input, OutputTokens: &output, CachedInputTokens: &cached, CacheWriteTokens: &cacheWrite}}}
	m.beginUsage(context.Background(), &r)
	r.Telemetry.FinishedAt, r.Telemetry.Outcome = end, "completed"
	m.finishUsage(r)
	usageRelationsNotify(m, "turn/completed", `{"threadId":"`+id+`","turn":{"id":"turn"}}`)
	m.usageTelemetry.wg.Wait()
}

func usageTestSameEpoch(before AccountSnapshot, at time.Time, percent float64) AccountSnapshot {
	after := usageTestSnapshot(at, percent)
	after.RateLimits.Primary.ResetsAt = before.RateLimits.Primary.ResetsAt
	after.RateLimits.Secondary.ResetsAt = before.RateLimits.Secondary.ResetsAt
	return after
}

func TestUsageZeroQuotaWaitsAndNextSignalIncludesAllRequestsOnce(t *testing.T) {
	m := newUsageTestMux(t)
	m.usageTelemetry.delays = []time.Duration{0, 0}
	start := time.Now().UTC()
	before := usageTestSnapshot(start, 10)
	zero1, zero2 := usageTestSameEpoch(before, start.Add(3*time.Second), 10), usageTestSameEpoch(before, start.Add(4*time.Second), 10)
	positive1, positive2 := usageTestSameEpoch(before, start.Add(7*time.Second), 12), usageTestSameEpoch(before, start.Add(8*time.Second), 12)
	reads := usageTestReadSequence(t, m, before, zero1, zero2, positive1, positive2)
	usageTestFinishedRequest(t, m, "one", start.Add(time.Second), start.Add(2*time.Second), 100)
	first, err := m.UsageTurn(context.Background(), "one", "turn")
	if err != nil || len(first.Observations) != 2 {
		t.Fatalf("missing zero observation: %+v %v", first, err)
	}
	for _, o := range first.Observations {
		if o.After.Quality != usage.QuotaAwaitingSignal || o.EligibilityReason != usage.QuotaAwaitingSignal || o.Eligible || o.RawDeltaPP == nil || *o.RawDeltaPP != 0 || len(o.Samples) != 2 {
			t.Fatalf("zero must remain pending evidence: %+v", o)
		}
	}
	if _, err := m.SetUsageCalibration(context.Background(), "one:turn", true, first.Revision, false); err != nil {
		t.Fatal(err)
	}
	status, err := m.usageLedger.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range status.Buckets {
		if bucket.SampleCount != 0 {
			t.Fatalf("zero observation trained: %+v", bucket)
		}
	}
	usageTestFinishedRequest(t, m, "two", start.Add(5*time.Second), start.Add(6*time.Second), 200)
	combined, err := m.UsageTurn(context.Background(), "one", "turn")
	if err != nil || len(combined.Observations) != 2 || combined.CalibrationEnabled {
		t.Fatalf("extension duplicated observation or retained stale attestation: %+v %v", combined, err)
	}
	for _, o := range combined.Observations {
		if o.ID != "one:"+o.Bucket || len(o.RequestIDs) != 2 || o.RequestIDs[0] != "one" || o.RequestIDs[1] != "two" || len(o.Samples) != 4 || o.ObservedPercentX20 == nil || *o.ObservedPercentX20 != 0.1 || o.After.Quality == usage.QuotaAwaitingSignal {
			t.Fatalf("combined observation lost tokens, identity, or samples: %+v", o)
		}
	}
	if _, err := m.SetUsageCalibration(context.Background(), "one:turn", true, combined.Revision, true); err != nil {
		t.Fatal(err)
	}
	status, err = m.usageLedger.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range status.Buckets {
		if bucket.SampleCount != 1 {
			t.Fatalf("combined usage did not train exactly once: %+v", bucket)
		}
	}
	pending, err := m.usageLedger.PendingQuotaObservations(context.Background())
	if err != nil || len(pending) != 0 || reads.Load() != 5 {
		t.Fatalf("settled pending/read count mismatch: pending=%+v reads=%d err=%v", pending, reads.Load(), err)
	}
}

func TestUsagePendingRecoveryPreservesExactCodexPoolBaseline(t *testing.T) {
	m := newUsageTestMux(t)
	m.usageTelemetry.delays = []time.Duration{0}
	start := time.Now().UTC()
	before := usageTestSnapshot(start, 10)
	zero := usageTestSameEpoch(before, start.Add(3*time.Second), 10)
	before.RateLimitsByLimitID = map[string]*RateLimits{"codex": before.RateLimits}
	zero.RateLimitsByLimitID = map[string]*RateLimits{"codex": zero.RateLimits}
	usageTestReadSequence(t, m, before, zero)
	usageTestFinishedRequest(t, m, "one", start.Add(time.Second), start.Add(2*time.Second), 100)
	pending, err := m.usageLedger.PendingQuotaObservations(context.Background())
	if err != nil || len(pending) != 2 {
		t.Fatalf("missing pending evidence: %+v %v", pending, err)
	}
	m.Close()
	restarted, err := New(Options{RealExecutable: "not-started", Store: m.store, Output: io.Discard, RequestSpending: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	restarted.runCtx, restarted.runCancel = context.WithCancel(context.Background())
	restarted.usageTelemetry.delays = []time.Duration{0}
	positive := usageTestSameEpoch(before, start.Add(6*time.Second), 12)
	positive.RateLimitsByLimitID = map[string]*RateLimits{"codex": positive.RateLimits}
	reads := usageTestReadSequence(t, restarted, positive)
	usageTestFinishedRequest(t, restarted, "two", start.Add(4*time.Second), start.Add(5*time.Second), 200)
	view, err := restarted.UsageTurn(context.Background(), "one", "turn")
	if err != nil || len(view.Observations) != 2 || reads.Load() != 1 {
		t.Fatalf("restart opened a new baseline/group: %+v reads=%d err=%v", view, reads.Load(), err)
	}
	for _, before := range pending {
		found := false
		for _, after := range view.Observations {
			if before.ID != after.ID {
				continue
			}
			found = true
			if !reflect.DeepEqual(before.Before, after.Before) || after.Before.Source != "account/rateLimits/read:codex" || before.Plan != after.Plan || before.CapacityMultiplier != after.CapacityMultiplier || len(after.RequestIDs) != 2 || len(after.Samples) != 2 || after.After.Quality == usage.QuotaAwaitingSignal {
				t.Fatalf("restart mutated baseline or lost cumulative usage: before=%+v after=%+v", before, after)
			}
		}
		if !found {
			t.Fatalf("observation %s replaced at restart", before.ID)
		}
	}
}

func TestUsagePendingResetAndPlanChangeCloseWithInvalidEvidence(t *testing.T) {
	for _, change := range []string{"reset", "plan"} {
		t.Run(change, func(t *testing.T) {
			m := newUsageTestMux(t)
			m.usageTelemetry.delays = []time.Duration{0}
			start := time.Now().UTC()
			before := usageTestSnapshot(start, 10)
			zero := usageTestSameEpoch(before, start.Add(3*time.Second), 10)
			terminal := usageTestSameEpoch(before, start.Add(6*time.Second), 12)
			if change == "plan" {
				terminal.PlanType = "prolite"
			} else {
				reset := *terminal.RateLimits.Primary.ResetsAt + 300
				terminal.RateLimits.Primary.ResetsAt, terminal.RateLimits.Secondary.ResetsAt = &reset, &reset
			}
			usageTestReadSequence(t, m, before, zero, terminal)
			usageTestFinishedRequest(t, m, "one", start.Add(time.Second), start.Add(2*time.Second), 100)
			usageTestFinishedRequest(t, m, "two", start.Add(4*time.Second), start.Add(5*time.Second), 200)
			view, err := m.UsageTurn(context.Background(), "one", "turn")
			if err != nil || len(view.Observations) != 2 {
				t.Fatalf("terminal evidence missing: %+v %v", view, err)
			}
			for _, o := range view.Observations {
				if o.ID != "one:"+o.Bucket || o.Plan != "plus" || o.CapacityMultiplier != 1 || o.Eligible || len(o.RequestIDs) != 2 || len(o.Samples) != 2 || o.After.Quality == usage.QuotaAwaitingSignal {
					t.Fatalf("invalid interval mutated baseline or remained trainable: %+v", o)
				}
				want := "quota-epoch-changed-or-unknown"
				if change == "plan" {
					want = "plan-changed"
					if o.After.Quality != want {
						t.Fatalf("plan annotation absent: %+v", o)
					}
				}
				if o.EligibilityReason != want {
					t.Fatalf("eligibility=%q want=%q", o.EligibilityReason, want)
				}
			}
			pending, err := m.usageLedger.PendingQuotaObservations(context.Background())
			if err != nil || len(pending) != 0 || len(m.usageInterval("primary").requests) != 0 {
				t.Fatalf("invalid interval did not close: pending=%+v %v", pending, err)
			}
		})
	}
}

func TestUsageMetadataReadIsReadOnlyAndPreservesMissingCounters(t *testing.T) {
	var methods []string
	snapshot, err := readUsageAccountSnapshot(context.Background(), "primary", func(_ context.Context, method string, params json.RawMessage) (protocol.Message, error) {
		methods = append(methods, method)
		if method == "account/read" {
			if string(params) != `{"refreshToken":false}` {
				t.Fatalf("telemetry requested credential refresh: %s", params)
			}
			return protocol.Message{Result: json.RawMessage(`{"account":{"type":"chatgpt","planType":"plus","email":"not-retained@example.invalid"}}`)}, nil
		}
		return protocol.Message{Result: json.RawMessage(`{"rateLimits":{"primary":{"windowDurationMins":300,"resetsAt":2000000000}},"rateLimitsByLimitId":{"codex":{"limitId":"codex","primary":{"usedPercent":0,"windowDurationMins":300,"resetsAt":2000000000},"secondary":{"windowDurationMins":10080,"resetsAt":2000000000}}}}`)}, nil
	})
	if err != nil || !reflect.DeepEqual(methods, []string{"account/read", "account/rateLimits/read"}) || snapshot.PlanType != "plus" || snapshot.RawAccount != nil || snapshot.Email != "" || snapshot.RateLimits.Primary != nil {
		t.Fatalf("read-only metadata capture mismatch: %+v %v", snapshot, err)
	}
	if got := usageQuotaSnapshot(snapshot, usage.BucketShort); got.UsedPercent == nil || *got.UsedPercent != 0 || got.Source != "account/rateLimits/read:codex" {
		t.Fatalf("reported zero lost: %+v", got)
	}
	if got := usageQuotaSnapshot(snapshot, usage.BucketWeekly); got.UsedPercent != nil {
		t.Fatalf("missing quota counter fabricated as zero: %+v", got)
	}
	_, err = readUsageAccountSnapshot(context.Background(), "primary", func(context.Context, string, json.RawMessage) (protocol.Message, error) {
		return protocol.Message{}, context.DeadlineExceeded
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("telemetry read timeout not returned: %v", err)
	}
	m := newUsageTestMux(t)
	m.markRuntimeHealthy("primary", "ready")
	before := m.runtimeState("primary")
	_, _ = m.accountSnapshotForUsage(context.Background(), "primary")
	after := m.runtimeState("primary")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed telemetry metadata read mutated account health: before=%+v after=%+v", before, after)
	}
}
