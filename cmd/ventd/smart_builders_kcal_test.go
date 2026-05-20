package main

import (
	"os"
	"path/filepath"
	"testing"

	acrunner "github.com/ventd/ventd/internal/acoustic/runner"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
)

// TestLoadKCalOffset_MissingFileReturnsZero exercises the no-mic
// fallback: when k_cal.json is absent, the offset is zero and
// buildAcousticBudget's CurrentDBA stays in within-host au — strict
// no-regression contract for hosts that haven't run mic-calibrate.
// (#1281)
func TestLoadKCalOffset_MissingFileReturnsZero(t *testing.T) {
	orig := kCalPath
	t.Cleanup(func() { kCalPath = orig })
	kCalPath = filepath.Join(t.TempDir(), "does-not-exist.json")

	if got := loadKCalOffset(); got != 0 {
		t.Errorf("loadKCalOffset missing file = %v, want 0", got)
	}
	if micCalibrated() {
		t.Errorf("micCalibrated missing file = true, want false")
	}
}

// TestLoadKCalOffset_ParsesPersistedKCal verifies that a calibration
// record produced by the `ventd calibrate --acoustic` runner is read
// back and the K_cal offset (in dB) is threaded into the acoustic
// budget. (#1281)
func TestLoadKCalOffset_ParsesPersistedKCal(t *testing.T) {
	orig := kCalPath
	t.Cleanup(func() { kCalPath = orig })
	dir := t.TempDir()
	kCalPath = filepath.Join(dir, "k_cal.json")

	rec := acrunner.Result{
		MicDevice:     "hw:CARD=USB,DEV=0",
		MicID:         "abc123",
		RefSPL:        94.0,
		Seconds:       30,
		RawDBFS:       -45.0,
		AWeightedDBFS: -50.0,
		KCalOffset:    139.0,
	}
	if err := acrunner.WriteResultJSON(kCalPath, rec); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if got := loadKCalOffset(); got != 139.0 {
		t.Errorf("loadKCalOffset = %v, want 139.0", got)
	}
	if !micCalibrated() {
		t.Errorf("micCalibrated = false, want true")
	}
}

// TestBuildAcousticBudget_AppliesKCalToCurrentDBA wires the offset
// through buildAcousticBudget. The compose result + K_cal lands in
// AcousticBudget.CurrentDBA, which the controller feeds into
// EvalDBABudget — so the dBA-gate operates in true dBA on calibrated
// hosts and within-host au on uncalibrated hosts. (#1281)
func TestBuildAcousticBudget_AppliesKCalToCurrentDBA(t *testing.T) {
	orig := kCalPath
	t.Cleanup(func() { kCalPath = orig })
	dir := t.TempDir()
	kCalPath = filepath.Join(dir, "k_cal.json")

	// Stage a fan with a sysfs-readable RPM path so the budget
	// builder enters the Compose path.
	rpmDir := t.TempDir()
	rpmPath := filepath.Join(rpmDir, "fan1_input")
	if err := os.WriteFile(rpmPath, []byte("1500\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	live := &config.Config{
		Smart: config.SmartConfig{Preset: "balanced"},
		Fans: []config.Fan{
			{Name: "case_fan", Type: "hwmon", RPMPath: rpmPath, MinPWM: 80, MaxPWM: 255},
		},
	}

	// Uncalibrated baseline.
	uncalib := buildAcousticBudget(live, "case_fan", controller.PresetBalanced)
	if uncalib.Target <= 0 {
		t.Fatalf("balanced target should be > 0, got %v", uncalib.Target)
	}
	if uncalib.CurrentDBA <= 0 {
		t.Fatalf("uncalibrated CurrentDBA should be > 0 (compose-only), got %v", uncalib.CurrentDBA)
	}

	// Now stage K_cal — same fan, same RPM, but the offset must lift
	// CurrentDBA by exactly KCalOffset dB.
	rec := acrunner.Result{MicDevice: "fake", KCalOffset: 50.0}
	if err := acrunner.WriteResultJSON(kCalPath, rec); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	calib := buildAcousticBudget(live, "case_fan", controller.PresetBalanced)
	delta := calib.CurrentDBA - uncalib.CurrentDBA
	if delta < 49.9 || delta > 50.1 {
		t.Errorf("calibrated CurrentDBA - uncalibrated = %v, want ~50.0", delta)
	}
}
