package calibrate

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// stageIdentityFixture builds a /sys-like tree under tmp with the given
// DMI fields and hwmon chip names. Returns the hwmonRoot path the
// identity helpers should be called with.
func stageIdentityFixture(t *testing.T, vendor, board string, chips []string) string {
	t.Helper()
	tmp := t.TempDir()
	sysRoot := filepath.Join(tmp, "sys")
	hwmonRoot := filepath.Join(sysRoot, "class", "hwmon")

	if vendor != "" || board != "" {
		dmiDir := filepath.Join(sysRoot, "devices", "virtual", "dmi", "id")
		if err := os.MkdirAll(dmiDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if vendor != "" {
			if err := os.WriteFile(filepath.Join(dmiDir, "board_vendor"), []byte(vendor+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if board != "" {
			if err := os.WriteFile(filepath.Join(dmiDir, "board_name"), []byte(board+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	for i, chip := range chips {
		dir := filepath.Join(hwmonRoot, "hwmon"+strconv.Itoa(i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "name"), []byte(chip+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	return hwmonRoot
}

func TestComputeHardwareIdentity_DeterministicOnSameInputs(t *testing.T) {
	root := stageIdentityFixture(t, "MSI", "MAG Z690 TOMAHAWK", []string{"nct6687", "coretemp"})
	a := ComputeHardwareIdentity(root)
	b := ComputeHardwareIdentity(root)
	if a != b {
		t.Errorf("identity not deterministic: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Errorf("identity length = %d, want 16 hex chars", len(a))
	}
}

func TestComputeHardwareIdentity_ChangesWithMotherboard(t *testing.T) {
	chips := []string{"nct6687", "coretemp"}
	a := ComputeHardwareIdentity(stageIdentityFixture(t, "MSI", "MAG Z690 TOMAHAWK", chips))
	b := ComputeHardwareIdentity(stageIdentityFixture(t, "ASUS", "ROG STRIX X670E", chips))
	if a == b {
		t.Error("identity should change when board vendor/name changes")
	}
}

func TestComputeHardwareIdentity_ChangesWhenChipAddedOrRemoved(t *testing.T) {
	a := ComputeHardwareIdentity(stageIdentityFixture(t, "MSI", "Z690", []string{"nct6687", "coretemp"}))
	added := ComputeHardwareIdentity(stageIdentityFixture(t, "MSI", "Z690", []string{"nct6687", "coretemp", "drivetemp"}))
	removed := ComputeHardwareIdentity(stageIdentityFixture(t, "MSI", "Z690", []string{"nct6687"}))
	if a == added {
		t.Error("identity should change when a chip is added")
	}
	if a == removed {
		t.Error("identity should change when a chip is removed")
	}
	if added == removed {
		t.Error("added != removed identity")
	}
}

func TestComputeHardwareIdentity_StableAcrossChipOrdering(t *testing.T) {
	a := ComputeHardwareIdentity(stageIdentityFixture(t, "MSI", "Z690", []string{"nct6687", "coretemp"}))
	b := ComputeHardwareIdentity(stageIdentityFixture(t, "MSI", "Z690", []string{"coretemp", "nct6687"}))
	if a != b {
		t.Errorf("identity must be order-independent (we sort chip names); got %q vs %q", a, b)
	}
}

func TestComputeHardwareIdentity_CaseInsensitiveChips(t *testing.T) {
	// Kernel sometimes flips case of chip names across releases (e.g. nvme
	// → NVME). Identity must survive that.
	a := ComputeHardwareIdentity(stageIdentityFixture(t, "MSI", "Z690", []string{"nct6687"}))
	b := ComputeHardwareIdentity(stageIdentityFixture(t, "MSI", "Z690", []string{"NCT6687"}))
	if a != b {
		t.Errorf("identity must be case-insensitive on chip names; got %q vs %q", a, b)
	}
}

func TestComputeHardwareIdentity_EmptyHostReturnsEmpty(t *testing.T) {
	// A fully-empty host (no DMI, no hwmon) means we cannot fingerprint —
	// identity is "" which signals "skip identity gating."
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if id := ComputeHardwareIdentity(root); id != "" {
		t.Errorf("empty host should yield empty identity, got %q", id)
	}
}

func TestCalibrate_V2EnvelopeMigratesByStampingCurrentIdentity(t *testing.T) {
	// Write a v2 envelope with no HardwareIdentity. Load with identity
	// gating enabled. Expect: records preserved (no autorecal trigger),
	// next save() writes v3 with the current identity stamped.
	dir := t.TempDir()
	calPath := filepath.Join(dir, "calibration.json")

	v2 := map[string]any{
		"schema_version": 2,
		"results": map[string]any{
			"/sys/class/hwmon/hwmon0/pwm1": map[string]any{
				"pwm_path":   "/sys/class/hwmon/hwmon0/pwm1",
				"start_pwm":  50,
				"max_rpm":    1500,
				"sweep_mode": "pwm",
			},
		},
	}
	b, _ := json.MarshalIndent(v2, "", "  ")
	if err := os.WriteFile(calPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWithIdentity(calPath, slog.Default(), nil, "identityA")
	if len(m.diagnostics) != 0 {
		t.Errorf("v2 envelope should NOT trigger any diagnostic on load; got %+v", m.diagnostics)
	}
	if got := len(m.AllResults()); got != 1 {
		t.Errorf("v2 records lost during migration; got %d results, want 1", got)
	}
}

func TestCalibrate_V3IdentityMismatchDropsRecordsAndEmitsDiag(t *testing.T) {
	dir := t.TempDir()
	calPath := filepath.Join(dir, "calibration.json")

	v3 := map[string]any{
		"schema_version":    3,
		"hardware_identity": "identityA",
		"results": map[string]any{
			"/sys/class/hwmon/hwmon0/pwm1": map[string]any{
				"pwm_path":   "/sys/class/hwmon/hwmon0/pwm1",
				"start_pwm":  50,
				"max_rpm":    1500,
				"sweep_mode": "pwm",
			},
			"/sys/class/hwmon/hwmon0/pwm2": map[string]any{
				"pwm_path":   "/sys/class/hwmon/hwmon0/pwm2",
				"start_pwm":  60,
				"max_rpm":    1800,
				"sweep_mode": "pwm",
			},
		},
	}
	b, _ := json.MarshalIndent(v3, "", "  ")
	if err := os.WriteFile(calPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWithIdentity(calPath, slog.Default(), nil, "identityB") // mismatch
	if len(m.diagnostics) != 1 {
		t.Fatalf("identity mismatch should emit 1 diagnostic; got %d", len(m.diagnostics))
	}
	d := m.diagnostics[0]
	if d.ID != "calibration.hardware_changed" {
		t.Errorf("diagnostic ID = %q, want calibration.hardware_changed", d.ID)
	}
	if len(d.Affected) != 2 {
		t.Errorf("diagnostic Affected len = %d, want 2", len(d.Affected))
	}
	if got := len(m.AllResults()); got != 0 {
		t.Errorf("mismatched records should be dropped; got %d still loaded", got)
	}
}

func TestCalibrate_V3IdentityMatchPreservesRecords(t *testing.T) {
	dir := t.TempDir()
	calPath := filepath.Join(dir, "calibration.json")

	v3 := map[string]any{
		"schema_version":    3,
		"hardware_identity": "identityA",
		"results": map[string]any{
			"/sys/class/hwmon/hwmon0/pwm1": map[string]any{
				"pwm_path":   "/sys/class/hwmon/hwmon0/pwm1",
				"start_pwm":  50,
				"max_rpm":    1500,
				"sweep_mode": "pwm",
			},
		},
	}
	b, _ := json.MarshalIndent(v3, "", "  ")
	if err := os.WriteFile(calPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWithIdentity(calPath, slog.Default(), nil, "identityA") // match
	if len(m.diagnostics) != 0 {
		t.Errorf("identity match should emit no diagnostic; got %+v", m.diagnostics)
	}
	if got := len(m.AllResults()); got != 1 {
		t.Errorf("matched records should load; got %d", got)
	}
}

func TestCalibrate_NewWithIdentityEmptyDisablesGating(t *testing.T) {
	// NewWithIdentity("") must behave exactly as legacy v2: no identity
	// gating, no diagnostics from identity-mismatch. Use a v3 envelope
	// with a non-empty identity to prove that.
	dir := t.TempDir()
	calPath := filepath.Join(dir, "calibration.json")
	v3 := map[string]any{
		"schema_version":    3,
		"hardware_identity": "somethingElse",
		"results": map[string]any{
			"/x": map[string]any{"pwm_path": "/x", "start_pwm": 50, "max_rpm": 1500, "sweep_mode": "pwm"},
		},
	}
	b, _ := json.MarshalIndent(v3, "", "  ")
	if err := os.WriteFile(calPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWithIdentity(calPath, slog.Default(), nil, "")
	if len(m.diagnostics) != 0 {
		t.Errorf("empty expectedIdentity should bypass identity check; got %+v", m.diagnostics)
	}
	if got := len(m.AllResults()); got != 1 {
		t.Errorf("records should load when gating is disabled; got %d", got)
	}
}

func TestCalibrate_SaveStampsIdentityIntoEnvelope(t *testing.T) {
	dir := t.TempDir()
	calPath := filepath.Join(dir, "calibration.json")

	m := NewWithIdentity(calPath, slog.Default(), nil, "identityX")
	m.results["/sys/class/hwmon/hwmon0/pwm1"] = Result{
		PWMPath: "/sys/class/hwmon/hwmon0/pwm1", StartPWM: 50, MaxRPM: 1500, SweepMode: SweepModePWM,
	}
	m.save()

	raw, err := os.ReadFile(calPath)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env["schema_version"] != float64(3) {
		t.Errorf("schema_version = %v, want 3", env["schema_version"])
	}
	if env["hardware_identity"] != "identityX" {
		t.Errorf("hardware_identity = %v, want identityX", env["hardware_identity"])
	}
}
