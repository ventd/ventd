// Package fakewmi provides a deterministic Windows Management Instrumentation interface for unit tests.
package fakewmi

import (
	"testing"

	"github.com/ventd/ventd/internal/testfixture/base"
)

// Fake provides a stub WMI interface.
type Fake struct {
	base.Base
}

// New returns a new Fake WMI.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{Base: base.NewBase(t)}
}

// Query queries WMI.
func (f *Fake) Query() {
	f.Rec.Record("Query")
}
