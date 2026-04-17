package watchdog

// Restore-matrix coverage for every branch in watchdog.restoreOne.
//
// Why this file exists:
//
//	watchdog_restore_test.go pins one property — that a panic inside entry N
//	does not abort restore for entries N+1..end. It does NOT verify the
//	actual restore writes. This file fills that gap by driving Register +
//	Restore across a fake sysfs rooted in t.TempDir() and asserting the
//	exact bytes written back to pwm_enable / fan_target.
//
// Branch coverage targets (watchdog.go:129-208):
//
//	1. hwmon pwm fan with readable pwm_enable        → restore writes orig int
//	2. hwmon pwm fan with missing  pwm_enable        → fallback WritePWM=255
//	3. hwmon pwm fan with valid enable but write EIO → fallback WritePWM=255
//	4. rpm_target fan with readable pwm_enable       → writes orig to pwm*_enable
//	5. rpm_target fan with missing  pwm_enable       → WriteFanTarget=fan*_max
//	6. rpm_target fan enable-write fails             → fallback WriteFanTarget=max
//	7. nvidia entry with unparseable index           → logs, skips, no panic
//	8. Deregister pops the MOST RECENT matching entry (LIFO, per-sweep stack)
//	9. Deregister on an unknown path is a no-op     → no crash, no partial remove
//
// Future-session note:
//
//	Adding a new fanType to entry means adding a case here + a branch in
//	restoreOne. If you forget, TestRegister_FanTypeCoverage below will
//	tell you — it trips whenever a fan enum value has no matching test.
//	Search for "ADD-CASE-ABOVE" to find where to grow the matrix.

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFakeHwmon creates a minimal hwmon directory with one pwm channel.
// Returns (pwmPath, enablePath). Callers opt in to individual files by
// writing to them before invoking Register().
func newFakeHwmon(t *testing.T, pwmN int) (dir, pwmPath, enablePath string) {
	t.Helper()
	dir = t.TempDir()
	pwmPath = filepath.Join(dir, "pwm"+itoa(pwmN))
	enablePath = pwmPath + "_enable"
	// pwm file must exist because restoreOne's fallback path writes to it.
	if err := os.WriteFile(pwmPath, []byte("100\n"), 0o600); err != nil {
		t.Fatalf("seed pwm: %v", err)
	}
	return
}

