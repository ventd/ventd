package marginal

import (
	"math"
	"testing"
	"time"

	"gonum.org/v1/gonum/mat"
)

// TestShard_DimensionFixedAt2 — RULE-CMB-SHARD-01.
func TestShard_DimensionFixedAt2(t *testing.T) {
	if DimC != 2 {
		t.Fatalf("DimC = %d; want 2 per R10 §10.1", DimC)
	}
	s, err := New(DefaultConfig("ch", "sig"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := s.theta.Len(); got != DimC {
		t.Errorf("theta.Len = %d; want %d", got, DimC)
	}
	if err := s.Update(time.Now(), []float64{1.0}, 0.0); err == nil {
		t.Errorf("Update with phi len 1 should fail (need DimC=2)")
	}
}

// TestRLS_RankOneUpdate_MatchesAnalytical — RULE-CMB-SHARD-02.
//
// Synthetic linear system: y = β_0 + β_1·load with β_0 = -0.05,
// β_1 = -0.01 (cooling fan: ramping helps less under heavy load).
// 500 random samples; assert convergence within tolerance.
func TestRLS_RankOneUpdate_MatchesAnalytical(t *testing.T) {
	const b0, b1 = -0.05, -0.01
	cfg := DefaultConfig("ch", "sig")
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.SetParentOutOfWarmup(true)

	rng := newRNG(42)
	for i := 0; i < 500; i++ {
		load := rng.Float64() // 0..1
		y := b0 + b1*load + 0.001*rng.Gaussian()
		_ = s.Update(time.Now().Add(time.Duration(i)*time.Second),
			[]float64{1.0, load}, y)
	}
	snap := s.Read()
	got := snap.Theta
	if math.Abs(got[0]-b0) > 0.005 {
		t.Errorf("β_0 = %f; want %f ±0.005", got[0], b0)
	}
	if math.Abs(got[1]-b1) > 0.005 {
		t.Errorf("β_1 = %f; want %f ±0.005", got[1], b1)
	}
}

// TestRLS_BoundedCovariance_TrPClamped — RULE-CMB-SHARD-03.
func TestRLS_BoundedCovariance_TrPClamped(t *testing.T) {
	s, err := New(DefaultConfig("ch", "sig"))
	if err != nil {
		t.Fatal(err)
	}
	// Constant input: entirely unidentifiable, P should grow until
	// clamped.
	for i := 0; i < 1000; i++ {
		_ = s.Update(time.Now(), []float64{1.0, 0.5}, -0.04)
	}
	tr := mat.Trace(s.p)
	if tr > TrPCap*1.01 {
		t.Errorf("tr(P) = %f exceeds clamp %f", tr, TrPCap)
	}
}

// TestSaturation_Path_A_Predicted — RULE-CMB-SAT-01.
//
// Set θ such that β_0 + β_1·load × ΔPWM ≈ 1°C (below 2°C threshold)
// and verify IsSaturated returns true. Uses a converged shard to
// satisfy the warmup-clear precondition.
func TestSaturation_Path_A_Predicted(t *testing.T) {
	s := convergedShard(t, []float64{0.5, 0.0}) // β_0 = 0.5 → predicted ΔT for ΔPWM=1 is 0.5°C
	if !s.IsSaturated(1, 0.0) {
		t.Errorf("expected saturated at predicted ΔT=0.5°C < 2°C threshold")
	}
	// β_0 = 3.0 → predicted ΔT for ΔPWM=1 is 3°C, above threshold
	s2 := convergedShard(t, []float64{3.0, 0.0})
	if s2.IsSaturated(1, 0.0) {
		t.Errorf("expected NOT saturated at predicted ΔT=3°C > 2°C threshold")
	}
}

// TestSaturation_Path_B_Observed — RULE-CMB-SAT-02.
func TestSaturation_Path_B_Observed(t *testing.T) {
	s := convergedShard(t, []float64{3.0, 0.0}) // Path A would say not-saturated
	for i := 0; i < SaturationNWritesFastLoop; i++ {
		s.ObserveOutcome(0.5, 200) // sub-2°C ΔT
	}
	if !s.IsSaturated(1, 0.0) {
		t.Errorf("expected saturated via Path B after %d sub-2°C writes",
			SaturationNWritesFastLoop)
	}
	if got := s.Read().ObservedSaturationPWM; got != 200 {
		t.Errorf("ObservedSaturationPWM = %d; want 200", got)
	}
	// Break the streak.
	s.ObserveOutcome(5.0, 200)
	if s.Read().ObservedSaturationPWM != 0 {
		t.Errorf("streak break should reset ObservedSaturationPWM to 0")
	}
}

// TestSaturation_FalseDuringWarmup — RULE-CMB-SAT-03.
//
// Wrong-direction Layer-B prior installed; saturation flag must be
// forced false during warmup (the wrong-prior guard).
func TestSaturation_FalseDuringWarmup(t *testing.T) {
	cfg := DefaultConfig("ch", "sig")
	cfg.LayerBPriorBii = 0.5 // wrong-direction prior (positive for cooling)
	cfg.LayerBConfirmed = true
	cfg.PWMUnitMax = 1
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Don't clear parent warmup → warmup never clears.
	if !s.Read().WarmingUp {
		t.Errorf("shard should be warming up by default")
	}
	if s.IsSaturated(1, 0.0) {
		t.Errorf("saturation MUST be false during warmup (wrong-prior guard)")
	}
}

// TestWarmupGate_RequiresLayerBClearance — RULE-CMB-WARMUP-01.
func TestWarmupGate_RequiresLayerBClearance(t *testing.T) {
	s, err := New(DefaultConfig("ch", "sig"))
	if err != nil {
		t.Fatal(err)
	}
	// Feed enough samples + drive tr(P) down → 2 conditions met.
	for i := 0; i < 60; i++ {
		_ = s.Update(time.Now(), []float64{1.0, float64(i%5) / 5.0}, -0.04)
	}
	if !s.Read().WarmingUp {
		t.Errorf("warmup must remain true while parent Layer-B is in warmup")
	}
	s.SetParentOutOfWarmup(true)
	if s.Read().WarmingUp {
		t.Errorf("warmup must clear once all 3 conditions + parent are met")
	}
}

// TestPriorSeeding_FromLayerB — RULE-CMB-PRIOR-01.
func TestPriorSeeding_FromLayerB(t *testing.T) {
	cfg := DefaultConfig("ch", "sig")
	cfg.LayerBPriorBii = -2.55 // ΔT_i per ΔPWM_i = -10m°C/PWM if PWMUnitMax=255
	cfg.LayerBConfirmed = true
	cfg.PWMUnitMax = 255
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := -2.55 / 255.0
	if got := s.theta.AtVec(0); math.Abs(got-want) > 1e-9 {
		t.Errorf("β_0 prior = %f; want %f", got, want)
	}
	if got := s.theta.AtVec(1); got != 0.0 {
		t.Errorf("β_1 must seed at 0; got %f", got)
	}

	// Without confirmation, prior is NOT used.
	cfg2 := cfg
	cfg2.LayerBConfirmed = false
	s2, _ := New(cfg2)
	if s2.theta.AtVec(0) != 0 {
		t.Errorf("unconfirmed Layer-B prior must NOT seed β_0")
	}
}

// TestSnapshotReadIsLockFree — RULE-CMB-RUNTIME-03.
func TestSnapshotReadIsLockFree(t *testing.T) {
	s, _ := New(DefaultConfig("ch", "sig"))
	s.mu.Lock()
	defer s.mu.Unlock()

	done := make(chan *Snapshot, 1)
	go func() { done <- s.Read() }()
	select {
	case snap := <-done:
		if snap == nil {
			t.Fatal("Read returned nil while mu held")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Read blocked on mu")
	}
}

// TestSnapshot_ExposesR12Inputs — RULE-CMB-CONF-01.
func TestSnapshot_ExposesR12Inputs(t *testing.T) {
	s := convergedShard(t, []float64{-0.05, -0.01})
	snap := s.Read()
	if snap.InitialP <= 0 {
		t.Errorf("InitialP must be > 0 in Snapshot; got %f", snap.InitialP)
	}
	if snap.Confidence.SampleCountTerm < 1.0 {
		t.Errorf("converged shard SampleCountTerm should saturate to 1.0; got %f",
			snap.Confidence.SampleCountTerm)
	}
	if snap.Confidence.CovarianceTerm < 0 || snap.Confidence.CovarianceTerm > 1 {
		t.Errorf("CovarianceTerm out of [0,1]: %f", snap.Confidence.CovarianceTerm)
	}
	if snap.Confidence.ResidualTerm < 0 || snap.Confidence.ResidualTerm > 1 {
		t.Errorf("ResidualTerm out of [0,1]: %f", snap.Confidence.ResidualTerm)
	}
}

// TestThresholds_MatchR11Locked — RULE-CMB-R11-01.
func TestThresholds_MatchR11Locked(t *testing.T) {
	if SaturationDeltaT != 2.0 {
		t.Errorf("SaturationDeltaT = %f; R11 §0 locks 2.0", SaturationDeltaT)
	}
	if SaturationNWritesFastLoop != 20 {
		t.Errorf("SaturationNWritesFastLoop = %d; R11 §0 locks 20", SaturationNWritesFastLoop)
	}
	if SaturationNReadsSlowLoop != 3 {
		t.Errorf("SaturationNReadsSlowLoop = %d; R11 §0 locks 3", SaturationNReadsSlowLoop)
	}
}

// convergedShard returns a Shard pre-loaded with the requested θ
// and enough samples to clear warmup (n ≥ 20, tr(P) ≤ 0.5 tr(P_0),
// parent out of warmup).
func convergedShard(t *testing.T, theta []float64) *Shard {
	t.Helper()
	cfg := DefaultConfig("ch", "sig")
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.SetParentOutOfWarmup(true)
	for i := 0; i < 60; i++ {
		load := float64(i%10) / 10.0
		y := theta[0] + theta[1]*load
		_ = s.Update(time.Now().Add(time.Duration(i)*time.Second),
			[]float64{1.0, load}, y)
	}
	return s
}

// rng is a deterministic xorshift64 + Box-Muller pair. Same shape
// as v0.5.7 coupling tests; kept here so the marginal package's
// tests are self-contained.
type rng struct{ seed uint64 }

func newRNG(seed uint64) *rng { return &rng{seed: seed} }

func (r *rng) next() uint64 {
	r.seed ^= r.seed << 13
	r.seed ^= r.seed >> 7
	r.seed ^= r.seed << 17
	return r.seed
}

func (r *rng) Float64() float64 {
	return float64(r.next()&0x7fffffff) / float64(0x7fffffff)
}

func (r *rng) Gaussian() float64 {
	u1 := r.Float64()
	if u1 < 1e-12 {
		u1 = 1e-12
	}
	u2 := r.Float64()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}
