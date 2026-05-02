package aggregator

import (
	"math"
	"testing"
	"time"
)

const epsilon = 1e-9

// TestAggregator_MinCollapse binds RULE-AGG-MIN-01:
//
//	w_raw = clamp(min(conf_A_decayed, conf_B_decayed, conf_C_decayed), 0, 1)
//
// Verifies the min collapse, the [0, 1] clamp, and that the smallest
// of the three inputs always wins.
func TestAggregator_MinCollapse(t *testing.T) {
	a := New(Config{})
	now := time.Unix(1_000_000, 0)
	cases := []struct {
		ca, cb, cc float64
		want       float64
	}{
		{0.9, 0.7, 0.5, 0.5},  // C smallest
		{0.4, 0.9, 0.9, 0.4},  // A smallest
		{0.6, 0.3, 0.8, 0.3},  // B smallest
		{0.0, 0.0, 0.0, 0.0},  // boundary low
		{1.0, 1.0, 1.0, 1.0},  // boundary high
		{-0.5, 0.5, 0.5, 0.0}, // clamp negative
		{0.5, 1.5, 0.5, 0.5},  // 1.5 ignored by min, but other two are ok
	}
	for i, tc := range cases {
		// Use a long-enough dt that the LPF's first sample equals w_raw,
		// so we can read w_raw cleanly via Wraw on the snapshot. Different
		// channelIDs per case so each starts fresh.
		channelID := "ch" + string(rune('0'+i))
		s := a.Tick(channelID, tc.ca, tc.cb, tc.cc, [3]bool{}, true, now)
		if math.Abs(s.Wraw-tc.want) > epsilon {
			t.Errorf("case %d (a=%v b=%v c=%v): Wraw got %v, want %v",
				i, tc.ca, tc.cb, tc.cc, s.Wraw, tc.want)
		}
	}
}

// TestAggregator_LPFWrapsMin binds RULE-AGG-LPF-01: the LPF
// (τ_w = 30 s) wraps the min, NOT each component separately.
//
// Verifies that on the first tick (no prior state) w_filt converges
// toward w_raw at rate dt/τ_w. With dt = τ_w / 2 = 15 s, w_filt =
// 0.5 · w_raw (starting from prev=0).
func TestAggregator_LPFWrapsMin(t *testing.T) {
	a := New(Config{})
	t0 := time.Unix(1_000_000, 0)

	// First tick to seed lastTick.
	a.Tick("ch1", 0.8, 0.8, 0.8, [3]bool{}, true, t0)

	// Second tick at t0 + 15s with all confs = 0.8. dt = 15s,
	// τ_w = 30s → α = 0.5. wFilt = prev + 0.5 · (0.8 − prev).
	//
	// prev was computed at the first tick: dt=0, so wFilt[0] = 0.
	// At t1: wFilt[1] = 0 + 0.5 · (0.8 - 0) = 0.4.
	t1 := t0.Add(15 * time.Second)
	s := a.Tick("ch1", 0.8, 0.8, 0.8, [3]bool{}, true, t1)
	if math.Abs(s.Wfilt-0.4) > 1e-6 {
		t.Errorf("Wfilt at t0+15s: got %v, want 0.4 (LPF α=0.5 from 0 toward 0.8)", s.Wfilt)
	}

	// Third tick at t1 + 15s with the same input. wFilt[2] = 0.4 +
	// 0.5 · (0.8 − 0.4) = 0.6.
	t2 := t1.Add(15 * time.Second)
	s = a.Tick("ch1", 0.8, 0.8, 0.8, [3]bool{}, true, t2)
	if math.Abs(s.Wfilt-0.6) > 1e-6 {
		t.Errorf("Wfilt at t1+15s: got %v, want 0.6", s.Wfilt)
	}
}

