package setup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/recovery"
)

// TestManager_Start_SurfacesWizardAlreadyRunning pins issue #1060 (setup F3):
// when a sibling wizard holds the lock file, Manager.Start MUST refuse the
// second start with *ErrWizardAlreadyRunning rather than silently spawning
// a concurrent wizard. Before this fix Manager.Start neither acquired nor
// consulted the lock; AcquireWizardLock was defined but had zero production
// callers — RULE-PREFLIGHT-CONCURRENT_wizard was structurally inert.
func TestManager_Start_SurfacesWizardAlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VENTD_WIZARD_LOCK_DIR", dir)

	// First, populate the lock file with the test process's PID so the
	// AcquireWizardLock probe sees an alive holder. Using our own PID is
	// guaranteed alive without spawning real subprocesses.
	rel, err := AcquireWizardLock()
	if err != nil {
		t.Fatalf("first AcquireWizardLock: %v", err)
	}
	t.Cleanup(rel)

	// Override the test seam so Start's caller sees the same error path it
	// would see in production — a sibling daemon already holding the lock.
	// The first AcquireWizardLock call above wrote our own PID; the canonical
	// AcquireWizardLock treats "lock holder == self" as stale-by-convention,
	// so we install a stub that always reports busy.
	orig := acquireWizardLockFn
	defer func() { acquireWizardLockFn = orig }()
	acquireWizardLockFn = func() (func(), error) {
		return nil, &ErrWizardAlreadyRunning{PID: 999999, Path: "fake"}
	}

	m := newManager(t)
	err = m.Start()
	if err == nil {
		t.Fatal("Start: want ErrWizardAlreadyRunning, got nil")
	}
	var already *ErrWizardAlreadyRunning
	if !errors.As(err, &already) {
		t.Errorf("Start: error %T = %v, want *ErrWizardAlreadyRunning", err, err)
	}

	// The failure-class plumbing must surface ClassConcurrentInstall so
	// the wizard recovery card renders the actionable "Take over PID N"
	// surface rather than a generic bundle option.
	p := m.Progress()
	if p.FailureClass != string(recovery.ClassConcurrentInstall) {
		t.Errorf("FailureClass = %q, want %q", p.FailureClass, recovery.ClassConcurrentInstall)
	}
}

// TestManager_Start_AcquiresAndReleasesWizardLock pins the happy-path
// wizard-lock lifecycle. The lock file must exist while the wizard goroutine
// is in flight and be removed by the time Done flips true.
func TestManager_Start_AcquiresAndReleasesWizardLock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VENTD_WIZARD_LOCK_DIR", dir)

	m := newManager(t)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait for the wizard to finish — the sandbox path is fast.
	waitDone(t, m, 5*time.Second)

	// Lock file must NOT exist after Done — the release defer should have
	// fired. (It briefly existed while run() was alive; we can't reliably
	// catch that mid-run from a test without races, but the post-Done
	// invariant is the load-bearing part.)
	rel, err := AcquireWizardLock()
	if err != nil {
		t.Fatalf("AcquireWizardLock after Done: %v (lock file was not released)", err)
	}
	rel()
}

// TestManager_Run_RecoversFromPanic pins issue #1061 (setup F4): a panic
// inside Manager.run must be recovered so the daemon's other goroutines
// (web server, controller, watchdog) survive. The wizard's terminal state
// must transition to "failed" so the web UI can surface the crash instead
// of leaving the operator with a frozen wizard.
//
// This test exercises the panic-recover via a direct call to run() with a
// goroutine that panics deterministically before the wizard's real phases
// take effect. Because run() is unexported, we drive it directly from this
// package-internal test rather than going through Start.
func TestManager_Run_RecoversFromPanic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VENTD_WIZARD_LOCK_DIR", dir)

	m := newManager(t)

	// Inject a panic by swapping the vendor-daemon probe — the first thing
	// run() touches. This proves the outermost defer recovers even on a
	// Phase-0 crash.
	m.SetVendorDaemonProbe(func(ctx context.Context) recovery.VendorDaemon {
		panic("simulated wizard goroutine crash")
	})

	// Track that the daemon survives by checking that the goroutine returns
	// normally. We call run directly (the public surface Start spawns a
	// goroutine; here we want synchronous panic-recovery observation).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// run was set up via Start's bookkeeping in production; for this
		// test we hand-seed the minimal state required so the deferred
		// cleanup in run doesn't fault on a half-initialised manager.
		m.mu.Lock()
		m.running = true
		m.done = false
		m.cancel = cancel
		m.mu.Unlock()
		m.run(ctx, func() {})
	}()
	wg.Wait()

	p := m.Progress()
	if p.Phase != "failed" {
		t.Errorf("Phase after panic = %q, want %q (web UI surfaces failed state)", p.Phase, "failed")
	}
	if !strings.Contains(p.Error, "wizard panic") {
		t.Errorf("Error after panic = %q, want substring %q", p.Error, "wizard panic")
	}
	if !p.Done {
		t.Error("Done after panic = false; the wizard goroutine should have completed (recovered) rather than hanging")
	}
}
