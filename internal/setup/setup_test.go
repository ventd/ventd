package setup

import (
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
)

// ═══════════════════════════════════════════════════════════════════════════
// Orchestration invariants — binds hwmon-safety.md and usability.md rules to
// the setup wizard entry points. Each subtest name matches the COVERAGE.md
// gap list. Tests that cannot be exercised without changing setup.go (hard-
// coded sysfs paths, concrete calibrate.Manager) are documented with t.Skip
// and tracked by follow-up issues.
// ═══════════════════════════════════════════════════════════════════════════

// ---------- detect/* ----------

func TestDetect_EmptyHwmonReturnsFriendlyError(t *testing.T) {
	// Invariant: usability.md — errors shown to the user must be human-
	// readable. No sysfs paths, no Go error strings, no stack traces.
	// In the sandbox (no /sys/class/hwmon fans), run() finishes with an
	// error describing the situation in plain English.
	m := newManager(t)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitDone(t, m, 5*time.Second)

	if final.Error == "" {
		t.Fatal("expected error when no fans are present")
	}

	forbidden := []string{
		"/sys/class/hwmon",
		"/sys/devices",
		"hwmon0",
		"pwm",
		"runtime error",
		"goroutine",
		"panic",
	}
	for _, f := range forbidden {
		if strings.Contains(final.Error, f) {
			t.Errorf("error contains sysfs/internal detail %q: %s", f, final.Error)
		}
	}
}

// ---------- calibrate/* ----------

func TestCalibrate_AbortRestoresPWMWithin2s(t *testing.T) {
	// Invariant: hwmon-safety.md rule 7 — calibration must be
	// interruptible; original PWM must be restored on abort.
	//
	// Testing this requires a fake calibrate.Manager that records
	// WritePWM calls. The current Manager holds a concrete
	// *calibrate.Manager; extracting an interface would be a setup.go
	// change.
	//
	// The abort→PWM-restore path is tested in internal/calibrate's own
	// suite and in the controller safety tests (#118). This test
	// documents the gap at the setup orchestration level.
	t.Skip("tracked by #132: extract calibrate.Manager interface for testable abort-restore")
}

func TestCalibrate_PanicRestoresPWM(t *testing.T) {
	// Invariant: hwmon-safety.md rule 4 — watchdog Restore() must fire
	// on any exit path including panics.
	//
	// run() defers cleanup but does not recover panics from within the
	// calibration goroutines. If a calibration goroutine panics, the
	// deferred watchdog restore in that goroutine fires only if the
	// calibrate.Manager wires it. Testing this requires injecting a
	// panicking calibration step.
	t.Skip("tracked by #132: extract calibrate.Manager interface for testable panic-restore")
}

func TestCalibrate_CtxCancelRestoresPWM(t *testing.T) {
	// Invariant: hwmon-safety.md rule 7 — context cancellation must
	// restore original PWM.
	//
	// Same structural constraint as the abort test: requires a fake
	// calibrate.Manager.
	t.Skip("tracked by #132: extract calibrate.Manager interface for testable ctx-cancel-restore")
}

func TestCalibrate_NeverWritesZeroWithoutAllowStop(t *testing.T) {
	// Invariant: hwmon-safety.md rule 1 — never PWM=0 without
	// allow_stop: true.
	//
	// The allow_stop field doesn't exist yet (#126). Once it lands, the
	// calibration sweep must respect it. For now, buildConfig never
	// produces MinPWM=0 for a non-pump fan (TestBuildConfig_
	// UncalibratedFanGetsSafeFloor pins MinPWM=20 fallback).
	//
	// The runtime PWM=0 gate was fixed in #124 for the controller; the
	// setup-time assertion requires the calibrate.Manager interface
	// extraction from #131.
	t.Skip("tracked by #126: allow_stop field + #132: calibrate.Manager interface")
}

