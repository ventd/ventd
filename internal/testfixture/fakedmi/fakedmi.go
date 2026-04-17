// Package fakedmi provides a deterministic Desktop Management Interface for unit tests.
package fakedmi

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakedmi. Reserved for future use.
type Options struct{}

// Fake provides a mock DMI interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake DMI.
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

// Read reads from DMI.
func (f *Fake) Read() {
	f.rec.Record("Read")
}
