package controller

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/watchdog"
)

// readIntFile is a small helper for the *_enable / fan_target assertions.
func readIntFile(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse %s = %q: %v", path, data, err)
	}
	return v
}

// runWithTimeout starts c.Run with a short tick interval and cancels it
// after wait. Returns Run's error so tests can pin shutdown semantics.
// The interval is intentionally < wait so at least one tick fires.
func runWithTimeout(t *testing.T, c *Controller, interval, wait time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- c.Run(ctx, interval) }()
	time.Sleep(wait)
	cancel()
	select {
	case err := <-errCh:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancel")
		return nil
	}
}

// TestRun_AcquiresManualPWMControl pins the documented startup contract:
// when Run starts, it sets pwm_enable=1 (manual control) before the first
// tick so the curve's writes aren't immediately overwritten by the BIOS
// auto-control loop.
func TestRun_AcquiresManualPWMControl(t *testing.T) {
	ff := newFakeFan(t) // pwm_enable preset to "2" (auto)
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	if err := runWithTimeout(t, c, 50*time.Millisecond, 80*time.Millisecond); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := readIntFile(t, ff.enablePath); got != 1 {
		t.Errorf("pwm_enable = %d, want 1 (manual control acquired)", got)
	}
}

// TestRun_NoPWMEnableFileTolerated pins the "driver without pwm_enable"
// path. The nct6683 driver backing NCT6687D doesn't expose pwm_enable;
// Run must log and continue rather than error out.
func TestRun_NoPWMEnableFileTolerated(t *testing.T) {
	ff := newFakeFan(t)
	if err := os.Remove(ff.enablePath); err != nil {
		t.Fatalf("rm enable: %v", err)
	}
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	if err := runWithTimeout(t, c, 30*time.Millisecond, 60*time.Millisecond); err != nil {
		t.Errorf("Run errored on missing pwm_enable; want graceful continue: %v", err)
	}
}

// TestRun_ContextCancelReturnsCleanly pins the shutdown contract: when
// the parent ctx is cancelled, Run returns nil (a normal stop, not an
// error) so main.go can sequence orderly shutdown without false alarms.
func TestRun_ContextCancelReturnsCleanly(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	err := runWithTimeout(t, c, 30*time.Millisecond, 50*time.Millisecond)
	if err != nil {
		t.Errorf("Run returned %v on ctx cancel, want nil", err)
	}
}

// TestRun_RPMTargetUsesCompanionEnablePath pins the rpm_target startup
// contract: an AMD GPU fan_target channel takes manual control via the
// companion pwmN_enable file (because fanN_target_enable doesn't exist).
//
// The test fixture provides both fan1_target and pwm1_enable in the
// same dir so the controller's RPMTargetEnablePath() helper resolves
// to a real file.
func TestRun_RPMTargetUsesCompanionEnablePath(t *testing.T) {
	dir := t.TempDir()
	fanTarget := filepath.Join(dir, "fan1_target")
	pwmEnable := filepath.Join(dir, "pwm1_enable")
	fanInput := filepath.Join(dir, "fan1_input")
	tempInput := filepath.Join(dir, "temp1_input")
	for _, f := range []struct{ p, c string }{
		{fanTarget, "1500\n"},
		{pwmEnable, "2\n"},
		{fanInput, "1500\n"},
		{tempInput, "60000\n"},
	} {
		if err := os.WriteFile(f.p, []byte(f.c), 0o600); err != nil {
			t.Fatalf("seed %s: %v", f.p, err)
		}
	}

	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: tempInput}},
		Fans: []config.Fan{{
			Name: "gpu fan", Type: "hwmon", PWMPath: fanTarget,
			ControlKind: "rpm_target",
			MinPWM:      40, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{{
			Name: "gpu_curve", Type: "fixed", Value: 200,
		}},
		Controls: []config.Control{{Fan: "gpu fan", Curve: "gpu_curve"}},
	}
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	wd := watchdog.New(silentLogger())
	c := New("gpu fan", "gpu_curve", fanTarget, "hwmon", cfgPtr, wd, &stubCal{}, silentLogger())

	if err := runWithTimeout(t, c, 50*time.Millisecond, 80*time.Millisecond); err != nil {
		t.Errorf("Run errored: %v", err)
	}
	if got := readIntFile(t, pwmEnable); got != 1 {
		t.Errorf("pwm1_enable for rpm_target fan = %d, want 1 (manual via companion)", got)
	}
}

