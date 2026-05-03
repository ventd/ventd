// Package doctor implements the v0.5.10 runtime issue surface — the
// post-install equivalent of the wizard recovery classifier (#800/#810).
// Doctor's three layers (detector → classifier → renderer) share the
// recovery package's FailureClass/Remediation catalogue so adding a
// runtime fault category is single-source: one entry in
// internal/recovery, one detector here, both UIs (CLI + web) pick it up.
package doctor

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
