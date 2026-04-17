// Package fakeuevent provides a deterministic uevent system interface for unit tests.
package fakeuevent

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakeuevent. Reserved for future use.
type Options struct{}

// Fake provides a mock uevent interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake uevent system.
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

// Watch watches for uevent changes.
func (f *Fake) Watch() {
	f.rec.Record("Watch")
}
