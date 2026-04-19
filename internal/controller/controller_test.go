package controller

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/curve"
	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/watchdog"
)

func TestParseNvidiaIndex(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    uint
		wantErr bool
	}{
		{"valid_zero", "0", 0, false},
		{"valid_positive", "3", 3, false},
		{"leading_zero", "007", 7, false},
		{"empty", "", 0, true},
		{"non_numeric", "gpu0", 0, true},
		{"negative", "-1", 0, true},
		{"whitespace", " 0", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseNvidiaIndex(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (value=%d)", got)
				}
				if !strings.Contains(err.Error(), tc.input) && tc.input != "" {
					t.Errorf("error %q does not echo input %q", err.Error(), tc.input)
				}
				if tc.input == "" && !strings.Contains(err.Error(), `""`) {
					t.Errorf("error %q does not quote empty input", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestReadAllSensors_NvidiaPathInvariant pins the invariant that a non-numeric
// nvidia sensor path at runtime is a config-load bug, not a silent-fallback
// "read GPU 0" situation. Valid paths must forward to the nvidia reader with
// the parsed index; invalid paths must skip the sensor (absent from the map)
// and must never cause the reader to be called with a bogus index.
//
// These subtests mutate the package-level readNvidiaMetric and therefore must
// not call t.Parallel(). t.Cleanup restores the production value after each
// subtest as defence-in-depth.
func TestReadAllSensors_NvidiaPathInvariant(t *testing.T) {
	// One hwmon sibling shared across all subtests: its presence in the
	// returned map proves the loop kept going after the bad nvidia entry.
	dir := t.TempDir()
	hwmonPath := filepath.Join(dir, "temp1_input")
	if err := os.WriteFile(hwmonPath, []byte("42000\n"), 0o600); err != nil {
		t.Fatalf("write hwmon fixture: %v", err)
	}
	hwmonSensor := config.Sensor{Name: "cpu", Type: "hwmon", Path: hwmonPath}

	cases := []struct {
		name    string
		path    string
		wantOK  bool // true: nvidia reader should be called and result landed in map
		wantIdx uint
	}{
		{"valid_zero", "0", true, 0},
		{"valid_positive", "3", true, 3},
		{"empty", "", false, 0},
		{"non_numeric", "gpu0", false, 0},
		{"negative", "-1", false, 0},
		{"overflow", "4294967296", false, 0}, // uint32 max + 1
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Do NOT call t.Parallel — readNvidiaMetric is a mutable global.
			t.Cleanup(func() { readNvidiaMetric = nvidia.ReadMetric })

			var (
				called  bool
				gotIdx  uint
				gotName string
			)
			readNvidiaMetric = func(idx uint, metric string) (float64, error) {
				called = true
				gotIdx = idx
				gotName = metric
				return 77.0, nil
			}

			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			nvSensor := config.Sensor{
				Name:   "gpu",
				Type:   "nvidia",
				Path:   tc.path,
				Metric: "temperature",
			}
			m := make(map[string]float64)
			readAllSensors(logger, []config.Sensor{nvSensor, hwmonSensor}, m, nil)

			// The hwmon sibling must always land in the map — skip-and-continue
			// means one bad sensor never hides a healthy one.
			if v, ok := m["cpu"]; !ok || v != 42.0 {
				t.Errorf("hwmon sibling: got value=%v present=%v, want 42.0 present", v, ok)
			}

			if tc.wantOK {
				if !called {
					t.Fatalf("readNvidiaMetric was not called for valid path %q", tc.path)
				}
				if gotIdx != tc.wantIdx {
					t.Errorf("readNvidiaMetric idx: got %d want %d", gotIdx, tc.wantIdx)
				}
				if gotName != "temperature" {
					t.Errorf("readNvidiaMetric metric: got %q want %q", gotName, "temperature")
				}
				if v, ok := m["gpu"]; !ok || v != 77.0 {
					t.Errorf("gpu sensor: got value=%v present=%v, want 77.0 present", v, ok)
				}
				if strings.Contains(buf.String(), "invariant violated") {
					t.Errorf("unexpected invariant log for valid path %q: %s", tc.path, buf.String())
				}
				return
			}

			// Invalid-path path: reader must never run with a bogus index,
			// sensor must be absent, and the invariant Error must be logged.
			if called {
				t.Errorf("readNvidiaMetric must not be called for bad path %q (idx=%d)", tc.path, gotIdx)
			}
			if _, ok := m["gpu"]; ok {
				t.Errorf("gpu sensor must be absent for bad path %q, got %v", tc.path, m["gpu"])
			}
			out := buf.String()
			if !strings.Contains(out, "invariant violated") {
				t.Errorf("expected invariant log for bad path %q, got: %s", tc.path, out)
			}
			if !strings.Contains(out, "level=ERROR") {
				t.Errorf("expected ERROR level for bad path %q, got: %s", tc.path, out)
			}
		})
	}
}

// TestTick_WriteRetryAndRestoreOnDoubleFailure asserts the full retry+restore
// path: two consecutive Write failures trigger (a) a write_retry WARN on the
// first failure, (b) a write_failed_restore_triggered ERROR on the second, and
// (c) exactly two Write calls total — one initial plus one retry.
//
// The watchdog is seeded with origEnable=2 for the fan path; after the tick,
// the enable file must read "2" (RestoreOne fired and wrote the origEnable back).
func TestTick_WriteRetryAndRestoreOnDoubleFailure(t *testing.T) {
	ff := newFakeFan(t) // pwm_enable=2

	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 40, MaxPWM: 200,
		}},
		Curves:   []config.CurveConfig{{Name: "cpu_curve", Type: "fixed", Value: 100}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	wd := watchdog.New(logger)
	wd.Register(ff.pwmPath, "hwmon") // origEnable=2 captured at registration

	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	c := New("cpu fan", "cpu_curve", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger)

	// Inject a fake backend whose first two Writes fail.
	writeErr := errors.New("sysfs write failed")
	fb := &fakeErrBackend{errs: []error{writeErr, writeErr}}
	c.backend = fb

	// Simulate daemon taking manual control (enable=1) so RestoreOne's write
	// of origEnable=2 is observable as a change.
	if err := os.WriteFile(ff.enablePath, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("perturb enable: %v", err)
	}

	c.tick()

	// RestoreOne must have fired: enable file restored to the captured origEnable (2).
	if got := readIntFile(t, ff.enablePath); got != 2 {
		t.Errorf("pwm_enable after double-fail tick = %d, want 2 (RestoreOne must have fired)", got)
	}

	// Structured event must appear in logs.
	logs := logBuf.String()
	if !strings.Contains(logs, "write_failed_restore_triggered") {
		t.Errorf("expected write_failed_restore_triggered event in logs, got: %s", logs)
	}

	// Write called exactly twice: initial attempt + one retry.
	if fb.writeCalls != 2 {
		t.Errorf("Write called %d times, want 2 (initial + one retry)", fb.writeCalls)
	}
}

// fakeErrBackend is a hal.FanBackend whose Write returns pre-configured errors
// for the first N calls, then succeeds. Other methods are no-ops.
type fakeErrBackend struct {
	writeCalls int
	errs       []error
}

func (b *fakeErrBackend) Enumerate(_ context.Context) ([]hal.Channel, error) {
	return nil, nil
}
func (b *fakeErrBackend) Read(_ hal.Channel) (hal.Reading, error) { return hal.Reading{}, nil }
func (b *fakeErrBackend) Write(_ hal.Channel, _ uint8) error {
	i := b.writeCalls
	b.writeCalls++
	if i < len(b.errs) {
		return b.errs[i]
	}
	return nil
}
func (b *fakeErrBackend) Restore(_ hal.Channel) error { return nil }
func (b *fakeErrBackend) Close() error                { return nil }
func (b *fakeErrBackend) Name() string                { return "fake" }

// makePICurveCfg builds a Config containing one sensor at tempPath, one fan
// at pwmPath, one PI curve, and a Control binding them.
func makePICurveCfg(ff fakeFan, fanName, curveName string, fanMin, fanMax uint8) *config.Config {
	kp := 2.5
	ki := 0.1
	sp := 65.0
	ic := 100.0
	ffVal := uint8(80)
	return &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: fanName, Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: fanMin, MaxPWM: fanMax,
		}},
		Curves: []config.CurveConfig{{
			Name:          curveName,
			Type:          "pi",
			Sensor:        "cpu",
			Setpoint:      &sp,
			Kp:            &kp,
			Ki:            &ki,
			FeedForward:   &ffVal,
			IntegralClamp: &ic,
		}},
		Controls: []config.Control{{Fan: fanName, Curve: curveName}},
	}
}

