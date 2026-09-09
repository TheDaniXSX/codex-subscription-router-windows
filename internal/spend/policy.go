// Package spend separates inference billing decisions from conversation ownership.
package spend

import (
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

var ErrUnavailable = errors.New("selected subscription is unavailable; no other subscription was charged")
var ErrNoCapacity = errors.New("no subscription has known available capacity")

type Mode struct {
	AccountID string // empty means Auto; nonempty is a strict spending instruction
}

// Candidate is a snapshot, not a promise of future quota. Unknown or stale quota
// is ineligible. Urgency is supplied by the existing expiry-aware scoring policy.
type Candidate struct {
	ID                        string
	Enabled, Connected, Known bool
	Remaining                 float64
	Urgency                   float64
	ObservedAt                time.Time
}

type Decision struct {
	Sequence       uint64 `json:"sequence"`
	PolicyRevision uint64 `json:"policyRevision"`
	AccountID      string `json:"accountId"`
	Reason         string `json:"reason"`
}

type Policy struct {
	mu                 sync.Mutex
	mode               Mode
	revision, sequence uint64
	inflight           map[string]int
	now                func() time.Time
	maxAge             time.Duration
}

func New(maxAge time.Duration) *Policy {
	return &Policy{inflight: make(map[string]int), now: time.Now, maxAge: maxAge}
}

// Set affects future reservations only. Already dispatched responses keep their
// billing identity, even when a mode changes while they are streaming.
func (p *Policy) Set(mode Mode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode != mode {
		p.mode = mode
		p.revision++
	}
}

// Reserve linearizes a new inference against mode changes and concurrent agents.
// release is idempotent and must be called only after the response finishes.
func (p *Policy) Reserve(candidates []Candidate) (Decision, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	eligible := make([]Candidate, 0, len(candidates))
	seen := make(map[string]bool)
	for _, c := range candidates {
		if c.ID == "" || seen[c.ID] {
			return Decision{}, nil, errors.New("invalid duplicate or empty subscription identity")
		}
		seen[c.ID] = true
		age := now.Sub(c.ObservedAt)
		if !c.Enabled || !c.Connected || !c.Known || age < 0 || age > p.maxAge || c.Remaining <= 0 || c.Remaining > 100 || math.IsNaN(c.Remaining) || math.IsInf(c.Remaining, 0) || math.IsNaN(c.Urgency) || math.IsInf(c.Urgency, 0) {
			continue
		}
		if p.mode.AccountID != "" && p.mode.AccountID != c.ID {
			continue
		}
		eligible = append(eligible, c)
	}
	if len(eligible) == 0 {
		if p.mode.AccountID != "" {
			return Decision{}, nil, ErrUnavailable
		}
		return Decision{}, nil, ErrNoCapacity
	}
	sort.Slice(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		// Each in-flight response adds pressure. This is not an invented token
		// reservation: the exact future token cost is unknown until completion.
		x, y := a.Urgency/float64(1+p.inflight[a.ID]), b.Urgency/float64(1+p.inflight[b.ID])
		if x != y {
			return x > y
		}
		if p.inflight[a.ID] != p.inflight[b.ID] {
			return p.inflight[a.ID] < p.inflight[b.ID]
		}
		if a.Remaining != b.Remaining {
			return a.Remaining > b.Remaining
		}
		return a.ID < b.ID
	})
	id := eligible[0].ID
	p.inflight[id]++
	p.sequence++
	reason := "auto"
	if p.mode.AccountID != "" {
		reason = "strict"
	}
	decision := Decision{p.sequence, p.revision, id, reason}
	var once sync.Once
	release := func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.inflight[id]--
			if p.inflight[id] == 0 {
				delete(p.inflight, id)
			}
		})
	}
	return decision, release, nil
}
