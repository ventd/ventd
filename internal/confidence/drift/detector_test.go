package drift

import (
	"math"
	"testing"
)

// obs drives one layer of one channel with a residual + convergence flag,
// holding the other two layers absent (Valid=false). Returns the flags.
func obs(d *Detector, ch string, layer int, residual float64, converged bool) [3]bool {
	var in [3]Inputs
	in[layer] = Inputs{Residual: residual, Converged: converged, Valid: true}
	return d.Observe(ch, in)
}

// TestDrift_ConvergenceGuardNeverFlagsWhileWarming binds RULE-DRIFT-CONVERGE-01:
// a layer that has not converged never flags, regardless of how large its
// residual is — a warming layer's high residual is not drift.
func TestDrift_ConvergenceGuardNeverFlagsWhileWarming(t *testing.T) {
	d := New(DefaultConfig())
	for i := 0; i < 200; i++ {
		if obs(d, "ch", LayerB, 1e6, false)[LayerB] {
			t.Fatalf("a non-converged layer must never flag (tick %d)", i)
		}
	}
	ev := d.Snapshot("ch")[LayerB]
	if ev.Drifting || ev.Converged {
		t.Fatalf("non-converged layer must report not-drifting + not-converged; got %+v", ev)
	}
}

// TestDrift_TripAndClearDwellHysteresis binds RULE-DRIFT-HYSTERESIS-01: a
// converged layer trips on a sustained residual step-up (no single-tick
// flap) and clears only after the residual returns to baseline.
func TestDrift_TripAndClearDwellHysteresis(t *testing.T) {
	c := DefaultConfig()
	d := New(c)

	for i := 0; i < c.WarmupTicks+10; i++ {
		obs(d, "ch", LayerC, 1.0, true) // residual magnitude 1 → sqrt 1
	}
	if d.Snapshot("ch")[LayerC].Drifting {
		t.Fatalf("steady converged layer must not be drifting")
	}

	// Step up: residual 25 → sqrt 5, far above the baseline of 1.
	trippedAt := -1
	for i := 0; i < 60; i++ {
		if obs(d, "ch", LayerC, 25.0, true)[LayerC] {
			trippedAt = i
			break
		}
	}
	if trippedAt < 0 {
		t.Fatalf("sustained residual step-up must trip drift")
	}
	if trippedAt < c.TripDwell-1 {
		t.Fatalf("flagged after %d ticks; TripDwell=%d", trippedAt+1, c.TripDwell)
	}

	// Step back down: must clear after the ClearDwell hysteresis.
	cleared := false
	for i := 0; i < 400; i++ {
		if !obs(d, "ch", LayerC, 1.0, true)[LayerC] {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Fatalf("residual returning to baseline must clear the drift flag")
	}
}

// TestDrift_BaselineFrozenWhileDrifting binds RULE-DRIFT-BASELINE-FREEZE-01:
// once flagged, the baseline μ stops updating, so a sustained anomaly
// cannot raise the reference to absorb itself (which would silently
// un-flag a genuine drift).
func TestDrift_BaselineFrozenWhileDrifting(t *testing.T) {
	c := DefaultConfig()
	d := New(c)

	for i := 0; i < c.WarmupTicks+10; i++ {
		obs(d, "ch", LayerA, 1.0, true)
	}

	// Trip with a sustained large residual.
	for i := 0; i < 60 && !d.Snapshot("ch")[LayerA].Drifting; i++ {
		obs(d, "ch", LayerA, 100.0, true)
	}
	if !d.Snapshot("ch")[LayerA].Drifting {
		t.Fatalf("expected the layer to be drifting after a sustained spike")
	}
	baselineAtTrip := d.Snapshot("ch")[LayerA].Baseline

	// Keep feeding the anomaly while flagged — the baseline must stay put.
	for i := 0; i < 80; i++ {
		obs(d, "ch", LayerA, 100.0, true)
	}
	baselineWhileDrifting := d.Snapshot("ch")[LayerA].Baseline
	if math.Abs(baselineWhileDrifting-baselineAtTrip) > 1e-9 {
		t.Fatalf("baseline must be frozen while drifting: %v != %v", baselineWhileDrifting, baselineAtTrip)
	}
}

// TestDrift_FreshDetectorStartsClean binds RULE-DRIFT-RESTART-01: a
// freshly-constructed Detector (the post-process-restart state) reports a
// clean snapshot for any channel and never flags on the first observation;
// drift state does not persist a restart.
func TestDrift_FreshDetectorStartsClean(t *testing.T) {
	d := New(DefaultConfig())

	ev := d.Snapshot("never-seen")
	for i := 0; i < 3; i++ {
		if ev[i].Drifting || ev[i].Converged {
			t.Fatalf("a fresh detector must report a clean, not-converged snapshot; layer %d = %+v", i, ev[i])
		}
	}
	if obs(d, "ch", LayerA, 1.0, true)[LayerA] {
		t.Fatalf("the first observation of a channel must not flag")
	}
}
