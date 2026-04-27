package hwdb

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ventd/ventd/internal/hwdb/dtfake"
)

// resetLoggedBoards clears the unsupported-log once-per-lifetime state.
// Only for test isolation.
func resetLoggedBoardsForTest() {
	unsupportedLogged.Range(func(k, _ any) bool {
		unsupportedLogged.Delete(k)
		return true
	})
}

// buildCatalogWithBoards extends buildValidCatalogFS with a boards YAML.
func buildCatalogWithBoards(t *testing.T, driverYAML, chipYAML, boardYAML string) fstest.MapFS {
	t.Helper()
	fsys := buildValidCatalogFS(t, driverYAML, chipYAML)
	if boardYAML != "" {
		fsys["boards/test.yaml"] = &fstest.MapFile{Data: []byte(boardYAML)}
	}
	return fsys
}

// minBoardYAML returns a minimal valid board YAML with an injected fingerprint block.
func minBoardYAML(id, chip, fingerprint, overrides string) string {
	ov := "overrides: {}"
	if overrides != "" {
		ov = overrides
	}
	return `schema_version: "1.1"
board_profiles:
  - id: "` + id + `"
    ` + fingerprint + `
    primary_controller:
      chip: "` + chip + `"
    additional_controllers: []
    ` + ov + `
    required_modprobe_args: []
    conflicts_with_userspace: []
    citations: []
    contributed_by: "anonymous"
    captured_at: "2026-04-26"
    verified: false
`
}

// TestSchemaValidator_RejectsBothFingerprintTypes verifies RULE-SCHEMA-08:
// a board profile with both dmi_fingerprint and dt_fingerprint set is rejected.
func TestSchemaValidator_RejectsBothFingerprintTypes(t *testing.T) {
	both := `schema_version: "1.1"
board_profiles:
  - id: "dual-fingerprint-board"
    dmi_fingerprint:
      sys_vendor: "LENOVO"
      product_name: "82WS"
      board_vendor: "LENOVO"
      board_name: "*"
      board_version: "*"
    dt_fingerprint:
      compatible: "raspberrypi,5-model-b"
    primary_controller:
      chip: "nct6798"
    additional_controllers: []
    overrides: {}
    required_modprobe_args: []
    conflicts_with_userspace: []
    citations: []
    contributed_by: "anonymous"
    captured_at: "2026-04-26"
    verified: false
`
	fsys := fstest.MapFS{
		"test.yaml": &fstest.MapFile{Data: []byte(both)},
	}
	_, err := LoadBoardCatalogFromFS(fsys)
	if err == nil {
		t.Fatal("expected error for dual-fingerprint board, got nil")
	}
	if !strings.Contains(err.Error(), "exactly one is required") {
		t.Errorf("error %q should contain \"exactly one is required\"", err.Error())
	}
}

// TestMatcher_BiosVersionGlob_Matches verifies RULE-FINGERPRINT-04:
// dmi_fingerprint.bios_version glob matches live DMI bios_version.
func TestMatcher_BiosVersionGlob_Matches(t *testing.T) {
	boardYAML := minBoardYAML("lenovo-legion-5", "test-chip", `dmi_fingerprint:
      sys_vendor: "LENOVO"
      product_name: "82WS"
      board_vendor: "LENOVO"
      board_name: "*"
      board_version: "*"
      bios_version: "GKCN*"`, "")

	cat := mustLoadCatalogWithBoards(t, validDriverYAML("test-drv"), validChipYAML("test-chip", "test-drv"), boardYAML)

	liveDMI := DMIFingerprint{
		SysVendor:   "LENOVO",
		ProductName: "82WS",
		BoardVendor: "LENOVO",
		BoardName:   "Legion 5 Pro 16ARH7H",
		BiosVersion: "GKCN58WW",
	}
	ecp, err := MatchV1WithCalibration(cat, "test-chip", liveDMI, nil, slog.Default())
	if err != nil {
		t.Fatalf("MatchV1WithCalibration: %v", err)
	}
	if ecp.Diagnostics.Tier != MatchTierBoard {
		t.Errorf("Tier = %v, want MatchTierBoard", ecp.Diagnostics.Tier)
	}
	if ecp.Diagnostics.MatchedBoardID != "lenovo-legion-5" {
		t.Errorf("MatchedBoardID = %q, want \"lenovo-legion-5\"", ecp.Diagnostics.MatchedBoardID)
	}
	if ecp.Diagnostics.Confidence < 0.9 {
		t.Errorf("Confidence = %.2f, want >= 0.9", ecp.Diagnostics.Confidence)
	}
}

