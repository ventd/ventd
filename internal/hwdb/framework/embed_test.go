package framework

import (
	"testing"
	"testing/fstest"
)

// TestLoadCatalog_EmbeddedFS_ParsesAllConfigs loads the real vendored corpus
// and pins the strategies the fw-fanctrl presets ship (RULE-FRAMEWORK-CATALOG-01:
// the embedded corpus must parse + validate cleanly).
func TestLoadCatalog_EmbeddedFS_ParsesAllConfigs(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if cat.Size() < 2 {
		t.Fatalf("Size() = %d, want >= 2 (mainline + amd)", cat.Size())
	}
	main := cat.Lookup(SourceMainline)
	if main == nil {
		t.Fatalf("mainline source %q not loaded", SourceMainline)
	}
	if main.Config.DefaultStrategy != "lazy" {
		t.Errorf("mainline defaultStrategy = %q, want lazy", main.Config.DefaultStrategy)
	}
	// The seven mainline strategy names are stable upstream identifiers.
	got := main.Config.StrategyNames()
	want := []string{"aeolus", "agile", "deaf", "laziest", "lazy", "medium", "very-agile"}
	if len(got) != len(want) {
		t.Fatalf("mainline strategies = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mainline strategies = %v, want %v", got, want)
		}
	}
	if cat.Lookup(SourceAMD) == nil {
		t.Errorf("amd fork source %q not loaded", SourceAMD)
	}
}

// TestLoadCatalogFS_RejectsMalformedJSON pins the fail-closed contract: a
// broken file aborts the load and names the file (RULE-FRAMEWORK-CATALOG-01).
func TestLoadCatalogFS_RejectsMalformedJSON(t *testing.T) {
	fsys := fstest.MapFS{
		"c/good.json": {Data: []byte(`{"defaultStrategy":"x","strategies":{"x":{"speedCurve":[{"temp":0,"speed":10}]}}}`)},
		"c/bad.json":  {Data: []byte(`{"defaultStrategy":`)},
	}
	_, err := LoadCatalogFS(fsys, "c")
	if err == nil {
		t.Fatal("want error for malformed JSON, got nil")
	}
	if !contains(err.Error(), "bad.json") {
		t.Errorf("error %q should name the offending file", err)
	}
}

// TestValidate_RejectsBadConfigs pins the corpus invariants
// (RULE-FRAMEWORK-CATALOG-02).
func TestValidate_RejectsBadConfigs(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"no_strategies", `{"defaultStrategy":"x","strategies":{}}`, "no strategies"},
		{"default_missing", `{"defaultStrategy":"y","strategies":{"x":{"speedCurve":[{"temp":0,"speed":0}]}}}`, "not a defined strategy"},
		{"discharge_missing", `{"defaultStrategy":"x","strategyOnDischarging":"z","strategies":{"x":{"speedCurve":[{"temp":0,"speed":0}]}}}`, "strategyOnDischarging"},
		{"speed_out_of_range", `{"defaultStrategy":"x","strategies":{"x":{"speedCurve":[{"temp":0,"speed":150}]}}}`, "out of [0,100]"},
		{"temps_descending", `{"defaultStrategy":"x","strategies":{"x":{"speedCurve":[{"temp":50,"speed":0},{"temp":40,"speed":10}]}}}`, "ascending"},
		{"empty_curve", `{"defaultStrategy":"x","strategies":{"x":{"speedCurve":[]}}}`, "empty speedCurve"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{"c/f.json": {Data: []byte(tc.json)}}
			_, err := LoadCatalogFS(fsys, "c")
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.want)
			}
			if !contains(err.Error(), tc.want) {
				t.Errorf("error %q should contain %q", err, tc.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
