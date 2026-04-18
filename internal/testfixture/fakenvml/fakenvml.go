// Package fakenvml provides a deterministic NVIDIA Management Library interface for unit tests.
package fakenvml

import (
	"testing"

	"github.com/ventd/ventd/internal/testfixture/base"
)

// Fake provides a stub NVML interface.
type Fake struct {
	base.Base
}

// New returns a new Fake NVML.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{Base: base.NewBase(t)}
}

// Load loads the NVML library.
func (f *Fake) Load() {
	f.Rec.Record("Load")
}
