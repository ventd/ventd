package detectors

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/iox"
	"github.com/ventd/ventd/internal/recovery"
)

// FreeSpaceProbeFn is the read-only state-directory probe consumed by
// StateFreeSpaceDetector. Production wires this to iox.EnsureFreeSpace
// so the same gate RULE-STATE-12 uses pre-flight on every KV write is
// also what doctor surfaces. Tests inject wrapped errors to exercise
// the errors.Is branches without standing up a near-full filesystem.
type FreeSpaceProbeFn func(path string, minBytes uint64) error

// StateFreeSpaceDetector is the doctor card for the
// iox.ErrInsufficientFreeSpace sentinel. Per RULE-STATE-12 a low-space
// state filesystem causes KVDB.Set / Delete / WithTransaction to refuse
// before mutating in-memory state — the daemon stays correct but every
// persist attempt fails until disk is freed. Without this detector the
// operator only learns about it via the journald error trail after the
// fact; with it, doctor reports the condition proactively as a Blocker.
//
// The audit gap this closes is pass-4 finding S5 (issue #1092):
// iox.ErrInsufficientFreeSpace had a documented errors.Is contract but
// zero production callers branching on the sentinel. This detector is
// that caller.
type StateFreeSpaceDetector struct {
	Path     string
	MinBytes uint64
	ProbeFn  FreeSpaceProbeFn
}

// NewStateFreeSpaceDetector wires production defaults:
//
//   - path == "" → "/var/lib/ventd" (the RULE-STATE-09 state directory).
//   - minBytes == 0 → iox.MinFreeBytesForState (the 1 MiB RULE-STATE-12 floor).
//   - probeFn == nil → iox.EnsureFreeSpace.
//
// Callers that want non-default values pass them explicitly; an empty
// argument is the "use the canonical production wiring" path.
func NewStateFreeSpaceDetector(path string, minBytes uint64, probeFn FreeSpaceProbeFn) *StateFreeSpaceDetector {
	if path == "" {
		path = "/var/lib/ventd"
	}
	if minBytes == 0 {
		minBytes = iox.MinFreeBytesForState
	}
	if probeFn == nil {
		probeFn = iox.EnsureFreeSpace
	}
	return &StateFreeSpaceDetector{Path: path, MinBytes: minBytes, ProbeFn: probeFn}
}

// Name returns the stable detector ID used by suppression keys, JSON
// output, --skip/--only filters, and the rule binding.
func (d *StateFreeSpaceDetector) Name() string { return "state_free_space" }

// Probe statfs's the state directory and classifies the result:
//
//   - nil error                                → no fact (healthy).
//   - errors.Is(err, os.ErrNotExist)           → no fact. RULE-STATE-10
//     guarantees the daemon will mkdir on first start; absence is the
//     normal pre-first-boot condition, not a failure.
//   - errors.Is(err, iox.ErrInsufficientFreeSpace) → Blocker fact.
//     This is the production-caller branch the audit demanded; RULE-
//     STATE-12 documents that KV writes hard-refuse in this state.
//   - any other measurement error              → Warning fact. Per
//     RULE-DOCTOR-04 the detector degrades gracefully rather than
//     pretending the gate is OK when statfs itself failed (EACCES on
//     non-root invocations is the canonical case).
func (d *StateFreeSpaceDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	err := d.ProbeFn(d.Path, d.MinBytes)
	if err == nil {
		return nil, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	now := timeNowFromDeps(deps)

	if errors.Is(err, iox.ErrInsufficientFreeSpace) {
		return []doctor.Fact{{
			Detector: d.Name(),
			Severity: doctor.SeverityBlocker,
			Class:    recovery.ClassUnknown,
			Title:    "State filesystem is below the free-space floor",
			Detail: fmt.Sprintf(
				"%v. Per RULE-STATE-12 the KV store refuses Set/Delete/WithTransaction before mutating in-memory state when fewer than %d bytes are available — calibration, polarity, and smart-mode state cannot persist until disk is freed.",
				err, d.MinBytes,
			),
			EntityHash: doctor.HashEntity("state_free_space", d.Path),
			Observed:   now,
			Journal:    []string{err.Error()},
		}}, nil
	}

	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: doctor.SeverityWarning,
		Class:    recovery.ClassUnknown,
		Title:    "Cannot measure free space for state directory",
		Detail: fmt.Sprintf(
			"%v. Doctor cannot confirm whether the RULE-STATE-12 KV-write gate is satisfied. Rerun `sudo ventd doctor` if this looks like a permissions issue.",
			err,
		),
		EntityHash: doctor.HashEntity("state_free_space_measure", d.Path),
		Observed:   now,
		Journal:    []string{err.Error()},
	}}, nil
}