// TestMatcher_BiosVersionAbsent_BehavesAsV1 verifies RULE-FINGERPRINT-05:
// a dmi_fingerprint without bios_version behaves as v1 (matches any bios_version).
func TestMatcher_BiosVersionAbsent_BehavesAsV1(t *testing.T) {
	boardYAML := minBoardYAML("lenovo-v1-compat", "test-chip", `dmi_fingerprint:
      sys_vendor: "LENOVO"
      product_name: "82WS"
      board_vendor: "LENOVO"
      board_name: "*"
      board_version: "*"`, "")

	cat := mustLoadCatalogWithBoards(t, validDriverYAML("test-drv"), validChipYAML("test-chip", "test-drv"), boardYAML)

	for _, biosVer := range []string{"GKCN58WW", "EUCN32WW", "H1CN45WW", ""} {
		liveDMI := DMIFingerprint{
			SysVendor:   "LENOVO",
			ProductName: "82WS",
			BoardVendor: "LENOVO",
			BiosVersion: biosVer,
		}
		ecp, err := MatchV1WithCalibration(cat, "test-chip", liveDMI, nil, slog.Default())
		if err != nil {
			t.Fatalf("BiosVersion=%q: MatchV1WithCalibration: %v", biosVer, err)
		}
		if ecp.Diagnostics.Tier != MatchTierBoard {
			t.Errorf("BiosVersion=%q: Tier = %v, want MatchTierBoard", biosVer, ecp.Diagnostics.Tier)
		}
	}
}

// TestMatcher_DTCompatibleGlob_Matches verifies RULE-FINGERPRINT-06:
// dt_fingerprint.compatible glob matches live device-tree compatible list.
func TestMatcher_DTCompatibleGlob_Matches(t *testing.T) {
	boardYAML := minBoardYAML("rpi5-dt", "test-chip", `dt_fingerprint:
      compatible: "raspberrypi,5-model-b"`, "")

	cat := mustLoadCatalogWithBoards(t, validDriverYAML("test-drv"), validChipYAML("test-chip", "test-drv"), boardYAML)

	livedt := dtfake.New().
		SetCompatible("brcm,bcm2712", "raspberrypi,5-model-b").
		SetModel("Raspberry Pi 5 Model B Rev 1.0").
		FS()

	dtData := ReadDTData(livedt)
	ecp, err := MatchV1WithDT(cat, "test-chip", DMIFingerprint{}, dtData, false, nil, slog.Default())
	if err != nil {
		t.Fatalf("MatchV1WithDT: %v", err)
	}
	if ecp.Diagnostics.Tier != MatchTierBoard {
		t.Errorf("Tier = %v, want MatchTierBoard", ecp.Diagnostics.Tier)
	}
	if ecp.Diagnostics.MatchedBoardID != "rpi5-dt" {
		t.Errorf("MatchedBoardID = %q, want \"rpi5-dt\"", ecp.Diagnostics.MatchedBoardID)
	}
}

