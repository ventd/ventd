package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// stageProbeFixture builds a /sys-like hwmon tree with the given chips.
// Each chip is a (name, []pwms) where each PWM optionally has a sibling
// fanN_input and pwmN_enable.
func stageProbeFixture(t *testing.T, root string, chipName string, pwms []pwmFixture) {
	t.Helper()
	for i, p := range pwms {
		chipDir := filepath.Join(root, "hwmon"+strconv.Itoa(i))
		if err := os.MkdirAll(chipDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(chipDir, "name"), []byte(chipName+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(chipDir, "pwm"+strconv.Itoa(p.idx)), "128\n")
		if p.hasEnable {
			writeFile(t, filepath.Join(chipDir, "pwm"+strconv.Itoa(p.idx)+"_enable"), "1\n")
		}
		if p.hasFanInput {
			writeFile(t, filepath.Join(chipDir, "fan"+strconv.Itoa(p.idx)+"_input"), "1500\n")
		}
		if p.label != "" {
			writeFile(t, filepath.Join(chipDir, "pwm"+strconv.Itoa(p.idx)+"_label"), p.label+"\n")
		}
	}
}

type pwmFixture struct {
	idx         int
	hasEnable   bool
	hasFanInput bool
	label       string
}

func TestProbePhase_Name(t *testing.T) {
	if (ProbePhase{}).Name() != "probe" {
		t.Error("Name() must be 'probe'")
	}
}

func TestProbePhase_FindsControllablePWMsAndPairsRPMs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	stageProbeFixture(t, root, "nct6687", []pwmFixture{
		{idx: 1, hasEnable: true, hasFanInput: true},
	})
	stageProbeFixture(t, filepath.Join(root, "extra"), "coretemp", []pwmFixture{})
	// Re-stage chip 2 separately to avoid index collision in stageProbeFixture.
	stageProbeFixture(t, root, "nct6687_2", []pwmFixture{
		{idx: 2, hasEnable: true, hasFanInput: true},
	})

	rc := &RunContext{HwmonRoot: root}
	out := (ProbePhase{}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status = %q (want Success); detail=%q", out.Status, out.Detail)
	}

	var art ProbeArtifact
	if err := json.Unmarshal(out.Artifact, &art); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(art.Fans) < 1 {
		t.Fatalf("expected ≥1 fan, got %d (artifact=%+v)", len(art.Fans), art)
	}
	if art.Fans[0].RPMPath == "" {
		t.Errorf("first fan should have paired RPM path: %+v", art.Fans[0])
	}
}

func TestProbePhase_SkipsPWMWithoutEnable(t *testing.T) {
	// A pwm<N> file without a sibling pwm<N>_enable is a read-only
	// monitoring value (e.g. nct6683 loaded for an NCT6687D chip).
	// Must NOT appear in the artifact.
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	stageProbeFixture(t, root, "nct6683", []pwmFixture{
		{idx: 1, hasEnable: false, hasFanInput: true},
	})

	rc := &RunContext{HwmonRoot: root}
	out := (ProbePhase{}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	var art ProbeArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Fans) != 0 {
		t.Errorf("read-only PWM must not be reported as controllable; got %+v", art.Fans)
	}
}

func TestProbePhase_UsesDriverLabelWhenAvailable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	stageProbeFixture(t, root, "nct6687", []pwmFixture{
		{idx: 1, hasEnable: true, hasFanInput: true, label: "CPU Fan"},
	})

	rc := &RunContext{HwmonRoot: root}
	out := (ProbePhase{}).Execute(context.Background(), rc)
	var art ProbeArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Fans) != 1 || art.Fans[0].LabelHint != "CPU Fan" {
		t.Errorf("driver label should win over synthesised; got %+v", art.Fans)
	}
}

func TestProbePhase_SynthesisesLabelWhenNoDriverLabel(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	stageProbeFixture(t, root, "nct6687", []pwmFixture{
		{idx: 3, hasEnable: true, hasFanInput: true},
	})

	rc := &RunContext{HwmonRoot: root}
	out := (ProbePhase{}).Execute(context.Background(), rc)
	var art ProbeArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Fans) != 1 {
		t.Fatalf("len(Fans) = %d", len(art.Fans))
	}
	if art.Fans[0].LabelHint == "" {
		t.Error("LabelHint should be synthesised when driver-supplied label is missing")
	}
}

func TestProbePhase_EmptyHwmonTreeYieldsZeroFans(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	rc := &RunContext{HwmonRoot: root}
	out := (ProbePhase{}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Errorf("empty tree should still succeed (ApplyPhase handles monitor-only); got %q",
			out.Status)
	}
	var art ProbeArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Fans) != 0 {
		t.Errorf("empty hwmon → expected 0 fans, got %d", len(art.Fans))
	}
}

func TestProbePhase_NoPairedFanInputLeavesRPMPathEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	stageProbeFixture(t, root, "nct6687", []pwmFixture{
		{idx: 1, hasEnable: true, hasFanInput: false}, // DC fan (no tach)
	})

	rc := &RunContext{HwmonRoot: root}
	out := (ProbePhase{}).Execute(context.Background(), rc)
	var art ProbeArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Fans) != 1 {
		t.Fatalf("len=%d", len(art.Fans))
	}
	if art.Fans[0].RPMPath != "" {
		t.Errorf("DC fan without fanN_input should have empty RPMPath; got %q", art.Fans[0].RPMPath)
	}
	if art.Fans[0].PWMPath == "" {
		t.Error("PWMPath should always be populated")
	}
}
