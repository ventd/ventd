package config

import (
	"os"
	"path/filepath"
	"testing"
)

// makeHwmonTree builds a fake hwmon tree under root that mimics what
// /sys/class/hwmon looks like. chips maps "hwmonN" → chip name; an
// empty chip name omits the name file (simulating a virtual class
// entry that has no chip identity).
func makeHwmonTree(t *testing.T, chips map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for hwmon, chip := range chips {
		dir := filepath.Join(root, hwmon)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		// pwm1 is the file paths point at; create it as a marker so
		// the dirname()/name lookup in chipNameFromSysfsPath has a
		// realistic target.
		if err := os.WriteFile(filepath.Join(dir, "pwm1"), []byte("128\n"), 0o644); err != nil {
			t.Fatalf("write pwm1: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "temp1_input"), []byte("45000\n"), 0o644); err != nil {
			t.Fatalf("write temp1_input: %v", err)
		}
		if chip != "" {
			if err := os.WriteFile(filepath.Join(dir, "name"), []byte(chip+"\n"), 0o644); err != nil {
				t.Fatalf("write name: %v", err)
			}
		}
	}
	return root
}

func TestEnrichChipName_PopulatesMissing(t *testing.T) {
	root := makeHwmonTree(t, map[string]string{
		"hwmon3": "nct6687",
		"hwmon4": "amdgpu",
	})

	cfg := &Config{
		Sensors: []Sensor{
			{Name: "cpu", Type: "hwmon", Path: filepath.Join(root, "hwmon3", "temp1_input")},
		},
		Fans: []Fan{
			{Name: "f1", Type: "hwmon", PWMPath: filepath.Join(root, "hwmon4", "pwm1")},
		},
	}
	EnrichChipName(cfg)

	if got, want := cfg.Sensors[0].ChipName, "nct6687"; got != want {
		t.Errorf("sensor ChipName: got %q, want %q", got, want)
	}
	if got, want := cfg.Fans[0].ChipName, "amdgpu"; got != want {
		t.Errorf("fan ChipName: got %q, want %q", got, want)
	}
}

func TestEnrichChipName_PreservesNonEmpty(t *testing.T) {
	// User-set ChipName must NOT be overwritten by enrichment, even
	// if the live name file disagrees.
	root := makeHwmonTree(t, map[string]string{
		"hwmon3": "nct6687",
	})

	cfg := &Config{
		Sensors: []Sensor{
			{Name: "cpu", Type: "hwmon", Path: filepath.Join(root, "hwmon3", "temp1_input"),
				ChipName: "user-overridden"},
		},
	}
	EnrichChipName(cfg)

	if got, want := cfg.Sensors[0].ChipName, "user-overridden"; got != want {
		t.Errorf("ChipName overwritten: got %q, want %q", got, want)
	}
}

func TestEnrichChipName_SkipsNonHwmon(t *testing.T) {
	cfg := &Config{
		Sensors: []Sensor{
			{Name: "gpu", Type: "nvidia", Path: "0"},
		},
		Fans: []Fan{
			{Name: "gpufan", Type: "nvidia", PWMPath: "0"},
		},
	}
	EnrichChipName(cfg)
	if cfg.Sensors[0].ChipName != "" {
		t.Errorf("nvidia sensor was enriched: got %q", cfg.Sensors[0].ChipName)
	}
	if cfg.Fans[0].ChipName != "" {
		t.Errorf("nvidia fan was enriched: got %q", cfg.Fans[0].ChipName)
	}
}

func TestEnrichChipName_StaleHwmonPathLeftEmpty(t *testing.T) {
	// Path points at a hwmonN that no longer exists. Read fails;
	// ChipName stays empty so ResolveHwmonPaths can be a no-op for
	// this entry rather than crashing on the stale path. This is
	// the upgrade-with-renumber case the design accepts.
	cfg := &Config{
		Sensors: []Sensor{
			{Name: "cpu", Type: "hwmon", Path: "/sys/class/hwmon/hwmon99/temp1_input"},
		},
	}
	EnrichChipName(cfg)
	if cfg.Sensors[0].ChipName != "" {
		t.Errorf("stale path produced ChipName %q, expected empty", cfg.Sensors[0].ChipName)
	}
}

func TestEnrichChipName_NilConfigNoOps(t *testing.T) {
	// Must not panic.
	EnrichChipName(nil)
}

func TestEnrichChipName_EmptyPathSkipped(t *testing.T) {
	// Hwmon entry with empty path (degenerate config) is skipped
	// rather than reading "/name".
	cfg := &Config{
		Sensors: []Sensor{
			{Name: "broken", Type: "hwmon", Path: ""},
		},
		Fans: []Fan{
			{Name: "broken", Type: "hwmon", PWMPath: ""},
		},
	}
	EnrichChipName(cfg)
	if cfg.Sensors[0].ChipName != "" {
		t.Errorf("empty-path sensor enriched: got %q", cfg.Sensors[0].ChipName)
	}
	if cfg.Fans[0].ChipName != "" {
		t.Errorf("empty-path fan enriched: got %q", cfg.Fans[0].ChipName)
	}
}

func TestEnrichChipName_NameWithExtraWhitespace(t *testing.T) {
	// Some hwmon drivers write trailing whitespace, BOM, multiple
	// newlines. Enrichment must trim cleanly so downstream
	// ResolveHwmonPaths comparisons match.
	root := t.TempDir()
	dir := filepath.Join(root, "hwmon3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "name"),
		[]byte("  nct6687  \n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pwm1"), []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Fans: []Fan{
			{Name: "fan", Type: "hwmon", PWMPath: filepath.Join(dir, "pwm1")},
		},
	}
	EnrichChipName(cfg)
	if got, want := cfg.Fans[0].ChipName, "nct6687"; got != want {
		t.Errorf("ChipName not trimmed: got %q, want %q", got, want)
	}
}

func TestEnrichChipName_DeviceStylePath(t *testing.T) {
	// Paths that come from /sys/devices/platform/<chip>/hwmon/hwmonN/
	// (instead of the /sys/class/hwmon/ symlink view) must enrich
	// just as cleanly. dirname()/name still resolves to the right
	// file because the chip writer puts `name` next to the pwm files.
	root := t.TempDir()
	devDir := filepath.Join(root, "platform", "nct6687.2592", "hwmon", "hwmon4")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "name"), []byte("nct6687\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "pwm1"), []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Fans: []Fan{
			{Name: "fan", Type: "hwmon", PWMPath: filepath.Join(devDir, "pwm1")},
		},
	}
	EnrichChipName(cfg)
	if got, want := cfg.Fans[0].ChipName, "nct6687"; got != want {
		t.Errorf("device-style path not enriched: got %q, want %q", got, want)
	}
}

