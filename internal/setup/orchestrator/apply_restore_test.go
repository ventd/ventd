package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readEnable reads a pwm_enable fixture file and returns its trimmed contents.
func readEnable(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(b))
}

// TestApplyPhase_RestoresExcludedChannelEnableToProbeValue is the apply-side
// regression test RULE-SETUP-NO-ORPHANED-CHANNELS asked for (it was TODO /
// allow-orphan): every probed hwmon channel that does NOT make it into the
// generated config must have pwm_enable restored to its probe-time captured
// value before the wizard returns — not left at the manual (1) state the
// calibrate sweep leaves, where neither ventd nor BIOS drives the fan and it
// can't ramp under load (the Dell SMM thermal-safety regression that motivated
// the rule). The complement matters too: an INCLUDED channel must be left
// untouched for the daemon to acquire, not eagerly handed back to firmware.
func TestApplyPhase_RestoresExcludedChannelEnableToProbeValue(t *testing.T) {
	stateDir := t.TempDir()
	rc := &RunContext{StateDir: stateDir}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	sys := t.TempDir()
	goodEnable := filepath.Join(sys, "pwm1_enable")
	phantomEnable := filepath.Join(sys, "pwm2_enable")
	// The calibrate→apply cascade leaves both channels in manual mode (1).
	writeFile(t, goodEnable, "1\n")
	writeFile(t, phantomEnable, "1\n")

	// Two probed fans, each carrying its probe-time captured pwm_enable (2 =
	// firmware auto). One classifies normal (→ admitted), one phantom (→ excluded).
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", ChipName: "x", LabelHint: "Good Fan", EnablePath: goodEnable, InitialEnable: 2},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2", ChipName: "x", LabelHint: "Phantom Fan", EnablePath: phantomEnable, InitialEnable: 2},
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
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	// The excluded (phantom) channel is restored to its probe-time pwm_enable (2).
	if got := readEnable(t, phantomEnable); got != "2" {
		t.Errorf("excluded channel pwm_enable = %q, want 2 (probe-time captured value restored)", got)
	}
	// The included channel is left untouched (manual 1) for the daemon to acquire.
	if got := readEnable(t, goodEnable); got != "1" {
		t.Errorf("included channel pwm_enable = %q, want 1 (untouched; the daemon manages it)", got)
	}
}