// TestMatcher_DTModelGlob_Matches verifies RULE-FINGERPRINT-07:
// dt_fingerprint.model glob matches live device-tree model string.
func TestMatcher_DTModelGlob_Matches(t *testing.T) {
	boardYAML := minBoardYAML("rpi5-model-glob", "test-chip", `dt_fingerprint:
      model: "Raspberry Pi 5*"`, "")

	cat := mustLoadCatalogWithBoards(t, validDriverYAML("test-drv"), validChipYAML("test-chip", "test-drv"), boardYAML)

	livedt := dtfake.New().
		SetModel("Raspberry Pi 5 Model B Rev 1.0").
		FS()

	dtData := ReadDTData(livedt)
	ecp, err := MatchV1WithDT(cat, "test-chip", DMIFingerprint{}, dtData, false, nil, slog.Default())
	if err != nil {
		t.Fatalf("MatchV1WithDT: %v", err)
	}
	if ecp.Diagnostics.Tier != MatchTierBoard {
		t.Errorf("Tier = %v, want MatchTierBoard", ecp.Diagnostics.Tier)
	}
	if ecp.Diagnostics.MatchedBoardID != "rpi5-model-glob" {
		t.Errorf("MatchedBoardID = %q, want \"rpi5-model-glob\"", ecp.Diagnostics.MatchedBoardID)
	}
}

// TestMatcher_UnsupportedEmitsLogOnce verifies RULE-OVERRIDE-UNSUPPORTED-01:
// a board with overrides.unsupported=true emits exactly one INFO log per process
// lifetime (identified by board_id), regardless of how many times it is matched.
func TestMatcher_UnsupportedEmitsLogOnce(t *testing.T) {
	resetLoggedBoardsForTest()
	t.Cleanup(resetLoggedBoardsForTest)

	boardYAML := minBoardYAML("hp-pavilion-unsupported", "test-chip", `dmi_fingerprint:
      sys_vendor: "HP"
      product_name: "Pavilion 15"
      board_vendor: "HP"
      board_name: "*"
      board_version: "*"`, `overrides:
      unsupported: true`)

	cat := mustLoadCatalogWithBoards(t, validDriverYAML("test-drv"), validChipYAML("test-chip", "test-drv"), boardYAML)

	liveDMI := DMIFingerprint{
		SysVendor:   "HP",
		ProductName: "Pavilion 15",
		BoardVendor: "HP",
	}

	logCount := 0
	log := slog.New(&countInfoHandler{count: &logCount})

	// First match: INFO log must fire.
	ecp, err := MatchV1WithDT(cat, "test-chip", liveDMI, LiveDTData{}, true, nil, log)
	if err != nil {
		t.Fatalf("first match: %v", err)
	}
	if !ecp.Unsupported {
		t.Error("first match: Unsupported should be true")
	}
	if logCount != 1 {
		t.Errorf("first match: INFO log count = %d, want 1", logCount)
	}

	// Second match: INFO log must NOT fire again.
	_, err = MatchV1WithDT(cat, "test-chip", liveDMI, LiveDTData{}, true, nil, log)
	if err != nil {
		t.Fatalf("second match: %v", err)
	}
	if logCount != 1 {
		t.Errorf("after second match: INFO log count = %d, want still 1", logCount)
	}
}

type countInfoHandler struct{ count *int }

func (h *countInfoHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *countInfoHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelInfo && strings.Contains(r.Message, "no Linux fan-control driver") {
		*h.count++
	}
	return nil
}
func (h *countInfoHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *countInfoHandler) WithGroup(_ string) slog.Handler      { return h }

// TestCalibration_UnsupportedSkipsAutocurve verifies RULE-OVERRIDE-UNSUPPORTED-02:
// ShouldSkipCalibration returns true when the ECP has Unsupported=true, and false
// when Unsupported=false.
func TestCalibration_UnsupportedSkipsAutocurve(t *testing.T) {
	t.Run("unsupported_true_skips", func(t *testing.T) {
		ecp := &EffectiveControllerProfile{Unsupported: true}
		if !ShouldSkipCalibration(ecp) {
			t.Error("ShouldSkipCalibration returned false for Unsupported=true")
		}
	})
	t.Run("unsupported_false_does_not_skip", func(t *testing.T) {
		ecp := &EffectiveControllerProfile{Unsupported: false}
		if ShouldSkipCalibration(ecp) {
			t.Error("ShouldSkipCalibration returned true for Unsupported=false")
		}
	})
}

