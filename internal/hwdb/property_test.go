package hwdb

import (
	"slices"
	"testing"
	"testing/quick"
)

// TestPropHWDBDeterministic verifies that Match is a pure function: calling
// it twice with the same HardwareFingerprint always produces the same result.
// This pins the invariant that neither the embedded YAML parse nor the
// resolved profile depends on any mutable global state beyond remoteDB
// (which is held constant by resetRemote for the duration of this test).
func TestPropHWDBDeterministic(t *testing.T) {
	resetRemote(t)

	prop := func(fp HardwareFingerprint) bool {
		p1, e1 := Match(fp)
		p2, e2 := Match(fp)

		bothNoMatch := e1 != nil && e2 != nil
		bothMatch := e1 == nil && e2 == nil

		if !bothNoMatch && !bothMatch {
			// one call returned a profile and the other did not — non-deterministic
			return false
		}
		if bothNoMatch {
			return true
		}
		return p1.Match == p2.Match &&
			p1.Notes == p2.Notes &&
			p1.Unverified == p2.Unverified &&
			slices.Equal(p1.Modules, p2.Modules)
	}

	if err := quick.Check(prop, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}
