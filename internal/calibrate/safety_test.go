package calibrate

import (
	"sync/atomic"
	"testing"
	"time"
)

// withFakeClock builds a sentinel wired to a fakeClock so tests
// can advance simulated time deterministically. Eliminates the
// 12 real-time time.Sleep ≥2s calls the v0.5.4 version of this
// file used to need.
func withFakeClock(escalate func()) (*ZeroPWMSentinel, *fakeClock) {
	c := newFakeClock()
	return newZeroPWMSentinelWithClock(nil, escalate, c), c
}

func TestZeroPWMSentinel_NonZeroNeverEscalates(t *testing.T) {
	var calls atomic.Int32
	s, clk := withFakeClock(func() { calls.Add(1) })
	defer s.Stop()

	s.Set(60)
	clk.Advance(ZeroPWMMaxDuration + 250*time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Errorf("escalate fired for non-zero value: got %d calls, want 0", got)
	}
}

func TestZeroPWMSentinel_ZeroFiresAfterTwoSeconds(t *testing.T) {
	var calls atomic.Int32
	s, clk := withFakeClock(func() { calls.Add(1) })
	defer s.Stop()

	s.Set(0)
	// Just before the deadline: not yet fired.
	clk.Advance(ZeroPWMMaxDuration - 250*time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Errorf("escalate fired early: got %d calls, want 0 before deadline", got)
	}
	// After the deadline + slack: must have fired exactly once.
	clk.Advance(500 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Errorf("escalate count after deadline: got %d, want 1", got)
	}
}

func TestZeroPWMSentinel_NonZeroBeforeDeadlineCancels(t *testing.T) {
	var calls atomic.Int32
	s, clk := withFakeClock(func() { calls.Add(1) })
	defer s.Stop()

	s.Set(0)
	clk.Advance(ZeroPWMMaxDuration / 2)
	s.Set(60) // cancels pending escalation

	// Wait past the original deadline; must NOT have fired.
	clk.Advance(ZeroPWMMaxDuration + 250*time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Errorf("escalate fired after non-zero cancel: got %d calls, want 0", got)
	}
}

func TestZeroPWMSentinel_ReArmAfterCancel(t *testing.T) {
	var calls atomic.Int32
	s, clk := withFakeClock(func() { calls.Add(1) })
	defer s.Stop()

	s.Set(0)
	clk.Advance(50 * time.Millisecond)
	s.Set(60) // cancel
	s.Set(0)  // re-arm — fresh 2s clock

	// At 1s after the second Set(0): not yet fired (re-arm reset clock).
	clk.Advance(ZeroPWMMaxDuration - 250*time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Errorf("escalate fired early after re-arm: got %d", got)
	}
	// After full 2s + slack from the second Set(0): must have fired.
	clk.Advance(500 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Errorf("escalate count after re-arm deadline: got %d, want 1", got)
	}
}

func TestZeroPWMSentinel_StopPreventsEscalation(t *testing.T) {
	var calls atomic.Int32
	s, clk := withFakeClock(func() { calls.Add(1) })

	s.Set(0)
	// Stop before the deadline: timer cancelled.
	clk.Advance(ZeroPWMMaxDuration / 2)
	s.Stop()

	clk.Advance(ZeroPWMMaxDuration + 250*time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Errorf("escalate fired after Stop: got %d", got)
	}

	// Subsequent Set calls must be no-ops.
	s.Set(0)
	clk.Advance(ZeroPWMMaxDuration + 250*time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Errorf("escalate fired after Stop+Set(0): got %d", got)
	}
}

func TestZeroPWMSentinel_ConcurrentSetsSafeUnderRace(t *testing.T) {
	// Run -race; a fan flapping between 0 and non-zero should never
	// fire escalate spuriously. End state determines outcome.
	//
	// This test deliberately uses the real clock — the race we're
	// testing is between concurrent Set goroutines and the timer
	// arm/cancel path, which only manifests with concurrent actual
	// goroutine scheduling. fakeClock.Advance is single-threaded
	// from the test goroutine and would not exercise the same race.
	var calls atomic.Int32
	s := NewZeroPWMSentinel(nil, func() { calls.Add(1) })
	defer s.Stop()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			s.Set(0)
			s.Set(60)
		}
		close(done)
	}()
	<-done

	// Final Set was non-zero: no escalation expected.
	time.Sleep(ZeroPWMMaxDuration + 250*time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Errorf("escalate fired during flap: got %d, want 0 (final state non-zero)", got)
	}
}

func TestZeroPWMSentinel_StopIsIdempotent(t *testing.T) {
	s := NewZeroPWMSentinel(nil, func() {})
	s.Set(0)
	s.Stop()
	s.Stop() // must not panic
}

func TestZeroPWMSentinel_TimingTighterThanReadmePromise(t *testing.T) {
	// README claim is "no more than 2 seconds". Verify the constant
	// matches that wording exactly so a future maintainer can't
	// silently widen it past the documented bound.
	if ZeroPWMMaxDuration != 2*time.Second {
		t.Errorf("ZeroPWMMaxDuration = %v; README promises max 2s and this constant must match",
			ZeroPWMMaxDuration)
	}
	if SafePWMFloor < 20 || SafePWMFloor > 80 {
		t.Errorf("SafePWMFloor = %d; want 20..80 (above start_pwm of most fans, still quiet)",
			SafePWMFloor)
	}
}
