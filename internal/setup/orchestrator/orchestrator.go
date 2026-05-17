package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ventd/ventd/internal/recovery"
)

// DefaultStateDir is the production location for the orchestrator's
// checkpoint file and per-phase state. Tests inject a t.TempDir().
const DefaultStateDir = "/var/lib/ventd/setup"

// Orchestrator runs a fixed sequence of Phases, persisting each Outcome
// to a CheckpointStore so a crash or explicit retry can resume without
// re-doing prior work.
//
// Concurrency: an Orchestrator instance is single-threaded by contract —
// Run, RetryPhase, and Resume must not overlap on the same instance.
// The caller (setup.Manager) gates this with its own mutex; the
// checkpoint store has its own lock as a defence-in-depth.
type Orchestrator struct {
	phases []Phase
	index  map[string]int // phase name → position in phases (O(1) lookup)
	store  *CheckpointStore
	rc     *RunContext
	logger *slog.Logger
}

// New constructs an orchestrator. Returns an error if two phases share a
// Name (duplicate keys would silently overwrite checkpoint entries).
// stateDir is created with 0755 if missing — production runs as root,
// so the mkdir is unconditional.
func New(rc *RunContext, phases ...Phase) (*Orchestrator, error) {
	if rc == nil {
		return nil, errors.New("orchestrator: RunContext is required")
	}
	if rc.Events == nil {
		rc.Events = noopSink{}
	}
	if rc.Logger == nil {
		rc.Logger = slog.Default()
	}
	if rc.StateDir == "" {
		rc.StateDir = DefaultStateDir
	}
	if err := os.MkdirAll(rc.StateDir, 0o755); err != nil {
		return nil, fmt.Errorf("orchestrator: mkdir %s: %w", rc.StateDir, err)
	}

	idx := make(map[string]int, len(phases))
	for i, p := range phases {
		name := p.Name()
		if name == "" {
			return nil, fmt.Errorf("orchestrator: phase at index %d has empty Name()", i)
		}
		if _, dup := idx[name]; dup {
			return nil, fmt.Errorf("orchestrator: duplicate phase name %q", name)
		}
		idx[name] = i
	}

	return &Orchestrator{
		phases: phases,
		index:  idx,
		store:  NewCheckpointStore(rc.StateDir),
		rc:     rc,
		logger: rc.Logger,
	}, nil
}

// Store exposes the checkpoint store so callers (web API retry handler,
// setup/reset endpoint, factory-reset) can read or wipe state without
// going through a phase.
func (o *Orchestrator) Store() *CheckpointStore { return o.store }

// Run executes phases sequentially from the first non-Success outcome
// in the checkpoint store. On a fresh install (no checkpoint), starts
// at phases[0]. On resume after a crash, skips phases whose persisted
// Status is StatusSuccess and re-runs everything from the first
// non-Success entry.
//
// Returns the full ordered slice of Outcomes (one per phase, in
// declaration order). A non-nil error indicates a phase produced a
// non-Success outcome AND the orchestrator could not record the failure
// (e.g. checkpoint store write failed). A successful run with a failed
// phase returns the slice and a nil error — the caller inspects
// Outcomes[i].Status to react.
func (o *Orchestrator) Run(ctx context.Context) ([]Outcome, error) {
	state, err := o.store.Load()
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load checkpoint: %w", err)
	}

	outcomes := make([]Outcome, 0, len(o.phases))
	for _, p := range o.phases {
		// Carry forward prior Success outcomes verbatim — the
		// resume contract is "skip what already worked."
		if prior, ok := state.Outcomes[p.Name()]; ok && prior.Status == StatusSuccess {
			outcomes = append(outcomes, prior)
			continue
		}

		out := o.runPhase(ctx, p)
		outcomes = append(outcomes, out)
		state.Outcomes[p.Name()] = out
		if err := o.store.Save(state); err != nil {
			return outcomes, fmt.Errorf("orchestrator: save checkpoint after %s: %w",
				p.Name(), err)
		}
		if out.Status != StatusSuccess && out.Status != StatusSkipped {
			// Short-circuit on failure. The caller (web UI, CLI)
			// surfaces the failing outcome and waits for either
			// a retry call or a sanitize+restart.
			return outcomes, nil
		}
		if err := ctx.Err(); err != nil {
			return outcomes, err
		}
	}
	return outcomes, nil
}

// RetryPhase re-runs a single named phase, overwriting its prior
// checkpoint entry. Subsequent calls to Run resume from the next phase
// if the retry succeeded. Returns the new Outcome.
func (o *Orchestrator) RetryPhase(ctx context.Context, name string) (Outcome, error) {
	i, ok := o.index[name]
	if !ok {
		return Outcome{}, fmt.Errorf("orchestrator: unknown phase %q", name)
	}
	p := o.phases[i]

	state, err := o.store.Load()
	if err != nil {
		return Outcome{}, fmt.Errorf("orchestrator: load checkpoint: %w", err)
	}

	out := o.runPhase(ctx, p)
	state.Outcomes[name] = out
	if err := o.store.Save(state); err != nil {
		return out, fmt.Errorf("orchestrator: save checkpoint after retry %s: %w",
			name, err)
	}
	return out, nil
}

// runPhase wraps a single Execute call with the orchestrator-stamped
// fields (Phase, StartedAt, FinishedAt) and panic recovery. A panicked
// phase becomes a StatusFailed outcome with ClassUnknown so the wizard
// surfaces it rather than the operator seeing a frozen UI.
func (o *Orchestrator) runPhase(ctx context.Context, p Phase) (out Outcome) {
	name := p.Name()
	rc := *o.rc
	rc.Logger = o.logger.With("phase", name)
	started := time.Now().UTC()

	defer func() {
		if r := recover(); r != nil {
			rc.Logger.Error("phase panicked", "panic", r)
			out = Outcome{
				Phase:      name,
				Status:     StatusFailed,
				Class:      recovery.ClassUnknown,
				Detail:     fmt.Sprintf("phase panicked: %v", r),
				StartedAt:  started,
				FinishedAt: time.Now().UTC(),
			}
		}
	}()

	rc.Events.Emit("info", name, "starting phase")
	out = p.Execute(ctx, &rc)
	out.Phase = name
	out.StartedAt = started
	if out.FinishedAt.IsZero() {
		out.FinishedAt = time.Now().UTC()
	}
	switch out.Status {
	case StatusSuccess:
		rc.Events.Emit("info", name, "phase completed")
	case StatusSkipped:
		rc.Events.Emit("info", name, "phase skipped: "+out.Detail)
	case StatusFailed:
		rc.Events.Emit("error", name, "phase failed: "+out.Detail)
	case StatusRunning, StatusPending:
		// Phase returned without setting a terminal status —
		// treat as failure so the wizard doesn't silently
		// proceed. This is a phase-implementation bug; flag
		// it loudly.
		rc.Logger.Error("phase returned non-terminal status",
			"status", out.Status)
		out.Status = StatusFailed
		out.Class = recovery.ClassUnknown
		if out.Detail == "" {
			out.Detail = fmt.Sprintf("phase returned non-terminal status %q", out.Status)
		}
		rc.Events.Emit("error", name, "phase failed: "+out.Detail)
	}
	return out
}
