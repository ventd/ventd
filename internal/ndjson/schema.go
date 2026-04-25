// Package ndjson provides schema-versioned NDJSON writing and reading.
// It is shared between the diagnostic bundle (internal/diag) and the
// spec-05-prep trace harness. Dependency direction: this package must
// not import internal/diag or any bundle-specific type.
//
// Schema version format: "<major>.<minor>" where major bumps for
// incompatible field changes, minor for additive changes. The reader
// is strict-major: it refuses to decode if the major component does
// not match the expected schema major.
package ndjson

import (
	"fmt"
	"strconv"
	"strings"
)

// SchemaVersion is a "<major>.<minor>" string e.g. "1.0", "2.3".
type SchemaVersion string

// Major returns the major component of the schema version.
func (s SchemaVersion) Major() int {
	parts := strings.SplitN(string(s), ".", 2)
	if len(parts) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(parts[0])
	return n
}

// String returns the schema version string.
func (s SchemaVersion) String() string { return string(s) }

// MustMajorMatch returns an error if the actual version's major
// component does not match the expected version's major component.
func MustMajorMatch(expected, actual SchemaVersion) error {
	if expected.Major() != actual.Major() {
		return fmt.Errorf("ndjson schema major mismatch: want %s, got %s", expected, actual)
	}
	return nil
}

// Known schema versions used by the diag bundle and trace harness.
const (
	SchemaPWMWriteV1     SchemaVersion = "1.0"
	SchemaThermalTraceV1 SchemaVersion = "1.0"
	SchemaDiagBundleV1   SchemaVersion = "1.0"
	SchemaVentdJournalV1 SchemaVersion = "1.0"
)

// Header is the mandatory first two fields of every NDJSON event line.
// All event types embed this (or include it as the first JSON fields).
type Header struct {
	SchemaVersion SchemaVersion `json:"schema_version"`
	Timestamp     string        `json:"ts"` // RFC 3339 nanosecond
}
