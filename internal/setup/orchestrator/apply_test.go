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

// seedPolarityCheckpoint writes a PolarityArtifact under the state
// dir so ApplyPhase has something to consume.
func seedPolarityCheckpoint(t *testing.T, rc *RunContext, art PolarityArtifact) {
	t.Helper()
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(art)
	state.Outcomes[(PolarityPhase{}).Name()] = Outcome{
		Phase:    (PolarityPhase{}).Name(),
		Status:   StatusSuccess,
		Artifact: raw,
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPhase_Name(t *testing.T) {
	if (ApplyPhase{}).Name() != "apply" {
		t.Error("Name() must be 'apply'")
	}
}

func TestApplyPhase_WritesConfigForProbedFans(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "ventd", "config.yaml")

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", RPMPath: "/sys/hwmon0/fan1_input", ChipName: "nct6687", LabelHint: "Cpu Fan"},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2", RPMPath: "/sys/hwmon0/fan2_input", ChipName: "nct6687", LabelHint: "System Fan 1"},
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{
			{PWMPath: "/sys/hwmon0/pwm1", Polarity: "normal"},
			{PWMPath: "/sys/hwmon0/pwm2", Polarity: "normal"},
		},
	})

	out := (ApplyPhase{ConfigPath: cfgPath}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse generated config: %v", err)
	}
	if len(cfg.Fans) != 2 {
		t.Errorf("expected 2 fans in config, got %d", len(cfg.Fans))
	}
	if cfg.Fans[0].Name != "Cpu Fan" {
		t.Errorf("Fan[0].Name = %q, want 'Cpu Fan'", cfg.Fans[0].Name)
	}
	if cfg.Fans[0].Type != "hwmon" {
		t.Errorf("Fan[0].Type = %q, want hwmon", cfg.Fans[0].Type)
	}
	if cfg.Fans[0].MinPWM == 0 || cfg.Fans[0].MaxPWM != 255 {
		t.Errorf("Fan[0] PWM bounds wrong: min=%d max=%d", cfg.Fans[0].MinPWM, cfg.Fans[0].MaxPWM)
	}
}

func TestApplyPhase_ExcludesPhantomFans(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", ChipName: "x", LabelHint: "Good Fan"},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2", ChipName: "x", LabelHint: "Phantom Fan"},
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{
			{PWMPath: "/sys/hwmon0/pwm1", Polarity: "normal"},
			{PWMPath: "/sys/hwmon0/pwm2", Polarity: "phantom"},
		},
	})

	out := (ApplyPhase{ConfigPath: cfgPath}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	body, _ := os.ReadFile(cfgPath)
	var cfg config.Config
	_ = yaml.Unmarshal(body, &cfg)
	if len(cfg.Fans) != 1 {
		t.Fatalf("phantom fan must be excluded; got %d fans", len(cfg.Fans))
	}
	if cfg.Fans[0].Name != "Good Fan" {
		t.Errorf("wrong fan kept; got %q", cfg.Fans[0].Name)
	}
}

func TestApplyPhase_MonitorOnlyWhenNoFans(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	seedProbeCheckpoint(t, rc, ProbeArtifact{Fans: nil})
	out := (ApplyPhase{ConfigPath: cfgPath}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	var art ApplyArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if !art.MonitorOnly {
		t.Error("zero-fan host should be flagged MonitorOnly")
	}
	if art.Fans != 0 {
		t.Errorf("Fans = %d, want 0", art.Fans)
	}
}

func TestApplyPhase_WriteIsAtomicNoTmpRemains(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: "/sys/hwmon0/pwm1", ChipName: "x", LabelHint: "F"}},
	})

	out := (ApplyPhase{ConfigPath: cfgPath}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	if _, err := os.Stat(cfgPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp file should not remain after atomic write; stat err=%v", err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("config file should exist after apply: %v", err)
	}
}

func TestApplyPhase_DefaultsToUnknownPolarityWhenPolarityArtifactMissing(t *testing.T) {
	// Apply runs without a prior Polarity checkpoint. Should still
	// write a config (Fans included, no Controls). Daemon's WritePWM
	// will refuse to drive "unknown" polarity channels until the
	// next wizard run.
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: "/sys/hwmon0/pwm1", ChipName: "x", LabelHint: "F"}},
	})
	// No PolarityPhase checkpoint seeded.

	out := (ApplyPhase{ConfigPath: cfgPath}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	body, _ := os.ReadFile(cfgPath)
	var cfg config.Config
	_ = yaml.Unmarshal(body, &cfg)
	if len(cfg.Fans) != 1 {
		t.Errorf("Fan should be included with unknown-polarity fallback; got %d", len(cfg.Fans))
	}
}

func TestApplyPhase_MissingProbeArtifactFails(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	out := (ApplyPhase{ConfigPath: cfgPath}).Execute(context.Background(), rc)
	if out.Status != StatusFailed {
		t.Errorf("missing prior ProbePhase should yield Failed, got %q", out.Status)
	}
}
