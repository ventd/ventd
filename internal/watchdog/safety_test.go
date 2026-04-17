package watchdog

// safety_test.go binds every rule in .claude/rules/watchdog-safety.md to a
// named subtest inside TestWDSafety_Invariants. The rule-lint under
// tools/rulelint walks the Bound: markers in the rules file and fails CI
// if any named subtest here goes missing.
//
// These tests are intentionally narrow and read-only with respect to
// production code — T-WD-01 is a test-only task. Any gap that would
// require a production change to verify is documented on the subtest
// itself and flagged in the PR body under CONCERNS, not hidden with a
// t.Skip.
//
// Shared fixture conventions:
//
//   newFakePWM(t)        → hwmon-style pwmN + pwmN_enable seeded in a TempDir
//   newFakeRPMTargetDir  → amdgpu-style fanN_target + pwmN_enable + fanN_max
//   nvmlStub             → a tiny in-process NVML-substitute used only to
//                          assert contract-level behaviour (no real driver).

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/testfixture/fakehwmon"
)

// TestWDSafety_Invariants is the rule-to-test index for the watchdog
// safety envelope. Each subtest binds one invariant from
// .claude/rules/watchdog-safety.md. Do not delete or rename a subtest
// without updating the Bound: line of the matching rule.
func TestWDSafety_Invariants(t *testing.T) {
	// ─── RULE-WD-RESTORE-EXIT ──────────────────────────────────────────

	t.Run("wd_restore_exit_touches_all_entries", func(t *testing.T) {
		// Every registered entry must receive a restore write when the
		// daemon exits gracefully. We stand up a fakehwmon tree with two
		// pwm channels, register both with origEnable=1, flip them to
		// manual-mode (2), call Restore, and assert both enable files
		// ended up back at 1.
		enable1 := 1
		enable2 := 1
		fake := fakehwmon.New(t, &fakehwmon.Options{
			Chips: []fakehwmon.ChipOptions{{
				Name: "nct6798",
				PWMs: []fakehwmon.PWMOptions{
					{Index: 1, PWM: 128, Enable: enable1},
					{Index: 2, PWM: 128, Enable: enable2},
				},
			}},
		})
		pwm1 := filepath.Join(fake.Root, "hwmon0", "pwm1")
		pwm2 := filepath.Join(fake.Root, "hwmon0", "pwm2")
		enablePath1 := pwm1 + "_enable"
		enablePath2 := pwm2 + "_enable"

		var buf bytes.Buffer
		w := New(slog.New(slog.NewTextHandler(&buf, nil)))
		w.Register(pwm1, "hwmon")
		w.Register(pwm2, "hwmon")

		// Simulate the daemon taking manual control of both channels.
		if err := os.WriteFile(enablePath1, []byte("1\n"), 0o600); err != nil {
			t.Fatalf("simulate manual ch1: %v", err)
		}
		if err := os.WriteFile(enablePath2, []byte("1\n"), 0o600); err != nil {
			t.Fatalf("simulate manual ch2: %v", err)
		}
		// Move both to some daemon-written non-original state so the
		// post-Restore read proves a write actually happened.
		if err := os.WriteFile(enablePath1, []byte("2\n"), 0o600); err != nil {
			t.Fatalf("perturb ch1: %v", err)
		}
		if err := os.WriteFile(enablePath2, []byte("2\n"), 0o600); err != nil {
			t.Fatalf("perturb ch2: %v", err)
		}

		w.Restore()

		if got := readTrimmed(t, enablePath1); got != strconv.Itoa(enable1) {
			t.Errorf("pwm1_enable after Restore = %q, want %q", got, strconv.Itoa(enable1))
		}
		if got := readTrimmed(t, enablePath2); got != strconv.Itoa(enable2) {
			t.Errorf("pwm2_enable after Restore = %q, want %q", got, strconv.Itoa(enable2))
		}
	})

	// ─── RULE-WD-RESTORE-PANIC ─────────────────────────────────────────

	t.Run("wd_restore_panic_continues_loop", func(t *testing.T) {
		// A synthetic panic injected on the first log call must be
		// recovered per-entry so the second entry still gets its
		// restore attempt. We inject via a custom slog handler that
		// counts calls and panics on call #1. Both entries use
		// origEnable=-1 to guarantee exactly one log call per entry
		// via the full-speed fallback's WritePWM-to-bogus-path failure.
		h := &countingPanicHandler{panicOn: 1}
		w := New(slog.New(h))
		w.entries = []entry{
			{pwmPath: "/nonexistent/wd-safety-a", fanType: "hwmon", origEnable: -1},
			{pwmPath: "/nonexistent/wd-safety-b", fanType: "hwmon", origEnable: -1},
		}

		// Must not panic out — the per-entry defer/recover catches it.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Restore panicked out: %v", r)
				}
			}()
			w.Restore()
		}()

		// entry 1: one log call (panic). restoreOne's recover frame
		// then emits an Error log (identity log). entry 2: at least one
		// log call. Total ≥ 3 is the load-bearing lower bound.
		if got := h.calls.Load(); got < 3 {
			t.Fatalf("expected >=3 log calls (entry1 fallback + recover diag + entry2 fallback), got %d", got)
		}
	})

	// ─── RULE-WD-FALLBACK-MISSING-PWMENABLE ────────────────────────────

	t.Run("wd_fallback_missing_pwm_enable_continues", func(t *testing.T) {
		// A channel with its pwm_enable file absent registers with
		// origEnable=-1 and at Restore time takes the PWM=255 fallback.
		// A second channel with a valid enable file must still be
		// restored afterwards — the missing-file path is log-and-
		// continue, not log-and-return.
		fake := fakehwmon.New(t, &fakehwmon.Options{
			Chips: []fakehwmon.ChipOptions{{
				PWMs: []fakehwmon.PWMOptions{
					{Index: 1, PWM: 77, Enable: 1},
					{Index: 2, PWM: 77, Enable: 1},
				},
			}},
		})
		chipDir := filepath.Join(fake.Root, "hwmon0")
		pwm1 := filepath.Join(chipDir, "pwm1")
		pwm2 := filepath.Join(chipDir, "pwm2")
		// Remove pwm1's enable file so Register records origEnable=-1
		// for the first entry but still has a real origEnable for the
		// second — the two entries exercise two different restoreOne
		// branches in the same Restore sweep.
		if err := os.Remove(pwm1 + "_enable"); err != nil {
			t.Fatalf("remove pwm1_enable: %v", err)
		}

		var buf bytes.Buffer
		w := New(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
		w.Register(pwm1, "hwmon") // origEnable=-1 (enable file missing)
		w.Register(pwm2, "hwmon") // origEnable=1

		// Daemon-written states prior to Restore.
		if err := os.WriteFile(pwm1, []byte("88\n"), 0o600); err != nil {
			t.Fatalf("seed pwm1: %v", err)
		}
		if err := os.WriteFile(pwm2+"_enable", []byte("2\n"), 0o600); err != nil {
			t.Fatalf("perturb pwm2_enable: %v", err)
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Restore panicked on missing pwm_enable: %v", r)
				}
			}()
			w.Restore()
		}()

		// Entry 1 fell back to PWM=255 on the duty-cycle file.
		if got := readTrimmed(t, pwm1); got != "255" {
			t.Errorf("pwm1 fallback after missing-enable = %q, want %q", got, "255")
		}
		// Entry 2 still got its enable restored — proves the loop
		// continued past the fallback in entry 1.
		if got := readTrimmed(t, pwm2+"_enable"); got != "1" {
			t.Errorf("pwm2_enable after Restore = %q, want %q (loop must not early-return)", got, "1")
		}
		// The fallback branch's operator-facing log line is the
		// identity of this rule — keep the literal anchor. If a
		// future cleanup softens the message, this test surfaces it.
		if !strings.Contains(buf.String(), "wrote PWM=255") {
			t.Errorf("expected fallback WARN anchor in logs, got: %s", buf.String())
		}
	})

	// ─── RULE-WD-NVIDIA-RESET ──────────────────────────────────────────

	t.Run("wd_nvidia_restore_uses_auto_not_zero", func(t *testing.T) {
		// The production path calls nvidia.ResetFanSpeed, which wraps
		// nvmlDeviceSetDefaultFanSpeed_v2 — the NVML manufacturer-default
		// primitive for fans (equivalent to
		// nvmlDeviceSetDefaultAutoBoostedClocksEnabled for clocks).
		// Without a real driver we can only assert the branch-level
		// contract: an nvidia entry with a non-parseable index is
		// logged-and-skipped, and a valid-looking index dispatches into
		// the nvidia package rather than writing PWM=0 anywhere.
		//
		// We verify the no-PWM-write clause indirectly by registering
		// a hwmon channel alongside and showing its pwm file is NOT
		// touched with "0" by an nvidia restore — the nvidia branch
		// returns before touching any hwmon path.
		fake := fakehwmon.New(t, &fakehwmon.Options{
			Chips: []fakehwmon.ChipOptions{{
				PWMs: []fakehwmon.PWMOptions{{Index: 1, PWM: 99, Enable: 1}},
			}},
		})
		pwm := filepath.Join(fake.Root, "hwmon0", "pwm1")

		var buf bytes.Buffer
		w := New(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

		// One nvidia entry with a malformed index + one real hwmon
		// channel the nvidia branch must NOT touch.
		stub := newNVMLStub()
		_ = stub // stub is documentation for how a real NVML test would
		// plug in; real nvml calls fall through to nvidia.ResetFanSpeed
		// which returns ErrNotAvailable on CI hosts without a driver —
		// that's still not a zero-write.

		w.entries = []entry{
			{pwmPath: "not-a-uint", fanType: "nvidia", origEnable: -1},
			{pwmPath: pwm, fanType: "hwmon", origEnable: 1},
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Restore panicked on nvidia bad-index entry: %v", r)
				}
			}()
			w.Restore()
		}()

		if !strings.Contains(buf.String(), "gpu index parse failed") {
			t.Errorf("expected nvidia parse-failed log, got: %s", buf.String())
		}
		// The hwmon pwm file must not have been set to "0" by the
		// nvidia branch. The hwmon branch below writes pwm_enable=1
		// back, not PWM=0, so the duty-cycle byte is whatever the fake
		// seeded it to.
		if got := readTrimmed(t, pwm); got == "0" {
			t.Errorf("hwmon pwm = %q; nvidia restore must not zero hwmon paths", got)
		}
	})

	// ─── RULE-WD-RPM-TARGET ────────────────────────────────────────────

	t.Run("wd_rpm_target_restore_uses_max_rpm", func(t *testing.T) {
		// A fan*_target channel with its pwm_enable file missing must
		// fall back to WriteFanTarget(fan*_max), not to "255" (which
		// would mean 255 RPM on this driver family). We seed fan1_max
		// to a non-default value so the assertion can't accidentally
		// pass via the hwmon default of 2000.
		dir := t.TempDir()
		target := filepath.Join(dir, "fan1_target")
		maxPath := filepath.Join(dir, "fan1_max")
		if err := os.WriteFile(target, []byte("1000\n"), 0o600); err != nil {
			t.Fatalf("seed fan1_target: %v", err)
		}
		if err := os.WriteFile(maxPath, []byte("3300\n"), 0o600); err != nil {
			t.Fatalf("seed fan1_max: %v", err)
		}
		// Deliberately NO pwm1_enable file.

		var buf bytes.Buffer
		w := New(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
		w.Register(target, "hwmon")

		w.Restore()

		if got := readTrimmed(t, target); got != "3300" {
			t.Errorf("fan1_target after rpm_target missing-enable = %q, want %q (fan1_max fallback, not raw 255)", got, "3300")
		}
		// Anchor the operator-facing log line for this branch.
		if !strings.Contains(buf.String(), "fan_target=max_rpm") {
			t.Errorf("expected fan_target=max_rpm anchor in logs, got: %s", buf.String())
		}
	})

	// ─── RULE-WD-DEREGISTER ────────────────────────────────────────────

	t.Run("wd_deregister_unknown_and_double_is_noop", func(t *testing.T) {
		// Deregister on an unknown path does not panic and does not
		// mutate entries. Double-Deregister of the same path removes
		// at most one entry (LIFO top) — the second call is a no-op.
		w := New(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
		w.entries = []entry{
			{pwmPath: "/a", fanType: "hwmon", origEnable: 1},
			{pwmPath: "/b", fanType: "hwmon", origEnable: 2},
			{pwmPath: "/a", fanType: "hwmon", origEnable: 3}, // sweep stack
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Deregister panicked on unknown path: %v", r)
				}
			}()
			w.Deregister("/does-not-exist")
		}()

		if len(w.entries) != 3 {
			t.Fatalf("unknown-path Deregister mutated entries: len=%d, want 3", len(w.entries))
		}

		// First Deregister("/a") pops the sweep entry (origEnable=3).
		w.Deregister("/a")
		if len(w.entries) != 2 {
			t.Fatalf("first Deregister('/a'): len=%d, want 2", len(w.entries))
		}
		// Surviving '/a' entry is the original (origEnable=1).
		var aOrig int
		for _, e := range w.entries {
			if e.pwmPath == "/a" {
				aOrig = e.origEnable
			}
		}
		if aOrig != 1 {
			t.Errorf("after first Deregister('/a'), surviving origEnable=%d, want 1", aOrig)
		}

		// Second Deregister("/a") pops the surviving entry.
		w.Deregister("/a")
		if len(w.entries) != 1 {
			t.Fatalf("second Deregister('/a'): len=%d, want 1", len(w.entries))
		}

		// Third Deregister("/a") is the no-op the rule binds to.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("third (no-op) Deregister('/a') panicked: %v", r)
				}
			}()
			w.Deregister("/a")
		}()
		if len(w.entries) != 1 {
			t.Errorf("no-op Deregister mutated entries: len=%d, want 1", len(w.entries))
		}
	})

	// ─── RULE-WD-REGISTER-IDEMPOTENT ───────────────────────────────────

	t.Run("wd_register_preserves_startup_origenable", func(t *testing.T) {
		// Register stacks; the per-sweep registration layers on top of
		// the startup registration for the same pwmPath. After any
		// Register + Deregister cycle, the startup entry's origEnable
		// must still be readable, not overwritten by a subsequent
		// Register capture.
		fake := fakehwmon.New(t, &fakehwmon.Options{
			Chips: []fakehwmon.ChipOptions{{
				PWMs: []fakehwmon.PWMOptions{{Index: 1, PWM: 100, Enable: 2}},
			}},
		})
		pwm := filepath.Join(fake.Root, "hwmon0", "pwm1")
		enablePath := pwm + "_enable"

		w := New(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
		w.Register(pwm, "hwmon") // startup: origEnable captured = 2

		// Daemon flips to manual mode (1).
		if err := os.WriteFile(enablePath, []byte("1\n"), 0o600); err != nil {
			t.Fatalf("simulate manual mode: %v", err)
		}

		// Sweep (calibration/setup) registers on top. The second Register
		// observes enable=1 — if it overwrote the startup entry's
		// origEnable, the daemon-exit Restore would later write "1"
		// instead of "2", losing the pre-daemon state.
		w.Register(pwm, "hwmon") // sweep: origEnable captured = 1
		w.Deregister(pwm)        // sweep ends → pops sweep entry

		if len(w.entries) != 1 {
			t.Fatalf("after sweep cycle, len(entries) = %d, want 1", len(w.entries))
		}
		if got := w.entries[0].origEnable; got != 2 {
			t.Fatalf("startup entry origEnable = %d, want 2 (must NOT be overwritten by sweep Register)", got)
		}

		// End-to-end proof: Restore writes the startup value back.
		w.Restore()
		if got := readTrimmed(t, enablePath); got != "2" {
			t.Errorf("pwm_enable after daemon-exit Restore = %q, want %q (startup value preserved)", got, "2")
		}
	})
}

