package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestEstimatorPendingScalarAndCollinearity(t *testing.T) {
	if f := fitSamples(BucketShort, 0, 2.5, nil); len(f.SampleIDs) != 0 || f.Scale != 0 || f.Mode != 0 {
		t.Fatalf("invented calibration: %+v", f)
	}
	s := trainingSample{ID: "one", Y: 3, Parts: []trainingPart{{Model: "gpt-6-sol", Features: [3]float64{.2, .1, .2}}}}
	f := fitSamples(BucketShort, 1, 2.5, []trainingSample{s})
	assertNear(t, "first scale", f.Scale, 6, 1e-12)
	if f.Mode != 0 || len(f.SampleIDs) != 1 {
		t.Fatalf("one sample must remain scalar: %+v", f)
	}
	var repeated []trainingSample
	for i := 0; i < 60; i++ {
		copy := s
		copy.ID = fmt.Sprint(i)
		repeated = append(repeated, copy)
	}
	f = fitSamples(BucketShort, 2, 2.5, repeated)
	if f.Mode != 0 {
		t.Fatalf("repeated collinear data fabricated identifiable features: %+v", f)
	}
}

func TestEstimatorRecoversIdentifiableCategoryModelFastAndPlanEffects(t *testing.T) {
	for _, test := range []struct{ mode, models, fastModes, plans int }{{1, 1, 1, 1}, {2, 2, 1, 1}, {3, 1, 2, 1}, {4, 1, 2, 2}} {
		t.Run(fmt.Sprint(test.mode), func(t *testing.T) {
			var samples []trainingSample
			for model := 0; model < test.models; model++ {
				for fast := 0; fast < test.fastModes; fast++ {
					for plan := 0; plan < test.plans; plan++ {
						for category := 0; category < 3; category++ {
							for repeat := 0; repeat < 6; repeat++ {
								p := trainingPart{Model: fmt.Sprint("model-", model), Fast: fast == 1, FastObserved: true, Capacity: float64(1 + 19*plan)}
								p.Features[category] = float64(1+repeat) / 100
								coefficient := float64(2 + category + model*3 + fast*5 + plan*7)
								samples = append(samples, trainingSample{ID: fmt.Sprint(len(samples)), Y: p.Features[category] * coefficient, Parts: []trainingPart{p}})
							}
						}
					}
				}
			}
			f := fitSamples(BucketShort, 1, 2.5, samples)
			if f.Mode != test.mode {
				t.Fatalf("mode=%d want %d", f.Mode, test.mode)
			}
			for _, s := range samples {
				assertNear(t, "recovered prediction", f.predictPart(s.Parts[0]), s.Y, 1e-4)
			}
		})
	}
}

func TestEstimatorResistsScalarOutlierAndDoesNotLearnRequestedFast(t *testing.T) {
	var samples []trainingSample
	for i := 0; i < 21; i++ {
		p := trainingPart{Model: "model", Fast: i%2 == 0, Capacity: 20, Features: [3]float64{1, 2, 3}}
		y := 12.0
		if i == 20 {
			y = 1200
		}
		samples = append(samples, trainingSample{ID: fmt.Sprint(i), Y: y, Parts: []trainingPart{p}})
	}
	f := fitSamples(BucketWeekly, 0, 2.5, samples)
	assertNear(t, "robust scalar", f.Scale, 2, 1e-12)
	if modeSupported(samples, 3) {
		t.Fatal("requested-only tier permits learning Fast effects")
	}
}

func calibrationFixture(t *testing.T, opts Options) (*Manager, context.Context, time.Time) {
	t.Helper()
	m := openRetentionTestLedger(t, filepath.Join(t.TempDir(), "ledger.sqlite"), opts)
	return m, context.Background(), time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
}

