package mux

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/spend"
	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/usage"
)

// Quota is measured across a quiet account interval, not assigned independently
// to each overlapping request. The ledger preserves every member of the group.
const usageReadTimeout = time.Second

type usageTracking struct {
	mu       sync.Mutex
	accounts map[string]*usageAccountInterval
	err      string
	read     func(context.Context, string) (AccountSnapshot, error)
	delays   []time.Duration
	wg       sync.WaitGroup
}

type usageAccountInterval struct {
	mu     sync.Mutex
	before AccountSnapshot
	// Keep the exact persisted baseline for every bucket. Reconstructing an
	// AccountSnapshot loses source/quality metadata and changes audit evidence.
	beforeSnapshots map[string]usage.QuotaSnapshot
	observationIDs  map[string]string
	capacity        float64
	requests        []string
	active          map[string]bool
	generation      uint64
	samples         map[string][]usage.QuotaSnapshot
}

func (m *Multiplexer) initUsage() {
	m.usageTelemetry = &usageTracking{accounts: make(map[string]*usageAccountInterval), read: m.accountSnapshotForUsage, delays: []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second}}
	if !m.requestSpending {
		return
	}
	root := strings.TrimSpace(m.usageRoot)
	if root == "" {
		root = m.store.Root()
	}
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		m.usageError(err)
		return
	}
	m.usageRoot = resolvedRoot
	// Explicit telemetry paths never fall back on failure: doing so would
	// silently write DEV calibration into the shared production store.
	relations, relationErr := newUsageRelationsState(filepath.Join(m.usageRoot, "usage-relations.json"))
	m.usageRelations = relations
	m.usageError(relationErr)
	ledger, err := usage.Open(filepath.Join(m.usageRoot, "usage-ledger.sqlite"), usage.Options{})
	if err != nil {
		m.usageError(err)
		return
	}
	m.usageLedger = ledger
	pending, pendingErr := ledger.PendingQuotaObservations(context.Background())
	m.usageError(pendingErr)
	for _, observation := range pending {
		interval := m.usageInterval(observation.AccountID)
		interval.before.ID = observation.AccountID
		interval.before.PlanType = observation.Plan
		interval.capacity = observation.CapacityMultiplier
		if interval.beforeSnapshots == nil {
			interval.beforeSnapshots = map[string]usage.QuotaSnapshot{}
			interval.observationIDs = map[string]string{}
		}
		interval.beforeSnapshots[observation.Bucket] = cloneUsageQuotaSnapshot(observation.Before)
		interval.observationIDs[observation.Bucket] = observation.ID
		for _, id := range observation.RequestIDs {
			interval.requests = appendUsageRequest(interval.requests, id)
		}
		if interval.samples == nil {
			interval.samples = map[string][]usage.QuotaSnapshot{}
		}
		interval.samples[observation.Bucket] = append([]usage.QuotaSnapshot(nil), observation.Samples...)
	}
}

func (m *Multiplexer) usageError(err error) {
	if err == nil || m.usageTelemetry == nil {
		return
	}
	m.usageTelemetry.mu.Lock()
	m.usageTelemetry.err = err.Error()
	m.usageTelemetry.mu.Unlock()
	m.publish(Event{Type: "usage-ledger-error", Message: "Usage history could not be saved; inference continues"})
}

func (m *Multiplexer) accountSnapshotForUsage(ctx context.Context, id string) (AccountSnapshot, error) {
	child, ok := m.child(id)
	if !ok {
		return AccountSnapshot{ID: id, observedAt: m.now().UTC()}, errors.New("usage account app-server unavailable")
	}
	// Telemetry uses a dedicated read path: a short metadata timeout must not
	// mark an otherwise healthy subscription unavailable to request routing.
	s, err := readUsageAccountSnapshot(ctx, id, child.Request)
	s.observedAt = m.now().UTC()
	// A routing preview is never a real measurement and must not train a model.
	m.previewMu.RLock()
	preview := m.rateLimitPreview != nil
	m.previewMu.RUnlock()
	if preview {
		s.RateLimits = nil
		s.RateLimitsByLimitID = nil
	}
	return s, err
}

