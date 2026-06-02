package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/ventd/ventd/internal/iox"
)

const pidFileName = "ventd.pid"

// ErrAlreadyRunning is returned when another ventd process is detected.
type ErrAlreadyRunning struct {
	PID int
}

func (e *ErrAlreadyRunning) Error() string {
	return fmt.Sprintf("another ventd process is already running (pid %d); "+
		"remove %s if the process no longer exists", e.PID, pidFileName)
}

// AcquirePID writes the current process PID to dir/ventd.pid and returns a
// release function that removes it. Returns ErrAlreadyRunning if a live
// process already owns the PID file (RULE-STATE-06).
//
// A stale PID file (process no longer alive) is removed and replaced.
func AcquirePID(dir string) (release func(), err error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("state: pid dir: %w", err)
	}
	path := filepath.Join(dir, pidFileName)

	// Read any existing PID file.
	if data, readErr := os.ReadFile(path); readErr == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && pid > 0 && isProcessAlive(pid) {
			return nil, &ErrAlreadyRunning{PID: pid}
		}
		// Stale PID file — remove it before creating our own.
		_ = os.Remove(path)
	}

	// Atomic write via iox.WriteFile (tempfile + fsync + rename +
	// dir-fsync). A concurrent reader of a half-written PID file would
	// get a parse failure and wrongly classify the file as stale,
	// racing a still-alive daemon over the same state directory.
	// Audit pass-6 finding C2 (#1051); RULE-IOX-01.
	content := strconv.Itoa(os.Getpid()) + "\n"
	if err := iox.WriteFile(path, []byte(content), fileMode); err != nil {
		return nil, fmt.Errorf("state: write pid file: %w", err)
	}

	return func() { _ = os.Remove(path) }, nil
}

// RunningPID reports the PID of a live ventd process owning dir's PID file,
// if any. It is the read-only counterpart to AcquirePID: it never creates,
// rewrites, or removes the file, so a health probe can run alongside a live
// daemon without disturbing its pidfile. Returns (0, false) when the file is
// absent, unparseable, or names a process that is no longer alive (a stale
// file — RunningPID does not clean it up; the next AcquirePID does).
func RunningPID(dir string) (pid int, running bool) {
	data, err := os.ReadFile(filepath.Join(dir, pidFileName))
	if err != nil {
		return 0, false
	}
	p, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr != nil || p <= 0 || !isProcessAlive(p) {
		return 0, false
	}
	return p, true
}

// isProcessAlive returns true if the process with the given PID exists and is
// running. Uses kill(pid, 0) which succeeds for any alive process the caller
// can signal, and fails with ESRCH when the process is not found.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