// mustLoadCatalogWithBoards loads a synthetic catalog from in-process YAML strings.
func mustLoadCatalogWithBoards(t *testing.T, driverYAML, chipYAML, boardYAML string) *Catalog {
	t.Helper()
	fsys := buildCatalogWithBoards(t, driverYAML, chipYAML, boardYAML)
	cat, err := LoadCatalogFromFS(fsys)
	if err != nil {
		t.Fatalf("LoadCatalogFromFS: %v", err)
	}
	return cat
}

// experimentalDriverYAML builds a complete driver YAML with an injected experimental block.
func experimentalDriverYAML(expBlock string) string {
	return `schema_version: "1.2"
driver_profiles:
  - module: "test-drv"
    family: "test"
    description: "test driver"
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
` + expBlock
}

// countWarnHandler counts slog WARN records containing a target substring.
type countWarnHandler struct {
	target string
	count  *int
}

func (h *countWarnHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *countWarnHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn && strings.Contains(r.Message, h.target) {
		*h.count++
	}
	return nil
}
func (h *countWarnHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *countWarnHandler) WithGroup(_ string) slog.Handler      { return h }

// TestSchemaValidator_ExperimentalBlock_AcceptsRecognizedKeys verifies RULE-EXPERIMENTAL-SCHEMA-01:
// a recognized experimental key with a bool value is accepted and parsed into ExperimentalBlock.
func TestSchemaValidator_ExperimentalBlock_AcceptsRecognizedKeys(t *testing.T) {
	drvYAML := experimentalDriverYAML("    experimental:\n      amd_overdrive: true\n")
	chipYAML := validChipYAML("test-chip", "test-drv")
	fsys := buildValidCatalogFS(t, drvYAML, chipYAML)
	cat, err := LoadCatalogFromFS(fsys)
	if err != nil {
		t.Fatalf("LoadCatalogFromFS: %v", err)
	}
	dp, ok := cat.Drivers["test-drv"]
	if !ok {
		t.Fatal("driver test-drv not found")
	}
	if !dp.Experimental.AMDOverdrive {
		t.Error("AMDOverdrive should be true")
	}
	if dp.Experimental.ILO4Unlocked || dp.Experimental.NvidiaCoolbits || dp.Experimental.IDRAC9LegacyRaw {
		t.Error("other experimental fields should be false")
	}
}

// TestSchemaValidator_ExperimentalBlock_RejectsNonBoolValue verifies RULE-EXPERIMENTAL-SCHEMA-02:
// a recognized experimental key with a non-bool value causes a load error.
func TestSchemaValidator_ExperimentalBlock_RejectsNonBoolValue(t *testing.T) {
	drvYAML := experimentalDriverYAML("    experimental:\n      amd_overdrive: \"yes\"\n")
	chipYAML := validChipYAML("test-chip", "test-drv")
	fsys := buildValidCatalogFS(t, drvYAML, chipYAML)
	_, err := LoadCatalogFromFS(fsys)
	if err == nil {
		t.Fatal("expected error for non-bool experimental value, got nil")
	}
	if !strings.Contains(err.Error(), "expected bool") {
		t.Errorf("error %q should contain \"expected bool\"", err.Error())
	}
}

