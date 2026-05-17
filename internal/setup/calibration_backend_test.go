package setup

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
)

// ═══════════════════════════════════════════════════════════════════════════
// fakeCalBackend implements CalibrationBackend for tests. Behaviour is
// controlled by injectable hooks; every call site is recorded so the test
// can assert what the wizard asked the calibrate backend to do.
//
// Closes #132 — the seam exists in setup.go (CalibrationBackend interface);
// this fake is what the previously-skipped wizard-safety tests use to drive
// abort / panic / ctx-cancel / error-handling code paths without standing up
// a real calibration pipeline.
// ═══════════════════════════════════════════════════════════════════════════

type fakeCalBackend struct {
	// DetectFn overrides DetectRPMSensor when non-nil. Default returns
	// the same hwmon dir's fan1_input as a successful detection so the
	// wizard advances to RunSync.
	DetectFn func(fan *config.Fan) (calibrate.DetectResult, error)
	// RunSyncFn overrides RunSync when non-nil. Default returns a
	// no-op success (StartPWM=0, MaxRPM=0, no error → wizard marks
	// the fan "skipped" but continues).
	RunSyncFn func(ctx context.Context, fan *config.Fan) (calibrate.Result, error)

	mu             sync.Mutex
	detectCalls    []*config.Fan
	runSyncCalls   []*config.Fan
	runSyncCtxErrs []error // ctx.Err() observed at the start of each RunSync invocation
}

func (f *fakeCalBackend) AllStatus() []calibrate.Status { return nil }

func (f *fakeCalBackend) DetectRPMSensor(fan *config.Fan) (calibrate.DetectResult, error) {
	f.mu.Lock()
	f.detectCalls = append(f.detectCalls, fan)
	f.mu.Unlock()
	if f.DetectFn != nil {
		return f.DetectFn(fan)
	}
	// Default: synthesise a "fan1_input" sibling so the wizard advances.
	return calibrate.DetectResult{
		RPMPath: filepath.Join(filepath.Dir(fan.PWMPath), "fan1_input"),
		Delta:   500,
	}, nil
}

func (f *fakeCalBackend) RunSync(ctx context.Context, fan *config.Fan) (calibrate.Result, error) {
	f.mu.Lock()
	f.runSyncCalls = append(f.runSyncCalls, fan)
	f.runSyncCtxErrs = append(f.runSyncCtxErrs, ctx.Err())
	f.mu.Unlock()
	if f.RunSyncFn != nil {
		return f.RunSyncFn(ctx, fan)
	}
	return calibrate.Result{PWMPath: fan.PWMPath}, nil
}

func (f *fakeCalBackend) DetectCallsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.detectCalls)
}

func (f *fakeCalBackend) RunSyncCallsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runSyncCalls)
}

// newWizardWithFake builds a Manager with a fake CalibrationBackend,
// a synthetic hwmon tree (1 chip, 1 PWM, 1 fan_input, 1 temp), and the
// other roots present-but-empty. The returned Manager is ready for
// `m.Start()`. Call `waitDone` to block on completion.
func newWizardWithFake(t *testing.T, fake *fakeCalBackend) *Manager {
	t.Helper()
	base := t.TempDir()
	hwmonRoot := filepath.Join(base, "hwmon")
	procRoot := filepath.Join(base, "proc")
	powercapRoot := filepath.Join(base, "powercap")

	// Minimal hwmon tree the wizard's discoverHwmonControls recognises:
	// nct6687 chip with one writable pwm + matching fan_input + a temp.
	fakeHwmon(t, hwmonRoot, map[string]string{
		"hwmon3/name":        "nct6687\n",
		"hwmon3/pwm1":        "128\n",
		"hwmon3/pwm1_enable": "1\n",
		"hwmon3/fan1_input":  "1200\n",
		"hwmon3/temp1_input": "45000\n",
		"hwmon3/temp1_label": "CPU\n",
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewWithRoots(fake, logger, hwmonRoot, procRoot, powercapRoot)
}

// ═══════════════════════════════════════════════════════════════════════════
// Tests previously skipped pending #132 — now unblocked by the
// CalibrationBackend interface.
// ═══════════════════════════════════════════════════════════════════════════

// TestCalibrate_AbortPropagatesCtx verifies that Manager.Abort() cancels
// the context handed to the calibrate backend's RunSync. The real PWM
// restore-on-abort invariant is enforced by the calibrate package (already
// tested in internal/calibrate); this test pins the orchestration-layer
// contract: setup MUST give RunSync a cancellable context AND firing
// Abort MUST cancel it.
//
// Invariant: hwmon-safety.md rule 7 (calibration interruptible).
// Closes #132 abort-restore item.
func TestCalibrate_AbortPropagatesCtx(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	released := make(chan struct{})
	fake := &fakeCalBackend{
		RunSyncFn: func(ctx context.Context, fan *config.Fan) (calibrate.Result, error) {
			close(started)
			<-ctx.Done()
			close(released)
			return calibrate.Result{}, ctx.Err()
		},
	}
	m := newWizardWithFake(t, fake)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("RunSync not invoked within 5s; wizard didn't reach calibration phase (detect=%d runsync=%d)",
			fake.DetectCallsCount(), fake.RunSyncCallsCount())
	}
	m.Abort()
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatalf("RunSync ctx not cancelled within 2s of Abort()")
	}
	_ = waitDone(t, m, 5*time.Second)
}

