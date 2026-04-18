// Package fakeuevent provides a deterministic uevent system interface for unit tests.
package fakeuevent

import (
	"testing"

	"github.com/ventd/ventd/internal/testfixture/base"
)

// Fake provides a stub uevent interface.
type Fake struct {
	base.Base
}

// New returns a new Fake uevent system.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{Base: base.NewBase(t)}
}

// Watch watches for uevent changes.
func (f *Fake) Watch() {
	f.Rec.Record("Watch")
}
