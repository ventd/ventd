package state

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestRunningPID covers the read-only liveness probe: absent file → not
// running; this process's own (alive) PID → running; a stale PID (no such
// process) → not running, and the file is left untouched (no cleanup).
func TestRunningPID(t *testing.T) {
	t.Run("absent pidfile", func(t *testing.T) {
		if pid, running := RunningPID(t.TempDir()); running || pid != 0 {
			t.Fatalf("RunningPID on empty dir = (%d,%v), want (0,false)", pid, running)
		}
	})

	t.Run("live pid", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, pidFileName), []byte(strconv.Itoa(os.Getpid())), 0o640); err != nil {
			t.Fatal(err)
		}
		pid, running := RunningPID(dir)
		if !running || pid != os.Getpid() {
			t.Fatalf("RunningPID = (%d,%v), want (%d,true)", pid, running, os.Getpid())
		}
	})

	t.Run("stale pid is not running and not cleaned up", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, pidFileName)
		// PID 2^31-1 is overwhelmingly unlikely to be a live process.
		if err := os.WriteFile(path, []byte("2147483647"), 0o640); err != nil {
			t.Fatal(err)
		}
		if _, running := RunningPID(dir); running {
			t.Fatal("stale PID reported as running")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("RunningPID must not remove a stale pidfile (read-only)")
		}
	})
}
