package cooling

import (
	"math"
	"testing"
)

// TestChassisCapacityW_ReferenceFanGives30W pins the calibration
// reference: a single 120 mm fan at 1500 RPM (≈ NF-A12x25 envelope)
// returns ~30 W. The whole estimator hangs off this anchor — if it
// drifts, every relative output drifts with it.
func TestChassisCapacityW_ReferenceFanGives30W(t *testing.T) {
	got := ChassisCapacityW([]FanInput{
		{Class: "case_120_140", DiameterMM: 120, MaxRPM: 1500},
	})
	if math.Abs(got-30) > 0.01 {
		t.Errorf("reference fan = %v W, want ~30 W", got)
	}
}

// TestChassisCapacityW_LargerFansContributeMore — a 140 mm fan at
// the same RPM has more capacity than a 120 mm fan (the (d/120)²
// scaling), and a 200 mm fan more again.
func TestChassisCapacityW_LargerFansContributeMore(t *testing.T) {
	c120 := ChassisCapacityW([]FanInput{
		{Class: "case_120_140", DiameterMM: 120, MaxRPM: 1500},
	})
	c140 := ChassisCapacityW([]FanInput{
		{Class: "case_120_140", DiameterMM: 140, MaxRPM: 1500},
	})
	c200 := ChassisCapacityW([]FanInput{
		{Class: "case_200", DiameterMM: 200, MaxRPM: 1500},
	})
	if c140 <= c120 {
		t.Errorf("140mm (%v) should exceed 120mm (%v) at same RPM", c140, c120)
	}
	if c200 <= c140 {
		t.Errorf("200mm (%v) should exceed 140mm (%v) at same RPM", c200, c140)
	}
}

// TestChassisCapacityW_PumpsContributeZero — AIO pumps move coolant
// through a loop, not chassis air. The estimator must not credit
// their RPM as airflow capacity.
func TestChassisCapacityW_PumpsContributeZero(t *testing.T) {
	got := ChassisCapacityW([]FanInput{
		{Class: "aio_pump", DiameterMM: 50, MaxRPM: 2700},
	})
	if got != 0 {
		t.Errorf("pump capacity = %v W, want 0", got)
	}
}

// TestChassisCapacityW_LaptopBlowerMuchLessThanCaseFan — a laptop
// blower at the same RPM as a 120 mm case fan must produce far less
// estimated capacity (small + radial + warm ambient).
func TestChassisCapacityW_LaptopBlowerMuchLessThanCaseFan(t *testing.T) {
	caseFan := ChassisCapacityW([]FanInput{
		{Class: "case_120_140", DiameterMM: 120, MaxRPM: 1500},
	})
	blower := ChassisCapacityW([]FanInput{
		{Class: "laptop_blower", DiameterMM: 50, MaxRPM: 4500},
	})
	if blower >= caseFan {
		t.Errorf("blower=%v should be << caseFan=%v", blower, caseFan)
	}
}

// TestChassisCapacityW_UnknownClassFallsBackToCaseDefault — an
// unrecognised class string must not crash and must produce a
// reasonable estimate via the case_120_140 fallback. (#1285)
func TestChassisCapacityW_UnknownClassFallsBackToCaseDefault(t *testing.T) {
	mystery := ChassisCapacityW([]FanInput{
		{Class: "totally_new_class", DiameterMM: 120, MaxRPM: 1500},
	})
	defaultCase := ChassisCapacityW([]FanInput{
		{Class: "case_120_140", DiameterMM: 120, MaxRPM: 1500},
	})
	if mystery != defaultCase {
		t.Errorf("unknown-class capacity = %v, want %v", mystery, defaultCase)
	}
}

// TestChassisCapacityW_NoFansReturnsZero — empty input is a clean
// no-op.
func TestChassisCapacityW_NoFansReturnsZero(t *testing.T) {
	if got := ChassisCapacityW(nil); got != 0 {
		t.Errorf("nil input = %v, want 0", got)
	}
	if got := ChassisCapacityW([]FanInput{}); got != 0 {
		t.Errorf("empty input = %v, want 0", got)
	}
}

// TestChassisCapacityW_UncalibratedFanContributesNothing — a fan
// without a MaxRPM measurement (calibrate skipped, sensor offline)
// shouldn't be credited as airflow.
func TestChassisCapacityW_UncalibratedFanContributesNothing(t *testing.T) {
	got := ChassisCapacityW([]FanInput{
		{Class: "case_120_140", DiameterMM: 120, MaxRPM: 0},
	})
	if got != 0 {
		t.Errorf("uncalibrated fan = %v, want 0", got)
	}
}

// TestCapacityAdequate verifies the gate's hysteresis: capacity must
// exceed CPU TDP by 25 % to count as adequate. Edge cases — zero
// signal in either input — return (true, false) so the doctor
// stays silent on hosts where the data isn't available yet.
func TestCapacityAdequate(t *testing.T) {
	tests := []struct {
		name             string
		capacityW, tdpW  float64
		wantAdequate, wantSignal bool
	}{
		{"comfortable_overhead", 200, 125, true, true},
		{"under_25pct_margin", 130, 125, false, true},
		{"way_under_chassis_tight", 80, 125, false, true},
		{"no_tdp_signal", 200, 0, true, false},
		{"no_capacity_signal", 0, 125, true, false},
		{"neither_signal", 0, 0, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adq, sig := CapacityAdequate(tc.capacityW, tc.tdpW)
			if adq != tc.wantAdequate || sig != tc.wantSignal {
				t.Errorf("CapacityAdequate(%v, %v) = (%v, %v), want (%v, %v)",
					tc.capacityW, tc.tdpW, adq, sig, tc.wantAdequate, tc.wantSignal)
			}
		})
	}
}
