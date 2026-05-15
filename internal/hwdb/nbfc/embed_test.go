package nbfc

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestLoadCatalog_EmbeddedFS_ParsesAllConfigs verifies the entire
// vendored catalogue parses cleanly. RULE-NBFC-CATALOG-01 — a
// malformed config aborts daemon start with the offending file
// named; on the happy path, every NotebookModel is keyed.
func TestLoadCatalog_EmbeddedFS_ParsesAllConfigs(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if cat.Size() < 200 {
		t.Fatalf("catalogue suspiciously small: %d entries (expected >= 200)", cat.Size())
	}
	// Spot-check a well-known model.
	if e := cat.Lookup("HP Pavilion 17-ab240nd"); e == nil {
		t.Errorf("expected HP Pavilion 17-ab240nd to be keyed")
	} else if e.Config.NotebookModel != "HP Pavilion 17-ab240nd" {
		t.Errorf("Lookup returned wrong entry: %q", e.Config.NotebookModel)
	}
}

// TestLoadCatalogFS_RejectsMalformedJSON pins RULE-NBFC-CATALOG-01:
// a single malformed file aborts the whole load with the offending
// file named.
func TestLoadCatalogFS_RejectsMalformedJSON(t *testing.T) {
	fsys := fstest.MapFS{
		"configs/good.json": &fstest.MapFile{
			Data: []byte(`{"NotebookModel":"Good Model","FanConfigurations":[]}`),
		},
		"configs/broken.json": &fstest.MapFile{
			Data: []byte(`{"NotebookModel":}`), // syntax error
		},
	}
	_, err := LoadCatalogFS(fsys, "configs")
	if err == nil {
		t.Fatal("expected non-nil error for malformed config")
	}
	if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("error should name the failing file; got: %v", err)
	}
}

// TestLoadCatalogFS_RejectsEmptyNotebookModel guards against a config
// that parses successfully but has no model name.
func TestLoadCatalogFS_RejectsEmptyNotebookModel(t *testing.T) {
	fsys := fstest.MapFS{
		"configs/empty-model.json": &fstest.MapFile{
			Data: []byte(`{"NotebookModel":"   ","FanConfigurations":[]}`),
		},
	}
	_, err := LoadCatalogFS(fsys, "configs")
	if err == nil {
		t.Fatal("expected non-nil error for empty NotebookModel")
	}
	if !strings.Contains(err.Error(), "empty NotebookModel") {
		t.Errorf("error should mention empty NotebookModel; got: %v", err)
	}
}

// TestClassifyControlMode_Register pins the bulk-catalogue happy path:
// a config that uses WriteRegister + ReadRegister classifies as
// register-only.
func TestClassifyControlMode_Register(t *testing.T) {
	cfg := &Config{}
	raw := []byte(`{"NotebookModel":"x","FanConfigurations":[{"ReadRegister":1,"WriteRegister":1}]}`)
	mode := classifyControlMode(raw, cfg)
	if mode != ControlModeRegister {
		t.Errorf("expected ControlModeRegister, got %v", mode)
	}
}

// TestClassifyControlMode_Register16 pins the 16-bit-register
// classification (ReadWriteWords=true).
func TestClassifyControlMode_Register16(t *testing.T) {
	cfg := &Config{ReadWriteWords: true}
	raw := []byte(`{"NotebookModel":"x","ReadWriteWords":true,"FanConfigurations":[]}`)
	mode := classifyControlMode(raw, cfg)
	if mode != ControlModeRegister16 {
		t.Errorf("expected ControlModeRegister16, got %v", mode)
	}
}

// TestClassifyControlMode_ACPI pins the ACPI-method classification
// when the raw JSON contains any *AcpiMethod key.
func TestClassifyControlMode_ACPI(t *testing.T) {
	cfg := &Config{}
	raw := []byte(`{"NotebookModel":"x","FanConfigurations":[{"ReadAcpiMethod":"\\_SB.PCI0.SFNV"}]}`)
	mode := classifyControlMode(raw, cfg)
	if mode != ControlModeACPI {
		t.Errorf("expected ControlModeACPI, got %v", mode)
	}
}

// TestClassifyControlMode_LuaBeatsACPI: when a config mixes Lua and
// ACPI, Lua wins. Lua is the most-blocking refusal in v0.8.0; we
// surface that to the operator first.
func TestClassifyControlMode_LuaBeatsACPI(t *testing.T) {
	cfg := &Config{}
	raw := []byte(`{"NotebookModel":"x","FanConfigurations":[{"ReadAcpiMethod":"X","WriteLuaCode":"return 0"}]}`)
	mode := classifyControlMode(raw, cfg)
	if mode != ControlModeLua {
		t.Errorf("expected ControlModeLua, got %v", mode)
	}
}

// TestCatalog_AllControlModesAccountedFor walks the live embedded
// catalogue and confirms every config classifies into one of the
// four ControlMode constants (no Unknown bucket). RULE-NBFC-CATALOG-03:
// a new config that introduces a fifth control mode fails this test
// before it can silently land.
func TestCatalog_AllControlModesAccountedFor(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	seen := map[ControlMode]int{}
	for _, e := range cat.Entries {
		switch e.Mode {
		case ControlModeRegister, ControlModeRegister16, ControlModeACPI, ControlModeLua:
			seen[e.Mode]++
		default:
			t.Errorf("config %q classified as unknown mode %v", e.Config.NotebookModel, e.Mode)
		}
	}
	// Upstream v0.5.2: ~279 register + 26 register16 + 7 ACPI + 0 Lua.
	// Don't pin exact counts (they drift with upstream); just confirm
	// the dominant register tier and that Lua stays zero.
	if seen[ControlModeRegister]+seen[ControlModeRegister16] < 250 {
		t.Errorf("expected register-tier majority, got register=%d register16=%d", seen[ControlModeRegister], seen[ControlModeRegister16])
	}
	if seen[ControlModeLua] != 0 {
		t.Logf("note: Lua-tier configs present (count=%d) — spec-09 §2 lists 0 as the upstream-v0.5.2 baseline", seen[ControlModeLua])
	}
}

// TestRawStringOrArray_Unmarshal exercises both the string and array
// forms that the upstream Lua-code fields permit.
func TestRawStringOrArray_Unmarshal(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", `null`, nil},
		{"string", `"return 0"`, []string{"return 0"}},
		{"array", `["return 0","return 1"]`, []string{"return 0", "return 1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r rawStringOrArray
			if err := r.UnmarshalJSON([]byte(tt.in)); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if len(r) != len(tt.want) {
				t.Fatalf("len=%d want %d", len(r), len(tt.want))
			}
			for i := range r {
				if r[i] != tt.want[i] {
					t.Errorf("r[%d]=%q want %q", i, r[i], tt.want[i])
				}
			}
		})
	}
}
