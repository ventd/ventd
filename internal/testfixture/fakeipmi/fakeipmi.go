// Package fakeipmi provides a deterministic IPMI interface for unit tests.
package fakeipmi

import (
	"testing"

	"github.com/ventd/ventd/internal/testfixture/base"
)

// Fake provides a stub IPMI interface.
type Fake struct {
	base.Base
}

// New returns a new Fake IPMI.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{Base: base.NewBase(t)}
}

// Read reads from IPMI.
func (f *Fake) Read() {
	f.Rec.Record("Read")
}
