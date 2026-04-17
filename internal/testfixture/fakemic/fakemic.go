// Package fakemic provides a deterministic synthetic fan-acoustic signal generator for unit tests.
package fakemic

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakemic. Reserved for future use.
type Options struct{}

// Fake provides a mock acoustic signal generator.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake microcontroller.
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

// Generate produces a synthetic acoustic sample.
func (f *Fake) Generate() {
	f.rec.Record("Generate")
}
