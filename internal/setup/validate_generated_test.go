package setup

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ventd/ventd/internal/config"
)

// hwmonFixture swaps config.hwmonRootFS for a fstest.MapFS carrying the
// named chips and restores it via t.Cleanup. Keeps the wizard-level tests
// independent of host sysfs — same pattern used in internal/config tests.
func hwmonFixture(t *testing.T, chips map[string]string) {
	t.Helper()
	fsys := fstest.MapFS{}
	for hwmon, chip := range chips {
		fsys[hwmon+"/name"] = &fstest.MapFile{Data: []byte(chip + "\n")}
	}
	prev := config.SetHwmonRootFS(fsys)
	t.Cleanup(func() { config.SetHwmonRootFS(prev) })
}

// TestValidateGeneratedConfig_HappyPath pins that a minimal, internally
// consistent config round-trips through yaml marshal + config.Parse.
func TestValidateGeneratedConfig_HappyPath(t *testing.T) {
	hwmonFixture(t, map[string]string{
		"hwmon0": "coretemp",
		"hwmon3": "nct6687",
	})
	cfg := &config.Config{
		Version: config.CurrentVersion,
		Web:     config.Web{Listen: "0.0.0.0:9999"},
		Sensors: []config.Sensor{{
			Name: "cpu_temp", Type: "hwmon",
			Path:     "/sys/class/hwmon/hwmon0/temp1_input",
			ChipName: "coretemp",
		}},
		Fans: []config.Fan{{
			Name:     "cpu_fan",
			Type:     "hwmon",
			PWMPath:  "/sys/class/hwmon/hwmon3/pwm1",
			ChipName: "nct6687",
			MinPWM:   40, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{{
			Name: "cpu_curve", Type: "linear",
			Sensor:  "cpu_temp",
			MinTemp: 40, MaxTemp: 85,
			MinPWM: 40, MaxPWM: 255,
		}},
		Controls: []config.Control{{Fan: "cpu_fan", Curve: "cpu_curve"}},
	}

	if err := validateGeneratedConfig(cfg); err != nil {
		t.Errorf("validateGeneratedConfig rejected a valid config: %v", err)
	}
}

// TestValidateGeneratedConfig_MissingCurveSource reproduces the shape of
// the phoenix-MS-7D25 rig failure (2026-04-15): a mix curve references a
// source that was never emitted. Apply's Parse would reject this; the
// pre-validation must too, so buildConfig bugs surface as a wizard error
// before the user clicks Apply.
func TestValidateGeneratedConfig_MissingCurveSource(t *testing.T) {
	cfg := &config.Config{
		Version: config.CurrentVersion,
		Web:     config.Web{Listen: "0.0.0.0:9999"},
		Curves: []config.CurveConfig{
			{Name: "cpu_curve", Type: "fixed", Value: 153},
			{
				Name: "case_curve", Type: "mix", Function: "max",
				Sources: []string{"cpu_curve", "gpu_curve"}, // gpu_curve not defined
			},
		},
	}

	err := validateGeneratedConfig(cfg)
	if err == nil {
		t.Fatal("validateGeneratedConfig accepted a config referencing an undefined curve source")
	}
	if !strings.Contains(err.Error(), "gpu_curve") {
		t.Errorf("error should name the missing source; got %v", err)
	}
}

// TestValidateGeneratedConfig_MissingSensorReference exercises another
// Parse-time check: a curve naming a sensor that wasn't defined.
func TestValidateGeneratedConfig_MissingSensorReference(t *testing.T) {
	cfg := &config.Config{
		Version: config.CurrentVersion,
		Web:     config.Web{Listen: "0.0.0.0:9999"},
		Curves: []config.CurveConfig{{
			Name: "cpu_curve", Type: "linear",
			Sensor:  "nonexistent_temp",
			MinTemp: 40, MaxTemp: 85,
			MinPWM: 40, MaxPWM: 255,
		}},
	}

	if err := validateGeneratedConfig(cfg); err == nil {
		t.Fatal("validateGeneratedConfig accepted a curve with an undefined sensor")
	}
}

// TestValidateGeneratedConfig_UnresolvableChipName is the direct regression
// test for issue #36: the wizard must not hand the operator a config the
// daemon's resolver will reject on next boot. Reproduces the class of
// failure seen on phoenix-MS-7D25 (2026-04-15): setup wrote a config
// referencing `chip_name: coretemp` when no coretemp-named hwmon device
// was enumerable in /sys. Even with the resolver bug fixed, the wizard
// must fail early so the operator gets a remediation path in the UI
// instead of a fatal on next daemon start.
func TestValidateGeneratedConfig_UnresolvableChipName(t *testing.T) {
	hwmonFixture(t, map[string]string{
		"hwmon0": "nct6687", // note: no coretemp present
	})

	cfg := &config.Config{
		Version: config.CurrentVersion,
		Web:     config.Web{Listen: "0.0.0.0:9999"},
		Sensors: []config.Sensor{{
			Name: "cpu_temp", Type: "hwmon",
			Path:     "/sys/class/hwmon/hwmon5/temp1_input",
			ChipName: "coretemp", // not loaded — module absent at scan time
		}},
		Fans: []config.Fan{{
			Name:     "cpu_fan",
			Type:     "hwmon",
			PWMPath:  "/sys/class/hwmon/hwmon0/pwm1",
			ChipName: "nct6687",
			MinPWM:   40, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{{
			Name: "cpu_curve", Type: "linear",
			Sensor:  "cpu_temp",
			MinTemp: 40, MaxTemp: 85,
			MinPWM: 40, MaxPWM: 255,
		}},
		Controls: []config.Control{{Fan: "cpu_fan", Curve: "cpu_curve"}},
	}

	err := validateGeneratedConfig(cfg)
	if err == nil {
		t.Fatal("validateGeneratedConfig accepted a config referencing an absent hwmon chip")
	}
	if !strings.Contains(err.Error(), "coretemp") {
		t.Errorf("error should name the missing chip; got %v", err)
	}
	if !strings.Contains(err.Error(), "resolver") {
		t.Errorf("error should mention the resolver so operators know what will reject it; got %v", err)
	}
}
