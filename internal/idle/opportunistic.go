package idle

import (
	"context"
	"time"
)

// opportunisticDurability is the durability window for OpportunisticGate.
// 600 s (10 minutes) is 2× the StartupGate window; rationale lives in
// spec-v0_5_5-opportunistic-probing.md §2.1 and RULE-OPP-IDLE-01.
const opportunisticDurability = 600 * time.Second

// sshIdleThresholdSeconds is the loginctl IdleSinceHint threshold above
// which a Remote=yes Active=yes session is treated as long-idle (e.g.,
// tmux attach left running) and does NOT block opportunistic probing.
const sshIdleThresholdSeconds int64 = 60

// Opportunistic-specific refusal reasons. The base StartupGate reasons
// (battery, container, scrub, etc.) are reused unchanged via
// CheckHardPreconditions; these add the v0.5.5 input-IRQ + SSH layer
// and the install-window / config-toggle refusals.
const (
	ReasonRecentInputIRQ          Reason = "recent_input_irq"
	ReasonActiveSSHSession        Reason = "active_ssh_session"
	ReasonOpportunisticDisabled   Reason = "opportunistic_disabled"
	ReasonOpportunisticBootWindow Reason = "opportunistic_boot_window"
)

// OpportunisticGateConfig extends GateConfig with the v0.5.5 input-IRQ
// and SSH session sources. Fields are injectable for tests.
type OpportunisticGateConfig struct {
	GateConfig
	// LoginctlOutput, when non-empty, is parsed in place of running
	// loginctl. Empty string means run the real binary.
	LoginctlOutput string
	// IRQReader, when non-nil, is called instead of reading
	// /proc/interrupts directly. Tests use this to simulate input
	// activity without a real /proc fixture.
	IRQReader func() (IRQCounters, error)
	// IsInputIRQOverride, when non-nil, classifies IRQs without
	// touching /sys. Tests only.
	IsInputIRQOverride func(id string) bool
}

// OpportunisticGate blocks until the system has been idle continuously
// for opportunisticDurability (default 600 s) AND no input IRQ has shown
// activity since the previous tick AND no remote loginctl session has
// been active within sshIdleThresholdSeconds. Hard preconditions
// inherited from StartupGate are checked unchanged (RULE-OPP-IDLE-04).
//
// Returns (true, ReasonOK, snapshot) on success; (false, reason, nil)
// on refusal or context cancel. Unlike StartupGate, there is no
// backoff scheduler — opportunistic probing is fire-and-forget at the
// scheduler tick cadence (default 60 s); refusing is cheap and the
// caller is expected to retry on the next tick.
func OpportunisticGate(ctx context.Context, cfg OpportunisticGateConfig) (bool, Reason, *Snapshot) {
	gateCfg := cfg.GateConfig
	if gateCfg.Durability == 0 {
		gateCfg.Durability = opportunisticDurability
	}

	clk := gateCfg.clock()
	dur := newDurabilityState(gateCfg.Durability, clk)
	tick := gateCfg.tickInterval()
	deps := snapshotDeps{procRoot: gateCfg.ProcRoot, sysRoot: gateCfg.SysRoot, clock: clk}

	var prevIRQ IRQCounters
	for {
		if ctx.Err() != nil {
			return false, ReasonOK, nil
		}

		snap := Capture(deps)
		idle, reason := evalPredicate(snap, gateCfg)

		// Layer in OpportunisticGate-specific signals on top of the
		// StartupGate predicate. Only checked when the base predicate
		// passes, since failing the base predicate is the cheaper-to-
		// observe refusal reason.
		if idle {
			if active, irq := evalInputIRQActivity(cfg, &prevIRQ); active {
				idle = false
				reason = ReasonRecentInputIRQ.WithDetail("irq=" + irq)
			}
		}
		if idle {
			if active, sid := evalSSHActivity(cfg); active {
				idle = false
				reason = ReasonActiveSSHSession.WithDetail("session=" + sid)
			}
		}

		if dur.Record(idle) {
			return true, ReasonOK, snap
		}

		if !idle {
			dur.Reset()
			return false, reason, nil
		}
		clk.Sleep(tick)
	}
}

// evalInputIRQActivity reads the current IRQ counters and compares
// against the previous snapshot. On the first call (prevIRQ empty)
// captures the baseline and reports no activity — the gate's
// durability requirement gives the caller plenty of subsequent ticks
// to detect a delta.
func evalInputIRQActivity(cfg OpportunisticGateConfig, prev *IRQCounters) (bool, string) {
	read := cfg.IRQReader
	if read == nil {
		read = func() (IRQCounters, error) {
			return ReadIRQCounters(cfg.ProcRoot)
		}
	}
	cur, err := read()
	if err != nil {
		// Conservative: parse failure means we can't prove the gate
		// is safe. RULE-OPP-IDLE-02 requires refusal in this case
		// with a parse_error detail.
		return true, "parse_error"
	}
	if len(*prev) == 0 {
		*prev = cur
		return false, ""
	}
	classify := cfg.IsInputIRQOverride
	if classify == nil {
		classify = func(id string) bool {
			return IsInputIRQ(cfg.SysRoot, cfg.ProcRoot, id)
		}
	}
	for id, n := range cur {
		p, ok := (*prev)[id]
		if !ok {
			continue
		}
		if n <= p {
			continue
		}
		if classify(id) {
			*prev = cur
			return true, id
		}
	}
	*prev = cur
	return false, ""
}

// evalSSHActivity returns true when loginctl reports an active remote
// session that has been idle for less than sshIdleThresholdSeconds.
func evalSSHActivity(cfg OpportunisticGateConfig) (bool, string) {
	if cfg.LoginctlOutput != "" {
		return HasRecentSSHActivityFromOutput(cfg.LoginctlOutput, sshIdleThresholdSeconds)
	}
	return HasRecentSSHActivity(sshIdleThresholdSeconds)
}
