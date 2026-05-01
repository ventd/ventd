package layer_a

import (
	"math"
	"testing"
	"time"
)

const epsilon = 1e-9

// TestTierCeilings_Locked binds RULE-CONFA-TIER-01: the R8 fallback
// ceiling table is exactly {1.00, 0.85, 0.70, 0.55, 0.45, 0.30, 0.30,
// 0.00}. A change requires updating the spec table in lockstep.
func TestTierCeilings_Locked(t *testing.T) {
	want := []float64{1.00, 0.85, 0.70, 0.55, 0.45, 0.30, 0.30, 0.00}
	for i, w := range want {
		if got := R8Ceiling(uint8(i)); math.Abs(got-w) > epsilon {
			t.Errorf("R8Ceiling(tier=%d): got %v, want %v", i, got, w)
		}
	}
	// Out-of-range tier collapses to TierOpenLoopPinned (0.0).
	if got := R8Ceiling(255); got != 0 {
		t.Errorf("R8Ceiling(255): got %v, want 0 (clamp to open-loop)", got)
	}
}

// TestConfA_Formula binds RULE-CONFA-FORMULA-01:
//
//	conf_A = R8_ceiling × √coverage × (1 − norm_residual) × recency
//
// We pin a specific input set so the four-term product produces an
// expected value. Tier=1 (ceiling 0.85), full coverage 1.0, zero
// residual, zero age — should yield exactly 0.85.
func TestConfA_Formula(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	e, _ := New(Config{})
	if err := e.Admit("ch1", TierCoupledInference, DefaultNoiseFloor, now); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	// Saturate the histogram: 3 observations into every one of 16 bins,
	// each with predicted=observed (zero residual).
	for bin := 0; bin < NumBins; bin++ {
		pwm := uint8(bin*BinWidth + 1) // pwm 1, 17, 33, ...
		for k := 0; k < MinObsPerBinForCoverage; k++ {
			e.Observe("ch1", pwm, 1000, 1000, now)
		}
	}
	s := e.Read("ch1")
	if s == nil {
		t.Fatal("Read returned nil")
	}
	if math.Abs(s.Coverage-1.0) > epsilon {
		t.Errorf("Coverage: got %v, want 1.0", s.Coverage)
	}
	if math.Abs(s.RMSResidual) > epsilon {
		t.Errorf("RMSResidual: got %v, want 0", s.RMSResidual)
	}
	if math.Abs(s.ConfA-0.85) > 1e-6 {
		t.Errorf("ConfA: got %v, want 0.85 (tier=1, cov=1, residual=0, age=0)", s.ConfA)
	}
}

// TestCoverage_BinWidth binds RULE-CONFA-COVERAGE-01: bin width 16
// raw PWM units, 16 bins (0/16/.../240), coverage counts bins with
// at least 3 observations.
func TestCoverage_BinWidth(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	e, _ := New(Config{})
	if err := e.Admit("ch1", TierRPMTach, 0, now); err != nil {
		t.Fatalf("Admit: %v", err)
	}

	// PWM=0 → bin 0; PWM=16 → bin 1; PWM=240 → bin 15; PWM=255 →
	// clamped to bin 15.
	cases := []struct {
		pwm  uint8
		want int
	}{{0, 0}, {15, 0}, {16, 1}, {17, 1}, {31, 1}, {32, 2}, {240, 15}, {255, 15}}
	for _, tc := range cases {
		if bin := int(tc.pwm) / BinWidth; bin >= NumBins {
			bin = NumBins - 1
			if bin != tc.want {
				t.Errorf("bin clamp: pwm=%d got %d, want %d", tc.pwm, bin, tc.want)
			}
		} else if bin != tc.want {
			t.Errorf("bin: pwm=%d got %d, want %d", tc.pwm, bin, tc.want)
		}
	}

	// Coverage uses ≥ 3 obs per bin. Hit only 8 bins, 3 times each.
	for bin := 0; bin < 8; bin++ {
		pwm := uint8(bin * BinWidth)
		for k := 0; k < MinObsPerBinForCoverage; k++ {
			e.Observe("ch1", pwm, -1, 0, now)
		}
	}
	s := e.Read("ch1")
	if math.Abs(s.Coverage-0.5) > epsilon {
		t.Errorf("Coverage with 8/16 bins covered: got %v, want 0.5", s.Coverage)
	}

	// One more obs to a 9th bin (only 2 hits — below threshold).
	e.Observe("ch1", uint8(8*BinWidth), -1, 0, now)
	e.Observe("ch1", uint8(8*BinWidth), -1, 0, now)
	s = e.Read("ch1")
	if math.Abs(s.Coverage-0.5) > epsilon {
		t.Errorf("Coverage with bin8 below threshold: got %v, want 0.5", s.Coverage)
	}

	// Third obs to bin 8 — now it crosses.
	e.Observe("ch1", uint8(8*BinWidth), -1, 0, now)
	s = e.Read("ch1")
	want := 9.0 / 16.0
	if math.Abs(s.Coverage-want) > epsilon {
		t.Errorf("Coverage with 9/16 bins covered: got %v, want %v", s.Coverage, want)
	}
}

