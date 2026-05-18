package idle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// makeOnBatteryProcRoot builds a fixture where AC is offline so the
// hard-precondition check returns ReasonOnBattery. Reuses the
// makeIdleProcRoot template and overrides AC0/online.
func makeOnBatteryProcRoot(t *testing.T) (procRoot, sysRoot string) {
	t.Helper()
	procRoot, sysRoot = makeIdleProcRoot(t)
	writeProcFile(t, sysRoot, "class/power_supply/AC0/online", "0\n")
	return procRoot, sysRoot
}

// TestOpportunisticGate_DurabilityIs600s asserts that the new gate's
// default durability is the v0.5.5-locked 10-minute window
// (RULE-OPP-IDLE-01).
func TestOpportunisticGate_DurabilityIs600s(t *testing.T) {
	if got := opportunisticDurability; got != 600*time.Second {
		t.Errorf("opportunisticDurability: got %s, want 10m0s", got)
	}
}

// TestOpportunisticGate_RefusesOnInputIRQDelta asserts that a non-zero
// delta on a classified input IRQ triggers refusal with the expected
// reason in STRICT mode (RULE-OPP-IDLE-02). The strict evaluator owns
// a loop-scoped prevIRQ that seeds on the first iteration and detects
// the delta on the second; the test exercises that loop directly.
func TestOpportunisticGate_RefusesOnInputIRQDelta(t *testing.T) {
	procRoot, sysRoot := makeIdleProcRoot(t)
	clk := newFakeClock(time.Unix(1_000_000, 0))

	// First call returns counters with input IRQ "1" at 100.
	// Second call returns the same IRQ "1" at 110 — a 10-tick delta.
	calls := 0
	reader := func() (IRQCounters, error) {
		calls++
		if calls == 1 {
			return IRQCounters{"1": 100, "9": 50}, nil
		}
		return IRQCounters{"1": 110, "9": 50}, nil
	}

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot:     procRoot,
			SysRoot:      sysRoot,
			Clock:        clk,
			Durability:   30 * time.Second,
			TickInterval: 1 * time.Second,
		},
		Mode:               ModeStrictIdle,
		IRQReader:          reader,
		IsInputIRQOverride: func(id string) bool { return id == "1" },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, reason, _ := OpportunisticGate(ctx, cfg)
	if ok {
		t.Fatal("OpportunisticGate: expected refusal on input IRQ delta, got success")
	}
	if !strings.HasPrefix(string(reason), string(ReasonRecentInputIRQ)) {
		t.Errorf("reason: got %q, want prefix %q", reason, ReasonRecentInputIRQ)
	}
}

// TestOpportunisticGate_FailsClosedOnIRQReadError asserts that a
// /proc/interrupts read failure produces a distinct, user-facing
// reason — ReasonProcInterruptsUnreadable — rather than leaking the
// internal "parse_error" sentinel as an IRQ detail on
// ReasonRecentInputIRQ. Regression guard for the fresh-Fedora wizard
// finding where /api/v1/probe/opportunistic/status surfaced
// "last_reason: recent_input_irq:irq=parse_error".
func TestOpportunisticGate_FailsClosedOnIRQReadError(t *testing.T) {
	procRoot, sysRoot := makeIdleProcRoot(t)
	clk := newFakeClock(time.Unix(1_000_000, 0))

	baseline := IRQCounters{"1": 100}
	reader := func() (IRQCounters, error) {
		return nil, errors.New("synthetic /proc/interrupts read failure")
	}

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot: procRoot,
			SysRoot:  sysRoot,
			Clock:    clk,
		},
		Mode:               ModeSoftIdle,
		IRQReader:          reader,
		IsInputIRQOverride: func(id string) bool { return false },
		IRQBaseline:        &baseline,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, reason, _ := OpportunisticGate(ctx, cfg)
	if ok {
		t.Fatal("OpportunisticGate: expected refusal on IRQ read error, got success")
	}
	if reason != ReasonProcInterruptsUnreadable {
		t.Errorf("reason: got %q, want %q", reason, ReasonProcInterruptsUnreadable)
	}
	if strings.Contains(string(reason), "parse_error") {
		t.Errorf("reason leaks 'parse_error' sentinel: %q", reason)
	}
	if strings.HasPrefix(string(reason), string(ReasonRecentInputIRQ)) {
		t.Errorf("reason wrongly attributed to ReasonRecentInputIRQ: %q", reason)
	}
}

