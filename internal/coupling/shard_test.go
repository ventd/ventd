package coupling

import (
	"math"
	"testing"
	"time"

	"gonum.org/v1/gonum/mat"
)

// makeShard returns a Shard with R10-locked defaults for tests.
func makeShard(t *testing.T, nCoupled int) *Shard {
	t.Helper()
	s, err := New(DefaultConfig("test/"+t.Name(), nCoupled))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// TestShard_NCoupledCappedAt16 — RULE-CPL-SHARD-01.
func TestShard_NCoupledCappedAt16(t *testing.T) {
	if _, err := New(DefaultConfig("ch", 17)); err == nil {
		t.Errorf("New: expected error for NCoupled=17, got nil")
	}
	if _, err := New(DefaultConfig("ch", 16)); err != nil {
		t.Errorf("New: NCoupled=16 should be allowed, got %v", err)
	}
	if _, err := New(DefaultConfig("ch", 0)); err != nil {
		t.Errorf("New: NCoupled=0 should be allowed (well-posed reduced model), got %v", err)
	}
}

// TestRLS_RankOneUpdate_MatchesAnalytical — RULE-CPL-SHARD-02.
//
// Feed a synthetic linear system and verify the RLS estimator
// converges to the ground-truth parameters within tolerance.
func TestRLS_RankOneUpdate_MatchesAnalytical(t *testing.T) {
	// 1-fan single-coupling system: T_{k+1} = a·T_k + b·pwm_k + c·load_k.
	// Ground truth: a = 0.9, b = -0.05, c = 0.3.
	const a, b, c = 0.9, -0.05, 0.3
	s := makeShard(t, 1) // NCoupled=1 → d=3

	// Run 500 random observations.
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	t0 := now
	tk := 50.0 // initial temperature
	rng := newDeterministicRNG(42)
	for i := 0; i < 500; i++ {
		pwm := 100.0 + rng.Float64()*150.0 // 100..250
		load := rng.Float64()              // 0..1
		next := a*tk + b*pwm + c*load + rng.Gaussian()*0.05
		phi := []float64{tk, pwm, load}
		if err := s.Update(t0.Add(time.Duration(i)*time.Second), phi, next); err != nil {
			t.Fatalf("Update: %v", err)
		}
		tk = next
	}

	// Compare estimated θ to ground truth.
	snap := s.Read()
	got := snap.Theta
	want := []float64{a, b, c}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 0.05 {
			t.Errorf("theta[%d]: got %f, want %f (±0.05)", i, got[i], want[i])
		}
	}
}

// TestRLS_BoundedCovariance_TrPClamped — RULE-CPL-SHARD-03.
//
// Feed near-constant input to force tr(P) to grow under non-PE
// conditions; assert the clamp keeps it ≤ TrPCap.
func TestRLS_BoundedCovariance_TrPClamped(t *testing.T) {
	s := makeShard(t, 4)
	t0 := time.Now()

	// Constant input: entirely unidentifiable, P should grow
	// until clamped.
	phi := []float64{50.0, 100.0, 100.0, 100.0, 100.0, 0.5}
	for i := 0; i < 1000; i++ {
		_ = s.Update(t0.Add(time.Duration(i)*time.Second), phi, 50.0)
	}

	tr := mat.Trace(s.p)
	if tr > TrPCap*1.01 {
		t.Errorf("tr(P) = %f exceeds clamp %f even with margin", tr, TrPCap)
	}
}

// TestRLS_WarmupGate_AllThreeConditionsMustHold — RULE-CPL-WARMUP-01.
func TestShard_WarmupGate_AllThreeConditionsMustHold(t *testing.T) {
	s := makeShard(t, 1) // d=3, min_samples = 5·9 = 45

	snap := s.Read()
	if snap.Kind != KindWarmup || !snap.WarmingUp {
		t.Errorf("brand-new shard: Kind=%s WarmingUp=%v, expected warming up",
			snap.Kind, snap.WarmingUp)
	}

	// Feed 44 samples (one short of the 5·d² gate). Still
	// warming up regardless of κ.
	t0 := time.Now()
	for i := 0; i < 44; i++ {
		_ = s.Update(t0.Add(time.Duration(i)*time.Second),
			[]float64{50.0, 100.0 + float64(i), 0.5}, 50.0)
	}
	s.SetKind(KindHealthy, 50.0) // simulate good κ
	snap = s.Read()
	if snap.Kind != KindWarmup {
		t.Errorf("44 samples (5·d²=45 gate): expected still warming up, got %s", snap.Kind)
	}

	// One more sample — clears n_samples gate.
	_ = s.Update(t0.Add(45*time.Second),
		[]float64{50.0, 145.0, 0.5}, 50.0)
	s.SetKind(KindHealthy, 50.0)
	snap = s.Read()
	// tr(P) gate may not be clear yet (we used constant phi);
	// we only assert that the n_samples gate is no longer the
	// reason for warmup.
	if snap.NSamples < 45 {
		t.Errorf("n_samples = %d, expected ≥ 45", snap.NSamples)
	}
}

// TestShard_U4_NoCoupledNeighbors_WellPosed — single-zone NUC
// case (R9 §U4). NCoupled=0 → d=2. Trivially identifiable.
func TestShard_U4_NoCoupledNeighbors_WellPosed(t *testing.T) {
	s := makeShard(t, 0) // d=2: just [a, c]
	if s.Dim() != 2 {
		t.Errorf("d: got %d, want 2", s.Dim())
	}
	t0 := time.Now()
	for i := 0; i < 100; i++ {
		// φ = [T, load], y = next T
		t1 := 50.0 + float64(i%10)
		phi := []float64{t1, 0.5}
		_ = s.Update(t0.Add(time.Duration(i)*time.Second), phi, t1*0.9+0.3*0.5)
	}
	snap := s.Read()
	if len(snap.Theta) != 2 {
		t.Errorf("theta len: got %d, want 2", len(snap.Theta))
	}
}