func TestEnrichChipName_MultipleEntriesIndependent(t *testing.T) {
	// Different chips on different hwmonN must each enrich to their
	// own name; no cross-contamination.
	root := makeHwmonTree(t, map[string]string{
		"hwmon0": "coretemp",
		"hwmon3": "nct6687",
		"hwmon4": "amdgpu",
	})

	cfg := &Config{
		Sensors: []Sensor{
			{Name: "cpu", Type: "hwmon", Path: filepath.Join(root, "hwmon0", "temp1_input")},
			{Name: "mb", Type: "hwmon", Path: filepath.Join(root, "hwmon3", "temp1_input")},
			{Name: "gpu", Type: "hwmon", Path: filepath.Join(root, "hwmon4", "temp1_input")},
		},
	}
	EnrichChipName(cfg)

	wants := []string{"coretemp", "nct6687", "amdgpu"}
	for i, want := range wants {
		if got := cfg.Sensors[i].ChipName; got != want {
			t.Errorf("Sensors[%d].ChipName: got %q, want %q", i, got, want)
		}
	}
}

func TestEnrichChipName_NameFileMissing(t *testing.T) {
	// Path exists but no `name` file alongside it (rare — virtual
	// class entries). Enrichment is silent; ChipName stays empty.
	root := makeHwmonTree(t, map[string]string{
		"hwmon3": "", // no chip name file
	})
	cfg := &Config{
		Sensors: []Sensor{
			{Name: "v", Type: "hwmon", Path: filepath.Join(root, "hwmon3", "temp1_input")},
		},
	}
	EnrichChipName(cfg)
	if cfg.Sensors[0].ChipName != "" {
		t.Errorf("missing name file produced ChipName %q", cfg.Sensors[0].ChipName)
	}
}
