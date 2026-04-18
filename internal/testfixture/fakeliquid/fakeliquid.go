// Package fakeliquid provides a deterministic liquid cooling monitor for unit tests.
package fakeliquid

import (
	"testing"

	"github.com/ventd/ventd/internal/testfixture/base"
)

// Fake provides a stub liquid cooling monitor.
type Fake struct {
	base.Base
}

// New returns a new Fake liquid cooler.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{Base: base.NewBase(t)}
}

// Pump controls the pump.
func (f *Fake) Pump() {
	f.Rec.Record("Pump")
}
