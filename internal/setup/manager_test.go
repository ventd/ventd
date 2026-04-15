package setup

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	hwmonpkg "github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/watchdog"
)

// newManager wires a Manager with the minimum dependencies needed to drive
// the wizard from a test. The calibrate manager points at a t.TempDir()
// path so each subtest gets an isolated checkpoint store; tests never share
// on-disk state.
func newManager(t *testing.T) *Manager {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wd := watchdog.New(logger)
	cal := calibrate.New(filepath.Join(t.TempDir(), "cal.json"), logger, wd)
	return New(cal, logger)
}

// waitDone polls Progress() until Done is true or the deadline elapses.
// It returns the final Progress snapshot. The poll cadence is 20ms so a
// fast wizard run (typical when the sandbox has no real fans) completes
// in ≤2 ticks; the timeout is the safety net for a stuck goroutine.
func waitDone(t *testing.T, m *Manager, timeout time.Duration) Progress {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p := m.Progress()
		if p.Done {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("setup wizard did not reach Done within %v", timeout)
	return Progress{}
}

// TestManager_StartTransitionsToRunningThenDone runs the wizard end-to-end
// in the sandbox. With no /sys/class/hwmon devices the wizard reaches the
// "no fan controllers were found" branch — that's the documented failure
// shape and is what every fresh-VM CI environment hits before hardware
// is wired up. The transition Running → Done with a non-empty Error is
// the user-visible contract; this test pins it.
func TestManager_StartTransitionsToRunningThenDone(t *testing.T) {
	m := newManager(t)

	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Immediately after Start, Running must be true.
	p := m.Progress()
	if !p.Running {
		t.Errorf("Progress just after Start: Running=false, want true")
	}

	final := waitDone(t, m, 5*time.Second)
	if !final.Done {
		t.Fatal("Done = false after waitDone returned")
	}
	if final.Running {
		t.Errorf("Running stayed true after Done")
	}
	// The sandbox path: no fans → an Error. We don't pin the exact text
	// (that lives in setup.go and may evolve), but it must be non-empty
	// and mention "fan" so the operator can tell what failed.
	if final.Error == "" || !strings.Contains(strings.ToLower(final.Error), "fan") {
		t.Errorf("Error = %q, want non-empty mentioning 'fan'", final.Error)
	}
}

// TestManager_StartTwiceWhileRunningRefuses pins the "no overlapping runs"
// guarantee. The wizard owns hwmon writes; a second concurrent Start would
// race on PWM channels.
func TestManager_StartTwiceWhileRunningRefuses(t *testing.T) {
	m := newManager(t)
	if err := m.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer waitDone(t, m, 5*time.Second)

	// Try to start again while still running.
	err := m.Start()
	// Race window: if the sandbox already finished run() between calls,
	// Start returns the "already completed" error instead. Both shapes are
	// valid refusals — the contract is "non-nil error, no second goroutine".
	if err == nil {
		t.Fatalf("second Start: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "already") {
		t.Errorf("error %q does not mention 'already'", msg)
	}
}

// TestManager_StartAfterDoneRefuses pins the post-completion contract:
// once a wizard run has finished, the operator must restart the daemon
// to attempt setup again. Re-using a completed manager would pick up
// stale state.
func TestManager_StartAfterDoneRefuses(t *testing.T) {
	m := newManager(t)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, m, 5*time.Second)

	err := m.Start()
	if err == nil {
		t.Fatal("Start after Done: want error, got nil")
	}
	if !strings.Contains(err.Error(), "already completed") {
		t.Errorf("error %q does not mention 'already completed'", err.Error())
	}
	if !strings.Contains(err.Error(), "restart") {
		t.Errorf("error %q does not mention 'restart' (operator hint)", err.Error())
	}
}

// TestManager_AbortBeforeStartIsNoop pins idempotence: the web handler
// can fire Abort unconditionally without first checking whether a run
// is in flight.
func TestManager_AbortBeforeStartIsNoop(t *testing.T) {
	m := newManager(t)
	m.Abort() // must not panic
	m.Abort() // must remain idempotent
	p := m.Progress()
	if p.Running || p.Done {
		t.Errorf("Abort changed state: Running=%v Done=%v", p.Running, p.Done)
	}
}

// TestManager_AbortAfterDoneIsNoop covers the "fire after completion" path.
// Because run() releases the cancel func on exit and the deferred Abort
// caller doesn't know that, Abort must tolerate a stale call.
func TestManager_AbortAfterDoneIsNoop(t *testing.T) {
	m := newManager(t)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, m, 5*time.Second)
	m.Abort() // post-Done — must not panic and must not mutate state
	p := m.Progress()
	if !p.Done {
		t.Errorf("Abort after Done flipped Done back to false")
	}
}