// TestController_PICurve_IntegralPerChannel pins the per-channel isolation
// invariant: two channels sharing the same CurveConfig type must maintain
// independent integral state. A bug that keys piState by curve-pointer
// instead of channel ID would cause both channels to share one integral.
func TestController_PICurve_IntegralPerChannel(t *testing.T) {
	ff1 := newFakeFan(t)
	ff2 := newFakeFan(t)

	// Channel 1 is hot (80°C), channel 2 is cold (50°C).
	if err := os.WriteFile(ff1.tempPath, []byte("80000\n"), 0o600); err != nil {
		t.Fatalf("write temp1: %v", err)
	}
	if err := os.WriteFile(ff2.tempPath, []byte("50000\n"), 0o600); err != nil {
		t.Fatalf("write temp2: %v", err)
	}

	cfg1 := makePICurveCfg(ff1, "fan1", "pi_curve", 30, 255)
	cfg2 := makePICurveCfg(ff2, "fan2", "pi_curve", 30, 255)

	c1 := newTestController(t, ff1, cfg1, &stubCal{}, "fan1", "pi_curve")
	c2 := newTestController(t, ff2, cfg2, &stubCal{}, "fan2", "pi_curve")

	// Run several ticks so integrals can diverge.
	for i := 0; i < 10; i++ {
		c1.tick()
		c2.tick()
	}

	i1 := c1.piState[ff1.pwmPath].Integral
	i2 := c2.piState[ff2.pwmPath].Integral

	// The hot channel must have a larger (more positive) integral.
	if i1 <= i2 {
		t.Errorf("channel integrals did not diverge: i1=%.2f, i2=%.2f; hot channel (i1) must be > cold (i2)", i1, i2)
	}
}

