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

// seedRPMDetectCheckpoint writes an RPMDetectArtifact under the state
// dir so ApplyPhase has something to consume. Used by the #598
// uncontrollable-channel test below.
func seedRPMDetectCheckpoint(t *testing.T, rc *RunContext, art RPMDetectArtifact) {
	t.Helper()
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(art)
	state.Outcomes[(RPMDetectPhase{}).Name()] = Outcome{
		Phase:    (RPMDetectPhase{}).Name(),
		Status:   StatusSuccess,
		Artifact: raw,
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
}

// TestApplyPhase_UncontrollableFromRPMDetectExcludesFan verifies the
// #598 path: when RPMDetect's sweep found no fan_input correlation for
// a channel, ApplyPhase excludes the channel from the active control
// config and records the exclusion in ApplyArtifact.Uncontrollable.
//
// The reason this is gated on the RPMDetect flag (not just empty
// RPMPath) is so tests without RPMDetect artifacts still admit fans
// with the pre-#598 behaviour — only fans RPMDetect actively cleared
// as uncontrollable are excluded.
func TestApplyPhase_UncontrollableFromRPMDetectExcludesFan(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	const goodPath = "/sys/hwmon0/pwm1"
	const badPath = "/sys/hwmon0/pwm2"

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: goodPath, RPMPath: "/sys/hwmon0/fan1_input", ChipName: "nct6687", LabelHint: "Cpu Fan"},
			{Index: 2, PWMPath: badPath, ChipName: "nct6687", LabelHint: "Mystery Fan"}, // no tach paired by probe
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{
			{PWMPath: goodPath, Polarity: "normal"},
			{PWMPath: badPath, Polarity: "normal"}, // PWM works but no RPM tach
		},
	})
	// Pre-#598: this fan would be added with RPMPath="" and the
	// daemon would silently fail to monitor it. With Uncontrollable=true
	// in the RPMDetectArtifact, ApplyPhase excludes it.
	seedRPMDetectCheckpoint(t, rc, RPMDetectArtifact{
		Results: []RPMDetectFanResult{
			{PWMPath: badPath, Uncontrollable: true, Skipped: "no fan_input responded to PWM ramp"},
		},
	})

	out := (ApplyPhase{ConfigPath: cfgPath}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	var art ApplyArtifact
	if err := json.Unmarshal(out.Artifact, &art); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	if len(art.Uncontrollable) != 1 {
		t.Fatalf("artifact should record 1 uncontrollable fan; got %d (%+v)", len(art.Uncontrollable), art.Uncontrollable)
	}
	if art.Uncontrollable[0].PWMPath != badPath {
		t.Errorf("uncontrollable.PWMPath=%q want %q", art.Uncontrollable[0].PWMPath, badPath)
	}
	if art.Uncontrollable[0].Reason != "no_sensor_correlated" {
		t.Errorf("uncontrollable.Reason=%q want %q", art.Uncontrollable[0].Reason, "no_sensor_correlated")
	}

	// The good fan still made it through.
	body, _ := os.ReadFile(cfgPath)
	var cfg config.Config
	_ = yaml.Unmarshal(body, &cfg)
	if len(cfg.Fans) != 1 {
		t.Fatalf("cfg.Fans len=%d want 1 (only the good fan); got %+v", len(cfg.Fans), cfg.Fans)
	}
	if cfg.Fans[0].PWMPath != goodPath {
		t.Errorf("admitted fan PWMPath=%q want %q", cfg.Fans[0].PWMPath, goodPath)
	}
}