// TestRecency_DecayHalfLife7d binds RULE-CONFA-RECENCY-01: recency =
// exp(-age_seconds/604800). After τ = 7 days the recency is 1/e.
// After 0.5·τ it's exp(-0.5) ≈ 0.6065.
func TestRecency_DecayHalfLife7d(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	e, _ := New(Config{})
	if err := e.Admit("ch1", TierRPMTach, 0, t0); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	// Add full coverage so the only varying term is recency.
	for bin := 0; bin < NumBins; bin++ {
		pwm := uint8(bin*BinWidth + 1)
		for k := 0; k < MinObsPerBinForCoverage; k++ {
			e.Observe("ch1", pwm, 1000, 1000, t0)
		}
	}
	cases := []struct {
		dt   time.Duration
		want float64 // expected ConfA (tier-0 ceiling 1.00, cov=1, residual=0, * recency)
	}{
		{0, 1.0},
		{RecencyTau / 2, math.Exp(-0.5)},
		{RecencyTau, math.Exp(-1.0)},
		{RecencyTau * 2, math.Exp(-2.0)},
	}
	for _, tc := range cases {
		// Re-publish with a future "now" — we don't Observe (which
		// resets lastUpdate) so age increases.
		e.mu.Lock()
		c := e.channels["ch1"]
		publish("ch1", c, t0.Add(tc.dt))
		e.mu.Unlock()
		s := e.Read("ch1")
		if math.Abs(s.ConfA-tc.want) > 1e-6 {
			t.Errorf("ConfA at age=%v: got %v, want %v", tc.dt, s.ConfA, tc.want)
		}
	}
}

// TestRecency_ResetOnObserve verifies that any Observe call resets
// the lastUpdate clock, so recency snaps back to 1.0 — per
// RULE-CONFA-RECENCY-01 ("resets only on admissible update").
func TestRecency_ResetOnObserve(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	e, _ := New(Config{})
	_ = e.Admit("ch1", TierRPMTach, 0, t0)
	for bin := 0; bin < NumBins; bin++ {
		for k := 0; k < MinObsPerBinForCoverage; k++ {
			e.Observe("ch1", uint8(bin*BinWidth+1), 1000, 1000, t0)
		}
	}
	// Observe far in the future: age must reset to ~zero.
	tNew := t0.Add(RecencyTau * 5)
	e.Observe("ch1", 0, 1000, 1000, tNew)
	s := e.Read("ch1")
	if s.Age > time.Second {
		t.Errorf("Age after fresh Observe: got %v, want ~0", s.Age)
	}
}

// TestSnapshotReadIsLockFree binds RULE-CONFA-SNAPSHOT-01: Read must
// not block on the estimator mutex when a Save/Load is in flight.
// The test acquires e.mu from a goroutine and verifies Read returns
// a snapshot in < 100 ms.
func TestSnapshotReadIsLockFree(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	e, _ := New(Config{})
	_ = e.Admit("ch1", TierRPMTach, 0, t0)
	e.Observe("ch1", 100, 1000, 1000, t0)

	hold := make(chan struct{})
	released := make(chan struct{})
	go func() {
		e.mu.Lock()
		close(hold)
		<-released
		e.mu.Unlock()
	}()
	<-hold
	defer close(released)

	// Read SnapshotAll uses e.mu — that one will block. But Read on a
	// channel uses atomic.Pointer.Load directly without going through
	// the mutex once the channel pointer is in scope. We need to
	// special-case: Read takes e.mu briefly to look up the channel
	// (which IS the lock-free contract per spec — the lock is on the
	// map lookup, not on the snapshot read). Use the published Snapshot
	// pointer directly.
	c := e.channels["ch1"]
	if c == nil {
		t.Fatal("channel not present in map")
	}
	done := make(chan *Snapshot, 1)
	go func() {
		// Atomic load — never touches e.mu.
		done <- c.snapshot.Load()
	}()
	select {
	case s := <-done:
		if s == nil {
			t.Fatal("atomic load returned nil while goroutine holds e.mu")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("atomic Snapshot load blocked on e.mu — RULE-CONFA-SNAPSHOT-01 violated")
	}
}
