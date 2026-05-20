package orchestrator

import (
	"testing"
)

// TestInflectionAnchorPcts_PreservesEndpoints — the first anchor is
// always the fan's StartPWM% and the last the saturation knee%,
// regardless of how the middle band is distributed. (#1284)
func TestInflectionAnchorPcts_PreservesEndpoints(t *testing.T) {
	curve := []CalibrateCurvePoint{
		{PWM: 50, RPM: 100},
		{PWM: 100, RPM: 800},
		{PWM: 150, RPM: 1500},
		{PWM: 200, RPM: 1800},
		{PWM: 255, RPM: 1900},
	}
	got := inflectionAnchorPcts(curve, 50, 20, 80, 6)
	if got[0] != 20 {
		t.Errorf("anchor[0] = %d, want 20 (bottomPct)", got[0])
	}
	if got[5] != 80 {
		t.Errorf("anchor[5] = %d, want 80 (topPct)", got[5])
	}
}

// TestInflectionAnchorPcts_LinearCurveCollapsesToLinear is the
// no-regression contract: a fan with a perfectly linear PWM→RPM
// response (J4125 mini-PC fans, some Noctua case fans) must receive
// a uniform PWM% distribution because equal-ΔRPM segments collapse
// to equal-ΔPWM. (#1284)
func TestInflectionAnchorPcts_LinearCurveCollapsesToLinear(t *testing.T) {
	// Synthesise a linear curve: RPM = 5 * PWM for PWM in [50, 255].
	curve := make([]CalibrateCurvePoint, 0, 256)
	for pwm := 50; pwm <= 255; pwm += 5 {
		curve = append(curve, CalibrateCurvePoint{PWM: uint8(pwm), RPM: 5 * pwm})
	}
	got := inflectionAnchorPcts(curve, 50, 20, 80, 5)
	// All four segments should be roughly equal — assert spacing is
	// monotone (no clustering).
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Errorf("non-monotone anchors: %v", got)
			return
		}
	}
	// Roughly uniform: each segment within ±5 percentage points.
	want := []int{20, 35, 50, 65, 80}
	for i, w := range want {
		diff := int(got[i]) - w
		if diff < -10 || diff > 10 {
			t.Errorf("linear-curve anchors %v, expected near %v (anchor %d diff = %d)",
				got, want, i, diff)
		}
	}
}

// TestInflectionAnchorPcts_SteepLowBandClustersAnchorsLow is the
// #1284 acceptance: a fan whose calibrate sweep is steep in the
// 100-150 PWM band and flat above must receive more anchors in the
// steep low band. We assert the median-anchor PWM% falls below
// linear-midpoint.
func TestInflectionAnchorPcts_SteepLowBandClustersAnchorsLow(t *testing.T) {
	// Synthesise a typical NCT6687 chassis-fan response: steep
	// rise from PWM=80 → PWM=140, then plateau.
	curve := []CalibrateCurvePoint{
		{PWM: 80, RPM: 200},
		{PWM: 100, RPM: 700},
		{PWM: 120, RPM: 1300},
		{PWM: 140, RPM: 1850},
		{PWM: 160, RPM: 2000},
		{PWM: 180, RPM: 2050},
		{PWM: 200, RPM: 2080},
		{PWM: 220, RPM: 2090},
		{PWM: 255, RPM: 2100},
	}
	got := inflectionAnchorPcts(curve, 80, 30, 90, 6)
	// Linear midpoint of 30..90 is 60. With steep low band, the
	// median anchor should sit below 60 — the curve is asking us to
	// invest anchor density in 30-55 (which maps to PWM 75-140
	// bytes), not 60-90.
	if got[3] >= 60 {
		t.Errorf("steep-low-band fan: anchor[3] = %d, expected < 60 (linear midpoint)", got[3])
	}
	// Endpoints still pinned.
	if got[0] != 30 || got[5] != 90 {
		t.Errorf("endpoints wrong: %v", got)
	}
}

// TestInflectionAnchorPcts_SparseCurveFallsBackToLinear pins the
// degraded-data path: with fewer than three monotonic points the
// function emits the v1 linear distribution rather than fabricating
// "inflection" from noise.
func TestInflectionAnchorPcts_SparseCurveFallsBackToLinear(t *testing.T) {
	curve := []CalibrateCurvePoint{
		{PWM: 50, RPM: 100},
		{PWM: 200, RPM: 1500},
	}
	got := inflectionAnchorPcts(curve, 50, 20, 80, 5)
	want := []uint8{20, 35, 50, 65, 80}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("sparse anchors[%d] = %d, want %d (linear fallback)", i, got[i], w)
		}
	}
}

// TestInflectionAnchorPcts_HandlesAnchorCountOfTwo guards against
// off-by-ones in the segment loop — a 2-anchor curve has no middle
// to distribute.
func TestInflectionAnchorPcts_HandlesAnchorCountOfTwo(t *testing.T) {
	curve := []CalibrateCurvePoint{
		{PWM: 50, RPM: 100},
		{PWM: 200, RPM: 1500},
	}
	got := inflectionAnchorPcts(curve, 50, 20, 80, 2)
	if got[0] != 20 || got[1] != 80 {
		t.Errorf("2-anchor curve: got %v, want [20 80]", got)
	}
}
