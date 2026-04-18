// Package fakecfg provides a deterministic configuration interface for unit tests.
package fakecfg

import (
	"testing"

	"github.com/ventd/ventd/internal/testfixture/base"
)

// Fake provides a stub configuration interface.
type Fake struct {
	base.Base
}

// New returns a new Fake config.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{Base: base.NewBase(t)}
}

// Load loads configuration.
func (f *Fake) Load() {
	f.Rec.Record("Load")
}
