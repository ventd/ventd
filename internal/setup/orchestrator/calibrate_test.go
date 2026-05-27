package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
)

// seedCalibrateCheckpoint writes a CalibrateArtifact to the run-
// context's state dir so a downstream phase (currently ApplyPhase)
// can load it without running the sweep. Used by apply tests that
// need controlled calibrate output. Lived in verify_test.go before
// VerifyPhase was absorbed into CalibratePhase.
func seedCalibrateCheckpoint(t *testing.T, rc *RunContext, art CalibrateArtifact) {
	t.Helper()
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(art)
	state.Outcomes[(CalibratePhase{}).Name()] = Outcome{
		Phase:    (CalibratePhase{}).Name(),
		Status:   StatusSuccess,
		Artifact: raw,
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
}

// fanFixture stages a synthetic pwm + fan_input pair under a temp
// dir for tests that exercise the sustained-spin check inside
// sweepOne (or, historically, VerifyPhase.checkOne).
type fanFixture struct {
	pwmInitial uint8
	rpmAfter   int
}

func stageFan(t *testing.T, dir string, idx int, f fanFixture) (pwmPath, rpmPath string) {
	t.Helper()
	pwmPath = filepath.Join(dir, "pwm"+itoaInt(idx))
	rpmPath = filepath.Join(dir, "fan"+itoaInt(idx)+"_input")
	if err := os.WriteFile(pwmPath, []byte(itoaInt(int(f.pwmInitial))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rpmPath, []byte(itoaInt(f.rpmAfter)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return pwmPath, rpmPath
}

// itoaInt is the strconv.Itoa-free integer-to-string helper used by
// the test fixtures. Kept in this package so the test files don't
// take a strconv dependency.
func itoaInt(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// fakeCalibrator is the test seam for CalibratePhase. Per-PWMPath
// configured results + an attempt log for assertions.
type fakeCalibrator struct {
	results  map[string]calibrate.Result
	errors   map[string]error
	attempts []string
}

func (f *fakeCalibrator) Calibrate(_ context.Context, fan *config.Fan) (calibrate.Result, error) {
	f.attempts = append(f.attempts, fan.PWMPath)
	if err, ok := f.errors[fan.PWMPath]; ok {
		return calibrate.Result{}, err
	}
	if r, ok := f.results[fan.PWMPath]; ok {
		return r, nil
	}
	return calibrate.Result{StartPWM: 80, MaxRPM: 1500}, nil
}

func TestCalibratePhase_Name(t *testing.T) {
	if (CalibratePhase{}).Name() != "calibrate" {
		t.Error("Name() must be 'calibrate'")
	}
}

// recordingCalibrator captures the config.Fan each sweep received so a
// test can assert the Type + RPMPath the phase handed down.
type recordingCalibrator struct{ got []*config.Fan }

func (r *recordingCalibrator) Calibrate(_ context.Context, fan *config.Fan) (calibrate.Result, error) {
	r.got = append(r.got, fan)
	return calibrate.Result{StartPWM: 80, MaxRPM: 1500}, nil
}

// TestCalibratePhase_HALFanGetsBackendTypeAndOverlaidTach pins the
// calibrate-side #1376 wiring: a HAL fan must reach the Calibrator with
// Type set to its backend (so hal.Resolve picks the right backend, not
// hwmon) and RPMPath overlaid from RPMDetect (the probe leaves it empty
// for HAL fans whose tach is on another chip). Without either, the msiec
// sweep would resolve to the wrong backend or calibrate blind.
func TestCalibratePhase_HALFanGetsBackendTypeAndOverlaidTach(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	const chanID = "/sys/devices/platform/msi-ec"
	const tach = "/sys/class/hwmon/hwmon4/fan1_input"
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{PWMPath: chanID, Backend: "msiec", ChipName: "msiec", LabelHint: "MSI EC Fan"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: chanID, Polarity: "normal"}},
	})
	seedRPMDetectCheckpoint(t, rc, RPMDetectArtifact{
		Results: []RPMDetectFanResult{{PWMPath: chanID, ResolvedRPM: tach, Improved: true}},
	})

	rec := &recordingCalibrator{}
	out := (CalibratePhase{Calibrator: rec}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	if len(rec.got) != 1 {
		t.Fatalf("expected 1 sweep, got %d", len(rec.got))
	}
	if rec.got[0].Type != "msiec" {
		t.Errorf("Calibrate received Type=%q, want msiec (else hal.Resolve picks the wrong backend)", rec.got[0].Type)
	}
	if rec.got[0].RPMPath != tach {
		t.Errorf("Calibrate received RPMPath=%q, want the RPMDetect-overlaid tach %q", rec.got[0].RPMPath, tach)
	}
}

func TestCalibratePhase_NoCalibratorWiredFails(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	out := (CalibratePhase{}).Execute(context.Background(), rc)
	if out.Status != StatusFailed {
		t.Errorf("missing Calibrator should fail; got %q", out.Status)
	}
}

func TestCalibratePhase_SweepsAllNonPhantomFans(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", LabelHint: "Fan 1"},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2", LabelHint: "Fan 2"},
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{
			{PWMPath: "/sys/hwmon0/pwm1", Polarity: "normal"},
			{PWMPath: "/sys/hwmon0/pwm2", Polarity: "phantom"},
		},
	})

	cal := &fakeCalibrator{
		results: map[string]calibrate.Result{
			"/sys/hwmon0/pwm1": {StartPWM: 60, MaxRPM: 1800, MinRPM: 800},
		},
	}
	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	if len(cal.attempts) != 1 || cal.attempts[0] != "/sys/hwmon0/pwm1" {
		t.Errorf("expected only non-phantom fan attempted; got %v", cal.attempts)
	}

	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 2 {
		t.Fatalf("artifact should record both fans; got %d", len(art.Results))
	}
	// Phantom one should carry SkippedWhy.
	var phantomEntry, sweepedEntry CalibrateFanResult
	for _, r := range art.Results {
		if r.PWMPath == "/sys/hwmon0/pwm2" {
			phantomEntry = r
		} else {
			sweepedEntry = r
		}
	}
	if phantomEntry.SkippedWhy == "" {
		t.Error("phantom fan should have SkippedWhy populated")
	}
	if sweepedEntry.StartPWM != 60 || sweepedEntry.MaxRPM != 1800 {
		t.Errorf("sweep result not captured: %+v", sweepedEntry)
	}
}