// TestTick_RPMTargetWritesFanTarget covers the rpm_target tick write
// path. The curve outputs a PWM-equivalent value (e.g. fixed 200), which
// the controller scales to RPM via fan*_max and writes to fan*_target.
func TestTick_RPMTargetWritesFanTarget(t *testing.T) {
	dir := t.TempDir()
	fanTarget := filepath.Join(dir, "fan1_target")
	fanMax := filepath.Join(dir, "fan1_max")
	tempInput := filepath.Join(dir, "temp1_input")
	for _, f := range []struct{ p, c string }{
		{fanTarget, "0\n"},
		{fanMax, "2550\n"}, // chosen so PWM 200 → RPM 2000 (round numbers)
		{tempInput, "60000\n"},
	} {
		if err := os.WriteFile(f.p, []byte(f.c), 0o600); err != nil {
			t.Fatalf("seed %s: %v", f.p, err)
		}
	}

	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: tempInput}},
		Fans: []config.Fan{{
			Name: "gpu fan", Type: "hwmon", PWMPath: fanTarget,
			ControlKind: "rpm_target", MinPWM: 40, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{{Name: "gpu_curve", Type: "fixed", Value: 200}},
		Controls: []config.Control{{Fan: "gpu fan", Curve: "gpu_curve"}},
	}
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	wd := watchdog.New(silentLogger())
	c := New("gpu fan", "gpu_curve", fanTarget, "hwmon", cfgPtr, wd, &stubCal{}, silentLogger())

	c.tick()

	got := readIntFile(t, fanTarget)
	// RPM = round(200/255 * 2550) = round(2000) = 2000
	want := 2000
	if got != want {
		t.Errorf("fan_target after rpm_target tick = %d, want %d", got, want)
	}
}

// TestTick_RPMTargetManualWritesFanTarget mirrors the manual-mode path
// for rpm_target fans. ManualPWM is a duty-cycle value; the controller
// scales it to RPM via fan*_max.
func TestTick_RPMTargetManualWritesFanTarget(t *testing.T) {
	dir := t.TempDir()
	fanTarget := filepath.Join(dir, "fan1_target")
	fanMax := filepath.Join(dir, "fan1_max")
	tempInput := filepath.Join(dir, "temp1_input")
	for _, f := range []struct{ p, c string }{
		{fanTarget, "0\n"},
		{fanMax, "2550\n"},
		{tempInput, "60000\n"},
	} {
		if err := os.WriteFile(f.p, []byte(f.c), 0o600); err != nil {
			t.Fatalf("seed %s: %v", f.p, err)
		}
	}

	manual := uint8(100) // → RPM = round(100/255 * 2550) = 1000
	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: tempInput}},
		Fans: []config.Fan{{
			Name: "gpu fan", Type: "hwmon", PWMPath: fanTarget,
			ControlKind: "rpm_target", MinPWM: 0, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{{Name: "gpu_curve", Type: "fixed", Value: 200}},
		Controls: []config.Control{{Fan: "gpu fan", Curve: "gpu_curve", ManualPWM: &manual}},
	}
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	wd := watchdog.New(silentLogger())
	c := New("gpu fan", "gpu_curve", fanTarget, "hwmon", cfgPtr, wd, &stubCal{}, silentLogger())

	c.tick()
	if got := readIntFile(t, fanTarget); got != 1000 {
		t.Errorf("fan_target after manual rpm_target tick = %d, want 1000", got)
	}
}
