package redactor

import (
	"encoding/json"
)

// Report is the REDACTION_REPORT.json written into every bundle.
// RULE-DIAG-PR2C-07: generated for every bundle including --redact=off.
type Report struct {
	RedactorVersion     int            `json:"redactor_version"`
	RedactorProfile     string         `json:"redactor_profile"`
	RedactionsByClass   map[string]int `json:"redactions_by_class"`
	NonRedactedFiles    []string       `json:"non_redacted_files,omitempty"`
	RedactionConsistent bool           `json:"redaction_consistent"`
	Warnings            []string       `json:"warnings,omitempty"`
}

// NewReport creates a zeroed report for the given profile.
func NewReport(profile string) *Report {
	return &Report{
		RedactorVersion:   1,
		RedactorProfile:   profile,
		RedactionsByClass: make(map[string]int),
	}
}

// Add increments the count for class by n.
func (r *Report) Add(class string, n int) {
	r.RedactionsByClass[class] += n
}

// MarshalJSON implements json.Marshaler (used by bundle writer).
func (r *Report) Marshal() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
