package setup

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestManager_CalibrationCompleteCallbackFires pins RULE-AGG-WIRING-01
// by driving the production helper Manager.fireCalibrationComplete
// directly. The helper is the single named-method dispatch surface
// Manager.run uses post-runAcousticGate (#1075 helper-extraction);
// binding the rule to the helper means a regression that drops the
// call site from Manager.run requires actively deleting a named-method
// reference rather than an inline read-and-invoke block.
func TestManager_CalibrationCompleteCallbackFires(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	m := &Manager{logger: logger}

	var (
		mu      sync.Mutex
		invoked int
		seenAt  time.Time
	)
	m.SetCalibrationCompleteFn(func(at time.Time) {
		mu.Lock()
		invoked++
		seenAt = at
		mu.Unlock()
	})

	// Drive the production helper directly — same code path
	// Manager.run uses.
	want := time.Now()
	m.fireCalibrationComplete(want)

	mu.Lock()
	defer mu.Unlock()
	if invoked != 1 {
		t.Errorf("callback invoked %d times; want 1", invoked)
	}
	if seenAt.IsZero() {
		t.Error("callback received zero time.Time; cold-start hard pin would stay inert")
	}
	if !seenAt.Equal(want) {
		t.Errorf("callback received wall-clock %v; want %v", seenAt, want)
	}
}

// TestManager_NilCalibrationCompleteCallbackIsNoOp asserts that
// Manager.fireCalibrationComplete is a clean no-op when no callback
// is wired (the test default, monitor-only systems). No panic, no
// error — and the field remains nil after the call so a subsequent
// SetCalibrationCompleteFn wires cleanly.
func TestManager_NilCalibrationCompleteCallbackIsNoOp(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	m := &Manager{logger: logger}

	// No SetCalibrationCompleteFn → fn field is nil. Production
	// helper MUST not panic.
	m.fireCalibrationComplete(time.Now())

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calibrationCompleteFn != nil {
		t.Error("fireCalibrationComplete mutated the callback field; want unchanged nil")
	}
}

// TestManager_CalibrationCompleteUnderConcurrentSet pins the
// locking contract: fireCalibrationComplete reads the field under
// m.mu, releases the lock, then invokes the callback without
// holding the lock so a slow hook cannot block other Manager
// operations. Verified by concurrent SetCalibrationCompleteFn +
// fireCalibrationComplete — neither should deadlock or race
// under -race.
func TestManager_CalibrationCompleteUnderConcurrentSet(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	m := &Manager{logger: logger}

	var invoked atomic.Int32
	m.SetCalibrationCompleteFn(func(time.Time) { invoked.Add(1) })

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.fireCalibrationComplete(time.Now())
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.SetCalibrationCompleteFn(func(time.Time) { invoked.Add(1) })
		}()
	}
	wg.Wait()
	if invoked.Load() < 1 {
		t.Errorf("expected at least one callback invocation; got %d", invoked.Load())
	}
}