// TestAggregator_LipschitzClamp binds RULE-AGG-LIPSCHITZ-01:
// |w_pred − w_pred_prev| ≤ L_max · dt = 0.05 · 2 = 0.1 per 2-s tick.
func TestAggregator_LipschitzClamp(t *testing.T) {
	a := New(Config{})
	t0 := time.Unix(1_000_000, 0)

	// Seed at high confidence for several ticks so wFilt + wPred
	// converge toward 1.0. Loop tick i=0..199 lands at t = t0 + 2i·s,
	// so the last tick was at t0 + 398s.
	for i := 0; i < 200; i++ {
		a.Tick("ch1", 1.0, 1.0, 1.0, [3]bool{}, true, t0.Add(time.Duration(i)*2*time.Second))
	}
	// Step the conf inputs to 0.0 exactly 2 s after the last loop tick.
	prev := a.Read("ch1").Wpred
	tStep := t0.Add(200 * 2 * time.Second) // t0 + 400s, i.e. last + 2s
	s := a.Tick("ch1", 0.0, 0.0, 0.0, [3]bool{}, true, tStep)
	delta := prev - s.Wpred
	maxAllowed := LMax * 2 // dt=2s → 0.05·2 = 0.1
	if delta > maxAllowed+1e-9 {
		t.Errorf("Lipschitz violation: delta=%v, max allowed %v", delta, maxAllowed)
	}
	if delta <= 0 {
		t.Errorf("expected wPred to fall after conf drop; delta=%v", delta)
	}
}

// TestAggregator_DriftDecaysBeforeMin binds RULE-AGG-DRIFT-01: per-
// layer 0.5^(t/60s) decay applies BEFORE the min collapse.
//
// Verifies that setting drift on layer A while the others are stable
// causes A's contribution to halve every 60 s; the min collapse
// then sees the decayed A value.
func TestAggregator_DriftDecaysBeforeMin(t *testing.T) {
	a := New(Config{})
	t0 := time.Unix(1_000_000, 0)

	// All three layers start at 1.0; layer A's drift is set at t0.
	// At t0 + 60s, conf_A_decayed = 1.0 · 0.5 = 0.5. min(0.5, 1, 1)
	// = 0.5 → w_raw = 0.5.
	a.SetDrift("ch1", LayerA, true, t0)
	t1 := t0.Add(60 * time.Second)
	driftFlags := [3]bool{true, false, false}
	s := a.Tick("ch1", 1.0, 1.0, 1.0, driftFlags, true, t1)
	if math.Abs(s.Wraw-0.5) > 1e-6 {
		t.Errorf("Wraw with A decay 60s: got %v, want 0.5", s.Wraw)
	}

	// At t0 + 120s, conf_A_decayed = 1.0 · 0.25.
	t2 := t0.Add(120 * time.Second)
	s = a.Tick("ch1", 1.0, 1.0, 1.0, driftFlags, true, t2)
	if math.Abs(s.Wraw-0.25) > 1e-6 {
		t.Errorf("Wraw with A decay 120s: got %v, want 0.25", s.Wraw)
	}
}

// TestAggregator_ColdStartHardPin binds RULE-AGG-COLDSTART-01: w_pred
// = 0 for the 5 minutes after Envelope C completion.
func TestAggregator_ColdStartHardPin(t *testing.T) {
	a := New(Config{})
	t0 := time.Unix(1_000_000, 0)
	a.SetEnvelopeCDoneAt(t0)

	// Inside the window: every Tick must produce w_pred = 0
	// regardless of the input confs.
	for _, dt := range []time.Duration{0, 1 * time.Minute, 4*time.Minute + 30*time.Second} {
		s := a.Tick("ch1", 1.0, 1.0, 1.0, [3]bool{}, true, t0.Add(dt))
		if s.Wpred != 0 {
			t.Errorf("at dt=%v inside cold-start window: Wpred=%v, want 0", dt, s.Wpred)
		}
		if s.UIState != UIStateColdStart {
			t.Errorf("at dt=%v: UIState=%q, want %q", dt, s.UIState, UIStateColdStart)
		}
	}

	// Outside the window: the LPF + Lipschitz machinery applies
	// normally. Tick a bunch to converge.
	tBase := t0.Add(10 * time.Minute)
	for i := 0; i < 200; i++ {
		a.Tick("ch1", 1.0, 1.0, 1.0, [3]bool{}, true, tBase.Add(time.Duration(i)*2*time.Second))
	}
	s := a.Read("ch1")
	if s.Wpred <= 0 {
		t.Errorf("after 200 ticks past cold-start: Wpred=%v, want > 0", s.Wpred)
	}
}

