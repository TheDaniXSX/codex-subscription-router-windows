package spend

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func fixture() (*Policy, []Candidate) {
	now := time.Unix(1000, 0)
	p := New(time.Minute)
	p.now = func() time.Time { return now }
	return p, []Candidate{{"primary", true, true, true, 58, 1, now}, {"second", true, true, true, 68, 1, now}}
}

func TestStrictNeverFallsBack(t *testing.T) {
	p, c := fixture()
	p.Set(Mode{AccountID: "second"})
	d, done, err := p.Reserve(c)
	if err != nil || d.AccountID != "second" || d.Reason != "strict" {
		t.Fatal(d, err)
	}
	done()
	for _, mutate := range []func(*Candidate){func(c *Candidate) { c.Remaining = 0 }, func(c *Candidate) { c.Connected = false }, func(c *Candidate) { c.Enabled = false }, func(c *Candidate) { c.Known = false }, func(c *Candidate) { c.ObservedAt = c.ObservedAt.Add(-2 * time.Minute) }} {
		_, next := fixture()
		mutate(&next[1])
		_, _, err = p.Reserve(next)
		if !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
}

func TestModeChangesAtNextRequestNotNextConversation(t *testing.T) {
	p, c := fixture()
	p.Set(Mode{AccountID: "primary"})
	first, release, err := p.Reserve(c)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	p.Set(Mode{AccountID: "second"})
	next, done, err := p.Reserve(c)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if first.AccountID != "primary" || next.AccountID != "second" || next.PolicyRevision <= first.PolicyRevision {
		t.Fatal(first, next)
	}
	// Old response finishes on primary. It cannot release the second's lease.
	release()
	release()
	if p.inflight["second"] != 1 {
		t.Fatal(p.inflight)
	}
}

func TestAutoReevaluatesAndAccountsForParallelWork(t *testing.T) {
	p, c := fixture()
	d, a, err := p.Reserve(c)
	if err != nil {
		t.Fatal(err)
	}
	defer a()
	n, b, err := p.Reserve(c)
	if err != nil {
		t.Fatal(err)
	}
	defer b()
	if d.AccountID == n.AccountID {
		t.Fatal("parallel work ignored", d, n)
	}
	a()
	b()
	c[0].Urgency = 10
	n, done, err := p.Reserve(c)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if n.AccountID != "primary" {
		t.Fatal(n)
	}
}

func TestConcurrentReservationsAndModeChanges(t *testing.T) {
	p, c := fixture()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				p.Set(Mode{})
				_, done, err := p.Reserve(c)
				if err != nil {
					t.Error(err)
					return
				}
				done()
				done()
			}
		}()
	}
	wg.Wait()
	if len(p.inflight) != 0 {
		t.Fatal(p.inflight)
	}
}
