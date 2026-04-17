// Package fakecfg provides a deterministic configuration interface for unit tests.
package fakecfg

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakecfg. Reserved for future use.
type Options struct{}

// Fake provides a mock configuration interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake config.
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

// Load loads configuration.
func (f *Fake) Load() {
	f.rec.Record("Load")
}
