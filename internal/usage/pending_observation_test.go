package usage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPendingObservationExtendsOneSamplePreservesZeroWorkAndRequiresReview(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	opts := Options{RateCards: usageTestRateCards()}
	m := openRetentionTestLedger(t, path, opts)
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	reset := start.Add(time.Hour).Unix()
	a := usageTestRecord("a", "thread:a", "account", start)
	addUsageRequest(t, ctx, m, a, start.Add(time.Second))
	zero := usageTestQuota(start.Add(2*time.Second), 10, reset)
	pending := QuotaObservation{ID: "group", AccountID: a.AccountID, Plan: a.AccountPlan, Bucket: BucketShort, CapacityMultiplier: 20,
		Before: usageTestQuota(start, 10, reset), After: zero, RequestIDs: []string{a.RequestID}, Samples: []QuotaSnapshot{zero}}
	pending.After.Quality = QuotaAwaitingSignal
	if err := m.ObserveQuota(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if err := m.SetCalibration(ctx, a.RootTurnID, true); err != nil {
		t.Fatal(err)
	}
	v, err := m.ViewTurn(ctx, a.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Observations[0].Eligible || v.Observations[0].ObservedPercentX20 == nil || *v.Observations[0].ObservedPercentX20 != 0 {
		t.Fatalf("pending zero was hidden or trains: %+v", v.Observations)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m = openRetentionTestLedger(t, path, opts)
	recovered, err := m.PendingQuotaObservations(ctx)
	if err != nil || len(recovered) != 1 {
		t.Fatalf("pending recovery: %+v %v", recovered, err)
	}
	if !sameJSON(recovered[0], pending) {
		t.Fatal("recovery rewrote raw pending evidence")
	}
	b := usageTestRecord("b", "thread:b", "account", start.Add(5*time.Second))
	addUsageRequest(t, ctx, m, b, start.Add(6*time.Second))
	if err = m.SetCalibration(ctx, b.RootTurnID, true); err != nil {
		t.Fatal(err)
	}
	final := pending
	final.RequestIDs = []string{a.RequestID, b.RequestID}
	final.After = usageTestQuota(start.Add(7*time.Second), 12, reset)
	final.Samples = []QuotaSnapshot{final.After}
	if err = m.ObserveQuota(ctx, final); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{a.RootTurnID, b.RootTurnID} {
		view, err := m.ViewTurn(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if view.CalibrationEnabled {
			t.Fatal("expanded composition kept earlier attestation")
		}
	}
	v, err = m.ViewTurn(ctx, a.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Observations) != 1 || len(v.Observations[0].Samples) != 2 || len(v.Observations[0].RequestIDs) != 2 {
		t.Fatalf("extension lost or duplicated evidence: %+v", v.Observations)
	}
	if *v.Observations[0].Samples[0].UsedPercent != 10 || *v.Observations[0].ObservedPercentX20 != 2 {
		t.Fatal("raw zero or full cumulative quota was not preserved")
	}
	if err = m.SetCalibrationBulk(ctx, []string{a.RootTurnID, b.RootTurnID}, true, v.Revision); err != nil {
		t.Fatal(err)
	}
	status, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bucket, _ := findUsageBucket(status, BucketShort)
	if bucket.SampleCount != 1 {
		t.Fatalf("one cumulative delta trained %d samples", bucket.SampleCount)
	}
	next := usageTestRecord("next", "thread:next", "account", start.Add(time.Minute))
	addUsageRequest(t, ctx, m, next, next.StartedAt.Add(time.Second))
	v, err = m.ViewTurn(ctx, next.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if v.EstimatedShort.PercentX20 == nil {
		t.Fatal("prediction unavailable")
	}
	assertNear(t, "zero-work included in estimator", *v.EstimatedShort.PercentX20, 1, 1e-9)
	if err = m.ObserveQuota(ctx, final); err != nil {
		t.Fatalf("exact final replay should be idempotent: %v", err)
	}
	changed := final
	changed.After = usageTestQuota(start.Add(8*time.Second), 13, reset)
	if err = m.ObserveQuota(ctx, changed); !errors.Is(err, ErrObservationImmutable) {
		t.Fatalf("final evidence mutated: %v", err)
	}
	if pending, err := m.PendingQuotaObservations(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("finalized interval still pending: %+v %v", pending, err)
	}
}

func TestPendingObservationScopeAndHistoryAreImmutable(t *testing.T) {
	m, ctx, start := calibrationFixture(t, Options{RateCards: usageTestRateCards()})
	reset := start.Add(time.Hour).Unix()
	r := usageTestRecord("a", "t:a", "account", start)
	addUsageRequest(t, ctx, m, r, start.Add(time.Second))
	before := usageTestQuota(start, 10, reset)
	after := usageTestQuota(start.Add(2*time.Second), 10, reset)
	after.Quality = QuotaAwaitingSignal
	o := QuotaObservation{ID: "o", AccountID: r.AccountID, Plan: r.AccountPlan, Bucket: BucketShort, CapacityMultiplier: 20, Before: before, After: after, RequestIDs: []string{r.RequestID}, Samples: []QuotaSnapshot{after}}
	if err := m.ObserveQuota(ctx, o); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*QuotaObservation)
	}{
		{"baseline", func(n *QuotaObservation) { n.Before.UsedPercent = pricingFloat(9) }},
		{"capacity", func(n *QuotaObservation) { n.CapacityMultiplier = 5 }},
		{"account", func(n *QuotaObservation) { n.AccountID = "other" }},
		{"member removed", func(n *QuotaObservation) { n.RequestIDs = []string{} }},
		{"duplicate member", func(n *QuotaObservation) { n.RequestIDs = []string{"a", "a"} }},
		{"time", func(n *QuotaObservation) { n.After.ObservedAt = start }},
		{"old sample", func(n *QuotaObservation) {
			changed := after
			changed.UsedPercent = pricingFloat(99)
			n.Samples = []QuotaSnapshot{changed}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := o
			n.After = usageTestQuota(start.Add(4*time.Second), 11, reset)
			tc.mutate(&n)
			if err := m.ObserveQuota(ctx, n); !errors.Is(err, ErrInvalidObservationExtension) {
				t.Fatalf("invalid mutation accepted: %v", err)
			}
		})
	}
	view, err := m.ViewTurn(ctx, r.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if *view.Observations[0].Before.UsedPercent != 10 || *view.Observations[0].After.UsedPercent != 10 {
		t.Fatal("failed extension changed evidence")
	}
}

func TestPendingPositiveBucketDoesNotTrainUntilOtherWindowSettles(t *testing.T) {
	m, ctx, start := calibrationFixture(t, Options{RateCards: usageTestRateCards()})
	reset := start.Add(time.Hour).Unix()
	r := usageTestRecord("a", "t:a", "account", start)
	addUsageRequest(t, ctx, m, r, start.Add(time.Second))
	o := QuotaObservation{ID: "o", AccountID: r.AccountID, Plan: r.AccountPlan, Bucket: BucketShort, CapacityMultiplier: 20, Before: usageTestQuota(start, 10, reset), After: usageTestQuota(start.Add(2*time.Second), 11, reset), RequestIDs: []string{r.RequestID}}
	o.After.Quality = QuotaAwaitingSignal
	if err := m.ObserveQuota(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := m.SetCalibration(ctx, r.RootTurnID, true); err != nil {
		t.Fatal(err)
	}
	v, err := m.ViewTurn(ctx, r.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Observations[0].Eligible || v.Observations[0].ObservedPercentX20 == nil {
		t.Fatal("pending positive must remain visible without training")
	}
	// A reset closes the accumulated interval as invalid; it must not survive
	// restart as a pending candidate or subtract quotas from different epochs.
	o.After = usageTestQuota(start.Add(4*time.Second), 1, reset+3600)
	if err = m.ObserveQuota(ctx, o); err != nil {
		t.Fatal(err)
	}
	v, err = m.ViewTurn(ctx, r.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Observations[0].Eligible || v.Observations[0].ObservedPercentX20 != nil {
		t.Fatal("reset interval fabricated a normalized delta")
	}
	pending, err := m.PendingQuotaObservations(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatal("reset did not close pending observation")
	}
}

func TestIncompatiblePlanOrPoolKeepsSnapshotsWithoutDisplayDelta(t *testing.T) {
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	reset := start.Add(time.Hour).Unix()
	for _, quality := range []string{"plan-changed", "unsupported-quota-pool"} {
		for _, atBefore := range []bool{false, true} {
			o := QuotaObservation{ID: "o", AccountID: "account", Plan: "pro", Bucket: BucketShort, CapacityMultiplier: 20,
				Before: usageTestQuota(start, 10, reset), After: usageTestQuota(start.Add(time.Second), 12, reset), RequestIDs: []string{"r"}}
			if atBefore {
				o.Before.Quality = quality
			} else {
				o.After.Quality = quality
			}
			v := buildObservationView(o)
			if v.RawDeltaPP != nil || v.ObservedPercentX20 != nil {
				t.Fatalf("%s published invalid display delta: %+v", quality, v)
			}
			if v.EligibilityReason != quality || *v.Before.UsedPercent != 10 || *v.After.UsedPercent != 12 {
				t.Fatalf("%s lost raw evidence: %+v", quality, v)
			}
		}
	}
}
