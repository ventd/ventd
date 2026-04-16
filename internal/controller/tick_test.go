package controller

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/watchdog"
)

// stubCal implements CalibrationChecker. The active map is consulted at
// IsCalibrating time, so tests can flip a fan into or out of "calibrating"
// state between ticks.
type stubCal struct {
	active map[string]bool
}

func (s *stubCal) IsCalibrating(pwmPath string) bool {
	if s == nil || s.active == nil {
		return false
	}
	return s.active[pwmPath]
}

// fakeFan builds an in-tmpdir hwmon channel: a pwmN file, a pwmN_enable
// file, and a temp1_input file. The returned struct gives the test the
// paths it needs to assert on.
type fakeFan struct {
	pwmPath    string
	enablePath string
	tempPath   string
}

func newFakeFan(t *testing.T) fakeFan {
	t.Helper()
	dir := t.TempDir()
	pwmPath := filepath.Join(dir, "pwm1")
	enablePath := filepath.Join(dir, "pwm1_enable")
	tempPath := filepath.Join(dir, "temp1_input")
	for _, p := range []struct{ path, content string }{
		{pwmPath, "0\n"},
		{enablePath, "2\n"},
		{tempPath, "60000\n"},
	} {
		if err := os.WriteFile(p.path, []byte(p.content), 0o600); err != nil {
			t.Fatalf("write %s: %v", p.path, err)
		}
	}
	return fakeFan{pwmPath: pwmPath, enablePath: enablePath, tempPath: tempPath}
}

// readPWMByte reads the current PWM value written to the fixture file.
func readPWMByte(t *testing.T, path string) uint8 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 8)
	if err != nil {
		t.Fatalf("parse %s = %q: %v", path, data, err)
	}
	return uint8(v)
}

// silentLogger returns a slog.Logger that drops every message. Used to
// keep test output clean while still exercising the controller's logging
// paths.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestController wires a Controller around a fixture fan. The watchdog
// is real but never registered against the fixture (the controller's
// constructor needs a non-nil watchdog only for panic recovery).
func newTestController(t *testing.T, ff fakeFan, cfg *config.Config, cal CalibrationChecker, fanName, curveName string) *Controller {
	t.Helper()
	logger := silentLogger()
	wd := watchdog.New(logger)
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	return New(fanName, curveName, ff.pwmPath, "hwmon", cfgPtr, wd, cal, logger)
}

// makeLinearCurveCfg builds a Config containing one sensor at tempPath,
// one fan at pwmPath, one linear curve, and a Control binding them. The
// helper is the smallest config shape that exercises a full tick().
func makeLinearCurveCfg(ff fakeFan, fanName, curveName string, fanMin, fanMax uint8) *config.Config {
	return &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: fanName, Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: fanMin, MaxPWM: fanMax,
		}},
		Curves: []config.CurveConfig{{
			Name: curveName, Type: "linear", Sensor: "cpu",
			MinTemp: 40, MaxTemp: 80, MinPWM: 0, MaxPWM: 255,
		}},
		Controls: []config.Control{{Fan: fanName, Curve: curveName}},
	}
}