// TestController_PIState_ResetOnPanic pins the panic-mode PI reset contract:
// on panic engagement the integral is zeroed so the forced 100% PWM does not
// wind up the integral. The state must still be zero after panic is released.
func TestController_PIState_ResetOnPanic(t *testing.T) {
	ff := newFakeFan(t)
	if err := os.WriteFile(ff.tempPath, []byte("85000\n"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}

	cfg := makePICurveCfg(ff, "fan1", "pi_curve", 30, 255)
	logger := silentLogger()
	wd := watchdog.New(logger)
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)

	panicFlag := &stubPanic{}
	c := New("fan1", "pi_curve", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger,
		WithPanicChecker(panicFlag))

	// Run ticks to build up integral.
	for i := 0; i < 10; i++ {
		c.tick()
	}
	if c.piState[ff.pwmPath].Integral == 0 {
		t.Fatal("expected non-zero integral after hot ticks, got 0")
	}

	// Engage panic mode — engagement resets piState.
	panicFlag.active = true
	c.tick() // tick detects engagement and resets piState

	if c.piState[ff.pwmPath].Integral != 0 {
		t.Errorf("piState.Integral = %.2f after panic engagement; want 0", c.piState[ff.pwmPath].Integral)
	}

	// Release panic — piState must still be zero (was reset on engagement
	// and no updates happened while panicked).
	panicFlag.active = false
	if c.piState[ff.pwmPath].Integral != 0 {
		t.Errorf("piState.Integral = %.2f after panic release; want 0", c.piState[ff.pwmPath].Integral)
	}
}

// stubPanic is a test PanicChecker that returns a controllable flag.
type stubPanic struct{ active bool }

func (s *stubPanic) IsPanicked(_ string) bool {
	if s == nil {
		return false
	}
	return s.active
}

// TODO(issue #313): bind this invariant to hwmon-safety.md once rule format lands

