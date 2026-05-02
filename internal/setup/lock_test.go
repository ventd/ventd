package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestRULE_WIZARD_GATE_LockAcquireRelease covers the basic happy path.
// Bound to RULE-WIZARD-GATE-LOCK-01.
func TestRULE_WIZARD_GATE_LockAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VENTD_WIZARD_LOCK_DIR", dir)

	release, err := AcquireWizardLock()
	if err != nil {
		t.Fatalf("AcquireWizardLock: %v", err)
	}
	t.Cleanup(release)

	want := filepath.Join(dir, "ventd-wizard.lock")
	if got := WizardLockPath(); got != want {
		t.Errorf("WizardLockPath() = %q, want %q", got, want)
	}
	data, err := os.ReadFile(WizardLockPath())
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	pid, _ := strconv.Atoi(string([]byte(data[:len(data)-1])))
	if pid != os.Getpid() {
		t.Errorf("lock PID = %d, want %d", pid, os.Getpid())
	}
	release()
	if _, err := os.Stat(WizardLockPath()); !os.IsNotExist(err) {
		t.Errorf("release should remove lock file, stat err = %v", err)
	}
}

// TestRULE_WIZARD_GATE_LockStalePidIsReused verifies that a lock file
// pointing at a dead PID is auto-removed and replaced. Bound to
// RULE-WIZARD-GATE-LOCK-02.
func TestRULE_WIZARD_GATE_LockStalePidIsReused(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VENTD_WIZARD_LOCK_DIR", dir)

	// PID 1 might be alive in a container; pick a definitely-dead PID
	// by writing one above the kernel's pid_max (usually 4194304). Some
	// kernels use a smaller pid_max so we pick 99999999 — below 32-bit
	// max but well above any plausible live PID on a real system.
	stalePID := "99999999\n"
	if err := os.WriteFile(WizardLockPath(), []byte(stalePID), 0o644); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}

	release, err := AcquireWizardLock()
	if err != nil {
		t.Fatalf("AcquireWizardLock should reuse stale lock, got: %v", err)
	}
	t.Cleanup(release)

	data, _ := os.ReadFile(WizardLockPath())
	got, _ := strconv.Atoi(string(data[:len(data)-1]))
	if got != os.Getpid() {
		t.Errorf("after reuse, lock PID = %d, want %d", got, os.Getpid())
	}
}

// TestRULE_WIZARD_GATE_LockLivePidRefuses verifies that a lock file
// pointing at a live (non-self) PID causes AcquireWizardLock to refuse
// with *ErrWizardAlreadyRunning. Bound to RULE-WIZARD-GATE-LOCK-03.
//
// Picks the test runner's parent PID rather than PID 1: PID 1 isn't
// always reliably present on every CI sandbox (some arm64 runner
// configurations namespace-isolate the test process so kill(1, 0)
// fails with ESRCH). The parent PID is guaranteed alive while this
// test runs, guaranteed non-self, and stable across runners.
func TestRULE_WIZARD_GATE_LockLivePidRefuses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VENTD_WIZARD_LOCK_DIR", dir)

	parentPID := os.Getppid()
	if parentPID <= 1 {
		t.Skipf("test parent PID is %d (no usable live non-self PID in this sandbox)", parentPID)
	}
	pidLine := strconv.Itoa(parentPID) + "\n"
	if err := os.WriteFile(WizardLockPath(), []byte(pidLine), 0o644); err != nil {
		t.Fatalf("seed live PID lock: %v", err)
	}

	_, err := AcquireWizardLock()
	if err == nil {
		t.Fatalf("AcquireWizardLock should refuse when live PID %d holds lock", parentPID)
	}
	var clash *ErrWizardAlreadyRunning
	if !errors.As(err, &clash) {
		t.Fatalf("err is %T, want *ErrWizardAlreadyRunning", err)
	}
	if clash.PID != parentPID {
		t.Errorf("ErrWizardAlreadyRunning.PID = %d, want %d", clash.PID, parentPID)
	}
}
