// Package base provides the shared scaffolding embedded by every stub fixture
// in internal/testfixture/fake*. Fixtures with real protocol implementations
// (fakehwmon, faketime, fakedt, fakepwmsys, fakehid, fakecrosec) do NOT embed
// this — they have their own constructors.
package base

import (
	"testing"

	"github.com/ventd/ventd/testutil"
)

// Base is the common skeleton shared across stub fixtures. Each fake embeds
// Base and adds its protocol-specific methods.
type Base struct {
	T   *testing.T
	Rec *testutil.CallRecorder
}

// NewBase returns a Base wired for teardown. Callers typically wrap this in a
// fake-specific constructor:
//
//	func New(t *testing.T) *Fake {
//	    return &Fake{Base: base.NewBase(t)}
//	}
func NewBase(t *testing.T) Base {
	t.Helper()
	return Base{T: t, Rec: testutil.NewCallRecorder()}
}
