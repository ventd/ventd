package experimental_test

import (
	"testing"

	"github.com/ventd/ventd/internal/experimental"
	"github.com/ventd/ventd/internal/hwdiag"
)

// TestExperimental_HwdiagEntryPublished binds RULE-EXPERIMENTAL-HWDIAG-PUBLISHED:
// Publish sets one hwdiag entry per active flag in ComponentExperimental.
func TestExperimental_HwdiagEntryPublished(t *testing.T) {
	store := hwdiag.NewStore()
	flags := experimental.Flags{
		AMDOverdrive:    true,
		ILO4Unlocked:    true,
		NVIDIACoolbits:  false,
		IDRAC9LegacyRaw: false,
	}

	experimental.Publish(store, flags)

	snap := store.Snapshot(hwdiag.Filter{Component: hwdiag.ComponentExperimental})
	if len(snap.Entries) != 2 {
		t.Fatalf("Publish with 2 active flags: got %d entries, want 2", len(snap.Entries))
	}

	ids := make(map[string]bool, 2)
	for _, e := range snap.Entries {
		ids[e.ID] = true
		if e.Component != hwdiag.ComponentExperimental {
			t.Errorf("entry %q: Component = %q, want %q", e.ID, e.Component, hwdiag.ComponentExperimental)
		}
	}
	if !ids["experimental.amd_overdrive"] {
		t.Error("missing entry for amd_overdrive")
	}
	if !ids["experimental.ilo4_unlocked"] {
		t.Error("missing entry for ilo4_unlocked")
	}
}

func TestExperimental_HwdiagNoEntriesWhenNoFlags(t *testing.T) {
	store := hwdiag.NewStore()

	experimental.Publish(store, experimental.Flags{})

	snap := store.Snapshot(hwdiag.Filter{Component: hwdiag.ComponentExperimental})
	if len(snap.Entries) != 0 {
		t.Errorf("Publish with zero active flags: got %d entries, want 0", len(snap.Entries))
	}
}
