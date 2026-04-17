// Package fakecrosec provides a deterministic Chrome EC interface for unit tests.
package fakecrosec

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakecrosec. Reserved for future use.
type Options struct{}

// Fake provides a mock Chrome EC interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake Chrome EC.
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

// Query queries the Chrome EC.
func (f *Fake) Query() {
	f.rec.Record("Query")
}
