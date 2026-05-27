package detectors

import (
	"context"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// WPredGateStatusFn reports the live w_pred_system gate snapshot the
// detector renders: whether the gate is open, the first failing reason +
// its human detail, and whether a gate exists at all (has=false in
// monitor-only mode). Production wires it to read the daemon's
// gate.Evaluator; tests inject a stub. A function seam keeps the
// detectors package free of an import on internal/confidence/gate.
type WPredGateStatusFn func() (open bool, reason, detail string, has bool)

// WPredGateDetector surfaces the v0.5.9 w_pred_system global gate
// (spec §2.5/§3.6): whether smart-mode predictive control is engaged, and
// when it isn't, why. A closed gate is usually benign and expected
// (smart mode off, boot warm-up, on battery, wizard not in control mode)
// → SeverityOK; only a concurrent fan stall is a genuine fault →
// SeverityWarning. Monitor-only hosts (no gate) report nothing. (#R11)
type WPredGateDetector struct {
	status WPredGateStatusFn
}

// NewWPredGateDetector constructs the detector. A nil status fn is a
// no-op (zero facts).
func NewWPredGateDetector(fn WPredGateStatusFn) *WPredGateDetector {
	return &WPredGateDetector{status: fn}
}

// Name returns the stable detector ID.
func (d *WPredGateDetector) Name() string { return "w_pred_gate" }

// Probe reads the gate snapshot and emits one status Fact when smart mode
// is active. Pure read (an atomic snapshot load through the seam); never
// touches sysfs. Silent in monitor-only mode.
func (d *WPredGateDetector) Probe(ctx context.Context, _ doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.status == nil {
		return nil, nil
	}
	open, reason, detail, has := d.status()
	if !has {
		// Monitor-only / no predictive gate — nothing to report.
		return nil, nil
	}
	if open {
		return []doctor.Fact{{
			Detector:   d.Name(),
			Severity:   doctor.SeverityOK,
			Class:      recovery.ClassUnknown,
			Title:      "Predictive control engaged",
			Detail:     "The w_pred_system gate is open: smart-mode predictive blending is active.",
			EntityHash: doctor.HashEntity("w_pred_gate", "open"),
			Observed:   time.Now(),
		}}, nil
	}

	// Closed. A mass-stall is a genuine fault worth a Warning; every
	// other reason is an expected, operator-controllable, or transient
	// state (so a freshly-booted host doesn't read as faulty).
	sev := doctor.SeverityOK
	title := "Predictive control paused"
	if reason == "mass_stall" {
		sev = doctor.SeverityWarning
		title = "Predictive control disabled: fans stalled"
	}
	return []doctor.Fact{{
		Detector:   d.Name(),
		Severity:   sev,
		Class:      recovery.ClassUnknown,
		Title:      title,
		Detail:     wpredGateReasonBody(reason, detail),
		EntityHash: doctor.HashEntity("w_pred_gate", reason),
		Observed:   time.Now(),
	}}, nil
}

// wpredGateReasonBody maps a gate reason code (+ its human detail) to an
// operator-facing explanation of why predictive control is paused.
func wpredGateReasonBody(reason, detail string) string {
	switch reason {
	case "smart_disabled":
		return "Smart mode is turned off in Settings; every fan follows its reactive curve. " +
			"Re-enable smart mode to resume predictive control."
	case "schema_not_loaded":
		return "Persisted state has not loaded with a valid schema; predictive control stays " +
			"off until it does."
	case "hard_precondition":
		cond := detail
		if cond == "" {
			cond = "a hard precondition is active"
		}
		return "Predictive control is paused while " + cond + ". It resumes automatically once " +
			"the condition clears (e.g. on AC power, outside a container, after the boot/resume " +
			"warm-up, when no disk scrub is running)."
	case "wizard_not_control":
		return "The setup wizard did not resolve to control mode (monitor-only or refused), so " +
			"predictive control is not engaged."
	case "mass_stall":
		extra := ""
		if detail != "" {
			extra = " (" + detail + ")"
		}
		return "Multiple fans are commanded to spin but read zero RPM" + extra + ". Predictive " +
			"control has yielded to the reactive curve until the stall clears — check fan power " +
			"and header connections."
	default:
		return "Predictive control is currently gated off."
	}
}
