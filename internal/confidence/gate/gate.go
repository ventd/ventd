// Package gate composes the v0.5.9 confidence controller's
// w_pred_system global AND-gate (spec-v0_5_9 §2.5 + §3.6) from real
// daemon signals. When the gate is closed, the aggregator forces every
// channel's w_pred to 0 (bypassing the Lipschitz cap) and the UI reports
// "refused": predictive control instantly yields to the reactive curve.
//
// The gate is the AND of five terms:
//
//	!smart_disabled            operator hasn't turned smart mode off (§3.6)
//	schema_loaded              persisted KV state opened with a good schema
//	hard_preconditions_ok      not on battery / in a container / during a
//	                           scrub / in the boot or post-resume warmup
//	wizard_outcome == control  the install wizard resolved to control mode
//	no_mass_stall              fewer than N fans are concurrently stalled
//
// The terms are deliberately handled as plain function seams returning
// primitives (not the idle/state/probe types) so this package stays a
// pure composition kernel that imports only the standard library — the
// same dependency-light discipline as internal/confidence/aggregator.
// The wiring layer (cmd/ventd) builds the closures over the real
// idle.CheckHardPreconditions / state.SchemaVersionLoaded /
// probe.LoadWizardOutcome / massstall.Tracker / config.SmartDisabled.
//
// Evaluation is expensive (CheckHardPreconditions globs sysfs and walks
// /proc), so it must NOT run per-fan-per-tick. A single daemon-lifetime
// Run goroutine re-evaluates every RefreshInterval and publishes an
// atomic Snapshot; the per-fan blend hook reads Open() lock-free. Worst-
// case latency from a term flipping to the gate closing is one interval
// (~one controller tick), after which the next fan tick forces w_pred=0.
package gate

import (
	"context"
	"sync/atomic"
	"time"
)

// RefreshInterval is the gate evaluator's re-evaluation cadence. Matched
// to the ~2 s controller tick: a closed gate takes effect on the next
// fan tick, and the aggregator's Lipschitz cap rides w_pred changes
// gradually on re-open, so finer granularity buys nothing.
const RefreshInterval = 2 * time.Second

// Reason names the first failing AND term (in the priority order below),
// or ReasonOK when the gate is open. ReasonInitialising is the fail-safe
// state before the first Evaluate.
type Reason string

const (
	ReasonOK             Reason = ""
	ReasonInitialising   Reason = "initialising"
	ReasonSmartDisabled  Reason = "smart_disabled"
	ReasonSchemaUnloaded Reason = "schema_not_loaded"
	ReasonPreconditions  Reason = "hard_precondition"
	ReasonWizardOutcome  Reason = "wizard_not_control"
	ReasonMassStall      Reason = "mass_stall"
)

// Snapshot is the lock-free gate state the blend hook reads each tick and
// the API/doctor surface render. Open == w_pred_system.
type Snapshot struct {
	Open   bool
	Reason Reason
	// Detail carries human context for the failing term (e.g. the idle
	// precondition reason "boot warm-up", or a mass-stall channel count).
	Detail string

	// Per-term state for the doctor/API surface.
	SchemaLoaded    bool
	PreconditionsOk bool
	WizardControl   bool
	MassStalled     bool
	SmartDisabled   bool

	EvaluatedAt time.Time
}

// Deps are the read-only signal seams. A nil seam is treated as
// "passing" (it never closes the gate), so a test can wire only the term
// under test, and a not-yet-wired term defaults permissive — the
// fail-safe lives in New (closed until the first Evaluate), not here.
type Deps struct {
	// SchemaLoaded reports state.State.SchemaVersionLoaded. nil → true.
	SchemaLoaded func() bool
	// PreconditionsOk reports idle hard-precondition clearance plus a
	// human detail string for the failing reason. nil → (true, "").
	PreconditionsOk func() (ok bool, detail string)
	// WizardControl reports whether the persisted wizard outcome is
	// control mode. nil → true.
	WizardControl func() bool
	// MassStalled reports a system-wide concurrent fan stall at now,
	// plus a human detail string (e.g. "3 fans stalled") for the
	// failing-reason surface. nil → (false, "").
	MassStalled func(now time.Time) (stalled bool, detail string)
	// SmartDisabled reports config.SmartDisabled. nil → false.
	SmartDisabled func() bool
	// Now is the injectable clock. nil → time.Now.
	Now func() time.Time
}

// Evaluator owns the gate's atomic snapshot and re-evaluation loop.
type Evaluator struct {
	deps Deps
	snap atomic.Pointer[Snapshot]
}

// New constructs an Evaluator and installs a fail-safe CLOSED snapshot so
// any reader racing ahead of the first Evaluate sees the gate shut.
func New(d Deps) *Evaluator {
	if d.Now == nil {
		d.Now = time.Now
	}
	e := &Evaluator{deps: d}
	e.snap.Store(&Snapshot{Reason: ReasonInitialising})
	return e
}

// Evaluate computes one Snapshot from the deps, stores it atomically, and
// returns it. The AND-order doubles as the Reason priority (most operator-
// actionable first): smart-disabled, schema, preconditions, wizard, mass-
// stall.
func (e *Evaluator) Evaluate() *Snapshot {
	now := e.deps.Now()
	s := &Snapshot{EvaluatedAt: now}

	s.SmartDisabled = e.deps.SmartDisabled != nil && e.deps.SmartDisabled()
	s.SchemaLoaded = e.deps.SchemaLoaded == nil || e.deps.SchemaLoaded()
	pcOk, pcDetail := true, ""
	if e.deps.PreconditionsOk != nil {
		pcOk, pcDetail = e.deps.PreconditionsOk()
	}
	s.PreconditionsOk = pcOk
	s.WizardControl = e.deps.WizardControl == nil || e.deps.WizardControl()
	massStalled, massDetail := false, ""
	if e.deps.MassStalled != nil {
		massStalled, massDetail = e.deps.MassStalled(now)
	}
	s.MassStalled = massStalled

	switch {
	case s.SmartDisabled:
		s.Reason = ReasonSmartDisabled
	case !s.SchemaLoaded:
		s.Reason = ReasonSchemaUnloaded
	case !s.PreconditionsOk:
		s.Reason = ReasonPreconditions
		s.Detail = pcDetail
	case !s.WizardControl:
		s.Reason = ReasonWizardOutcome
	case s.MassStalled:
		s.Reason = ReasonMassStall
		s.Detail = massDetail
	default:
		s.Open = true
	}

	e.snap.Store(s)
	return s
}

// Open is the hot-path reader the blend hook calls each tick. Lock-free.
// Returns false for a nil evaluator or before the first Evaluate (fail-
// safe). The monitor-only "no gate wired → predictive allowed" case is
// handled by the blend hook's own nil check, not here.
func (e *Evaluator) Open() bool {
	if e == nil {
		return false
	}
	s := e.snap.Load()
	return s != nil && s.Open
}

// Read returns the most recent snapshot for the API/doctor surface.
// nil-safe; never nil after New.
func (e *Evaluator) Read() *Snapshot {
	if e == nil {
		return nil
	}
	return e.snap.Load()
}

// Run re-evaluates the gate every RefreshInterval until ctx is done. One
// goroutine per daemon lifetime; evaluates once immediately so the gate
// reflects reality without waiting a full interval.
func (e *Evaluator) Run(ctx context.Context) {
	e.Evaluate()
	t := time.NewTicker(RefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Evaluate()
		}
	}
}
