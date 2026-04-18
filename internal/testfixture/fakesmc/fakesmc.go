// Package fakesmc provides a deterministic System Management Controller interface for unit tests.
package fakesmc

import (
	"testing"

	"github.com/ventd/ventd/internal/testfixture/base"
)

// Fake provides a stub SMC interface.
type Fake struct {
	base.Base
}

// New returns a new Fake SMC.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{Base: base.NewBase(t)}
}

// ReadSensor reads a sensor from SMC.
func (f *Fake) ReadSensor() {
	f.Rec.Record("ReadSensor")
}
