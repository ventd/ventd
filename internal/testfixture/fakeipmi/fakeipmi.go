// Package fakeipmi provides a deterministic IPMI interface for unit tests.
package fakeipmi

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakeipmi. Reserved for future use.
type Options struct{}

// Fake provides a mock IPMI interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake IPMI.
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

// Read reads from IPMI.
func (f *Fake) Read() {
	f.rec.Record("Read")
}
