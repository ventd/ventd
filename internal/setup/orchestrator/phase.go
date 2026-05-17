// Package orchestrator runs the setup wizard as a sequence of independently
// checkpointable phases. Each Phase is a self-contained unit with a stable
// Name, an Execute body that returns a structured Outcome, and an Artifact
// payload that downstream phases (or the web UI) can consume.
//
// The orchestrator persists every Outcome under
// /var/lib/ventd/setup/state.json so the wizard can resume mid-flight after a
// crash, and can re-run a single failed phase via the upcoming
// /api/setup/phase/<name>/retry endpoint without wiping prior progress.
//
// This package is the v0.8.x replacement for the monolithic
// internal/setup.Manager.run() (3000-line goroutine). Phases are added one at
// a time behind the VENTD_USE_ORCHESTRATOR=1 environment gate; once all
// phases are migrated, the legacy code path is deleted.
package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/ventd/ventd/internal/recovery"
)

// Status is the lifecycle state of a single phase execution.
type Status string

const (
	// StatusPending is the initial state before a phase is reached.
	// Not currently emitted by the orchestrator — reserved for the
	// web UI's "this phase has not yet run" rendering.
	StatusPending Status = "pending"

	// StatusRunning is set transiently while Execute is in flight.
	// Persisted to disk on entry so a crash leaves a "was running
	// when we died" breadcrumb that resume can act on.
	StatusRunning Status = "running"

	// StatusSuccess means the phase completed and its postconditions
	// hold. Subsequent runs skip this phase unless RetryPhase is
	// explicitly invoked.
	StatusSuccess Status = "success"

	// StatusFailed means the phase returned a non-Success outcome.
	// The Class field identifies which recovery card the UI should
	// render. Resume restarts at this phase.
	StatusFailed Status = "failed"

	// StatusSkipped means the phase's Preconditions returned a
	// skip signal — for example, the Polarity phase skips when no
	// fans were enumerated. Skipped phases do not block resume.
	StatusSkipped Status = "skipped"
)

// Outcome is the structured result of one phase execution. It is persisted
// verbatim to the checkpoint store after every phase, success or failure.
type Outcome struct {
	// Phase is the producing phase's Name(). Mirrored here so a
	// caller iterating over Orchestrator.Run's returned slice
	// doesn't need a second lookup.
	Phase string `json:"phase"`

	// Status is the lifecycle state. See the Status constants.
	Status Status `json:"status"`

	// Class is the recovery.FailureClass on a non-Success outcome.
	// Empty on Success or Skipped. The web UI maps this to a
	// remediation card; the existing classifier taxonomy is reused
	// so we don't fork the card catalog.
	Class recovery.FailureClass `json:"class,omitempty"`

	// Detail is the human-readable failure context shown in the
	// recovery card body. Plain English, no internal jargon.
	Detail string `json:"detail,omitempty"`

	// StartedAt and FinishedAt are stamped by the orchestrator
	// around the Execute call. UTC.
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`

	// Artifact is the phase-specific result payload, serialised
	// JSON. Phase implementations marshal their typed artifact into
	// this field; consumers unmarshal back into the matching type.
	// Untyped storage avoids the orchestrator depending on every
	// phase's artifact schema.
	Artifact json.RawMessage `json:"artifact,omitempty"`
}

// Phase is one unit of wizard work. Implementations should be small,
// stateless, and free of side-effects beyond their declared artifact +
// the kernel/userspace changes documented in their Execute doc.
type Phase interface {
	// Name returns a stable, lowercase, snake_case identifier.
	// Used as the checkpoint key, the API route segment for
	// per-phase retry, and the wizard's user-facing phase label.
	// Must be unique within an Orchestrator's phase set.
	Name() string

	// Execute runs the phase body. The returned Outcome's Phase,
	// StartedAt, and FinishedAt are overwritten by the orchestrator
	// — implementations should populate Status, Class, Detail, and
	// Artifact only. Returning a Status other than Success short-
	// circuits the run; the orchestrator does not invoke later
	// phases until the failing phase is retried.
	Execute(ctx context.Context, rc *RunContext) Outcome
}

// RunContext bundles the runtime dependencies a Phase needs. Passed by
// pointer so phases can compose helper structs without copying the slog.Logger
// pointer on every Execute call.
type RunContext struct {
	// Logger is the structured logger phases write to. The
	// orchestrator wraps it with phase=<name> attribute before
	// handing to Execute.
	Logger *slog.Logger

	// HwmonRoot is the sysfs root, /sys/class/hwmon in production
	// and a t.TempDir() fixture in tests. Phases that scan the
	// hwmon tree must read through this rather than the hard-coded
	// path so they remain unit-testable.
	HwmonRoot string

	// ProcRoot is /proc in production, fixture root in tests.
	ProcRoot string

	// StateDir is the orchestrator's per-host state directory,
	// typically /var/lib/ventd/setup/. Created by the checkpoint
	// store if missing; phases that need their own sub-state
	// nest under here.
	StateDir string

	// Events is the back-channel for human-facing progress events.
	// Phases call Events.Emit during Execute to surface
	// what-am-I-doing-now lines in the wizard's activity feed.
	// nil = no-op (tests).
	Events EventSink
}

// EventSink is the back-channel for human-facing progress events. Implemented
// by setup.Manager (which forwards into the existing events ring buffer) in
// production and by a no-op stub in tests.
type EventSink interface {
	// Emit appends one activity-feed line. level is one of
	// "info" / "warn" / "error"; tag is a short scope hint like
	// "inventory" or "driver_install"; text is plain English.
	Emit(level, tag, text string)
}

// noopSink discards events. Used when RunContext.Events is nil.
type noopSink struct{}

func (noopSink) Emit(string, string, string) {}

// Log returns rc.Logger or slog.Default() when Logger is nil. Phases
// access the logger via this helper so a test that constructs a
// RunContext directly (without going through Orchestrator.New) does
// not crash on a nil-logger dereference.
func (rc *RunContext) Log() *slog.Logger {
	if rc == nil || rc.Logger == nil {
		return slog.Default()
	}
	return rc.Logger
}

// Sink returns rc.Events or noopSink{} when Events is nil. Same
// rationale as Log — defensive for direct-Execute test callers.
func (rc *RunContext) Sink() EventSink {
	if rc == nil || rc.Events == nil {
		return noopSink{}
	}
	return rc.Events
}

// EncodeArtifact JSON-marshals an artifact, returning a usable Outcome on
// failure (Status: failed, Class: ClassUnknown). Phases call this on a
// success path to populate Outcome.Artifact concisely:
//
//	art := InventoryArtifact{...}
//	out := Outcome{Status: StatusSuccess}
//	out.Artifact, err = EncodeArtifact(art)
//	if err != nil { return failOutcome(err) }
//
// The function exists so the Phase contract doesn't force every
// implementation to handle json.Marshal's error path inline.
func EncodeArtifact(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