func TestCalibratePhase_PerFanErrorDoesNotFailPhase(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/p1", LabelHint: "Fan 1"},
			{Index: 2, PWMPath: "/p2", LabelHint: "Fan 2"},
		},
	})
	cal := &fakeCalibrator{
		errors:  map[string]error{"/p1": errors.New("watchdog timed out")},
		results: map[string]calibrate.Result{"/p2": {StartPWM: 50, MaxRPM: 1200}},
	}
	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("per-fan failures must not fail the phase; got %q", out.Status)
	}
	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 2 {
		t.Fatalf("len=%d", len(art.Results))
	}
	if art.Results[0].Error == "" || art.Results[1].StartPWM == 0 {
		t.Errorf("error vs success not recorded correctly: %+v", art.Results)
	}
}

// TestCalibratePhase_FansOnDifferentChipsRunInParallel proves the PR#B7
// fanout: two fans on two different chips must enter the Calibrator
// concurrently. The fake calibrator blocks every call on a barrier
// that releases only when ≥2 goroutines are simultaneously inside it.
// If CalibratePhase were serial (the regression we're fixing), the
// second fan would never reach the barrier and the test would time
// out at the 3-second deadline.
func TestCalibratePhase_FansOnDifferentChipsRunInParallel(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmonA/pwm1", ChipName: "chipA", LabelHint: "A1"},
			{Index: 1, PWMPath: "/sys/hwmonB/pwm1", ChipName: "chipB", LabelHint: "B1"},
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{
			{PWMPath: "/sys/hwmonA/pwm1", Polarity: "normal"},
			{PWMPath: "/sys/hwmonB/pwm1", Polarity: "normal"},
		},
	})

	cal := newBarrierCalibrator(2)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out := (CalibratePhase{Calibrator: cal}).Execute(ctx, rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	if got := cal.peakInFlight(); got < 2 {
		t.Errorf("peak concurrent calibrations = %d, want >= 2 (parallelism regressed)", got)
	}

	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 2 {
		t.Fatalf("artifact should record both fans; got %d", len(art.Results))
	}
}

// TestCalibratePhase_FansOnSameChipRunSerially proves the safety
// constraint of the fanout: fans on the SAME chip MUST sweep one at a
// time WHEN the chip family is not in the within-chip-parallel-safe
// allowlist. Super-I/O parts that share PWM-enable registers across
// pwmN channels can race the chip's fan-control state machine when
// two pwmN sweeps overlap. The barrier never sees >1 in-flight when
// WithinChipParallel returns false (the default).
func TestCalibratePhase_FansOnSameChipRunSerially(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", ChipName: "nct6798", LabelHint: "F1"},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2", ChipName: "nct6798", LabelHint: "F2"},
		},
	})

	cal := newBarrierCalibrator(1) // releases as soon as one goroutine arrives
	// nil WithinChipParallel → conservative default (serial-within-chip)
	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	if got := cal.peakInFlight(); got != 1 {
		t.Errorf("same-chip fans without within-chip-parallel must serialise; peak in-flight = %d, want 1", got)
	}
}