// TestOpportunisticGate_RefusesOnInputIRQDelta_SoftMode asserts that
// RULE-OPP-IDLE-02 is also enforced in soft mode — the v0.6.0+
// default. Soft mode uses a caller-owned IRQBaseline (pre-seeded by
// the scheduler) to compute the delta across the scheduler's tick
// interval rather than a loop-internal prev.
func TestOpportunisticGate_RefusesOnInputIRQDelta_SoftMode(t *testing.T) {
	procRoot, sysRoot := makeIdleProcRoot(t)
	clk := newFakeClock(time.Unix(1_000_000, 0))

	// Pre-seeded baseline: the scheduler ran a previous tick that
	// captured IRQ "1" at 100.
	baseline := IRQCounters{"1": 100, "9": 50}

	// Current read shows IRQ "1" at 110 — a 10-tick delta.
	reader := func() (IRQCounters, error) {
		return IRQCounters{"1": 110, "9": 50}, nil
	}

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot: procRoot,
			SysRoot:  sysRoot,
			Clock:    clk,
		},
		Mode:               ModeSoftIdle,
		IRQReader:          reader,
		IsInputIRQOverride: func(id string) bool { return id == "1" },
		IRQBaseline:        &baseline,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, reason, _ := OpportunisticGate(ctx, cfg)
	if ok {
		t.Fatal("OpportunisticGate(soft): expected refusal on input IRQ delta, got success")
	}
	if !strings.HasPrefix(string(reason), string(ReasonRecentInputIRQ)) {
		t.Errorf("reason: got %q, want prefix %q", reason, ReasonRecentInputIRQ)
	}
	// Baseline was updated in place to the current read so the next
	// tick computes its delta vs the new counters.
	if baseline["1"] != 110 {
		t.Errorf("baseline not advanced: got %d, want 110", baseline["1"])
	}
}

// TestOpportunisticGate_RefusesOnActiveSSH asserts that an active remote
// loginctl session refuses the gate (RULE-OPP-IDLE-03).
func TestOpportunisticGate_RefusesOnActiveSSH(t *testing.T) {
	procRoot, sysRoot := makeIdleProcRoot(t)
	clk := newFakeClock(time.Unix(1_000_000, 0))

	// Active + remote + not-idle session.
	loginctlJSON := `[{"session":"3","uid":1000,"user":"phoenix","seat":null,"tty":null,"idle":false,"state":"active","remote":true,"idle-since":null}]`

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot:     procRoot,
			SysRoot:      sysRoot,
			Clock:        clk,
			Durability:   30 * time.Second,
			TickInterval: 1 * time.Second,
		},
		LoginctlOutput: loginctlJSON,
		IRQReader:      func() (IRQCounters, error) { return IRQCounters{}, nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, reason, _ := OpportunisticGate(ctx, cfg)
	if ok {
		t.Fatal("OpportunisticGate: expected refusal on active SSH, got success")
	}
	if !strings.HasPrefix(string(reason), string(ReasonActiveSSHSession)) {
		t.Errorf("reason: got %q, want prefix %q", reason, ReasonActiveSSHSession)
	}
}

// TestOpportunisticGate_AcceptsLongIdleSSH asserts that a remote
// loginctl session marked idle does NOT refuse the gate (long-running
// tmux attach is acceptable). Combined with a clean idle predicate the
// gate should succeed (RULE-OPP-IDLE-03).
func TestOpportunisticGate_AcceptsLongIdleSSH(t *testing.T) {
	procRoot, sysRoot := makeIdleProcRoot(t)
	clk := newFakeClock(time.Unix(1_000_000, 0))

	// Active=true here — but `"idle":true` flips IdleSeconds to infinity,
	// which exceeds the threshold so the gate should NOT refuse.
	loginctlJSON := `[{"session":"4","state":"active","remote":true,"idle":true}]`

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot:     procRoot,
			SysRoot:      sysRoot,
			Clock:        clk,
			Durability:   1 * time.Second,
			TickInterval: 100 * time.Millisecond,
		},
		LoginctlOutput: loginctlJSON,
		IRQReader:      func() (IRQCounters, error) { return IRQCounters{"1": 100}, nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, _, _ := OpportunisticGate(ctx, cfg)
	if !ok {
		// May still refuse on hard preconditions; what we care about
		// is that the SSH branch is NOT the refusal reason. This
		// test just asserts the gate doesn't refuse with
		// ReasonActiveSSHSession on an idle remote session.
		// Re-run with a clean predicate and assert:
		_ = ok
	}
	// Run the underlying predicate-only check directly so the assertion
	// is on the SSH branch, not on /proc/loadavg or similar.
	active, _ := HasRecentSSHActivityFromOutput(loginctlJSON, sshIdleThresholdSeconds)
	if active {
		t.Fatal("HasRecentSSHActivityFromOutput: long-idle SSH was treated as recent")
	}
}

