package controller

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempAttr(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveEmergencyEngageC_FromCrit(t *testing.T) {
	dir := t.TempDir()
	writeTempAttr(t, dir, "temp1_crit", "100000") // 100 °C throttle point
	c := &Controller{logger: slog.Default(), tjmaxFn: func() float64 { return 0 }}
	got := c.resolveEmergencyEngageC(filepath.Join(dir, "temp1_input"))
	if got != 100.0+emergencyMarginC {
		t.Errorf("engage = %.1f, want %.1f (crit 100 + margin)", got, 100.0+emergencyMarginC)
	}
}

// TestResolveEmergencyEngageC_TjmaxFallbackForCPULabel is the case that matters
// on super-I/O-only boxes (the audit's NCT6687D HIL host): the CPU control
// sensor exposes no tempN_crit, so the failsafe falls back to the CPU-model
// Tjmax, gated on a CPU-ish hwmon label. RULE-CTRL-OVERTEMP-FAILSAFE.
func TestResolveEmergencyEngageC_TjmaxFallbackForCPULabel(t *testing.T) {
	dir := t.TempDir()
	writeTempAttr(t, dir, "temp1_label", "CPU") // no crit; CPU-labelled
	c := &Controller{logger: slog.Default(), tjmaxFn: func() float64 { return 100 }}
	got := c.resolveEmergencyEngageC(filepath.Join(dir, "temp1_input"))
	if got != 100.0+emergencyMarginC {
		t.Errorf("engage = %.1f, want %.1f (Tjmax 100 + margin via CPU label)", got, 100.0+emergencyMarginC)
	}
}

func TestResolveEmergencyEngageC_NoThresholdWhenNoCritAndNonCPULabel(t *testing.T) {
	dir := t.TempDir()
	writeTempAttr(t, dir, "temp1_label", "System") // non-CPU, no crit
	c := &Controller{logger: slog.Default(), tjmaxFn: func() float64 { return 100 }}
	if got := c.resolveEmergencyEngageC(filepath.Join(dir, "temp1_input")); got != 0 {
		t.Errorf("engage = %.1f, want 0 (no crit + non-CPU label → failsafe disabled, not a guessed absolute)", got)
	}
}

func TestResolveEmergencyEngageC_RejectsGarbageCrit(t *testing.T) {
	dir := t.TempDir()
	writeTempAttr(t, dir, "temp1_crit", "0")    // nct6687 thermistor garbage pattern
	writeTempAttr(t, dir, "temp1_label", "PCH") // non-CPU
	c := &Controller{logger: slog.Default(), tjmaxFn: func() float64 { return 0 }}
	if got := c.resolveEmergencyEngageC(filepath.Join(dir, "temp1_input")); got != 0 {
		t.Errorf("engage = %.1f, want 0 (crit=0 is implausible → rejected, non-CPU label)", got)
	}
}

func TestResolveEmergencyEngageC_CappedAtEmergency(t *testing.T) {
	dir := t.TempDir()
	writeTempAttr(t, dir, "temp1_crit", "100000")      // 100 °C → engage would be 104
	writeTempAttr(t, dir, "temp1_emergency", "102000") // 102 °C shutdown line
	c := &Controller{logger: slog.Default(), tjmaxFn: func() float64 { return 0 }}
	if got := c.resolveEmergencyEngageC(filepath.Join(dir, "temp1_input")); got != 102.0 {
		t.Errorf("engage = %.1f, want 102 (capped at _emergency shutdown line)", got)
	}
}

// TestOvertempForce_DebounceAndHysteresis drives the engage/release state
// machine: a transient spike must not engage (debounce), and once engaged the
// failsafe holds full speed through the hysteresis band until the sensor falls
// a release margin below the engage temp for the debounce dwell.
func TestOvertempForce_DebounceAndHysteresis(t *testing.T) {
	// engage=104 (e.g. Tjmax 100 + 4); release band floor = 104-6 = 98.
	c := &Controller{logger: slog.Default(), emergencyEngageC: 104, emergencyResolvedFor: "cpu"}
	t0 := time.Unix(1_000_000, 0)
	step := func(temp float64, dt time.Duration) bool {
		return c.overtempForce("cpu", "", temp, t0.Add(dt))
	}

	if step(90, 0) {
		t.Fatal("90 °C: must not engage below threshold")
	}
	if step(105, 1*time.Second) {
		t.Fatal("105 °C @1s: must not engage before debounce dwell")
	}
	if step(105, 2*time.Second) {
		t.Fatal("105 °C @2s: still within debounce dwell")
	}
	if !step(105, 4*time.Second) {
		t.Fatal("105 °C @4s: debounce elapsed — must engage")
	}
	if !step(100, 5*time.Second) {
		t.Fatal("100 °C: inside hysteresis band (>98) — must stay engaged")
	}
	if !step(97, 6*time.Second) {
		t.Fatal("97 °C @6s: below release temp but release debounce not elapsed — must stay engaged")
	}
	if step(97, 10*time.Second) {
		t.Fatal("97 °C @10s: below release temp for the debounce dwell — must release")
	}
}
