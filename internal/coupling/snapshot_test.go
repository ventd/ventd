package coupling

import (
	"math"
	"testing"
)

// RULE-CPL-CONF-01: A WarmingUp snapshot returns Confidence == 0
// regardless of the other fields. The warmup gate is the canonical
// "not yet trustworthy" signal — Layer-C / aggregator must see 0
// during warmup so the predictive controller stays disengaged.
func TestConfidence_WarmingUpReturnsZero(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		ChannelID: "/sys/class/hwmon/hwmon0/pwm1",
		Kind:      KindWarmup,
		Theta:     []float64{0.98, -0.5},
		NSamples:  1000,
		TrP:       0.1,
		Kappa:     1.0,
		WarmingUp: true,
	}
	if got := s.Confidence(); got != 0 {
		t.Fatalf("WarmingUp snapshot Confidence = %v, want 0", got)
	}
}

// RULE-CPL-CONF-02: A snapshot at the centre of the healthy region
// (κ≤100, tr(P)→0, NSamples ≥ 50) returns Confidence == 1.0.
// Verifies the four-term product is multiplicatively normalised.
func TestConfidence_HealthyShardReturnsOne(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Kind:      KindHealthy,
		Theta:     []float64{0.98, -0.5},
		NSamples:  100, // ≥ NMinR12 (50) ⇒ sample term = 1.0
		TrP:       0.0, // covariance term = 1.0
		Kappa:     50,  // identifiability = 1.0
		WarmingUp: false,
	}
	if got := s.Confidence(); got != 1.0 {
		t.Fatalf("healthy Confidence = %v, want 1.0", got)
	}
}

// RULE-CPL-CONF-03: identifiability_term tapers linearly in log10(κ)
// from 1.0 at κ=100 down to 0.0 at κ=1e4. Verify the boundaries plus
// a midpoint and explicit out-of-range cases.
func TestConfidence_KappaTaperLinearInLog10(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		kappa float64
		want  float64
		tol   float64
	}{
		{"low: κ=1 → 1.0", 1, 1.0, 1e-9},
		{"boundary: κ=100 → 1.0", 100, 1.0, 1e-9},
		{"midpoint: κ=1000 (log=3) → 0.5", 1000, 0.5, 1e-9},
		{"boundary: κ=1e4 → 0.0", 1e4, 0.0, 1e-9},
		{"high: κ=1e6 → 0.0", 1e6, 0.0, 1e-9},
		{"inf → 0.0", math.Inf(1), 0.0, 1e-9},
		{"NaN → 0.0", math.NaN(), 0.0, 1e-9},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := identifiabilityTerm(tc.kappa)
			if math.Abs(got-tc.want) > tc.tol {
				t.Fatalf("identifiabilityTerm(%v) = %v, want %v ± %v",
					tc.kappa, got, tc.want, tc.tol)
			}
		})
	}
}

// covariance_term sanity: TrP=0 → 1.0, TrP=TrPCap → 0.0, TrP=TrPCap/2 → 0.5.
func TestConfidence_CovarianceTermBoundary(t *testing.T) {
	t.Parallel()
	if got := covarianceTerm(0); got != 1.0 {
		t.Fatalf("trP=0: got %v, want 1.0", got)
	}
	if got := covarianceTerm(TrPCap); got != 0.0 {
		t.Fatalf("trP=TrPCap: got %v, want 0.0", got)
	}
	if got := covarianceTerm(TrPCap / 2); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("trP=TrPCap/2: got %v, want 0.5", got)
	}
	// TrP > TrPCap should saturate at 0, not go negative.
	if got := covarianceTerm(TrPCap * 10); got != 0.0 {
		t.Fatalf("trP=10·TrPCap: got %v, want 0.0", got)
	}
}

// sample_count_term: 0 samples → 0; NMinR12 samples → 1.0; >NMinR12
// saturates at 1.0.
func TestConfidence_SampleCountTermBoundary(t *testing.T) {
	t.Parallel()
	if got := sampleCountTerm(0); got != 0.0 {
		t.Fatalf("n=0: got %v, want 0.0", got)
	}
	if got := sampleCountTerm(NMinR12); got != 1.0 {
		t.Fatalf("n=NMinR12: got %v, want 1.0", got)
	}
	if got := sampleCountTerm(NMinR12 * 10); got != 1.0 {
		t.Fatalf("n=10·NMinR12: got %v, want 1.0 (saturated)", got)
	}
	if got := sampleCountTerm(NMinR12 / 2); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("n=NMinR12/2: got %v, want 0.5", got)
	}
}

// nil-receiver safety — controllers may pass nil snapshots when
// Layer-B has no shard for the channel yet.
func TestConfidence_NilReceiverReturnsZero(t *testing.T) {
	t.Parallel()
	var s *Snapshot
	if got := s.Confidence(); got != 0 {
		t.Fatalf("nil Snapshot.Confidence() = %v, want 0", got)
	}
}
