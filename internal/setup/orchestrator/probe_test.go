package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ventd/ventd/internal/hal"
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

// TestProbePhase_PopulatesMonitorChannels covers the #796 wiring:
// ProbePhase must call EnumerateMonitorChannels and surface the
// per-channel visibility verdicts on the artifact so ApplyPhase and
// the dashboard can filter ghost fans. Stages a real chip with one
// controllable PWM + a paired fan1_input, plus a second unpaired
// fan2_input on the same chip. Asserts:
//   - len(MonitorChannels) == 2 (one per fan*_input file)
//   - the paired channel has PairedPWM populated
//   - the unpaired all-zero channel is classified phantom
//
// Detailed verdict-rule coverage (real / mirror / phantom transitions,
// MirrorOf back-references) lives in probe.tach_classify_test.go; this
// test asserts only the orchestrator-side wiring (artifact field,
// pairedPWMs derivation from art.Fans).
func TestProbePhase_PopulatesMonitorChannels(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	// pwm1 controllable + fan1_input non-zero → real (paired).
	stageProbeFixture(t, root, "nct6687", []pwmFixture{
		{idx: 1, hasEnable: true, hasFanInput: true},
	})
	// Add an unpaired fan2_input reading 0 in the same hwmon0 chip
	// dir so the classifier sees the phantom case alongside.
	chipDir := filepath.Join(root, "hwmon0")
	if err := os.WriteFile(filepath.Join(chipDir, "fan2_input"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := &RunContext{HwmonRoot: root}
	out := (ProbePhase{}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	var art ProbeArtifact
	if err := json.Unmarshal(out.Artifact, &art); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(art.MonitorChannels) != 2 {
		t.Fatalf("expected 2 monitor channels (fan1_input + fan2_input); got %d: %+v",
			len(art.MonitorChannels), art.MonitorChannels)
	}

	byTach := map[string]bool{}
	var paired, phantom int
	for _, ch := range art.MonitorChannels {
		byTach[ch.TachPath] = true
		if ch.PairedPWM != "" {
			paired++
		}
		if ch.Visibility == "phantom" {
			phantom++
		}
	}
	if paired != 1 {
		t.Errorf("expected exactly 1 paired channel; got %d (%+v)", paired, art.MonitorChannels)
	}
	if phantom != 1 {
		t.Errorf("expected fan2_input classified phantom (all-zero, no paired PWM); got phantom=%d (%+v)",
			phantom, art.MonitorChannels)
	}
}

// TestProbePhase_HALPassDiscoversNonHwmonFan is the #1376 regression:
// a host with no controllable hwmon PWM (an MSI laptop: tach-only
// msi_wmi_platform + coretemp) but a live msiec HAL channel must yield a
// ProbedFan tagged Backend="msiec" with the channel's inner ID as
// PWMPath, so apply can emit a Type:"msiec" fan the resolver drives.
func TestProbePhase_HALPassDiscoversNonHwmonFan(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	enumerate := func(context.Context) ([]hal.Channel, error) {
		return []hal.Channel{
			// The target: a writable msiec channel (tagged as Enumerate emits).
			{ID: "msiec:/sys/devices/platform/msi-ec", Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore},
			// hwmon is owned by the sysfs glob — must be skipped to avoid double-listing.
			{ID: "hwmon:/sys/class/hwmon/hwmon0/pwm1", Caps: hal.CapRead | hal.CapWritePWM},
			// nvml/gpu are owned by NVMLPhase — must be skipped.
			{ID: "nvml:0", Caps: hal.CapRead | hal.CapWritePWM},
			// A read-only channel (no CapWritePWM) — nothing to control, skip.
			{ID: "ipmi:/dev/ipmi0#3", Caps: hal.CapRead},
		}, nil
	}

	rc := &RunContext{HwmonRoot: root}
	out := (ProbePhase{HALEnumerate: enumerate}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	var art ProbeArtifact
	if err := json.Unmarshal(out.Artifact, &art); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(art.Fans) != 1 {
		t.Fatalf("expected exactly 1 fan (only the writable msiec channel); got %d: %+v", len(art.Fans), art.Fans)
	}
	f := art.Fans[0]
	if f.Backend != "msiec" {
		t.Errorf("Backend = %q, want %q", f.Backend, "msiec")
	}
	if f.PWMPath != "/sys/devices/platform/msi-ec" {
		t.Errorf("PWMPath = %q, want the channel inner ID %q", f.PWMPath, "/sys/devices/platform/msi-ec")
	}
	if fanType(f) != "msiec" {
		t.Errorf("fanType = %q, want %q", fanType(f), "msiec")
	}
	if f.RPMPath != "" {
		t.Errorf("RPMPath = %q, want empty (paired later by RPMDetect)", f.RPMPath)
	}
}

// TestProbePhase_NilHALEnumerateIsHwmonOnly pins the back-compat path:
// when no HAL enumerator is wired (older tests, checkpoints), the phase
// behaves exactly like the pre-#1376 hwmon-only probe.
func TestProbePhase_NilHALEnumerateIsHwmonOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	out := (ProbePhase{}).Execute(context.Background(), &RunContext{HwmonRoot: root})
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	var art ProbeArtifact
	if err := json.Unmarshal(out.Artifact, &art); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(art.Fans) != 0 {
		t.Fatalf("expected 0 fans (empty hwmon tree, no HAL pass); got %d: %+v", len(art.Fans), art.Fans)
	}
}
