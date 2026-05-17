package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/config"
)

// TestApplyPhase_FullConfigWhenSensorAndCalAvailable verifies the
// happy path: sensor discovered, calibration data present →
// resulting config has Sensors + Fans + Curves + Controls and the
// daemon will run active control immediately.
func TestApplyPhase_FullConfigWhenSensorAndCalAvailable(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	// Stage a CPU sensor under a fake hwmon root.
	hwmonRoot := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	stageSensorFixture(t, filepath.Join(hwmonRoot, "hwmon0"), "coretemp",
		map[int]string{1: "Package id 0"})

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", RPMPath: "/sys/hwmon0/fan1_input", ChipName: "nct6687", LabelHint: "Cpu Fan"},
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: "/sys/hwmon0/pwm1", Polarity: "normal"}},
	})
	seedCalibrateCheckpoint(t, rc, CalibrateArtifact{
		Results: []CalibrateFanResult{
			{PWMPath: "/sys/hwmon0/pwm1", StartPWM: 50, MaxRPM: 2000, SweepMode: "pwm"},
		},
	})

	out := (ApplyPhase{ConfigPath: cfgPath, HwmonRoot: hwmonRoot}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse generated config: %v", err)
	}

	if len(cfg.Sensors) != 1 || cfg.Sensors[0].Name != "cpu_temp" {
		t.Errorf("Sensors wrong: %+v", cfg.Sensors)
	}
	if len(cfg.Fans) != 1 {
		t.Fatalf("Fans len = %d, want 1", len(cfg.Fans))
	}
	if cfg.Fans[0].MinPWM != 50 {
		t.Errorf("Fan MinPWM should be from calibration (50), got %d", cfg.Fans[0].MinPWM)
	}
	if len(cfg.Curves) != 1 || cfg.Curves[0].Name != "default" {
		t.Errorf("Curves wrong: %+v", cfg.Curves)
	}
	if len(cfg.Controls) != 1 {
		t.Errorf("Controls wrong: %+v", cfg.Controls)
	}
	if cfg.Controls[0].Fan != "Cpu Fan" || cfg.Controls[0].Curve != "default" {
		t.Errorf("Control mapping wrong: %+v", cfg.Controls[0])
	}
}

// TestApplyPhase_NoSensorFallsBackToMonitorOnly verifies that when
// no CPU sensor is discoverable, the resulting config still has
// Fans (so they appear in the dashboard) but no Controls — the
// daemon runs in monitor-only mode and the operator can add curves
// via the web UI.
func TestApplyPhase_NoSensorFallsBackToMonitorOnly(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	hwmonRoot := filepath.Join(t.TempDir(), "sys", "class", "hwmon") // empty
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: "/sys/hwmon0/pwm1", ChipName: "x", LabelHint: "F"}},
	})

	out := (ApplyPhase{ConfigPath: cfgPath, HwmonRoot: hwmonRoot}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	body, _ := os.ReadFile(cfgPath)
	var cfg config.Config
	_ = yaml.Unmarshal(body, &cfg)
	if len(cfg.Fans) != 1 {
		t.Errorf("Fan should still be listed; got %d", len(cfg.Fans))
	}
	if len(cfg.Controls) != 0 {
		t.Errorf("no sensor → no Controls (monitor-only); got %+v", cfg.Controls)
	}
	if len(cfg.Curves) != 0 {
		t.Errorf("no sensor → no Curves; got %+v", cfg.Curves)
	}
}