// TestSchemaValidator_ExperimentalBlock_RejectsTypoWithSuggestion verifies RULE-EXPERIMENTAL-SCHEMA-03:
// an unknown experimental key with Levenshtein distance ≤ 2 is rejected with a "Did you mean:" hint.
func TestSchemaValidator_ExperimentalBlock_RejectsTypoWithSuggestion(t *testing.T) {
	// "amd_overdiv" is distance 2 from "amd_overdrive" (two missing chars: 'r' and 'e').
	drvYAML := experimentalDriverYAML("    experimental:\n      amd_overdiv: true\n")
	chipYAML := validChipYAML("test-chip", "test-drv")
	fsys := buildValidCatalogFS(t, drvYAML, chipYAML)
	_, err := LoadCatalogFromFS(fsys)
	if err == nil {
		t.Fatal("expected error for near-typo experimental key, got nil")
	}
	if !strings.Contains(err.Error(), "Did you mean:") {
		t.Errorf("error %q should contain \"Did you mean:\"", err.Error())
	}
}

// TestSchemaValidator_ExperimentalBlock_WarnsUnknownKeyOnce verifies RULE-EXPERIMENTAL-SCHEMA-04:
// an unrecognized key with Levenshtein distance > 2 emits exactly one WARN per process lifetime.
func TestSchemaValidator_ExperimentalBlock_WarnsUnknownKeyOnce(t *testing.T) {
	resetWarnedExperimentalKeysForTest()
	t.Cleanup(func() { resetWarnedExperimentalKeysForTest() })

	var warnCount int
	log := slog.New(&countWarnHandler{target: "unknown key ignored", count: &warnCount})

	// "future_feature_xyz" is far from every known key (distance >> 2).
	raw := map[string]any{"future_feature_xyz": true}
	for i := 0; i < 2; i++ {
		if _, err := validateExperimental(raw, log); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
	}
	if warnCount != 1 {
		t.Errorf("WARN count = %d, want exactly 1", warnCount)
	}
}

// TestSchemaValidator_ExperimentalBlockAbsent_BehavesAsV1_1 verifies RULE-EXPERIMENTAL-SCHEMA-05:
// a profile without an experimental block has all-false ExperimentalBlock (v1.1 behavior preserved).
func TestSchemaValidator_ExperimentalBlockAbsent_BehavesAsV1_1(t *testing.T) {
	fsys := buildValidCatalogFS(t, validDriverYAML("test-drv"), validChipYAML("test-chip", "test-drv"))
	cat, err := LoadCatalogFromFS(fsys)
	if err != nil {
		t.Fatalf("LoadCatalogFromFS: %v", err)
	}
	dp, ok := cat.Drivers["test-drv"]
	if !ok {
		t.Fatal("driver test-drv not found")
	}
	if dp.Experimental != (ExperimentalBlock{}) {
		t.Errorf("Experimental should be zero value, got %+v", dp.Experimental)
	}
}

// TestMatcher_ExperimentalEligibility_OrsBoardAndGPU verifies RULE-EXPERIMENTAL-MERGE-01:
// CatalogMatch.ExperimentalEligibility OR-merges flags from board and driver profiles.
func TestMatcher_ExperimentalEligibility_OrsBoardAndGPU(t *testing.T) {
	boardEntry := &BoardCatalogEntry{
		Experimental: ExperimentalBlock{ILO4Unlocked: true},
	}
	driverProfile := &DriverProfile{
		Experimental: ExperimentalBlock{NvidiaCoolbits: true},
	}
	m := &CatalogMatch{Board: boardEntry, Driver: driverProfile}
	got := m.ExperimentalEligibility()
	if !got.ILO4Unlocked {
		t.Error("ILO4Unlocked should be true (asserted by board)")
	}
	if !got.NvidiaCoolbits {
		t.Error("NvidiaCoolbits should be true (asserted by driver)")
	}
	if got.AMDOverdrive || got.IDRAC9LegacyRaw {
		t.Errorf("AMDOverdrive and IDRAC9LegacyRaw should be false, got AMDOverdrive=%v IDRAC9LegacyRaw=%v",
			got.AMDOverdrive, got.IDRAC9LegacyRaw)
	}
}
