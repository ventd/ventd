package drift

import "testing"

// TestDriftChart_StepTripsAtControlLimit binds RULE-DRIFT-CHART-01: the
// pure step() chart holds a steady baseline without flagging, trips when
// the monitored statistic sustains a level above μ+L·σ (after the warmup
// gate + TripDwell), clears with LClear hysteresis after ClearDwell, and
// is deterministic.
func TestDriftChart_StepTripsAtControlLimit(t *testing.T) {
	c := DefaultConfig()

	// Warm gate: a step-up BEFORE WarmupTicks observations must not trip,
	// even though z exceeds the control limit — the baseline isn't trusted.
	var sw chartState
	for i := 0; i < 5; i++ {
		sw = step(sw, 1.0, true, c)
	}
	for i := 0; i < c.TripDwell+2; i++ {
		sw = step(sw, 5.0, true, c)
	}
	if sw.flagged {
		t.Fatalf("must not trip before WarmupTicks observations (baseline untrusted)")
	}

	// Warm up fully on a steady baseline; a steady signal never flags.
	var s chartState
	for i := 0; i < c.WarmupTicks+10; i++ {
		s = step(s, 1.0, true, c)
	}
	if s.flagged {
		t.Fatalf("a steady baseline must not flag")
	}

	// Sustained step-up above the control limit → trips after TripDwell.
	trippedAt := -1
	for i := 0; i < 50; i++ {
		s = step(s, 5.0, true, c)
		if s.flagged {
			trippedAt = i
			break
		}
	}
	if trippedAt < 0 {
		t.Fatalf("a sustained step-up above μ+L·σ must trip the chart")
	}
	if trippedAt < c.TripDwell-1 {
		t.Fatalf("tripped after %d steps; TripDwell=%d requires at least that many", trippedAt+1, c.TripDwell)
	}

	// Returning to baseline → clears after ClearDwell hysteresis.
	clearedAt := -1
	for i := 0; i < 300; i++ {
		s = step(s, 1.0, true, c)
		if !s.flagged {
			clearedAt = i
			break
		}
	}
	if clearedAt < 0 {
		t.Fatalf("returning to baseline must clear the drift flag")
	}

	// Determinism: an identical input sequence yields identical state.
	var a, b chartState
	for _, r := range []float64{1, 1, 2, 1, 3, 1, 0.5, 4} {
		a = step(a, r, true, c)
		b = step(b, r, true, c)
	}
	if a != b {
		t.Fatalf("step must be deterministic: %+v != %+v", a, b)
	}
}