// TestApplyPhase_VerifyPhantomFanExcluded verifies that fans
// reclassified as phantom by the post-calibration verify are excluded
// from the resulting config (in addition to polarity-phantom fans).
func TestApplyPhase_VerifyPhantomFanExcluded(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	hwmonRoot := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	stageSensorFixture(t, filepath.Join(hwmonRoot, "hwmon0"), "coretemp",
		map[int]string{1: "Package id 0"})

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/p1", LabelHint: "Good Fan"},
			{Index: 2, PWMPath: "/p2", LabelHint: "Verify-Phantom Fan"},
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{
			{PWMPath: "/p1", Polarity: "normal"},
			{PWMPath: "/p2", Polarity: "normal"},
		},
	})
	// Verify marks /p2 phantom (calibration looked OK but post-cal
	// full-speed read showed 0 RPM).
	store := NewCheckpointStore(stateDir)
	state, _ := store.Load()
	state.Outcomes[(VerifyPhase{}).Name()] = Outcome{
		Phase:  (VerifyPhase{}).Name(),
		Status: StatusSuccess,
		Artifact: mustJSON(VerifyArtifact{
			Results: []VerifyFanResult{
				{PWMPath: "/p1", Phantom: false},
				{PWMPath: "/p2", Phantom: true, ReclassifiedFrom: "normal"},
			},
		}),
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}

	out := (ApplyPhase{ConfigPath: cfgPath, HwmonRoot: hwmonRoot}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	body, _ := os.ReadFile(cfgPath)
	var cfg config.Config
	_ = yaml.Unmarshal(body, &cfg)
	if len(cfg.Fans) != 1 || cfg.Fans[0].Name != "Good Fan" {
		t.Errorf("verify-phantom fan must be excluded; got fans=%+v", cfg.Fans)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// TestApplyPhase_PopulatesHwmonDevice exercises the PR#B7 fix: ApplyPhase
// must populate config.Fan.HwmonDevice and config.Sensor.HwmonDevice with
// the stable /sys/devices/... path resolved through each chip's `device`
// symlink. Without this, ResolveHwmonPaths can't tell two hwmonN entries
// that share a chip_name apart — the daemon refuses to load the config.
//
// Fixture: tempdir with two hwmonN chips both named "nct6687", each
// pointing at a distinct /sys/devices/... target via its `device`
// symlink. Asserts the fan and sensor written into the generated config
// carry the right stable path.
func TestApplyPhase_PopulatesHwmonDevice(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	fakeSys := t.TempDir()
	hwmonRoot := filepath.Join(fakeSys, "sys", "class", "hwmon")
	devicesRoot := filepath.Join(fakeSys, "sys", "devices", "platform")

	// Two chips, both reporting chip_name="nct6687" — the dual-bind
	// case (in-kernel nct6683 reporting nct6687 + OOT nct6687) is the
	// motivating scenario for this fix.
	stageHwmonFixtureWithDevice(t,
		filepath.Join(hwmonRoot, "hwmon0"),
		filepath.Join(devicesRoot, "nct6683.2592"),
		"nct6687")
	stageHwmonFixtureWithDevice(t,
		filepath.Join(hwmonRoot, "hwmon1"),
		filepath.Join(devicesRoot, "nct6687.2592"),
		"nct6687")

	pwmPath := filepath.Join(hwmonRoot, "hwmon1", "pwm1")
	if err := os.WriteFile(pwmPath, []byte("128\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stage a CPU sensor under hwmon0 too — buildConfig also wires
	// HwmonDevice into Sensors.
	stageSensorFixture(t, filepath.Join(hwmonRoot, "hwmon0"), "nct6687",
		map[int]string{1: "Package id 0"})
	// stageHwmonFixtureWithDevice wrote a `name` file already; redo it
	// to "coretemp"-style so DiscoverCPUSensor picks this chip. Use
	// coretemp so the well-known-CPU-chip pass1 succeeds.
	if err := os.WriteFile(filepath.Join(hwmonRoot, "hwmon0", "name"),
		[]byte("coretemp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: pwmPath, ChipName: "nct6687", LabelHint: "Cpu Fan"},
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwmPath, Polarity: "normal"}},
	})

	out := (ApplyPhase{ConfigPath: cfgPath, HwmonRoot: hwmonRoot}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse generated config: %v", err)
	}

	wantFanDevice := filepath.Join(devicesRoot, "nct6687.2592")
	if len(cfg.Fans) != 1 {
		t.Fatalf("Fans len=%d, want 1", len(cfg.Fans))
	}
	if cfg.Fans[0].HwmonDevice != wantFanDevice {
		t.Errorf("Fan HwmonDevice=%q, want %q", cfg.Fans[0].HwmonDevice, wantFanDevice)
	}
	wantSensorDevice := filepath.Join(devicesRoot, "nct6683.2592")
	if len(cfg.Sensors) != 1 {
		t.Fatalf("Sensors len=%d, want 1", len(cfg.Sensors))
	}
	if cfg.Sensors[0].HwmonDevice != wantSensorDevice {
		t.Errorf("Sensor HwmonDevice=%q, want %q", cfg.Sensors[0].HwmonDevice, wantSensorDevice)
	}
}

// TestApplyPhase_HwmonDeviceEmptyWhenSymlinkMissing covers the
// virtual-subsystem case (acpitz, drivetemp configs without a `device`
// symlink in the hwmonN dir): HwmonDevice must remain empty. The
// daemon's resolver treats an empty HwmonDevice as "use chip_name
// alone" which is the pre-disambiguation behaviour and strictly weaker
// than failing the write. Existing single-chip configs keep working
// without operator intervention.
func TestApplyPhase_HwmonDeviceEmptyWhenSymlinkMissing(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	hwmonRoot := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	chipDir := filepath.Join(hwmonRoot, "hwmon0")
	if err := os.MkdirAll(chipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pwmPath := filepath.Join(chipDir, "pwm1")
	if err := os.WriteFile(pwmPath, []byte("128\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No `device` symlink staged.

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwmPath, ChipName: "x", LabelHint: "F"}},
	})

	out := (ApplyPhase{ConfigPath: cfgPath, HwmonRoot: hwmonRoot}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	body, _ := os.ReadFile(cfgPath)
	var cfg config.Config
	_ = yaml.Unmarshal(body, &cfg)
	if len(cfg.Fans) != 1 || cfg.Fans[0].HwmonDevice != "" {
		t.Errorf("missing device symlink should leave HwmonDevice empty; got %+v", cfg.Fans)
	}
}

// stageHwmonFixtureWithDevice creates chipDir with a `name` file and a
// `device` symlink pointing at deviceTarget. Used to drive the
// HwmonDevice-resolution path under a tempdir without touching real
// /sys.
func stageHwmonFixtureWithDevice(t *testing.T, chipDir, deviceTarget, chipName string) {
	t.Helper()
	if err := os.MkdirAll(chipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deviceTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chipDir, "name"),
		[]byte(chipName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(deviceTarget, filepath.Join(chipDir, "device")); err != nil {
		t.Fatal(err)
	}
}
