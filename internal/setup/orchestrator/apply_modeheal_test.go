package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/config"
)

// TestApplyPhase_HealedFanPersistsPWMMode: a calibrate result the
// self-heal recovered (ModeHealed + ResolvedPWMMode="dc") must reach the
// applied config as an admitted fan carrying pwm_mode: dc, so the hwmon
// controller re-asserts DC mode at runtime (#759).
func TestApplyPhase_HealedFanPersistsPWMMode(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	const pwm = "/sys/hwmon0/pwm1"
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, RPMPath: "/sys/hwmon0/fan1_input", ChipName: "nct6775", LabelHint: "Cpu Fan"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "normal"}},
	})
	seedCalibrateCheckpoint(t, rc, CalibrateArtifact{
		Results: []CalibrateFanResult{{
			PWMPath:         pwm,
			StartPWM:        60,
			MaxRPM:          1800,
			SweepMode:       "pwm",
			ModeHealed:      true,
			ResolvedPWMMode: "dc",
		}},
	})

	out := (ApplyPhase{ConfigPath: cfgPath}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if len(cfg.Fans) != 1 {
		t.Fatalf("expected the healed fan admitted, got %d fans", len(cfg.Fans))
	}
	if cfg.Fans[0].PWMMode != "dc" {
		t.Fatalf("healed fan must carry pwm_mode=dc, got %q", cfg.Fans[0].PWMMode)
	}
}

// TestApplyPhase_UnhealedModeMismatchExcludedWithBIOSGuidance: a fan that
// stayed ModeMismatchSuspected (no heal — non-writable driver) must be
// excluded as uncontrollable with a mode_mismatch reason and NOT carry a
// pwm_mode in any admitted fan.
func TestApplyPhase_UnhealedModeMismatchExcludedWithBIOSGuidance(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	const pwm = "/sys/hwmon0/pwm1"
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: pwm, RPMPath: "/sys/hwmon0/fan1_input", ChipName: "it87", LabelHint: "Sys Fan"}},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: pwm, Polarity: "normal"}},
	})
	seedCalibrateCheckpoint(t, rc, CalibrateArtifact{
		Results: []CalibrateFanResult{{
			PWMPath:               pwm,
			MaxRPM:                1500,
			SweepMode:             "pwm",
			ModeMismatchSuspected: true,
			ModeMismatchEvidence:  "flat_rpm_with_stuck_full_speed",
		}},
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
		t.Fatalf("expected 1 uncontrollable fan, got %d (%+v)", len(art.Uncontrollable), art.Uncontrollable)
	}
	if !strings.HasPrefix(art.Uncontrollable[0].Reason, "mode_mismatch") {
		t.Fatalf("uncontrollable reason=%q, want a mode_mismatch* token", art.Uncontrollable[0].Reason)
	}

	body, _ := os.ReadFile(cfgPath)
	var cfg config.Config
	_ = yaml.Unmarshal(body, &cfg)
	for _, f := range cfg.Fans {
		if f.PWMPath == pwm {
			t.Fatalf("mode-mismatch fan must NOT be admitted to the config, got %+v", f)
		}
	}
}
