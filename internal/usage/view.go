package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
)

func (m *Manager) viewTurn(ctx context.Context, rootID string) (TurnView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return TurnView{}, err
	}
	defer tx.Rollback()
	view := TurnView{RootTurnID: rootID, Requests: []RecordView{}, Agents: []AgentBreakdown{}, Observations: []ObservationView{}}
	view.ThreadID, view.TurnID = splitRootTurnID(rootID)
	view.Revision, err = m.revision(ctx, tx)
	if err != nil {
		return view, err
	}
	var selected, completed int
	err = tx.QueryRowContext(ctx, `SELECT thread_id,turn_id,calibration_enabled,completed FROM root_turns WHERE root_turn_id=?`, rootID).Scan(&view.ThreadID, &view.TurnID, &selected, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return view, ErrTurnNotFound
	}
	if err != nil {
		return view, err
	}
	view.CalibrationEnabled, view.Completed = selected == 1, completed == 1
	rows, err := tx.QueryContext(ctx, `SELECT q.record_json,q.api_cost_json,COALESCE(s.estimate_json,'{}'),COALESCE(w.estimate_json,'{}') FROM requests q LEFT JOIN frozen_estimates s ON s.request_id=q.request_id AND s.bucket='short' LEFT JOIN frozen_estimates w ON w.request_id=q.request_id AND w.bucket='weekly' WHERE q.root_turn_id=? ORDER BY q.started_ns,q.request_id`, rootID)
	if err != nil {
		return view, err
	}
	for rows.Next() {
		var raw, cost, short, weekly string
		if err = rows.Scan(&raw, &cost, &short, &weekly); err != nil {
			rows.Close()
			return view, err
		}
		var record RecordView
		for _, pair := range []struct {
			raw    string
			target any
		}{{raw, &record.Record}, {cost, &record.APICost}, {short, &record.EstimatedShort}, {weekly, &record.EstimatedWeekly}} {
			if err = json.Unmarshal([]byte(pair.raw), pair.target); err != nil {
				rows.Close()
				return view, err
			}
		}
		view.Requests = append(view.Requests, record)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return view, err
	}
	rows.Close()
	view.Observations, err = loadObservationViews(ctx, tx, rootID)
	if err != nil {
		return view, err
	}
	for i := range view.Requests {
		for _, o := range view.Observations {
			for _, id := range o.RequestIDs {
				if id != view.Requests[i].RequestID {
					continue
				}
				view.Requests[i].GroupObservationID = o.ID
				if o.Bucket == BucketShort {
					view.Requests[i].SharedObservedShort = o.ObservedPercentX20
				} else {
					view.Requests[i].SharedObservedWeekly = o.ObservedPercentX20
				}
			}
		}
	}
	view.TotalUsage, view.TotalUsageComplete = aggregateUsage(view.Requests)
	view.TotalAPICostUSD = aggregateCost(view.Requests)
	view.TotalKnownAPICostUSD = aggregateKnownCost(view.Requests)
	view.EstimatedShort = aggregateEstimate(view.Requests, false)
	view.EstimatedWeekly = aggregateEstimate(view.Requests, true)
	agents := map[string][]RecordView{}
	for _, r := range view.Requests {
		id := first(r.AgentID, r.ThreadID)
		agents[id] = append(agents[id], r)
	}
	ids := make([]string, 0, len(agents))
	for id := range agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		tokens, _ := aggregateUsage(agents[id])
		view.Agents = append(view.Agents, AgentBreakdown{AgentID: id, RootTurnID: rootID, RequestCount: len(agents[id]), Usage: tokens, APICostUSD: aggregateCost(agents[id])})
	}
	return view, tx.Commit()
}