func readUsageAccountSnapshot(ctx context.Context, id string, request func(context.Context, string, json.RawMessage) (protocol.Message, error)) (AccountSnapshot, error) {
	s := AccountSnapshot{ID: id}
	response, err := request(ctx, "account/read", json.RawMessage(`{"refreshToken":false}`))
	if err != nil {
		return s, err
	}
	var account struct {
		Account *struct {
			Type     string `json:"type"`
			PlanType string `json:"planType"`
		} `json:"account"`
	}
	if err := json.Unmarshal(response.Result, &account); err != nil {
		return s, errors.New("invalid usage account metadata")
	}
	if account.Account == nil {
		return s, nil
	}
	s.PlanType, s.AuthType = account.Account.PlanType, account.Account.Type
	if s.AuthType != "chatgpt" {
		return s, nil
	}
	response, err = request(ctx, "account/rateLimits/read", nil)
	if err != nil {
		return s, err
	}
	var limits struct {
		RateLimits *usageWireRateLimits            `json:"rateLimits"`
		ByID       map[string]*usageWireRateLimits `json:"rateLimitsByLimitId"`
	}
	if err := json.Unmarshal(response.Result, &limits); err != nil {
		return s, errors.New("invalid usage quota metadata")
	}
	s.RateLimits = limits.RateLimits.snapshot()
	if len(limits.ByID) > 0 {
		s.RateLimitsByLimitID = make(map[string]*RateLimits, len(limits.ByID))
		for key, value := range limits.ByID {
			s.RateLimitsByLimitID[key] = value.snapshot()
		}
	}
	return s, nil
}

type usageWireWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *int64   `json:"windowDurationMins"`
	ResetsAt           *int64   `json:"resetsAt"`
}

func (w *usageWireWindow) snapshot() *RateLimitWindow {
	if w == nil || w.UsedPercent == nil || !validUsageQuotaPercent(*w.UsedPercent) {
		return nil
	}
	return &RateLimitWindow{UsedPercent: *w.UsedPercent, WindowDurationMins: w.WindowDurationMins, ResetsAt: w.ResetsAt}
}

type usageWireRateLimits struct {
	LimitID   string           `json:"limitId"`
	Primary   *usageWireWindow `json:"primary"`
	Secondary *usageWireWindow `json:"secondary"`
}

func (r *usageWireRateLimits) snapshot() *RateLimits {
	if r == nil {
		return nil
	}
	return &RateLimits{LimitID: r.LimitID, Primary: r.Primary.snapshot(), Secondary: r.Secondary.snapshot()}
}

func (m *Multiplexer) usageInterval(id string) *usageAccountInterval {
	t := m.usageTelemetry
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.accounts[id] == nil {
		t.accounts[id] = &usageAccountInterval{active: make(map[string]bool)}
	}
	return t.accounts[id]
}

func (m *Multiplexer) beginUsage(ctx context.Context, record *spend.Record) {
	if m.usageLedger == nil || record.Telemetry == nil || m.closing.Load() {
		return
	}
	u := record.Telemetry
	m.associateUsageRecord(u, u.ParentThreadID)
	interval := m.usageInterval(u.AccountID)
	interval.mu.Lock()
	defer interval.mu.Unlock()
	if len(interval.requests) == 0 {
		readCtx, cancel := context.WithTimeout(ctx, usageReadTimeout)
		interval.before, _ = m.usageTelemetry.read(readCtx, u.AccountID)
		cancel()
		interval.beforeSnapshots = map[string]usage.QuotaSnapshot{}
		interval.observationIDs = map[string]string{}
		interval.capacity = usagePlanCapacity(interval.before.PlanType)
		for _, bucket := range []string{usage.BucketShort, usage.BucketWeekly} {
			interval.beforeSnapshots[bucket] = cloneUsageQuotaSnapshot(usageQuotaSnapshot(interval.before, bucket))
			interval.observationIDs[bucket] = u.RequestID + ":" + bucket
		}
	}
	u.AccountPlan = interval.before.PlanType
	u.StartedAt = m.now().UTC()
	interval.generation++
	interval.requests = appendUsageRequest(interval.requests, u.RequestID)
	interval.active[u.RequestID] = true
	m.usageError(m.usageLedger.Begin(context.WithoutCancel(ctx), *u))
}

