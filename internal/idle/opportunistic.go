package idle

import (
	"context"
	"time"

	"github.com/ventd/ventd/internal/sysclass"
)

// opportunisticDurability is the durability window for OpportunisticGate
// when running in ModeStrictIdle. 600 s (10 minutes) is 2× the
// StartupGate window; rationale lives in
// spec-v0_5_5-opportunistic-probing.md §2.1 and RULE-OPP-IDLE-01.
// ModeSoftIdle (v0.6.0+ default) does NOT use a durability loop.
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
	ReasonRecentInputIRQ           Reason = "recent_input_irq"
	ReasonActiveSSHSession         Reason = "active_ssh_session"
	ReasonOpportunisticDisabled    Reason = "opportunistic_disabled"
	ReasonOpportunisticBootWindow  Reason = "opportunistic_boot_window"
	ReasonProcInterruptsUnreadable Reason = "proc_interrupts_unreadable"
)

// IdleGateMode selects between the v0.6.0+ soft-default OpportunisticGate
// evaluator and the legacy v0.5.x strict evaluator. Strict was the only
// mode through v0.5.x (600 s durability + tight PSI thresholds tuned for
// calibration-grade quiescence). v0.6.0 defaults to soft so smart-mode
// can learn under realistic workload (RULE-OPP-IDLE-SOFT-MODE); the
// strict path is preserved behind `--strict-idle-gate` for operators on
// hosts where the soft thresholds prove too permissive.
type IdleGateMode uint8

const (
	// ModeSoftIdle is the v0.6.0+ default: single-shot evaluation,
	// soft PSI thresholds, soft loadavg fallback. Cross-call IRQ
	// delta state is owned by the caller via IRQBaseline.
	ModeSoftIdle IdleGateMode = 0
	// ModeStrictIdle is the legacy v0.5.x evaluator: 600 s
	// durability loop + tight PSI thresholds. Operator escape hatch
	// via `--strict-idle-gate`.
	ModeStrictIdle IdleGateMode = 1
)

// OpportunisticGateConfig extends GateConfig with the v0.5.5 input-IRQ
// and SSH session sources. Fields are injectable for tests.
type OpportunisticGateConfig struct {
	GateConfig
	// Mode selects soft (default, v0.6.0+) vs strict (legacy v0.5.x)
	// evaluation. Zero value = ModeSoftIdle.
	Mode IdleGateMode
	// Class drives the soft-mode workload thresholds via
	// LookupSoftIdleThresholds. Zero value (ClassUnknown) falls
	// through to ClassMidDesktop — the consumer-grade default that
	// matches the envelope-thresholds fallback. Production wires the
	// detected class from sysclass.Detection; tests that don't care
	// can leave this zero and inherit MidDesktop's relaxed numbers.
	Class sysclass.SystemClass
	// SoftThresholds, when non-nil, replaces the per-class lookup
	// entirely — used by tests to assert specific ceilings without
	// pinning to a real class. Production leaves this nil.
	SoftThresholds *SoftIdleThresholds
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
	// IRQBaseline is the caller-owned IRQ-counter snapshot used by
	// the soft evaluator for cross-call delta detection. The
	// scheduler initialises one zero-valued IRQCounters per
	// scheduler-lifetime and passes the same pointer on every tick,
	// so "any classified input IRQ has activity since the previous
	// gate evaluation" reads naturally across the ~60 s scheduler
	// tick interval. The strict evaluator manages its own
	// loop-scoped prev and ignores this field. Nil means "no
	// baseline" — soft mode seeds it from the current read and
	// admits the first call without enforcing the IRQ check.
	IRQBaseline *IRQCounters
	// ProcessBaseline is the caller-owned snapshot of blocked
	// processes that were already running at the previous gate
	// evaluation. The soft evaluator uses it to distinguish NEW
	// blocked work (e.g., a one-off rsync the operator just started
	// — refuse the probe) from steady-state blocked workload (e.g.,
	// the always-running ffmpeg on a Plex / Jellyfin homelab —
	// tolerate). Same caller-owned-baseline shape as IRQBaseline:
	// scheduler initialises an empty map per scheduler-lifetime,
	// passes the same pointer on every tick; the soft path admits
	// the first call (no baseline) and seeds, then on subsequent
	// ticks refuses only when snap.Processes \ baseline is non-empty.
	// Nil means "no baseline tracking" — every blocked process found
	// in the current snapshot refuses, matching pre-fix behaviour.
	ProcessBaseline *map[string]int
}

// softThresholds returns the per-class soft-mode thresholds for this
// config, honouring an explicit SoftThresholds override when set.
func (c OpportunisticGateConfig) softThresholds() SoftIdleThresholds {
	if c.SoftThresholds != nil {
		return *c.SoftThresholds
	}
	return LookupSoftIdleThresholds(c.Class)
}

