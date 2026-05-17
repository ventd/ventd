package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
)

// fakeCalibrator is the test seam for CalibratePhase. Per-PWMPath
// configured results + an attempt log for assertions.
type fakeCalibrator struct {
	results  map[string]calibrate.Result
	errors   map[string]error
	attempts []string
}

func (f *fakeCalibrator) Calibrate(_ context.Context, fan *config.Fan) (calibrate.Result, error) {
	f.attempts = append(f.attempts, fan.PWMPath)
	if err, ok := f.errors[fan.PWMPath]; ok {
		return calibrate.Result{}, err
	}
	if r, ok := f.results[fan.PWMPath]; ok {
		return r, nil
	}
	return calibrate.Result{StartPWM: 80, MaxRPM: 1500}, nil
}

func TestCalibratePhase_Name(t *testing.T) {
	if (CalibratePhase{}).Name() != "calibrate" {
		t.Error("Name() must be 'calibrate'")
	}
}

func TestCalibratePhase_NoCalibratorWiredFails(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	out := (CalibratePhase{}).Execute(context.Background(), rc)
	if out.Status != StatusFailed {
		t.Errorf("missing Calibrator should fail; got %q", out.Status)
	}
}

func TestCalibratePhase_SweepsAllNonPhantomFans(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", LabelHint: "Fan 1"},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2", LabelHint: "Fan 2"},
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{
			{PWMPath: "/sys/hwmon0/pwm1", Polarity: "normal"},
			{PWMPath: "/sys/hwmon0/pwm2", Polarity: "phantom"},
		},
	})

	cal := &fakeCalibrator{
		results: map[string]calibrate.Result{
			"/sys/hwmon0/pwm1": {StartPWM: 60, MaxRPM: 1800, MinRPM: 800},
		},
	}
	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	if len(cal.attempts) != 1 || cal.attempts[0] != "/sys/hwmon0/pwm1" {
		t.Errorf("expected only non-phantom fan attempted; got %v", cal.attempts)
	}

	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 2 {
		t.Fatalf("artifact should record both fans; got %d", len(art.Results))
	}
	// Phantom one should carry SkippedWhy.
	var phantomEntry, sweepedEntry CalibrateFanResult
	for _, r := range art.Results {
		if r.PWMPath == "/sys/hwmon0/pwm2" {
			phantomEntry = r
		} else {
			sweepedEntry = r
		}
	}
	if phantomEntry.SkippedWhy == "" {
		t.Error("phantom fan should have SkippedWhy populated")
	}
	if sweepedEntry.StartPWM != 60 || sweepedEntry.MaxRPM != 1800 {
		t.Errorf("sweep result not captured: %+v", sweepedEntry)
	}
}

func TestCalibratePhase_PerFanErrorDoesNotFailPhase(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/p1", LabelHint: "Fan 1"},
			{Index: 2, PWMPath: "/p2", LabelHint: "Fan 2"},
		},
	})
	cal := &fakeCalibrator{
		errors:  map[string]error{"/p1": errors.New("watchdog timed out")},
		results: map[string]calibrate.Result{"/p2": {StartPWM: 50, MaxRPM: 1200}},
	}
	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("per-fan failures must not fail the phase; got %q", out.Status)
	}
	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 2 {
		t.Fatalf("len=%d", len(art.Results))
	}
	if art.Results[0].Error == "" || art.Results[1].StartPWM == 0 {
		t.Errorf("error vs success not recorded correctly: %+v", art.Results)
	}
}

func TestCalibratePhase_EmptyProbeArtifactSkips(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{Fans: nil})
	out := (CalibratePhase{Calibrator: &fakeCalibrator{}}).Execute(context.Background(), rc)
	if out.Status != StatusSkipped {
		t.Errorf("empty probe → Skipped, got %q", out.Status)
	}
}
