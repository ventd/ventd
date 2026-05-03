package calibrate

import (
	"sort"
	"sync"
	"time"
)

// fakeClock is a deterministic Clock for sentinel tests. Time
// only advances when Advance(d) is called; pending timers whose
// deadline has passed fire synchronously inside Advance, before
// Advance returns. This lets test bodies assert callback effects
// immediately after advancing.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock    *fakeClock
	deadline time.Time
	fn       func()
	stopped  bool
	fired    bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0).UTC()}
}

// AfterFunc enqueues fn to run when the simulated clock has
// advanced by d. Returns the Timer for cancellation.
func (c *fakeClock) AfterFunc(d time.Duration, fn func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{
		clock:    c,
		deadline: c.now.Add(d),
		fn:       fn,
	}
	c.timers = append(c.timers, t)
	return t
}

// Stop on a fakeTimer mirrors *time.Timer.Stop semantics: returns
// true if the timer was stopped before firing, false otherwise.
func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.fired || t.stopped {
		return false
	}
	t.stopped = true
	return true
}

// Advance moves the clock forward by d and fires any pending
// timers whose deadline ≤ new now. Callbacks run synchronously
// in deadline order, NOT under c.mu — so a callback that enqueues
// or stops other timers via AfterFunc / Stop won't deadlock.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	target := c.now

	// Snapshot timers whose deadline has passed, in deadline order.
	due := []*fakeTimer{}
	for _, t := range c.timers {
		if !t.stopped && !t.fired && !t.deadline.After(target) {
			due = append(due, t)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		return due[i].deadline.Before(due[j].deadline)
	})
	// Mark fired before releasing lock so no double-fire.
	for _, t := range due {
		t.fired = true
	}
	c.mu.Unlock()

	// Fire outside the lock so callbacks can lock without deadlocking.
	for _, t := range due {
		t.fn()
	}
}