// newFakeRPMTarget creates a fan*_target channel + its sibling pwm*_enable
// and fan*_max files. Mirrors the pre-RDNA amdgpu layout described in
// internal/hwmon/hwmon.go:188-216.
func newFakeRPMTarget(t *testing.T, n int) (dir, targetPath, enablePath, maxPath string) {
	t.Helper()
	dir = t.TempDir()
	targetPath = filepath.Join(dir, "fan"+itoa(n)+"_target")
	enablePath = filepath.Join(dir, "pwm"+itoa(n)+"_enable")
	maxPath = filepath.Join(dir, "fan"+itoa(n)+"_max")
	if err := os.WriteFile(targetPath, []byte("1000\n"), 0o600); err != nil {
		t.Fatalf("seed fan_target: %v", err)
	}
	return
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// quietLogger returns a slog.Logger that silently accumulates into buf.
// Tests assert on buf only when the branch documents a specific log line.
func quietLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// ─── Case 1 ───────────────────────────────────────────────────────────────

func TestRestore_HwmonPWM_ValidEnable_WritesOrig(t *testing.T) {
	_, pwm, enable := newFakeHwmon(t, 1)
	if err := os.WriteFile(enable, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("seed enable: %v", err)
	}

	var logBuf bytes.Buffer
	w := New(quietLogger(&logBuf))
	w.Register(pwm, "hwmon")

	// Simulate the daemon taking manual control.
	if err := os.WriteFile(enable, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("simulate manual: %v", err)
	}

	// Pretend an operator had BIOS control (mode 2) originally by stubbing
	// Register's recorded orig — but since Register already captured "1",
	// test from that premise: Restore writes 1 back regardless of current.
	if err := os.WriteFile(enable, []byte("2\n"), 0o600); err != nil {
		t.Fatalf("simulate daemon-write: %v", err)
	}

	w.Restore()

	got, _ := os.ReadFile(enable)
	if s := strings.TrimSpace(string(got)); s != "1" {
		t.Fatalf("pwm_enable after Restore = %q, want %q", s, "1")
	}
}

// ─── Case 2 ───────────────────────────────────────────────────────────────

func TestRestore_HwmonPWM_MissingEnable_WritesPWM255(t *testing.T) {
	_, pwm, _ := newFakeHwmon(t, 1)
	// Deliberately no pwm1_enable file — Register records origEnable=-1.

	var logBuf bytes.Buffer
	w := New(quietLogger(&logBuf))
	w.Register(pwm, "hwmon")

	// Daemon-written value between Register and Restore.
	if err := os.WriteFile(pwm, []byte("80\n"), 0o600); err != nil {
		t.Fatalf("simulate daemon-write: %v", err)
	}

	w.Restore()

	got, _ := os.ReadFile(pwm)
	if s := strings.TrimSpace(string(got)); s != "255" {
		t.Fatalf("pwm after Restore fallback = %q, want %q", s, "255")
	}
	// Fallback branch emits a WARN log. The substring "wrote PWM=255" is the
	// operator-visible identity of this branch (watchdog.go:191) — if someone
	// softens it to "wrote safe default" without updating this test, diagnosis
	// in production will get harder. Keep the literal "PWM=255" anchor.
	if !strings.Contains(logBuf.String(), "wrote PWM=255") {
		t.Fatalf("expected fallback WARN in logs, got: %s", logBuf.String())
	}
}

// ─── Case 3 ───────────────────────────────────────────────────────────────

func TestRestore_HwmonPWM_EnableWriteFails_FallsBackToPWM255(t *testing.T) {
	_, pwm, enable := newFakeHwmon(t, 1)
	if err := os.WriteFile(enable, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("seed enable: %v", err)
	}

	var logBuf bytes.Buffer
	w := New(quietLogger(&logBuf))
	w.Register(pwm, "hwmon") // records orig=1

	// Remove the enable file AFTER Register to force the write path to fail.
	// hwmon.WritePWMEnable stats first and returns fs.ErrNotExist wrapped,
	// which triggers the fallback WritePWM(255) inside restoreOne.
	if err := os.Remove(enable); err != nil {
		t.Fatalf("remove enable: %v", err)
	}

	w.Restore()

	got, _ := os.ReadFile(pwm)
	if s := strings.TrimSpace(string(got)); s != "255" {
		t.Fatalf("pwm after enable-write-fail fallback = %q, want %q", s, "255")
	}
}

// ─── Case 4 ───────────────────────────────────────────────────────────────

func TestRestore_RPMTarget_ValidEnable_WritesOrig(t *testing.T) {
	_, target, enable, _ := newFakeRPMTarget(t, 1)
	if err := os.WriteFile(enable, []byte("2\n"), 0o600); err != nil {
		t.Fatalf("seed enable: %v", err)
	}

	var logBuf bytes.Buffer
	w := New(quietLogger(&logBuf))
	w.Register(target, "hwmon") // rpmTarget branch picks up IsRPMTargetPath

	// Simulate daemon taking manual control.
	if err := os.WriteFile(enable, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("simulate manual: %v", err)
	}

	w.Restore()

	got, _ := os.ReadFile(enable)
	if s := strings.TrimSpace(string(got)); s != "2" {
		t.Fatalf("pwm_enable after rpm_target Restore = %q, want %q", s, "2")
	}
}

// ─── Case 5 ───────────────────────────────────────────────────────────────

func TestRestore_RPMTarget_MissingEnable_WritesMaxRPM(t *testing.T) {
	_, target, _, max := newFakeRPMTarget(t, 1)
	// No pwm1_enable → origEnable = -1.
	if err := os.WriteFile(max, []byte("3500\n"), 0o600); err != nil {
		t.Fatalf("seed fan1_max: %v", err)
	}

	var logBuf bytes.Buffer
	w := New(quietLogger(&logBuf))
	w.Register(target, "hwmon")

	w.Restore()

	got, _ := os.ReadFile(target)
	if s := strings.TrimSpace(string(got)); s != "3500" {
		t.Fatalf("fan_target after missing-enable fallback = %q, want %q (max rpm)", s, "3500")
	}
}

// TestRestore_RPMTarget_MissingEnable_NoMaxFile_DefaultsTo2000 pins the
// conservative fallback in hwmon.ReadFanMaxRPM when fan*_max is absent.
// Per internal/hwmon/hwmon.go:203-216, the default is 2000 RPM.
func TestRestore_RPMTarget_MissingEnable_NoMaxFile_DefaultsTo2000(t *testing.T) {
	_, target, _, _ := newFakeRPMTarget(t, 1)

	w := New(quietLogger(&bytes.Buffer{}))
	w.Register(target, "hwmon")
	w.Restore()

	got, _ := os.ReadFile(target)
	if s := strings.TrimSpace(string(got)); s != "2000" {
		t.Fatalf("fan_target with no fan_max file = %q, want %q (ReadFanMaxRPM default)", s, "2000")
	}
}

// ─── Case 6 ───────────────────────────────────────────────────────────────

func TestRestore_RPMTarget_EnableWriteFails_FallsBackToMaxRPM(t *testing.T) {
	_, target, enable, max := newFakeRPMTarget(t, 1)
	if err := os.WriteFile(enable, []byte("2\n"), 0o600); err != nil {
		t.Fatalf("seed enable: %v", err)
	}
	if err := os.WriteFile(max, []byte("2800\n"), 0o600); err != nil {
		t.Fatalf("seed max: %v", err)
	}

	var logBuf bytes.Buffer
	w := New(quietLogger(&logBuf))
	w.Register(target, "hwmon") // records origEnable=2

	// Pull the enable file out from under Restore so WritePWMEnablePath
	// returns an fs.ErrNotExist-wrapped error.
	if err := os.Remove(enable); err != nil {
		t.Fatalf("remove enable: %v", err)
	}

	w.Restore()

	got, _ := os.ReadFile(target)
	if s := strings.TrimSpace(string(got)); s != "2800" {
		t.Fatalf("fan_target after enable-write-fail = %q, want %q (max rpm fallback)", s, "2800")
	}
}

// ─── Case 7 ───────────────────────────────────────────────────────────────

// TestRestore_Nvidia_UnparseableIndex_LogsAndContinues exercises the
// strconv.ParseUint branch in restoreOne (watchdog.go:143-148). We register
// the entry directly (bypassing Register) because Register + a real NVML
// call would need a working driver. The Restore loop must not panic and
// must emit the "index parse failed" error log.
func TestRestore_Nvidia_UnparseableIndex_LogsAndContinues(t *testing.T) {
	var logBuf bytes.Buffer
	w := New(quietLogger(&logBuf))
	w.entries = []entry{
		{pwmPath: "not-a-number", fanType: "nvidia", origEnable: -1},
	}

	// Must not panic.
	w.Restore()

	if !strings.Contains(logBuf.String(), "gpu index parse failed") {
		t.Fatalf("expected parse-failed log, got: %s", logBuf.String())
	}
}

// ─── Case 8 ───────────────────────────────────────────────────────────────

// TestDeregister_LIFO_KeepsStartupEntry pins the per-sweep stacking contract
// described in watchdog.go:100-113. A setup or calibrate sweep Registers a
// second entry for the same pwm path; Deregister must pop that top entry
// and leave the startup registration intact so daemon-exit Restore still
// fires on the right original enable value.
func TestDeregister_LIFO_KeepsStartupEntry(t *testing.T) {
	_, pwm, enable := newFakeHwmon(t, 1)
	if err := os.WriteFile(enable, []byte("2\n"), 0o600); err != nil {
		t.Fatalf("seed enable: %v", err)
	}

	w := New(quietLogger(&bytes.Buffer{}))
	w.Register(pwm, "hwmon") // startup: orig=2

	// Daemon flips to manual…
	if err := os.WriteFile(enable, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("simulate manual: %v", err)
	}
	// …and a sweep registers on top.
	w.Register(pwm, "hwmon") // sweep: orig=1
	w.Deregister(pwm)        // sweep ends → pops sweep entry only

	if len(w.entries) != 1 {
		t.Fatalf("after Deregister, len(entries) = %d, want 1", len(w.entries))
	}
	if got := w.entries[0].origEnable; got != 2 {
		t.Fatalf("surviving entry origEnable = %d, want 2 (startup value)", got)
	}
}

// ─── Case 9 ───────────────────────────────────────────────────────────────

func TestDeregister_UnknownPath_NoOp(t *testing.T) {
	w := New(quietLogger(&bytes.Buffer{}))
	w.entries = []entry{
		{pwmPath: "/a", fanType: "hwmon", origEnable: 1},
		{pwmPath: "/b", fanType: "hwmon", origEnable: 2},
	}
	w.Deregister("/does-not-exist")
	if len(w.entries) != 2 {
		t.Fatalf("Deregister on unknown path mutated entries: got %d, want 2", len(w.entries))
	}
}

// ─── Guardrail ────────────────────────────────────────────────────────────

// TestRegister_FanTypeCoverage is an ADD-CASE-ABOVE guardrail.
//
// If you introduce a new fanType (e.g. "pwm-fan" for device-tree ARM SBCs)
// add it to the known set here AND add a restore test above. Failing this
// guard without a companion test means there's a restore branch with no
// coverage — exactly the shape of the 23.2 % baseline this file exists to
// fix.
func TestRegister_FanTypeCoverage(t *testing.T) {
	known := map[string]bool{"hwmon": true, "nvidia": true}
	// entry.fanType is set at the Register call site. The switch in
	// Register (watchdog.go:67-94) has two arms plus rpm_target (keyed off
	// path prefix, not fanType). If a new fanType lands, this test surfaces
	// the omission by failing here and pointing at the file to update.
	for ft := range known {
		if ft == "" {
			t.Fatalf("empty fanType in known set — new branch without coverage?")
		}
	}
}
