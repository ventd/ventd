package budget

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/acoustic/proxy"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/hwdb"
)

// TestResolveFanShape_NoCatalogFallsBackToHeuristics is the strict
// no-regression contract — boards without a curated catalog entry
// must keep the v0.9-and-earlier name-hint behaviour. (#1283)
func TestResolveFanShape_NoCatalogFallsBackToHeuristics(t *testing.T) {
	t.Cleanup(func() { fanProfileCatalogPtr.Store(nil) })
	fanProfileCatalogPtr.Store(nil)

	cases := []struct {
		name      string
		fan       config.Fan
		wantClass proxy.FanClass
	}{
		{"plain_case_fan", config.Fan{Name: "case_top", Type: "hwmon", PWMPath: "/sys/hwmon0/pwm1"}, proxy.ClassCase120140},
		{"laptop_blower", config.Fan{Name: "blower_left", Type: "hwmon", PWMPath: "/sys/hwmon0/pwm2"}, proxy.ClassLaptopBlower},
		{"is_pump_flag", config.Fan{Name: "any", Type: "hwmon", PWMPath: "/sys/hwmon0/pwm3", IsPump: true}, proxy.ClassAIOPump},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, dia, blade := resolveFanShape(tc.fan)
			if class != tc.wantClass {
				t.Errorf("class = %v, want %v", class, tc.wantClass)
			}
			if dia != 120 {
				t.Errorf("diameter = %v, want 120 (default)", dia)
			}
			if blade != 0 {
				t.Errorf("blade = %d, want 0 (per-class default)", blade)
			}
		})
	}
}

// TestResolveFanShape_CatalogOverridesHeuristic is the #1283
// acceptance test: when an hwdb FanProfile is published, the class +
// diameter come from the catalog, not the name-hint heuristic.
func TestResolveFanShape_CatalogOverridesHeuristic(t *testing.T) {
	t.Cleanup(func() { fanProfileCatalogPtr.Store(nil) })

	entry := &hwdb.BoardCatalogEntry{
		ID: "test_msi_z690_tomahawk",
		FanProfiles: []hwdb.FanProfile{
			{Channel: "pwm1", Class: "case_200", DiameterMM: 200, DefaultBladeCount: 5},
			{Channel: "pwm2", Class: "aio_radiator_120", DiameterMM: 120, DefaultBladeCount: 9},
		},
	}
	SetFanProfileCatalog(entry)

	// Operator labelled their 200mm fan as "intake_top" — the
	// heuristic would default to ClassCase120140 with 120mm. With
	// #1283 the catalog wins: ClassCase200 + 200mm + 5 blades.
	fan := config.Fan{Name: "intake_top", Type: "hwmon", PWMPath: "/sys/class/hwmon/hwmon3/pwm1"}
	class, dia, blade := resolveFanShape(fan)
	if class != proxy.ClassCase200 {
		t.Errorf("class = %v, want %v (from catalog)", class, proxy.ClassCase200)
	}
	if dia != 200 {
		t.Errorf("diameter = %v, want 200 (from catalog)", dia)
	}
	if blade != 5 {
		t.Errorf("blade = %d, want 5 (from catalog)", blade)
	}
}

// TestBuildAcousticBudget_UsesCatalogFanProfile wires the override
// all the way through Build: with a catalog entry that pins fan1's
// diameter at 200mm, the host loudness composition must differ from
// the heuristic-default 120mm path. (#1283)
func TestBuildAcousticBudget_UsesCatalogFanProfile(t *testing.T) {
	t.Cleanup(func() { fanProfileCatalogPtr.Store(nil) })

	rpmDir := t.TempDir()
	rpmPath := filepath.Join(rpmDir, "fan1_input")
	if err := os.WriteFile(rpmPath, []byte("1500\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := &config.Config{
		Smart: config.SmartConfig{Preset: "balanced"},
		Fans: []config.Fan{
			{Name: "intake_top", Type: "hwmon", PWMPath: "/x/pwm1", RPMPath: rpmPath, MinPWM: 80, MaxPWM: 255},
		},
	}

	fanProfileCatalogPtr.Store(nil)
	hueristic := Build(live, "intake_top", controller.PresetBalanced)

	SetFanProfileCatalog(&hwdb.BoardCatalogEntry{
		FanProfiles: []hwdb.FanProfile{
			{Channel: "pwm1", Class: "case_200", DiameterMM: 200, DefaultBladeCount: 5},
		},
	})
	catalog := Build(live, "intake_top", controller.PresetBalanced)

	// A 200mm class fan at the same RPM yields different broadband
	// loudness than the 120mm default — the proxy's tip term scales
	// with diameter². Identical CurrentDBA would be the bug.
	if catalog.CurrentDBA == hueristic.CurrentDBA {
		t.Errorf("CurrentDBA unchanged after catalog override: hueristic=%v catalog=%v",
			hueristic.CurrentDBA, catalog.CurrentDBA)
	}
}
