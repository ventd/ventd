package watchdog

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
)

// flakyHandler panics on the Nth log call (1-indexed) and counts all calls.
// Used to inject a synthetic panic into the watchdog restore path without
// having to stub every sysfs function.
type flakyHandler struct {
	calls   atomic.Int32
	panicOn int32
}

func (h *flakyHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *flakyHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *flakyHandler) WithGroup(string) slog.Handler            { return h }
func (h *flakyHandler) Handle(context.Context, slog.Record) error {
	n := h.calls.Add(1)
	if n == h.panicOn {
		panic("synthetic restore panic")
	}
	return nil
}

// TestRestorePanicInOneEntryContinuesLoop asserts that if a panic fires while
// restoring entry N, the watchdog still attempts entries N+1..end. Without
// restoreOne's recover, the first panic would unwind the loop and leave the
// remaining fans at whatever PWM the daemon last wrote.
func TestRestorePanicInOneEntryContinuesLoop(t *testing.T) {
	h := &flakyHandler{panicOn: 1} // first log call panics
	w := New(slog.New(h))

	// Two entries with origEnable=-1 → each one takes the full-speed fallback
	// branch, which calls hwmon.WritePWM on a bogus path. The write fails
	// (ENOENT), triggering a single logger call per entry. Entry 1's log call
	// panics; entry 2's log call must still run.
	w.entries = []entry{
		{pwmPath: "/nonexistent/watchdog-test-a", fanType: "hwmon", origEnable: -1},
		{pwmPath: "/nonexistent/watchdog-test-b", fanType: "hwmon", origEnable: -1},
	}

	// Must not panic out of Restore — recover inside restoreOne catches it.
	w.Restore()

	// Entry 1: one log call (panics). Entry 2: at least one log call (no panic).
	// Plus the recover itself emits an Error log for entry 1. So >=3.
	//
	// Mutation check: do NOT "simplify" this to >=2. The count of 3 is load-bearing
	// — it distinguishes (a) recover fires AND emits a diagnostic identity log
	// (>=3) from (b) recover swallows silently without logging (2). Dropping the
	// bound to 2 would pass even if the identity log line is removed, defeating
	// half the test's purpose.
	if got := h.calls.Load(); got < 3 {
		t.Fatalf("expected >=3 log calls (entry1 fallback + recover log + entry2 fallback), got %d", got)
	}
}
