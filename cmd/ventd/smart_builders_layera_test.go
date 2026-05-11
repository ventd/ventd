package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/probe"
)

func silentLayerALogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestBuildLayerA_LoadChannelRestoresHistogram pins
// RULE-CONFA-WIRING-02: buildLayerAEstimator MUST call LoadChannel
// per channel after Admit, restoring the persisted bin histogram +
// first-contact flag from <stateDir>/smart/conf-A/.
//
// Without this wiring conf_A re-warms from zero on every daemon
// restart (issue #1035 row 2).
func TestBuildLayerA_LoadChannelRestoresHistogram(t *testing.T) {
	stateDir := t.TempDir()
	const fp = "fp-test"
	ch := "/sys/class/hwmon/hwmon0/pwm1"

	// Phase 1: produce a saved Bucket via a real Estimator that has
	// observed enough ticks to mark a bin "covered" (3 obs in the
	// same bin → coverage = 1/16).
	{
		est, err := layer_a.New(layer_a.Config{})
		if err != nil {
			t.Fatalf("layer_a.New: %v", err)
		}
		now := time.Now()
		if err := est.Admit(ch, 0, layer_a.DefaultNoiseFloor, now); err != nil {
			t.Fatalf("Admit: %v", err)
		}
		est.Observe(ch, 100, -1, 0, now)
		est.Observe(ch, 100, -1, 0, now.Add(time.Second))
		est.Observe(ch, 100, -1, 0, now.Add(2*time.Second))
		est.MarkFirstContact(ch, now.Add(3*time.Second))
		if err := est.Save(stateDir, fp); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	// Phase 2: a fresh estimator built via buildLayerAEstimator MUST
	// load the persisted state. The first-contact flag + bin counts
	// (and thus coverage) come back.
	channels := []*probe.ControllableChannel{{PWMPath: ch}}
	est := buildLayerAEstimator(channels, stateDir, fp, silentLayerALogger())
	if est == nil {
		t.Fatal("buildLayerAEstimator returned nil with one channel")
	}
	snap := est.Read(ch)
	if snap == nil {
		t.Fatal("Read returned nil after LoadChannel; bucket discarded?")
	}
	if !snap.SeenFirstContact {
		t.Errorf("SeenFirstContact = false after Load; want true (RULE-CONFA-FIRSTCONTACT-01)")
	}
	wantCoverage := 1.0 / float64(layer_a.NumBins)
	if snap.Coverage < wantCoverage-1e-9 {
		t.Errorf("Coverage = %v after Load; want >= %v", snap.Coverage, wantCoverage)
	}
}

// TestBuildLayerA_EmptyStateDirSkipsLoad: an empty stateDir disables
// persistence (test scaffolding path). Build still produces a fresh
// estimator with all channels admitted but zero coverage.
func TestBuildLayerA_EmptyStateDirSkipsLoad(t *testing.T) {
	channels := []*probe.ControllableChannel{
		{PWMPath: "/sys/class/hwmon/hwmon0/pwm1"},
	}
	est := buildLayerAEstimator(channels, "", "fp-test", silentLayerALogger())
	if est == nil {
		t.Fatal("buildLayerAEstimator returned nil")
	}
	snap := est.Read("/sys/class/hwmon/hwmon0/pwm1")
	if snap == nil {
		t.Fatal("Read returned nil after empty-stateDir Admit")
	}
	if snap.Coverage != 0 {
		t.Errorf("Coverage = %v on fresh estimator with empty stateDir; want 0", snap.Coverage)
	}
}
