package doctor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ventd/ventd/internal/recovery"
)

// schemaVersion pins the Report JSON shape per RULE-DOCTOR-08. A bump
// is a breaking change to web UI consumers + spec-11 wizard +
// `ventd doctor --json` output; require a spec amendment first.
const schemaVersion = "1"

// PerDetectorTimeout is the budget RULE-DOCTOR-09 caps each detector
// at. The runner cancels any Probe that exceeds this; the cancelled
// detector emits an error Fact and does not block the rest of the
// Report. With ~20 detectors this fits the 2-second total budget.
const PerDetectorTimeout = 200 * time.Millisecond

// Report is the snapshot of one full detector run. The CLI renders
// it as text (default) or JSON (--json). The web UI subscribes to
// Reports via SSE and renders the recovery-card surface.
type Report struct {
	// Schema pins the JSON shape per RULE-DOCTOR-08.
	Schema string `json:"schema_version"`

	// Generated is the wall-clock when RunOnce completed.
	Generated time.Time `json:"generated"`

	// Facts is the union of every detector's emitted Facts after
	// suppression filtering. Order is stable (detector-name,
	// then entity_hash) so successive Reports diff cleanly.
	Facts []Fact `json:"facts"`

	// DetectorErrors records detectors whose Probe itself failed
	// (returned a non-nil error or panicked). One entry per failing
	// detector. Distinct from Facts with SeverityBlocker — a detector
	// error means the detector itself broke, not that the system has
	// a blocker fault.
	DetectorErrors []DetectorError `json:"detector_errors,omitempty"`

	// Severity is the worst-case rollup across Facts. Drives the
	// CLI exit code per RULE-DOCTOR-02.
	Severity Severity `json:"severity"`
}

// DetectorError records a single detector's runtime failure (panic,
// timeout, or non-nil error from Probe).
type DetectorError struct {
	Detector string `json:"detector"`
	Err      string `json:"err"`
}

// Runner orchestrates detectors against shared deps (suppression
// store + classifier + clock).
type Runner struct {
	detectors []Detector
	suppress  *SuppressionStore
	classify  ClassifyFn
	now       func() time.Time
}

// NewRunner constructs a runner with the given detectors and shared
// state. classify defaults to recovery.Classify; now defaults to
// time.Now. Either may be nil (zero values are fine in tests that
// don't exercise classification or time-based assertions).
func NewRunner(detectors []Detector, suppress *SuppressionStore, classify ClassifyFn, now func() time.Time) *Runner {
	if classify == nil {
		classify = recovery.Classify
	}
	if now == nil {
		now = time.Now
	}
	// Defensive copy + stable order so the Skip/Only flags are
	// deterministic and the Report's per-detector iteration is
	// reproducible across runs.
	sorted := make([]Detector, len(detectors))
	copy(sorted, detectors)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name() < sorted[j].Name()
	})
	return &Runner{
		detectors: sorted,
		suppress:  suppress,
		classify:  classify,
		now:       now,
	}
}

// RunOptions tunes a single RunOnce invocation. All fields optional;
// zero value = run every registered detector with default budgets.
type RunOptions struct {
	// Skip excludes detectors whose Name() is in the slice. Wins over
	// Only on conflict (Skip is a hard exclusion).
	Skip []string

	// Only includes ONLY detectors whose Name() is in the slice. Empty
	// means "include all". Names not present in the registry are
	// silently dropped.
	Only []string

	// PerDetectorTimeout overrides the package-level default. Pass 0
	// to keep the default.
	PerDetectorTimeout time.Duration
}

// RunOnce executes every applicable detector once and returns a
// Report. The returned error is non-nil only when the runner itself
// could not produce a Report (e.g. ctx cancelled before any detector
// ran); detector-level errors surface in Report.DetectorErrors and
// don't block other detectors.
func (r *Runner) RunOnce(ctx context.Context, opts RunOptions) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, fmt.Errorf("doctor: ctx cancelled before run: %w", err)
	}

	timeout := opts.PerDetectorTimeout
	if timeout <= 0 {
		timeout = PerDetectorTimeout
	}

	skip := setOf(opts.Skip)
	only := setOf(opts.Only)
	includeOnly := len(only) > 0

	deps := Deps{
		Now:      r.now,
		Classify: r.classify,
		Suppress: r.suppress,
	}

	var facts []Fact
	var errs []DetectorError

	for _, det := range r.detectors {
		name := det.Name()
		if _, blocked := skip[name]; blocked {
			continue
		}
		if includeOnly {
			if _, allowed := only[name]; !allowed {
				continue
			}
		}

		dctx, cancel := context.WithTimeout(ctx, timeout)
		got, err := safeProbe(dctx, det, deps)
		cancel()

		if err != nil {
			errs = append(errs, DetectorError{Detector: name, Err: err.Error()})
			continue
		}
		for _, f := range got {
			if r.suppress.IsSuppressed(name, f.EntityHash) {
				continue
			}
			facts = append(facts, f)
		}
	}

	// Stable order: detector name, then entity hash.
	sort.SliceStable(facts, func(i, j int) bool {
		if facts[i].Detector != facts[j].Detector {
			return facts[i].Detector < facts[j].Detector
		}
		return facts[i].EntityHash < facts[j].EntityHash
	})

	rep := Report{
		Schema:         schemaVersion,
		Generated:      r.now(),
		Facts:          facts,
		DetectorErrors: errs,
		Severity:       severityRollup(facts),
	}
	return rep, nil
}

// severityRollup returns the worst Severity across facts. Empty slice
// rolls up to SeverityOK (zero value).
func severityRollup(facts []Fact) Severity {
	var s Severity
	for _, f := range facts {
		s = Worse(s, f.Severity)
	}
	return s
}

// safeProbe calls det.Probe and recovers from panics so one bad
// detector doesn't take down the whole run. A panic surfaces as a
// DetectorError, not a Fact.
func safeProbe(ctx context.Context, det Detector, deps Deps) (facts []Fact, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("doctor: detector %q panicked: %v", det.Name(), rec)
		}
	}()
	facts, err = det.Probe(ctx, deps)
	if err != nil {
		// Wrap context-deadline as a clearly-attributed error so the
		// CLI / web UI can render it differently from "real" errors.
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("doctor: detector %q exceeded %v: %w", det.Name(), PerDetectorTimeout, err)
		}
	}
	return facts, err
}

// setOf turns a slice of names into a set keyed by name. Empty
// slice → empty map.
func setOf(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}
