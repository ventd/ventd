package stall

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

// gaussianNoise returns n samples of normalised white noise with the
// given amplitude scale. Deterministic via a seeded math/rand so tests
// are reproducible.
func gaussianNoise(n int, amplitude float64, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = amplitude * r.NormFloat64()
	}
	return out
}

// burstSignal mixes a low-amplitude Gaussian floor with periodic
// transients. Used to simulate stalled-fan bursty noise.
func burstSignal(n int, floorAmp, burstAmp float64, burstSpacing int, seed int64) []float64 {
	out := gaussianNoise(n, floorAmp, seed)
	for i := burstSpacing / 2; i < n; i += burstSpacing {
		// Single-sample positive burst at burstAmp.
		out[i] += burstAmp
	}
	return out
}

// TestRULE_STALL_01_DefaultThresholdsLocked binds RULE-STALL-01: the
// canonical R31 thresholds (6 dB / 2.0 / 1.5 / 2-of-3) are the
// DefaultConfig values and a sample-rate of 48 kHz / window of 3 s.
func TestRULE_STALL_01_DefaultThresholdsLocked(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.BroadbandRiseDB != 6.0 {
		t.Errorf("BroadbandRiseDB = %v, want 6.0", cfg.BroadbandRiseDB)
	}
	if cfg.CrestFactorExcess != 2.0 {
		t.Errorf("CrestFactorExcess = %v, want 2.0", cfg.CrestFactorExcess)
	}
	if cfg.KurtosisExcess != 1.5 {
		t.Errorf("KurtosisExcess = %v, want 1.5", cfg.KurtosisExcess)
	}
	if cfg.GateThreshold != 2 {
		t.Errorf("GateThreshold = %d, want 2 (2-of-3 gate)", cfg.GateThreshold)
	}
	if cfg.SampleRate != 48000 {
		t.Errorf("SampleRate = %v, want 48000 Hz", cfg.SampleRate)
	}
	if cfg.WindowSeconds != 3.0 {
		t.Errorf("WindowSeconds = %v, want 3.0 s", cfg.WindowSeconds)
	}
}

// TestRULE_STALL_02_TwoOfThreeGateFires binds RULE-STALL-02: the gate
// fires when at least 2 of 3 criteria exceed their thresholds.
// Exhaustive truth table over the 8 firing combinations.
func TestRULE_STALL_02_TwoOfThreeGateFires(t *testing.T) {
	cfg := DefaultConfig()
	healthy := Features{BroadbandDB: -30, CrestFactor: 3.0, Kurtosis: 0.0}

	cases := []struct {
		name           string
		broadbandDelta float64
		crestDelta     float64
		kurtosisDelta  float64
		wantFire       bool
	}{
		{"none_fires", 0, 0, 0, false},
		{"broadband_only", 7, 0, 0, false},       // 1-of-3
		{"crest_only", 0, 2.5, 0, false},         // 1-of-3
		{"kurtosis_only", 0, 0, 2.0, false},      // 1-of-3
		{"broadband_and_crest", 7, 2.5, 0, true}, // 2-of-3 ✓
		{"broadband_and_kurt", 7, 0, 2.0, true},  // 2-of-3 ✓
		{"crest_and_kurt", 0, 2.5, 2.0, true},    // 2-of-3 ✓
		{"all_three", 7, 2.5, 2.0, true},         // 3-of-3 ✓
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			current := Features{
				BroadbandDB: healthy.BroadbandDB + tc.broadbandDelta,
				CrestFactor: healthy.CrestFactor + tc.crestDelta,
				Kurtosis:    healthy.Kurtosis + tc.kurtosisDelta,
			}
			r := Evaluate(healthy, current, cfg)
			if r.StallSuspected != tc.wantFire {
				t.Errorf("%s: StallSuspected = %v, want %v (fired %v/%v/%v)",
					tc.name, r.StallSuspected, tc.wantFire,
					r.FiredBroadband, r.FiredCrest, r.FiredKurtosis)
			}
		})
	}
}

// TestRULE_STALL_03_HealthyFixturePassesWithoutAlert binds RULE-STALL-03:
// a healthy-fan synthetic fixture (Gaussian noise at the same level as
// the reference) does NOT trip the gate.
func TestRULE_STALL_03_HealthyFixturePassesWithoutAlert(t *testing.T) {
	cfg := DefaultConfig()

	// 3-second window at 48 kHz = 144000 samples. Gaussian noise
	// has crest factor ≈ 3.5 and kurtosis ≈ 0 — matches the
	// healthy reference within statistical noise.
	healthySamples := gaussianNoise(48000*3, 0.05, 42)
	currentSamples := gaussianNoise(48000*3, 0.05, 1337) // different seed, same statistics

	healthy := Extract(healthySamples)
	current := Extract(currentSamples)

	r := Evaluate(healthy, current, cfg)
	if r.StallSuspected {
		t.Errorf("healthy fixture tripped gate: rise=%.2f crest_ex=%.3f kurt_ex=%.3f",
			r.BroadbandRise, r.CrestExcess, r.KurtosisExcess)
	}
}

