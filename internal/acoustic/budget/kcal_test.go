package budget

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	acrunner "github.com/ventd/ventd/internal/acoustic/runner"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
)

// TestLoadKCalOffset_MissingFileReturnsZero exercises the no-mic
// fallback: when k_cal.json is absent, the offset is zero and
// Build's CurrentDBA stays in within-host au — strict
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
// through Build. The compose result + K_cal lands in
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
	uncalib := Build(live, "case_fan", controller.PresetBalanced)
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

	calib := Build(live, "case_fan", controller.PresetBalanced)
	delta := calib.CurrentDBA - uncalib.CurrentDBA
	if delta < 49.9 || delta > 50.1 {
		t.Errorf("calibrated CurrentDBA - uncalibrated = %v, want ~50.0", delta)
	}
}

// TestLoadKCalOffset_MtimeCache exercises the R7b mtime/size gate that
// keeps loadKCalOffset off the open+read+unmarshal path on the hot
// per-tick acoustic budget. It pins the behaviour contract: the cache
// must (1) reload when a recalibration rewrites k_cal.json (no stale
// value), (2) serve the last-parsed value when the file is unchanged
// without re-reading it, and (3) invalidate on a missing file so a
// later write is picked up — the missing→present transition the
// per-tick builder depends on. (#1281)
func TestLoadKCalOffset_MtimeCache(t *testing.T) {
	orig := kCalPath
	t.Cleanup(func() { kCalPath = orig })
	dir := t.TempDir()
	kCalPath = filepath.Join(dir, "k_cal.json")

	write := func(offset float64, mtime time.Time) {
		t.Helper()
		rec := acrunner.Result{MicDevice: "fake", KCalOffset: offset}
		if err := acrunner.WriteResultJSON(kCalPath, rec); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		if err := os.Chtimes(kCalPath, mtime, mtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	// Cold load.
	t0 := time.Now().Add(-2 * time.Hour)
	write(10.0, t0)
	if got := loadKCalOffset(); got != 10.0 {
		t.Fatalf("cold read = %v, want 10.0", got)
	}

	// Recalibration: new offset + later mtime ⇒ reload, no stale value.
	t1 := t0.Add(time.Hour)
	write(20.0, t1)
	if got := loadKCalOffset(); got != 20.0 {
		t.Fatalf("post-recalibration read = %v, want 20.0 (stale cache?)", got)
	}

	// Cache hit: corrupt the bytes but preserve size + mtime so the gate
	// still matches. A cached read must return 20.0 without re-parsing
	// the now-invalid file — proving the per-tick path serves the memo.
	validBytes, err := os.ReadFile(kCalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kCalPath, bytes.Repeat([]byte("X"), len(validBytes)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(kCalPath, t1, t1); err != nil {
		t.Fatal(err)
	}
	if got := loadKCalOffset(); got != 20.0 {
		t.Errorf("cache-hit read = %v, want cached 20.0 (re-parsed corrupted file?)", got)
	}

	// Missing file ⇒ 0 and cache invalidated; a later write is picked up.
	if err := os.Remove(kCalPath); err != nil {
		t.Fatal(err)
	}
	if got := loadKCalOffset(); got != 0 {
		t.Errorf("missing-file read = %v, want 0", got)
	}
	write(30.0, t1.Add(time.Hour))
	if got := loadKCalOffset(); got != 30.0 {
		t.Errorf("re-created-file read = %v, want 30.0", got)
	}
}
