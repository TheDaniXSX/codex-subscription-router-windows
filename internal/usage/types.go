// Package usage stores privacy-limited inference telemetry and learns an
// empirical subscription-quota estimate from user-approved observations.
package usage

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

const (
	BucketShort         = "short"
	BucketWeekly        = "weekly"
	DefaultMaxRootTurns = 5000
	EstimatorVersion    = "usage-calibration-v1"
)

var ErrRevisionConflict = errors.New("usage revision conflict")
var ErrTurnNotFound = errors.New("usage turn not found")
var ErrObservationImmutable = errors.New("final quota observation is immutable")
var ErrInvalidObservationExtension = errors.New("invalid pending quota observation extension")

// TokenUsage preserves missing values as nil. A zero is meaningful only when
// the upstream explicitly reported it.
type TokenUsage struct {
	InputTokens       *int64 `json:"inputTokens,omitempty"`
	OutputTokens      *int64 `json:"outputTokens,omitempty"`
	TotalTokens       *int64 `json:"totalTokens,omitempty"`
	CachedInputTokens *int64 `json:"cachedInputTokens,omitempty"`
	CacheWriteTokens  *int64 `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens   *int64 `json:"reasoningTokens,omitempty"`
	Source            string `json:"source,omitempty"`
	Quality           string `json:"quality,omitempty"`
}

// Record is metadata for one physical inference request. It deliberately has
// no prompt or response-body field.
type Record struct {
	RequestID       string      `json:"requestId"`
	RootTurnID      string      `json:"rootTurnId,omitempty"`
	ParentRequestID string      `json:"parentRequestId,omitempty"`
	AgentID         string      `json:"agentId,omitempty"`
	Subagent        bool        `json:"subagent"`
	AccountID       string      `json:"accountId,omitempty"`
	AccountPlan     string      `json:"accountPlan,omitempty"`
	ModelRequested  string      `json:"modelRequested,omitempty"`
	ModelServed     string      `json:"modelServed,omitempty"`
	TierRequested   string      `json:"tierRequested,omitempty"`
	TierServed      string      `json:"tierServed,omitempty"`
	ReasoningEffort string      `json:"reasoningEffort,omitempty"`
	ThreadID        string      `json:"threadId,omitempty"`
	ParentThreadID  string      `json:"parentThreadId,omitempty"`
	TurnID          string      `json:"turnId,omitempty"`
	Outcome         string      `json:"outcome,omitempty"`
	StartedAt       time.Time   `json:"startedAt,omitempty"`
	FinishedAt      time.Time   `json:"finishedAt,omitempty"`
	Usage           *TokenUsage `json:"usage,omitempty"`
	Fast            *bool       `json:"fast,omitempty"`
}

// RateCard contains published API pricing for one exact model and API tier.
// Prices are USD per million tokens. Fast mode is a separate subscription
// quota prior and is never folded into API cost.
type RateCard struct {
	Version                     string   `json:"version"`
	Model                       string   `json:"model"`
	APITier                     string   `json:"apiTier"`
	InputUSDPerMillion          float64  `json:"inputUsdPerMillion"`
	CachedInputUSDPerMillion    float64  `json:"cachedInputUsdPerMillion"`
	OutputUSDPerMillion         float64  `json:"outputUsdPerMillion"`
	CacheWriteUSDPerMillion     *float64 `json:"cacheWriteUsdPerMillion,omitempty"`
	ShortContextMaxTokens       *int64   `json:"shortContextMaxTokens,omitempty"`
	LongContextThreshold        *int64   `json:"longContextThreshold,omitempty"`
	LongInputUSDPerMillion      *float64 `json:"longInputUsdPerMillion,omitempty"`
	LongCachedUSDPerMillion     *float64 `json:"longCachedUsdPerMillion,omitempty"`
	LongCacheWriteUSDPerMillion *float64 `json:"longCacheWriteUsdPerMillion,omitempty"`
	LongOutputUSDPerMillion     *float64 `json:"longOutputUsdPerMillion,omitempty"`
}

type Options struct {
	RateCards    []RateCard
	MaxRootTurns int
	FastPrior    float64
	Now          func() time.Time
}

type QuotaSnapshot struct {
	UsedPercent        *float64  `json:"usedPercent,omitempty"`
	ObservedAt         time.Time `json:"observedAt"`
	WindowDurationMins *int64    `json:"windowDurationMins,omitempty"`
	ResetsAt           *int64    `json:"resetsAt,omitempty"`
	Source             string    `json:"source,omitempty"`
	Quality            string    `json:"quality,omitempty"`
}

// QuotaObservation describes one quota delta for the exact requests bracketed
// by Before and After. Each bucket is an independent observation.
type QuotaObservation struct {
	ID                 string          `json:"id"`
	AccountID          string          `json:"accountId"`
	Plan               string          `json:"plan"`
	Bucket             string          `json:"bucket"`
	CapacityMultiplier float64         `json:"capacityMultiplier"`
	Before             QuotaSnapshot   `json:"before"`
	After              QuotaSnapshot   `json:"after"`
	Samples            []QuotaSnapshot `json:"samples,omitempty"`
	RequestIDs         []string        `json:"requestIds"`
}

type APICost struct {
	USD               *float64  `json:"usd,omitempty"`
	InputUSD          *float64  `json:"inputUsd,omitempty"`
	CachedInputUSD    *float64  `json:"cachedInputUsd,omitempty"`
	CacheWriteUSD     *float64  `json:"cacheWriteUsd,omitempty"`
	OutputUSD         *float64  `json:"outputUsd,omitempty"`
	RateVersion       string    `json:"rateVersion,omitempty"`
	RateCard          *RateCard `json:"rateCard,omitempty"`
	StandardRateCard  *RateCard `json:"standardRateCard,omitempty"`
	UnavailableReason string    `json:"unavailableReason,omitempty"`
}

type Estimate struct {
	PercentX20          *float64  `json:"percentX20,omitempty"`
	ModelVersion        string    `json:"modelVersion,omitempty"`
	Confidence          string    `json:"confidence,omitempty"`
	SampleCount         int       `json:"sampleCount"`
	Coefficient         *float64  `json:"coefficient,omitempty"`
	FeatureCoefficients []float64 `json:"featureCoefficients,omitempty"`
	FastMultiplier      *float64  `json:"fastMultiplier,omitempty"`
	PlanMultiplier      *float64  `json:"planMultiplier,omitempty"`
	UnavailableReason   string    `json:"unavailableReason,omitempty"`
}

type RecordView struct {
	Record
	APICost              APICost  `json:"apiCost"`
	EstimatedShort       Estimate `json:"estimatedShort"`
	EstimatedWeekly      Estimate `json:"estimatedWeekly"`
	GroupObservationID   string   `json:"groupObservationId,omitempty"`
	SharedObservedShort  *float64 `json:"sharedObservedShort,omitempty"`
	SharedObservedWeekly *float64 `json:"sharedObservedWeekly,omitempty"`
}

type ObservationView struct {
	ID                 string          `json:"id"`
	AccountID          string          `json:"accountId"`
	Plan               string          `json:"plan"`
	Bucket             string          `json:"bucket"`
	CapacityMultiplier float64         `json:"capacityMultiplier"`
	Before             QuotaSnapshot   `json:"before"`
	After              QuotaSnapshot   `json:"after"`
	Samples            []QuotaSnapshot `json:"samples,omitempty"`
	RawDeltaPP         *float64        `json:"rawDeltaPp,omitempty"`
	ObservedPercentX20 *float64        `json:"observedPercentX20,omitempty"`
	RequestIDs         []string        `json:"requestIds"`
	RootTurnIDs        []string        `json:"rootTurnIds"`
	CalibrationEnabled bool            `json:"calibrationEnabled"`
	Eligible           bool            `json:"eligible"`
	EligibilityReason  string          `json:"eligibilityReason,omitempty"`
	Revision           uint64          `json:"revision"`
}

type AgentBreakdown struct {
	AgentID      string      `json:"agentId,omitempty"`
	RootTurnID   string      `json:"rootTurnId,omitempty"`
	RequestCount int         `json:"requestCount"`
	Usage        *TokenUsage `json:"usage,omitempty"`
	APICostUSD   *float64    `json:"apiCostUsd,omitempty"`
}

type TurnView struct {
	RootTurnID           string            `json:"rootTurnId"`
	ThreadID             string            `json:"threadId,omitempty"`
	TurnID               string            `json:"turnId,omitempty"`
	Revision             uint64            `json:"revision"`
	CalibrationEnabled   bool              `json:"calibrationEnabled"`
	Completed            bool              `json:"completed"`
	Requests             []RecordView      `json:"requests"`
	TotalUsage           *TokenUsage       `json:"totalUsage,omitempty"`
	TotalUsageComplete   bool              `json:"totalUsageComplete"`
	TotalAPICostUSD      *float64          `json:"totalApiCostUsd,omitempty"`
	TotalKnownAPICostUSD *float64          `json:"totalKnownApiCostUsd,omitempty"`
	EstimatedShort       Estimate          `json:"estimatedShort"`
	EstimatedWeekly      Estimate          `json:"estimatedWeekly"`
	Agents               []AgentBreakdown  `json:"agents"`
	Observations         []ObservationView `json:"observations"`
}

type BucketStatus struct {
	Bucket          string   `json:"bucket"`
	SampleCount     int      `json:"sampleCount"`
	ModelVersion    string   `json:"modelVersion"`
	ScalarUSDPerX20 *float64 `json:"scalarUsdPerX20,omitempty"`
	FeatureFit      bool     `json:"featureFit"`
	Confidence      string   `json:"confidence"`
	ResidualMAD     *float64 `json:"residualMad,omitempty"`
}

type Status struct {
	Revision      uint64         `json:"revision"`
	RootTurnCount int            `json:"rootTurnCount"`
	MaxRootTurns  int            `json:"maxRootTurns"`
	Buckets       []BucketStatus `json:"buckets"`
}

type Manager struct {
	db      *sql.DB
	options Options
	mu      sync.Mutex
}

// SQLite-backed implementation methods.
func Open(path string, opts Options) (*Manager, error) { return open(path, opts) }

func (m *Manager) Begin(ctx context.Context, record Record) error { return m.begin(ctx, record) }
func (m *Manager) Finish(ctx context.Context, requestID string, final Record) error {
	return m.finish(ctx, requestID, final)
}
func (m *Manager) CompleteTurn(ctx context.Context, rootTurnID string) error {
	return m.completeTurn(ctx, rootTurnID)
}
func (m *Manager) SetRelation(ctx context.Context, requestID, rootTurnID, parentRequestID, agentID string) error {
	return m.setRelation(ctx, requestID, rootTurnID, parentRequestID, agentID)
}
func (m *Manager) SetParentThread(ctx context.Context, requestID, parentThreadID string) error {
	return m.setParentThread(ctx, requestID, parentThreadID)
}
func (m *Manager) ObserveQuota(ctx context.Context, observation QuotaObservation) error {
	return m.observeQuota(ctx, observation)
}
func (m *Manager) PendingQuotaObservations(ctx context.Context) ([]QuotaObservation, error) {
	return m.pendingQuotaObservations(ctx)
}
func (m *Manager) ViewTurn(ctx context.Context, rootTurnID string) (TurnView, error) {
	return m.viewTurn(ctx, rootTurnID)
}
func (m *Manager) SetCalibration(ctx context.Context, rootTurnID string, enabled bool) error {
	return m.setCalibration(ctx, rootTurnID, enabled)
}
func (m *Manager) SetCalibrationRevision(ctx context.Context, rootTurnID string, enabled bool, expectedRevision uint64) error {
	return m.setCalibrationRevision(ctx, rootTurnID, enabled, expectedRevision)
}
func (m *Manager) SetCalibrationBulk(ctx context.Context, rootTurnIDs []string, enabled bool, expectedRevision uint64) error {
	return m.setCalibrationBulk(ctx, rootTurnIDs, enabled, expectedRevision)
}
func (m *Manager) Status(ctx context.Context) (Status, error) { return m.status(ctx) }
func (m *Manager) Close() error                               { return m.close() }