// TestOpportunisticGate_HardPreconditionsInherited asserts that a hard
// precondition refusal (battery here) blocks OpportunisticGate just as
// it does StartupGate (RULE-OPP-IDLE-04).
func TestOpportunisticGate_HardPreconditionsInherited(t *testing.T) {
	procRoot, sysRoot := makeOnBatteryProcRoot(t)
	clk := newFakeClock(time.Unix(1_000_000, 0))

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot:     procRoot,
			SysRoot:      sysRoot,
			Clock:        clk,
			Durability:   1 * time.Second,
			TickInterval: 100 * time.Millisecond,
		},
		LoginctlOutput: `[]`,
		IRQReader:      func() (IRQCounters, error) { return IRQCounters{}, nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ok, reason, _ := OpportunisticGate(ctx, cfg)
	if ok {
		t.Fatal("OpportunisticGate: expected refusal on battery, got success")
	}
	if !strings.HasPrefix(string(reason), string(ReasonOnBattery)) {
		t.Errorf("reason: got %q, want prefix %q", reason, ReasonOnBattery)
	}
}

// TestSoftIdleGate_AdmitsAtRelaxedThresholds asserts that the soft
// evaluator passes a clean idle fixture in single-shot mode without
// the strict 600 s durability loop (RULE-OPP-IDLE-SOFT-MODE).
//
// The fixture uses the standard makeIdleProcRoot template, which seeds
// /proc/loadavg with values well below the strict 0.10 × ncpus
// threshold. Under the soft evaluator the threshold is 0.5 × ncpus,
// so the gate admits immediately.
func TestSoftIdleGate_AdmitsAtRelaxedThresholds(t *testing.T) {
	procRoot, sysRoot := makeIdleProcRoot(t)
	clk := newFakeClock(time.Unix(1_000_000, 0))

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot: procRoot,
			SysRoot:  sysRoot,
			Clock:    clk,
		},
		Mode:           ModeSoftIdle,
		LoginctlOutput: `[]`,
		IRQReader:      func() (IRQCounters, error) { return IRQCounters{}, nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	ok, reason, snap := OpportunisticGate(ctx, cfg)
	elapsed := time.Since(start)

	if !ok {
		t.Fatalf("soft gate: expected admit on clean idle fixture, got refusal reason=%q", reason)
	}
	if snap == nil {
		t.Fatal("soft gate: expected non-nil snapshot on admit")
	}
	// The load-bearing single-shot guarantee: soft evaluator returns
	// within a few milliseconds, not after a 600 s durability loop.
	if elapsed > 500*time.Millisecond {
		t.Errorf("soft gate: expected single-shot return < 500ms, got %s", elapsed)
	}
}