// TestCalibratePhase_FansOnSameChipParallelWhenSafe proves the #1219
// within-chip-parallel path: when WithinChipParallel returns true for
// the chip family, fans on the same chip enter Calibrate concurrently.
// The barrier waits for 4 simultaneous goroutines to prove a stronger
// shape than 2-of-2 (which a flaky scheduler could produce sequentially
// on a slow runner).
func TestCalibratePhase_FansOnSameChipParallelWhenSafe(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	const chipName = "nct6687"
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", ChipName: chipName, LabelHint: "F1"},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2", ChipName: chipName, LabelHint: "F2"},
			{Index: 3, PWMPath: "/sys/hwmon0/pwm3", ChipName: chipName, LabelHint: "F3"},
			{Index: 4, PWMPath: "/sys/hwmon0/pwm4", ChipName: chipName, LabelHint: "F4"},
		},
	})

	cal := newBarrierCalibrator(4) // releases once all 4 are inside
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	phase := CalibratePhase{
		Calibrator: cal,
		WithinChipParallel: func(c string) bool {
			return c == chipName
		},
	}
	out := phase.Execute(ctx, rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	if got := cal.peakInFlight(); got < 4 {
		t.Errorf("within-chip parallel: peak in-flight = %d, want >= 4 (#1219 regressed)", got)
	}

	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 4 {
		t.Errorf("artifact should record all 4 fans; got %d", len(art.Results))
	}
}

// barrierCalibrator records the peak number of goroutines
// simultaneously inside Calibrate and releases each one as soon as
// `release` goroutines have arrived. Used to prove orchestrator
// fanout shape without timing-sensitive sleeps.
type barrierCalibrator struct {
	release int32
	gate    chan struct{}
	once    sync.Once

	mu     sync.Mutex
	live   int32
	peak   int32
	closed atomic.Bool
}

func newBarrierCalibrator(release int) *barrierCalibrator {
	return &barrierCalibrator{
		release: int32(release),
		gate:    make(chan struct{}),
	}
}

func (b *barrierCalibrator) Calibrate(ctx context.Context, _ *config.Fan) (calibrate.Result, error) {
	b.mu.Lock()
	b.live++
	if b.live > b.peak {
		b.peak = b.live
	}
	live := b.live
	b.mu.Unlock()

	if live >= b.release {
		b.once.Do(func() {
			b.closed.Store(true)
			close(b.gate)
		})
	}

	// Wait either for the gate or ctx — but in success-path tests
	// the gate closes before ctx times out.
	select {
	case <-b.gate:
	case <-ctx.Done():
		b.mu.Lock()
		b.live--
		b.mu.Unlock()
		return calibrate.Result{}, ctx.Err()
	}

	b.mu.Lock()
	b.live--
	b.mu.Unlock()
	return calibrate.Result{StartPWM: 80, MaxRPM: 1500}, nil
}

func (b *barrierCalibrator) peakInFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(b.peak)
}

func TestCalibratePhase_EmptyProbeArtifactSkips(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{Fans: nil})
	out := (CalibratePhase{Calibrator: &fakeCalibrator{}}).Execute(context.Background(), rc)
	if out.Status != StatusSkipped {
		t.Errorf("empty probe → Skipped, got %q", out.Status)
	}
}

// withFastSustainedSpinCheck shrinks the sustained-spin constants so
// tests don't pay the production 3.75 s wall-clock. Restored in
// t.Cleanup so concurrent tests aren't affected.
func withFastSustainedSpinCheck(t *testing.T) {
	t.Helper()
	settle, samples, interval := sustainedSpinSettle, sustainedSpinSamples, sustainedSpinInterval
	sustainedSpinSettle = 10 * time.Millisecond
	sustainedSpinSamples = 3
	sustainedSpinInterval = 2 * time.Millisecond
	t.Cleanup(func() {
		sustainedSpinSettle = settle
		sustainedSpinSamples = samples
		sustainedSpinInterval = interval
	})
}

