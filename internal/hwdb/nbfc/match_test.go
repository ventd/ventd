package nbfc

import (
	"testing"
	"testing/fstest"

	"github.com/ventd/ventd/internal/hwdb"
)

// testCatalog builds a small in-memory catalogue for matcher tests.
func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	fsys := fstest.MapFS{
		"configs/HP Pavilion 15-cs0xxx.json": &fstest.MapFile{
			Data: []byte(`{"NotebookModel":"HP Pavilion 15-cs0xxx","FanConfigurations":[{"ReadRegister":1,"WriteRegister":1}]}`),
		},
		"configs/HP Pavilion 17-ab240nd.json": &fstest.MapFile{
			Data: []byte(`{"NotebookModel":"HP Pavilion 17-ab240nd","FanConfigurations":[{"ReadRegister":88,"WriteRegister":88}]}`),
		},
		"configs/Acer Aspire 5749.json": &fstest.MapFile{
			Data: []byte(`{"NotebookModel":"Acer Aspire 5749","FanConfigurations":[{"ReadRegister":1,"WriteRegister":1}]}`),
		},
		"configs/HP 250 G8 Notebook PC.json": &fstest.MapFile{
			Data: []byte(`{"NotebookModel":"HP 250 G8 Notebook PC","FanConfigurations":[{"ReadAcpiMethod":"X","WriteAcpiMethod":"Y"}]}`),
		},
	}
	cat, err := LoadCatalogFS(fsys, "configs")
	if err != nil {
		t.Fatalf("LoadCatalogFS: %v", err)
	}
	return cat
}

// TestMatch_ExactProductName pins the tier-1 match: a live DMI
// product_name equal to an upstream NotebookModel returns MatchExact.
func TestMatch_ExactProductName(t *testing.T) {
	cat := testCatalog(t)
	dmi := hwdb.DMI{ProductName: "HP Pavilion 17-ab240nd"}
	entry, tier := Match(cat, dmi)
	if entry == nil || tier != MatchExact {
		t.Fatalf("got (%v, %v), want exact match", entry, tier)
	}
	if entry.Config.NotebookModel != "HP Pavilion 17-ab240nd" {
		t.Errorf("matched wrong entry: %q", entry.Config.NotebookModel)
	}
}

// TestMatch_PrefixGlob pins the tier-2 wildcard match. The upstream
// "HP Pavilion 15-cs0xxx" should match a live "HP Pavilion 15-cs0098nx"
// by stripping the trailing xxx and prefix-matching.
func TestMatch_PrefixGlob(t *testing.T) {
	cat := testCatalog(t)
	dmi := hwdb.DMI{ProductName: "HP Pavilion 15-cs0098nx"}
	entry, tier := Match(cat, dmi)
	if entry == nil || tier != MatchPrefix {
		t.Fatalf("got (%v, %v), want prefix match", entry, tier)
	}
	if entry.Config.NotebookModel != "HP Pavilion 15-cs0xxx" {
		t.Errorf("matched wrong entry: %q", entry.Config.NotebookModel)
	}
}

// TestMatch_NoMatchReturnsNone pins that an unrelated DMI returns
// (nil, MatchNone) — the not-an-error case.
func TestMatch_NoMatchReturnsNone(t *testing.T) {
	cat := testCatalog(t)
	dmi := hwdb.DMI{ProductName: "Some Random Tablet 9000"}
	entry, tier := Match(cat, dmi)
	if entry != nil || tier != MatchNone {
		t.Errorf("got (%v, %v), want (nil, MatchNone)", entry, tier)
	}
}

// TestMatch_NilCatalogReturnsNone — defensive: a nil catalog from
// LoadCatalog at daemon-start failure must not crash the matcher.
func TestMatch_NilCatalogReturnsNone(t *testing.T) {
	dmi := hwdb.DMI{ProductName: "Anything"}
	entry, tier := Match(nil, dmi)
	if entry != nil || tier != MatchNone {
		t.Errorf("nil catalog should return (nil, MatchNone); got (%v, %v)", entry, tier)
	}
}

// TestMatch_CaseFoldedExact pins that minor case drift between the
// upstream catalog and live DMI still resolves as MatchExact (some
// OEMs alter casing post-contribution).
func TestMatch_CaseFoldedExact(t *testing.T) {
	cat := testCatalog(t)
	dmi := hwdb.DMI{ProductName: "acer aspire 5749"}
	entry, tier := Match(cat, dmi)
	if entry == nil || tier != MatchExact {
		t.Fatalf("got (%v, %v), want exact case-folded match", entry, tier)
	}
}

// TestMatch_DeterministicPure pins that Match is pure — same inputs
// always produce the same output, regardless of process state.
// RULE-NBFC-CATALOG-02.
func TestMatch_DeterministicPure(t *testing.T) {
	cat := testCatalog(t)
	dmi := hwdb.DMI{ProductName: "HP Pavilion 17-ab240nd"}
	first, firstTier := Match(cat, dmi)
	for i := 0; i < 100; i++ {
		entry, tier := Match(cat, dmi)
		if entry != first || tier != firstTier {
			t.Fatalf("Match drifted on iteration %d: got (%v, %v), want (%v, %v)", i, entry, tier, first, firstTier)
		}
	}
}

// TestMatch_ACPIConfigStillMatches pins that an ACPI-only config
// (e.g. HP 250 G8 Notebook PC) still resolves to MatchExact — the
// control mode is downstream of matching. The detector decides
// what to do with the ACPI deferral based on Entry.Mode.
func TestMatch_ACPIConfigStillMatches(t *testing.T) {
	cat := testCatalog(t)
	dmi := hwdb.DMI{ProductName: "HP 250 G8 Notebook PC"}
	entry, tier := Match(cat, dmi)
	if entry == nil || tier != MatchExact {
		t.Fatalf("got (%v, %v), want exact match", entry, tier)
	}
	if entry.Mode != ControlModeACPI {
		t.Errorf("expected ControlModeACPI for HP 250 G8, got %v", entry.Mode)
	}
}

// TestStripTrailingX pins the upstream "-xxx" glob convention parse.
func TestStripTrailingX(t *testing.T) {
	tests := []struct{ in, want string }{
		{"HP Pavilion 15-cs0xxx", "HP Pavilion 15-cs0"},
		{"HP ENVY x360 Convertible 13-aXXX", "HP ENVY x360 Convertible 13-a"},
		{"NoTrailing", "NoTrailing"},
		{"", ""},
		{"xxx", ""},
	}
	for _, tt := range tests {
		got := stripTrailingX(tt.in)
		if got != tt.want {
			t.Errorf("stripTrailingX(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
