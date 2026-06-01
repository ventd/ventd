package watchdog

// safety_test.go binds every rule in docs/rules/watchdog-safety.md to a
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
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/testfixture/fakehwmon"
)

// TestWDSafety_Invariants is the rule-to-test index for the watchdog
// safety envelope. Each subtest binds one invariant from
// docs/rules/watchdog-safety.md. Do not delete or rename a subtest
// without updating the Bound: line of the matching rule.
func TestWDSafety_Invariants(t *testing.T) {
	// ─── RULE-WD-RESTORE-EXIT ──────────────────────────────────────────

	t.Run("wd_restore_exit_touches_all_entries", func(t *testing.T) {
		// Every registered entry must receive a restore write when the
		// daemon exits gracefully. We stand up a fakehwmon tree with two
		// pwm channels seeded enable=2 (BIOS auto — the legitimate
		// pre-daemon state), register both, flip them to manual-mode
		// (1), call Restore, and assert both enable files ended up
		// back at 2.
		//
		// Pre-#1039 seed was enable=1 (the manual-mode case), but per
		// RULE-WD-PRIOR-CRASH-FALLBACK that's now treated as a prior-
		// crash residual and Register overrides origEnable to 2. Tests
		// that want to exercise restore-to-original use enable=2 at
		// seed time.
		enable1 := 2
		enable2 := 2
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

		w.Restore()

		if got := readTrimmed(t, enablePath1); got != strconv.Itoa(enable1) {
			t.Errorf("pwm1_enable after Restore = %q, want %q", got, strconv.Itoa(enable1))
		}
		if got := readTrimmed(t, enablePath2); got != strconv.Itoa(enable2) {
			t.Errorf("pwm2_enable after Restore = %q, want %q", got, strconv.Itoa(enable2))
		}

		// RULE-WD-RESTORE-EXIT also covers RestoreOne (per #287 audit). Verify
		// that after a successful Restore, a subsequent RestoreOne(pwm1)
		// writes origEnable back without modifying the entries slice.
		if err := os.WriteFile(enablePath1, []byte("1\n"), 0o600); err != nil {
			t.Fatalf("perturb ch1 for RestoreOne leg: %v", err)
		}
		w.RestoreOne(pwm1)
		if got := readTrimmed(t, enablePath1); got != strconv.Itoa(enable1) {
			t.Errorf("RestoreOne(pwm1): pwm1_enable = %q, want %q",
				got, strconv.Itoa(enable1))
		}
		if len(w.entries) != 2 {
			t.Errorf("RestoreOne must not deregister: len(entries) = %d, want 2",
				len(w.entries))
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
					{Index: 1, PWM: 77, Enable: 2},
					{Index: 2, PWM: 77, Enable: 2},
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
		w.Register(pwm2, "hwmon") // origEnable=2 (legitimate pre-daemon state)

		// Daemon-written states prior to Restore.
		if err := os.WriteFile(pwm1, []byte("88\n"), 0o600); err != nil {
			t.Fatalf("seed pwm1: %v", err)
		}
		if err := os.WriteFile(pwm2+"_enable", []byte("1\n"), 0o600); err != nil {
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
		if got := readTrimmed(t, pwm2+"_enable"); got != "2" {
			t.Errorf("pwm2_enable after Restore = %q, want %q (loop must not early-return)", got, "2")
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

	// ─── RULE-WD-RESTORE-BUDGET ────────────────────────────────────────

	t.Run("wd_restore_completes_within_budget", func(t *testing.T) {
		// Per-channel restores run in parallel goroutines. With three
		// fast-write channels (fakehwmon writes are microseconds) and
		// a generous 500 ms budget, every entry's _enable file MUST
		// be back at its origEnable value within the budget, no
		// abandoned-channels WARN must fire, and total wall clock
		// MUST be under the budget — proves that the parallel
		// dispatch landed and the deadline-exceeded branch is NOT
		// firing on the happy path.
		fake := fakehwmon.New(t, &fakehwmon.Options{
			Chips: []fakehwmon.ChipOptions{{
				Name: "nct6798",
				PWMs: []fakehwmon.PWMOptions{
					{Index: 1, PWM: 128, Enable: 2},
					{Index: 2, PWM: 128, Enable: 2},
					{Index: 3, PWM: 128, Enable: 2},
				},
			}},
		})
		paths := []string{
			filepath.Join(fake.Root, "hwmon0", "pwm1"),
			filepath.Join(fake.Root, "hwmon0", "pwm2"),
			filepath.Join(fake.Root, "hwmon0", "pwm3"),
		}

		var buf bytes.Buffer
		w := New(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		for _, p := range paths {
			w.Register(p, "hwmon")
			// Move to manual mode (1) so the post-Restore read proves
			// a write back to BIOS auto (2) actually happened.
			if err := os.WriteFile(p+"_enable", []byte("1\n"), 0o600); err != nil {
				t.Fatalf("perturb %s_enable: %v", p, err)
			}
		}

		// The happy path is proven deterministically by the two checks
		// below — every channel restored to BIOS auto (2) AND no
		// abandoned-channels WARN, which together prove the deadline-
		// exceeded branch did not fire. The earlier wall-clock upper-
		// bound assertion (elapsed < 400 ms) raced scheduler jitter
		// under -race on shared GHA runners and added nothing the
		// no-WARN check doesn't already guarantee (#1361).
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		w.RestoreCtx(ctx)
		for _, p := range paths {
			if got := readTrimmed(t, p+"_enable"); got != "2" {
				t.Errorf("%s_enable after RestoreCtx = %q, want %q", p, got, "2")
			}
		}
		if strings.Contains(buf.String(), "abandoning in-flight goroutines") {
			t.Errorf("happy path emitted abandoned-channels WARN; logs:\n%s", buf.String())
		}
	})

	t.Run("wd_restore_budget_exceeded_logs_abandoned_continues_others", func(t *testing.T) {
		// Inject a stub that hangs for one channel and runs the real
		// restore for the others. With a 100 ms budget the hung
		// channel's goroutine is abandoned (it keeps sleeping until
		// the test exits) but the function MUST return within the
		// budget + a small grace, the other channels MUST be
		// restored, and the abandoned-channels WARN MUST name the
		// hung path so operators can correlate the journal entry
		// to the specific fan that didn't complete.
		fake := fakehwmon.New(t, &fakehwmon.Options{
			Chips: []fakehwmon.ChipOptions{{
				Name: "nct6798",
				PWMs: []fakehwmon.PWMOptions{
					{Index: 1, PWM: 128, Enable: 2},
					{Index: 2, PWM: 128, Enable: 2},
					{Index: 3, PWM: 128, Enable: 2},
				},
			}},
		})
		hungPath := filepath.Join(fake.Root, "hwmon0", "pwm2")
		fastPaths := []string{
			filepath.Join(fake.Root, "hwmon0", "pwm1"),
			filepath.Join(fake.Root, "hwmon0", "pwm3"),
		}

		stop := make(chan struct{})
		t.Cleanup(func() { close(stop) })

		var buf bytes.Buffer
		w := New(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		w.Register(fastPaths[0], "hwmon")
		w.Register(hungPath, "hwmon")
		w.Register(fastPaths[1], "hwmon")
		// Issue #1178: per-instance seam, set BEFORE Register's
		// downstream RestoreCtx ever reads it. Hung-path stub blocks
		// until the test's deferred close(stop); fast paths fall
		// through to the real restoreOne, exercising the deadline-
		// exceeded vs successful-restore mix the rule requires.
		w.SetRestoreOneFnForTest(func(e entry) {
			if e.pwmPath == hungPath {
				select {
				case <-stop:
				case <-time.After(5 * time.Second):
				}
				return
			}
			w.restoreOne(e)
		})

		for _, p := range append(fastPaths, hungPath) {
			// Perturb to manual mode (1) so the post-Restore read
			// proves a write back to BIOS auto (2) actually happened.
			if err := os.WriteFile(p+"_enable", []byte("1\n"), 0o600); err != nil {
				t.Fatalf("perturb %s_enable: %v", p, err)
			}
		}

		// The hung channel's goroutine blocks until t.Cleanup closes
		// `stop`, so RestoreCtx returning AT ALL — while that peer is
		// still in-flight — deterministically proves the budget branch
		// unblocked the caller (otherwise RestoreCtx would block on the
		// hung goroutine indefinitely). Synchronise on a done channel
		// rather than asserting a wall-clock upper bound, which raced
		// scheduler jitter under -race on shared runners (#1361). The
		// 5 s safety timeout never false-fires on a correct impl (which
		// returns within the 100 ms budget) and a genuine leak hangs it
		// to a clear failure.
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		done := make(chan struct{})
		go func() {
			w.RestoreCtx(ctx)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("RestoreCtx did not unblock within 5s despite the 100ms budget; the deadline-exceeded branch failed to unblock the caller while a peer was hung")
		}
		for _, p := range fastPaths {
			if got := readTrimmed(t, p+"_enable"); got != "2" {
				t.Errorf("%s_enable after RestoreCtx = %q, want %q (parallel restore must have completed despite the hung peer)", p, got, "2")
			}
		}
		// The hung path's _enable file is still at the perturbed value
		// because its goroutine never reached the backend write.
		if got := readTrimmed(t, hungPath+"_enable"); got != "1" {
			t.Errorf("hung path %s_enable = %q, want %q (its goroutine must still be running, not a successful write)", hungPath, got, "1")
		}
		// The WARN log must name the abandoned channel so an operator
		// reading the journal can correlate the budget overrun to the
		// specific fan path.
		logOut := buf.String()
		if !strings.Contains(logOut, "abandoning in-flight goroutines") {
			t.Errorf("expected abandoned-channels WARN, got:\n%s", logOut)
		}
		if !strings.Contains(logOut, hungPath) {
			t.Errorf("expected hung path %q named in WARN, got:\n%s", hungPath, logOut)
		}
		if !strings.Contains(logOut, `"abandoned_count":1`) && !strings.Contains(logOut, "abandoned_count=1") {
			t.Errorf("expected abandoned_count=1 in WARN, got:\n%s", logOut)
		}
	})

	t.Run("wd_restore_pre_cancelled_ctx_skips_backend", func(t *testing.T) {
		// A ctx that's already cancelled at the moment RestoreCtx
		// reaches restoreOneCtx for an entry must skip the backend
		// dispatch entirely. The _enable file stays at its perturbed
		// value (proof we did NOT call into the backend) and a WARN
		// MUST be emitted naming the skip + the cancellation cause.
		fake := fakehwmon.New(t, &fakehwmon.Options{
			Chips: []fakehwmon.ChipOptions{{
				Name: "nct6798",
				PWMs: []fakehwmon.PWMOptions{
					{Index: 1, PWM: 128, Enable: 1},
				},
			}},
		})
		pwm := filepath.Join(fake.Root, "hwmon0", "pwm1")

		var buf bytes.Buffer
		w := New(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		w.Register(pwm, "hwmon")
		if err := os.WriteFile(pwm+"_enable", []byte("2\n"), 0o600); err != nil {
			t.Fatalf("perturb pwm_enable: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel before RestoreCtx is even called

		w.RestoreCtx(ctx)

		// _enable file must still read 2 — restoreOneCtx returned
		// without dispatching to the backend.
		if got := readTrimmed(t, pwm+"_enable"); got != "2" {
			t.Errorf("pwm_enable after pre-cancelled RestoreCtx = %q, want %q (backend MUST be skipped)", got, "2")
		}
		// The skip path may emit either the per-entry "ctx cancelled
		// before backend call" WARN OR the budget-exceeded
		// "abandoning in-flight goroutines" WARN depending on whether
		// the goroutine reached its ctx pre-check before the
		// RestoreCtx outer select fired. Either is acceptable; both
		// communicate "we did not dispatch the backend on this run."
		logOut := buf.String()
		if !strings.Contains(logOut, "ctx cancelled before backend call") &&
			!strings.Contains(logOut, "abandoning in-flight goroutines") {
			t.Errorf("expected a ctx-skip or budget-exceeded WARN, got:\n%s", logOut)
		}
	})

	// ─── RULE-WD-PER-SYSCALL-DEADLINE ──────────────────────────────────

	t.Run("wd_per_syscall_deadline_register_read_abandoned", func(t *testing.T) {
		// Register's Read of pwm_enable runs under a per-syscall
		// deadline (DefaultRegisterDeadline) so a hot-plug or hung
		// chip cannot block daemon startup indefinitely (#1042).
		//
		// We exercise the helper directly with a pre-cancelled ctx —
		// the read goroutine cannot return faster than the ctx-done
		// branch, so we get a deterministic abandoned-read error.
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-cancelled
		_, err := readPWMEnableWithDeadline(ctx, "/proc/self/comm")
		if err == nil {
			t.Fatalf("readPWMEnableWithDeadline with pre-cancelled ctx returned nil err")
		}
		if !strings.Contains(err.Error(), "abandoned") {
			t.Errorf("expected 'abandoned' in err, got %q", err.Error())
		}
	})

	t.Run("wd_per_syscall_deadline_write_does_not_leak_past_parent", func(t *testing.T) {
		// writeWithDeadline must return when ctx fires even if the
		// underlying os.WriteFile is STILL blocked inside the kernel.
		// A FIFO with no reader makes os.WriteFile block on open(2)
		// indefinitely, so the `done` channel never fires and the
		// ctx-abandonment branch deterministically wins — no wall-clock
		// bound to race scheduler jitter under -race on shared runners
		// (#1361). The previous version wrote to a nonexistent dir
		// (which fails fast, so the abandonment path was rarely even
		// exercised) and asserted a 250 ms upper bound that flaked.
		fifo := filepath.Join(t.TempDir(), "wd-deadline.fifo")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Skipf("mkfifo unsupported on this platform/fs: %v", err)
		}
		// Hold the read end open (but never read) so the writer's
		// os.WriteFile opens successfully and then BLOCKS once it fills
		// the pipe buffer with a payload larger than the buffer. Blocking
		// on write(2) — rather than on open(2) for a readerless FIFO —
		// keeps the FIFO present for the whole test, so the abandoned
		// goroutine never re-creates a file under the TempDir and cleanup
		// is race-free.
		reader, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatalf("open fifo read end: %v", err)
		}
		t.Cleanup(func() {
			// Draining the buffer lets the abandoned writer finish and
			// close, so it doesn't linger; EOF arrives once it does.
			_, _ = io.Copy(io.Discard, reader)
			_ = reader.Close()
		})
		// 1 MiB dwarfs the 64 KiB default pipe capacity, so the write
		// blocks well before completing.
		payload := make([]byte, 1<<20)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- writeWithDeadline(ctx, fifo, payload, 0o600) }()

		// Fire the deadline. The write goroutine is blocked filling the
		// FIFO buffer, so writeWithDeadline must return via the
		// ctx-abandonment branch — proving it does not leak past the
		// parent's deadline.
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("writeWithDeadline returned nil err while the FIFO write was blocked; abandonment path expected")
			}
			if !strings.Contains(err.Error(), "abandoned") {
				t.Errorf("expected 'abandoned' in err, got %q", err.Error())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("writeWithDeadline leaked past parent: did not return within 5s of ctx cancel while the FIFO write was blocked")
		}
	})

	// ─── RULE-WD-PRIOR-CRASH-FALLBACK ──────────────────────────────────

	t.Run("wd_register_live_enable_1_falls_back_to_bios_auto", func(t *testing.T) {
		// Register-time pwm_enable=1 (manual) is treated as a prior-
		// daemon-crash residual: the previous daemon left the chip in
		// manual mode and exited without restoring. Without the
		// fallback, a fresh daemon's Register would capture
		// origEnable=1 and on subsequent Restore would write 1 back,
		// leaving the fan in manual mode at the daemon's last byte
		// (often 0). The fallback overrides origEnable to
		// SafePreDaemonEnable (2 = BIOS auto), which is the safe
		// unhanded-back. Per RULE-WD-PRIOR-CRASH-FALLBACK / #1039.
		fake := fakehwmon.New(t, &fakehwmon.Options{
			Chips: []fakehwmon.ChipOptions{{
				Name: "nct6798",
				PWMs: []fakehwmon.PWMOptions{
					{Index: 1, PWM: 100, Enable: 1}, // prior-crash residual
				},
			}},
		})
		pwm := filepath.Join(fake.Root, "hwmon0", "pwm1")

		var buf bytes.Buffer
		w := New(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		w.Register(pwm, "hwmon")

		if len(w.entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(w.entries))
		}
		if got := w.entries[0].origEnable; got != SafePreDaemonEnable {
			t.Errorf("origEnable on prior-crash residual = %d, want %d (SafePreDaemonEnable)",
				got, SafePreDaemonEnable)
		}

		// End-to-end: Restore writes the safe fallback, not the
		// captured-1 value.
		w.Restore()
		if got := readTrimmed(t, pwm+"_enable"); got != "2" {
			t.Errorf("pwm_enable after Restore = %q, want %q (safe fallback)", got, "2")
		}
		if !strings.Contains(buf.String(), "prior-crash residual") {
			t.Errorf("expected 'prior-crash residual' WARN, got:\n%s", buf.String())
		}
	})

	t.Run("wd_register_with_store_recovers_last_known_good", func(t *testing.T) {
		// When the LastKnownStore has a persisted pre-daemon value,
		// Register uses it instead of SafePreDaemonEnable on the
		// prior-crash path. This is the "synthesise a prior crash"
		// case: state KV has pwm_enable=2 from the last clean run,
		// the chip reads 1 (manual — crash residual), and Register
		// recovers the persisted 2 rather than guessing.
		fake := fakehwmon.New(t, &fakehwmon.Options{
			Chips: []fakehwmon.ChipOptions{{
				Name: "nct6798",
				PWMs: []fakehwmon.PWMOptions{
					{Index: 1, PWM: 100, Enable: 1}, // prior-crash residual
				},
			}},
		})
		pwm := filepath.Join(fake.Root, "hwmon0", "pwm1")

		// Seed the store using the stable-identity key the watchdog
		// will compute at Register time so the lookup hits the new
		// shape (#1331). Without device-symlink resolution under the
		// fakehwmon dir the identity degrades to LegacyPath; place
		// the value under the corresponding LegacyKey().
		identity := ChannelIdentity{LegacyPath: pwm}
		store := &fakeLastKnownStore{values: map[string]int{identity.Key(): 2}}
		var buf bytes.Buffer
		w := NewWithStore(slog.New(slog.NewTextHandler(&buf, nil)), store)
		w.Register(pwm, "hwmon")

		if got := w.entries[0].origEnable; got != 2 {
			t.Errorf("origEnable with persisted store = %d, want %d", got, 2)
		}
	})

	t.Run("wd_register_legitimate_value_persists_to_store", func(t *testing.T) {
		// When the live read returns a legitimate (non-1) pre-daemon
		// value, Register persists it to the LastKnownStore for
		// future prior-crash recovery. Mirrors RULE-WD-PRIOR-CRASH-
		// FALLBACK's reverse direction.
		fake := fakehwmon.New(t, &fakehwmon.Options{
			Chips: []fakehwmon.ChipOptions{{
				Name: "nct6798",
				PWMs: []fakehwmon.PWMOptions{
					{Index: 1, PWM: 100, Enable: 2},
				},
			}},
		})
		pwm := filepath.Join(fake.Root, "hwmon0", "pwm1")

		store := &fakeLastKnownStore{values: map[string]int{}}
		w := NewWithStore(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), store)
		w.Register(pwm, "hwmon")

		// Identity resolution under fakehwmon may not produce a chip
		// name + bus addr (the fake doesn't ship the `device` symlink
		// every chip would carry on a real host), so the stable key
		// degrades to LegacyKey shape. Either is acceptable for this
		// regression: the contract is that the legitimate read landed
		// in the store, not the specific key shape (#1331).
		identity := ChannelIdentity{LegacyPath: pwm}
		got, ok := store.values[identity.Key()]
		if !ok {
			t.Errorf("store after legitimate Register: no entry under Key()=%q (values=%v)", identity.Key(), store.values)
		} else if got != 2 {
			t.Errorf("store after legitimate Register = %d, want 2", got)
		}
		if PreDaemonEnableKey(pwm) == "" {
			t.Errorf("PreDaemonEnableKey returned empty string")
		}
	})

	// ─── RULE-WD-IPMI-ROUTING ──────────────────────────────────────────

	t.Run("wd_register_ipmi_routes_restore_through_watchdog", func(t *testing.T) {
		// RegisterIPMI binds a vendor-specific restore primitive to
		// a channel ID; the watchdog's Restore loop dispatches to
		// the callback when this entry's turn comes up. The
		// cross-cutting RULE-WD-RESTORE-EXIT contract (every entry
		// touched on every documented exit path) extends to IPMI
		// channels with no change to the IPMI backend's own restore
		// implementation. Per RULE-WD-IPMI-ROUTING / #1043.
		var called atomic.Int32
		var lastChannel atomic.Pointer[string]
		ipmiChannelID := "ipmi:sensor0"

		w := New(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
		w.RegisterIPMI(ipmiChannelID, func() error {
			called.Add(1)
			id := ipmiChannelID
			lastChannel.Store(&id)
			return nil
		})

		w.Restore()

		if got := called.Load(); got != 1 {
			t.Errorf("IPMI restore callback called %d times, want 1", got)
		}
		if got := lastChannel.Load(); got == nil || *got != ipmiChannelID {
			t.Errorf("IPMI restore callback received wrong channel; got %v want %s", got, ipmiChannelID)
		}
	})

	// ─── NVML deadline wrapper ─────────────────────────────────────────
	// The NVML deadline wrapper lives in internal/hal/nvml/backend.go
	// (nvmlResetWithDeadline). Its own bound subtest lives in that
	// package's test file — this rule's binding here documents the
	// cross-package contract that a hung NVML reset cannot stall the
	// watchdog's restore budget. Per RULE-WD-PER-SYSCALL-DEADLINE /
	// #1040.
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

// fakeLastKnownStore is an in-memory LastKnownStore for the
// RULE-WD-PRIOR-CRASH-FALLBACK subtests. Production callers wrap
// state.KVDB; the test fixture deliberately does not import state
// to keep the test hermetic.
//
// values is keyed by the post-#1331 stable-identity key. legacyValues
// holds pre-migration entries keyed by the LegacyPath shape; GetPreDaemonEnable
// returns the stable hit first and falls back to the legacy lookup so
// the migration shim is exercised by the test suite. SetPreDaemonEnable
// always writes the stable key + deletes the legacy entry if present.
type fakeLastKnownStore struct {
	mu            sync.Mutex
	values        map[string]int
	legacyValues  map[string]int
	setCalls      []fakeStoreSetCall
	migratedPaths []string
}

type fakeStoreSetCall struct {
	Key   string
	Value int
}

func (f *fakeLastKnownStore) GetPreDaemonEnable(id ChannelIdentity) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.values[id.Key()]; ok {
		return v, true
	}
	if v, ok := f.legacyValues[id.LegacyKey()]; ok {
		return v, true
	}
	return 0, false
}

func (f *fakeLastKnownStore) SetPreDaemonEnable(id ChannelIdentity, value int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.values == nil {
		f.values = map[string]int{}
	}
	key := id.Key()
	f.values[key] = value
	f.setCalls = append(f.setCalls, fakeStoreSetCall{Key: key, Value: value})
	if f.legacyValues != nil {
		if _, ok := f.legacyValues[id.LegacyKey()]; ok {
			delete(f.legacyValues, id.LegacyKey())
			f.migratedPaths = append(f.migratedPaths, id.LegacyPath)
		}
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