// OpportunisticGate evaluates the opportunistic-probe idle gate.
// Mode dispatch:
//
//   - ModeSoftIdle (default, RULE-OPP-IDLE-SOFT-MODE): single-shot
//     evaluation against soft PSI thresholds + hard preconditions +
//     IRQ delta + SSH check. Returns (true, ReasonOK, snapshot) when
//     all checks pass at this instant. The scheduler's 60 s tick
//     cadence supplies the temporal envelope; no durability loop.
//
//   - ModeStrictIdle (legacy v0.5.x, --strict-idle-gate): blocks
//     until the strict predicate has been continuously TRUE for
//     opportunisticDurability (default 600 s) AND no input IRQ has
//     shown activity since the previous tick AND no remote loginctl
//     session has been active within sshIdleThresholdSeconds.
//
// In both modes the hard preconditions inherited from StartupGate
// (battery, container, scrub, blocked-process, post-resume warmup)
// are checked unchanged (RULE-OPP-IDLE-04). The input-IRQ check
// (RULE-OPP-IDLE-02) and the active-SSH check (RULE-OPP-IDLE-03) are
// also enforced in both modes; only the workload pressure
// thresholds + durability change.
//
// Returns (false, reason, nil) on refusal or context cancel.
// Refusing is cheap and the caller is expected to retry on the next
// scheduler tick.
func OpportunisticGate(ctx context.Context, cfg OpportunisticGateConfig) (bool, Reason, *Snapshot) {
	if cfg.Mode == ModeStrictIdle {
		return strictOpportunisticGate(ctx, cfg)
	}
	return softOpportunisticGate(ctx, cfg)
}

// softOpportunisticGate is the v0.6.0+ default evaluator. Single-shot:
// captures one snapshot, runs the relaxed PSI predicate plus the
// inherited hard preconditions + IRQ + SSH checks, and returns.
func softOpportunisticGate(ctx context.Context, cfg OpportunisticGateConfig) (bool, Reason, *Snapshot) {
	if ctx.Err() != nil {
		return false, ReasonOK, nil
	}

	gateCfg := cfg.GateConfig
	clk := gateCfg.clock()
	deps := snapshotDeps{procRoot: gateCfg.ProcRoot, sysRoot: gateCfg.SysRoot, clock: clk}

	// Hard preconditions are evaluated first because they're the
	// cheapest-to-observe refusal reason (RULE-OPP-IDLE-04
	// inheritance). The blocked-process check is skipped here and
	// re-evaluated below via evalBlockedProcesses so soft mode can
	// apply the per-tick ProcessBaseline tolerance — steady-state
	// homelab workloads (Plex transcoding, always-on backup daemons)
	// are tolerated, only NEW blocked-listed processes refuse.
	pre := checkHardPreconditionsSkipBlocked(gateCfg.ProcRoot, gateCfg.SysRoot, gateCfg.AllowOverride)
	if r := pre.Reason(); r != ReasonOK {
		return false, r, nil
	}

	snap := Capture(deps)

	// Process blocklist (RULE-IDLE-06). Baseline-tolerant: a
	// blocked-listed process that was already running at the
	// previous gate evaluation is treated as steady-state homelab
	// load (Plex transcoding, an always-on backup daemon, etc.)
	// and tolerated. Only NEW blocked processes — present this
	// tick but not the previous tick — refuse the probe, which is
	// what catches the "operator just kicked off a one-off rsync"
	// case the blocklist was designed for. Caller wires
	// ProcessBaseline (scheduler heap, same pattern as IRQBaseline)
	// so the comparison is across ~60 s scheduler ticks. Nil
	// baseline = first-call seeding (admit + record), matching
	// IRQBaseline first-call semantics.
	if r, refuse := evalBlockedProcesses(snap.Processes, cfg.ProcessBaseline); refuse {
		return false, r, nil
	}

	// Soft PSI / loadavg (RULE-OPP-IDLE-SOFT-MODE relaxed thresholds).
	thr := cfg.softThresholds()
	if PSIAvailable(gateCfg.ProcRoot) {
		if ok, r := evalSoftPSIPredicate(snap.PSI, thr); !ok {
			return false, r, nil
		}
	} else {
		if ok, r := evalSoftLoadAvgPredicate(snap.LoadAvg, thr); !ok {
			return false, r, nil
		}
	}

	// Input-IRQ delta (RULE-OPP-IDLE-02). When IRQBaseline is nil,
	// the first call admits without enforcement (no prior reading);
	// the scheduler's persistent baseline pointer means every
	// subsequent tick computes the delta vs the previous read.
	if cfg.IRQBaseline != nil {
		if active, r := evalInputIRQActivity(cfg, cfg.IRQBaseline); active {
			return false, r, nil
		}
	} else {
		// Caller didn't wire a baseline — seed a local one and skip
		// the delta check. Used by tests that don't exercise IRQ
		// behaviour; production always supplies IRQBaseline.
		var local IRQCounters
		_, _ = evalInputIRQActivity(cfg, &local)
	}

	// Active SSH session (RULE-OPP-IDLE-03).
	if active, sid := evalSSHActivity(cfg); active {
		return false, ReasonActiveSSHSession.WithDetail("session=" + sid), nil
	}

	return true, ReasonOK, snap
}

