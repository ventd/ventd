package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestCheckResolvable_HappyPath pins that a resolvable config passes
// without mutation of the caller's *Config.
func TestCheckResolvable_HappyPath(t *testing.T) {
	fsys := hwmonFS(map[string]string{
		"hwmon0": "coretemp",
		"hwmon4": "nct6687",
	}, nil)
	prev := SetHwmonRootFS(fsys)
	t.Cleanup(func() { SetHwmonRootFS(prev) })

	cfg := &Config{
		Sensors: []Sensor{{
			Name:     "cpu_temp",
			Type:     "hwmon",
			Path:     "/sys/class/hwmon/hwmon0/temp1_input",
			ChipName: "coretemp",
		}},
		Fans: []Fan{{
			Name:     "cpu_fan",
			Type:     "hwmon",
			PWMPath:  "/sys/class/hwmon/hwmon4/pwm1",
			ChipName: "nct6687",
			MinPWM:   40, MaxPWM: 255,
		}},
	}
	origPath := cfg.Sensors[0].Path
	origPWM := cfg.Fans[0].PWMPath

	if err := CheckResolvable(cfg); err != nil {
		t.Fatalf("CheckResolvable rejected a resolvable config: %v", err)
	}
	if cfg.Sensors[0].Path != origPath || cfg.Fans[0].PWMPath != origPWM {
		t.Errorf("CheckResolvable mutated the caller's config (sensor %q→%q, fan %q→%q)",
			origPath, cfg.Sensors[0].Path, origPWM, cfg.Fans[0].PWMPath)
	}
}

// TestCheckResolvable_UnknownChip exercises the wizard-facing failure
// mode: generated config references a chip_name that's not present in
// live sysfs. This is exactly the class of failure usability.md says the
// wizard must surface before writing — not after the next daemon restart.
func TestCheckResolvable_UnknownChip(t *testing.T) {
	fsys := hwmonFS(map[string]string{
		"hwmon0": "nct6687",
	}, nil)
	prev := SetHwmonRootFS(fsys)
	t.Cleanup(func() { SetHwmonRootFS(prev) })

	cfg := &Config{
		Sensors: []Sensor{{
			Name:     "cpu_temp",
			Type:     "hwmon",
			Path:     "/sys/class/hwmon/hwmon5/temp1_input",
			ChipName: "coretemp", // not loaded
		}},
	}

	err := CheckResolvable(cfg)
	if err == nil {
		t.Fatal("CheckResolvable accepted a config referencing a chip absent from sysfs")
	}
	if !strings.Contains(err.Error(), "coretemp") {
		t.Errorf("error should name the missing chip; got %v", err)
	}
}

// TestCheckResolvable_NilInput ensures callers get a clear error rather
// than a nil-pointer panic when handed a nil *Config.
func TestCheckResolvable_NilInput(t *testing.T) {
	if err := CheckResolvable(nil); err == nil {
		t.Fatal("CheckResolvable accepted nil config")
	}
}

// TestCheckResolvable_EmptyChipNameIgnored matches ResolveHwmonPaths
// semantics: entries without ChipName are left alone, so the check
// passes even if the fsys is empty.
//
// Uses /sys/class/hwmon/hwmon99/… so that EnrichChipName (called via
// CheckResolvable) reads real sysfs — which is inevitable since
// EnrichChipName bypasses the fsys injection for Save() correctness,
// see resolve_hwmon.go:65-68 — and silently no-ops: index 99 doesn't
// exist on any real host, so os.ReadFile returns ENOENT, ChipName
// stays empty, and the resolver skips the entry as the test expects.
// Without this, a live hwmon0 on the test host (coretemp, acpitz,
// hidpp_battery_*, etc.) would leak into ChipName via EnrichChipName
// and the test would see the resolver reject the populated chip.
func TestCheckResolvable_EmptyChipNameIgnored(t *testing.T) {
	prev := SetHwmonRootFS(fstest.MapFS{})
	t.Cleanup(func() { SetHwmonRootFS(prev) })

	cfg := &Config{
		Sensors: []Sensor{{
			Name: "cpu_temp",
			Type: "hwmon",
			Path: "/sys/class/hwmon/hwmon99/temp1_input",
			// ChipName intentionally empty (pre-upgrade config shape).
		}},
	}
	if err := CheckResolvable(cfg); err != nil {
		t.Fatalf("CheckResolvable should skip entries without ChipName: %v", err)
	}
}
