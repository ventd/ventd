// Package fakenvml provides a deterministic NVIDIA Management Library interface for unit tests.
package fakenvml

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakenvml. Reserved for future use.
type Options struct{}

// Fake provides a mock NVML interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake NVML.
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

// Load loads the NVML library.
func (f *Fake) Load() {
	f.rec.Record("Load")
}
