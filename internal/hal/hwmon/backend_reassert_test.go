package hwmon_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hal/hwmon"
)

func readEnableFile(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse %s = %q: %v", path, string(data), err)
	}
	return v
}

// TestReassertIfReverted_SilentRevertReacquiresManualMode is the regression
// guard for RULE-HWMON-MODE-REASSERT-READBACK: after ventd has acquired manual
// mode, if the chip silently reverts pwm_enable to firmware/auto (resume from
// suspend, or a BIOS that reasserts without EBUSY), the throttled read-back in
// ensureManualMode detects it and re-asserts manual mode. The EBUSY retry in
// Write only covers the case where the duty write errors; this covers the
// silent revert that would otherwise leave ventd writing ignored bytes.
func TestReassertIfReverted_SilentRevertReacquiresManualMode(t *testing.T) {
	dir := t.TempDir()
	pwmPath := filepath.Join(dir, "pwm1")
	enablePath := filepath.Join(dir, "pwm1_enable")
	if err := os.WriteFile(pwmPath, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Start in firmware/auto (2) — the pre-ventd state.
	if err := os.WriteFile(enablePath, []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := hwmon.NewBackend(slog.Default())
	base := time.Unix(1_700_000_000, 0)
	clk := base
	b.SetClockForTest(func() time.Time { return clk })

	ch := hwmon.MakeTestChannel(pwmPath, false)

	// First write acquires manual mode (pwm_enable 2 → 1) and seeds the
	// read-back throttle at `base`. Duty-write outcome is irrelevant to this
	// test, so tolerate it (the temp file may not satisfy every WritePWM check).
	_ = b.Write(ch, 128)
	if got := readEnableFile(t, enablePath); got != 1 {
		t.Fatalf("after acquire: pwm_enable=%d, want 1 (manual)", got)
	}

	// Simulate a silent revert to firmware/auto.
	if err := os.WriteFile(enablePath, []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A write still inside the throttle window must NOT read back / re-assert.
	_ = b.Write(ch, 128)
	if got := readEnableFile(t, enablePath); got != 2 {
		t.Fatalf("within throttle window pwm_enable should be untouched: got %d, want 2", got)
	}

	// Advance past the throttle: the next write detects the revert and re-asserts.
	clk = base.Add(hwmon.ReassertReadbackInterval + time.Second)
	_ = b.Write(ch, 128)
	if got := readEnableFile(t, enablePath); got != 1 {
		t.Errorf("after silent revert + throttle elapsed: pwm_enable=%d, want 1 (re-asserted)", got)
	}
}

// TestReassertIfReverted_NoEnableFileIsSkipped verifies the read-back is a safe
// no-op on drivers that don't expose pwm_enable (e.g. in-tree nct6683 for the
// NCT6687D): the channel is acquired via the not-supported branch and the
// read-back can't verify, so it must skip without error rather than panic or
// spuriously write.
func TestReassertIfReverted_NoEnableFileIsSkipped(t *testing.T) {
	dir := t.TempDir()
	pwmPath := filepath.Join(dir, "pwm1")
	if err := os.WriteFile(pwmPath, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Deliberately do NOT create pwm1_enable — the driver doesn't expose it.

	b := hwmon.NewBackend(slog.Default())
	base := time.Unix(1_700_000_000, 0)
	clk := base
	b.SetClockForTest(func() time.Time { return clk })

	ch := hwmon.MakeTestChannel(pwmPath, false)

	// Acquire (hits the pwm_enable-not-supported branch), then advance the
	// clock and write again — reassertIfReverted must read-error and skip.
	_ = b.Write(ch, 128)
	clk = base.Add(hwmon.ReassertReadbackInterval + time.Second)
	_ = b.Write(ch, 128) // must not panic

	if _, err := os.Stat(filepath.Join(dir, "pwm1_enable")); !os.IsNotExist(err) {
		t.Errorf("pwm1_enable should not have been created by the read-back skip path (err=%v)", err)
	}
}
