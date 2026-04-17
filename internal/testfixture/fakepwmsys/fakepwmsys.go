// Package fakepwmsys provides a deterministic PWM sysfs interface for unit tests.
package fakepwmsys

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakepwmsys. Reserved for future use.
type Options struct{}

// Fake provides a mock PWM sysfs interface.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake PWM sysfs.
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

// WritePWM writes a PWM value.
func (f *Fake) WritePWM() {
	f.rec.Record("WritePWM")
}
