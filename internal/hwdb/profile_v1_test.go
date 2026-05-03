package hwdb

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"
)

// TestRuleHwdbPR2_01 verifies RULE-HWDB-PR2-01: every driver_profile MUST
// declare all required fields; missing field causes LoadCatalogFromFS to fail.
func TestRuleHwdbPR2_01(t *testing.T) {
	t.Run("TestRuleHwdbPR2_01", func(t *testing.T) {
		// A complete valid driver profile must load cleanly.
		valid := buildValidCatalogFS(t, validDriverYAML("valid-driver"), "")
		_, err := LoadCatalogFromFS(valid)
		if err != nil {
			t.Fatalf("valid driver: unexpected error: %v", err)
		}

		// Missing required field "capability" must be rejected.
		missingCap := buildValidCatalogFS(t, missingCapabilityDriverYAML(), "")
		_, err = LoadCatalogFromFS(missingCap)
		if err == nil {
			t.Fatal("missing capability: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "required field") {
			t.Errorf("missing capability: error %q should mention \"required field\"", err.Error())
		}

		// Missing required field "exit_behaviour" must be rejected.
		missingExit := buildValidCatalogFS(t, missingExitBehaviourDriverYAML(), "")
		_, err = LoadCatalogFromFS(missingExit)
		if err == nil {
			t.Fatal("missing exit_behaviour: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "required field") && !strings.Contains(err.Error(), "exit_behaviour") {
			t.Errorf("missing exit_behaviour: error %q should mention the field", err.Error())
		}
	})
}

// TestRuleHwdbPR2_02 verifies RULE-HWDB-PR2-02: chip_profile.inherits_driver
// MUST resolve to a known driver_profile.module.
func TestRuleHwdbPR2_02(t *testing.T) {
	t.Run("TestRuleHwdbPR2_02", func(t *testing.T) {
		// Valid chip referencing a known driver loads cleanly.
		fsys := buildValidCatalogFS(t, validDriverYAML("testdrv"), validChipYAML("testchip", "testdrv"))
		_, err := LoadCatalogFromFS(fsys)
		if err != nil {
			t.Fatalf("valid chip: unexpected error: %v", err)
		}

		// Chip referencing unknown driver must be rejected.
		fsys2 := buildValidCatalogFS(t, validDriverYAML("testdrv"), validChipYAML("badchip", "no-such-driver"))
		_, err = LoadCatalogFromFS(fsys2)
		if err == nil {
			t.Fatal("unknown driver ref: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no-such-driver") {
			t.Errorf("unknown driver ref: error %q should name the bad driver", err.Error())
		}
	})
}

// TestRuleHwdbPR2_03 verifies RULE-HWDB-PR2-03: the three-tier matcher refuses
// to resolve when a board's primary_controller.chip does not resolve.
// In PR 2a the board layer is a stub; we test via MigrateModuleProfileToECP
// which exercises the same resolution chain.
func TestRuleHwdbPR2_03(t *testing.T) {
	t.Run("TestRuleHwdbPR2_03", func(t *testing.T) {
		cat := mustLoadEmbeddedCatalog(t)

		// Chip name "nct6798" must resolve via the chip catalog.
		ecp, err := MigrateModuleProfileToECP(cat, "nct6798", slog.Default())
		if err != nil {
			t.Fatalf("nct6798 should resolve: %v", err)
		}
		if ecp.ChipName != "nct6798" {
			t.Errorf("want ChipName=nct6798, got %q", ecp.ChipName)
		}

		// Unknown chip name must fail.
		_, err = MigrateModuleProfileToECP(cat, "definitely-not-a-real-chip-zzz", slog.Default())
		if err == nil {
			t.Fatal("unknown chip: expected ErrNoMatch, got nil")
		}
	})
}

// TestRuleHwdbPR2_04 verifies RULE-HWDB-PR2-04: pwm_unit_max MUST be set when
// pwm_unit is step_0_N or cooling_level.
func TestRuleHwdbPR2_04(t *testing.T) {
	t.Run("TestRuleHwdbPR2_04", func(t *testing.T) {
		// step_0_N without pwm_unit_max must be rejected.
		stepNoMax := buildValidCatalogFS(t, stepDriverYAML("stepdrv", false), "")
		_, err := LoadCatalogFromFS(stepNoMax)
		if err == nil {
			t.Fatal("step_0_N without pwm_unit_max: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "pwm_unit_max") {
			t.Errorf("step driver error %q should mention pwm_unit_max", err.Error())
		}

		// step_0_N WITH pwm_unit_max set must load cleanly.
		stepWithMax := buildValidCatalogFS(t, stepDriverYAML("stepdrv", true), "")
		_, err = LoadCatalogFromFS(stepWithMax)
		if err != nil {
			t.Fatalf("step_0_N with pwm_unit_max: unexpected error: %v", err)
		}

		// duty_0_255 without pwm_unit_max must succeed (max not required).
		dutyNoMax := buildValidCatalogFS(t, validDriverYAML("dutydrv"), "")
		_, err = LoadCatalogFromFS(dutyNoMax)
		if err != nil {
			t.Fatalf("duty_0_255 without pwm_unit_max: unexpected error: %v", err)
		}
	})
}

// TestRuleHwdbPR2_05 verifies RULE-HWDB-PR2-05: pwm_enable_modes MUST contain
// a "manual" entry when capability is rw_full, rw_quirk, or rw_step.
func TestRuleHwdbPR2_05(t *testing.T) {
	t.Run("TestRuleHwdbPR2_05", func(t *testing.T) {
		// rw_full driver without manual mode must be rejected.
		noManual := buildValidCatalogFS(t, rwDriverNoManualYAML("rwdrv"), "")
		_, err := LoadCatalogFromFS(noManual)
		if err == nil {
			t.Fatal("rw_full without manual: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "manual") {
			t.Errorf("no-manual error %q should mention \"manual\"", err.Error())
		}

		// ro_design driver without manual mode must be accepted.
		roDriver := buildValidCatalogFS(t, roDriverYAML("rodrv"), "")
		_, err = LoadCatalogFromFS(roDriver)
		if err != nil {
			t.Fatalf("ro_design without manual: unexpected error: %v", err)
		}
	})
}

// TestRuleHwdbPR2_06 verifies RULE-HWDB-PR2-06: recommended_alternative_driver
// MUST be non-null when capability == ro_pending_oot.
func TestRuleHwdbPR2_06(t *testing.T) {
	t.Run("TestRuleHwdbPR2_06", func(t *testing.T) {
		// ro_pending_oot with null alternative must be rejected.
		noAlt := buildValidCatalogFS(t, roPendingOOTNoAltYAML("oot-no-alt"), "")
		_, err := LoadCatalogFromFS(noAlt)
		if err == nil {
			t.Fatal("ro_pending_oot without alternative: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "recommended_alternative_driver") {
			t.Errorf("no-alt error %q should mention recommended_alternative_driver", err.Error())
		}

		// ro_pending_oot with a valid alternative must load cleanly.
		withAlt := buildValidCatalogFS(t, roPendingOOTWithAltYAML("oot-with-alt"), "")
		_, err = LoadCatalogFromFS(withAlt)
		if err != nil {
			t.Fatalf("ro_pending_oot with alternative: unexpected error: %v", err)
		}
	})
}

// TestRuleHwdbPR2_07 verifies RULE-HWDB-PR2-07: fan_control_capable:false
// profiles must not be calibrated.
func TestRuleHwdbPR2_07(t *testing.T) {
	t.Run("TestRuleHwdbPR2_07", func(t *testing.T) {
		// fan_control_capable:false → ShouldCalibrate returns false.
		ecpNoControl := &EffectiveControllerProfile{FanControlCapable: false}
		if ShouldCalibrate(ecpNoControl) {
			t.Error("fan_control_capable:false: ShouldCalibrate should return false")
		}

		// fan_control_capable:true → ShouldCalibrate returns true.
		ecpControl := &EffectiveControllerProfile{FanControlCapable: true}
		if !ShouldCalibrate(ecpControl) {
			t.Error("fan_control_capable:true: ShouldCalibrate should return true")
		}
	})
}

// TestRuleHwdbPR2_08 verifies RULE-HWDB-PR2-08: bios_overridden:true causes
// ShouldApplyCurve to refuse curve writes. The apply-path controller test
// (TestWriteWithRetry_RefusesBIOSOverridden) verifies the full wiring.
func TestRuleHwdbPR2_08(t *testing.T) {
	t.Run("TestRuleHwdbPR2_08", func(t *testing.T) {
		// nil → permit (pre-calibration channels).
		ok, err := ShouldApplyCurve(nil)
		if !ok || err != nil {
			t.Errorf("nil cal: ShouldApplyCurve should return (true, nil), got (%v, %v)", ok, err)
		}

		// bios_overridden:true → refuse.
		calOverridden := &ChannelCalibration{
			HwmonName:      "nct6798",
			ChannelIndex:   1,
			BIOSOverridden: true,
		}
		ok, err = ShouldApplyCurve(calOverridden)
		if ok {
			t.Error("bios_overridden:true: ShouldApplyCurve should return false")
		}
		if err == nil {
			t.Error("bios_overridden:true: ShouldApplyCurve should return non-nil error")
		}

		// bios_overridden:false → permit.
		calOK := &ChannelCalibration{
			HwmonName:    "nct6798",
			ChannelIndex: 2,
		}
		ok, err = ShouldApplyCurve(calOK)
		if !ok {
			t.Error("bios_overridden:false: ShouldApplyCurve should return true")
		}
		if err != nil {
			t.Errorf("bios_overridden:false: ShouldApplyCurve returned unexpected error: %v", err)
		}
	})
}

// TestRuleHwdbPR2_09 verifies RULE-HWDB-PR2-09: BIOS version mismatch triggers
// recalibration.
func TestRuleHwdbPR2_09(t *testing.T) {
	t.Run("TestRuleHwdbPR2_09", func(t *testing.T) {
		run := &CalibrationRun{
			DMIFingerprint: "asus-z790-a",
			BIOSVersion:    "ASUS 0805",
		}

		// nil → always needs recalibration.
		if !NeedsRecalibration(nil, "ASUS 0805") {
			t.Error("nil run: NeedsRecalibration should return true")
		}

		// Mismatch → needs recalibration.
		if !NeedsRecalibration(run, "ASUS 1001") {
			t.Error("BIOS version mismatch: NeedsRecalibration should return true")
		}

		// Match → no recalibration needed.
		if NeedsRecalibration(run, "ASUS 0805") {
			t.Error("BIOS version match: NeedsRecalibration should return false")
		}
	})
}

// TestRuleHwdbPR2_11 verifies RULE-HWDB-PR2-11: PR 1 → PR 2 migration via
// MigrateModuleProfileToECP resolves chip names and driver modules, logging
// a warning for the driver-module fallback path.
func TestRuleHwdbPR2_11(t *testing.T) {
	t.Run("TestRuleHwdbPR2_11", func(t *testing.T) {
		cat := mustLoadEmbeddedCatalog(t)

		// Path 1: PR 1 string matches a chip name — resolves cleanly.
		ecp, err := MigrateModuleProfileToECP(cat, "nct6798", slog.Default())
		if err != nil {
			t.Fatalf("chip-name path: unexpected error: %v", err)
		}
		if ecp.ChipName != "nct6798" {
			t.Errorf("chip-name path: want ChipName=nct6798, got %q", ecp.ChipName)
		}
		if ecp.Module != "nct6775" {
			t.Errorf("chip-name path: want Module=nct6775, got %q", ecp.Module)
		}
		for _, w := range ecp.Diagnostics.Warnings {
			if strings.Contains(w, "migration fallback") {
				t.Errorf("chip-name path should not produce migration warning, got: %q", w)
			}
		}

		// Path 2: PR 1 string matches only a driver module (not a chip name) —
		// resolves with a logged warning. "it87" is a driver module; its chips are
		// "it8603", "it8620" etc. — "it87" itself is not a chip name in the catalog.
		var warned bool
		warnHandler := slogWarnCapture(&warned)
		ecp2, err := MigrateModuleProfileToECP(cat, "it87", slog.New(warnHandler))
		if err != nil {
			t.Fatalf("driver-module path: unexpected error: %v", err)
		}
		if ecp2.Module != "it87" {
			t.Errorf("driver-module path: want Module=it87, got %q", ecp2.Module)
		}
		if len(ecp2.Diagnostics.Warnings) == 0 {
			t.Error("driver-module path: expected diagnostic warning, got none")
		}
	})
}

// TestRuleHwdbPR2_12 verifies RULE-HWDB-PR2-12: LoadCatalogFromFS refuses
// an invalid profile and loads a valid one cleanly.
func TestRuleHwdbPR2_12(t *testing.T) {
	t.Run("TestRuleHwdbPR2_12", func(t *testing.T) {
		// Invalid profile (missing capability) must fail.
		invalid := buildValidCatalogFS(t, missingCapabilityDriverYAML(), "")
		_, err := LoadCatalogFromFS(invalid)
		if err == nil {
			t.Fatal("invalid catalog: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "required field") {
			t.Errorf("invalid catalog error %q should mention \"required field\"", err.Error())
		}

		// Valid catalog must load cleanly.
		valid := buildValidCatalogFS(t, validDriverYAML("valid12"), "")
		_, err = LoadCatalogFromFS(valid)
		if err != nil {
			t.Fatalf("valid catalog: unexpected error: %v", err)
		}
	})
}

// TestRuleHwdbPR2_13 verifies RULE-HWDB-PR2-13: every driver_profile MUST
// declare exit_behaviour from the known enum; missing/unknown value is rejected.
func TestRuleHwdbPR2_13(t *testing.T) {
	t.Run("TestRuleHwdbPR2_13", func(t *testing.T) {
		// All valid enum values load cleanly.
		for _, v := range []string{"force_max", "restore_auto", "preserve", "bios_dependent"} {
			fsys := buildValidCatalogFS(t, driverWithExitBehaviourYAML("drv13-"+v, v), "")
			_, err := LoadCatalogFromFS(fsys)
			if err != nil {
				t.Errorf("exit_behaviour=%q: unexpected error: %v", v, err)
			}
		}

		// Unknown value must be rejected.
		fsys := buildValidCatalogFS(t, driverWithExitBehaviourYAML("drv13-bad", "unknown_mode"), "")
		_, err := LoadCatalogFromFS(fsys)
		if err == nil {
			t.Fatal("unknown exit_behaviour: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "exit_behaviour") {
			t.Errorf("unknown exit_behaviour error %q should mention the field", err.Error())
		}
	})
}

// TestRuleHwdbPR2_14 verifies RULE-HWDB-PR2-14: runtime_conflict_detection_supported
// MUST be an explicit boolean; absent field is rejected.
func TestRuleHwdbPR2_14(t *testing.T) {
	t.Run("TestRuleHwdbPR2_14", func(t *testing.T) {
		// Explicit false must load cleanly.
		explicitFalse := buildValidCatalogFS(t, driverWithRCDSYAML("drv14-false", "false"), "")
		_, err := LoadCatalogFromFS(explicitFalse)
		if err != nil {
			t.Fatalf("explicit false: unexpected error: %v", err)
		}

		// Explicit true must load cleanly.
		explicitTrue := buildValidCatalogFS(t, driverWithRCDSYAML("drv14-true", "true"), "")
		_, err = LoadCatalogFromFS(explicitTrue)
		if err != nil {
			t.Fatalf("explicit true: unexpected error: %v", err)
		}

		// Absent field (omitted from YAML) must be rejected.
		absent := buildValidCatalogFS(t, driverMissingRCDSYAML("drv14-absent"), "")
		_, err = LoadCatalogFromFS(absent)
		if err == nil {
			t.Fatal("absent runtime_conflict_detection_supported: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "runtime_conflict_detection_supported") {
			t.Errorf("absent RCDS error %q should name the field", err.Error())
		}
	})
}

// TestHWDB_GPUEntriesV1Compatible verifies RULE-GPU-PR2D-04: all GPU driver YAML
// entries validate against the existing schema v1.0 with no new fields.
func TestHWDB_GPUEntriesV1Compatible(t *testing.T) {
	cat := mustLoadEmbeddedCatalog(t)

	gpuModules := []string{"nvidia", "amdgpu", "amdgpu_rdna3", "i915", "xe", "nouveau", "radeon"}
	for _, mod := range gpuModules {
		dp, ok := cat.Drivers[mod]
		if !ok {
			t.Errorf("GPU driver %q not found in embedded catalog", mod)
			continue
		}
		if dp == nil {
			t.Errorf("GPU driver %q has nil profile", mod)
		}
	}

	// Chip profiles for GPU hwmon names must be present.
	gpuChips := []string{"amdgpu", "nouveau", "i915", "xe", "radeon"}
	for _, chip := range gpuChips {
		if _, ok := cat.Chips[chip]; !ok {
			t.Errorf("GPU chip profile %q not found in embedded catalog", chip)
		}
	}
}

// --- helpers ---

func mustLoadEmbeddedCatalog(t *testing.T) *Catalog {
	t.Helper()
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog (embedded): %v", err)
	}
	return cat
}

// buildValidCatalogFS returns an fstest.MapFS with optional driver and chip YAML
// content. Passing an empty string for either skips that file.
func buildValidCatalogFS(t *testing.T, driverYAML, chipYAML string) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{}
	if driverYAML != "" {
		fsys["drivers/test.yaml"] = &fstest.MapFile{Data: []byte(driverYAML)}
	}
	if chipYAML != "" {
		fsys["chips/test.yaml"] = &fstest.MapFile{Data: []byte(chipYAML)}
	}
	return fsys
}

// validDriverYAML returns a complete, valid driver YAML for the given module name.
func validDriverYAML(module string) string {
	return `schema_version: "1.0"
driver_profiles:
  - module: "` + module + `"
    family: "test-family"
    description: "test driver for unit tests"
    capability: "rw_full"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: true
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "static_normal"
    exit_behaviour: "force_max"
    runtime_conflict_detection_supported: true
    firmware_curve_offload_override: null
    citations: []
`
}

func validChipYAML(name, driver string) string {
	return `schema_version: "1.0"
chip_profiles:
  - name: "` + name + `"
    inherits_driver: "` + driver + `"
    description: "test chip"
    overrides: {}
    channel_overrides: {}
    citations: []
`
}

func missingCapabilityDriverYAML() string {
	return `schema_version: "1.0"
driver_profiles:
  - module: "missing-cap"
    family: "test-family"
    description: "driver with missing capability"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: true
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "static_normal"
    exit_behaviour: "force_max"
    runtime_conflict_detection_supported: true
    firmware_curve_offload_override: null
    citations: []
`
}

func missingExitBehaviourDriverYAML() string {
	return `schema_version: "1.0"
driver_profiles:
  - module: "missing-exit"
    family: "test-family"
    description: "driver with missing exit_behaviour"
    capability: "rw_full"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: true
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "static_normal"
    runtime_conflict_detection_supported: true
    firmware_curve_offload_override: null
    citations: []
`
}

func stepDriverYAML(module string, withMax bool) string {
	max := "null"
	if withMax {
		max = "3"
	}
	return `schema_version: "1.0"
driver_profiles:
  - module: "` + module + `"
    family: "test-step"
    description: "step-based test driver"
    capability: "rw_step"
    pwm_unit: "step_0_N"
    pwm_unit_max: ` + max + `
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "state_off"
    polling_latency_ms_hint: 100
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: true
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "not_applicable"
    exit_behaviour: "restore_auto"
    runtime_conflict_detection_supported: true
    firmware_curve_offload_override: false
    citations: []
`
}

func rwDriverNoManualYAML(module string) string {
	return `schema_version: "1.0"
driver_profiles:
  - module: "` + module + `"
    family: "test-rw"
    description: "rw driver without manual mode"
    capability: "rw_full"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "2": "bios_auto"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: true
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "static_normal"
    exit_behaviour: "force_max"
    runtime_conflict_detection_supported: true
    firmware_curve_offload_override: null
    citations: []
`
}

func roDriverYAML(module string) string {
	return `schema_version: "1.0"
driver_profiles:
  - module: "` + module + `"
    family: "test-ro"
    description: "read-only driver"
    capability: "ro_design"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes: {}
    off_behaviour: "bios_dependent"
    polling_latency_ms_hint: 100
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: false
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "not_applicable"
    exit_behaviour: "bios_dependent"
    runtime_conflict_detection_supported: false
    firmware_curve_offload_override: false
    citations: []
`
}

func roPendingOOTNoAltYAML(module string) string {
	return `schema_version: "1.0"
driver_profiles:
  - module: "` + module + `"
    family: "test-oot"
    description: "oot driver without alternative set"
    capability: "ro_pending_oot"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: false
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "probe_required"
    exit_behaviour: "force_max"
    runtime_conflict_detection_supported: true
    firmware_curve_offload_override: null
    citations: []
`
}

func roPendingOOTWithAltYAML(module string) string {
	return `schema_version: "1.0"
driver_profiles:
  - module: "` + module + `"
    family: "test-oot"
    description: "oot driver with alternative"
    capability: "ro_pending_oot"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver:
      module: "alt-mod"
      source: "github.com/example/alt-mod"
      install_method: "dkms"
      package_hint: null
      reason: "test alternative driver"
      applies_to_boards: []
      module_args_hint: []
    conflicts_with_userspace: []
    fan_control_capable: false
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "probe_required"
    exit_behaviour: "force_max"
    runtime_conflict_detection_supported: true
    firmware_curve_offload_override: null
    citations: []
`
}

func driverWithExitBehaviourYAML(module, exitBehaviour string) string {
	return `schema_version: "1.0"
driver_profiles:
  - module: "` + module + `"
    family: "test-exit"
    description: "driver with specific exit_behaviour"
    capability: "rw_full"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: true
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "static_normal"
    exit_behaviour: "` + exitBehaviour + `"
    runtime_conflict_detection_supported: true
    firmware_curve_offload_override: null
    citations: []
`
}

func driverWithRCDSYAML(module, rcds string) string {
	return `schema_version: "1.0"
driver_profiles:
  - module: "` + module + `"
    family: "test-rcds"
    description: "driver with explicit runtime_conflict_detection_supported"
    capability: "rw_full"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: true
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "static_normal"
    exit_behaviour: "force_max"
    runtime_conflict_detection_supported: ` + rcds + `
    firmware_curve_offload_override: null
    citations: []
`
}

func driverMissingRCDSYAML(module string) string {
	// Deliberately omit runtime_conflict_detection_supported.
	return `schema_version: "1.0"
driver_profiles:
  - module: "` + module + `"
    family: "test-rcds-absent"
    description: "driver with absent runtime_conflict_detection_supported"
    capability: "rw_full"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: true
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "static_normal"
    exit_behaviour: "force_max"
    firmware_curve_offload_override: null
    citations: []
`
}

// slogWarnCapture returns an slog.Handler that sets *warned when a Warn message
// is received.
func slogWarnCapture(warned *bool) slog.Handler {
	return &warnCapHandler{warned: warned}
}

type warnCapHandler struct {
	warned *bool
}

func (h *warnCapHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *warnCapHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		*h.warned = true
	}
	return nil
}
func (h *warnCapHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *warnCapHandler) WithGroup(_ string) slog.Handler      { return h }

// driverWithKernelVersionYAML returns a complete driver YAML with a
// kernel_version block — used by RULE-HWDB-PR2-17.
func driverWithKernelVersionYAML(module, min, max string) string {
	kv := "kernel_version:\n"
	if min != "" {
		kv += "      min: \"" + min + "\"\n"
	}
	if max != "" {
		kv += "      max: \"" + max + "\"\n"
	}
	return `schema_version: "1.3"
driver_profiles:
  - module: "` + module + `"
    family: "test-kver"
    description: "driver with kernel_version range"
    capability: "rw_full"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: true
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "static_normal"
    exit_behaviour: "force_max"
    runtime_conflict_detection_supported: true
    firmware_curve_offload_override: null
    citations: []
    ` + kv
}

// driverWithBlacklistYAML returns a driver YAML with a blacklist_before_install.
func driverWithBlacklistYAML(module string, modules []string) string {
	bl := "blacklist_before_install:\n"
	for _, m := range modules {
		bl += "      - \"" + m + "\"\n"
	}
	return `schema_version: "1.3"
driver_profiles:
  - module: "` + module + `"
    family: "test-blacklist"
    description: "driver with blacklist_before_install"
    capability: "rw_full"
    pwm_unit: "duty_0_255"
    pwm_unit_max: null
    pwm_enable_modes:
      "1": "manual"
    off_behaviour: "stops"
    polling_latency_ms_hint: 50
    recommended_alternative_driver: null
    conflicts_with_userspace: []
    fan_control_capable: true
    fan_control_via: null
    required_modprobe_args: []
    pwm_polarity_reservation: "static_normal"
    exit_behaviour: "force_max"
    runtime_conflict_detection_supported: true
    firmware_curve_offload_override: null
    citations: []
    ` + bl
}

// TestRuleHwdbPR2_15 verifies RULE-HWDB-PR2-15: pwm_groups validates
// that channel is non-empty, fans is non-empty, and fan ids are unique.
// Bound: see .claude/rules/hwdb-pr2-15.md
func TestRuleHwdbPR2_15(t *testing.T) {
	t.Run("TestRuleHwdbPR2_15", func(t *testing.T) {
		// Happy path: a board with two pwm_groups loads cleanly.
		bd := minBoardYAML("brd-pwmgrp-ok", "test-chip",
			`dmi_fingerprint: {sys_vendor: "X", product_name: "Y", board_vendor: "X", board_name: "Y", board_version: ""}`,
			"pwm_groups:\n      - {channel: pwm1, fans: [cpu_fan, sys_fan_1]}\n      - {channel: pwm5, fans: [gpu0]}")
		fsys := buildCatalogWithBoards(t, validDriverYAML("nct6798"), validChipYAML("test-chip", "nct6798"), bd)
		if _, err := LoadCatalogFromFS(fsys); err != nil {
			t.Fatalf("happy: %v", err)
		}
		// Empty channel rejected.
		bd = minBoardYAML("brd-pwmgrp-empty-channel", "test-chip",
			`dmi_fingerprint: {sys_vendor: "X", product_name: "Y", board_vendor: "X", board_name: "Y", board_version: ""}`,
			"pwm_groups:\n      - {channel: \"\", fans: [cpu_fan]}")
		fsys = buildCatalogWithBoards(t, validDriverYAML("nct6798"), validChipYAML("test-chip", "nct6798"), bd)
		if _, err := LoadCatalogFromFS(fsys); err == nil || !strings.Contains(err.Error(), "channel must be non-empty") {
			t.Errorf("empty channel: want validation error, got %v", err)
		}
		// Empty fans rejected.
		bd = minBoardYAML("brd-pwmgrp-empty-fans", "test-chip",
			`dmi_fingerprint: {sys_vendor: "X", product_name: "Y", board_vendor: "X", board_name: "Y", board_version: ""}`,
			"pwm_groups:\n      - {channel: pwm1, fans: []}")
		fsys = buildCatalogWithBoards(t, validDriverYAML("nct6798"), validChipYAML("test-chip", "nct6798"), bd)
		if _, err := LoadCatalogFromFS(fsys); err == nil || !strings.Contains(err.Error(), "must list at least one fan") {
			t.Errorf("empty fans: want validation error, got %v", err)
		}
		// Duplicate fan ids rejected.
		bd = minBoardYAML("brd-pwmgrp-dup", "test-chip",
			`dmi_fingerprint: {sys_vendor: "X", product_name: "Y", board_vendor: "X", board_name: "Y", board_version: ""}`,
			"pwm_groups:\n      - {channel: pwm1, fans: [cpu_fan, cpu_fan]}")
		fsys = buildCatalogWithBoards(t, validDriverYAML("nct6798"), validChipYAML("test-chip", "nct6798"), bd)
		if _, err := LoadCatalogFromFS(fsys); err == nil || !strings.Contains(err.Error(), "duplicate fan id") {
			t.Errorf("duplicate fan: want validation error, got %v", err)
		}
	})
}

// TestRuleHwdbPR2_16 verifies RULE-HWDB-PR2-16: blacklist_before_install
// rejects empty entries and duplicates; happy path loads cleanly.
// Bound: see .claude/rules/hwdb-pr2-16.md
func TestRuleHwdbPR2_16(t *testing.T) {
	t.Run("TestRuleHwdbPR2_16", func(t *testing.T) {
		// Happy: two distinct modules loads cleanly.
		fsys := buildValidCatalogFS(t, driverWithBlacklistYAML("drv16-ok", []string{"nct6683", "nct6675"}), "")
		if _, err := LoadCatalogFromFS(fsys); err != nil {
			t.Fatalf("happy: %v", err)
		}
		// Empty entry rejected.
		fsys = buildValidCatalogFS(t, driverWithBlacklistYAML("drv16-empty", []string{"nct6683", ""}), "")
		if _, err := LoadCatalogFromFS(fsys); err == nil || !strings.Contains(err.Error(), "empty module name") {
			t.Errorf("empty module: want validation error, got %v", err)
		}
		// Duplicate entries rejected.
		fsys = buildValidCatalogFS(t, driverWithBlacklistYAML("drv16-dup", []string{"nct6683", "nct6683"}), "")
		if _, err := LoadCatalogFromFS(fsys); err == nil || !strings.Contains(err.Error(), "duplicate module") {
			t.Errorf("duplicate: want validation error, got %v", err)
		}
	})
}

// TestRuleHwdbPR2_17 verifies RULE-HWDB-PR2-17: kernel_version range
// requires dotted-numeric strings and Min <= Max when both set.
// Bound: see .claude/rules/hwdb-pr2-17.md
func TestRuleHwdbPR2_17(t *testing.T) {
	t.Run("TestRuleHwdbPR2_17", func(t *testing.T) {
		// Happy: a valid range loads cleanly.
		fsys := buildValidCatalogFS(t, driverWithKernelVersionYAML("drv17-range", "6.2", "7.1"), "")
		if _, err := LoadCatalogFromFS(fsys); err != nil {
			t.Fatalf("happy range: %v", err)
		}
		// Min-only loads cleanly (Max omitted = open upper bound).
		fsys = buildValidCatalogFS(t, driverWithKernelVersionYAML("drv17-min-only", "6.13", ""), "")
		if _, err := LoadCatalogFromFS(fsys); err != nil {
			t.Fatalf("min-only: %v", err)
		}
		// Non-numeric min rejected.
		fsys = buildValidCatalogFS(t, driverWithKernelVersionYAML("drv17-bad-min", "abc", ""), "")
		if _, err := LoadCatalogFromFS(fsys); err == nil || !strings.Contains(err.Error(), "kernel_version.min") {
			t.Errorf("bad min: want validation error, got %v", err)
		}
		// Min > Max rejected.
		fsys = buildValidCatalogFS(t, driverWithKernelVersionYAML("drv17-inverted", "7.1", "6.2"), "")
		if _, err := LoadCatalogFromFS(fsys); err == nil || !strings.Contains(err.Error(), "exceeds kernel_version.max") {
			t.Errorf("inverted: want validation error, got %v", err)
		}
		// 6.10 > 6.9 (numeric, not lex) — validate compareDottedVersions.
		fsys = buildValidCatalogFS(t, driverWithKernelVersionYAML("drv17-numsort", "6.9", "6.10"), "")
		if _, err := LoadCatalogFromFS(fsys); err != nil {
			t.Errorf("6.9 should be < 6.10 (numeric): %v", err)
		}
	})
}
