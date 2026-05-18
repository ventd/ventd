package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/config"
)

// TestApplyPhase_DefaultCurveTypeIsPoints is the regression guard for #1224.
// ApplyPhase generates a 3-anchor PWM curve. The runtime curve registry
// resolves Type:"linear" to Linear.Evaluate, which uses the struct's flat
// MinPWM/MaxPWM fields and ignores Points[] entirely; Type:"points" resolves
// to Points.Evaluate, which uses the anchor list. If apply.go ever emits
// Type:"linear" again with Points[]-only data, every fresh install ships
// fans that cannot ramp above per-fan min_pwm regardless of temperature.
//
// The fix is a one-line type-string change in apply.go; this test pins
// it so the wizard cannot silently regress to the unsafe pairing.
func TestApplyPhase_DefaultCurveTypeIsPoints(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", RPMPath: "/sys/hwmon0/fan1_input", ChipName: "nct6687", LabelHint: "Cpu Fan"},
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{
			{PWMPath: "/sys/hwmon0/pwm1", Polarity: "normal"},
		},
	})

	out := (ApplyPhase{ConfigPath: cfgPath}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("apply status=%q detail=%q", out.Status, out.Detail)
	}

	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse generated config: %v", err)
	}

	if len(cfg.Curves) == 0 {
		t.Skip("monitor-only path (no CPU sensor discovered in sandbox); curve type pin doesn't apply")
	}
	got := cfg.Curves[0].Type
	if got != "points" {
		t.Errorf("default curve Type = %q, want %q — \"linear\" + Points[] is the #1224 unsafe pairing that pins every fan at min_pwm regardless of temperature", got, "points")
	}
	if len(cfg.Curves[0].Points) < 2 {
		t.Errorf("default curve must ship at least 2 Points anchors (got %d) so the runtime Points evaluator has something to interpolate between", len(cfg.Curves[0].Points))
	}
	// Sanity: the highest-temperature anchor must specify a PWM percent
	// strictly above zero. If this drops to zero (or nil) we're back at
	// the same "fans never ramp" symptom even with the correct type.
	last := cfg.Curves[0].Points[len(cfg.Curves[0].Points)-1]
	if last.PWMPct == nil || *last.PWMPct == 0 {
		t.Errorf("highest-temperature anchor must specify PWMPct > 0; got %+v", last)
	}
}
