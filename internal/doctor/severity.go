// Package doctor implements the v0.5.10 runtime issue surface — the
// post-install equivalent of the wizard recovery classifier (#800/#810).
// Doctor's three layers (detector → classifier → renderer) share the
// recovery package's FailureClass/Remediation catalogue so adding a
// runtime fault category is single-source: one entry in
// internal/recovery, one detector here, both UIs (CLI + web) pick it up.
package doctor

import "encoding/json"

// Severity is the doctor-output triage level. The values map directly
// to the CLI exit codes pinned by RULE-DOCTOR-02:
//
//	0 = OK          (no Warning, no Blocker)
//	1 = Warning     (one or more Warning, no Blocker)
//	2 = Blocker     (one or more Blocker)
//	3 = Error       (doctor itself failed to complete)
//
// The order of constants is deliberate: zero value is OK so a freshly-
// allocated Fact is non-alarming, and Worse() picks the correct enum
// member by integer comparison.
type Severity uint8

const (
	SeverityOK      Severity = 0
	SeverityWarning Severity = 1
	SeverityBlocker Severity = 2
)

// String renders the severity as a stable lowercase token for JSON
// output and CLI text. The schema-versioned JSON output (RULE-DOCTOR-08)
// pins these names; renaming requires a schema bump.
func (s Severity) String() string {
	switch s {
	case SeverityOK:
		return "ok"
	case SeverityWarning:
		return "warning"
	case SeverityBlocker:
		return "blocker"
	default:
		return "unknown"
	}
}

// MarshalJSON emits the lowercase token form so the JSON wire format
// matches RULE-DOCTOR-08's schema-versioned promise. Without this, the
// uint8 zero value marshalled as the integer 0 (and 1/2 for the other
// members), and the web /doctor surface crashed on
// `(f.severity || "ok").toLowerCase()` because it assumed a string.
// Caught live on Phoenix's HIL after v0.5.26 rollout.
func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON accepts both the canonical string form ("ok" /
// "warning" / "blocker") and the legacy integer form (0 / 1 / 2).
// The legacy path keeps round-trips working against persisted JSON
// written by daemons predating MarshalJSON — diagnostic bundles or
// piped doctor output captured before v0.5.27.
func (s *Severity) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	if b[0] == '"' {
		var name string
		if err := json.Unmarshal(b, &name); err != nil {
			return err
		}
		switch name {
		case "ok":
			*s = SeverityOK
		case "warning":
			*s = SeverityWarning
		case "blocker":
			*s = SeverityBlocker
		default:
			*s = Severity(99) // → String() = "unknown" → ExitCode = 3
		}
		return nil
	}
	var n uint8
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = Severity(n)
	return nil
}

// Worse returns whichever of two severities is more alarming. Used by
// the Runner to roll a slice of per-detector severities up into a
// single Report-level severity for the exit code.
func Worse(a, b Severity) Severity {
	if a > b {
		return a
	}
	return b
}

// ExitCode maps a Severity to the CLI exit code per RULE-DOCTOR-02.
// SeverityError (3) is not represented here — the runner returns 3
// when a panic or context-cancel prevents the report itself from
// completing, distinct from the report having Blocker entries.
func (s Severity) ExitCode() int {
	switch s {
	case SeverityOK:
		return 0
	case SeverityWarning:
		return 1
	case SeverityBlocker:
		return 2
	default:
		return 3
	}
}
