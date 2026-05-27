package main

import (
	"testing"

	"github.com/ventd/ventd/internal/hwdb"
)

// TestFindMatchingBoardEntry_SkipsWildcardEntries pins the tier-1
// preference: wildcard-only "*" DMI entries are tier-3 generics with
// no board-specific FanProfiles, and must not be returned as the
// match. (#1283)
//
// findMatchingBoardEntry stays in package main (the daemon composition
// root selects the catalog entry and publishes it via
// budget.SetFanProfileCatalog); the per-fan acoustic classification it
// feeds moved to internal/acoustic/budget in R6b.
func TestFindMatchingBoardEntry_SkipsWildcardEntries(t *testing.T) {
	cat := &hwdb.Catalog{
		Boards: []*hwdb.BoardCatalogEntry{
			{
				ID: "wildcard_generic",
				DMIFingerprint: &hwdb.BoardDMIFingerprint{
					SysVendor: "*", ProductName: "*",
				},
			},
			{
				ID: "tier1_msi_z690",
				DMIFingerprint: &hwdb.BoardDMIFingerprint{
					SysVendor:   "Micro-Star International Co., Ltd.",
					ProductName: "*",
					BoardVendor: "Micro-Star International Co., Ltd.",
					BoardName:   "MAG Z690 TOMAHAWK*",
				},
			},
		},
	}
	dmi := hwdb.DMIFingerprint{
		SysVendor:   "Micro-Star International Co., Ltd.",
		ProductName: "MS-7D32",
		BoardVendor: "Micro-Star International Co., Ltd.",
		BoardName:   "MAG Z690 TOMAHAWK WIFI DDR4 (MS-7D32)",
	}
	got := findMatchingBoardEntry(cat, dmi)
	if got == nil {
		t.Fatal("expected match, got nil")
	}
	if got.ID != "tier1_msi_z690" {
		t.Errorf("matched %q, want tier1_msi_z690", got.ID)
	}
}

// TestIsWildcardDMIFingerprint pins the helper's contract — every
// field empty/"*" returns true; any specific value returns false.
func TestIsWildcardDMIFingerprint(t *testing.T) {
	tests := []struct {
		name string
		f    *hwdb.BoardDMIFingerprint
		want bool
	}{
		{"all_wildcard", &hwdb.BoardDMIFingerprint{SysVendor: "*", ProductName: "*"}, true},
		{"all_empty", &hwdb.BoardDMIFingerprint{}, true},
		{"specific_vendor", &hwdb.BoardDMIFingerprint{SysVendor: "MSI"}, false},
		{"nil", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWildcardDMIFingerprint(tc.f); got != tc.want {
				t.Errorf("isWildcard(%+v) = %v, want %v", tc.f, got, tc.want)
			}
		})
	}
}