// TestManager_MarkAppliedAndProgressNeeded pins the "wizard finished, config
// committed" transition. Before MarkApplied, Progress.Needed is true (the
// UI keeps the wizard surface visible); after, Needed flips to false.
func TestManager_MarkAppliedAndProgressNeeded(t *testing.T) {
	m := newManager(t)

	// Before any run, Needed reflects the !applied state.
	if got := m.Progress().Needed; !got {
		t.Errorf("Progress.Needed before run = false, want true")
	}

	m.MarkApplied()
	if got := m.Progress().Needed; got {
		t.Errorf("Progress.Needed after MarkApplied = true, want false")
	}

	// ProgressNeeded combines Needed with the live config — even when
	// applied, an empty live config (no Controls) flips Needed back on.
	emptyCfg := &config.Config{}
	// applied=true, but live config empty → contract is: applied wins,
	// because the wizard produced the config that was just applied.
	p := m.ProgressNeeded(emptyCfg)
	if p.Needed {
		t.Errorf("ProgressNeeded after MarkApplied with empty cfg: Needed=true, want false (applied wins)")
	}
}

// TestManager_ProgressNeededReflectsLiveConfig covers the "no run yet"
// branch of ProgressNeeded: when the manager hasn't applied a config and
// the live config has zero Controls, Needed must be true; with controls,
// false.
func TestManager_ProgressNeededReflectsLiveConfig(t *testing.T) {
	m := newManager(t)
	emptyCfg := &config.Config{}
	if !m.ProgressNeeded(emptyCfg).Needed {
		t.Errorf("ProgressNeeded(empty) = false, want true")
	}
	withCtl := &config.Config{Controls: []config.Control{{Fan: "f", Curve: "c"}}}
	if m.ProgressNeeded(withCtl).Needed {
		t.Errorf("ProgressNeeded(non-empty) = true, want false")
	}
}

// TestManager_GeneratedConfigBeforeRunIsNil pins the "ask early" case:
// callers that poll GeneratedConfig get nil until run() finishes
// successfully, and nil also when run() failed (the sandbox case).
func TestManager_GeneratedConfigBeforeRunIsNil(t *testing.T) {
	m := newManager(t)
	if cfg := m.GeneratedConfig(); cfg != nil {
		t.Errorf("GeneratedConfig before Start = %+v, want nil", cfg)
	}

	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, m, 5*time.Second)
	// Sandbox: run() returned an error before building a config.
	if cfg := m.GeneratedConfig(); cfg != nil {
		t.Errorf("GeneratedConfig after failed run = %+v, want nil", cfg)
	}
}

// TestManager_RunBlockingReturnsErrorMessage covers the CLI --setup path.
// RunBlocking is the synchronous counterpart of Start used by the binary
// when the wizard runs at the terminal. Errors must be propagated as a
// regular Go error rather than swallowed.
func TestManager_RunBlockingReturnsErrorMessage(t *testing.T) {
	m := newManager(t)
	err := m.RunBlocking()
	if err == nil {
		t.Fatal("RunBlocking: want error in sandbox, got nil")
	}
	// The wizard surfaced the no-fans error as a regular error value;
	// the message should match Progress.Error so CLI and HTTP report
	// identical text to the operator.
	p := m.Progress()
	if err.Error() != p.Error {
		t.Errorf("RunBlocking err = %q, Progress.Error = %q, want equal", err.Error(), p.Error)
	}
}

// TestManager_RunBlockingRefusesSecondCall pins the "complete-once" rule
// against the synchronous entry point as well as Start.
func TestManager_RunBlockingRefusesSecondCall(t *testing.T) {
	m := newManager(t)
	_ = m.RunBlocking() // sandbox failure is fine; we just need it to finish
	err := m.RunBlocking()
	if err == nil {
		t.Fatal("second RunBlocking: want error, got nil")
	}
}

// TestManager_PhaseUpdatesAreVisibleViaProgress runs the wizard and asserts
// that by Done time, Phase is non-empty (run() updates it through several
// labels). The sandbox path always advances at least to "scanning_fans".
func TestManager_PhaseUpdatesAreVisibleViaProgress(t *testing.T) {
	m := newManager(t)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitDone(t, m, 5*time.Second)
	if final.Phase == "" {
		t.Errorf("Phase at Done is empty; want one of detecting/scanning_fans/...")
	}
	// PhaseMsg must be human-readable text (non-empty).
	if final.PhaseMsg == "" {
		t.Errorf("PhaseMsg at Done is empty; setPhase should have set it")
	}
}