// TestTick_ClampClipsCurveOutputBelowMinPWM pins the safety lower bound:
// at temp=45°C the linear curve outputs ~32, but with fan.MinPWM=80 the
// write must be 80, never 32. This is the contract that prevents a misset
// curve from stalling a fan at idle.
func TestTick_ClampClipsCurveOutputBelowMinPWM(t *testing.T) {
	ff := newFakeFan(t)
	if err := os.WriteFile(ff.tempPath, []byte("45000\n"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 80, 255)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
	c.tick()

	if got := readPWMByte(t, ff.pwmPath); got != 80 {
		t.Errorf("PWM after tick = %d, want 80 (clamped to MinPWM)", got)
	}
}

// TestTick_ClampClipsCurveOutputAboveMaxPWM pins the safety upper bound:
// at temp=120°C the linear curve clamps to 255, but with fan.MaxPWM=200
// the write must be 200, never 255. Used for fans that scream at full
// speed and the operator caps them.
func TestTick_ClampClipsCurveOutputAboveMaxPWM(t *testing.T) {
	ff := newFakeFan(t)
	if err := os.WriteFile(ff.tempPath, []byte("120000\n"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
	c.tick()

	if got := readPWMByte(t, ff.pwmPath); got != 200 {
		t.Errorf("PWM after tick = %d, want 200 (clamped to MaxPWM)", got)
	}
}

// TestTick_PassthroughInsideRange covers the common case: at 60°C with a
// linear 40→80°C / 0→255 PWM curve, the curve outputs ~127. With fan
// limits 40–200 that passes through unchanged.
func TestTick_PassthroughInsideRange(t *testing.T) {
	ff := newFakeFan(t) // temp1_input pre-set to 60000
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
	c.tick()

	got := readPWMByte(t, ff.pwmPath)
	// linear(60, 40,80, 0,255) = 127.5 → round to 128
	if got != 128 {
		t.Errorf("PWM after tick = %d, want 128 (curve passthrough)", got)
	}
}

// TestTick_PumpFloorEnforcedViaMinPWM pins the pump-fan safety contract.
// The controller doesn't own a pump-specific code path — buildConfig sets
// Fan.MinPWM = max(stopPWM, PumpMinimum). At runtime the standard clamp
// then enforces "pump never drops below PumpMinimum".
//
// Test: pump fan with MinPWM=80 (>= MinPumpPWM=20). At 45°C (curve output
// ~32), the write must be the pump floor (80), not the curve value.
func TestTick_PumpFloorEnforcedViaMinPWM(t *testing.T) {
	ff := newFakeFan(t)
	if err := os.WriteFile(ff.tempPath, []byte("45000\n"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "pump", Type: "hwmon", PWMPath: ff.pwmPath,
			IsPump: true, PumpMinimum: 80,
			MinPWM: 80, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{{
			Name: "cpu_curve", Type: "linear", Sensor: "cpu",
			MinTemp: 40, MaxTemp: 80, MinPWM: 0, MaxPWM: 255,
		}},
		Controls: []config.Control{{Fan: "pump", Curve: "cpu_curve"}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "pump", "cpu_curve")
	c.tick()

	if got := readPWMByte(t, ff.pwmPath); got != 80 {
		t.Errorf("pump PWM = %d, want 80 (pump floor enforced via clamp)", got)
	}
}

// TestTick_ManualOverrideBeatsCurve pins the manual-mode contract: when
// Control.ManualPWM is set, the fan tracks that value regardless of
// sensor reads. Also verifies the manual value is itself clamped to
// the fan's MinPWM/MaxPWM.
func TestTick_ManualOverrideBeatsCurve(t *testing.T) {
	ff := newFakeFan(t) // temp 60°C
	manual := uint8(150)
	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 40, MaxPWM: 200,
		}},
		Curves: []config.CurveConfig{{
			Name: "cpu_curve", Type: "linear", Sensor: "cpu",
			MinTemp: 40, MaxTemp: 80, MinPWM: 0, MaxPWM: 255,
		}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve", ManualPWM: &manual}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
	c.tick()

	if got := readPWMByte(t, ff.pwmPath); got != 150 {
		t.Errorf("PWM = %d, want 150 (manual override)", got)
	}
}

// TestTick_ManualOverrideClampedToFanLimits pins the safety contract on
// the manual-mode write: even an explicitly-set ManualPWM value cannot
// escape [MinPWM, MaxPWM].
func TestTick_ManualOverrideClampedToFanLimits(t *testing.T) {
	ff := newFakeFan(t)
	manual := uint8(250)
	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 40, MaxPWM: 200,
		}},
		Curves:   []config.CurveConfig{{Name: "cpu_curve", Type: "fixed", Value: 100}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve", ManualPWM: &manual}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
	c.tick()

	if got := readPWMByte(t, ff.pwmPath); got != 200 {
		t.Errorf("PWM = %d, want 200 (manual clamped to MaxPWM)", got)
	}
}

// TestTick_ClearingManualReturnsToCurve covers the bind-rebind transition.
// First tick with manual=180 → fan tracks 180. Second tick after the
// Control.ManualPWM is cleared → fan tracks the curve again.
func TestTick_ClearingManualReturnsToCurve(t *testing.T) {
	ff := newFakeFan(t) // temp 60°C → curve outputs ~128
	manual := uint8(180)
	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 40, MaxPWM: 200,
		}},
		Curves: []config.CurveConfig{{
			Name: "cpu_curve", Type: "linear", Sensor: "cpu",
			MinTemp: 40, MaxTemp: 80, MinPWM: 0, MaxPWM: 255,
		}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve", ManualPWM: &manual}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != 180 {
		t.Errorf("manual tick: PWM = %d, want 180", got)
	}

	// Clear ManualPWM and store a new config snapshot atomically.
	cfg.Controls[0].ManualPWM = nil
	c.cfg.Store(cfg)

	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != 128 {
		t.Errorf("post-clear curve tick: PWM = %d, want ~128", got)
	}
}

// TestTick_YieldsWhenCalibrationInProgress is the load-bearing sentinel-
// cooperation test. While calibrate.Manager is running a sweep on this
// PWM path, the controller must yield — every PWM write during a sweep
// would race the sentinel's 2s zero-PWM watchdog.
//
// Setup: pre-write a non-zero PWM, mark IsCalibrating(pwmPath)=true, then
// fire a tick that would otherwise write something else. The PWM file
// must be unchanged after the tick returns.
func TestTick_YieldsWhenCalibrationInProgress(t *testing.T) {
	ff := newFakeFan(t)
	// Pre-stamp the PWM with a sentinel value the tick must not overwrite.
	if err := os.WriteFile(ff.pwmPath, []byte("123\n"), 0o600); err != nil {
		t.Fatalf("seed pwm: %v", err)
	}
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 255)
	cal := &stubCal{active: map[string]bool{ff.pwmPath: true}}
	c := newTestController(t, ff, cfg, cal, "cpu fan", "cpu_curve")

	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != 123 {
		t.Errorf("PWM = %d, want 123 (tick should have yielded to calibration)", got)
	}

	// Once calibration finishes, the next tick takes over again.
	cal.active[ff.pwmPath] = false
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got == 123 {
		t.Errorf("PWM still 123 after calibration cleared; tick should have written")
	}
}

