// Package faketime provides a deterministic, goroutine-safe fake clock for
// tests. It supports monotonic time, timers, tickers, and an Advance method
// that fires pending deadlines synchronously.
//
// Production code that needs to be testable under faketime should accept a
// Clock (or its methods individually); test code creates a Clock via New and
// advances time explicitly. The WaitUntil helper polls a condition using real
// wall-clock time for tests that interact with code still on the real clock.
package faketime

import (
	"sync"
	"testing"
	"time"
)

// Clock is a fake monotonic clock. All methods are goroutine-safe.
type Clock struct {
	mu      sync.Mutex
	now     time.Time
	timers  []*Timer
	tickers []*Ticker
	t       *testing.T
}

// New creates a Clock initialised to initial and registers a t.Cleanup that
// warns about orphan timers/tickers (created but never stopped).
func New(t *testing.T, initial time.Time) *Clock {
	t.Helper()
	c := &Clock{
		now: initial,
		t:   t,
	}
	t.Cleanup(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, tm := range c.timers {
			if !tm.stopped && !tm.fired {
				t.Logf("faketime: orphan timer (deadline %v) was never stopped or fired", tm.deadline)
			}
		}
		for _, tk := range c.tickers {
			if !tk.stopped {
				t.Logf("faketime: orphan ticker (interval %v) was never stopped", tk.interval)
			}
		}
	})
	return c
}

// Now returns the current fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d and fires any pending timers/tickers
// whose deadline falls at or before the new time. Timers are fired in deadline
// order. Tickers receive one tick per interval crossed (extras are dropped if
// the channel is full, matching real time.Ticker semantics).
//
// Panics if d is negative.
func (c *Clock) Advance(d time.Duration) {
	if d < 0 {
		panic("faketime: Advance with negative duration")
	}
	c.mu.Lock()
	target := c.now.Add(d)
	c.now = target

	// Fire timers in deadline order.
	for {
		idx := -1
		var earliest time.Time
		for i, tm := range c.timers {
			if tm.stopped || tm.fired {
				continue
			}
			if idx == -1 || tm.deadline.Before(earliest) {
				idx = i
				earliest = tm.deadline
			}
		}
		if idx == -1 || earliest.After(target) {
			break
		}
		tm := c.timers[idx]
		tm.fired = true
		// Send non-blocking (channel has buffer of 1).
		select {
		case tm.c <- earliest:
		default:
		}
	}

	// Fire tickers: deliver one value per interval crossed.
	for _, tk := range c.tickers {
		if tk.stopped {
			continue
		}
		for !tk.nextTick.After(target) {
			select {
			case tk.c <- tk.nextTick:
			default:
				// Drop, matching real Ticker semantics.
			}
			tk.nextTick = tk.nextTick.Add(tk.interval)
		}
	}

	c.mu.Unlock()
}

// After returns a channel that receives the current fake time when d has
// elapsed (via Advance). Equivalent to NewTimer(d).C.
func (c *Clock) After(d time.Duration) <-chan time.Time {
	return c.NewTimer(d).C
}

// Timer is a fake timer whose deadline is driven by Clock.Advance.
type Timer struct {
	C        <-chan time.Time
	c        chan time.Time
	deadline time.Time
	fired    bool
	stopped  bool
	clock    *Clock
}

// NewTimer creates a Timer that fires when the clock reaches now+d.
func (c *Clock) NewTimer(d time.Duration) *Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	tm := &Timer{
		C:        ch,
		c:        ch,
		deadline: c.now.Add(d),
		clock:    c,
	}
	c.timers = append(c.timers, tm)
	return tm
}

// Stop prevents the Timer from firing. Returns true if the timer was stopped
// before it fired, false if it had already fired or been stopped.
func (tm *Timer) Stop() bool {
	tm.clock.mu.Lock()
	defer tm.clock.mu.Unlock()
	if tm.stopped || tm.fired {
		return false
	}
	tm.stopped = true
	return true
}

// Ticker is a fake ticker whose ticks are driven by Clock.Advance.
type Ticker struct {
	C        <-chan time.Time
	c        chan time.Time
	interval time.Duration
	nextTick time.Time
	stopped  bool
	clock    *Clock
}

// NewTicker creates a Ticker that ticks every d when the clock advances.
// Panics if d <= 0.
func (c *Clock) NewTicker(d time.Duration) *Ticker {
	if d <= 0 {
		panic("faketime: non-positive interval for NewTicker")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	tk := &Ticker{
		C:        ch,
		c:        ch,
		interval: d,
		nextTick: c.now.Add(d),
		clock:    c,
	}
	c.tickers = append(c.tickers, tk)
	return tk
}

// Stop turns off the ticker. After Stop, no more ticks will be sent. Stop
// does not close the channel.
func (tk *Ticker) Stop() {
	tk.clock.mu.Lock()
	defer tk.clock.mu.Unlock()
	tk.stopped = true
}

// WaitUntil polls condition at ~1ms real-time intervals until it returns true
// or timeout elapses. Calls t.Fatal on timeout. This is a real-time helper
// for tests that interact with code still on the real clock.
func WaitUntil(t *testing.T, condition func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("faketime.WaitUntil: condition not met within %v", timeout)
		}
		time.Sleep(1 * time.Millisecond)
	}
}
