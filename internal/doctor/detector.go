package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/ventd/ventd/internal/recovery"
)

// ClassifyFn is the signature of recovery.Classify. Detectors that need
// to map a free-form error to a FailureClass call through a ClassifyFn
// (defaults to recovery.Classify) so tests can substitute a stub
// classifier without setting up the full recovery package's regex chain.
type ClassifyFn func(phase string, err error, journal []string) recovery.FailureClass

// Fact is one observation produced by a Detector. Each detector returns
// zero or more Facts per Probe call; Severity=OK Facts are emitted so
// the JSON output can show "this check ran and passed" (RULE-DOCTOR-03,
// no silent passes).
//
// EntityHash distinguishes "the AppArmor denial on /sys/class/hwmon vs
// the AppArmor denial on /var/lib/ventd" so the Suppression store can
// dismiss one without affecting the other. The detector computes it
// from whatever identifying data makes sense for that fault class —
// typically a sysfs path, a unit name, or a kernel-version string.
type Fact struct {
	// Detector is the stable name of the producer (matches Detector.Name).
	Detector string `json:"detector"`

	// Severity drives the exit code + UI rendering.
	Severity Severity `json:"severity"`

	// Class is the FailureClass this fact resolves to. ClassUnknown is
	// valid — it routes to the generic "send diagnostic bundle" card.
	Class recovery.FailureClass `json:"class"`

	// Title is one-line operator-facing summary (rendered as card title).
	Title string `json:"title"`

	// Detail is the longer description (rendered as card body).
	Detail string `json:"detail,omitempty"`

	// EntityHash uniquely identifies WHAT this fact is about, so a
	// suppression for "AppArmor denial on path X" doesn't suppress
	// "AppArmor denial on path Y". 16 lowercase hex chars (truncated
	// SHA-256) — opaque to the operator, stable across daemon restarts.
	EntityHash string `json:"entity_hash"`

	// Observed is the wall-clock time the detector saw this state.
	Observed time.Time `json:"observed"`

	// Journal is optional per-fact context (truncated stderr, audit
	// log line, etc.) that feeds the recovery classifier.
	Journal []string `json:"journal,omitempty"`
}

// HashEntity computes the EntityHash for a fact from one or more
// stable identifying strings. Concatenation order matters; callers
// pass identifiers in a fixed order.
func HashEntity(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Detector probes one runtime fault source and returns Facts.
//
// Detectors MUST be pure read — they may never write to /sys, /dev,
// or /var/lib/ventd. RULE-DOCTOR-01 enforces this via static analysis
// against the internal/doctor/ tree.
//
// Probe is called every detector tick (default 60 s); it must respect
// ctx for cancellation and return promptly. RULE-DOCTOR-09 caps each
// detector at 200 ms.
type Detector interface {
	// Name returns a stable token used for suppression keys, JSON
	// output, --skip/--only flags, and rule bindings. Must be a
	// short snake_case identifier (e.g. "kernel_update", "rpm_stuck").
	Name() string

	// Probe reads system state and returns zero or more Facts.
	// Returning a non-nil error means "the detector itself broke";
	// the runner reports this distinct from a Fact with
	// SeverityBlocker. Returning a nil slice with no error means
	// "ran cleanly, nothing to report".
	Probe(ctx context.Context, deps Deps) ([]Fact, error)
}

// Deps is the read-only environment a Detector needs. Concrete types
// are kept at interface level so each detector can declare exactly
// what it touches, and so tests can inject fakes without the runner
// having to know the full surface.
//
// Each detector's Probe method type-asserts on Deps for the bits it
// needs; the runner constructs one Deps per tick and passes the same
// value to every detector.
type Deps struct {
	// Now is the wall-clock the detector should stamp Facts with.
	// Injected so tests are deterministic.
	Now func() time.Time

	// Classify maps (phase, err, journal) to a FailureClass. Defaults
	// to recovery.Classify; tests can substitute a stub.
	Classify ClassifyFn

	// Suppress lets a detector skip emitting a Fact whose
	// (name, entity_hash) is currently dismissed. Detectors that
	// produce expensive Facts can short-circuit on Suppress.IsSuppressed
	// before doing the work; the runner also filters at emission time.
	Suppress *SuppressionStore
}