// TestTick_FanNotInLiveConfigSkips pins the silent-skip contract for the
// "fan was just removed from config" race. The controller's pwmPath is
// stable but the live config no longer mentions it; tick() must log a
// warning and not write anything (no spurious PWM resets).
func TestTick_FanNotInLiveConfigSkips(t *testing.T) {
	ff := newFakeFan(t)
	if err := os.WriteFile(ff.pwmPath, []byte("99\n"), 0o600); err != nil {
		t.Fatalf("seed pwm: %v", err)
	}
	// Config has no Fans at all; controller is for "ghost fan".
	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "ghost", "cpu_curve")
	c.tick()

	if got := readPWMByte(t, ff.pwmPath); got != 99 {
		t.Errorf("PWM changed despite missing fan config: got %d, want 99", got)
	}
}

// TestTick_CurveNotInLiveConfigSkips pins the symmetric "curve removed
// from live config" branch — silent skip, no write.
func TestTick_CurveNotInLiveConfigSkips(t *testing.T) {
	ff := newFakeFan(t)
	if err := os.WriteFile(ff.pwmPath, []byte("99\n"), 0o600); err != nil {
		t.Fatalf("seed pwm: %v", err)
	}
	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 40, MaxPWM: 255,
		}},
		// No Curves, but Controls names one — simulates a half-applied edit.
		Controls: []config.Control{{Fan: "cpu fan", Curve: "ghost_curve"}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "ghost_curve")
	c.tick()

	if got := readPWMByte(t, ff.pwmPath); got != 99 {
		t.Errorf("PWM changed despite missing curve: got %d, want 99", got)
	}
}

// TestTick_BadCurveBuildSkips covers the case where findCurve succeeds
// but buildCurve fails (invalid mix function). The tick must skip the
// write rather than panic or write a bogus value.
func TestTick_BadCurveBuildSkips(t *testing.T) {
	ff := newFakeFan(t)
	if err := os.WriteFile(ff.pwmPath, []byte("77\n"), 0o600); err != nil {
		t.Fatalf("seed pwm: %v", err)
	}
	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 40, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{{
			Name: "broken_curve", Type: "mix", Function: "BOGUS_FUNC", Sources: nil,
		}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "broken_curve"}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "broken_curve")
	c.tick()

	if got := readPWMByte(t, ff.pwmPath); got != 77 {
		t.Errorf("PWM changed despite curve build failure: got %d, want 77", got)
	}
}

// TestTick_SensorReadFailureFallsBackToMaxPWM pins the "loud-on-data-loss"
// safety: when the curve's sensor is missing from the readings (sensor
// failed earlier in the tick), curve.Linear returns its MaxPWM. With fan
// limits matching, the fan ends up at full speed — never silently
// stalling on a flaky sensor.
func TestTick_SensorReadFailureFallsBackToMaxPWM(t *testing.T) {
	ff := newFakeFan(t)
	// Delete the sensor input so readAllSensors fails for it.
	if err := os.Remove(ff.tempPath); err != nil {
		t.Fatalf("rm temp: %v", err)
	}
	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 40, MaxPWM: 200,
		}},
		Curves: []config.CurveConfig{{
			Name: "cpu_curve", Type: "linear", Sensor: "cpu",
			MinTemp: 40, MaxTemp: 80, MinPWM: 50, MaxPWM: 255,
		}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
	c.tick()

	// Linear with absent sensor → MaxPWM (255). Fan clamps to its MaxPWM (200).
	if got := readPWMByte(t, ff.pwmPath); got != 200 {
		t.Errorf("PWM on sensor failure = %d, want 200 (loud fallback)", got)
	}
}
