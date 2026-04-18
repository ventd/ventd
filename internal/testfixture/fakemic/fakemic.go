// Package fakemic provides a deterministic synthetic fan-acoustic signal generator for unit tests.
package fakemic

import (
	"testing"

	"github.com/ventd/ventd/internal/testfixture/base"
)

// Fake provides a stub acoustic signal generator.
type Fake struct {
	base.Base
}

// New returns a new Fake acoustic signal generator.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{Base: base.NewBase(t)}
}

// Generate produces a synthetic acoustic sample.
func (f *Fake) Generate() {
	f.Rec.Record("Generate")
}
