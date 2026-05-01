package coupling

import (
	"math"
	"testing"
)

// TestWindowedRegressor_W60Subsampled — RULE-CPL-IDENT-01.
func TestWindowedRegressor_W60Subsampled(t *testing.T) {
	w := NewWindow(3, 60)
	if w.Count() != 0 {
		t.Errorf("empty window count: %d", w.Count())
	}
	for i := 0; i < 30; i++ {
		_ = w.Add([]float64{float64(i), 1.0, 0.5})
	}
	if w.Count() != 30 {
		t.Errorf("count after 30 adds: %d", w.Count())
	}
	for i := 0; i < 60; i++ {
		_ = w.Add([]float64{float64(i + 30), 1.0, 0.5})
	}
	if w.Count() != 60 {
		t.Errorf("count after capacity overflow: %d (should clamp to 60)", w.Count())
	}
}

// TestKappa_ThreeWayClassification — RULE-CPL-IDENT-02.
func TestKappa_ThreeWayClassification(t *testing.T) {
	tests := []struct {
		kappa float64
		want  SnapshotKind
	}{
		{50.0, KindHealthy},
		{HealthyKappaThreshold, KindHealthy},
		{HealthyKappaThreshold + 0.01, KindMarginal},
		{1000.0, KindMarginal},
		{UnidentifiableKappaThreshold, KindMarginal},
		{UnidentifiableKappaThreshold + 0.01, KindUnidentifiable},
		{1e6, KindUnidentifiable},
		{math.Inf(1), KindUnidentifiable},
		{math.NaN(), KindUnidentifiable},
	}
	for _, tc := range tests {
		got := ClassifyKappa(tc.kappa)
		if got != tc.want {
			t.Errorf("ClassifyKappa(%v): got %s, want %s", tc.kappa, got, tc.want)
		}
	}
}

// TestPearson_CoVaryingFansDetected — RULE-CPL-IDENT-03.
func TestPearson_CoVaryingFansDetected(t *testing.T) {
	// d = 1 + NCoupled (=3) + 1 = 5. Columns [T, pwm1, pwm2, pwm3, load].
	// Fan-PWM columns at indices [1, 4).
	// pwm1 == pwm2 (perfect Y-cable). pwm3 independent.
	w := NewWindow(5, 60)
	for i := 0; i < 60; i++ {
		f := float64(i)
		_ = w.Add([]float64{
			50.0 + f*0.1,  // T
			100.0 + f,     // pwm1
			100.0 + f,     // pwm2 (identical to pwm1)
			120.0 - f*0.5, // pwm3 (anti-correlated; not co-varying with pwm1)
			0.5,           // load
		})
	}
	pairs := w.FindCoVaryingPairs(3)
	found := false
	for _, p := range pairs {
		if (p[0] == 1 && p[1] == 2) || (p[0] == 2 && p[1] == 1) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FindCoVaryingPairs: did not detect pwm1==pwm2 (got %v)", pairs)
	}
	// Should NOT also flag pwm1==pwm3 (anti-correlated, not co-varying).
	for _, p := range pairs {
		if (p[0] == 1 && p[1] == 3) || (p[0] == 3 && p[1] == 1) {
			t.Errorf("FindCoVaryingPairs: false-positive on anti-correlated pwm1↔pwm3")
		}
	}
}

// TestKappa_IndependentColumnsHealthy — sanity check that
// zero-mean independent columns produce a reasonable κ.
//
// The window holds raw φ rows; ΦᵀΦ is the *uncentered* second
// moment, which is dominated by column means when they're
// non-zero. This test uses centered input ([-50, 50]) so the
// κ value reflects column independence rather than bias
// dominance.
func TestKappa_IndependentColumnsHealthy(t *testing.T) {
	w := NewWindow(3, 60)
	rng := newDeterministicRNG(12345)
	for i := 0; i < 60; i++ {
		_ = w.Add([]float64{
			rng.Float64()*100 - 50,
			rng.Float64()*100 - 50,
			rng.Float64()*100 - 50,
		})
	}
	kappa := w.Kappa()
	if kappa > UnidentifiableKappaThreshold {
		t.Errorf("κ = %v on zero-mean independent random columns is unidentifiable", kappa)
	}
}
