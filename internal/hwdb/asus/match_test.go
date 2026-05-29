package asus

import (
	"testing"

	"github.com/ventd/ventd/internal/hwdb"
)

// TestMatch_ASUSReturnsGHelper binds RULE-ASUS-CATALOG-03: the matcher is
// deterministic and keyed on the ASUS sys_vendor.
func TestMatch_ASUSReturnsGHelper(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	t.Run("asus_host_matches", func(t *testing.T) {
		dmi := hwdb.DMI{SysVendor: "ASUSTeK COMPUTER INC.", ProductName: "ROG Zephyrus G14"}
		e, ok := Match(cat, dmi)
		if !ok || e == nil {
			t.Fatalf("Match on ASUS host = (%v, %v), want a g-helper entry", e, ok)
		}
		if e.Source != SourceGHelper {
			t.Errorf("matched source = %q, want %q", e.Source, SourceGHelper)
		}
	})

	t.Run("non_asus_host_no_match", func(t *testing.T) {
		dmi := hwdb.DMI{SysVendor: "Micro-Star International Co., Ltd.", ProductName: "MS-7D25"}
		if _, ok := Match(cat, dmi); ok {
			t.Error("Match on non-ASUS host returned a match, want none")
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		dmi := hwdb.DMI{SysVendor: "ASUS"}
		first, ok1 := Match(cat, dmi)
		second, ok2 := Match(cat, dmi)
		if ok1 != ok2 || (first == nil) != (second == nil) {
			t.Fatal("Match not deterministic across calls")
		}
	})
}

// TestIsASUS pins the case-folded sys_vendor detection.
func TestIsASUS(t *testing.T) {
	cases := map[string]bool{
		"ASUSTeK COMPUTER INC.":        true,
		"ASUS":                         true,
		"asustek":                      true,
		"Micro-Star International Co.": false,
		"":                             false,
	}
	for vendor, want := range cases {
		if got := IsASUS(hwdb.DMI{SysVendor: vendor}); got != want {
			t.Errorf("IsASUS(%q) = %v, want %v", vendor, got, want)
		}
	}
}