// TestAggregator_GlobalGate binds RULE-AGG-GLOBAL-01: the global
// w_pred_system AND-gate forces every channel's w_pred to 0
// immediately (bypassing Lipschitz).
func TestAggregator_GlobalGate(t *testing.T) {
	a := New(Config{})
	t0 := time.Unix(1_000_000, 0)

	// Converge w_pred to a high value with the gate ON.
	for i := 0; i < 200; i++ {
		a.Tick("ch1", 1.0, 1.0, 1.0, [3]bool{}, true, t0.Add(time.Duration(i)*2*time.Second))
	}
	prev := a.Read("ch1").Wpred
	if prev < 0.5 {
		t.Fatalf("setup: Wpred didn't converge above 0.5 (got %v)", prev)
	}

	// Flip the gate OFF. The next Tick must drop w_pred to 0
	// regardless of how far above the Lipschitz cap that drop is.
	tStep := t0.Add(202 * 2 * time.Second)
	s := a.Tick("ch1", 1.0, 1.0, 1.0, [3]bool{}, false, tStep)
	if s.Wpred != 0 {
		t.Errorf("global gate OFF: Wpred=%v, want 0 (Lipschitz must NOT clamp this drop)", s.Wpred)
	}
	if s.UIState != UIStateRefused {
		t.Errorf("global gate OFF: UIState=%q, want %q", s.UIState, UIStateRefused)
	}
}

// TestAggregator_ActiveSignatureCollapse binds RULE-AGG-SIG-COLLAPSE-01:
// conf_C is supplied directly by the caller as the active-signature
// shard's product term; when no warmed shard exists, conf_C = 0 and
// the LPF rides w_pred down at L_max.
//
// We simulate this by passing conf_C = 0 while A and B are high.
// The min collapse picks 0; LPF + Lipschitz pull w_pred toward 0
// at rate ≤ L_max · dt.
func TestAggregator_ActiveSignatureCollapse(t *testing.T) {
	a := New(Config{})
	t0 := time.Unix(1_000_000, 0)
	// Seed high.
	for i := 0; i < 200; i++ {
		a.Tick("ch1", 1.0, 1.0, 1.0, [3]bool{}, true, t0.Add(time.Duration(i)*2*time.Second))
	}
	prev := a.Read("ch1").Wpred

	// Drop conf_C to 0 (active signature has no warmed shard) exactly
	// 2 s after the last loop tick.
	tStep := t0.Add(200 * 2 * time.Second)
	s := a.Tick("ch1", 1.0, 1.0, 0.0, [3]bool{}, true, tStep)

	if s.Wraw != 0 {
		t.Errorf("Wraw after conf_C=0: got %v, want 0 (min collapse picks the smallest)", s.Wraw)
	}
	delta := prev - s.Wpred
	if delta > LMax*2+1e-9 {
		t.Errorf("Wpred dropped faster than Lipschitz allows: delta=%v, max %v", delta, LMax*2)
	}
	if delta <= 0 {
		t.Errorf("expected Wpred to fall after conf_C drop; delta=%v", delta)
	}
}

// TestAggregator_UIStateClassification verifies the 5-state UI
// label produced under representative conditions. Tied loosely to
// RULE-UI-CONF-01 (the dedicated UI test will live alongside the
// dashboard rendering code).
func TestAggregator_UIStateClassification(t *testing.T) {
	a := New(Config{})
	t0 := time.Unix(1_000_000, 0)

	// gate off → Refused
	s := a.Tick("ch1", 0.9, 0.9, 0.9, [3]bool{}, false, t0)
	if s.UIState != UIStateRefused {
		t.Errorf("gate-off: UIState=%q, want %q", s.UIState, UIStateRefused)
	}

	// drift on → Drifting (gate on, no cold-start)
	s = a.Tick("ch2", 0.9, 0.9, 0.9, [3]bool{true, false, false}, true, t0)
	if s.UIState != UIStateDrifting {
		t.Errorf("drift-on: UIState=%q, want %q", s.UIState, UIStateDrifting)
	}

	// cold-start active → ColdStart
	a.SetEnvelopeCDoneAt(t0)
	s = a.Tick("ch3", 0.9, 0.9, 0.9, [3]bool{}, true, t0)
	if s.UIState != UIStateColdStart {
		t.Errorf("cold-start: UIState=%q, want %q", s.UIState, UIStateColdStart)
	}
}
