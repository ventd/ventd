// Package faketime provides a deterministic time mocking interface for unit tests.
package faketime

import (
	"testing"
	"time"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for faketime. Reserved for future use.
type Options struct{}

// Fake provides a mock time interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake time.
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

// Advance advances time by the given duration.
func (f *Fake) Advance(d time.Duration) {
	f.rec.Record("Advance", d)
}
