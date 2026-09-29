package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

const QuotaAwaitingSignal = "awaiting-quota-signal"

func observationFromView(v ObservationView) QuotaObservation {
	return QuotaObservation{ID: v.ID, AccountID: v.AccountID, Plan: v.Plan, Bucket: v.Bucket,
		CapacityMultiplier: v.CapacityMultiplier, Before: v.Before, After: v.After,
		RequestIDs: append([]string{}, v.RequestIDs...), Samples: append([]QuotaSnapshot(nil), v.Samples...)}
}

func (m *Manager) pendingQuotaObservations(ctx context.Context) ([]QuotaObservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, err := m.db.QueryContext(ctx, `SELECT observation_json FROM observations ORDER BY before_ns,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []QuotaObservation{}
	for rows.Next() {
		var raw string
		var view ObservationView
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &view); err != nil {
			return nil, err
		}
		if view.After.Quality == QuotaAwaitingSignal {
			result = append(result, observationFromView(view))
		}
	}
	return result, rows.Err()
}

func sameJSON(a, b any) bool {
	x, e1 := json.Marshal(a)
	y, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && string(x) == string(y)
}

// An open, quantized observation can grow, but its baseline and prior members
// cannot change. Final observations remain immutable evidence.
func prepareObservationExtension(saved ObservationView, next QuotaObservation) (QuotaObservation, bool, error) {
	previous := observationFromView(saved)
	oldCore, newCore := previous, next
	oldCore.Samples = nil
	newCore.Samples = nil
	if sameJSON(oldCore, newCore) {
		known := true
		for _, s := range next.Samples {
			found := false
			for _, old := range previous.Samples {
				if sameJSON(s, old) {
					found = true
					break
				}
			}
			known = known && found
		}
		if known {
			return previous, false, nil
		}
	}
	if previous.After.Quality != QuotaAwaitingSignal {
		return next, false, ErrObservationImmutable
	}
	if previous.ID != next.ID || previous.AccountID != next.AccountID || previous.Bucket != next.Bucket || previous.Plan != next.Plan || previous.CapacityMultiplier != next.CapacityMultiplier || !sameJSON(previous.Before, next.Before) || !next.After.ObservedAt.After(previous.After.ObservedAt) {
		return next, false, ErrInvalidObservationExtension
	}
	seen := map[string]bool{}
	for _, id := range next.RequestIDs {
		if id == "" || seen[id] {
			return next, false, ErrInvalidObservationExtension
		}
		seen[id] = true
	}
	for _, id := range previous.RequestIDs {
		if !seen[id] {
			return next, false, ErrInvalidObservationExtension
		}
	}
	merged := append([]QuotaSnapshot(nil), previous.Samples...)
	for _, s := range append([]QuotaSnapshot{previous.After}, next.Samples...) {
		found := false
		for _, old := range merged {
			// Quality is an observation-state annotation; preserve the first
			// raw sample when the same reading is supplied again.
			a, b := s, old
			a.Quality = ""
			b.Quality = ""
			if sameJSON(a, b) {
				found = true
				break
			}
			if s.ObservedAt.Equal(old.ObservedAt) {
				return next, false, ErrInvalidObservationExtension
			}
		}
		if !found {
			merged = append(merged, s)
		}
	}
	next.Samples = merged
	return next, true, nil
}

func quotaEpochKey(snapshot QuotaSnapshot) string {
	if snapshot.ResetsAt == nil || snapshot.WindowDurationMins == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d:%d", *snapshot.WindowDurationMins, *snapshot.ResetsAt)
}

func buildObservationView(o QuotaObservation) ObservationView {
	v := ObservationView{ID: o.ID, AccountID: o.AccountID, Plan: o.Plan, Bucket: o.Bucket,
		CapacityMultiplier: o.CapacityMultiplier, Before: o.Before, After: o.After, Samples: append([]QuotaSnapshot(nil), o.Samples...),
		RequestIDs: append([]string{}, o.RequestIDs...), RootTurnIDs: []string{}}
	bad := func(reason string) ObservationView { v.EligibilityReason = reason; return v }
	if o.Before.UsedPercent == nil || o.After.UsedPercent == nil {
		return bad("quota-reading-missing")
	}
	a, b := *o.Before.UsedPercent, *o.After.UsedPercent
	if !finite(a) || !finite(b) || a < 0 || a > 100 || b < 0 || b > 100 {
		return bad("quota-reading-invalid")
	}
	if o.Before.ObservedAt.IsZero() || o.After.ObservedAt.IsZero() || !o.After.ObservedAt.After(o.Before.ObservedAt) {
		return bad("quota-time-invalid")
	}
	if quotaEpochKey(o.Before) == "unknown" || quotaEpochKey(o.Before) != quotaEpochKey(o.After) {
		return bad("quota-epoch-changed-or-unknown")
	}
	if *o.Before.WindowDurationMins <= 0 || *o.Before.ResetsAt <= o.Before.ObservedAt.Unix() || *o.After.ResetsAt <= o.After.ObservedAt.Unix() {
		return bad("quota-reset-crossed")
	}
	if b < a {
		return bad("quota-reading-decreased")
	}
	// Keep the snapshots, but do not publish a delta across incompatible
	// subscription plans or an unsupported quota pool, even for display.
	if o.Before.Quality == "unsupported-quota-pool" || o.After.Quality == "unsupported-quota-pool" {
		return bad("unsupported-quota-pool")
	}
	if o.Before.Quality == "plan-changed" || o.After.Quality == "plan-changed" {
		return bad("plan-changed")
	}
	delta := b - a
	v.RawDeltaPP = &delta
	if !finite(o.CapacityMultiplier) || (o.CapacityMultiplier != 1 && o.CapacityMultiplier != 5 && o.CapacityMultiplier != 20) {
		return bad("account-capacity-unknown")
	}
	x20 := delta * o.CapacityMultiplier / 20
	v.ObservedPercentX20 = &x20
	if o.After.Quality == QuotaAwaitingSignal {
		return bad(QuotaAwaitingSignal)
	}
	if delta == 0 {
		return bad("quota-quantized-or-delayed")
	}
	return v
}

func observationEligibility(o QuotaObservation, v ObservationView, validMembers bool) bool {
	return validMembers && o.After.Quality != QuotaAwaitingSignal && v.EligibilityReason == "" && v.CalibrationEnabled && v.ObservedPercentX20 != nil &&
		*v.ObservedPercentX20 > 0 && len(o.RequestIDs) > 0 && len(v.RootTurnIDs) > 0
}

func refreshObservationsForRequest(ctx context.Context, tx *sql.Tx, requestID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT o.observation_json FROM observations o JOIN observation_members m ON m.observation_id=o.id WHERE m.request_id=?`, requestID)
	if err != nil {
		return err
	}
	var saved []ObservationView
	for rows.Next() {
		var raw string
		var view ObservationView
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &view)
		}
		if err != nil {
			rows.Close()
			return err
		}
		saved = append(saved, view)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, old := range saved {
		updated, err := refreshObservationView(ctx, tx, old)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(updated)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE observations SET observation_json=? WHERE id=?`, string(encoded), old.ID); err != nil {
			return err
		}
	}
	return nil
}
