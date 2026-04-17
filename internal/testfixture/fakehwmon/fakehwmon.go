// Package fakehwmon provides a deterministic /sys/class/hwmon tree for unit tests.
package fakehwmon

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Options holds configuration for fakehwmon. Reserved for future use.
type Options struct{}

// Fake provides a mock hwmon sysfs tree.
type Fake struct {
	rec *testutil.CallRecorder
}

// New returns a new Fake hwmon.
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

// Reset resets the hwmon tree.
func (f *Fake) Reset() {
	f.rec.Record("Reset")
}
