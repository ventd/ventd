// Package fakeliquid provides a deterministic liquid cooling monitor for unit tests.
package fakeliquid

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakeliquid. Reserved for future use.
type Options struct{}

// Fake provides a mock liquid cooling monitor.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake liquid cooler.
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

// Pump controls the pump.
func (f *Fake) Pump() {
	f.rec.Record("Pump")
}