// strictOpportunisticGate is the legacy v0.5.x evaluator preserved
// behind `--strict-idle-gate`. Identical to v0.5.x OpportunisticGate.
func strictOpportunisticGate(ctx context.Context, cfg OpportunisticGateConfig) (bool, Reason, *Snapshot) {
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
			if active, r := evalInputIRQActivity(cfg, &prevIRQ); active {
				idle = false
				reason = r
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

// evalBlockedProcesses implements the soft-mode process blocklist check
// with caller-owned baseline tolerance. The blocklist names heavy
// transient work (rsync, ffmpeg, dnf, …); on a laptop these are
// genuinely transient and refusing is correct, but on a 24/7 homelab
// the media-transcoder entries (ffmpeg, plex-transcoder, jellyfin-
// ffmpeg, x265, HandBrakeCLI) are steady-state services that would
// permanently block opportunistic learning.
//
// Baseline-tolerance idiom (mirrors evalInputIRQActivity):
//   - prev == nil: caller hasn't wired baseline tracking; behave
//     like pre-fix code and refuse on any blocked process found.
//   - prev empty: first call — seed the baseline from snap, admit
//     without enforcing the check. The next tick will diff against
//     this seed.
//   - prev non-empty: refuse only on processes present in snap but
//     NOT in prev (i.e. NEW since the previous tick); update prev
//     to the current snap whether we refuse or admit.
//
// Returns (Reason, refuse) where refuse=true means caller should
// return Reason as the refusal reason. Reason is ReasonOK when
// admitting.
func evalBlockedProcesses(snap map[string]int, prev *map[string]int) (Reason, bool) {
	if prev == nil {
		for name := range snap {
			return ReasonBlockedProcess.WithDetail(name), true
		}
		return ReasonOK, false
	}
	if len(*prev) == 0 {
		// Seed the baseline; admit without enforcing the check.
		next := make(map[string]int, len(snap))
		for name, n := range snap {
			next[name] = n
		}
		*prev = next
		return ReasonOK, false
	}
	// Refuse on any process present in snap but not in prev.
	for name := range snap {
		if _, baseline := (*prev)[name]; !baseline {
			// New blocked process — refuse, but ALSO update prev to
			// the current snap so the same process doesn't keep
			// triggering refusal forever (matches the "rsync that's
			// been running for an hour is now steady state" intuition).
			next := make(map[string]int, len(snap))
			for n, c := range snap {
				next[n] = c
			}
			*prev = next
			return ReasonBlockedProcess.WithDetail(name), true
		}
	}
	// Everything in snap is baseline-resident; admit and refresh
	// baseline so processes that exit are dropped from the next
	// comparison set.
	next := make(map[string]int, len(snap))
	for name, n := range snap {
		next[name] = n
	}
	*prev = next
	return ReasonOK, false
}

// evalInputIRQActivity reads the current IRQ counters and compares
// against the previous snapshot. On the first call (prevIRQ empty)
// captures the baseline and reports no activity — the gate's
// durability requirement gives the caller plenty of subsequent ticks
// to detect a delta.
//
// Returns (active, reason). On parse failure, fails-closed with
// ReasonProcInterruptsUnreadable rather than ReasonRecentInputIRQ.
// The previous implementation composed "irq=parse_error" as a detail
// on ReasonRecentInputIRQ, which leaked the internal sentinel into
// the user-facing /api/v1/probe/opportunistic/status surface.
func evalInputIRQActivity(cfg OpportunisticGateConfig, prev *IRQCounters) (bool, Reason) {
	read := cfg.IRQReader
	if read == nil {
		read = func() (IRQCounters, error) {
			return ReadIRQCounters(cfg.ProcRoot)
		}
	}
	cur, err := read()
	if err != nil {
		// Conservative: parse failure means we can't prove the gate
		// is safe. RULE-OPP-IDLE-02 requires refusal in this case;
		// surface a distinct reason so operators see "proc_interrupts
		// _unreadable" rather than "recent_input_irq:irq=parse_error".
		return true, ReasonProcInterruptsUnreadable
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
			return true, ReasonRecentInputIRQ.WithDetail("irq=" + id)
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
