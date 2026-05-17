package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
)

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
// time. Super-I/O parts share PWM-enable registers across pwmN
// channels; concurrent sweeps on one chip can race the chip's
// fan-control state machine. Asserts the barrier never sees >1
// in-flight when both fans share a chip name.
func TestCalibratePhase_FansOnSameChipRunSerially(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", ChipName: "nct6687", LabelHint: "F1"},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2", ChipName: "nct6687", LabelHint: "F2"},
		},
	})

	cal := newBarrierCalibrator(1) // releases as soon as one goroutine arrives
	out := (CalibratePhase{Calibrator: cal}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	if got := cal.peakInFlight(); got != 1 {
		t.Errorf("same-chip fans must serialise; peak in-flight = %d, want 1", got)
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
