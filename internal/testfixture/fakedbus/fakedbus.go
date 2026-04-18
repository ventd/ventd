// Package fakedbus provides a deterministic D-Bus interface for unit tests.
package fakedbus

import (
	"testing"

	"github.com/ventd/ventd/internal/testfixture/base"
)

// Fake provides a stub D-Bus interface.
type Fake struct {
	base.Base
}

// New returns a new Fake D-Bus.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{Base: base.NewBase(t)}
}

// Call calls a D-Bus method.
func (f *Fake) Call() {
	f.Rec.Record("Call")
}
