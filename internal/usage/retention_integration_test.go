package usage

import (
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openRetentionTestLedger(t *testing.T, path string, opts Options) *Manager {
	t.Helper()
	manager, err := Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func usageTestRateCards() []RateCard {
	return []RateCard{{
		Version:                  "fixture-2026-09",
		Model:                    "gpt-6-astra",
		APITier:                  "standard",
		InputUSDPerMillion:       10,
		CachedInputUSDPerMillion: 1,
		OutputUSDPerMillion:      20,
	}}
}

func usageTestRecord(id, rootID, accountID string, started time.Time) Record {
	input, cached, output := int64(1000), int64(100), int64(300)
	total := input + output
	fast := false
	threadID, turnID := splitRootTurnID(rootID)
	return Record{
		RequestID:      id,
		RootTurnID:     rootID,
		AccountID:      accountID,
		AccountPlan:    "pro",
		ModelRequested: "gpt-6-astra",
		ModelServed:    "gpt-6-astra",
		TierRequested:  "standard",
		TierServed:     "standard",
		ThreadID:       threadID,
		TurnID:         turnID,
		Outcome:        "completed",
		StartedAt:      started,
		Usage: &TokenUsage{
			InputTokens:       &input,
			CachedInputTokens: &cached,
			OutputTokens:      &output,
			TotalTokens:       &total,
			Source:            "fixture",
			Quality:           "complete",
		},
		Fast: &fast,
	}
}

func addUsageRequest(t *testing.T, ctx context.Context, manager *Manager, record Record, finished time.Time) {
	t.Helper()
	if err := manager.Begin(ctx, record); err != nil {
		t.Fatal(err)
	}
	final := record
	final.FinishedAt = finished
	if err := manager.Finish(ctx, record.RequestID, final); err != nil {
		t.Fatal(err)
	}
}

func addCompletedUsageTree(t *testing.T, ctx context.Context, manager *Manager, rootID, accountID string, started time.Time, withAgent bool) {
	t.Helper()
	rootRequestID := "root-" + rootID
	root := usageTestRecord(rootRequestID, rootID, accountID, started)
	addUsageRequest(t, ctx, manager, root, started.Add(time.Second))
	if withAgent {
		child := usageTestRecord("child-"+rootID, rootID, accountID, started.Add(100*time.Millisecond))
		child.ParentRequestID = rootRequestID
		child.AgentID = "agent-" + rootID
		child.Subagent = true
		addUsageRequest(t, ctx, manager, child, started.Add(2*time.Second))
	}
	if err := manager.CompleteTurn(ctx, rootID); err != nil {
		t.Fatal(err)
	}
}

func usageTestQuota(at time.Time, used float64, resetAt int64) QuotaSnapshot {
	duration := int64(10080)
	return QuotaSnapshot{
		UsedPercent:        &used,
		ObservedAt:         at,
		WindowDurationMins: &duration,
		ResetsAt:           &resetAt,
		Source:             "fixture",
		Quality:            "fresh",
	}
}

func findUsageBucket(status Status, bucket string) (BucketStatus, bool) {
	for _, item := range status.Buckets {
		if item.Bucket == bucket {
			return item, true
		}
	}
	return BucketStatus{}, false
}

func assertNear(t *testing.T, label string, got, want, tolerance float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > tolerance {
		t.Fatalf("%s: got %.12g, want %.12g ± %.12g", label, got, want, tolerance)
	}
}

func TestUsageLedgerRetainsOldRootsAcrossRestartAndEvictsWholeTreeAtCap(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage-ledger.sqlite")
	opts := Options{MaxRootTurns: 2, RateCards: usageTestRateCards()}
	manager := openRetentionTestLedger(t, path, opts)

	old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	current := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	addCompletedUsageTree(t, ctx, manager, "old-thread:old-turn", "acct-a", old, true)
	addCompletedUsageTree(t, ctx, manager, "current-thread:current-turn", "acct-a", current, true)

	// Retention is count based, not an age expiry. An old tree remains while it
	// is within the configured root-turn cap, including after reopening SQLite.
	if status, err := manager.Status(ctx); err != nil || status.RootTurnCount != 2 || status.MaxRootTurns != 2 {
		t.Fatalf("unexpected pre-restart retention status: %+v, %v", status, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	manager = openRetentionTestLedger(t, path, opts)
	oldView, err := manager.ViewTurn(ctx, "old-thread:old-turn")
	if err != nil || !oldView.Completed || len(oldView.Requests) != 2 {
		t.Fatalf("old tree was lost or truncated across restart: requests=%d completed=%v err=%v", len(oldView.Requests), oldView.Completed, err)
	}

	addCompletedUsageTree(t, ctx, manager, "new-thread:new-turn", "acct-a", current.Add(time.Hour), true)
	if _, err := manager.ViewTurn(ctx, "old-thread:old-turn"); err == nil {
		t.Fatal("oldest root still exists after exceeding the two-root cap")
	}
	for _, rootID := range []string{"current-thread:current-turn", "new-thread:new-turn"} {
		view, err := manager.ViewTurn(ctx, rootID)
		if err != nil || !view.Completed || len(view.Requests) != 2 {
			t.Fatalf("retained root %q lost part of its tree: requests=%d completed=%v err=%v", rootID, len(view.Requests), view.Completed, err)
		}
	}
	status, err := manager.Status(ctx)
	if err != nil || status.RootTurnCount != 2 {
		t.Fatalf("retention did not return to cap: %+v, %v", status, err)
	}
}

func TestUsageLedgerKeepsActiveRootsOutsideCapAndMarksRestartInterrupted(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage-ledger.sqlite")
	opts := Options{MaxRootTurns: 1}
	manager := openRetentionTestLedger(t, path, opts)
	started := time.Now().UTC()

	for _, rootID := range []string{"active-a:turn-a", "active-b:turn-b"} {
		request := usageTestRecord("request-"+rootID, rootID, "acct-a", started)
		request.Usage = nil
		request.Fast = nil
		request.Outcome = "in-progress"
		if err := manager.Begin(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	for _, rootID := range []string{"active-a:turn-a", "active-b:turn-b"} {
		view, err := manager.ViewTurn(ctx, rootID)
		if err != nil || view.Completed || len(view.Requests) != 1 {
			t.Fatalf("active root %q was incorrectly evicted at the completed-root cap: %+v, %v", rootID, view, err)
		}
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	manager = openRetentionTestLedger(t, path, opts)
	for _, rootID := range []string{"active-a:turn-a", "active-b:turn-b"} {
		view, err := manager.ViewTurn(ctx, rootID)
		if err != nil || view.Completed || len(view.Requests) != 1 {
			t.Fatalf("uncompleted root %q was not retained across restart: %+v, %v", rootID, view, err)
		}
		record := view.Requests[0]
		if record.Outcome != "interrupted-restart" || !record.FinishedAt.IsZero() || record.Usage != nil {
			t.Fatalf("reopened active request must be diagnostic and non-trainable: %+v", record)
		}
	}
}

func TestUsageLedgerNormalizesObservedQuotaToPro20WithoutChangingAPICost(t *testing.T) {
	ctx := context.Background()
	manager := openRetentionTestLedger(t, filepath.Join(t.TempDir(), "usage-ledger.sqlite"), Options{
		MaxRootTurns: 4,
		RateCards:    usageTestRateCards(),
	})
	started := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	resetAt := started.Add(24 * time.Hour).Unix()
	cases := []struct {
		name     string
		capacity float64
		wantX20  float64
	}{
		{name: "plus-x1", capacity: 1, wantX20: 0.5},
		{name: "pro-x5", capacity: 5, wantX20: 2.5},
		{name: "pro-x20", capacity: 20, wantX20: 10},
	}
	var firstAPICost float64
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rootID := tc.name + ":turn"
			requestID := "request-" + tc.name
			request := usageTestRecord(requestID, rootID, tc.name, started)
			request.AccountPlan = tc.name
			request.Fast = boolPointer(false)
			addUsageRequest(t, ctx, manager, request, started.Add(time.Second))
			if err := manager.CompleteTurn(ctx, rootID); err != nil {
				t.Fatal(err)
			}
			observation := QuotaObservation{
				ID:                 "obs-" + tc.name,
				AccountID:          tc.name,
				Plan:               tc.name,
				Bucket:             BucketWeekly,
				CapacityMultiplier: tc.capacity,
				Before:             usageTestQuota(started, 20, resetAt),
				After:              usageTestQuota(started.Add(2*time.Second), 30, resetAt),
				RequestIDs:         []string{requestID},
			}
			if err := manager.ObserveQuota(ctx, observation); err != nil {
				t.Fatal(err)
			}
			view, err := manager.ViewTurn(ctx, rootID)
			if err != nil || len(view.Observations) != 1 {
				t.Fatalf("missing quota observation: %+v, %v", view, err)
			}
			if view.Observations[0].ObservedPercentX20 == nil {
				t.Fatalf("normalized observed value is unavailable: %+v", view.Observations[0])
			}
			assertNear(t, "observed ×20", *view.Observations[0].ObservedPercentX20, tc.wantX20, 1e-9)
			if len(view.Requests) != 1 || view.Requests[0].APICost.USD == nil {
				t.Fatalf("API cost unavailable: %+v", view.Requests)
			}
			assertNear(t, "API USD", *view.Requests[0].APICost.USD, 0.0151, 1e-9)
			if firstAPICost == 0 {
				firstAPICost = *view.Requests[0].APICost.USD
			} else {
				assertNear(t, "API USD independent of subscription capacity", *view.Requests[0].APICost.USD, firstAPICost, 1e-12)
			}
		})
	}
}

func TestSharedObservationTrainsOnceOnlyWhenAllRootsAreSelectedAndSurvivesPeerEviction(t *testing.T) {
	ctx := context.Background()
	manager := openRetentionTestLedger(t, filepath.Join(t.TempDir(), "usage-ledger.sqlite"), Options{
		MaxRootTurns: 2,
		RateCards:    usageTestRateCards(),
	})
	started := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	resetAt := started.Add(24 * time.Hour).Unix()
	rootA, rootB := "thread-a:turn-a", "thread-b:turn-b"
	requestA, requestB := "request-a", "request-b"

	for _, item := range []struct{ id, root string }{{requestA, rootA}, {requestB, rootB}} {
		request := usageTestRecord(item.id, item.root, "acct-shared", started)
		addUsageRequest(t, ctx, manager, request, started.Add(time.Second))
		if err := manager.CompleteTurn(ctx, item.root); err != nil {
			t.Fatal(err)
		}
	}
	observation := QuotaObservation{
		ID:                 "shared-observation",
		AccountID:          "acct-shared",
		Plan:               "pro",
		Bucket:             BucketWeekly,
		CapacityMultiplier: 20,
		Before:             usageTestQuota(started, 20, resetAt),
		After:              usageTestQuota(started.Add(2*time.Second), 30, resetAt),
		RequestIDs:         []string{requestA, requestB},
	}
	if err := manager.ObserveQuota(ctx, observation); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bucket, ok := findUsageBucket(status, BucketWeekly); ok && bucket.SampleCount != 0 {
		t.Fatalf("unselected roots trained the model: %+v", bucket)
	}

	if err := manager.SetCalibration(ctx, rootA, true); err != nil {
		t.Fatal(err)
	}
	viewA, err := manager.ViewTurn(ctx, rootA)
	if err != nil || len(viewA.Observations) != 1 || viewA.Observations[0].Eligible || viewA.Observations[0].CalibrationEnabled {
		t.Fatalf("partial member selection should not train a shared observation: %+v, %v", viewA, err)
	}
	if err := manager.SetCalibration(ctx, rootB, true); err != nil {
		t.Fatal(err)
	}
	viewA, err = manager.ViewTurn(ctx, rootA)
	if err != nil || len(viewA.Observations) != 1 || !viewA.Observations[0].Eligible || !viewA.Observations[0].CalibrationEnabled {
		t.Fatalf("fully selected shared observation was not eligible: %+v, %v", viewA, err)
	}
	if len(viewA.Observations[0].RequestIDs) != 2 || len(viewA.Observations[0].RootTurnIDs) != 2 {
		t.Fatalf("shared observation membership was duplicated or lost: %+v", viewA.Observations[0])
	}
	status, err = manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	weekly, ok := findUsageBucket(status, BucketWeekly)
	if !ok || weekly.SampleCount != 1 {
		t.Fatalf("one shared quota delta must contribute exactly one calibration sample, got %+v", status.Buckets)
	}

	// A third completed root evicts rootA at the two-root cap. The historical
	// shared measurement remains visible from retained rootB, but must stop
	// training because the full member set can no longer be reconstructed.
	addCompletedUsageTree(t, ctx, manager, "thread-c:turn-c", "acct-shared", started.Add(time.Hour), false)
	viewB, err := manager.ViewTurn(ctx, rootB)
	if err != nil || len(viewB.Observations) != 1 {
		t.Fatalf("retained peer lost shared observation evidence: %+v, %v", viewB, err)
	}
	shared := viewB.Observations[0]
	if shared.ID != observation.ID || len(shared.RequestIDs) != 2 || len(shared.RootTurnIDs) != 2 {
		t.Fatalf("eviction erased or rewrote the original shared membership: %+v", shared)
	}
	if shared.Eligible {
		t.Fatalf("observation with an evicted member still trains: %+v", shared)
	}
	status, err = manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	weekly, ok = findUsageBucket(status, BucketWeekly)
	if ok && weekly.SampleCount != 0 {
		t.Fatalf("evicted shared observation still contributes to calibration: %+v", weekly)
	}
}

func boolPointer(value bool) *bool { return &value }

func TestUsageLedgerRejectsDowngradeWithoutChangingDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage-ledger.sqlite")
	manager := openRetentionTestLedger(t, path, Options{MaxRootTurns: 2})
	rootID := "thread:turn"
	addCompletedUsageTree(t, ctx, manager, rootID, "acct-a", time.Now().UTC(), false)
	before, err := manager.ViewTurn(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a future schema. An older binary must leave the database intact.
	dsn := sqliteFileURL(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 999`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	beforeDowngrade, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, Options{MaxRootTurns: 2}); err == nil {
		t.Fatal("opened a usage database from a newer schema")
	}
	afterDowngrade, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterDowngrade) != string(beforeDowngrade) {
		t.Fatal("downgrade attempt modified the newer database")
	}
	// Restore the fixture version so the manager can read and compare its rows.
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	manager = openRetentionTestLedger(t, path, Options{MaxRootTurns: 2})
	after, err := manager.ViewTurn(ctx, rootID)
	if err != nil || len(after.Requests) != len(before.Requests) || after.RootTurnID != before.RootTurnID {
		t.Fatalf("downgrade attempt changed retained usage data: before=%+v after=%+v err=%v", before, after, err)
	}
}
