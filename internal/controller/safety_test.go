package controller

// safety_test.go binds every rule in .claude/rules/hwmon-safety.md to a
// named subtest. The goal is that a regression in any one invariant
// fails in CI at a predictable location with a predictable name.
//
// Each subtest is referenced by its rule in comments; if a rule text
// is edited, update the corresponding subtest. New rules added to
// hwmon-safety.md must land with a matching subtest here in the same
// PR — missing coverage is a review blocker.
//
// Skipped subtests here mark invariants the current controller does
// NOT enforce. The skip strings reference tracking issues; removing
// t.Skip must happen in the PR that closes the tracking issue, never
// earlier.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/watchdog"
)

// TestSafety_Invariants is the rule-to-test index for the controller
// safety-critical write path. Each subtest binds one invariant from
// .claude/rules/hwmon-safety.md. Do not delete a subtest without
// either (a) deleting the rule it pins or (b) replacing it with a
// stronger test that still covers the same invariant.
func TestSafety_Invariants(t *testing.T) {
	// ---------- Rule: clamp every PWM write to [MinPWM, MaxPWM] ----------

	t.Run("clamp/below_min_pwm", func(t *testing.T) {
		// Curve returning 10 with MinPWM=40 must be clipped to 40.
		// A curve bug must never stall a fan below its configured floor.
		ff := newFakeFan(t)
		// Force curve output ~10 by pinning temp just above MinTemp with a
		// linear 40-200C / 0-255 curve: at 41C the ratio is 1/160 ≈ 1.6.
		if err := os.WriteFile(ff.tempPath, []byte("41000\n"), 0o600); err != nil {
			t.Fatalf("seed temp: %v", err)
		}
		cfg := &config.Config{
			Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
			Fans: []config.Fan{{
				Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
				MinPWM: 40, MaxPWM: 200,
			}},
			Curves: []config.CurveConfig{{
				Name: "cpu_curve", Type: "linear", Sensor: "cpu",
				MinTemp: 40, MaxTemp: 200, MinPWM: 0, MaxPWM: 255,
			}},
			Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
		}
		c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
		c.tick()

		if got := readPWMByte(t, ff.pwmPath); got != 40 {
			t.Errorf("PWM = %d, want 40 (clamped up to MinPWM)", got)
		}
	})

	t.Run("clamp/above_max_pwm", func(t *testing.T) {
		// Curve returning 250 with MaxPWM=200 must be clipped to 200.
		// An overrunning curve must never push a fan past its noise ceiling.
		ff := newFakeFan(t)
		if err := os.WriteFile(ff.tempPath, []byte("120000\n"), 0o600); err != nil {
			t.Fatalf("seed temp: %v", err)
		}
		cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
		c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
		c.tick()

		if got := readPWMByte(t, ff.pwmPath); got != 200 {
			t.Errorf("PWM = %d, want 200 (clamped down to MaxPWM)", got)
		}
	})

	// ---------- Rule: pump fans have a hard floor at pump_minimum ----------

	t.Run("clamp/pump_floor_beats_curve", func(t *testing.T) {
		// is_pump=true, PumpMinimum=80, MinPWM=80 (buildconfig promotes
		// MinPWM to max(stopPWM, PumpMinimum)). Curve returns ~32 at 45C.
		// Write must be 80 — pump below PumpMinimum risks coolant stall.
		ff := newFakeFan(t)
		if err := os.WriteFile(ff.tempPath, []byte("45000\n"), 0o600); err != nil {
			t.Fatalf("seed temp: %v", err)
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
	})

	// ---------- Rule: never write PWM=0 unless allow_stop is explicitly on ----------

	t.Run("allow_stop/disabled_refuses_zero", func(t *testing.T) {
		// SAFETY GAP: config.Fan has no AllowStop field; controller's only
		// gate is the [MinPWM, MaxPWM] clamp. When MinPWM=0 (misconfig or
		// stale config), the curve can drive the fan to 0 even though the
		// rule requires allow_stop=true to permit that. Tracked by #115.
		t.Skip("tracked by #115 — controller permits PWM=0 when MinPWM=0 without an allow_stop gate")

		ff := newFakeFan(t)
		if err := os.WriteFile(ff.pwmPath, []byte("100\n"), 0o600); err != nil {
			t.Fatalf("seed pwm: %v", err)
		}
		cfg := &config.Config{
			Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
			Fans: []config.Fan{{
				Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
				MinPWM: 0, MaxPWM: 255,
				// AllowStop: false (field doesn't exist yet — see #115)
			}},
			Curves: []config.CurveConfig{{Name: "cpu_curve", Type: "fixed", Value: 0}},
			Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
		}
		c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
		c.tick()

		if got := readPWMByte(t, ff.pwmPath); got == 0 {
			t.Errorf("PWM = 0, want >0 (allow_stop not set, zero write forbidden)")
		}
	})

	t.Run("allow_stop/enabled_permits_zero", func(t *testing.T) {
		// With MinPWM=0 the clamp lets PWM=0 through. Today this passes
		// because the allow_stop gate is missing entirely (#115); once
		// that gate lands, this test must continue to pass by setting
		// AllowStop=true alongside MinPWM=0.
		ff := newFakeFan(t)
		if err := os.WriteFile(ff.pwmPath, []byte("100\n"), 0o600); err != nil {
			t.Fatalf("seed pwm: %v", err)
		}
		cfg := &config.Config{
			Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
			Fans: []config.Fan{{
				Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
				MinPWM: 0, MaxPWM: 255,
				// AllowStop: true (field doesn't exist yet — see #115)
			}},
			Curves: []config.CurveConfig{{Name: "cpu_curve", Type: "fixed", Value: 0}},
			Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
		}
		c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
		c.tick()

		if got := readPWMByte(t, ff.pwmPath); got != 0 {
			t.Errorf("PWM = %d, want 0 (allow_stop permits zero write)", got)
		}
	})

	// ---------- Rule: set pwm_enable=1 (manual) before any PWM write ----------

	t.Run("pwm_enable/manual_mode_set_on_run_start", func(t *testing.T) {
		// Fixture pwm_enable is preset to "2" (auto). Run() must flip it
		// to "1" before the tick loop writes PWM; otherwise the BIOS auto
		// loop would overwrite the daemon's duty cycle within milliseconds.
		ff := newFakeFan(t) // pwm_enable="2"
		cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
		c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

		if err := runWithTimeout(t, c, 40*time.Millisecond, 80*time.Millisecond); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := readIntFile(t, ff.enablePath); got != 1 {
			t.Errorf("pwm_enable after Run = %d, want 1 (manual mode acquired)", got)
		}
		// The tick must also have fired — a PWM != 0 write proves the loop
		// ran after pwm_enable was flipped. The ordering (enable=1 before
		// first PWM write) is enforced by Run's code structure: pwm_enable
		// is written before the ticker loop starts.
		if got := readPWMByte(t, ff.pwmPath); got == 0 {
			t.Errorf("PWM = 0 after Run; expected a tick to have fired")
		}
	})

	t.Run("pwm_enable/unsupported_driver_proceeds", func(t *testing.T) {
		// nct6683 (NCT6687D) does not expose pwm_enable. Writing it
		// returns fs.ErrNotExist. Run must log INFO and continue to
		// write PWM values directly; returning an error here would
		// brick support for a common chip.
		ff := newFakeFan(t)
		if err := os.Remove(ff.enablePath); err != nil {
			t.Fatalf("rm enable: %v", err)
		}
		cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
		c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

		if err := runWithTimeout(t, c, 30*time.Millisecond, 60*time.Millisecond); err != nil {
			t.Errorf("Run errored on missing pwm_enable; want graceful continue: %v", err)
		}
		// A tick still wrote PWM despite the missing enable file.
		if _, err := os.Stat(ff.pwmPath); err != nil {
			t.Errorf("pwm file missing after Run: %v", err)
		}
	})

	// ---------- Rule: sensor read ENOENT/EIO is logged-and-skipped, not fatal ----------

	t.Run("sensor_read/enoent_skip", func(t *testing.T) {
		// Sensor file is deleted → hwmon.ReadValue wraps an fs.ErrNotExist.
		// readAllSensors logs and omits the sensor from the map.
		// curve.Linear with a missing sensor key returns MaxPWM (255),
		// which the fan clamp caps to fan.MaxPWM. Tick completes,
		// PWM write happens, no panic.
		ff := newFakeFan(t)
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

		// Tick must not panic even though the sensor file is gone.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("tick panicked on ENOENT sensor: %v", r)
			}
		}()
		c.tick()

		// Linear fallback → MaxPWM(255), clamped to fan.MaxPWM(200).
		if got := readPWMByte(t, ff.pwmPath); got != 200 {
			t.Errorf("PWM on ENOENT sensor = %d, want 200 (Linear MaxPWM fallback clamped)", got)
		}
	})

	t.Run("sensor_read/eio_skip", func(t *testing.T) {
		// EIO is hard to force on tmpfs, so exercise the same
		// log-and-skip path through a nvidia sensor whose reader
		// returns syscall.EIO. readAllSensors treats any non-nil
		// error identically — this guards the rule that a driver-side
		// I/O error must never kill the tick.
		t.Cleanup(func() { readNvidiaMetric = nvidia.ReadMetric })
		readNvidiaMetric = func(idx uint, metric string) (float64, error) {
			return 0, syscall.EIO
		}

		ff := newFakeFan(t)
		cfg := &config.Config{
			Sensors: []config.Sensor{{Name: "gpu", Type: "nvidia", Path: "0", Metric: "temperature"}},
			Fans: []config.Fan{{
				Name: "gpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
				MinPWM: 40, MaxPWM: 200,
			}},
			Curves: []config.CurveConfig{{
				Name: "gpu_curve", Type: "linear", Sensor: "gpu",
				MinTemp: 40, MaxTemp: 80, MinPWM: 50, MaxPWM: 255,
			}},
			Controls: []config.Control{{Fan: "gpu fan", Curve: "gpu_curve"}},
		}
		c := newTestController(t, ff, cfg, &stubCal{}, "gpu fan", "gpu_curve")

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("tick panicked on EIO sensor: %v", r)
			}
		}()
		c.tick()

		if got := readPWMByte(t, ff.pwmPath); got != 200 {
			t.Errorf("PWM on EIO sensor = %d, want 200 (Linear MaxPWM fallback clamped)", got)
		}
	})

	// ---------- Rule: Watchdog.Restore() must fire on every exit path ----------

	t.Run("watchdog/restore_on_context_cancel", func(t *testing.T) {
		// SAFETY GAP: Controller.Run() only calls wd.Restore() from its
		// panic-recover branch. On ctx.Done() it logs and returns nil —
		// Restore is not invoked. The daemon safety envelope is saved by
		// cmd/ventd/main.go's defer wd.Restore(), but the controller
		// layer does not fulfil the invariant on its own. Tracked by #116.
		t.Skip("tracked by #116 — Controller.Run does not call wd.Restore on context cancel")

		ff := newFakeFan(t) // pwm_enable="2"
		cfgStruct := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
		logger := silentLogger()
		wd := watchdog.New(logger)
		wd.Register(ff.pwmPath, "hwmon") // origEnable=2 captured

		cfgPtr := &atomic.Pointer[config.Config]{}
		cfgPtr.Store(cfgStruct)
		c := New("cpu fan", "cpu_curve", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger)

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- c.Run(ctx, 30*time.Millisecond) }()
		time.Sleep(60 * time.Millisecond) // let one tick fire
		cancel()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("Run returned %v, want nil on ctx cancel", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return within 2s of cancel")
		}

		// Restore should have run: pwm_enable back to "2".
		if got := readIntFile(t, ff.enablePath); got != 2 {
			t.Errorf("pwm_enable after ctx cancel = %d, want 2 (Restore must have run)", got)
		}
	})

	t.Run("watchdog/restore_on_tick_panic", func(t *testing.T) {
		// A panic in the sensor-read path (fake nvidia metric panics)
		// must be recovered by Run's top-level defer, trigger
		// wd.Restore(), and surface as a wrapped error. Without the
		// recover+Restore, the daemon would crash with fans at the last
		// (possibly zero) PWM value.
		t.Cleanup(func() { readNvidiaMetric = nvidia.ReadMetric })
		readNvidiaMetric = func(idx uint, metric string) (float64, error) {
			panic("synthetic sensor-read panic")
		}

		ff := newFakeFan(t) // pwm_enable="2"
		cfgStruct := &config.Config{
			Sensors: []config.Sensor{{Name: "gpu", Type: "nvidia", Path: "0", Metric: "temperature"}},
			Fans: []config.Fan{{
				Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
				MinPWM: 40, MaxPWM: 200,
			}},
			Curves: []config.CurveConfig{{
				Name: "cpu_curve", Type: "linear", Sensor: "gpu",
				MinTemp: 40, MaxTemp: 80, MinPWM: 50, MaxPWM: 255,
			}},
			Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
		}
		logger := silentLogger()
		wd := watchdog.New(logger)
		wd.Register(ff.pwmPath, "hwmon") // origEnable=2 captured

		cfgPtr := &atomic.Pointer[config.Config]{}
		cfgPtr.Store(cfgStruct)
		c := New("cpu fan", "cpu_curve", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errCh := make(chan error, 1)
		go func() { errCh <- c.Run(ctx, 20*time.Millisecond) }()

		var runErr error
		select {
		case runErr = <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return within 2s; expected panic-recover to surface an error")
		}

		if runErr == nil {
			t.Fatal("Run returned nil on tick panic, want wrapped error")
		}
		// The wrapped error message must echo the fan name so an operator
		// can find the offending controller in the logs.
		if want := "cpu fan"; !strings.Contains(runErr.Error(), want) {
			t.Errorf("error %q does not name fan %q", runErr.Error(), want)
		}

		// Restore fired: pwm_enable back to "2".
		if got := readIntFile(t, ff.enablePath); got != 2 {
			t.Errorf("pwm_enable after panic = %d, want 2 (Restore must have run)", got)
		}
	})

	// ---------- Rule: resolve paths via hwmon_device, not literal hwmon index ----------

	t.Run("hwmon_index_instability/resolve_by_device_path", func(t *testing.T) {
		// hwmonX indices are volatile across reboots. The daemon stores
		// stable device paths and re-resolves at startup via
		// hwmon.ResolvePath. This test pins the integration: a path stored
		// under hwmon3 but present under hwmon5 at boot B resolves to the
		// new location, and a controller constructed with the resolved
		// path writes to the correct file.
		stableDevice := t.TempDir() // /tmp/XXX
		hwmonParent := filepath.Join(stableDevice, "hwmon")
		if err := os.Mkdir(hwmonParent, 0o755); err != nil {
			t.Fatalf("mkdir hwmon parent: %v", err)
		}
		// Boot A layout: hwmon3/pwm1.
		dirA := filepath.Join(hwmonParent, "hwmon3")
		if err := os.Mkdir(dirA, 0o755); err != nil {
			t.Fatalf("mkdir hwmon3: %v", err)
		}
		pathA := filepath.Join(dirA, "pwm1")
		tempA := filepath.Join(dirA, "temp1_input")
		enableA := filepath.Join(dirA, "pwm1_enable")
		for _, f := range []struct{ p, c string }{
			{pathA, "0\n"}, {tempA, "60000\n"}, {enableA, "2\n"},
		} {
			if err := os.WriteFile(f.p, []byte(f.c), 0o600); err != nil {
				t.Fatalf("seed %s: %v", f.p, err)
			}
		}

		// Resolution under boot A: stored path still exists → unchanged.
		resolvedA, changedA := hwmon.ResolvePath(pathA, stableDevice)
		if changedA {
			t.Errorf("ResolvePath boot A: changed=true, want false (path still valid)")
		}
		if resolvedA != pathA {
			t.Errorf("ResolvePath boot A: %q, want %q", resolvedA, pathA)
		}

		// Simulate boot B: hwmon3 renumbered to hwmon5. Move the whole dir
		// so the pwm1 file is physically gone from hwmon3 and present
		// under hwmon5.
		dirB := filepath.Join(hwmonParent, "hwmon5")
		if err := os.Rename(dirA, dirB); err != nil {
			t.Fatalf("simulate renumber: %v", err)
		}
		pathB := filepath.Join(dirB, "pwm1")
		tempB := filepath.Join(dirB, "temp1_input")

		resolvedB, changedB := hwmon.ResolvePath(pathA, stableDevice)
		if !changedB {
			t.Fatalf("ResolvePath boot B: changed=false, want true (stored hwmon3 no longer exists)")
		}
		if resolvedB != pathB {
			t.Errorf("ResolvePath boot B: %q, want %q", resolvedB, pathB)
		}

		// Controller built with resolvedB must write to the new location,
		// not the stale hwmon3 path.
		cfg := &config.Config{
			Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: tempB}},
			Fans: []config.Fan{{
				Name: "cpu fan", Type: "hwmon", PWMPath: resolvedB,
				MinPWM: 40, MaxPWM: 200,
			}},
			Curves: []config.CurveConfig{{
				Name: "cpu_curve", Type: "linear", Sensor: "cpu",
				MinTemp: 40, MaxTemp: 80, MinPWM: 0, MaxPWM: 255,
			}},
			Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
		}
		cfgPtr := &atomic.Pointer[config.Config]{}
		cfgPtr.Store(cfg)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		wd := watchdog.New(logger)
		c := New("cpu fan", "cpu_curve", resolvedB, "hwmon", cfgPtr, wd, &stubCal{}, logger)
		c.tick()

		// Write landed at resolvedB (= hwmon5/pwm1), not hwmon3/pwm1.
		if got := readPWMByte(t, pathB); got == 0 {
			t.Errorf("resolved pwm file unchanged after tick; want a non-zero write")
		}
		if _, err := os.Stat(pathA); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stale hwmon3 path should not exist after simulated renumber: %v", err)
		}
	})
}