// Read all JSON before querying members because SQLite has one private
// connection. Shared observations remain one object per measurement ID.
func loadObservationViews(ctx context.Context, tx *sql.Tx, rootID string) ([]ObservationView, error) {
	query := `SELECT observation_json FROM observations ORDER BY before_ns,id`
	args := []any{}
	if rootID != "" {
		query = `SELECT o.observation_json FROM observations o WHERE EXISTS(SELECT 1 FROM observation_members m WHERE m.observation_id=o.id AND m.root_turn_id=?) ORDER BY o.before_ns,o.id`
		args = append(args, rootID)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	result := []ObservationView{}
	for rows.Next() {
		var raw string
		var view ObservationView
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &view)
		}
		if err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, view)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range result {
		result[i], err = refreshObservationView(ctx, tx, result[i])
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func refreshObservationView(ctx context.Context, tx *sql.Tx, saved ObservationView) (ObservationView, error) {
	observation := observationFromView(saved)
	view := buildObservationView(observation)
	view.Revision = saved.Revision
	valid := true
	reason := ""
	if saved.EligibilityReason == "overlapping-quota-interval" {
		valid = false
		reason = saved.EligibilityReason
	}
	roots := map[string]bool{}
	// Preserve the original members even after retention removes a peer.
	for _, id := range saved.RootTurnIDs {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM root_turns WHERE root_turn_id=?`, id).Scan(&exists); err != nil {
			return view, err
		}
		if exists == 0 {
			roots[id] = true
		}
	}
	seen := map[string]bool{}
	for _, id := range saved.RequestIDs {
		if seen[id] {
			valid = false
			reason = first(reason, "duplicate-request-member")
			continue
		}
		seen[id] = true
		var raw, costRaw string
		err := tx.QueryRowContext(ctx, `SELECT record_json,api_cost_json FROM requests WHERE request_id=?`, id).Scan(&raw, &costRaw)
		if errors.Is(err, sql.ErrNoRows) {
			valid = false
			reason = first(reason, "member-history-evicted")
			continue
		}
		if err != nil {
			return view, err
		}
		var record Record
		var cost APICost
		if err = json.Unmarshal([]byte(raw), &record); err != nil {
			return view, err
		}
		if err = json.Unmarshal([]byte(costRaw), &cost); err != nil {
			return view, err
		}
		if record.RootTurnID == "" {
			valid = false
			reason = first(reason, "root-turn-unresolved")
		} else {
			roots[record.RootTurnID] = true
		}
		if record.AccountID != saved.AccountID {
			valid = false
			reason = first(reason, "mixed-account")
		}
		if record.AccountPlan != "" && record.AccountPlan != saved.Plan {
			valid = false
			reason = first(reason, "plan-changed")
		}
		if record.FinishedAt.IsZero() {
			valid = false
			reason = first(reason, "request-unfinished")
		}
		if record.StartedAt.Before(saved.Before.ObservedAt) || record.FinishedAt.After(saved.After.ObservedAt) {
			valid = false
			reason = first(reason, "request-outside-observation")
		}
		if record.Usage == nil || record.Usage.InputTokens == nil || record.Usage.CachedInputTokens == nil || record.Usage.OutputTokens == nil || validatePricingUsage(record.Usage) != "" {
			valid = false
			reason = first(reason, "usage-unavailable")
		}
		if record.Fast == nil && pricingTier(first(record.TierServed, record.TierRequested)) != "standard" && pricingTier(first(record.TierServed, record.TierRequested)) != "fast" {
			valid = false
			reason = first(reason, "fast-mode-unknown")
		}
		if cost.StandardRateCard == nil {
			valid = false
			reason = first(reason, "quota-rate-unavailable")
		}
		if _, featureReason := apiFeatures(record, cost); featureReason != "" {
			valid = false
			reason = first(reason, featureReason)
		}
	}
	// No known request in this account's measured interval may be omitted.
	rows, err := tx.QueryContext(ctx, `SELECT request_id FROM requests WHERE account_id=? AND started_ns < ? AND (finished_ns=0 OR finished_ns>?)`, saved.AccountID, nanos(saved.After.ObservedAt), nanos(saved.Before.ObservedAt))
	if err != nil {
		return view, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return view, err
		}
		if !seen[id] {
			valid = false
			reason = first(reason, "known-request-omitted")
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return view, err
	}
	rows.Close()
	view.RootTurnIDs = []string{}
	for id := range roots {
		view.RootTurnIDs = append(view.RootTurnIDs, id)
	}
	sort.Strings(view.RootTurnIDs)
	view.CalibrationEnabled = len(roots) > 0
	for id := range roots {
		var included int
		if err := tx.QueryRowContext(ctx, `SELECT calibration_enabled FROM root_turns WHERE root_turn_id=?`, id).Scan(&included); err != nil || included != 1 {
			view.CalibrationEnabled = false
		}
	}
	if reason != "" {
		view.EligibilityReason = reason
	}
	view.Eligible = observationEligibility(observation, view, valid && len(seen) > 0)
	if !view.Eligible && view.EligibilityReason == "" {
		view.EligibilityReason = "observation-not-trainable"
	}
	return view, nil
}

func aggregateUsage(records []RecordView) (*TokenUsage, bool) {
	if len(records) == 0 {
		return nil, false
	}
	sum := func(get func(*TokenUsage) *int64) *int64 {
		var total int64
		for _, r := range records {
			if r.Usage == nil {
				return nil
			}
			v := get(r.Usage)
			if v == nil || *v < 0 || total > math.MaxInt64-*v {
				return nil
			}
			total += *v
		}
		return &total
	}
	u := &TokenUsage{
		InputTokens: sum(func(u *TokenUsage) *int64 { return u.InputTokens }), OutputTokens: sum(func(u *TokenUsage) *int64 { return u.OutputTokens }),
		CachedInputTokens: sum(func(u *TokenUsage) *int64 { return u.CachedInputTokens }), CacheWriteTokens: sum(func(u *TokenUsage) *int64 { return u.CacheWriteTokens }),
		TotalTokens: sum(func(u *TokenUsage) *int64 { return u.TotalTokens }), ReasoningTokens: sum(func(u *TokenUsage) *int64 { return u.ReasoningTokens }), Source: "unique-inferences",
	}
	complete := u.InputTokens != nil && u.OutputTokens != nil && u.CachedInputTokens != nil
	for _, r := range records {
		if r.FinishedAt.IsZero() || validatePricingUsage(r.Usage) != "" {
			complete = false
		}
	}
	if complete {
		u.Quality = "complete"
	} else {
		u.Quality = "partial"
	}
	return u, complete
}

func aggregateKnownCost(records []RecordView) *float64 {
	total, known := 0.0, false
	for _, r := range records {
		if r.APICost.USD != nil {
			total += *r.APICost.USD
			known = true
			continue
		}
		for _, v := range []*float64{r.APICost.InputUSD, r.APICost.CachedInputUSD, r.APICost.CacheWriteUSD, r.APICost.OutputUSD} {
			if v != nil {
				total += *v
				known = true
			}
		}
	}
	if !known || !finite(total) {
		return nil
	}
	return &total
}

func aggregateCost(records []RecordView) *float64 {
	if len(records) == 0 {
		return nil
	}
	total := 0.0
	for _, r := range records {
		if r.APICost.USD == nil {
			return nil
		}
		total += *r.APICost.USD
	}
	if !finite(total) {
		return nil
	}
	return &total
}

func aggregateEstimate(records []RecordView, weekly bool) Estimate {
	result := Estimate{UnavailableReason: "pending-calibration", Confidence: "uncalibrated"}
	if len(records) == 0 {
		return result
	}
	total := 0.0
	versions := map[string]bool{}
	for i, r := range records {
		e := r.EstimatedShort
		if weekly {
			e = r.EstimatedWeekly
		}
		if e.PercentX20 == nil {
			result.UnavailableReason = first(e.UnavailableReason, "component-unavailable")
			return result
		}
		total += *e.PercentX20
		versions[e.ModelVersion] = true
		if i == 0 || e.SampleCount < result.SampleCount {
			result.SampleCount = e.SampleCount
		}
	}
	if !finite(total) {
		return result
	}
	result.PercentX20 = &total
	result.UnavailableReason = ""
	result.Confidence = "provisional"
	if len(versions) == 1 {
		for v := range versions {
			result.ModelVersion = v
		}
	} else {
		result.ModelVersion = "mixed-frozen-versions"
	}
	return result
}

func (m *Manager) status(ctx context.Context) (Status, error) {
	m.mu.Lock()
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	result := Status{MaxRootTurns: m.options.MaxRootTurns, Buckets: []BucketStatus{}}
	result.Revision, err = m.revision(ctx, tx)
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM root_turns`).Scan(&result.RootTurnCount)
	}
	if err != nil {
		tx.Rollback()
		m.mu.Unlock()
		return result, err
	}
	err = tx.Commit()
	m.mu.Unlock()
	if err != nil {
		return result, err
	}
	for _, bucket := range []string{BucketShort, BucketWeekly} {
		fit, err := m.currentFit(ctx, bucket)
		if err != nil {
			return result, err
		}
		result.Buckets = append(result.Buckets, fit.bucketStatus())
	}
	return result, nil
}