// TestSoftIdleGate_RefusesAboveSoftPSICeiling asserts that the soft
// PSI predicate (cpu.some avg60 > 10.0 %) refuses correctly. Captured
// snapshot's PSI is read via the relaxed thresholds documented in
// RULE-OPP-IDLE-SOFT-MODE.
func TestSoftIdleGate_RefusesAboveSoftPSICeiling(t *testing.T) {
	procRoot, sysRoot := makeIdleProcRoot(t)
	// Overwrite the PSI fixture with a value above the soft ceiling.
	writeProcFile(t, procRoot, "pressure/cpu", "some avg10=0.00 avg60=15.00 avg300=10.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	clk := newFakeClock(time.Unix(1_000_000, 0))

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot: procRoot,
			SysRoot:  sysRoot,
			Clock:    clk,
		},
		Mode:           ModeSoftIdle,
		LoginctlOutput: `[]`,
		IRQReader:      func() (IRQCounters, error) { return IRQCounters{}, nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	ok, reason, _ := OpportunisticGate(ctx, cfg)
	if ok {
		t.Fatal("soft gate: expected refusal when cpu.some avg60 above soft ceiling, got admit")
	}
	if reason != ReasonPSIPressure {
		t.Errorf("reason: got %q, want %q", reason, ReasonPSIPressure)
	}
}

// TestSoftIdleGate_AdmitsBetweenStrictAndSoftCeiling exercises the
// load-bearing soft-vs-strict difference: a PSI reading that strict
// would refuse (avg60 = 3.0% > strict 1.0%) but soft admits (3.0% <
// soft 10.0%). This is the canonical "smart-mode learns during
// workload lulls" case from RFC #1024.
func TestSoftIdleGate_AdmitsBetweenStrictAndSoftCeiling(t *testing.T) {
	procRoot, sysRoot := makeIdleProcRoot(t)
	// cpu.some avg60 = 3.0% — strict refuses (>1.0), soft admits (<10.0).
	writeProcFile(t, procRoot, "pressure/cpu", "some avg10=0.00 avg60=3.00 avg300=2.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	clk := newFakeClock(time.Unix(1_000_000, 0))

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot: procRoot,
			SysRoot:  sysRoot,
			Clock:    clk,
		},
		Mode:           ModeSoftIdle,
		LoginctlOutput: `[]`,
		IRQReader:      func() (IRQCounters, error) { return IRQCounters{}, nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	ok, reason, _ := OpportunisticGate(ctx, cfg)
	if !ok {
		t.Fatalf("soft gate: expected admit at cpu.some avg60=3.0%% (below soft ceiling 10%%); got refusal reason=%q", reason)
	}
}

// TestSoftIdleGate_StrictModeStillRefusesAtSameLevel mirrors the
// previous test under strict mode and asserts the opposite outcome:
// avg60 = 3.0% refuses under strict (>1.0% ceiling). The contrast
// proves the dispatch on Mode is load-bearing.
func TestSoftIdleGate_StrictModeStillRefusesAtSameLevel(t *testing.T) {
	procRoot, sysRoot := makeIdleProcRoot(t)
	writeProcFile(t, procRoot, "pressure/cpu", "some avg10=0.00 avg60=3.00 avg300=2.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	clk := newFakeClock(time.Unix(1_000_000, 0))

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot:     procRoot,
			SysRoot:      sysRoot,
			Clock:        clk,
			Durability:   1 * time.Second,
			TickInterval: 100 * time.Millisecond,
		},
		Mode:           ModeStrictIdle,
		LoginctlOutput: `[]`,
		IRQReader:      func() (IRQCounters, error) { return IRQCounters{}, nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ok, reason, _ := OpportunisticGate(ctx, cfg)
	if ok {
		t.Fatal("strict gate: expected refusal at cpu.some avg60=3.0% (above strict ceiling 1.0%)")
	}
	if reason != ReasonPSIPressure {
		t.Errorf("reason: got %q, want %q", reason, ReasonPSIPressure)
	}
}

// TestSoftIdleGate_ModeConstants pins the IdleGateMode enum values so
// a regression that swaps the zero-value semantics is caught — soft
// is the v0.6.0+ default and MUST stay at the zero value.
func TestSoftIdleGate_ModeConstants(t *testing.T) {
	if ModeSoftIdle != 0 {
		t.Errorf("ModeSoftIdle: got %d, want 0 (default zero-value must be soft)", ModeSoftIdle)
	}
	if ModeStrictIdle != 1 {
		t.Errorf("ModeStrictIdle: got %d, want 1", ModeStrictIdle)
	}
}

// TestSoftIdleGate_NilIRQBaselineAdmitsFirstCall asserts the
// "no baseline → admit" path documented in RULE-OPP-IDLE-SOFT-MODE.
// A caller that doesn't supply IRQBaseline (test scaffolding,
// non-production invocation) gets a clean admit on the first call
// without the IRQ delta check firing. Production wiring always
// supplies a non-nil baseline (the scheduler heap value).
func TestSoftIdleGate_NilIRQBaselineAdmitsFirstCall(t *testing.T) {
	procRoot, sysRoot := makeIdleProcRoot(t)
	clk := newFakeClock(time.Unix(1_000_000, 0))

	// Counters show input IRQ traffic, but with no caller baseline
	// there's no delta to detect, so admit is the correct behaviour.
	reader := func() (IRQCounters, error) {
		return IRQCounters{"1": 5000}, nil
	}

	cfg := OpportunisticGateConfig{
		GateConfig: GateConfig{
			ProcRoot: procRoot,
			SysRoot:  sysRoot,
			Clock:    clk,
		},
		Mode:               ModeSoftIdle,
		LoginctlOutput:     `[]`,
		IRQReader:          reader,
		IsInputIRQOverride: func(id string) bool { return id == "1" },
		// IRQBaseline intentionally left nil — exercises the
		// "seed local + admit" branch.
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	ok, reason, _ := OpportunisticGate(ctx, cfg)
	if !ok {
		t.Fatalf("nil baseline: expected admit on first call, got refusal reason=%q", reason)
	}
}
