package setup

import (
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// ═══════════════════════════════════════════════════════════════════════════
// Orchestration invariants — binds hwmon-safety.md and usability.md rules to
// the setup wizard entry points. Each subtest name matches the COVERAGE.md
// gap list. Tests that cannot be exercised without changing setup.go (hard-
// coded sysfs paths, concrete calibrate.Manager) are documented with t.Skip
// and tracked by follow-up issues.
// ═══════════════════════════════════════════════════════════════════════════

// ---------- calibrate/* ----------

// The five tests previously skipped here (TestCalibrate_AbortRestoresPWMWithin2s,
// TestCalibrate_PanicRestoresPWM, TestCalibrate_CtxCancelRestoresPWM,
// TestCalibrate_NeverWritesZeroWithoutAllowStop, TestCalibrate_PumpNeverBelowPumpMinimum)
// are now exercised in calibration_backend_test.go via the CalibrationBackend
// seam introduced for #132. The two MinPWM/pump-floor invariants live at the
// buildConfig layer and are pinned by TestBuildConfig_NeverProducesMinPWMZero
// + TestBuildConfig_PumpFloorNeverBelowMinPumpPWM — they were misframed at the
// calibrate-call layer (the cfgFan handed to RunSync intentionally carries
// 0/255 bounds because calibration is what finds the real floor; the floor
// then lands in the generated config, which is where the safety contract is
// enforced).

// ---------- apply/* ----------

func TestApply_RejectsConfigWithMinPWMZeroNoAllowStop(t *testing.T) {
	// Invariant: hwmon-safety.md rule 1 — a config with min_pwm=0 and
	// no allow_stop gate must be rejected before Apply commits it to
	// disk.
	//
	// The allow_stop field is tracked by #126. Once it lands,
	// config.Parse must reject MinPWM=0 without AllowStop=true.
	// validateGeneratedConfig wraps config.Parse, so this test would
	// pass through automatically.
	//
	// Until #126 lands, verify the current invariant: buildConfig never
	// produces MinPWM=0. The uncalibrated-fan fallback is 20.
	fans := []fanDiscovery{{
		name: "cpu fan", fanType: "hwmon", chipName: "nct6687",
		pwmPath: "/sys/class/hwmon/hwmon3/pwm1",
	}}
	cfg := buildConfig(fans, "coretemp",
		"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
		false, "", 0, &HWProfile{})

	for _, f := range cfg.Fans {
		if f.MinPWM == 0 {
			t.Errorf("buildConfig produced MinPWM=0 for fan %q without allow_stop gate", f.Name)
		}
	}
}

// ---------- detect_rpm/* ----------

// TestDetectRPM_ENOENTSkipNotCrash and TestDetectRPM_EIOSkipNotCrash
// are now exercised in calibration_backend_test.go via the
// CalibrationBackend seam introduced for #132.

// ═══════════════════════════════════════════════════════════════════════════
// Additional invariant tests targeting actual uncovered code paths in
// internal/setup that DON'T require setup.go changes.
// ═══════════════════════════════════════════════════════════════════════════

// ---------- buildConfig invariants ----------

func TestBuildConfig_NeverProducesMinPWMZero(t *testing.T) {
	// Invariant: hwmon-safety.md rule 1 — no fan should ever have
	// MinPWM=0 in the generated config. The wizard must always
	// produce a safe floor.
	cases := []struct {
		name string
		fans []fanDiscovery
	}{
		{
			name: "uncalibrated_fan",
			fans: []fanDiscovery{{
				name: "f1", fanType: "hwmon", chipName: "nct6687",
				pwmPath: "/sys/class/hwmon/hwmon3/pwm1",
			}},
		},
		{
			name: "zero_start_and_stop",
			fans: []fanDiscovery{{
				name: "f1", fanType: "hwmon", chipName: "nct6687",
				pwmPath:  "/sys/class/hwmon/hwmon3/pwm1",
				startPWM: 0, stopPWM: 0,
			}},
		},
		{
			name: "pump_with_low_calibration",
			fans: []fanDiscovery{{
				name: "Pump", fanType: "hwmon", chipName: "nct6687",
				pwmPath:  "/sys/class/hwmon/hwmon3/pwm4",
				startPWM: 5, stopPWM: 3, isPump: true,
			}},
		},
		{
			name: "nvidia_fan",
			fans: []fanDiscovery{{
				name: "gpu0", fanType: "nvidia",
				pwmPath: "0",
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hasCPU := false
			for _, f := range tc.fans {
				if f.fanType == "hwmon" {
					hasCPU = true
					break
				}
			}
			cpuSensor, cpuPath := "", ""
			if hasCPU {
				cpuSensor = "coretemp"
				cpuPath = "/sys/class/hwmon/hwmon0/temp1_input"
			}
			hasGPU := false
			for _, f := range tc.fans {
				if f.fanType == "nvidia" {
					hasGPU = true
				}
			}
			cfg := buildConfig(tc.fans, cpuSensor, cpuPath, 55.0,
				hasGPU, "", 60.0, &HWProfile{})

			for _, f := range cfg.Fans {
				if f.MinPWM == 0 {
					t.Errorf("fan %q has MinPWM=0; hwmon-safety.md requires a non-zero floor", f.Name)
				}
			}
		})
	}
}

func TestBuildConfig_PumpMinPWMNeverBelowFloor(t *testing.T) {
	// Invariant: hwmon-safety.md rule 6 — pump fans have a hard floor
	// at config.MinPumpPWM. buildConfig must enforce this regardless
	// of what calibration measured.
	floor := uint8(config.MinPumpPWM)
	cases := []struct {
		name    string
		stopPWM uint8
	}{
		{"stop_zero", 0},
		{"stop_below_floor", floor - 1},
		{"stop_at_floor", floor},
		{"stop_above_floor", floor + 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fans := []fanDiscovery{{
				name: "pump", fanType: "hwmon", chipName: "nct6687",
				pwmPath: "/sys/class/hwmon/hwmon3/pwm4",
				stopPWM: tc.stopPWM, isPump: true,
			}}
			cfg := buildConfig(fans, "coretemp",
				"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
				false, "", 0, &HWProfile{})

			if len(cfg.Fans) != 1 {
				t.Fatalf("expected 1 fan, got %d", len(cfg.Fans))
			}
			fan := cfg.Fans[0]
			if fan.MinPWM < floor {
				t.Errorf("pump MinPWM = %d, want >= %d (MinPumpPWM)", fan.MinPWM, floor)
			}
			if fan.PumpMinimum < floor {
				t.Errorf("pump PumpMinimum = %d, want >= %d (MinPumpPWM)", fan.PumpMinimum, floor)
			}
		})
	}
}

// ---------- validateGeneratedConfig ----------

func TestValidateGeneratedConfig_RejectsDanglingFanReference(t *testing.T) {
	// Invariant: a Control referencing a fan that isn't defined must
	// be caught at generation time, not on the Apply click.
	cfg := &config.Config{
		Version: config.CurrentVersion,
		Web:     config.Web{Listen: "0.0.0.0:9999"},
		Curves: []config.CurveConfig{{
			Name: "cpu_curve", Type: "fixed", Value: 128,
		}},
		Controls: []config.Control{{Fan: "nonexistent_fan", Curve: "cpu_curve"}},
	}
	err := validateGeneratedConfig(cfg)
	if err == nil {
		t.Fatal("validateGeneratedConfig accepted a control referencing an undefined fan")
	}
}

func TestValidateGeneratedConfig_RejectsDanglingCurveReference(t *testing.T) {
	cfg := &config.Config{
		Version: config.CurrentVersion,
		Web:     config.Web{Listen: "0.0.0.0:9999"},
		Fans: []config.Fan{{
			Name: "cpu_fan", Type: "hwmon",
			PWMPath: "/sys/class/hwmon/hwmon3/pwm1",
			MinPWM:  40, MaxPWM: 255,
		}},
		Controls: []config.Control{{Fan: "cpu_fan", Curve: "nonexistent_curve"}},
	}
	err := validateGeneratedConfig(cfg)
	if err == nil {
		t.Fatal("validateGeneratedConfig accepted a control referencing an undefined curve")
	}
}