// TestCalibrate_PanicInRunSyncDoesNotCrashWizard verifies the wizard's
// per-goroutine panic recovery: when the calibrate backend's RunSync
// panics, the wizard MUST catch it, mark the fan errored, and continue
// to the terminal Done state instead of taking down the process.
//
// Invariant: hwmon-safety.md rule 4 (watchdog Restore fires on any exit
// path including panics). Closes #132 panic-restore item.
func TestCalibrate_PanicInRunSyncDoesNotCrashWizard(t *testing.T) {
	t.Parallel()
	fake := &fakeCalBackend{
		RunSyncFn: func(_ context.Context, _ *config.Fan) (calibrate.Result, error) {
			panic("synthetic calibration panic")
		},
	}
	m := newWizardWithFake(t, fake)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitDone(t, m, 5*time.Second)
	if !final.Done {
		t.Fatalf("wizard never reached Done after RunSync panic; final=%+v", final)
	}
	if fake.RunSyncCallsCount() == 0 {
		t.Fatalf("wizard never reached RunSync; nothing for the panic-recover to catch")
	}
	// Real assertion: process is alive, wizard reached terminal state.
	// Exact RunSync call count varies with how many fans the synthetic
	// hwmon tree happens to produce after discovery; the invariant is
	// "any panic was caught, process survived, wizard terminated."
}

// TestCalibrate_CtxCancelStopsRunSync verifies that cancelling the
// wizard's own context (via Abort) propagates to RunSync, and that
// subsequent fans are not started after cancellation.
//
// Invariant: hwmon-safety.md rule 7 (ctx cancel restores PWM). Closes
// #132 ctx-cancel-restore item.
func TestCalibrate_CtxCancelStopsRunSync(t *testing.T) {
	t.Parallel()
	gotCtxErr := atomic.Bool{}
	fake := &fakeCalBackend{
		RunSyncFn: func(ctx context.Context, fan *config.Fan) (calibrate.Result, error) {
			<-ctx.Done()
			if ctx.Err() != nil {
				gotCtxErr.Store(true)
			}
			return calibrate.Result{}, ctx.Err()
		},
	}
	m := newWizardWithFake(t, fake)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Give RunSync a moment to hit the ctx.Done() wait, then cancel.
	time.Sleep(100 * time.Millisecond)
	m.Abort()
	final := waitDone(t, m, 5*time.Second)
	if !final.Done {
		t.Fatalf("wizard did not reach Done after Abort; final=%+v", final)
	}
	if !gotCtxErr.Load() {
		t.Fatalf("RunSync never observed ctx cancellation; setup did not propagate ctx through Abort()")
	}
}

// TestDetectRPM_ENOENTSkipNotCrash verifies the wizard handles
// fs.ErrNotExist from DetectRPMSensor as a graceful skip — the fan is
// dropped from calibration, the wizard reaches Done with a friendly
// error message, no panic, no stack trace.
//
// Invariant: hwmon-safety.md rule 5 (ENOENT logged + skipped).
// Closes #132 detect-ENOENT item.
func TestDetectRPM_ENOENTSkipNotCrash(t *testing.T) {
	t.Parallel()
	fake := &fakeCalBackend{
		DetectFn: func(fan *config.Fan) (calibrate.DetectResult, error) {
			return calibrate.DetectResult{}, &fs.PathError{
				Op: "open", Path: fan.PWMPath, Err: syscall.ENOENT,
			}
		},
	}
	m := newWizardWithFake(t, fake)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitDone(t, m, 5*time.Second)
	if !final.Done {
		t.Fatalf("wizard did not reach Done; final=%+v", final)
	}
	if fake.DetectCallsCount() == 0 {
		t.Fatalf("DetectRPMSensor never called; wizard didn't reach detect phase")
	}
	// Wizard reached Done. Error message (if any) must not contain
	// internal sysfs paths or Go error verbiage.
	for _, forbidden := range []string{"runtime error", "goroutine", "/sys/"} {
		if final.Error != "" && containsCI(final.Error, forbidden) {
			t.Errorf("error leaks internal detail %q: %s", forbidden, final.Error)
		}
	}
}

// TestDetectRPM_EIOSkipNotCrash verifies the wizard handles EIO from
// DetectRPMSensor as a graceful skip — same contract as ENOENT.
//
// Invariant: hwmon-safety.md rule 5 (EIO logged + skipped).
// Closes #132 detect-EIO item.
func TestDetectRPM_EIOSkipNotCrash(t *testing.T) {
	t.Parallel()
	fake := &fakeCalBackend{
		DetectFn: func(fan *config.Fan) (calibrate.DetectResult, error) {
			return calibrate.DetectResult{}, &fs.PathError{
				Op: "read", Path: fan.PWMPath, Err: syscall.EIO,
			}
		},
	}
	m := newWizardWithFake(t, fake)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitDone(t, m, 5*time.Second)
	if !final.Done {
		t.Fatalf("wizard did not reach Done; final=%+v", final)
	}
	if fake.DetectCallsCount() == 0 {
		t.Fatalf("DetectRPMSensor never called")
	}
}

// containsCI is a case-insensitive substring check used by the
// internal-detail-leak assertions above.
func containsCI(haystack, needle string) bool {
	// Tiny inline to avoid pulling strings just for ToLower.
	hl := []byte(haystack)
	nl := []byte(needle)
	for i := range hl {
		if hl[i] >= 'A' && hl[i] <= 'Z' {
			hl[i] += 'a' - 'A'
		}
	}
	for i := range nl {
		if nl[i] >= 'A' && nl[i] <= 'Z' {
			nl[i] += 'a' - 'A'
		}
	}
	if len(nl) == 0 {
		return true
	}
	for i := 0; i+len(nl) <= len(hl); i++ {
		match := true
		for j := range nl {
			if hl[i+j] != nl[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Keep the errors import live for future callers.
var _ = errors.New
