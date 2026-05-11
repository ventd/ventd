package hwmon_test

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

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

// TestWrite_EBUSY_ReacquiresAndRetries is the binding for
// RULE-HWMON-MODE-REACQUIRE (issue #904). Some BIOSes — Gigabyte
// Q-Fan / Smart Fan Control on IT8xxx is the canonical case —
// periodically reassert pwm_enable=2 mid-run. The next duty-cycle
// write then returns EBUSY because the chip is back in firmware
// auto. Backend.Write must (1) drop the cached acquired-state for
// the channel, (2) re-write pwm_enable=1, and (3) retry the
// original duty-cycle write exactly once. A second EBUSY surfaces
// the failure so the controller logs it against the fan.
func TestWrite_EBUSY_ReacquiresAndRetries(t *testing.T) {
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm4"

	var enableCount, dutyCount atomic.Int32

	// pwm_enable=1 always succeeds. We expect TWO calls: the first
	// during the initial Write, the second during the EBUSY retry.
	enableFn := func(pwmPath string, value int) error {
		enableCount.Add(1)
		return nil
	}

	// First duty write returns EBUSY (BIOS reasserted auto mid-run).
	// Second duty write succeeds (manual mode re-acquired).
	dutyFn := func(st hwmon.State, pwm uint8) error {
		n := dutyCount.Add(1)
		if n == 1 {
			return fmt.Errorf("hwmon: write pwm %s=%d: %w", st.PWMPath, pwm, syscall.EBUSY)
		}
		return nil
	}

	b := hwmon.NewBackendForTestWithDuty(slog.Default(), enableFn, dutyFn)
	ch := hwmon.MakeTestChannel(pwmPath, false)

	if err := b.Write(ch, 191); err != nil {
		t.Fatalf("Write: expected success after EBUSY retry, got %v", err)
	}
	if got := enableCount.Load(); got != 2 {
		t.Errorf("writePWMEnable call count = %d, want 2 (initial acquire + post-EBUSY re-acquire)", got)
	}
	if got := dutyCount.Load(); got != 2 {
		t.Errorf("writeDuty call count = %d, want 2 (initial + retry)", got)
	}
}

// TestWrite_PersistentEBUSY_FailsAfterOneRetry pins the
// "no infinite-retry" half of RULE-HWMON-MODE-REACQUIRE. If the
// duty-write returns EBUSY twice in a row (BIOS contesting on a
// tighter timer than our single retry can absorb), the second
// EBUSY surfaces to the caller so the controller logs it against
// the fan and triggers the fan-aborted path. This is a separate
// problem (heartbeat) from this primitive recovery.
func TestWrite_PersistentEBUSY_FailsAfterOneRetry(t *testing.T) {
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm5"

	var dutyCount atomic.Int32
	enableFn := func(pwmPath string, value int) error { return nil }
	dutyFn := func(st hwmon.State, pwm uint8) error {
		dutyCount.Add(1)
		return fmt.Errorf("hwmon: write pwm %s=%d: %w", st.PWMPath, pwm, syscall.EBUSY)
	}

	b := hwmon.NewBackendForTestWithDuty(slog.Default(), enableFn, dutyFn)
	ch := hwmon.MakeTestChannel(pwmPath, false)

	err := b.Write(ch, 191)
	if err == nil {
		t.Fatal("Write: expected error on persistent EBUSY, got nil")
	}
	if !errors.Is(err, syscall.EBUSY) {
		t.Errorf("Write: expected wrapped EBUSY, got %v", err)
	}
	if got := dutyCount.Load(); got != 2 {
		t.Errorf("writeDuty call count = %d, want 2 (initial + one retry only — never spin)", got)
	}
}

