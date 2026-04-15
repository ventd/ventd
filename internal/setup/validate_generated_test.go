package setup

import (
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestValidateGeneratedConfig_HappyPath pins that a minimal, internally
// consistent config round-trips through yaml marshal + config.Parse.
func TestValidateGeneratedConfig_HappyPath(t *testing.T) {
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
