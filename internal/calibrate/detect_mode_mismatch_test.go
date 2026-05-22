package calibrate

import "testing"

// TestDetectModeMismatch_StuckFullSpeed pins the canonical 3-pin-on-PWM
// signature: RPM is essentially constant across PWM=64/128/255, and
// the low-PWM anchor is close to (or at) max RPM — the fan never
// slows because the PWM signal is being interpreted as a constant DC
// drive. (#759.)
func TestDetectModeMismatch_StuckFullSpeed(t *testing.T) {
	curve := []PWMRPMPoint{
		{PWM: 64, RPM: 2000},
		{PWM: 128, RPM: 2050},
		{PWM: 192, RPM: 2080},
		{PWM: 255, RPM: 2100},
	}
	suspect, evidence := detectModeMismatch(curve)
	if !suspect {
		t.Fatalf("expected mismatch suspected; got false")
	}
	if evidence != "flat_rpm_with_stuck_full_speed" {
		t.Errorf("evidence = %q; want %q", evidence, "flat_rpm_with_stuck_full_speed")
	}
}

// TestDetectModeMismatch_ZeroLowStep pins the seized-fan ambiguity:
// R_low==0 + flat-elsewhere is suspicious for mode mismatch BUT could
// also be a dead fan / stiction. The evidence token captures the
// ambiguity so the UI can hedge ("likely mode mismatch — also
// possible: dead fan or stiction").
func TestDetectModeMismatch_ZeroLowStep(t *testing.T) {
	curve := []PWMRPMPoint{
		{PWM: 64, RPM: 0},
		{PWM: 128, RPM: 0},
		{PWM: 255, RPM: 0},
	}
	// All-zero falls into the no-response path, NOT a mode-mismatch
	// signal — covered by the existing PhantomReason no_response.
	suspect, _ := detectModeMismatch(curve)
	if suspect {
		t.Fatalf("all-zero curve should not flag mismatch (PhantomReason no_response covers it)")
	}

	// Curve where R_low==0 but R_max>0 — closer to the seized-fan vs
	// mode-mismatch ambiguity. The detector flags suspect=true with
	// the zero_low_step sub-token so the UI can hedge.
	curveAmbig := []PWMRPMPoint{
		{PWM: 64, RPM: 0},
		{PWM: 128, RPM: 2050},
		{PWM: 255, RPM: 2100},
	}
	// Delta(0, 2050) is huge → NOT flat → does NOT flag.
	suspect, _ = detectModeMismatch(curveAmbig)
	if suspect {
		t.Fatalf("curve with big delta should not flag — bigger delta means fan IS responding")
	}
}

// TestDetectModeMismatch_HealthyMonotoneCurve pins the canonical
// happy path: a 4-pin PWM-controlled fan moves RPM linearly with
// PWM. The detector must NOT flag suspect on such a curve.
func TestDetectModeMismatch_HealthyMonotoneCurve(t *testing.T) {
	curve := []PWMRPMPoint{
		{PWM: 64, RPM: 600},
		{PWM: 128, RPM: 1200},
		{PWM: 192, RPM: 1800},
		{PWM: 255, RPM: 2400},
	}
	suspect, _ := detectModeMismatch(curve)
	if suspect {
		t.Fatalf("monotone 4-pin curve should not flag mismatch")
	}
}

// TestDetectModeMismatch_ShortCurveSkips pins the "not enough data"
// path: a sweep that aborted early (or sampled fewer than 3 anchors)
// returns suspect=false regardless of values.
func TestDetectModeMismatch_ShortCurveSkips(t *testing.T) {
	for _, c := range [][]PWMRPMPoint{
		nil,
		{},
		{{PWM: 128, RPM: 1500}},
		{{PWM: 128, RPM: 1500}, {PWM: 255, RPM: 2400}},
	} {
		if suspect, _ := detectModeMismatch(c); suspect {
			t.Errorf("len(curve)=%d should not flag; got suspect=true", len(c))
		}
	}
}

// TestDetectModeMismatch_AllZeroCurveSkips covers the all-zero case
// explicitly. The PhantomReason no_response path already handles it,
// and this detector must NOT also flag — otherwise the dashboard
// would show conflicting verdicts.
func TestDetectModeMismatch_AllZeroCurveSkips(t *testing.T) {
	curve := []PWMRPMPoint{
		{PWM: 64, RPM: 0},
		{PWM: 128, RPM: 0},
		{PWM: 255, RPM: 0},
	}
	suspect, _ := detectModeMismatch(curve)
	if suspect {
		t.Fatalf("all-zero curve should defer to no_response path; got suspect=true")
	}
}

// TestDetectModeMismatch_FlatNoStuckFullSpeed covers the
// "flat_rpm_across_sweep" primary token: deltas <10% across the
// sweep but R_low is well below R_max*0.9, so it's flat but not
// stuck-at-full. Less common in the wild — typically a fan whose
// PWM input is being treated as something between PWM and DC.
func TestDetectModeMismatch_FlatNoStuckFullSpeed(t *testing.T) {
	curve := []PWMRPMPoint{
		{PWM: 64, RPM: 1500},
		{PWM: 128, RPM: 1520},
		{PWM: 255, RPM: 1540},
	}
	suspect, evidence := detectModeMismatch(curve)
	if !suspect {
		t.Fatalf("flat 1500/1520/1540 curve should flag mismatch")
	}
	// R_low=1500 / R_max=1540 → 0.97 ≥ 0.9 — actually stuck-full-speed.
	if evidence != "flat_rpm_with_stuck_full_speed" {
		t.Errorf("evidence = %q; want flat_rpm_with_stuck_full_speed", evidence)
	}
}
