package experimental_test

import (
	"testing"

	"github.com/ventd/ventd/internal/experimental"
)

// TestDiag_SnapshotIncludesActiveAndPreconditions binds RULE-EXPERIMENTAL-DIAG-INCLUSION:
// Snapshot encodes active flags and all-flags precondition status.
func TestDiag_SnapshotIncludesActiveAndPreconditions(t *testing.T) {
	flags := experimental.Flags{
		AMDOverdrive: true,
		ILO4Unlocked: true,
	}

	snap := experimental.Snapshot(flags)

	// Active slice must contain exactly the two enabled flags in canonical order.
	wantActive := []string{"amd_overdrive", "ilo4_unlocked"}
	if len(snap.Active) != len(wantActive) {
		t.Fatalf("Active length = %d, want %d; got %v", len(snap.Active), len(wantActive), snap.Active)
	}
	for i, name := range wantActive {
		if snap.Active[i] != name {
			t.Errorf("Active[%d] = %q, want %q", i, snap.Active[i], name)
		}
	}

	// Preconditions map must contain all 4 canonical flag names.
	wantKeys := experimental.All()
	for _, name := range wantKeys {
		if _, ok := snap.Preconditions[name]; !ok {
			t.Errorf("Preconditions missing key %q", name)
		}
	}

	// Stub preconditions are always not-met.
	for name, p := range snap.Preconditions {
		if p.Met {
			t.Errorf("Preconditions[%q].Met = true, want false (stub)", name)
		}
		if p.Detail == "" {
			t.Errorf("Preconditions[%q].Detail is empty", name)
		}
	}
}

func TestDiag_SnapshotNoActiveFlags(t *testing.T) {
	snap := experimental.Snapshot(experimental.Flags{})
	if len(snap.Active) != 0 {
		t.Errorf("Active = %v, want empty", snap.Active)
	}
	if len(snap.Preconditions) != len(experimental.All()) {
		t.Errorf("Preconditions len = %d, want %d", len(snap.Preconditions), len(experimental.All()))
	}
}
