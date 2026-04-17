package calibrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hal"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/testfixture/faketime"
	"github.com/ventd/ventd/internal/watchdog"
)

// makeFakeHwmon creates a temp directory with pwm1, pwm1_enable, fan1_input
// pre-populated. The hwmon helpers in internal/hwmon use os.ReadFile/WriteFile
// directly, so a flat temp dir is indistinguishable from a real sysfs entry.
func makeFakeHwmon(t *testing.T, initialPWM, initialEnable, initialRPM int) string {
	t.Helper()
	dir := t.TempDir()
	pwmPath := filepath.Join(dir, "pwm1")
	enablePath := filepath.Join(dir, "pwm1_enable")
	rpmPath := filepath.Join(dir, "fan1_input")
	mustWrite := func(p string, v int) {
		if err := os.WriteFile(p, []byte(itoa(v)+"\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mustWrite(pwmPath, initialPWM)
	mustWrite(enablePath, initialEnable)
	mustWrite(rpmPath, initialRPM)
	return pwmPath
}

func itoa(v int) string { return jsonNumber(int64(v)) }

func jsonNumber(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// makeHwmonResolver returns a ChannelResolver backed by a fresh halhwmon.Backend.
// The resolver builds a hal.Channel from the fan's PWMPath and ControlKind so
// tests drive calibration through the HAL layer without a real sysfs enumeration.
func makeHwmonResolver(t *testing.T) (ChannelResolver, *halhwmon.Backend) {
	t.Helper()
	backend := halhwmon.NewBackend(nil)
	return func(_ context.Context, fan *config.Fan) (hal.FanBackend, hal.Channel, error) {
		isRPM := fan.ControlKind == "rpm_target"
		caps := hal.CapRead | hal.CapWritePWM | hal.CapRestore
		if isRPM {
			caps = hal.CapRead | hal.CapWriteRPMTarget | hal.CapRestore
		}
		return backend, hal.Channel{
			ID:   fan.PWMPath,
			Caps: caps,
			Opaque: halhwmon.State{
				PWMPath:    fan.PWMPath,
				RPMTarget:  isRPM,
				OrigEnable: -1,
			},
		}, nil
	}, backend
}

func TestResumeFromCheckpoint(t *testing.T) {
	pwmPath := makeFakeHwmon(t, 0, 2, 1500)
	calPath := filepath.Join(t.TempDir(), "calibration.json")

	const resumeAt = 15
	prev := Result{
		PWMPath:        pwmPath,
		StartPWM:       50,
		MinRPM:         800,
		MaxRPM:         1500,
		Curve:          []PWMRPMPoint{{PWM: 50, RPM: 800}, {PWM: 64, RPM: 950}},
		Partial:        true,
		CompletedSteps: resumeAt,
	}
	env := onDiskEnvelope{
		SchemaVersion: SchemaVersion,
		Results:       map[string]Result{pwmPath: prev},
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := os.WriteFile(calPath, data, 0644); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	m := New(calPath, logger, nil)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)

	// load() should have populated the partial result.
	loaded, ok := m.results[pwmPath]
	if !ok {
		t.Fatal("load() did not populate results")
	}
	if !loaded.Partial || loaded.CompletedSteps != resumeAt {
		t.Fatalf("loaded result wrong: partial=%v completed=%d", loaded.Partial, loaded.CompletedSteps)
	}

	// Pre-cancelled context: runSync hits ctx.Err() at the top of the first
	// loop iteration after restoring resume state, so we observe both the
	// resume log and a snapshot reflecting the resumed step.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fan := &config.Fan{
		Name:    "test",
		Type:    "hwmon",
		PWMPath: pwmPath,
		MinPWM:  0,
		MaxPWM:  255,
	}
	result, err := m.RunSync(ctx, fan)
	if err == nil {
		t.Fatalf("expected ctx.Canceled error, got nil; result=%+v", result)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "resuming from checkpoint") {
		t.Errorf("expected resume log; got:\n%s", logs)
	}
	if !strings.Contains(logs, "completed_steps=15") {
		t.Errorf("expected completed_steps=15 in log; got:\n%s", logs)
	}

	if result.CompletedSteps != resumeAt {
		t.Errorf("expected returned CompletedSteps=%d, got %d", resumeAt, result.CompletedSteps)
	}
	if result.StartPWM != 50 || result.MinRPM != 800 || result.MaxRPM != 1500 {
		t.Errorf("expected resume to restore prior counters; got start=%d min=%d max=%d",
			result.StartPWM, result.MinRPM, result.MaxRPM)
	}
	if len(result.Curve) != len(prev.Curve) {
		t.Errorf("expected curve to retain pre-checkpoint points (%d); got %d",
			len(prev.Curve), len(result.Curve))
	}
}

func TestAbortRestoresPWM(t *testing.T) {
	pwmPath := makeFakeHwmon(t, 100, 2, 1500)
	calPath := filepath.Join(t.TempDir(), "calibration.json")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wd := watchdog.New(logger)
	// Simulate the daemon-startup registration (main.go:110): captures the
	// pre-calibration pwm_enable so wd.Restore() can put it back later.
	wd.Register(pwmPath, "hwmon")
	m := New(calPath, logger, wd)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)

	fan := &config.Fan{
		Name:    "test",
		Type:    "hwmon",
		PWMPath: pwmPath,
		MinPWM:  0,
		MaxPWM:  255,
	}

	if err := m.Start(fan); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the goroutine to actually be in the sweep (in ctxSleep).
	if !waitFor(t, 500*time.Millisecond, func() bool { return m.IsCalibrating(pwmPath) }) {
		t.Fatal("calibration did not start within 500ms")
	}

	abortAt := time.Now()
	m.Abort(pwmPath)

	if !waitFor(t, 2*time.Second, func() bool { return !m.IsCalibrating(pwmPath) }) {
		t.Fatalf("calibration did not terminate within 2s of abort (elapsed %v)", time.Since(abortAt))
	}
	elapsed := time.Since(abortAt)
	if elapsed > 2*time.Second {
		t.Errorf("abort path took %v (want < 2s)", elapsed)
	}

	// runSync's defer writes minPWM (0) on exit. Verify the safe-PWM landed.
	pwmData, err := os.ReadFile(pwmPath)
	if err != nil {
		t.Fatalf("read pwm: %v", err)
	}
	if got := strings.TrimSpace(string(pwmData)); got != "0" {
		t.Errorf("expected pwm=0 after abort defer, got %q", got)
	}

	// State should be "aborted".
	var foundState string
	for _, s := range m.AllStatus() {
		if s.PWMPath == pwmPath {
			foundState = s.State
		}
	}
	if foundState != "aborted" {
		t.Errorf("expected state=aborted, got %q", foundState)
	}

	// Daemon-exit Restore (simulated): the per-sweep entry was deregistered
	// cleanly, so the surviving startup entry drives pwm_enable back to its
	// captured value (2).
	wd.Restore()
	enableData, err := os.ReadFile(pwmPath + "_enable")
	if err != nil {
		t.Fatalf("read pwm_enable: %v", err)
	}
	if got := strings.TrimSpace(string(enableData)); got != "2" {
		t.Errorf("expected pwm_enable=2 after Restore, got %q", got)
	}
}

func TestSchemaMismatchSurfacesDiagnostic(t *testing.T) {
	calPath := filepath.Join(t.TempDir(), "calibration.json")
	const futurePath = "/sys/class/hwmon/hwmon0/pwm1"
	future := []byte(`{
  "schema_version": 999,
  "results": {
    "` + futurePath + `": {
      "pwm_path": "` + futurePath + `",
      "start_pwm": 50,
      "max_rpm": 1500
    }
  }
}`)
	if err := os.WriteFile(calPath, future, 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	pwmStat0, _ := os.Stat(calPath)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(calPath, logger, nil)

	diags := m.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d (%+v)", len(diags), diags)
	}
	if diags[0].Remediation == nil || diags[0].Remediation.AutoFixID != AutoFixRecalibrate {
		t.Errorf("expected Remediation.AutoFixID=%q, got %+v", AutoFixRecalibrate, diags[0].Remediation)
	}
	if diags[0].Severity != "warn" {
		t.Errorf("expected severity=warn, got %q", diags[0].Severity)
	}

	found := false
	for _, p := range diags[0].Affected {
		if p == futurePath {
			found = true
		}
	}
	if !found {
		t.Errorf("affected list missing pwm path; got %v", diags[0].Affected)
	}

	// Results withheld so the daemon falls back to live-config defaults.
	if r := m.AllResults(); len(r) != 0 {
		t.Errorf("expected empty results when schema is future, got %d entries", len(r))
	}

	// Sanity: load must not have rewritten the file (no PWM writes either,
	// implicit because the test passes nil watchdog and never calls runSync).
	pwmStat1, _ := os.Stat(calPath)
	if !pwmStat0.ModTime().Equal(pwmStat1.ModTime()) {
		t.Errorf("load mutated calibration.json; mtime moved %v \u2192 %v",
			pwmStat0.ModTime(), pwmStat1.ModTime())
	}
}

func TestSchemaMigrationFromBareMap(t *testing.T) {
	calPath := filepath.Join(t.TempDir(), "calibration.json")
	const legacyPath = "/sys/class/hwmon/hwmon0/pwm1"
	bare := []byte(`{
  "` + legacyPath + `": {
    "pwm_path": "` + legacyPath + `",
    "start_pwm": 50,
    "stop_pwm": 30,
    "max_rpm": 1500,
    "min_rpm": 800
  }
}`)
	if err := os.WriteFile(calPath, bare, 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(calPath, logger, nil)

	results := m.AllResults()
	if len(results) != 1 {
		t.Fatalf("expected 1 result after migration, got %d", len(results))
	}
	r, ok := results[legacyPath]
	if !ok {
		t.Fatal("expected legacy pwm path in results")
	}
	if r.StartPWM != 50 || r.StopPWM != 30 || r.MaxRPM != 1500 || r.MinRPM != 800 {
		t.Errorf("migrated fields don't match: %+v", r)
	}

	if d := m.Diagnostics(); len(d) != 0 {
		t.Errorf("expected no diagnostics for legacy migration, got %v", d)
	}

	// Next save() must produce a v1 envelope, not a bare map.
	m.save()
	data, err := os.ReadFile(calPath)
	if err != nil {
		t.Fatalf("read after save: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("post-save not parseable as JSON: %v\n%s", err, data)
	}
	if _, ok := probe["schema_version"]; !ok {
		t.Errorf("expected schema_version in saved file; got:\n%s", data)
	}
	if _, ok := probe["results"]; !ok {
		t.Errorf("expected results in saved file; got:\n%s", data)
	}

	var env onDiskEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("envelope round-trip failed: %v", err)
	}
	if env.SchemaVersion != SchemaVersion {
		t.Errorf("expected schema_version=%d, got %d", SchemaVersion, env.SchemaVersion)
	}
	if got, ok := env.Results[legacyPath]; !ok || got.StartPWM != 50 {
		t.Errorf("envelope round-trip lost legacy data: %+v", env.Results)
	}
}

// TestConcurrentAllResults exercises AllResults under -race while a sweep
// progresses. Regression coverage for two related bugs:
//  1. snapshot() in runSync used to alias the live `points` slice into the
//     stored Result; the next append() could race a concurrent reader.
//  2. AllResults returned the same slice header to every caller; a caller
//     mutating Curve would corrupt internal state seen by the next caller.
//
// Both fixes deep-copy Curve. The test pre-seeds a partial result so checkpoints
// land in m.results almost immediately, then pounds AllResults from several
// goroutines while runSync is in its loop.
func TestConcurrentAllResults(t *testing.T) {
	pwmPath := makeFakeHwmon(t, 0, 2, 1500)
	calPath := filepath.Join(t.TempDir(), "calibration.json")

	seed := Result{
		PWMPath:        pwmPath,
		StartPWM:       50,
		MaxRPM:         1500,
		MinRPM:         800,
		Partial:        true,
		CompletedSteps: 1,
		Curve:          []PWMRPMPoint{{PWM: 50, RPM: 800}},
	}
	env := onDiskEnvelope{SchemaVersion: SchemaVersion, Results: map[string]Result{pwmPath: seed}}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := os.WriteFile(calPath, data, 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wd := watchdog.New(logger)
	wd.Register(pwmPath, "hwmon")
	m := New(calPath, logger, wd)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)

	fan := &config.Fan{
		Name:    "test",
		Type:    "hwmon",
		PWMPath: pwmPath,
		MinPWM:  0,
		MaxPWM:  255,
	}
	if err := m.Start(fan); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(t, 500*time.Millisecond, func() bool { return m.IsCalibrating(pwmPath) }) {
		t.Fatal("calibration did not start")
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					for _, r := range m.AllResults() {
						for _, p := range r.Curve {
							_ = p
						}
					}
				}
			}
		}()
	}

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	m.Abort(pwmPath)
	if !waitFor(t, 2*time.Second, func() bool { return !m.IsCalibrating(pwmPath) }) {
		t.Fatal("calibration did not terminate within 2s of abort")
	}

	// Mutation isolation: writing through one snapshot must not leak into the next.
	a := m.AllResults()[pwmPath]
	if len(a.Curve) == 0 {
		t.Fatal("expected non-empty curve from AllResults")
	}
	original := a.Curve[0]
	a.Curve[0] = PWMRPMPoint{PWM: 99, RPM: 0}
	b := m.AllResults()[pwmPath]
	if len(b.Curve) == 0 || b.Curve[0] != original {
		t.Errorf("AllResults aliased: caller mutation leaked. original=%+v, leaked=%+v",
			original, b.Curve[0])
	}
}

