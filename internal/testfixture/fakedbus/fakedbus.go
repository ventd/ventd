// Package fakedbus provides a deterministic D-Bus interface for unit tests.
package fakedbus

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakedbus. Reserved for future use.
type Options struct{}

// Fake provides a mock D-Bus interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake D-Bus.
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

// Call calls a D-Bus method.
func (f *Fake) Call() {
	f.rec.Record("Call")
}
