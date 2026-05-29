package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/curve"
	"github.com/ventd/ventd/internal/hal"
)

// fakeCurveSink is a hal.FanBackend + hal.CurveSink test double. Enumerate
// returns one channel keyed by id with the configured caps; WriteCurve records
// every programmed curve so tests can assert program-once + on-change
// semantics; per-tick Write always fails so a test that accidentally drives
// the per-tick path is caught.
type fakeCurveSink struct {
	id       string
	caps     hal.Caps
	writes   [][]hal.CurvePoint
	writeErr error
}

func (f *fakeCurveSink) Name() string { return "fakecs" }
func (f *fakeCurveSink) Close() error { return nil }
func (f *fakeCurveSink) Enumerate(context.Context) ([]hal.Channel, error) {
	return []hal.Channel{{ID: f.id, Role: hal.RoleGPU, Caps: f.caps}}, nil
}
func (f *fakeCurveSink) Read(hal.Channel) (hal.Reading, error) { return hal.Reading{OK: true}, nil }
func (f *fakeCurveSink) Write(hal.Channel, uint8) error {
	return errors.New("fakecs: per-tick Write must not be used on a curve-sink channel")
}
func (f *fakeCurveSink) Restore(hal.Channel) error { return nil }
func (f *fakeCurveSink) WriteCurve(_ hal.Channel, pts []hal.CurvePoint) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes = append(f.writes, append([]hal.CurvePoint(nil), pts...))
	return nil
}

const curveSinkTestPath = "/sys/class/drm/card0"

func newCurveSinkController(be hal.FanBackend, cfg *config.Config) *Controller {
	var ptr atomic.Pointer[config.Config]
	ptr.Store(cfg)
	return &Controller{
		fanName:            "gpu",
		curveName:          "gpu_curve",
		pwmPath:            curveSinkTestPath,
		fanType:            "fakecs",
		cfg:                &ptr,
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		backend:            be,
		rawSensorsBuf:      map[string]float64{},
		smoothedBuf:        map[string]float64{},
		sentinelBuf:        map[string]bool{},
		sensorInvalidSince: map[string]time.Time{},
		piState:            map[string]curve.PIState{},
	}
}

