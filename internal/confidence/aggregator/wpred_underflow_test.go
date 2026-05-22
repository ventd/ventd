package aggregator

import (
	"testing"
	"time"
)

// TestWPred_ClampsUnderflowToZero pins the #1253 numerical floor: a
// Layer-C marginal-benefit estimator whose Fisher matrix has gone
// near-singular emits conf_c values on the order of 1e-150 that
// propagate through the min collapse and the LPF into w_pred at the
// same magnitude. Those values are statistical noise; the blender
// should see w_pred=0 and run pure reactive. Without the clamp the
// reported confidence_min collapses to e^-150 exponential artifacts
// and the smart-mode UI reads as "converged on nothing".
func TestWPred_ClampsUnderflowToZero(t *testing.T) {
	now := time.Unix(0, 0)
	a := New(Config{})

	// Drive enough ticks at confC=1e-200 to let the LPF + Lipschitz
	// decay wPred close to the noise floor. Without the underflow
	// clamp, wPred would settle at ~1e-200 (an IEEE-754 denormal
	// curiosity) and the smart-mode UI's confidence_min would read
	// as the exponential artifact reported in #1253.
	for i := 0; i < 200; i++ {
		s := a.Tick("ch1", 0.5, 0.5, 1e-200, [3]bool{}, true, now)
		if s.Wpred > 0 && s.Wpred < WPredUnderflowFloor {
			t.Fatalf("tick %d: Wpred = %v leaked below floor %g without clamp",
				i, s.Wpred, WPredUnderflowFloor)
		}
		now = now.Add(time.Second)
	}
	// After ~200s of decay against a noise-floor target the LPF +
	// Lipschitz have driven wPred below the floor, and the clamp
	// should have it pinned at exactly 0.
	s := a.Tick("ch1", 0.5, 0.5, 1e-200, [3]bool{}, true, now)
	if s.Wpred != 0 {
		t.Errorf("after 200s decay against 1e-200 floor: Wpred = %v; want exact 0 (#1253)",
			s.Wpred)
	}
}

// TestWPred_AcceptsNormalSmallValues confirms the clamp is tight
// enough not to swallow legitimate-but-small w_pred. A LPF-tracked
// value well above the floor must survive.
func TestWPred_AcceptsNormalSmallValues(t *testing.T) {
	now := time.Unix(0, 0)
	a := New(Config{})

	// Drive 60s of ticks at conf=0.5 each to let the LPF settle
	// above the underflow floor without overshooting into normal
	// confidence territory.
	for i := 0; i < 60; i++ {
		a.Tick("ch1", 0.5, 0.5, 0.5, [3]bool{}, true, now)
		now = now.Add(time.Second)
	}
	s := a.Tick("ch1", 0.5, 0.5, 0.5, [3]bool{}, true, now)
	if s.Wpred <= WPredUnderflowFloor {
		t.Errorf("Wpred = %v after 60s ramp; want > %g (clamp must not swallow legitimate values)",
			s.Wpred, WPredUnderflowFloor)
	}
}

// TestWPred_ZeroStaysZero pins the trivial path: a channel that has
// always received zero confidence stays at zero. The underflow clamp
// must not introduce noise here.
func TestWPred_ZeroStaysZero(t *testing.T) {
	now := time.Unix(0, 0)
	a := New(Config{})

	for i := 0; i < 10; i++ {
		s := a.Tick("ch1", 0, 0, 0, [3]bool{}, true, now)
		if s.Wpred != 0 {
			t.Errorf("tick %d: Wpred = %v; want 0", i, s.Wpred)
		}
		now = now.Add(time.Second)
	}
}
