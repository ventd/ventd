package framework

import (
	"testing"

	"github.com/ventd/ventd/internal/hwdb"
)

// TestMatch_FrameworkReturnsMainline pins that a Framework DMI resolves to the
// canonical mainline preset, and the match is deterministic
// (RULE-FRAMEWORK-CATALOG-03).
func TestMatch_FrameworkReturnsMainline(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	dmi := hwdb.DMI{SysVendor: "Framework", ProductName: "Laptop 13", BoardName: "FRANMACP0A"}

	entry, ok := Match(cat, dmi)
	if !ok || entry == nil {
		t.Fatal("Framework DMI must match")
	}
	if entry.Source != SourceMainline {
		t.Errorf("matched source = %q, want %q", entry.Source, SourceMainline)
	}
	// Deterministic: same input → same output.
	for i := 0; i < 50; i++ {
		e2, ok2 := Match(cat, dmi)
		if !ok2 || e2.Source != entry.Source {
			t.Fatalf("Match not deterministic at iteration %d", i)
		}
	}
}

func TestMatch_NonFrameworkReturnsFalse(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, vendor := range []string{"LENOVO", "Dell Inc.", "Micro-Star International", "", "  "} {
		if _, ok := Match(cat, hwdb.DMI{SysVendor: vendor}); ok {
			t.Errorf("vendor %q must not match Framework", vendor)
		}
	}
}

func TestMatch_NilCatalogReturnsFalse(t *testing.T) {
	if _, ok := Match(nil, hwdb.DMI{SysVendor: "Framework"}); ok {
		t.Error("nil catalog must return false")
	}
}

func TestIsFramework_CaseInsensitive(t *testing.T) {
	for _, v := range []string{"Framework", "framework", "FRAMEWORK", " Framework "} {
		if !IsFramework(hwdb.DMI{SysVendor: v}) {
			t.Errorf("IsFramework(%q) = false, want true", v)
		}
	}
}

// TestStrategy_SpeedAt pins the interpolation a consumer relies on to preview /
// adopt a vendored curve.
func TestStrategy_SpeedAt(t *testing.T) {
	s := Strategy{SpeedCurve: []SpeedPoint{
		{TempC: 50, SpeedPct: 15},
		{TempC: 70, SpeedPct: 35},
		{TempC: 85, SpeedPct: 100},
	}}
	cases := []struct {
		temp, want int
	}{
		{0, 15},    // below first → first speed
		{50, 15},   // at first anchor
		{60, 25},   // midpoint of 50→70 / 15→35
		{70, 35},   // at middle anchor
		{85, 100},  // at last anchor
		{120, 100}, // above last → last speed
	}
	for _, tc := range cases {
		if got := s.SpeedAt(tc.temp); got != tc.want {
			t.Errorf("SpeedAt(%d) = %d, want %d", tc.temp, got, tc.want)
		}
	}
	if got := (Strategy{}).SpeedAt(60); got != 0 {
		t.Errorf("empty-curve SpeedAt = %d, want 0", got)
	}
}