// TestRead_OKFalseZeroesOtherFields is the regression binding for #1049.
//
// Pre-fix, Backend.Read returned Reading{OK: false, RPM: <valid>} when the
// PWM-read leg failed but the RPM-read leg succeeded — consumers that
// ignored OK saw a partial reading with a "valid" RPM that did not
// correspond to a controllable PWM value. The invariant on hal.Reading
// (see backend.go doc-comment) requires OK=false carry no sub-state.
//
// This test sets up a tempdir with a valid fan*_input file (RPM read
// succeeds) and NO pwm* file (PWM read fails with ENOENT) and asserts
// that Read returns Reading{OK: false} with every other field zero.
func TestRead_OKFalseZeroesOtherFields(t *testing.T) {
	dir := t.TempDir()
	// Lay down a valid fan1_input — the RPM read leg will succeed at
	// 1500 RPM. No pwm1 file exists, so the PWM read leg returns ENOENT
	// and the OK flag gets cleared. Pre-fix, the partial Reading would
	// have carried RPM=1500 alongside OK=false; the post-fix invariant
	// zeroes it.
	pwmPath := filepath.Join(dir, "pwm1")
	rpmPath := filepath.Join(dir, "fan1_input")
	if err := os.WriteFile(rpmPath, []byte("1500\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", rpmPath, err)
	}

	b := hwmon.NewBackend(slog.Default())
	ch := hwmon.MakeTestChannel(pwmPath, false)

	got, err := b.Read(ch)
	if err != nil {
		t.Fatalf("Read: unexpected error %v", err)
	}
	if got.OK {
		t.Fatalf("Read: OK = true, want false (pwm read should have failed)")
	}
	want := hal.Reading{OK: false}
	if got != want {
		t.Errorf("Read returned %+v with OK=false; want %+v (#1049 invariant: empty-by-construction)", got, want)
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

// TestEBUSYRate_TracksWithinWindow pins the happy path for
// RULE-HWMON-EBUSY-RATE-OBSERVABILITY: every EBUSY event under the
// same window increments the per-channel counter; EBUSYRates()
// reports the running count with WindowStart held at the first
// event's timestamp.
func TestEBUSYRate_TracksWithinWindow(t *testing.T) {
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm1"

	// Persistent EBUSY so every Write call records the storm event.
	enableFn := func(pwmPath string, value int) error { return nil }
	dutyFn := func(st hwmon.State, pwm uint8) error {
		return fmt.Errorf("hwmon: write pwm %s=%d: %w", st.PWMPath, pwm, syscall.EBUSY)
	}

	b := hwmon.NewBackendForTestWithDuty(slog.Default(), enableFn, dutyFn)

	// Freeze the rate-tracking clock so the window doesn't roll
	// while the test ticks 3 events. Start at T=0, never advance.
	t0 := time.Unix(1_000_000, 0)
	b.SetClockForTest(func() time.Time { return t0 })

	ch := hwmon.MakeTestChannel(pwmPath, false)
	for i := 0; i < 3; i++ {
		_ = b.Write(ch, 100)
	}

	rates := b.EBUSYRates()
	got, ok := rates[pwmPath]
	if !ok {
		t.Fatalf("EBUSYRates missing %q; got keys=%v", pwmPath, mapKeys(rates))
	}
	if got.EventCount != 3 {
		t.Errorf("EventCount=%d, want 3", got.EventCount)
	}
	if got.WindowStart != t0.Unix() {
		t.Errorf("WindowStart=%d, want %d", got.WindowStart, t0.Unix())
	}
	if got.WindowSeconds != int(hwmon.EBUSYWindow.Seconds()) {
		t.Errorf("WindowSeconds=%d, want %d", got.WindowSeconds, int(hwmon.EBUSYWindow.Seconds()))
	}
}

// TestEBUSYRate_WindowResetAfterExpiry pins the rolling-window
// reset: an EBUSY event after EBUSYWindow seconds since the first
// event opens a new window (count=1, WindowStart=now). The earlier
// burst's history is forgotten — by design, the daemon's interest
// is "is the channel storming RIGHT NOW".
func TestEBUSYRate_WindowResetAfterExpiry(t *testing.T) {
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm2"
	enableFn := func(pwmPath string, value int) error { return nil }
	dutyFn := func(st hwmon.State, pwm uint8) error {
		return fmt.Errorf("hwmon: write pwm %s=%d: %w", st.PWMPath, pwm, syscall.EBUSY)
	}
	b := hwmon.NewBackendForTestWithDuty(slog.Default(), enableFn, dutyFn)

	var now atomic.Int64
	t0 := time.Unix(1_000_000, 0)
	now.Store(t0.Unix())
	b.SetClockForTest(func() time.Time { return time.Unix(now.Load(), 0) })

	ch := hwmon.MakeTestChannel(pwmPath, false)
	// First burst: 2 events at T=0.
	for i := 0; i < 2; i++ {
		_ = b.Write(ch, 100)
	}
	// Roll forward past the window.
	now.Add(int64(hwmon.EBUSYWindow.Seconds()) + 1)
	// One more event — should open a NEW window.
	_ = b.Write(ch, 100)

	got := b.EBUSYRates()[pwmPath]
	if got.EventCount != 1 {
		t.Errorf("EventCount after window-roll=%d, want 1", got.EventCount)
	}
	if got.WindowStart != now.Load() {
		t.Errorf("WindowStart after window-roll=%d, want %d", got.WindowStart, now.Load())
	}
}

// TestEBUSYRate_NoEventsReturnsEmpty pins the no-storm path: a
// channel that has never thrown EBUSY isn't listed in EBUSYRates.
// A doctor detector reading the map sees no false positives.
func TestEBUSYRate_NoEventsReturnsEmpty(t *testing.T) {
	enableFn := func(pwmPath string, value int) error { return nil }
	dutyFn := func(st hwmon.State, pwm uint8) error { return nil }
	b := hwmon.NewBackendForTestWithDuty(slog.Default(), enableFn, dutyFn)
	ch := hwmon.MakeTestChannel("/sys/class/hwmon/hwmon0/pwm3", false)

	// Happy-path write — no EBUSY.
	if err := b.Write(ch, 100); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := b.EBUSYRates(); len(got) != 0 {
		t.Errorf("EBUSYRates after happy-path write: got %v, want empty", got)
	}
}

// TestEBUSYRate_PerChannelIsolation pins that two channels storming
// in parallel don't collide — each carries its own counter and
// WindowStart. Critical for boards where one channel suffers
// Q-Fan reassertion but another is well-behaved.
func TestEBUSYRate_PerChannelIsolation(t *testing.T) {
	enableFn := func(pwmPath string, value int) error { return nil }
	// Storm only pwmA; pwmB succeeds cleanly.
	dutyFn := func(st hwmon.State, pwm uint8) error {
		if st.PWMPath == "/sys/class/hwmon/hwmon0/pwmA" {
			return fmt.Errorf("hwmon: write pwm %s=%d: %w", st.PWMPath, pwm, syscall.EBUSY)
		}
		return nil
	}
	b := hwmon.NewBackendForTestWithDuty(slog.Default(), enableFn, dutyFn)
	t0 := time.Unix(2_000_000, 0)
	b.SetClockForTest(func() time.Time { return t0 })

	for i := 0; i < 4; i++ {
		_ = b.Write(hwmon.MakeTestChannel("/sys/class/hwmon/hwmon0/pwmA", false), 100)
		_ = b.Write(hwmon.MakeTestChannel("/sys/class/hwmon/hwmon0/pwmB", false), 100)
	}

	rates := b.EBUSYRates()
	if got := rates["/sys/class/hwmon/hwmon0/pwmA"].EventCount; got != 4 {
		t.Errorf("pwmA EventCount=%d, want 4", got)
	}
	if _, ok := rates["/sys/class/hwmon/hwmon0/pwmB"]; ok {
		t.Errorf("pwmB listed in EBUSYRates despite clean writes")
	}
}

// TestEBUSYRate_ThresholdConstantsLocked pins the threshold values
// so a future refactor can't silently shrink (and over-log) or
// widen (and under-warn) the escalation ladder.
func TestEBUSYRate_ThresholdConstantsLocked(t *testing.T) {
	if hwmon.EBUSYWindow != 60*time.Second {
		t.Errorf("EBUSYWindow=%v, want 60s", hwmon.EBUSYWindow)
	}
	if hwmon.EBUSYWarnThreshold != 5 {
		t.Errorf("EBUSYWarnThreshold=%d, want 5", hwmon.EBUSYWarnThreshold)
	}
	if hwmon.EBUSYEscalateThreshold != 20 {
		t.Errorf("EBUSYEscalateThreshold=%d, want 20", hwmon.EBUSYEscalateThreshold)
	}
}

func mapKeys(m map[string]hwmon.EBUSYRate) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