// TestRULE_STALL_04_AdvisoryOnlyContract binds RULE-STALL-04: the
// detector's API surface is purely informational — Result has no
// fan-write / abort / disable methods, and the package exports no
// symbol that would alter control behaviour.
//
// Static-API contract test: introspects the Result type and asserts
// it has only the expected informational fields. A future regression
// that adds an "AbortFan" or "RefuseWrite" method to Result will fail
// this test.
func TestRULE_STALL_04_AdvisoryOnlyContract(t *testing.T) {
	rt := reflect.TypeOf(Result{})

	// Allowlist of legitimate Result fields. Anything outside this
	// set indicates a contract widening that needs explicit review.
	allowed := map[string]bool{
		"StallSuspected": true,
		"BroadbandRise":  true,
		"CrestExcess":    true,
		"KurtosisExcess": true,
		"FiredBroadband": true,
		"FiredCrest":     true,
		"FiredKurtosis":  true,
	}

	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if !allowed[name] {
			t.Errorf("Result has unexpected field %q — advisory-only contract widened. "+
				"If this is intentional, update the allowlist + rule text.", name)
		}
	}

	// Result MUST have no methods beyond the implicit zero-method
	// struct. Adding methods would suggest the detector controls
	// something downstream — outside the advisory contract.
	if rt.NumMethod() != 0 {
		t.Errorf("Result has %d method(s); advisory-only contract requires zero methods",
			rt.NumMethod())
	}
}

// TestRULE_STALL_05_BurstTransientsTripGate binds RULE-STALL-05: the
// canonical "stalled-fan synthetic fixture" (low Gaussian floor +
// periodic high-amplitude bursts) trips at least 2-of-3 criteria
// against a healthy Gaussian-noise reference, validating that the
// feature extractor sees the stall signature.
func TestRULE_STALL_05_BurstTransientsTripGate(t *testing.T) {
	cfg := DefaultConfig()

	// Healthy: pure Gaussian noise.
	healthySamples := gaussianNoise(48000*3, 0.05, 7)
	healthy := Extract(healthySamples)

	// Stalled: same-amplitude Gaussian floor + periodic 0.8-amplitude
	// bursts every ~50 ms (2400 samples at 48 kHz). The bursts spike
	// peak relative to RMS (crest factor surge), spike kurtosis
	// (4th-moment heavy tail), and raise broadband RMS slightly.
	stalledSamples := burstSignal(48000*3, 0.05, 0.8, 2400, 13)
	stalled := Extract(stalledSamples)

	r := Evaluate(healthy, stalled, cfg)
	if !r.StallSuspected {
		t.Errorf("stalled-fan fixture did not trip 2-of-3 gate: "+
			"rise=%.2f (≥%.1f? %v), crest_ex=%.3f (≥%.1f? %v), kurt_ex=%.3f (≥%.1f? %v)",
			r.BroadbandRise, cfg.BroadbandRiseDB, r.FiredBroadband,
			r.CrestExcess, cfg.CrestFactorExcess, r.FiredCrest,
			r.KurtosisExcess, cfg.KurtosisExcess, r.FiredKurtosis)
	}
}

// TestExtract_EmptyInputReturnsSilenceFloor pins the boundary case
// where Extract is called with an empty sample slice. BroadbandDB
// floors at -120 dBFS to match RMSdBFS's silence convention; the
// other fields are zero-valued.
func TestExtract_EmptyInputReturnsSilenceFloor(t *testing.T) {
	f := Extract(nil)
	if f.BroadbandDB != -120 {
		t.Errorf("Extract(nil).BroadbandDB = %v, want -120 (silence floor)", f.BroadbandDB)
	}
	if f.CrestFactor != 0 {
		t.Errorf("Extract(nil).CrestFactor = %v, want 0", f.CrestFactor)
	}
	if f.Kurtosis != 0 {
		t.Errorf("Extract(nil).Kurtosis = %v, want 0", f.Kurtosis)
	}

	// Also exercise the all-zeros (silent) path explicitly.
	f2 := Extract(make([]float64, 1000))
	if f2.BroadbandDB != -120 {
		t.Errorf("Extract(silence).BroadbandDB = %v, want -120", f2.BroadbandDB)
	}
}

// TestExtract_GaussianNoiseStatistics pins that Extract on a known
// distribution returns features close to the analytical expectation.
// Catches regressions in the moment computation.
func TestExtract_GaussianNoiseStatistics(t *testing.T) {
	samples := gaussianNoise(48000*3, 0.1, 99)
	f := Extract(samples)

	// RMS of N(0, 0.1) ≈ 0.1; 20·log10(0.1) = -20 dB. Allow ±1 dB
	// for finite-sample noise.
	if math.Abs(f.BroadbandDB-(-20)) > 1.0 {
		t.Errorf("Gaussian RMS dB = %v, want ≈ -20 ±1", f.BroadbandDB)
	}
	// Crest factor for Gaussian over 144000 samples is ≈ 4-5
	// (probability of |x| > 4σ within the window ≈ 1-erf(4/√2) ≈ 6.3e-5,
	// times 144000 samples ≈ 9 expected outliers above 4σ). Allow
	// 3.0 to 6.0.
	if f.CrestFactor < 3.0 || f.CrestFactor > 6.0 {
		t.Errorf("Gaussian crest factor = %v, want roughly [3, 6]", f.CrestFactor)
	}
	// Excess kurtosis for Gaussian is 0. Allow ±0.5 for finite
	// sample noise on N=144000.
	if math.Abs(f.Kurtosis) > 0.5 {
		t.Errorf("Gaussian excess kurtosis = %v, want ≈ 0 ±0.5", f.Kurtosis)
	}
}