// TestManager_FansSliceIsCopiedFromProgress pins the snapshot semantics
// that Progress() returns. Mutating the returned Fans slice must not
// race with subsequent wizard updates — the manager owns the canonical
// state and Progress hands out copies.
func TestManager_FansSliceIsCopiedFromProgress(t *testing.T) {
	m := newManager(t)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, m, 5*time.Second)

	p1 := m.Progress()
	// Mutate the returned slice — a real consumer should not, but this is
	// the safety check.
	if p1.Fans != nil {
		p1.Fans = append(p1.Fans, FanState{Name: "spurious"})
	}
	p2 := m.Progress()
	for _, f := range p2.Fans {
		if f.Name == "spurious" {
			t.Errorf("internal Fans slice was mutated by external append: %+v", p2.Fans)
		}
	}
}

// TestManager_InstallLogIsCopied is the same snapshot guarantee for
// InstallLog. Mutating the returned slice must not affect future polls.
func TestManager_InstallLogIsCopied(t *testing.T) {
	m := newManager(t)
	// Push something through the private appendInstallLog so we have a
	// non-nil slice to test against.
	m.appendInstallLog("hello")
	m.appendInstallLog("world")

	p1 := m.Progress()
	if got := len(p1.InstallLog); got != 2 {
		t.Fatalf("InstallLog len = %d, want 2", got)
	}
	p1.InstallLog[0] = "MUTATED"

	p2 := m.Progress()
	if p2.InstallLog[0] != "hello" {
		t.Errorf("InstallLog[0] mutated externally: %q", p2.InstallLog[0])
	}
}

// TestManager_EmitBuildFailedDiag covers the unhappy-path diag emitter
// that fires when InstallDriver returns a non-reboot error. Used in the
// live wizard but never triggered in sandbox; this test exercises the
// mapping directly.
func TestManager_EmitBuildFailedDiag(t *testing.T) {
	store := hwdiag.NewStore()
	m := newManager(t)
	m.SetDiagnosticStore(store)

	nd := hwmonpkg.DriverNeed{Key: "nct6687d", ChipName: "NCT6687D", Module: "nct6687"}
	m.emitBuildFailedDiag(nd, errors.New("compiler error: undefined symbol"))

	snap := store.Snapshot(hwdiag.Filter{})
	if len(snap.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(snap.Entries))
	}
	e := snap.Entries[0]
	if e.ID != hwdiag.IDOOTBuildFailed {
		t.Errorf("ID = %q, want %q", e.ID, hwdiag.IDOOTBuildFailed)
	}
	if e.Severity != hwdiag.SeverityError {
		t.Errorf("Severity = %q, want %q", e.Severity, hwdiag.SeverityError)
	}
	if !strings.Contains(e.Detail, "compiler error") {
		t.Errorf("Detail = %q, want it to include the underlying error text", e.Detail)
	}
	if len(e.Affected) != 1 || e.Affected[0] != "nct6687" {
		t.Errorf("Affected = %v, want [nct6687]", e.Affected)
	}
	// Build-failed has no remediation: the diag is informational.
	if e.Remediation != nil {
		t.Errorf("Remediation = %+v, want nil (no auto-fix for unanticipated build failure)", e.Remediation)
	}
}

// TestManager_EmitBuildFailedDiagNoStoreIsNoOp pins the nil-store contract
// that the CLI --setup path depends on (CLI never sets a store).
func TestManager_EmitBuildFailedDiagNoStoreIsNoOp(t *testing.T) {
	m := newManager(t)
	// intentionally no SetDiagnosticStore
	m.emitBuildFailedDiag(
		hwmonpkg.DriverNeed{Key: "k", ChipName: "C", Module: "m"},
		errors.New("boom"),
	)
}

// TestManager_EmitDMICandidatesWrapper covers the public wrapper around the
// fixture-tested emitDMICandidatesFor. The wrapper calls hwmonpkg.ReadDMI("")
// against the live root; in sandbox that returns an empty DMIInfo, which
// emits the no_match entry.
func TestManager_EmitDMICandidatesWrapper(t *testing.T) {
	store := hwdiag.NewStore()
	m := newManager(t)
	m.SetDiagnosticStore(store)

	m.emitDMICandidates() // live ReadDMI("")
	snap := store.Snapshot(hwdiag.Filter{Component: hwdiag.ComponentDMI})
	if len(snap.Entries) == 0 {
		t.Fatal("DMI emit produced no entries; want at least the no_match fallback")
	}
	// Exact ID depends on the host's DMI; in sandbox it's no_match.
	// Only invariant we pin here: at least one DMI entry was set.
}