// TestCalibratePhase_TrustsCurveWhenSustainedSamplesZero is the
// fresh-Fedora regression. The sweep measures a real curve
// (MaxRPM > 0) for a Dell SMM fan, but the post-sweep sustained-spin
// check samples zero RPM because the EC reasserts Q-Fan-style control
// between the sweep's last write and the sustained-check write. With
// the old verify-phase architecture this reclassified the fan as
// phantom and excluded it from the applied config. The fix admits
// the fan and flags DisagreedWithSustainedCheck for the doctor page.
func TestCalibratePhase_TrustsCurveWhenSustainedSamplesZero(t *testing.T) {
	withFastSustainedSpinCheck(t)
	dir := t.TempDir()
	pwm, rpm := stageFan(t, dir, 1, fanFixture{pwmInitial: 128, rpmAfter: 0})

	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, RPMPath: rpm, ChipName: "dell_smm", LabelHint: "F1"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "normal"}},
	})

	// Fake calibrator returns a real curve for this PWM path. The
	// sustained-spin check then runs against the staged sysfs fixture
	// where fan_input reads 0 — exactly the Dell SMM disagreement.
	cal := &fakeCalibrator{results: map[string]calibrate.Result{
		pwm: {StartPWM: 76, MaxRPM: 2112, MinRPM: 1401},
	}}

	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(art.Results))
	}
	r := art.Results[0]
	if r.Phantom {
		t.Errorf("fan should NOT be phantom when sweep measured MaxRPM>0: %+v", r)
	}
	if !r.DisagreedWithSustainedCheck {
		t.Errorf("DisagreedWithSustainedCheck should be set when sustained samples are zero on a curve-validated fan: %+v", r)
	}
	if r.MaxRPM != 2112 {
		t.Errorf("MaxRPM should be preserved from the sweep: got %d, want 2112", r.MaxRPM)
	}
	if len(r.SustainedRPMs) == 0 {
		t.Errorf("SustainedRPMs should carry the sample slice for diagnostics: %+v", r)
	}
}

// TestCalibratePhase_PhantomWhenBothSweepAndSustainedSeeZero is the
// orthogonal regression: a channel that nothing's wired to should
// still be flagged Phantom by the sweep+sustained-check combo. The
// fakeCalibrator returns MaxRPM=0 to model a sweep that found no
// real fan; the sustained-spin check on the staged fan_input (zero
// RPM) seals the verdict.
func TestCalibratePhase_PhantomWhenBothSweepAndSustainedSeeZero(t *testing.T) {
	withFastSustainedSpinCheck(t)
	dir := t.TempDir()
	pwm, rpm := stageFan(t, dir, 1, fanFixture{pwmInitial: 128, rpmAfter: 0})

	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, RPMPath: rpm, ChipName: "x", LabelHint: "Phantom"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "normal"}},
	})

	cal := &fakeCalibrator{results: map[string]calibrate.Result{
		pwm: {StartPWM: 0, MaxRPM: 0, MinRPM: 0},
	}}

	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}

	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 1 || !art.Results[0].Phantom {
		t.Errorf("no curve + zero sustained samples must phantom; got %+v", art)
	}
	if art.Results[0].DisagreedWithSustainedCheck {
		t.Errorf("DisagreedWithSustainedCheck must be false when sweep saw no curve: %+v", art.Results[0])
	}
}

