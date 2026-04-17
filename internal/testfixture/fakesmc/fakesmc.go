// Package fakesmc provides a deterministic System Management Controller interface for unit tests.
package fakesmc

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakesmc. Reserved for future use.
type Options struct{}

// Fake provides a mock SMC interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake SMC.
func New(t *testing.T, opts *Options) *Fake {
	t.Helper()
	if opts == nil {
		opts = &Options{}
	}
	_ = opts
	t.Cleanup(func() {})
	return &Fake{
		rec: testutil.NewCallRecorder(),
	}
}

// ReadSensor reads a sensor from SMC.
func (f *Fake) ReadSensor() {
	f.rec.Record("ReadSensor")
}
