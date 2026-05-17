package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedCalibrateCheckpoint writes a CalibrateArtifact for verify
// dependency.
func seedCalibrateCheckpoint(t *testing.T, rc *RunContext, art CalibrateArtifact) {
	t.Helper()
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(art)
	state.Outcomes[(CalibratePhase{}).Name()] = Outcome{
		Phase:    (CalibratePhase{}).Name(),
		Status:   StatusSuccess,
		Artifact: raw,
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
}

// stageFanSysfs creates pwm + fan_input under a temp dir for verify tests.
type fanFixture struct {
	pwmInitial uint8
	rpmAfter   int // RPM value verify will read after writing full-speed
}

func stageFan(t *testing.T, dir string, idx int, f fanFixture) (pwmPath, rpmPath string) {
	t.Helper()
	pwmPath = filepath.Join(dir, "pwm"+itoaInt(idx))
	rpmPath = filepath.Join(dir, "fan"+itoaInt(idx)+"_input")
	if err := os.WriteFile(pwmPath, []byte(itoaInt(int(f.pwmInitial))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rpmPath, []byte(itoaInt(f.rpmAfter)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return pwmPath, rpmPath
}

func itoaInt(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

func TestVerifyPhase_Name(t *testing.T) {
	if (VerifyPhase{}).Name() != "verify" {
		t.Error("Name() must be 'verify'")
	}
}

func TestVerifyPhase_FanWithRPMAdmitted(t *testing.T) {
	dir := t.TempDir()
	pwm, rpm := stageFan(t, dir, 1, fanFixture{pwmInitial: 128, rpmAfter: 1500})

	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, RPMPath: rpm, LabelHint: "F1"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "normal"}},
	})

	out := (VerifyPhase{
		SettleDuration: 10 * time.Millisecond,
		SampleCount:    2,
		SampleInterval: 5 * time.Millisecond,
	}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	var art VerifyArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 1 || art.Results[0].Phantom {
		t.Errorf("RPM>0 fan should be admitted (non-phantom); got %+v", art)
	}
}

func TestVerifyPhase_AllZeroSamplesMarksPhantom(t *testing.T) {
	dir := t.TempDir()
	pwm, rpm := stageFan(t, dir, 1, fanFixture{pwmInitial: 128, rpmAfter: 0})

	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, RPMPath: rpm, LabelHint: "F1"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "normal"}},
	})

	out := (VerifyPhase{
		SettleDuration: 10 * time.Millisecond,
		SampleCount:    2,
		SampleInterval: 5 * time.Millisecond,
	}).Execute(context.Background(), rc)
	var art VerifyArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if !art.Results[0].Phantom {
		t.Errorf("all-zero-RPM samples should mark as phantom; got %+v", art.Results[0])
	}
	if art.Results[0].ReclassifiedFrom != "normal" {
		t.Errorf("ReclassifiedFrom should record original polarity; got %q", art.Results[0].ReclassifiedFrom)
	}
}

func TestVerifyPhase_RestoresOriginalPWMOnExit(t *testing.T) {
	dir := t.TempDir()
	pwm, rpm := stageFan(t, dir, 1, fanFixture{pwmInitial: 128, rpmAfter: 1500})

	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, RPMPath: rpm, LabelHint: "F1"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "normal"}},
	})

	_ = (VerifyPhase{
		SettleDuration: 10 * time.Millisecond,
		SampleCount:    1,
		SampleInterval: 5 * time.Millisecond,
	}).Execute(context.Background(), rc)

	got, err := os.ReadFile(pwm)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "128" {
		t.Errorf("verify must restore original PWM; got %q want 128", string(got))
	}
}

func TestVerifyPhase_PhantomPolaritySkipped(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: "/p1", RPMPath: "/r1", LabelHint: "F1"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: "/p1", Polarity: "phantom"}},
	})
	out := (VerifyPhase{}).Execute(context.Background(), rc)
	var art VerifyArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if art.Results[0].Skipped == "" || !art.Results[0].Phantom {
		t.Errorf("phantom fan should be skipped + recorded phantom: %+v", art.Results[0])
	}
}

func TestVerifyPhase_InvertedPolarityWritesRaw0(t *testing.T) {
	// An inverted fan takes full-speed at raw 0. The verify must
	// write raw 0 (not raw 255) — otherwise the fan stays stopped
	// and gets incorrectly marked phantom.
	dir := t.TempDir()
	pwm, rpm := stageFan(t, dir, 1, fanFixture{pwmInitial: 100, rpmAfter: 1500})

	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, RPMPath: rpm, LabelHint: "F1"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "inverted"}},
	})

	out := (VerifyPhase{
		SettleDuration: 5 * time.Millisecond,
		SampleCount:    1,
		SampleInterval: 1 * time.Millisecond,
	}).Execute(context.Background(), rc)
	var art VerifyArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if art.Results[0].Phantom {
		t.Errorf("inverted fan with RPM>0 must be admitted (polarity-aware verify); got phantom")
	}
}

func TestVerifyPhase_CalSkippedFanCarriesForward(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: "/p1", LabelHint: "F1"}},
	})
	seedCalibrateCheckpoint(t, rc, CalibrateArtifact{
		Results: []CalibrateFanResult{
			{PWMPath: "/p1", SkippedWhy: "polarity=phantom"},
		},
	})
	out := (VerifyPhase{}).Execute(context.Background(), rc)
	var art VerifyArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if !art.Results[0].Phantom || art.Results[0].Skipped == "" {
		t.Errorf("calibration-skipped fan should be phantom + skipped in verify; got %+v", art.Results[0])
	}
}
