package hwmon_test

import (
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/hwmon"
)

// TestEnsureManualMode_NoFalseCacheOnPermissionError is the regression test
// for the mark-before-write bug fixed in this commit.
//
// Before the fix, ensureManualMode called LoadOrStore before the sysfs write,
// so a permission error on the first call would permanently suppress retries.
// After the fix it calls Load first and only stores on write success (or
// documented-absence ErrNotExist), so the second call re-attempts the write.
func TestEnsureManualMode_NoFalseCacheOnPermissionError(t *testing.T) {
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm1"

	var callCount atomic.Int32

	// First call: fail with ErrPermission. Second call: succeed.
	writeFn := func(pwmPath string, value int) error {
		n := callCount.Add(1)
		if n == 1 {
			return os.ErrPermission
		}
		return nil
	}

	b := hwmon.NewBackendForTest(slog.Default(), writeFn, nil)
	ch := hwmon.MakeTestChannel(pwmPath, false)

	// First Write: ensureManualMode sees ErrPermission, must return an error
	// and must NOT cache the path in b.acquired.
	err := b.Write(ch, 128)
	if err == nil {
		t.Fatal("first Write: expected error from ErrPermission, got nil")
	}
	if !errors.Is(err, hal.ErrNotPermitted) {
		t.Errorf("first Write: expected hal.ErrNotPermitted, got %v", err)
	}
	if callCount.Load() != 1 {
		t.Errorf("first Write: writePWMEnable called %d times, want 1", callCount.Load())
	}

	// Second Write: ensureManualMode must re-attempt the sysfs write (call
	// count goes from 1 → 2) because the first call did not set b.acquired.
	err = b.Write(ch, 128)
	if err != nil {
		// The sysfs write itself succeeds on call 2; any remaining error would
		// be from the actual PWM write which we haven't faked — but since this
		// is a unit test without real sysfs, the WritePWM call may fail with a
		// path-not-found error.  What we care about is that writePWMEnable was
		// called a second time.
		_ = err // tolerate downstream PWM write failing in test environment
	}
	if callCount.Load() != 2 {
		t.Errorf("second Write: writePWMEnable call count = %d, want 2 (re-attempted after first failure)",
			callCount.Load())
	}
}

// TestEnsureManualMode_CachedOnSuccess verifies that a successful
// ensureManualMode call stores the path so subsequent calls are skipped
// (writePWMEnable called only once even across multiple Write calls).
func TestEnsureManualMode_CachedOnSuccess(t *testing.T) {
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm2"

	var callCount atomic.Int32
	writeFn := func(pwmPath string, value int) error {
		callCount.Add(1)
		return nil // always succeed
	}

	b := hwmon.NewBackendForTest(slog.Default(), writeFn, nil)
	ch := hwmon.MakeTestChannel(pwmPath, false)

	// Three Writes in a row: ensureManualMode should attempt the sysfs write
	// only on the first call and cache the result for the next two.
	for i := range 3 {
		_ = b.Write(ch, uint8(64+i))
	}
	if callCount.Load() != 1 {
		t.Errorf("writePWMEnable call count = %d after 3 Writes, want 1 (cached after first success)",
			callCount.Load())
	}
}

// TestEnsureManualMode_ErrNotExistCached verifies that a missing pwm_enable
// file (fs.ErrNotExist) is treated as a documented absence and cached, so
// the not-found message is emitted only once (not on every tick).
func TestEnsureManualMode_ErrNotExistCached(t *testing.T) {
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm3"

	var callCount atomic.Int32
	writeFn := func(pwmPath string, value int) error {
		callCount.Add(1)
		// Simulate a driver that doesn't expose pwm_enable.
		return &os.PathError{Op: "stat", Path: pwmPath + "_enable", Err: os.ErrNotExist}
	}

	b := hwmon.NewBackendForTest(slog.Default(), writeFn, nil)
	ch := hwmon.MakeTestChannel(pwmPath, false)

	for i := range 3 {
		_ = b.Write(ch, uint8(64+i))
	}
	if callCount.Load() != 1 {
		t.Errorf("writePWMEnable call count = %d after 3 Writes with ErrNotExist, want 1 (cached)",
			callCount.Load())
	}
}
