package asus

import (
	"testing"
	"testing/fstest"
)

// TestLoadCatalog_EmbeddedFS_ParsesAllPresets loads the real vendored corpus
// and pins the g-helper default curves it ships (RULE-ASUS-CATALOG-01: the
// embedded corpus must parse + validate cleanly).
func TestLoadCatalog_EmbeddedFS_ParsesAllPresets(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if cat.Size() < 1 {
		t.Fatalf("Size() = %d, want >= 1", cat.Size())
	}
	want := []string{ModeBalanced, ModeSilent, ModeTurbo} // sorted
	got := cat.Modes()
	if len(got) != len(want) {
		t.Fatalf("Modes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Modes() = %v, want %v", got, want)
		}
	}
	// Pin a couple of the transcribed g-helper values so a bad re-sync is
	// caught: balanced CPU peaks at 69%, turbo GPU peaks at 95%.
	bal, ok := cat.Mode(ModeBalanced)
	if !ok {
		t.Fatal("balanced mode missing")
	}
	if peak := bal.PeakDuty("cpu"); peak != 69 {
		t.Errorf("balanced CPU peak duty = %d, want 69", peak)
	}
	turbo, ok := cat.Mode(ModeTurbo)
	if !ok {
		t.Fatal("turbo mode missing")
	}
	if peak := turbo.PeakDuty("gpu"); peak != 95 {
		t.Errorf("turbo GPU peak duty = %d, want 95", peak)
	}
	if len(turbo.CPU) != 8 || len(turbo.GPU) != 8 {
		t.Errorf("turbo curve lengths = cpu %d / gpu %d, want 8 each", len(turbo.CPU), len(turbo.GPU))
	}
}

// TestLoadCatalogFS_RejectsMalformedJSON binds RULE-ASUS-CATALOG-01: a config
// file that is not valid JSON aborts the load with the offending file named.
func TestLoadCatalogFS_RejectsMalformedJSON(t *testing.T) {
	fsys := fstest.MapFS{
		"configs/broken.json": &fstest.MapFile{Data: []byte(`{"source":"x","presets":[`)},
	}
	_, err := LoadCatalogFS(fsys, "configs")
	if err == nil {
		t.Fatal("LoadCatalogFS accepted malformed JSON, want error")
	}
}

// TestValidate_RejectsBadConfigs binds RULE-ASUS-CATALOG-02: every vendored
// preset satisfies the curve invariants.
func TestValidate_RejectsBadConfigs(t *testing.T) {
	cases := map[string]*Config{
		"no presets": {Source: "x"},
		"empty mode": {Presets: []Preset{{
			CPU: []CurvePoint{{TempC: 40, Pct: 10}},
			GPU: []CurvePoint{{TempC: 40, Pct: 10}},
		}}},
		"duplicate mode": {Presets: []Preset{
			{Mode: "silent", CPU: []CurvePoint{{TempC: 40, Pct: 10}}, GPU: []CurvePoint{{TempC: 40, Pct: 10}}},
			{Mode: "silent", CPU: []CurvePoint{{TempC: 40, Pct: 10}}, GPU: []CurvePoint{{TempC: 40, Pct: 10}}},
		}},
		"empty cpu curve": {Presets: []Preset{{Mode: "silent", GPU: []CurvePoint{{TempC: 40, Pct: 10}}}}},
		"duty over 100": {Presets: []Preset{{
			Mode: "silent",
			CPU:  []CurvePoint{{TempC: 40, Pct: 120}},
			GPU:  []CurvePoint{{TempC: 40, Pct: 10}},
		}}},
		"inverted temps": {Presets: []Preset{{
			Mode: "silent",
			CPU:  []CurvePoint{{TempC: 60, Pct: 10}, {TempC: 40, Pct: 20}},
			GPU:  []CurvePoint{{TempC: 40, Pct: 10}},
		}}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validate(cfg); err == nil {
				t.Errorf("validate accepted %q, want error", name)
			}
		})
	}
}

// TestDutyAt_Interpolates pins the linear interpolation + clamping the
// consumer (doctor surface, future wizard import) relies on.
func TestDutyAt_Interpolates(t *testing.T) {
	p := Preset{
		Mode: "test",
		CPU:  []CurvePoint{{TempC: 40, Pct: 0}, {TempC: 60, Pct: 50}, {TempC: 80, Pct: 100}},
		GPU:  []CurvePoint{{TempC: 40, Pct: 20}},
	}
	cases := []struct {
		device string
		temp   int
		want   int
	}{
		{"cpu", 30, 0},   // below first anchor → hold first
		{"cpu", 50, 25},  // midpoint of 40→60 / 0→50
		{"cpu", 70, 75},  // midpoint of 60→80 / 50→100
		{"cpu", 90, 100}, // above last → hold last
		{"gpu", 100, 20}, // single-point curve → flat
	}
	for _, c := range cases {
		if got := p.DutyAt(c.device, c.temp); got != c.want {
			t.Errorf("DutyAt(%q, %d) = %d, want %d", c.device, c.temp, got, c.want)
		}
	}
}
