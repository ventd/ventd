package redactor

import (
	"os"
)

// Redactor applies a set of primitives to content bytes, accumulating
// counts into a Report. One Redactor instance is shared for the entire
// bundle generation run so the mapping store is consistent.
type Redactor struct {
	cfg        Config
	store      *MappingStore
	primitives []Primitive
	report     *Report
	// Needles for the self-check pass: cleartext strings that must not appear.
	selfCheckNeedles []string
}

// New creates a Redactor from cfg and store.
// If store is nil, a fresh in-memory store is used.
func New(cfg Config, store *MappingStore) *Redactor {
	if store == nil {
		store = NewMappingStore()
	}
	r := &Redactor{
		cfg:        cfg,
		store:      store,
		primitives: buildPrimitives(cfg),
		report:     NewReport(cfg.Profile),
	}
	// Build self-check needles from hostname (P1) and any user names.
	host := cfg.Hostname
	if host == "" {
		host, _ = os.Hostname()
	}
	if host != "" {
		r.selfCheckNeedles = append(r.selfCheckNeedles, host)
	}
	return r
}

// Apply runs all primitives over content and returns the redacted result.
func (r *Redactor) Apply(content []byte) []byte {
	for _, p := range r.primitives {
		var n int
		content, n = p.Redact(content, r.store)
		r.report.Add(p.Name(), n)
	}
	return content
}

// Report returns the accumulated redaction report.
func (r *Redactor) Report() *Report { return r.report }

// SelfCheckNeedles returns the cleartext strings that must not survive in the bundle.
func (r *Redactor) SelfCheckNeedles() []string { return r.selfCheckNeedles }