func (m *Multiplexer) finishUsage(record spend.Record) {
	if m.usageLedger == nil || record.Telemetry == nil {
		return
	}
	u := record.Telemetry
	m.associateUsageRecord(u, u.ParentThreadID)
	m.usageError(m.usageLedger.Finish(context.Background(), u.RequestID, *u))
	interval := m.usageInterval(u.AccountID)
	interval.mu.Lock()
	delete(interval.active, u.RequestID)
	generation := interval.generation
	idle := len(interval.active) == 0 && len(interval.requests) > 0
	interval.mu.Unlock()
	m.publish(Event{Type: "usage-updated", Data: map[string]string{"rootId": u.RootTurnID}})
	if idle {
		// Serialize Add with Close's transition so Wait never races a new worker.
		m.lifecycleMu.Lock()
		if !m.closing.Load() {
			m.usageTelemetry.wg.Add(1)
			go func() { defer m.usageTelemetry.wg.Done(); m.settleUsage(u.AccountID, interval, generation) }()
		}
		m.lifecycleMu.Unlock()
	}
}

func (m *Multiplexer) settleUsage(accountID string, interval *usageAccountInterval, generation uint64) {
	m.lifecycleMu.Lock()
	ctx := m.runCtx
	m.lifecycleMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	var after AccountSnapshot
	samples := map[string][]usage.QuotaSnapshot{}
	started := time.Now()
	for _, delay := range m.usageTelemetry.delays {
		timer := time.NewTimer(max(0, delay-time.Since(started)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		interval.mu.Lock()
		current := !m.closing.Load() && generation == interval.generation && len(interval.active) == 0 && len(interval.requests) > 0
		interval.mu.Unlock()
		if !current {
			return
		}
		// Never hold the account lock during a post-request network read. New
		// work joins the group freely and invalidates this worker's generation.
		readCtx, cancel := context.WithTimeout(ctx, usageReadTimeout)
		after, _ = m.usageTelemetry.read(readCtx, accountID)
		cancel()
		for _, bucket := range []string{usage.BucketShort, usage.BucketWeekly} {
			samples[bucket] = append(samples[bucket], usageQuotaSnapshot(after, bucket))
		}
	}
	interval.mu.Lock()
	defer interval.mu.Unlock()
	if m.closing.Load() || generation != interval.generation || len(interval.active) != 0 || len(interval.requests) == 0 {
		return
	}
	plan, capacity := interval.before.PlanType, interval.capacity
	requests := append([]string(nil), interval.requests...)
	keepPending := usageAwaitingQuotaSignal(plan, capacity, interval.beforeSnapshots, after)
	if interval.samples == nil {
		interval.samples = map[string][]usage.QuotaSnapshot{}
	}
	for bucket, readings := range samples {
		interval.samples[bucket] = append(interval.samples[bucket], readings...)
	}
	for bucket, before := range interval.beforeSnapshots {
		observation := usage.QuotaObservation{
			ID: interval.observationIDs[bucket], AccountID: accountID,
			Plan: plan, Bucket: bucket, CapacityMultiplier: capacity,
			Before: before, After: usageQuotaSnapshot(after, bucket), RequestIDs: requests,
			Samples: interval.samples[bucket],
		}
		if keepPending {
			observation.After.Quality = usage.QuotaAwaitingSignal
		} else if plan != after.PlanType {
			// Normalization evidence stays at the original plan/capacity. The
			// terminal annotation prevents a cross-plan interval from training.
			observation.After.Quality = "plan-changed"
		}
		if err := m.usageLedger.ObserveQuota(context.Background(), observation); err != nil {
			m.usageError(err)
			continue
		}
		if !keepPending {
			delete(interval.beforeSnapshots, bucket)
			delete(interval.observationIDs, bucket)
			delete(interval.samples, bucket)
		}
	}
	if len(interval.beforeSnapshots) == 0 {
		interval.requests = nil
		interval.before = AccountSnapshot{}
		interval.capacity = 0
		interval.samples = nil
	}
	m.publish(Event{Type: "usage-updated", AccountID: accountID})
}

// Do not discard the tokens behind a rounded zero and then train only on the
// next positive delta. Carry the full interval forward until there is signal.
func usageAwaitingQuotaSignal(plan string, capacity float64, before map[string]usage.QuotaSnapshot, after AccountSnapshot) bool {
	if plan != after.PlanType || (capacity != 1 && capacity != 5 && capacity != 20) {
		return false
	}
	zero, valid := false, false
	for bucket, a := range before {
		b := usageQuotaSnapshot(after, bucket)
		if a.UsedPercent == nil && b.UsedPercent == nil {
			continue
		}
		if a.UsedPercent == nil || b.UsedPercent == nil || a.ResetsAt == nil || b.ResetsAt == nil || a.WindowDurationMins == nil || b.WindowDurationMins == nil {
			return false
		}
		if !validUsageQuotaPercent(*a.UsedPercent) || !validUsageQuotaPercent(*b.UsedPercent) || !b.ObservedAt.After(a.ObservedAt) {
			return false
		}
		if *a.ResetsAt != *b.ResetsAt || *a.WindowDurationMins != *b.WindowDurationMins || *b.ResetsAt <= b.ObservedAt.Unix() || *b.UsedPercent < *a.UsedPercent {
			return false
		}
		valid = true
		zero = zero || *a.UsedPercent == *b.UsedPercent
	}
	return valid && zero
}

func validUsageQuotaPercent(percent float64) bool {
	return !math.IsNaN(percent) && !math.IsInf(percent, 0) && percent >= 0 && percent <= 100
}

func appendUsageRequest(ids []string, id string) []string {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func cloneUsageQuotaSnapshot(snapshot usage.QuotaSnapshot) usage.QuotaSnapshot {
	if snapshot.UsedPercent != nil {
		value := *snapshot.UsedPercent
		snapshot.UsedPercent = &value
	}
	if snapshot.WindowDurationMins != nil {
		value := *snapshot.WindowDurationMins
		snapshot.WindowDurationMins = &value
	}
	if snapshot.ResetsAt != nil {
		value := *snapshot.ResetsAt
		snapshot.ResetsAt = &value
	}
	return snapshot
}

func usagePlanCapacity(plan string) float64 {
	switch plan {
	case "plus":
		return 1
	case "prolite":
		return 5
	case "pro":
		return 20
	}
	return 0
}

func usageQuotaSnapshot(account AccountSnapshot, bucket string) usage.QuotaSnapshot {
	result := usage.QuotaSnapshot{ObservedAt: account.observedAt, Source: "account/rateLimits/read", Quality: "server-resolution-unknown"}
	limits := account.RateLimits
	if len(account.RateLimitsByLimitID) > 0 {
		// A distinct model quota must not be confused with the common Codex
		// subscription pool or added to its percentage. Preserve unknowns.
		limits = account.RateLimitsByLimitID["codex"]
		result.Source = "account/rateLimits/read:codex"
	}
	if limits != nil && limits.LimitID != "" && limits.LimitID != "codex" {
		result.Quality = "unsupported-quota-pool"
		return result
	}
	if limits == nil {
		return result
	}
	for _, window := range []*RateLimitWindow{limits.Primary, limits.Secondary} {
		if window == nil || window.WindowDurationMins == nil {
			continue
		}
		// Do not duplicate one available window into both buckets.
		isWeekly := *window.WindowDurationMins >= 24*60
		if (bucket == usage.BucketWeekly) != isWeekly {
			continue
		}
		percent := window.UsedPercent
		result.UsedPercent = &percent
		result.WindowDurationMins, result.ResetsAt = window.WindowDurationMins, window.ResetsAt
		break
	}
	return result
}

type UsageTurnView struct {
	usage.TurnView
	PendingRelations    int    `json:"pendingRelations"`
	ActiveDescendants   int    `json:"activeDescendants"`
	AssociationComplete bool   `json:"associationComplete"`
	Unit                string `json:"unit"`
}

func (m *Multiplexer) enrichUsageView(view usage.TurnView) UsageTurnView {
	pending, active := m.usageRelationCoverage(view.RootTurnID)
	return UsageTurnView{TurnView: view, PendingRelations: pending, ActiveDescendants: active, AssociationComplete: pending == 0 && active == 0, Unit: "Pro20"}
}

func (m *Multiplexer) UsageTurn(ctx context.Context, threadID, turnID string) (UsageTurnView, error) {
	if m.usageLedger == nil {
		return UsageTurnView{}, errors.New("usage ledger unavailable")
	}
	view, err := m.usageLedger.ViewTurn(ctx, threadID+":"+turnID)
	if errors.Is(err, usage.ErrTurnNotFound) {
		err = nil
	}
	return m.enrichUsageView(view), err
}

func (m *Multiplexer) UsageStatus(ctx context.Context) (any, error) {
	if m.usageLedger == nil {
		return nil, errors.New("usage ledger unavailable")
	}
	status, err := m.usageLedger.Status(ctx)
	if err != nil {
		return nil, err
	}
	m.usageTelemetry.mu.Lock()
	lastError := m.usageTelemetry.err
	m.usageTelemetry.mu.Unlock()
	return struct {
		usage.Status
		Error string `json:"error,omitempty"`
		Unit  string `json:"unit"`
	}{status, lastError, "Pro20"}, nil
}

func (m *Multiplexer) SetUsageCalibration(ctx context.Context, rootID string, included bool, revision uint64, includeRelated bool) (UsageTurnView, error) {
	if m.usageLedger == nil {
		return UsageTurnView{}, errors.New("usage ledger unavailable")
	}
	m.usageMutationMu.Lock()
	defer m.usageMutationMu.Unlock()
	current, err := m.usageLedger.ViewTurn(ctx, rootID)
	if err != nil {
		return UsageTurnView{}, err
	}
	if revision != current.Revision {
		return UsageTurnView{}, usage.ErrRevisionConflict
	}
	if len(current.Requests) == 0 {
		return UsageTurnView{}, errors.New("turn has no recorded usage")
	}
	roots := []string{rootID}
	if includeRelated {
		seen := map[string]bool{rootID: true}
		// Transitive closure keeps every shared measurement whole, including
		// chains of overlaps on more than one account.
		for i := 0; i < len(roots); i++ {
			if len(roots) > 5000 {
				return UsageTurnView{}, errors.New("observation group too large")
			}
			view, readErr := m.usageLedger.ViewTurn(ctx, roots[i])
			if readErr != nil {
				return UsageTurnView{}, readErr
			}
			for _, observation := range view.Observations {
				for _, id := range observation.RootTurnIDs {
					if id != "" && !seen[id] {
						seen[id] = true
						roots = append(roots, id)
					}
				}
			}
		}
	}
	if err = m.usageLedger.SetCalibrationBulk(ctx, roots, included, revision); err != nil {
		return UsageTurnView{}, err
	}
	m.publish(Event{Type: "usage-updated", Data: map[string]string{"rootId": rootID}})
	view, err := m.usageLedger.ViewTurn(ctx, rootID)
	return m.enrichUsageView(view), err
}