// TestController_ErrNotPermittedFatal regresses #288: a hal.ErrNotPermitted
// returned by Write must cause Run() to return a non-nil error immediately
// rather than loop-and-retry. The returned error must satisfy
// errors.Is(err, hal.ErrNotPermitted) so callers can distinguish
// permission failures from transient write failures.
func TestController_ErrNotPermittedFatal(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)

	logger := silentLogger()
	wd := watchdog.New(logger)
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	c := New("cpu fan", "cpu_curve", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger)

	// Inject a backend whose Write always returns hal.ErrNotPermitted.
	c.backend = &fakeErrBackend{errs: []error{hal.ErrNotPermitted}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- c.Run(ctx, 20*time.Millisecond) }()

	select {
	case runErr := <-errCh:
		if runErr == nil {
			t.Fatal("Run returned nil on ErrNotPermitted; want non-nil fatal error")
		}
		if !errors.Is(runErr, hal.ErrNotPermitted) {
			t.Errorf("Run returned %v; want errors.Is(..., hal.ErrNotPermitted) == true", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after ErrNotPermitted; expected fatal propagation")
	}
}

// makeManualFanCfg builds a Config with ManualPWM set on the control binding,
// exercising the manual-mode tick path instead of the curve-driven path.
func makeManualFanCfg(ff fakeFan, fanName string, manualPWM *uint8) *config.Config {
	return &config.Config{
		Fans: []config.Fan{{
			Name: fanName, Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 40, MaxPWM: 200,
		}},
		Controls: []config.Control{{Fan: fanName, ManualPWM: manualPWM}},
	}
}

// TestController_ErrNotPermittedFatal_ManualMode regresses the manual-PWM
// Write site introduced by #341: a hal.ErrNotPermitted on the manual-mode
// branch must cause Run() to return a non-nil fatal error, not loop-and-log.
// Mirrors TestController_ErrNotPermittedFatal which covers the curve-driven path.
func TestController_ErrNotPermittedFatal_ManualMode(t *testing.T) {
	ff := newFakeFan(t)
	pwm := uint8(128)
	cfg := makeManualFanCfg(ff, "cpu fan", &pwm)

	logger := silentLogger()
	wd := watchdog.New(logger)
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	c := New("cpu fan", "", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger)

	// Inject a backend whose Write always returns hal.ErrNotPermitted.
	c.backend = &fakeErrBackend{errs: []error{hal.ErrNotPermitted}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- c.Run(ctx, 20*time.Millisecond) }()

	select {
	case runErr := <-errCh:
		if runErr == nil {
			t.Fatal("Run returned nil on ErrNotPermitted; want non-nil fatal error")
		}
		if !errors.Is(runErr, hal.ErrNotPermitted) {
			t.Errorf("Run returned %v; want errors.Is(..., hal.ErrNotPermitted) == true", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after ErrNotPermitted; expected fatal propagation")
	}
}

// TestController_ManualWriteRetryAndRestore regresses #272: the manual-mode
// write path must use the same retry+RestoreOne semantics as the curve path.
// Two consecutive Write failures must trigger RestoreOne (not just log-and-return).
func TestController_ManualWriteRetryAndRestore(t *testing.T) {
	ff := newFakeFan(t) // pwm_enable=2

	manual := uint8(150)
	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 40, MaxPWM: 200,
		}},
		Curves:   []config.CurveConfig{{Name: "cpu_curve", Type: "fixed", Value: 100}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve", ManualPWM: &manual}},
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	wd := watchdog.New(logger)
	wd.Register(ff.pwmPath, "hwmon") // origEnable=2 captured at registration

	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	c := New("cpu fan", "cpu_curve", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger)

	writeErr := errors.New("sysfs write failed")
	fb := &fakeErrBackend{errs: []error{writeErr, writeErr}}
	c.backend = fb

	// Simulate daemon having taken manual control (enable=1) so RestoreOne's
	// write of origEnable=2 is observable as a change.
	if err := os.WriteFile(ff.enablePath, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("perturb enable: %v", err)
	}

	c.tick()

	// RestoreOne must have fired: enable file restored to the captured origEnable (2).
	if got := readIntFile(t, ff.enablePath); got != 2 {
		t.Errorf("pwm_enable after double-fail manual tick = %d, want 2 (RestoreOne must have fired)", got)
	}

	// Structured event must appear in logs.
	if !strings.Contains(logBuf.String(), "write_failed_restore_triggered") {
		t.Errorf("expected write_failed_restore_triggered event in logs, got: %s", logBuf.String())
	}

	// Write called exactly twice: initial attempt + one retry.
	if fb.writeCalls != 2 {
		t.Errorf("Write called %d times, want 2 (initial + one retry)", fb.writeCalls)
	}
}

// Verify PICurve implements StatefulCurve at compile time.
var _ curve.StatefulCurve = (*curve.PICurve)(nil)
