// Package fakemic provides a deterministic microcontroller interface for unit tests.
package fakemic

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakemic. Reserved for future use.
type Options struct{}

// Fake provides a mock microcontroller interface.
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

// Send sends data to the microcontroller.
func (f *Fake) Send() {
	f.rec.Record("Send")
}