// ─── test helpers ─────────────────────────────────────────────────────

// readTrimmed reads a sysfs-style file and returns its content with
// trailing whitespace removed. t.Fatalfs on read error so subtests can
// assert inline on the returned string.
func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(b))
}

// countingPanicHandler is a slog.Handler that counts Handle calls and
// panics on the panicOn-th call (1-indexed). Used by the RESTORE-PANIC
// subtest to inject a synthetic panic into restoreOne without touching
// production code.
type countingPanicHandler struct {
	calls   atomic.Int32
	panicOn int32
}

func (h *countingPanicHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *countingPanicHandler) WithAttrs(_ []slog.Attr) slog.Handler         { return h }
func (h *countingPanicHandler) WithGroup(_ string) slog.Handler              { return h }
func (h *countingPanicHandler) Handle(_ context.Context, _ slog.Record) error {
	n := h.calls.Add(1)
	if n == h.panicOn {
		panic("synthetic safety-test panic")
	}
	return nil
}

// nvmlStub is a minimal in-process stand-in for NVML used to document the
// contract asserted by RULE-WD-NVIDIA-RESET. It records the last call made
// against it so a future refactor that wires NVML through the hal layer
// can flip the nvidia branch to call this stub under test.
type nvmlStub struct {
	resetCalls atomic.Int32
	lastIndex  atomic.Uint32
}

func newNVMLStub() *nvmlStub { return &nvmlStub{} }

// ResetFan records a call. Not wired into production today — the watchdog
// calls nvidia.ResetFanSpeed directly — so this stub exists purely to
// pin the shape of the contract for future production changes. See
// CONCERNS in the task PR body for the gap.
func (s *nvmlStub) ResetFan(index uint) error {
	s.resetCalls.Add(1)
	s.lastIndex.Store(uint32(index))
	return nil
}