func TestCalibrate_PumpNeverBelowPumpMinimum(t *testing.T) {
	// Invariant: hwmon-safety.md rule 6 — pump fans have a hard floor
	// at PumpMinimum; calibration must never write below it.
	//
	// buildConfig already enforces this at config-generation time
	// (TestBuildConfig_PumpFloorNeverBelowMinPumpPWM). The calibration-
	// time assertion requires the calibrate.Manager interface
	// extraction (#131).
	t.Skip("tracked by #132: extract calibrate.Manager interface for testable pump-floor enforcement")
}

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

func TestDetectRPM_ENOENTSkipNotCrash(t *testing.T) {
	// Invariant: hwmon-safety.md rule 5 — handle ENOENT gracefully
	// (log and skip, don't crash).
	//
	// The RPM detection path in run() calls m.cal.DetectRPMSensor which
	// reads from real sysfs. On error, run() sets DetectPhase="none" and
	// logs, which is the correct graceful-skip. Testing this code path
	// requires a fake calibrate.Manager.
	t.Skip("tracked by #132: extract calibrate.Manager interface for testable RPM detection errors")
}

func TestDetectRPM_EIOSkipNotCrash(t *testing.T) {
	// Same constraint as ENOENT — needs calibrate.Manager fake.
	t.Skip("tracked by #132: extract calibrate.Manager interface for testable RPM detection errors")
}

// ---------- reboot/* ----------

func TestReboot_DoesNotFireWhenVentdIsPid1ish(t *testing.T) {
	// Invariant: in container-like environments where PID 1 is ventd,
	// handleSystemReboot must refuse with a diagnostic.
	//
	// handleSystemReboot lives in internal/web/server.go:919, not
	// internal/setup. This test documents the gap; the invariant belongs
	// in a web handler test suite.
	t.Skip("tracked by #133: handleSystemReboot lives in internal/web, not internal/setup")
}

// ---------- wizard/* ----------

func TestWizard_StateMachineRejectsOutOfOrderSteps(t *testing.T) {
	// Invariant: submitting "apply" before "detect" must return a
	// user-facing error.
	//
	// The state machine enforcement (if any) lives in the HTTP handlers
	// handleSetupApply / handleSetupStart in internal/web/server.go.
	// The setup.Manager itself is state-tracked (running/done/applied)
	// and Start() refuses if already running or done, but "step order"
	// is an HTTP-layer concern.
	t.Skip("tracked by #133: wizard state machine enforcement lives in internal/web")
}

// ═══════════════════════════════════════════════════════════════════════════
// Additional invariant tests targeting actual uncovered code paths in
// internal/setup that DON'T require setup.go changes.
// ═══════════════════════════════════════════════════════════════════════════

// ---------- run() error message quality ----------

func TestRun_NoFansErrorIsUserFacing(t *testing.T) {
	// Invariant: usability.md — error messages must be readable by
	// someone who has never opened a terminal. The no-fans error must
	// describe the situation, not reference internal details.
	m := newManager(t)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitDone(t, m, 5*time.Second)

	if final.Error == "" {
		t.Fatal("expected non-empty error in sandbox (no fans)")
	}

	lower := strings.ToLower(final.Error)
	if !strings.Contains(lower, "fan") {
		t.Errorf("error does not mention 'fan': %s", final.Error)
	}

	internalDetails := []string{
		"nil pointer",
		"index out of range",
		"runtime",
		".go:",
		"ENOENT",
		"EIO",
	}
	for _, detail := range internalDetails {
		if strings.Contains(final.Error, detail) {
			t.Errorf("error contains internal detail %q: %s", detail, final.Error)
		}
	}
}

func TestRun_PhaseReachesAtLeastDetecting(t *testing.T) {
	// Even with no fans, the wizard must advance through the detecting
	// phase before declaring failure. This pins the minimum phase
	// progression.
	m := newManager(t)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitDone(t, m, 5*time.Second)

	validPhases := map[string]bool{
		"detecting":         true,
		"installing_driver": true,
		"scanning_fans":     true,
		"detecting_rpm":     true,
		"calibrating":       true,
		"finalizing":        true,
	}
	if !validPhases[final.Phase] {
		t.Errorf("Phase = %q, want one of %v", final.Phase, validPhases)
	}
}

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
