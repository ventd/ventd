package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// WizardLockPath returns the canonical path of the wizard run lock.
// Precedence:
//
//  1. $VENTD_WIZARD_LOCK_DIR overrides everything when set — the test
//     hook for hermetic per-tempdir lock isolation.
//  2. Root-mode (euid==0) uses /run.
//  3. Rootless uses $XDG_RUNTIME_DIR.
//  4. Falls back to /tmp when neither is available.
//
// The lock is best-effort coordination, not a security gate. The path
// agrees with internal/hwmon's wizardLockPath() helper so the preflight's
// AnotherWizardRunning probe and the gate that writes the lock observe
// the same file.
func WizardLockPath() string {
	if dir := os.Getenv("VENTD_WIZARD_LOCK_DIR"); dir != "" {
		return filepath.Join(dir, "ventd-wizard.lock")
	}
	if os.Geteuid() == 0 {
		return "/run/ventd-wizard.lock"
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "ventd-wizard.lock")
	}
	return "/tmp/ventd-wizard.lock"
}

// ErrWizardAlreadyRunning is returned by AcquireWizardLock when another
// live ventd setup wizard already holds the lock. The PID field carries
// the holder so the wizard surface can render an actionable
// "Take over PID N" button rather than a generic error.
type ErrWizardAlreadyRunning struct {
	PID  int
	Path string
}

func (e *ErrWizardAlreadyRunning) Error() string {
	return fmt.Sprintf("setup: another wizard is already running (pid %d, lock %s)",
		e.PID, e.Path)
}

// AcquireWizardLock writes the current PID to WizardLockPath() and returns
// a release func that removes the file. Returns *ErrWizardAlreadyRunning
// when a live PID already holds the lock; stale lock files (PID no longer
// alive) are removed and replaced.
//
// The lock is advisory — there's no kernel-level flock, just a file with
// a PID. That matches the rest of the daemon's coordination model
// (RULE-STATE-06 uses the same pattern for the daemon-instance PID
// file). The wizard runs as a long-lived goroutine in a single ventd
// process; the lock's job is to detect a sibling daemon, not to guard
// against in-process re-entry.
func AcquireWizardLock() (release func(), err error) {
	path := WizardLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("setup: lock dir: %w", err)
	}

	if data, readErr := os.ReadFile(path); readErr == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && pid > 0 && pid != os.Getpid() && isWizardAlive(pid) {
			return nil, &ErrWizardAlreadyRunning{PID: pid, Path: path}
		}
		_ = os.Remove(path) // stale, our own, or unparseable — drop it
	}

	content := strconv.Itoa(os.Getpid()) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("setup: write wizard lock: %w", err)
	}
	return func() { _ = os.Remove(path) }, nil
}

// ForceReleaseWizardLock removes the lock file regardless of who holds
// it. Used by the take-over endpoint when the operator confirms in the
// UI that they want to wrest control from a stuck sibling. Idempotent —
// removing a missing file is not an error.
func ForceReleaseWizardLock() error {
	err := os.Remove(WizardLockPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// isWizardAlive mirrors state.isProcessAlive — kept inline rather than
// imported so the setup package has no circular dependency on state.
func isWizardAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
