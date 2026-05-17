package setup

import (
	"errors"
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

// TestManager_Run_RecoversFromPanic pinned issue #1061 (F4)'s panic-
// recovery contract by injecting via SetVendorDaemonProbe — a seam
// the legacy Manager.run touched in Phase 0. With the legacy wizard
// removed, the orchestrator owns vendor-daemon detection through its
// own probe (recovery.DetectVendorDaemon), and per-phase panic-recover
// lives in orchestrator.runPhase (covered by orchestrator/orchestrator_test.go).
// The outer Manager.run defer remains as defence-in-depth for runOrchestrator
// bootstrap panics but is no longer exercisable from a Manager-level
// injection seam. Test removed.