func observeFixture(t *testing.T, m *Manager, ctx context.Context, id string, r Record, start time.Time, raw float64, capacity float64) {
	t.Helper()
	reset := start.Add(24 * time.Hour).Unix()
	err := m.ObserveQuota(ctx, QuotaObservation{ID: id, AccountID: r.AccountID, Plan: r.AccountPlan, Bucket: BucketShort, CapacityMultiplier: capacity,
		Before: usageTestQuota(start, 10, reset), After: usageTestQuota(start.Add(2*time.Second), 10+raw, reset), RequestIDs: []string{r.RequestID}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLedgerFreezeBeforeTrainingUnselectAndReplay(t *testing.T) {
	m, ctx, start := calibrationFixture(t, Options{RateCards: usageTestRateCards()})
	r := usageTestRecord("r1", "thread:one", "account", start)
	addUsageRequest(t, ctx, m, r, start.Add(time.Second))
	before, err := m.ViewTurn(ctx, r.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if before.EstimatedShort.PercentX20 != nil {
		t.Fatal("first prediction fabricated an absolute percent")
	}
	observeFixture(t, m, ctx, "obs1", r, start, 2, 20)
	if err = m.SetCalibration(ctx, r.RootTurnID, true); err != nil {
		t.Fatal(err)
	}
	next := usageTestRecord("r2", "thread:two", "account", start.Add(time.Minute))
	addUsageRequest(t, ctx, m, next, next.StartedAt.Add(time.Second))
	view, err := m.ViewTurn(ctx, next.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if view.EstimatedShort.PercentX20 == nil {
		t.Fatalf("calibrated prediction missing: %+v", view)
	}
	assertNear(t, "prediction", *view.EstimatedShort.PercentX20, 2, 1e-9)
	frozen, _ := json.Marshal(view.Requests[0])
	if err = m.SetCalibration(ctx, r.RootTurnID, false); err != nil {
		t.Fatal(err)
	}
	changed := next
	changed.Usage = &TokenUsage{InputTokens: pricingInt(9999), CachedInputTokens: pricingInt(0), OutputTokens: pricingInt(8888)}
	changed.FinishedAt = next.StartedAt.Add(30 * time.Second)
	if err = m.Finish(ctx, next.RequestID, changed); err != nil {
		t.Fatal(err)
	}
	view, err = m.ViewTurn(ctx, next.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	replay, _ := json.Marshal(view.Requests[0])
	if string(frozen) != string(replay) {
		t.Fatal("replay or unselect rewrote frozen evidence")
	}
	first, err := m.ViewTurn(ctx, r.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if first.EstimatedShort.PercentX20 != nil {
		t.Fatal("calibration rewrote its own pre-training prediction")
	}
	status, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bucket, _ := findUsageBucket(status, BucketShort)
	if bucket.SampleCount != 0 {
		t.Fatal("unselected observation still trains")
	}
}

func TestLedgerCalibratesWithoutExactAPIUSDAndPreservesRawNormalization(t *testing.T) {
	m, ctx, start := calibrationFixture(t, Options{})
	r := usageTestRecord("r1", "thread:one", "account", start)
	r.AccountPlan = "plus"
	addUsageRequest(t, ctx, m, r, start.Add(time.Second))
	observeFixture(t, m, ctx, "obs1", r, start, 10, 1)
	if err := m.SetCalibration(ctx, r.RootTurnID, true); err != nil {
		t.Fatal(err)
	}
	view, err := m.ViewTurn(ctx, r.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if view.TotalAPICostUSD != nil || view.TotalKnownAPICostUSD == nil {
		t.Fatal("unknown cache-write count must produce partial API cost")
	}
	if !view.Observations[0].Eligible {
		t.Fatalf("API surcharge absence blocked quota learning: %+v", view.Observations[0])
	}
	assertNear(t, "raw", *view.Observations[0].RawDeltaPP, 10, 1e-12)
	assertNear(t, "x20", *view.Observations[0].ObservedPercentX20, .5, 1e-12)
	next := usageTestRecord("r2", "thread:two", "account-pro", start.Add(time.Minute))
	addUsageRequest(t, ctx, m, next, next.StartedAt.Add(time.Second))
	view, err = m.ViewTurn(ctx, next.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if view.EstimatedShort.PercentX20 == nil {
		t.Fatal("prediction missing")
	}
	assertNear(t, "x20 plan invariant", *view.EstimatedShort.PercentX20, .5, 1e-12)
}

func TestLedgerLateMemberInvalidatesSelectionButReplayDoesNot(t *testing.T) {
	m, ctx, start := calibrationFixture(t, Options{RateCards: usageTestRateCards()})
	r := usageTestRecord("r1", "thread:one", "account", start)
	addUsageRequest(t, ctx, m, r, start.Add(time.Second))
	if err := m.SetCalibration(ctx, r.RootTurnID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Begin(ctx, r); err != nil {
		t.Fatal(err)
	}
	v, err := m.ViewTurn(ctx, r.RootTurnID)
	if err != nil || !v.CalibrationEnabled {
		t.Fatalf("replay changed attestation: %+v %v", v, err)
	}
	child := usageTestRecord("r2", r.RootTurnID, "account", start.Add(time.Minute))
	if err = m.Begin(ctx, child); err != nil {
		t.Fatal(err)
	}
	v, err = m.ViewTurn(ctx, r.RootTurnID)
	if err != nil || v.CalibrationEnabled {
		t.Fatal("new descendant retained old attestation")
	}
	if err = m.SetCalibration(ctx, r.RootTurnID, true); err != nil {
		t.Fatal(err)
	}
	detached := usageTestRecord("r3", "", "account", start.Add(2*time.Minute))
	addUsageRequest(t, ctx, m, detached, detached.StartedAt.Add(time.Second))
	if err = m.SetRelation(ctx, detached.RequestID, r.RootTurnID, "r1", "agent"); err != nil {
		t.Fatal(err)
	}
	v, err = m.ViewTurn(ctx, r.RootTurnID)
	if err != nil || v.CalibrationEnabled {
		t.Fatal("new association retained old attestation")
	}
}

func TestObservationInvalidAndQuantizedDeltas(t *testing.T) {
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	reset := start.Add(time.Hour).Unix()
	base := QuotaObservation{ID: "o", AccountID: "a", Bucket: BucketShort, CapacityMultiplier: 20, Before: usageTestQuota(start, 10, reset), After: usageTestQuota(start.Add(time.Second), 12, reset), RequestIDs: []string{"r"}}
	for _, tc := range []struct {
		name   string
		mutate func(*QuotaObservation)
	}{
		{"zero", func(o *QuotaObservation) { o.After.UsedPercent = pricingFloat(10) }},
		{"negative", func(o *QuotaObservation) { o.After.UsedPercent = pricingFloat(9) }},
		{"reset", func(o *QuotaObservation) { v := reset + 1; o.After.ResetsAt = &v }},
		{"unknown-capacity", func(o *QuotaObservation) { o.CapacityMultiplier = 0 }},
		{"missing", func(o *QuotaObservation) { o.Before.UsedPercent = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			tc.mutate(&o)
			v := buildObservationView(o)
			v.CalibrationEnabled = true
			v.RootTurnIDs = []string{"root"}
			if observationEligibility(o, v, true) {
				t.Fatal("invalid observation can train")
			}
		})
	}
	zero := base
	zero.After.UsedPercent = pricingFloat(10)
	v := buildObservationView(zero)
	if v.ObservedPercentX20 == nil || *v.ObservedPercentX20 != 0 {
		t.Fatal("quantized zero must still be visible")
	}
}

func TestObservationRejectsOmittedKnownRequestsAndOverlappingLabels(t *testing.T) {
	m, ctx, start := calibrationFixture(t, Options{RateCards: usageTestRateCards()})
	a := usageTestRecord("a", "t:a", "acct", start)
	b := usageTestRecord("b", "t:b", "acct", start)
	addUsageRequest(t, ctx, m, a, start.Add(time.Second))
	addUsageRequest(t, ctx, m, b, start.Add(time.Second))
	observeFixture(t, m, ctx, "only-a", a, start, 1, 20)
	if err := m.SetCalibration(ctx, a.RootTurnID, true); err != nil {
		t.Fatal(err)
	}
	v, err := m.ViewTurn(ctx, a.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Observations[0].Eligible || v.Observations[0].EligibilityReason != "known-request-omitted" {
		t.Fatalf("partial known group trains: %+v", v.Observations)
	}
	observeFixture(t, m, ctx, "duplicate-interval", a, start, 1, 20)
	v, err = m.ViewTurn(ctx, a.RootTurnID)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range v.Observations {
		if o.ID == "duplicate-interval" && o.EligibilityReason != "overlapping-quota-interval" {
			t.Fatalf("duplicate interval not rejected: %+v", o)
		}
	}
}
