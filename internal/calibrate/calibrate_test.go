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
	if diags[0].AutoFixID != AutoFixRecalibrate {
		t.Errorf("expected AutoFixID=%q, got %q", AutoFixRecalibrate, diags[0].AutoFixID)
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
		t.Errorf("load mutated calibration.json; mtime moved %v → %v",
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
