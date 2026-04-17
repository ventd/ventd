// Package fakewmi provides a deterministic Windows Management Instrumentation interface for unit tests.
package fakewmi

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakewmi. Reserved for future use.
type Options struct{}

// Fake provides a mock WMI interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake WMI.
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

// Query queries WMI.
func (f *Fake) Query() {
	f.rec.Record("Query")
}
