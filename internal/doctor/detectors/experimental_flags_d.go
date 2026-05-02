package detectors

import (
	"context"
	"fmt"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/recovery"
)

// HwdiagSnapshotter is the read-only surface
// ExperimentalFlagsDetector needs from the spec-15 hwdiag.Store.
// Production wires the daemon's *hwdiag.Store; tests pass a stub.
//
// Per RULE-DOCTOR-10 the detector MUST consume the store rather than
// re-implementing flag resolution — that keeps the doctor surface
// in lockstep with the rest of the daemon's experimental state.
type HwdiagSnapshotter interface {
	Snapshot(f hwdiag.Filter) hwdiag.Snapshot
}

// ExperimentalFlagsDetector lists every active experimental flag as
// an INFO-level Fact (Severity=OK in doctor's enum). Operators using
// experimental features should know they're on; the wizard's
// `--enable-amd-overdrive` / `--enable-ilo4-unlocked` and friends
// alter daemon behaviour in ways the support flow needs to see.
//
// Severity is intentionally OK (not Warning) — these flags are
// operator-opt-in. Surface them so they're visible in the diag bundle
// and on the Doctor page, not so they nag for dismissal.
type ExperimentalFlagsDetector struct {
	// Store is the hwdiag store. Daemon wiring passes the global
	// instance; tests pass an in-memory stub.
	Store HwdiagSnapshotter
}

// NewExperimentalFlagsDetector constructs a detector. nil store →
// detector is a no-op (lets test setups skip wiring when they don't
// exercise this path).
func NewExperimentalFlagsDetector(store HwdiagSnapshotter) *ExperimentalFlagsDetector {
	return &ExperimentalFlagsDetector{Store: store}
}

// Name returns the stable detector ID.
func (d *ExperimentalFlagsDetector) Name() string { return "experimental_flags" }

// Probe queries the hwdiag store for ComponentExperimental entries
// and emits one OK Fact per active flag.
func (d *ExperimentalFlagsDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Store == nil {
		return nil, nil
	}

	snap := d.Store.Snapshot(hwdiag.Filter{Component: hwdiag.ComponentExperimental})
	if len(snap.Entries) == 0 {
		return nil, nil
	}

	now := timeNowFromDeps(deps)
	facts := make([]doctor.Fact, 0, len(snap.Entries))
	for _, e := range snap.Entries {
		facts = append(facts, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityOK,
			Class:    recovery.ClassUnknown,
			Title:    fmt.Sprintf("Experimental flag active: %s", e.ID),
			Detail:   fmt.Sprintf("hwdiag entry %s reports active. Detail: %s", e.ID, e.Detail),
			EntityHash: doctor.HashEntity("experimental_flags", e.ID),
			Observed:   now,
		})
	}
	return facts, nil
}
