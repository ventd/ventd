package controller

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/probe"
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
	// Pre-seed the "cpu" sensor's resolved threshold so overtempForce skips the
	// sysfs resolve (sensorPath is "").
	c := &Controller{logger: slog.Default(), emergency: map[string]*emergencyState{
		"cpu": {engageC: 104, resolved: true},
	}}
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

// TestTick_OvertempFailsafeEndToEnd drives the REAL control tick against a
// fake hwmon fan and asserts the full over-temperature failsafe chain
// (RULE-CTRL-OVERTEMP-FAILSAFE): the debounce holds off a false-fire on the
// first over-temp tick, then once the over-temp dwell exceeds emergencyDebounce
// the failsafe forces PWM=255 — overriding the operator max_pwm cap — and on
// cool-down releases back to curve control. Unlike the resolveEmergencyEngageC
// unit tests (threshold math only) and computePWM (stubbed over-temp fn), this
// exercises tick() → overtempForce → the post-clamp 255 override end to end,
// the actual byte written to the fan.
func TestTick_OvertempFailsafeEndToEnd(t *testing.T) {
	ff := newFakeFan(t)
	chipDir := filepath.Dir(ff.tempPath)
	// crit 90 °C → engage 90 + emergencyMarginC. No Tjmax fallback.
	writeTempAttr(t, chipDir, "temp1_crit", "90000")
	const cap = 200 // operator max_pwm cap the failsafe must bypass
	engageC := 90.0 + emergencyMarginC

	cfg := makeLinearCurveCfg(ff, "cpu", "cpu_curve", 40, cap)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu", "cpu_curve")
	c.tjmaxFn = func() float64 { return 0 } // crit is the only threshold source

	// 1) Below engage: normal curve control, capped at max_pwm. Arms the failsafe.
	writeTempAttr(t, chipDir, "temp1_input", "60000")
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got == 255 {
		t.Fatalf("PWM=255 at 60 °C — failsafe must not arm-fire below engage")
	}

	// 2) Spike above engage, single tick: debounce must hold, so the write is
	// still the capped curve value (255 curve clamped to max_pwm=200), NOT 255.
	writeTempAttr(t, chipDir, "temp1_input", "95000") // > engageC, curve wants 255
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != cap {
		t.Errorf("first over-temp tick: PWM=%d, want %d (cap; debounce must hold off the failsafe)", got, cap)
	}

	// 3) Backdate the over-temp dwell past emergencyDebounce, tick again:
	// failsafe engages and forces full speed, bypassing the max_pwm cap.
	c.emergency["cpu"].overSince = time.Now().Add(-emergencyDebounce - time.Second)
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != 255 {
		t.Errorf("engaged failsafe: PWM=%d, want 255 (forced full speed bypassing max_pwm=%d)", got, cap)
	}
	if !c.emergency["cpu"].engaged {
		t.Error("emergencyEngaged=false after engagement")
	}
	_ = engageC

	// 4) Cool below the release margin, backdate the under-temp dwell, tick:
	// failsafe releases and returns to capped curve control.
	writeTempAttr(t, chipDir, "temp1_input", "60000") // well below engage − release
	c.tick()                                          // observes cool, starts underTempSince
	c.emergency["cpu"].underSince = time.Now().Add(-emergencyDebounce - time.Second)
	c.tick()
	if c.emergency["cpu"].engaged {
		t.Error("failsafe still engaged after cool-down + dwell; expected release")
	}
	if got := readPWMByte(t, ff.pwmPath); got == 255 {
		t.Errorf("after release: PWM=255, want curve/cap control (≤ %d)", cap)
	}
}

// TestTick_InvertedPolarityClosedLoopWritesFlippedByte drives a full control
// tick against a fake hwmon fan through the REAL hwmon backend with an
// inverted-polarity channel, and asserts the byte that actually lands on the
// fan file is flipped (255 − curve). RULE-POLARITY-11 is pinned at the
// writeWithRetry boundary with a fake backend; this closes the loop end to end
// — sensor → curve → polarity.WritePWM → real backend → sysfs byte — because a
// wrong-direction write is the most dangerous control bug (full speed when it
// should idle, or idle when it should cool).
func TestTick_InvertedPolarityClosedLoopWritesFlippedByte(t *testing.T) {
	ff := newFakeFan(t)
	// 60 °C on a linear 40→80 / 0→255 curve → 128 logical PWM.
	writeTempAttr(t, filepath.Dir(ff.tempPath), "temp1_input", "60000")
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 0, 255)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
	// Inverted channel — the real backend must receive 255 − logical.
	c.polarityCh = &probe.ControllableChannel{PWMPath: ff.pwmPath, Polarity: "inverted"}

	c.tick()

	got := readPWMByte(t, ff.pwmPath)
	// linear(60,40,80,0,255)=127.5→128 logical; inverted → 255-128 = 127.
	if got != 127 {
		t.Errorf("inverted closed-loop write = %d, want 127 (255 − 128); a raw write would land 128", got)
	}
}

// TestTick_OvertempFailsafeNoFalseFireAtThrottlePoint pins the other half of
// RULE-CTRL-OVERTEMP-FAILSAFE: a chip that operates AT its crit/throttle point
// by design (common — CPUs throttle at Tjmax under sustained load) must NEVER
// trip the failsafe, because engage = crit + emergencyMarginC. Even with the
// over-temp dwell backdated far past the debounce, a temp at-or-below the
// throttle point (but below engage) must keep curve control, not scream the
// fans to full speed forever. A regression here is a noise/usability disaster
// that looks like a "stuck at 100%" bug.
func TestTick_OvertempFailsafeNoFalseFireAtThrottlePoint(t *testing.T) {
	ff := newFakeFan(t)
	chipDir := filepath.Dir(ff.tempPath)
	writeTempAttr(t, chipDir, "temp1_crit", "90000") // throttle 90 °C → engage 94 °C
	cfg := makeLinearCurveCfg(ff, "cpu", "cpu_curve", 40, 200)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu", "cpu_curve")
	c.tjmaxFn = func() float64 { return 0 }

	// Sit at 92 °C — above the 90 °C throttle point, but below the 94 °C engage.
	writeTempAttr(t, chipDir, "temp1_input", "92000")
	c.tick()
	// Even if the dwell somehow accrued, the temp never reaches engage, so the
	// failsafe must stay disengaged. Backdate to prove it's the threshold, not
	// the debounce, holding it off.
	c.emergency["cpu"].overSince = time.Now().Add(-emergencyDebounce - time.Second)
	c.tick()

	if c.emergency["cpu"].engaged {
		t.Error("failsafe engaged at 92 °C (throttle 90, engage 94) — must not fire on a chip hot by design")
	}
	if got := readPWMByte(t, ff.pwmPath); got == 255 {
		t.Errorf("PWM=255 at 92 °C below engage — false over-temp fire (fans stuck at full speed)")
	}
}