func curveSinkTestConfig() *config.Config {
	return &config.Config{
		Fans: []config.Fan{{
			Name: "gpu", Type: "fakecs", PWMPath: curveSinkTestPath, MinPWM: 0, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{{
			Name: "gpu_curve", Type: "points", Sensor: "gpu_temp",
			MinTemp: 40, MaxTemp: 90,
			Points: []config.CurvePoint{
				{Temp: 40, PWM: 51}, {Temp: 65, PWM: 128}, {Temp: 90, PWM: 255},
			},
		}},
		Controls: []config.Control{{Fan: "gpu", Curve: "gpu_curve"}},
	}
}

// TestCurveSinkBackend_Detection pins that the curve-sink path is taken only
// when the channel advertises CapWriteCurve — a CurveSink-capable backend
// exposing the channel as per-tick PWM falls through to the normal tick path.
func TestCurveSinkBackend_Detection(t *testing.T) {
	t.Run("curve_cap_takes_curve_sink_path", func(t *testing.T) {
		be := &fakeCurveSink{id: curveSinkTestPath, caps: hal.CapRead | hal.CapRestore | hal.CapWriteCurve}
		c := newCurveSinkController(be, curveSinkTestConfig())
		if c.curveSinkBackend() == nil {
			t.Error("CapWriteCurve channel must resolve to a CurveSink")
		}
	})
	t.Run("pwm_cap_falls_through", func(t *testing.T) {
		// Same backend (implements CurveSink) but the channel is per-tick PWM.
		be := &fakeCurveSink{id: curveSinkTestPath, caps: hal.CapRead | hal.CapRestore | hal.CapWritePWM}
		c := newCurveSinkController(be, curveSinkTestConfig())
		if c.curveSinkBackend() != nil {
			t.Error("CapWritePWM channel must fall through to the per-tick path, not CurveSink")
		}
	})
}

// TestProgramCurveSink_ProgramsOnceAndOnChange is the core behaviour: the
// hardware curve is programmed once, NOT re-programmed when nothing changed,
// and re-programmed when the bound curve changes. The programmed anchors are
// ascending and clamped to the fan bounds.
func TestProgramCurveSink_ProgramsOnceAndOnChange(t *testing.T) {
	be := &fakeCurveSink{id: curveSinkTestPath, caps: hal.CapRead | hal.CapRestore | hal.CapWriteCurve}
	cfg := curveSinkTestConfig()
	c := newCurveSinkController(be, cfg)

	c.programCurveSink(be)
	if len(be.writes) != 1 {
		t.Fatalf("first program: got %d WriteCurve calls, want 1", len(be.writes))
	}
	// Ascending temperatures, percentages within [0,100].
	pts := be.writes[0]
	if len(pts) == 0 {
		t.Fatal("programmed curve has no points")
	}
	for i := 1; i < len(pts); i++ {
		if pts[i].TempC < pts[i-1].TempC {
			t.Errorf("points not ascending by temp at %d", i)
		}
	}
	for _, p := range pts {
		if p.Pct < 0 || p.Pct > 100 {
			t.Errorf("pct %d out of range", p.Pct)
		}
	}

	// No change → no re-program.
	c.programCurveSink(be)
	if len(be.writes) != 1 {
		t.Fatalf("unchanged curve re-programmed: got %d calls, want 1", len(be.writes))
	}

	// Change the bound curve (lower the max anchor) → re-program.
	newCfg := curveSinkTestConfig()
	newCfg.Curves[0].Points[2].PWM = 200
	var ptr atomic.Pointer[config.Config]
	ptr.Store(newCfg)
	c.cfg = &ptr
	c.programCurveSink(be)
	if len(be.writes) != 2 {
		t.Fatalf("changed curve not re-programmed: got %d calls, want 2", len(be.writes))
	}
}

// TestProgramCurveSink_FanBoundsClampPercent: a max_pwm cap on the fan caps
// the programmed curve's top percentage — the firmware can't be told to exceed
// the operator's limit.
func TestProgramCurveSink_FanBoundsClampPercent(t *testing.T) {
	be := &fakeCurveSink{id: curveSinkTestPath, caps: hal.CapRead | hal.CapRestore | hal.CapWriteCurve}
	cfg := curveSinkTestConfig()
	cfg.Fans[0].MaxPWM = 128 // ~50%
	c := newCurveSinkController(be, cfg)

	c.programCurveSink(be)
	if len(be.writes) != 1 {
		t.Fatalf("got %d calls, want 1", len(be.writes))
	}
	for _, p := range be.writes[0] {
		if p.Pct > pwmToPct(128) {
			t.Errorf("pct %d exceeds the fan max_pwm cap (%d%%)", p.Pct, pwmToPct(128))
		}
	}
}

// TestProgramCurveSink_ManualOverrideFlatCurve: a manual fixed-duty control
// programs a flat curve at the manual percentage.
func TestProgramCurveSink_ManualOverrideFlatCurve(t *testing.T) {
	be := &fakeCurveSink{id: curveSinkTestPath, caps: hal.CapRead | hal.CapRestore | hal.CapWriteCurve}
	cfg := curveSinkTestConfig()
	manual := uint8(153) // 60%
	cfg.Controls[0].ManualPWM = &manual
	c := newCurveSinkController(be, cfg)

	c.programCurveSink(be)
	if len(be.writes) != 1 {
		t.Fatalf("got %d calls, want 1", len(be.writes))
	}
	want := pwmToPct(153)
	for _, p := range be.writes[0] {
		if p.Pct != want {
			t.Errorf("manual flat curve pct = %d, want %d", p.Pct, want)
		}
	}
}

// TestProgramCurveSink_ShadowSuppressesWrite: in shadow mode no curve is
// programmed (the operator's controller stays in charge).
func TestProgramCurveSink_ShadowSuppressesWrite(t *testing.T) {
	be := &fakeCurveSink{id: curveSinkTestPath, caps: hal.CapRead | hal.CapRestore | hal.CapWriteCurve}
	cfg := curveSinkTestConfig()
	cfg.Apply.Shadow = true
	c := newCurveSinkController(be, cfg)

	c.programCurveSink(be)
	if len(be.writes) != 0 {
		t.Errorf("shadow mode programmed a curve (%d calls); must suppress", len(be.writes))
	}
}

// TestProgramCurveSink_RetriesAfterError: a failed WriteCurve leaves the
// fingerprint unset so the next tick retries rather than wedging.
func TestProgramCurveSink_RetriesAfterError(t *testing.T) {
	be := &fakeCurveSink{
		id: curveSinkTestPath, caps: hal.CapRead | hal.CapRestore | hal.CapWriteCurve,
		writeErr: errors.New("boom"),
	}
	c := newCurveSinkController(be, curveSinkTestConfig())

	c.programCurveSink(be)
	if c.hasProgrammedCurve {
		t.Error("failed program must leave fingerprint unset for retry")
	}
	// Clear the error and retry: now it should succeed and record the sig.
	be.writeErr = nil
	c.programCurveSink(be)
	if !c.hasProgrammedCurve {
		t.Error("successful retry must record the fingerprint")
	}
}
