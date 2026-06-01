package orchestrator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ventd/ventd/internal/calibrate"
)

// modeHealEnv seeds a single mode-mismatch fan and returns the artifact
// from running CalibratePhase with the given mode-heal policy. The
// fakeCalibrator's first Calibrate flags ModeMismatchSuspected; a heal
// (when attempted and configured) returns the recovered DC-mode result.
func modeHealEnv(t *testing.T, modeWritable bool, configureHeal bool) (*fakeCalibrator, CalibrateArtifact) {
	t.Helper()
	rc := &RunContext{StateDir: t.TempDir()}
	const pwm = "/sys/hwmon0/pwm1"
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, ChipName: "nct6775", LabelHint: "CPU Fan"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "normal"}},
	})

	cal := &fakeCalibrator{
		results: map[string]calibrate.Result{
			pwm: {ModeMismatchSuspected: true, ModeMismatchEvidence: "flat_rpm_with_stuck_full_speed", MaxRPM: 1500, MinRPM: 1450},
		},
	}
	if configureHeal {
		cal.healResults = map[string]calibrate.Result{
			pwm: {
				ModeHealed:           true,
				ResolvedPWMMode:      "dc",
				ModeMismatchEvidence: "self_healed_dc_mode",
				StartPWM:             60,
				MaxRPM:               1800,
				MinRPM:               400,
				Curve:                []calibrate.PWMRPMPoint{{PWM: 64, RPM: 400}, {PWM: 255, RPM: 1800}},
			},
		}
	}

	phase := CalibratePhase{
		Calibrator:         cal,
		ModeWritableDriver: func(chip string) bool { return modeWritable && chip == "nct6775" },
	}
	out := phase.Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	var art CalibrateArtifact
	if err := json.Unmarshal(out.Artifact, &art); err != nil {
		t.Fatalf("unmarshal artifact: %v", err)
	}
	return cal, art
}

// TestCalibratePhase_ModeHealRunsAndRecovers: a writable driver +
// successful heal must rewrite the entry from the recovered DC-mode
// sweep — ModeHealed set, ResolvedPWMMode="dc", ModeMismatchSuspected
// cleared, and the responsive curve carried.
func TestCalibratePhase_ModeHealRunsAndRecovers(t *testing.T) {
	cal, art := modeHealEnv(t, true, true)

	if len(cal.healAttempts) != 1 {
		t.Fatalf("expected exactly one heal attempt, got %v", cal.healAttempts)
	}
	if len(art.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(art.Results))
	}
	r := art.Results[0]
	if !r.ModeHealed || r.ResolvedPWMMode != "dc" {
		t.Fatalf("entry not healed: ModeHealed=%v ResolvedPWMMode=%q", r.ModeHealed, r.ResolvedPWMMode)
	}
	if r.ModeMismatchSuspected {
		t.Fatal("healed entry must clear ModeMismatchSuspected")
	}
	if r.MaxRPM != 1800 || r.StartPWM != 60 {
		t.Fatalf("entry must reflect the DC-mode sweep, got MaxRPM=%d StartPWM=%d", r.MaxRPM, r.StartPWM)
	}
	if len(r.Curve) != 2 {
		t.Fatalf("entry must carry the recovered curve, got %d points", len(r.Curve))
	}
}

// TestCalibratePhase_ModeHealSkippedWhenDriverNotWritable: the policy
// gate must keep the self-heal from ever firing on a non-writable
// driver — the entry stays ModeMismatchSuspected for BIOS surfacing.
func TestCalibratePhase_ModeHealSkippedWhenDriverNotWritable(t *testing.T) {
	cal, art := modeHealEnv(t, false, true)

	if len(cal.healAttempts) != 0 {
		t.Fatalf("heal must NOT be attempted on a non-writable driver, got %v", cal.healAttempts)
	}
	r := art.Results[0]
	if !r.ModeMismatchSuspected || r.ModeHealed {
		t.Fatalf("non-writable entry must stay ModeMismatchSuspected and unhealed, got %+v", r)
	}
}

// TestCalibratePhase_ModeHealUnsuccessfulKeepsVerdict: a writable driver
// whose heal reports "didn't help" leaves the original flat verdict in
// place (ApplyPhase then surfaces BIOS guidance).
func TestCalibratePhase_ModeHealUnsuccessfulKeepsVerdict(t *testing.T) {
	cal, art := modeHealEnv(t, true, false) // heal attempted, but no healResult → (·,false,nil)

	if len(cal.healAttempts) != 1 {
		t.Fatalf("expected one heal attempt, got %v", cal.healAttempts)
	}
	r := art.Results[0]
	if r.ModeHealed {
		t.Fatal("entry must not be marked healed when the heal didn't help")
	}
	if !r.ModeMismatchSuspected {
		t.Fatal("entry must retain ModeMismatchSuspected for BIOS surfacing")
	}
}
