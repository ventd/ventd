// Package fakedmi provides a deterministic Desktop Management Interface for unit tests.
package fakedmi

import (
	"testing"

	"github.com/ventd/ventd/internal/testfixture/base"
)

// Fake provides a stub DMI interface.
type Fake struct {
	base.Base
}

// New returns a new Fake DMI.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{Base: base.NewBase(t)}
}

// Read reads from DMI.
func (f *Fake) Read() {
	f.Rec.Record("Read")
}