// TestCalibratePhase_FlagsNonMonotonicCurve uses the exact Dell SMM
// fan curve captured during the fresh-Fedora wizard test (issue
// #1214): the sweep peaks at PWM=165 / 2112 RPM and drops back to
// ~1595 RPM through PWM=255 because the EC reasserts Q-Fan-style
// control at high PWM. The largest drop (2088→1595 = 493 RPM at
// PWM=178→191) is 23% of MaxRPM — well over the 15% threshold —
// so the fan should be admitted with NonMonotonicCurve=true and
// MaxDropRPM=493.
func TestCalibratePhase_FlagsNonMonotonicCurve(t *testing.T) {
	withFastSustainedSpinCheck(t)
	dir := t.TempDir()
	pwm, rpm := stageFan(t, dir, 1, fanFixture{pwmInitial: 128, rpmAfter: 1500})

	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, RPMPath: rpm, ChipName: "dell_smm", LabelHint: "Dell Fan"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "normal"}},
	})

	// Actual curve recorded during the 2026-05-18 Fedora test.
	dellSMMCurve := []calibrate.PWMRPMPoint{
		{PWM: 0, RPM: 1485}, {PWM: 12, RPM: 0}, {PWM: 25, RPM: 0},
		{PWM: 38, RPM: 0}, {PWM: 51, RPM: 0}, {PWM: 63, RPM: 0},
		{PWM: 76, RPM: 1401}, {PWM: 89, RPM: 1579}, {PWM: 102, RPM: 1591},
		{PWM: 114, RPM: 1590}, {PWM: 127, RPM: 1617}, {PWM: 140, RPM: 1567},
		{PWM: 153, RPM: 1594}, {PWM: 165, RPM: 2112}, {PWM: 178, RPM: 2088},
		{PWM: 191, RPM: 1595}, {PWM: 204, RPM: 1602}, {PWM: 216, RPM: 1586},
		{PWM: 229, RPM: 1601}, {PWM: 242, RPM: 1599}, {PWM: 255, RPM: 1648},
	}
	cal := &fakeCalibrator{results: map[string]calibrate.Result{
		pwm: {StartPWM: 76, MaxRPM: 2112, MinRPM: 1401, Curve: dellSMMCurve},
	}}

	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(art.Results))
	}
	r := art.Results[0]
	if !r.NonMonotonicCurve {
		t.Errorf("Dell SMM curve must flag NonMonotonicCurve (max drop = 493 RPM, 23%% of MaxRPM): %+v", r)
	}
	if r.MaxDropRPM != 493 {
		t.Errorf("MaxDropRPM = %d, want 493 (from PWM=178/2088 → PWM=191/1595)", r.MaxDropRPM)
	}
	// Admission unaffected: fan is real, just irregular.
	if r.Phantom {
		t.Errorf("non-monotonic curve must not phantom-flag the fan: %+v", r)
	}
}

// TestCalibratePhase_MonotonicCurveNotFlagged asserts the noise
// floor: a well-behaved curve with small step-to-step jitter (well
// below 15% of MaxRPM) should NOT trip the NonMonotonicCurve flag.
func TestCalibratePhase_MonotonicCurveNotFlagged(t *testing.T) {
	withFastSustainedSpinCheck(t)
	dir := t.TempDir()
	pwm, rpm := stageFan(t, dir, 1, fanFixture{pwmInitial: 128, rpmAfter: 1500})

	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, RPMPath: rpm, ChipName: "nct6687", LabelHint: "Clean Fan"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "normal"}},
	})

	// Monotonic curve with small ±50 RPM jitter (well under 15% of 2000 = 300).
	clean := []calibrate.PWMRPMPoint{
		{PWM: 80, RPM: 600}, {PWM: 100, RPM: 800}, {PWM: 120, RPM: 1000},
		{PWM: 140, RPM: 1180}, {PWM: 160, RPM: 1360}, {PWM: 180, RPM: 1530},
		{PWM: 200, RPM: 1700}, {PWM: 220, RPM: 1860}, {PWM: 240, RPM: 1950},
		{PWM: 255, RPM: 2000},
	}
	cal := &fakeCalibrator{results: map[string]calibrate.Result{
		pwm: {StartPWM: 80, MaxRPM: 2000, MinRPM: 600, Curve: clean},
	}}

	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if art.Results[0].NonMonotonicCurve {
		t.Errorf("clean monotonic curve must NOT flag NonMonotonicCurve: %+v", art.Results[0])
	}
	if art.Results[0].MaxDropRPM != 0 {
		t.Errorf("MaxDropRPM = %d on monotonic curve, want 0", art.Results[0].MaxDropRPM)
	}
}

// TestCalibratePhase_SustainedCheckSkippedWhenNoRPMPath asserts that
// channels without a paired fan_input get admitted (no phantom flag)
// since the sustained-spin check needs a tach to read. Mirrors the
// old VerifyPhase "no RPM tach path; cannot verify" skip path.
func TestCalibratePhase_SustainedCheckSkippedWhenNoRPMPath(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: "/synthetic/pwm1", RPMPath: "", ChipName: "x", LabelHint: "DC"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: "/synthetic/pwm1", Polarity: "normal"}},
	})

	cal := &fakeCalibrator{results: map[string]calibrate.Result{
		"/synthetic/pwm1": {StartPWM: 80, MaxRPM: 1500},
	}}
	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	var art CalibrateArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 1 || art.Results[0].Phantom {
		t.Errorf("RPM-less fan should be admitted (sustained check skipped); got %+v", art)
	}
	if len(art.Results[0].SustainedRPMs) != 0 {
		t.Errorf("SustainedRPMs should be empty when check was skipped: %+v", art.Results[0].SustainedRPMs)
	}
}
