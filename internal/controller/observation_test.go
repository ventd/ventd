package controller

import (
	"context"
	"os"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/watchdog"
)

func cfgAtomicPtr(cfg *config.Config) *atomic.Pointer[config.Config] {
	p := &atomic.Pointer[config.Config]{}
	p.Store(cfg)
	return p
}

// TestEmitObservation_PopulatesSensorReadings is the v0.5.8.1 plumbing
// contract: after a successful PWM write, the controller emits one
// ObsRecord whose SensorReadings field carries a clone of the per-tick
// sensor map (name → °C). Without this, Layer-A conf_A coverage cannot
// be computed and Layer-C marginal-benefit can never see real ΔT.
func TestEmitObservation_PopulatesSensorReadings(t *testing.T) {
	ff := newFakeFan(t)
	if err := os.WriteFile(ff.tempPath, []byte("60000\n"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)

	var captured *ObsRecord
	appendFn := func(rec *ObsRecord) { captured = rec }
	labelFn := func() string { return "test-label" }

	logger := silentLogger()
	wd := watchdog.New(logger)
	c := New(
		"cpu fan", "cpu_curve",
		ff.pwmPath, "hwmon",
		cfgAtomicPtr(cfg), wd, &stubCal{}, logger,
		WithObservation(appendFn, labelFn),
	)
	c.tick()

	if captured == nil {
		t.Fatal("emitObservation did not fire — obsAppend was never called")
	}
	if got, want := captured.SignatureLabel, "test-label"; got != want {
		t.Errorf("SignatureLabel: got %q, want %q", got, want)
	}
	if captured.SensorReadings == nil {
		t.Fatal("SensorReadings is nil — controller did not populate the per-tick map")
	}
	v, ok := captured.SensorReadings["cpu"]
	if !ok {
		t.Fatalf("SensorReadings missing key %q; got map %v", "cpu", captured.SensorReadings)
	}
	if v < 59 || v > 61 {
		t.Errorf("SensorReadings[cpu] = %.2f, want ~60.0 (fixture pre-set)", v)
	}
}

// TestEmitObservation_ClonesNotAliasesRawBuf protects the controller hot
// loop from a use-after-tick bug: emitObservation must clone rawSensorsBuf
// into ObsRecord.SensorReadings, not pass a reference. Otherwise the next
// tick (which mutates rawSensorsBuf in place) would race the writer.
func TestEmitObservation_ClonesNotAliasesRawBuf(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)

	var captured *ObsRecord
	appendFn := func(rec *ObsRecord) { captured = rec }

	logger := silentLogger()
	wd := watchdog.New(logger)
	c := New(
		"cpu fan", "cpu_curve",
		ff.pwmPath, "hwmon",
		cfgAtomicPtr(cfg), wd, &stubCal{}, logger,
		WithObservation(appendFn, nil),
	)
	c.tick()

	if captured == nil || captured.SensorReadings == nil {
		t.Fatal("expected captured ObsRecord with SensorReadings populated")
	}
	pre := captured.SensorReadings["cpu"]

	// Simulate the next tick mutating the buffer in place. The captured
	// record's map must NOT change.
	c.rawSensorsBuf["cpu"] = 999.0
	if got := captured.SensorReadings["cpu"]; got != pre {
		t.Errorf("captured SensorReadings aliased rawSensorsBuf: pre=%.2f post=%.2f", pre, got)
	}
}

// TestEmitObservation_NilWhenObsHookAbsent verifies the v0.5.4 nil-safe
// contract is preserved: with no WithObservation option, the controller
// behaves exactly as it did before — no allocation, no closure call.
func TestEmitObservation_NilWhenObsHookAbsent(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
	c.tick() // must not panic; obsAppend is nil
}

// fakeRPMBackend captures Read responses so we can assert RPM ends up in
// ObsRecord.RPM (issue #1047).
type fakeRPMBackend struct {
	rpm uint16
	ok  bool
	pwm uint8
}

func (f *fakeRPMBackend) Enumerate(_ context.Context) ([]hal.Channel, error) { return nil, nil }
func (f *fakeRPMBackend) Read(_ hal.Channel) (hal.Reading, error) {
	return hal.Reading{PWM: f.pwm, RPM: f.rpm, OK: f.ok}, nil
}
func (f *fakeRPMBackend) Write(_ hal.Channel, _ uint8) error { return nil }
func (f *fakeRPMBackend) Restore(_ hal.Channel) error        { return nil }
func (f *fakeRPMBackend) Close() error                       { return nil }
func (f *fakeRPMBackend) Name() string                       { return "fake-rpm" }

// TestEmitObservation_PopulatesRPMFromBackend pins issue #1047
// (RULE-CTRL-OBS-RPM-01): the observation record's RPM must come from the
// HAL backend's Read result, not the previous hard-coded -1 sentinel.
// Smart-mode's fallback-tier classifier (R8) keys ConfA ceiling on whether
// real tach data exists; RPM=-1 kept every channel pinned at tier 7.
func TestEmitObservation_PopulatesRPMFromBackend(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)

	var captured *ObsRecord
	appendFn := func(rec *ObsRecord) { captured = rec }

	logger := silentLogger()
	wd := watchdog.New(logger)
	c := New(
		"cpu fan", "cpu_curve",
		ff.pwmPath, "hwmon",
		cfgAtomicPtr(cfg), wd, &stubCal{}, logger,
		WithObservation(appendFn, nil),
	)
	// Swap in a fake backend that returns a known RPM. The fan_input
	// sysfs file isn't present in newFakeFan's fixture, so without the
	// backend swap the production backend's Read would land in the
	// error path and RPM would be -1.
	c.backend = &fakeRPMBackend{rpm: 1234, ok: true}

	c.tick()

	if captured == nil {
		t.Fatal("emitObservation did not fire")
	}
	if captured.RPM != 1234 {
		t.Errorf("ObsRecord.RPM = %d, want 1234 (issue #1047: tach read not wired)", captured.RPM)
	}
}

// TestEmitObservation_RPMMinusOneOnBackendError pins the negative case for
// issue #1047: a backend Read that fails (OK=false) records RPM=-1 so smart-
// mode consumers can distinguish "tach unavailable" from real readings.
func TestEmitObservation_RPMMinusOneOnBackendError(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)

	var captured *ObsRecord
	appendFn := func(rec *ObsRecord) { captured = rec }

	logger := silentLogger()
	wd := watchdog.New(logger)
	c := New(
		"cpu fan", "cpu_curve",
		ff.pwmPath, "hwmon",
		cfgAtomicPtr(cfg), wd, &stubCal{}, logger,
		WithObservation(appendFn, nil),
	)
	c.backend = &fakeRPMBackend{ok: false}

	c.tick()

	if captured == nil {
		t.Fatal("emitObservation did not fire")
	}
	if captured.RPM != -1 {
		t.Errorf("ObsRecord.RPM on failed Read = %d, want -1 (tach-unavailable sentinel)", captured.RPM)
	}
}
