package redactor

// Primitive is the interface implemented by each of the 10 redaction
// primitives. Redact is a pure transformation: it takes raw content bytes
// and returns the redacted version plus the count of replacements made.
// State is threaded via the MappingStore argument.
type Primitive interface {
	// Name returns the class name used in REDACTION_REPORT.json.
	Name() string
	// Redact applies the primitive to content and returns the redacted
	// content and the number of distinct replacements performed.
	Redact(content []byte, store *MappingStore) (redacted []byte, count int)
}
