package idle

import (
	"context"
	"runtime"
	"time"
)

const (
	defaultDurability   = 300 * time.Second
	defaultTickInterval = 10 * time.Second

	// PSI thresholds (spec §4.3).
	psiCPUSomeAvg60  = 1.0
	psiCPUSomeAvg300 = 0.8
	psiIOSomeAvg60   = 5.0
	psiIOSomeAvg300  = 3.0
	psiMemFullAvg60  = 0.5

	// Loadavg fallback: ≤ 0.10 × ncpus.
	loadAvgThresholdPerCPU = 0.10
)

// GateConfig holds injectable parameters for StartupGate and RuntimeCheck.
// All pointer/zero fields default to real system resources.
type GateConfig struct {
	ProcRoot      string
	SysRoot       string
	Clock         Clock
	Durability    time.Duration
	TickInterval  time.Duration
	AllowOverride bool
	// RandFloat provides the [0,1) uniform random source for backoff jitter.
	// Nil uses math/rand/v2.Float64 (non-deterministic). Set in tests to a
	// deterministic source to verify RULE-IDLE-08 exactly.
	RandFloat func() float64
}

func (c GateConfig) clock() Clock {
	if c.Clock != nil {
		return c.Clock
	}
	return newClock()
}

func (c GateConfig) durability() time.Duration {
	if c.Durability > 0 {
		return c.Durability
	}
	return defaultDurability
}

func (c GateConfig) tickInterval() time.Duration {
	if c.TickInterval > 0 {
		return c.TickInterval
	}
	return defaultTickInterval
}

// StartupGate blocks until the idle predicate has been continuously TRUE for
// at least GateConfig.Durability (default 300 s), or ctx is cancelled, or the
// daily backoff cap is reached. On success returns (true, ReasonOK, snapshot)
// where snapshot captures all signals at the moment of gate opening
// (RULE-IDLE-01, RULE-IDLE-10). On refusal schedules the next attempt via the
// backoff formula (RULE-IDLE-08).
func StartupGate(ctx context.Context, cfg GateConfig) (bool, Reason, *Snapshot) {
	clk := cfg.clock()
	dur := newDurabilityState(cfg.durability(), clk)
	tick := cfg.tickInterval()
	deps := snapshotDeps{procRoot: cfg.ProcRoot, sysRoot: cfg.SysRoot, clock: clk}

	var refusals int

	for {
		if ctx.Err() != nil {
			return false, ReasonOK, nil
		}

		snap := Capture(deps)
		idle, reason := evalPredicate(snap, cfg)

		if dur.Record(idle) {
			return true, ReasonOK, snap
		}

		var delay time.Duration
		if !idle {
			dur.Reset()
			if cfg.RandFloat != nil {
				delay = BackoffDet(refusals, cfg.RandFloat)
			} else {
				delay = Backoff(refusals)
			}
			if delay == 0 {
				// Daily cap reached.
				return false, reason, nil
			}
			refusals++
		} else {
			// Predicate TRUE but durability window not yet elapsed — continue polling.
			delay = tick
		}

		clk.Sleep(delay)
	}
}

// RuntimeCheck evaluates the idle predicate instantaneously against the
// baseline snapshot. Returns (true, ReasonOK) when no new activity exceeds
// delta thresholds since baseline was captured (RULE-IDLE-07). Hard
// preconditions (battery, container) are always evaluated fresh.
func RuntimeCheck(_ context.Context, baseline *Snapshot, cfg GateConfig) (bool, Reason) {
	if baseline == nil {
		return false, ReasonOK
	}
	clk := cfg.clock()
	deps := snapshotDeps{procRoot: cfg.ProcRoot, sysRoot: cfg.SysRoot, clock: clk}
	snap := Capture(deps)

	// Hard preconditions are always evaluated fresh (RULE-IDLE-02, RULE-IDLE-03).
	pre := CheckHardPreconditions(cfg.ProcRoot, cfg.SysRoot, cfg.AllowOverride)
	if r := pre.Reason(); r != ReasonOK {
		return false, r
	}

	// Evaluate PSI or loadavg (RULE-IDLE-04).
	if PSIAvailable(cfg.ProcRoot) {
		if ok, r := evalPSIPredicate(snap.PSI); !ok {
			return false, r
		}
	} else {
		if ok, r := evalLoadAvgPredicate(snap.LoadAvg); !ok {
			return false, r
		}
	}

	// Check for NEW blocked processes (RULE-IDLE-07: baseline-resident activity
	// does not cause refusal — only new activity beyond baseline).
	for name := range snap.Processes {
		if _, inBaseline := baseline.Processes[name]; !inBaseline {
			return false, ReasonBlockedProcess.WithDetail(name)
		}
	}

	return true, ReasonOK
}

// evalPredicate evaluates the full idle predicate for a freshly captured
// snapshot. Used by StartupGate.
func evalPredicate(snap *Snapshot, cfg GateConfig) (bool, Reason) {
	// Hard preconditions (RULE-IDLE-02, RULE-IDLE-03, RULE-IDLE-09).
	pre := CheckHardPreconditions(cfg.ProcRoot, cfg.SysRoot, cfg.AllowOverride)
	if r := pre.Reason(); r != ReasonOK {
		return false, r
	}

	// Process blocklist (RULE-IDLE-06).
	for name := range snap.Processes {
		return false, ReasonBlockedProcess.WithDetail(name)
	}

	// Primary signal: PSI (RULE-IDLE-04).
	if PSIAvailable(cfg.ProcRoot) {
		return evalPSIPredicate(snap.PSI)
	}

	// Fallback: loadavg (RULE-IDLE-04, RULE-IDLE-05).
	return evalLoadAvgPredicate(snap.LoadAvg)
}

// evalPSIPredicate checks the PSI thresholds from spec §4.3.
func evalPSIPredicate(psi PSIReadings) (bool, Reason) {
	if psi.CPUSomeAvg60 > psiCPUSomeAvg60 || psi.CPUSomeAvg300 > psiCPUSomeAvg300 {
		return false, ReasonPSIPressure
	}
	if psi.IOSomeAvg60 > psiIOSomeAvg60 || psi.IOSomeAvg300 > psiIOSomeAvg300 {
		return false, ReasonPSIPressure
	}
	if psi.MemFullAvg60 > psiMemFullAvg60 {
		return false, ReasonPSIPressure
	}
	return true, ReasonOK
}

// evalLoadAvgPredicate checks the loadavg fallback threshold from spec §4.3.
func evalLoadAvgPredicate(la [3]float64) (bool, Reason) {
	ncpus := runtime.NumCPU()
	if ncpus < 1 {
		ncpus = 1
	}
	threshold := loadAvgThresholdPerCPU * float64(ncpus)
	if la[0] > threshold {
		return false, ReasonCPUIdle
	}
	return true, ReasonOK
}