// TestResumeDiscardedOnFingerprintMismatch seeds a partial checkpoint whose
// fan_fingerprint doesn't match the live fan (MaxPWM changed). The sweep must
// ignore the checkpoint and start fresh — an aggressive resume would apply a
// curve shaped for hardware that no longer exists.
func TestResumeDiscardedOnFingerprintMismatch(t *testing.T) {
	pwmPath := makeFakeHwmon(t, 0, 2, 1500)
	calPath := filepath.Join(t.TempDir(), "calibration.json")

	prev := Result{
		PWMPath:        pwmPath,
		StartPWM:       50,
		MinRPM:         800,
		MaxRPM:         1500,
		Curve:          []PWMRPMPoint{{PWM: 50, RPM: 800}},
		Partial:        true,
		CompletedSteps: 10,
		FanFingerprint: "hwmon|" + pwmPath + "|0|200", // old MaxPWM=200
	}
	env := onDiskEnvelope{SchemaVersion: SchemaVersion, Results: map[string]Result{pwmPath: prev}}
	data, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(calPath, data, 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	m := New(calPath, logger, nil)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fan := &config.Fan{Name: "test", Type: "hwmon", PWMPath: pwmPath, MinPWM: 0, MaxPWM: 255}
	result, err := m.RunSync(ctx, fan)
	if err == nil {
		t.Fatal("expected ctx cancel error")
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "fingerprint mismatch") {
		t.Errorf("expected fingerprint mismatch log; got:\n%s", logs)
	}
	if strings.Contains(logs, "resuming from checkpoint") {
		t.Errorf("did not expect resume log; got:\n%s", logs)
	}
	if result.StartPWM != 0 || result.MaxRPM != 0 || len(result.Curve) != 0 {
		t.Errorf("expected fresh state, got %+v", result)
	}
}

// TestAbortPersistsTerminalState ensures that after an abort, the on-disk
// record is a terminal aborted state (Partial=false, Aborted=true). A daemon
// restart must NOT try to resume a user-aborted sweep.
func TestAbortPersistsTerminalState(t *testing.T) {
	pwmPath := makeFakeHwmon(t, 100, 2, 1500)
	calPath := filepath.Join(t.TempDir(), "calibration.json")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wd := watchdog.New(logger)
	wd.Register(pwmPath, "hwmon")
	m := New(calPath, logger, wd)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)

	fan := &config.Fan{Name: "test", Type: "hwmon", PWMPath: pwmPath, MinPWM: 0, MaxPWM: 255}
	if err := m.Start(fan); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(t, 500*time.Millisecond, func() bool { return m.IsCalibrating(pwmPath) }) {
		t.Fatal("calibration did not start")
	}

	m.Abort(pwmPath)
	if !waitFor(t, 2*time.Second, func() bool { return !m.IsCalibrating(pwmPath) }) {
		t.Fatal("calibration did not terminate")
	}

	// Re-load from disk and confirm the on-disk record is terminal.
	data, err := os.ReadFile(calPath)
	if err != nil {
		t.Fatalf("read calibration.json: %v", err)
	}
	var env onDiskEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	r, ok := env.Results[pwmPath]
	if !ok {
		t.Fatalf("no record for %s on disk", pwmPath)
	}
	if r.Partial {
		t.Errorf("expected Partial=false on disk after abort, got true")
	}
	if !r.Aborted {
		t.Errorf("expected Aborted=true on disk after abort")
	}
	if r.CompletedSteps != 0 || r.DownRampPWM != 0 {
		t.Errorf("expected resume anchors cleared, got steps=%d down=%d",
			r.CompletedSteps, r.DownRampPWM)
	}

	// Startup-equivalent: a fresh Manager loading this file must not resume.
	var logBuf bytes.Buffer
	logger2 := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	m2 := New(calPath, logger2, nil)
	r2, _ := makeHwmonResolver(t)
	m2.SetChannelResolver(r2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m2.RunSync(ctx, fan); err == nil {
		t.Fatal("expected ctx cancel")
	}
	if strings.Contains(logBuf.String(), "resuming from checkpoint") {
		t.Errorf("aborted record was resumed on next startup; logs:\n%s", logBuf.String())
	}
}

// makeFakeRPMTargetHwmon creates a temp directory with fan1_target,
// fan1_input, fan1_min, fan1_max, and pwm1_enable populated. rpmResponder, if
// non-nil, is invoked whenever fan*_target is written and should compute the
// fan*_input value the next read will return. This lets tests drive the
// settle loop: a clean responder echoes target\u2192actual for instant settle; a
// non-responsive responder holds fan1_input at a fixed value to force
// "never settled" behaviour.
func makeFakeRPMTargetHwmon(t *testing.T, initialEnable int, minRPM, maxRPM, initialActual int) (targetPath string, inputPath string) {
	t.Helper()
	dir := t.TempDir()
	targetPath = filepath.Join(dir, "fan1_target")
	inputPath = filepath.Join(dir, "fan1_input")
	enablePath := filepath.Join(dir, "pwm1_enable")
	minPath := filepath.Join(dir, "fan1_min")
	maxPath := filepath.Join(dir, "fan1_max")
	mustWrite := func(p string, v int) {
		if err := os.WriteFile(p, []byte(itoa(v)+"\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mustWrite(targetPath, minRPM)
	mustWrite(inputPath, initialActual)
	mustWrite(enablePath, initialEnable)
	mustWrite(minPath, minRPM)
	mustWrite(maxPath, maxRPM)
	return targetPath, inputPath
}

// rpmResponderLoop runs in a goroutine: reads fan1_target periodically and
// mirrors its value into fan1_input so the calibrate settle loop observes a
// "clean" fan that instantly matches setpoint. Stops when ctx is done.
func rpmResponderLoop(ctx context.Context, targetPath, inputPath string) {
	go func() {
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				data, err := os.ReadFile(targetPath)
				if err != nil {
					continue
				}
				_ = os.WriteFile(inputPath, data, 0644)
			}
		}
	}()
}

// TestSelectSweepMode — capability-based mode dispatch. ControlKind="rpm_target"
// maps to the RPM sweep; everything else defaults to PWM.
func TestSelectSweepMode(t *testing.T) {
	cases := []struct {
		name string
		fan  *config.Fan
		want string
	}{
		{"pwm default", &config.Fan{Type: "hwmon", PWMPath: "/sys/.../pwm1"}, SweepModePWM},
		{"explicit pwm ControlKind", &config.Fan{Type: "hwmon", ControlKind: "pwm"}, SweepModePWM},
		{"rpm_target ControlKind", &config.Fan{Type: "hwmon", ControlKind: "rpm_target"}, SweepModeRPM},
		{"nvidia is pwm", &config.Fan{Type: "nvidia", PWMPath: "0"}, SweepModePWM},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectSweepMode(tc.fan); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRPMSweepHappyPath runs the RPM-target calibration against a clean mock
// fan (fan1_input mirrors fan1_target via responder). All 10 steps should
// settle, and the final Result should record SweepMode="rpm", a populated
// RPMCurve, and MinRPM/MaxRPM pulled from the first/last settled samples.
func TestRPMSweepHappyPath(t *testing.T) {
	targetPath, inputPath := makeFakeRPMTargetHwmon(t, 2, 500, 2500, 500)
	calPath := filepath.Join(t.TempDir(), "calibration.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rpmResponderLoop(ctx, targetPath, inputPath)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(calPath, logger, nil)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)

	fan := &config.Fan{
		Name:        "amd",
		Type:        "hwmon",
		ControlKind: "rpm_target",
		PWMPath:     targetPath,
	}

	result, err := m.RunSync(ctx, fan)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if result.SweepMode != SweepModeRPM {
		t.Errorf("expected SweepMode=%q, got %q", SweepModeRPM, result.SweepMode)
	}
	if len(result.RPMCurve) != 10 {
		t.Fatalf("expected 10 rpm samples, got %d", len(result.RPMCurve))
	}
	settled := 0
	for _, p := range result.RPMCurve {
		if p.Settled {
			settled++
		}
	}
	if settled != 10 {
		t.Errorf("expected all 10 samples settled under clean responder, got %d", settled)
	}
	if result.MinRPM <= 0 || result.MaxRPM <= 0 || result.MinRPM > result.MaxRPM {
		t.Errorf("expected sensible MinRPM/MaxRPM, got min=%d max=%d", result.MinRPM, result.MaxRPM)
	}
	if result.Partial {
		t.Errorf("final result should not be Partial")
	}
	// pwm1_enable should have been taken to manual control.
	enableData, _ := os.ReadFile(filepath.Dir(targetPath) + "/pwm1_enable")
	if strings.TrimSpace(string(enableData)) != "1" {
		t.Errorf("expected pwm1_enable=1 after calibration (manual control), got %q", enableData)
	}
}

// TestRPMSweepAbortPersistsTerminalState — abort mid-RPM-sweep. The on-disk
// record must carry SweepMode="rpm", Aborted=true, Partial=false so that
// restart doesn't attempt to resume a user-cancelled sweep.
//
// Migrated from fixed time.Sleep to faketime.WaitUntil: the original 700ms
// sleep was dead weight — no assertion inspects RPMCurve samples. Removing it
// and polling via WaitUntil cuts wall-clock from ~730ms to <50ms.
func TestRPMSweepAbortPersistsTerminalState(t *testing.T) {
	// No responder — fan1_input never matches target, so every step spends the
	// full 5s settle window. Plenty of headroom to abort mid-sweep.
	targetPath, _ := makeFakeRPMTargetHwmon(t, 2, 500, 2500, 100)
	calPath := filepath.Join(t.TempDir(), "calibration.json")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wd := watchdog.New(logger)
	wd.Register(targetPath, "hwmon")
	m := New(calPath, logger, wd)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)

	fan := &config.Fan{
		Name:        "amd",
		Type:        "hwmon",
		ControlKind: "rpm_target",
		PWMPath:     targetPath,
	}

	if err := m.Start(fan); err != nil {
		t.Fatalf("Start: %v", err)
	}
	faketime.WaitUntil(t, func() bool { return m.IsCalibrating(targetPath) }, 500*time.Millisecond)

	// Abort immediately — the terminal-state assertions (SweepMode, Partial,
	// Aborted, CompletedSteps) do not depend on any sweep step completing.
	m.Abort(targetPath)
	faketime.WaitUntil(t, func() bool { return !m.IsCalibrating(targetPath) }, 2*time.Second)
	// running=false is set in runSyncRPM's defer; m.save() runs after in run().
	// Poll for the file to appear so we don't race on slow arm64 runners.
	faketime.WaitUntil(t, func() bool { _, err := os.Stat(calPath); return err == nil }, 500*time.Millisecond)

	data, err := os.ReadFile(calPath)
	if err != nil {
		t.Fatalf("read calibration.json: %v", err)
	}
	var env onDiskEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	r, ok := env.Results[targetPath]
	if !ok {
		t.Fatalf("no record for %s on disk", targetPath)
	}
	if r.SweepMode != SweepModeRPM {
		t.Errorf("expected SweepMode=%q on aborted record, got %q", SweepModeRPM, r.SweepMode)
	}
	if r.Partial {
		t.Error("expected Partial=false after abort")
	}
	if !r.Aborted {
		t.Error("expected Aborted=true after abort")
	}
	if r.CompletedSteps != 0 {
		t.Errorf("expected CompletedSteps=0 after abort, got %d", r.CompletedSteps)
	}
}

// TestLoadV1EnvelopeUnderV2 — a checkpoint file written by a v1 daemon (no
// SweepMode field on records) must load cleanly under the v2 loader with
// SweepMode defaulted to "pwm", and a subsequent resume must treat it as a
// PWM sweep.
func TestLoadV1EnvelopeUnderV2(t *testing.T) {
	pwmPath := makeFakeHwmon(t, 0, 2, 1500)
	calPath := filepath.Join(t.TempDir(), "calibration.json")

	// Hand-craft a v1-shaped envelope: schema_version=1 and records lack the
	// sweep_mode field entirely. Fingerprint matches the live fan so the
	// resume path is exercised (not discarded as mismatched).
	fan := &config.Fan{Name: "test", Type: "hwmon", PWMPath: pwmPath, MinPWM: 0, MaxPWM: 255}
	fp := fanFingerprint(fan)
	const resumeAt = 15
	v1 := `{
  "schema_version": 1,
  "results": {
    "` + pwmPath + `": {
      "pwm_path": "` + pwmPath + `",
      "start_pwm": 50,
      "max_rpm": 1500,
      "min_rpm": 800,
      "partial": true,
      "completed_steps": ` + itoa(resumeAt) + `,
      "fan_fingerprint": "` + fp + `"
    }
  }
}`
	if err := os.WriteFile(calPath, []byte(v1), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	m := New(calPath, logger, nil)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)

	loaded, ok := m.results[pwmPath]
	if !ok {
		t.Fatal("v1 record did not load")
	}
	if loaded.SweepMode != SweepModePWM {
		t.Errorf("expected SweepMode defaulted to %q on v1 record, got %q",
			SweepModePWM, loaded.SweepMode)
	}
	if !loaded.Partial || loaded.CompletedSteps != resumeAt {
		t.Errorf("partial/completed_steps not preserved: %+v", loaded)
	}

	// Resume must use the PWM path, not RPM.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := m.RunSync(ctx, fan)
	if err == nil {
		t.Fatal("expected ctx.Canceled")
	}
	if result.SweepMode != SweepModePWM {
		t.Errorf("resumed result SweepMode=%q, expected pwm", result.SweepMode)
	}
	if !strings.Contains(logBuf.String(), "resuming from checkpoint") {
		t.Errorf("expected resume log; got:\n%s", logBuf.String())
	}
}

// waitFor polls cond every 5ms up to timeout. Returns true once cond is true.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestRemapKey(t *testing.T) {
	const (
		oldPath = "/sys/class/hwmon/hwmon3/pwm1"
		newPath = "/sys/class/hwmon/hwmon4/pwm1"
	)
	seed := Result{PWMPath: oldPath, StartPWM: 60, MaxRPM: 1500}

	t.Run("remaps existing key", func(t *testing.T) {
		m := newManagerWith(t, map[string]Result{oldPath: seed})
		m.RemapKey(oldPath, newPath)

		all := m.AllResults()
		if _, present := all[oldPath]; present {
			t.Errorf("oldPath still present after remap")
		}
		got, ok := all[newPath]
		if !ok {
			t.Fatalf("newPath missing after remap; got keys %v", keys(all))
		}
		if got.PWMPath != newPath {
			t.Errorf("PWMPath inside Result = %q, want %q (must be rewritten)", got.PWMPath, newPath)
		}
		if got.StartPWM != seed.StartPWM || got.MaxRPM != seed.MaxRPM {
			t.Errorf("payload lost in remap: %+v", got)
		}
	})

	t.Run("missing oldPath is a no-op", func(t *testing.T) {
		m := newManagerWith(t, map[string]Result{newPath: seed})
		m.RemapKey(oldPath, newPath)

		all := m.AllResults()
		if len(all) != 1 {
			t.Fatalf("unexpected entries after no-op remap: %v", all)
		}
		if _, ok := all[newPath]; !ok {
			t.Errorf("newPath clobbered by no-op remap")
		}
	})

	t.Run("both keys present: new overwritten with old", func(t *testing.T) {
		existing := Result{PWMPath: newPath, StartPWM: 99, MaxRPM: 9999}
		m := newManagerWith(t, map[string]Result{oldPath: seed, newPath: existing})
		m.RemapKey(oldPath, newPath)

		all := m.AllResults()
		if _, present := all[oldPath]; present {
			t.Errorf("oldPath still present after remap")
		}
		got := all[newPath]
		if got.StartPWM != seed.StartPWM || got.MaxRPM != seed.MaxRPM {
			t.Errorf("RemapKey must overwrite the destination with the source payload; got %+v", got)
		}
	})
}

func TestSetDiagnosticStoreBackfillsPending(t *testing.T) {
	// Seed an on-disk calibration.json whose schema_version is in the future so
	// load() records a diagnostic. Then SetDiagnosticStore(nonNil) must
	// replay that diagnostic into the attached store.
	dir := t.TempDir()
	calPath := filepath.Join(dir, "calibration.json")
	envelope := map[string]any{
		"schema_version": SchemaVersion + 1,
		"results":        map[string]Result{},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := os.WriteFile(calPath, raw, 0o644); err != nil {
		t.Fatalf("write cal: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(calPath, logger, nil)

	diags := m.Diagnostics()
	if len(diags) == 0 {
		t.Fatal("expected load() to record a diagnostic for unsupported schema")
	}

	// Attaching nil is a no-op but must not panic.
	m.SetDiagnosticStore(nil)

	store := hwdiag.NewStore()
	m.SetDiagnosticStore(store)

	// The store should now contain the entry load() captured.
	snap := store.Snapshot(hwdiag.Filter{})
	if len(snap.Entries) != len(diags) {
		t.Errorf("store holds %d entries after attach, want %d", len(snap.Entries), len(diags))
	}
	if len(snap.Entries) > 0 && snap.Entries[0].ID != diags[0].ID {
		t.Errorf("store entry ID = %q, want %q", snap.Entries[0].ID, diags[0].ID)
	}
}

// newManagerWith returns a Manager pre-seeded with the given results. It uses
// an in-memory calibration path so save() writes into the test's tempdir.
func newManagerWith(t *testing.T, seed map[string]Result) *Manager {
	t.Helper()
	calPath := filepath.Join(t.TempDir(), "calibration.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(calPath, logger, nil)
	m.mu.Lock()
	for k, v := range seed {
		m.results[k] = v
	}
	m.mu.Unlock()
	return m
}

func keys(m map[string]Result) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