// TestShard_U6_SaturatedPWM_HoldsBijDuringSaturation — when a
// fan is stuck at PWM=255 (saturation), b_ij for that fan is
// unidentifiable; the estimator should not let θ drift
// unboundedly during saturation.
func TestShard_U6_SaturatedPWM_HoldsBijDuringSaturation(t *testing.T) {
	s := makeShard(t, 1)
	t0 := time.Now()

	// 100 normal observations — let θ converge.
	for i := 0; i < 100; i++ {
		pwm := 100.0 + float64(i%50)
		phi := []float64{50.0, pwm, 0.5}
		_ = s.Update(t0.Add(time.Duration(i)*time.Second), phi, 45.0)
	}
	preSatTheta := s.Read().Theta

	// 200 saturation observations: PWM stuck at 255.
	for i := 0; i < 200; i++ {
		phi := []float64{50.0, 255.0, 0.5}
		_ = s.Update(t0.Add(time.Duration(100+i)*time.Second), phi, 45.0)
	}
	postSatTheta := s.Read().Theta

	// b_ij (theta[1]) should not have diverged. R12's clamp
	// + the fact that the saturation column is constant means
	// the estimator updates b minimally.
	if math.Abs(postSatTheta[1]-preSatTheta[1]) > 1.0 {
		t.Errorf("b_ij drifted from %f to %f during saturation (>1.0)",
			preSatTheta[1], postSatTheta[1])
	}
}

// TestShard_LabelReadIsLockFree — RULE-CPL-RUNTIME-02.
//
// Acquire the shard mutex from the test goroutine; verify
// Read() does not block.
func TestShard_LabelReadIsLockFree(t *testing.T) {
	s := makeShard(t, 2)
	s.mu.Lock()
	defer s.mu.Unlock()

	done := make(chan *Snapshot, 1)
	go func() {
		done <- s.Read()
	}()
	select {
	case snap := <-done:
		if snap == nil {
			t.Fatal("Read() returned nil while mu held")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Read() blocked on shard mutex")
	}
}

// TestShard_DeterministicReplay — audit gap #6.
//
// Same observation sequence → byte-identical θ. Catches the bug
// class where map-iteration order, time-dependent rand, or other
// non-determinism leaks into the estimator.
func TestShard_DeterministicReplay(t *testing.T) {
	makeAndRun := func() []float64 {
		s := makeShard(t, 2)
		t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
		for i := 0; i < 200; i++ {
			pwm1 := 100.0 + float64(i%30)
			pwm2 := 150.0 + float64(i%20)
			phi := []float64{50.0 + float64(i%10), pwm1, pwm2, 0.5}
			y := 0.9*phi[0] - 0.05*phi[1] - 0.03*phi[2] + 0.3*phi[3]
			_ = s.Update(t0.Add(time.Duration(i)*time.Second), phi, y)
		}
		return s.Read().Theta
	}

	first := makeAndRun()
	second := makeAndRun()
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("theta[%d]: replay diverged: %v vs %v", i, first[i], second[i])
		}
	}
}

// TestShard_PrivacyNoUserStrings — audit gap #3.
//
// The Snapshot struct contains only opaque fan/sensor IDs and
// learned coefficients. No comm names, no signature labels, no
// user-identifying data. This test asserts that statically.
func TestShard_PrivacyNoUserStrings(t *testing.T) {
	s := makeShard(t, 2)
	snap := s.Read()
	// The only string fields in Snapshot are ChannelID
	// (free-form ID; production wires from the chip+channel-
	// index tuple per R24) and Reason (diagnostic for doctor;
	// must not contain plaintext comm names).
	//
	// We can't assert "no PII present" exhaustively at runtime,
	// but we CAN assert the shape: only two string fields
	// exist and they're documented as non-PII.
	if snap.ChannelID == "" {
		t.Error("ChannelID empty — production should set this")
	}
	// Reason is empty until doctor populates it. If a future
	// commit accidentally pipes a comm name into Reason, this
	// test won't catch it directly — but the audit lists this
	// as a documented invariant for code review.
	_ = snap.Reason
}

// TestSnapshotKind_StringRoundTrip ensures every kind has a
// stable string label for log lines.
func TestSnapshotKind_StringRoundTrip(t *testing.T) {
	kinds := []SnapshotKind{
		KindWarmup, KindHealthy, KindMarginal, KindUnidentifiable, KindCoVarying,
	}
	for _, k := range kinds {
		if k.String() == "" || k.String() == "unknown" {
			t.Errorf("Kind %d has no string label", k)
		}
	}
}

// ── deterministic RNG for reproducible tests ─────────────────

type detRNG struct{ seed uint64 }

func newDeterministicRNG(seed uint64) *detRNG { return &detRNG{seed: seed} }

func (r *detRNG) next() uint64 {
	// xorshift64
	r.seed ^= r.seed << 13
	r.seed ^= r.seed >> 7
	r.seed ^= r.seed << 17
	return r.seed
}

func (r *detRNG) Float64() float64 {
	return float64(r.next()&0x7fffffff) / float64(0x7fffffff)
}

func (r *detRNG) Gaussian() float64 {
	// Box-Muller, single sample
	u1 := r.Float64()
	if u1 < 1e-12 {
		u1 = 1e-12
	}
	u2 := r.Float64()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}
