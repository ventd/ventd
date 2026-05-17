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
