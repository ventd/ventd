package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/config"
)

// seedNVMLCheckpoint writes an NVMLArtifact under the state dir so
// ApplyPhase loads the GPU fan list as if NVMLPhase had run on a host
// with NVIDIA hardware.
func seedNVMLCheckpoint(t *testing.T, rc *RunContext, art NVMLArtifact) {
	t.Helper()
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(art)
	state.Outcomes[(NVMLPhase{}).Name()] = Outcome{
		Phase:    (NVMLPhase{}).Name(),
		Status:   StatusSuccess,
		Artifact: raw,
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
}

// TestApplyPhase_EmitsNVIDIASensorsForEachGPUFan pins the #1226 fix:
// when NVMLPhase discovers an NVIDIA GPU fan, ApplyPhase must also
// emit matching `type:nvidia` sensors so the dashboard can render
// GPU temperature + fan-speed alongside CPU + hwmon fans. Before
// this, the wizard wrote the fan entry but no sensor, leaving the
// dashboard showing `rpm: null` and no GPU temp under sensors[].
func TestApplyPhase_EmitsNVIDIASensorsForEachGPUFan(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

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
	seedNVMLCheckpoint(t, rc, NVMLArtifact{
		Available: true,
		Fans: []NVMLGPUFan{
			{Index: 0, Label: "gpu0", HasTemp: true, TempC: 30},
			{Index: 1, Label: "gpu1", HasTemp: true, TempC: 32},
		},
	})

	out := (ApplyPhase{ConfigPath: cfgPath, HwmonRoot: hwmonRoot}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	body, _ := os.ReadFile(cfgPath)
	var cfg config.Config
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	// Expect cpu_temp + 4 GPU sensors (2 GPUs x {temp, fan_pct}).
	wantByName := map[string]config.Sensor{
		"cpu_temp":     {Name: "cpu_temp", Type: "hwmon"},
		"gpu0_temp":    {Name: "gpu0_temp", Type: "nvidia", Path: "0", Metric: "temp"},
		"gpu0_fan_pct": {Name: "gpu0_fan_pct", Type: "nvidia", Path: "0", Metric: "fan_pct"},
		"gpu1_temp":    {Name: "gpu1_temp", Type: "nvidia", Path: "1", Metric: "temp"},
		"gpu1_fan_pct": {Name: "gpu1_fan_pct", Type: "nvidia", Path: "1", Metric: "fan_pct"},
	}
	gotByName := map[string]config.Sensor{}
	for _, s := range cfg.Sensors {
		gotByName[s.Name] = s
	}
	for name, want := range wantByName {
		got, ok := gotByName[name]
		if !ok {
			t.Errorf("missing sensor %q from emitted config (got names: %v)", name, sensorNames(cfg.Sensors))
			continue
		}
		if got.Type != want.Type {
			t.Errorf("sensor %q Type = %q, want %q", name, got.Type, want.Type)
		}
		if want.Path != "" && got.Path != want.Path {
			t.Errorf("sensor %q Path = %q, want %q", name, got.Path, want.Path)
		}
		if want.Metric != "" && got.Metric != want.Metric {
			t.Errorf("sensor %q Metric = %q, want %q", name, got.Metric, want.Metric)
		}
	}
}

// TestApplyPhase_PerFanCurveAnchorDensity pins the #1231 fix carried
// forward into the per-fan curve generator (#1272): a wide-range hwmon
// fan must receive a curve with more anchors than a narrow-range fan
// so each per-tick PWM step is small enough that the fan can absorb
// it without overshoot. Target ~30 PWM units per segment.
//
// buildConfig always sets fan.MaxPWM=255 from probe-time, so the
// effective range driving anchor density is (255 - StartPWM).
func TestApplyPhase_PerFanCurveAnchorDensity(t *testing.T) {
	cases := []struct {
		name        string
		startPWM    uint8
		wantAnchors int
	}{
		{"narrow_range_floor_3", 215, 3},
		{"dell_smm_class", 76, 6},
		{"nct_full_range", 12, 8},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := &RunContext{StateDir: t.TempDir()}
			cfgPath := filepath.Join(t.TempDir(), "config.yaml")

			hwmonRoot := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
			stageSensorFixture(t, filepath.Join(hwmonRoot, "hwmon0"), "coretemp",
				map[int]string{1: "Package id 0"})

			seedProbeCheckpoint(t, rc, ProbeArtifact{
				Fans: []ProbedFan{
					{Index: 1, PWMPath: "/sys/hwmon0/pwm1", RPMPath: "/sys/hwmon0/fan1_input", ChipName: "x", LabelHint: "Fan1"},
				},
			})
			seedPolarityCheckpoint(t, rc, PolarityArtifact{
				Results: []PolarityFanResult{{PWMPath: "/sys/hwmon0/pwm1", Polarity: "normal"}},
			})
			seedCalibrateCheckpoint(t, rc, CalibrateArtifact{
				Results: []CalibrateFanResult{
					{
						PWMPath:   "/sys/hwmon0/pwm1",
						StartPWM:  tc.startPWM,
						MaxRPM:    2000,
						SweepMode: "pwm",
						// Linear monotonic curve so saturation knee
						// lands at PWM=255 (no clamp): decoupled from
						// the anchor-density check.
						Curve: []CalibrateCurvePoint{
							{PWM: tc.startPWM, RPM: 800},
							{PWM: 128, RPM: 1400},
							{PWM: 255, RPM: 2000},
						},
					},
				},
			})

			out := (ApplyPhase{ConfigPath: cfgPath, HwmonRoot: hwmonRoot}).Execute(context.Background(), rc)
			if out.Status != StatusSuccess {
				t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
			}

			body, _ := os.ReadFile(cfgPath)
			var cfg config.Config
			if err := yaml.Unmarshal(body, &cfg); err != nil {
				t.Fatalf("parse config: %v", err)
			}
			if len(cfg.Curves) != 1 {
				t.Fatalf("want 1 curve, got %d", len(cfg.Curves))
			}
			pts := cfg.Curves[0].Points
			if got := len(pts); got != tc.wantAnchors {
				t.Errorf("startPWM=%d -> anchors %d, want %d (anchors=%s)",
					tc.startPWM, got, tc.wantAnchors, anchorSummary(pts))
			}
			if pts[0].Temp != 40 {
				t.Errorf("first anchor Temp = %v, want 40", pts[0].Temp)
			}
			if pts[len(pts)-1].Temp != 90 {
				t.Errorf("last anchor Temp = %v, want 90", pts[len(pts)-1].Temp)
			}
			// Bottom PWMPct: at least minSpinPctFloor, and at least
			// StartPWM-as-percent if that is higher.
			gotBottom := uint8(0)
			if pts[0].PWMPct != nil {
				gotBottom = *pts[0].PWMPct
			}
			expBottom := uint8(int(tc.startPWM) * 100 / 255)
			if expBottom < minSpinPctFloor {
				expBottom = minSpinPctFloor
			}
			if gotBottom < expBottom {
				t.Errorf("bottom PWMPct = %d, want >= %d (StartPWM=%d)",
					gotBottom, expBottom, tc.startPWM)
			}
			if pts[len(pts)-1].PWMPct == nil || *pts[len(pts)-1].PWMPct != 100 {
				t.Errorf("top PWMPct = %v, want 100 (linear curve, no knee)", pts[len(pts)-1].PWMPct)
			}

			for i := 1; i < len(pts); i++ {
				if pts[i].Temp <= pts[i-1].Temp {
					t.Errorf("anchor %d Temp %v <= prev %v", i, pts[i].Temp, pts[i-1].Temp)
				}
				if *pts[i].PWMPct < *pts[i-1].PWMPct {
					t.Errorf("anchor %d PWMPct %d < prev %d", i, *pts[i].PWMPct, *pts[i-1].PWMPct)
				}
			}
		})
	}
}

// TestApplyPhase_PerFanCurveSaturationKneeCap pins the saturation-knee
// behaviour: when a fan's Curve[] shows RPM plateauing or dropping
// above some PWM (vendor-EC clamping, mechanical ceiling), the per-fan
// curve's top anchor PWMPct must cap at the knee — not drive the fan
// to PWM=255 where additional duty cycle produces noise without
// airflow (#1272).
func TestApplyPhase_PerFanCurveSaturationKneeCap(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	hwmonRoot := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	stageSensorFixture(t, filepath.Join(hwmonRoot, "hwmon0"), "coretemp",
		map[int]string{1: "Package id 0"})

	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", RPMPath: "/sys/hwmon0/fan1_input", ChipName: "nct6687", LabelHint: "ClampedFan"},
		},
	})
	seedPolarityCheckpoint(t, rc, PolarityArtifact{
		Results: []PolarityFanResult{{PWMPath: "/sys/hwmon0/pwm1", Polarity: "normal"}},
	})
	// Dell-SMM-style curve: rises monotonically to PWM=165, then EC
	// clamps and RPM drops back. MaxRPM=2112, 95% = 2006, last sample
	// at or above 2006 in the rising envelope is PWM=165 (RPM=2112).
	// Expected knee = 165 / 255 = 64.7% -> uint8(64).
	seedCalibrateCheckpoint(t, rc, CalibrateArtifact{
		Results: []CalibrateFanResult{
			{
				PWMPath:   "/sys/hwmon0/pwm1",
				StartPWM:  76,
				MaxRPM:    2112,
				SweepMode: "pwm",
				Curve: []CalibrateCurvePoint{
					{PWM: 76, RPM: 1401},
					{PWM: 89, RPM: 1567},
					{PWM: 102, RPM: 1617},
					{PWM: 114, RPM: 1680},
					{PWM: 127, RPM: 1850},
					{PWM: 140, RPM: 1980},
					{PWM: 153, RPM: 2055},
					{PWM: 165, RPM: 2112},
					{PWM: 178, RPM: 2088},
					{PWM: 191, RPM: 1595},
					{PWM: 204, RPM: 1586},
					{PWM: 217, RPM: 1601},
					{PWM: 230, RPM: 1648},
					{PWM: 255, RPM: 1648},
				},
			},
		},
	})

	out := (ApplyPhase{ConfigPath: cfgPath, HwmonRoot: hwmonRoot}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}

	body, _ := os.ReadFile(cfgPath)
	var cfg config.Config
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if len(cfg.Curves) != 1 {
		t.Fatalf("want 1 curve, got %d", len(cfg.Curves))
	}
	pts := cfg.Curves[0].Points
	top := *pts[len(pts)-1].PWMPct
	// 165 * 100 / 255 = 64. The knee must cap at this or just under.
	if top > 70 {
		t.Errorf("top anchor PWMPct = %d, expected <= 70 (knee at PWM=165 -> %d%%): anchors=%s",
			top, 165*100/255, anchorSummary(pts))
	}
}

// sensorNames extracts sensor names for readable test failure messages.
func sensorNames(ss []config.Sensor) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}

// anchorSummary renders a tiny curve summary for readable failures.
func anchorSummary(pts []config.CurvePoint) string {
	parts := make([]string, len(pts))
	for i, p := range pts {
		pwmStr := "-"
		if p.PWMPct != nil {
			pwmStr = fmt.Sprintf("%d", *p.PWMPct)
		}
		parts[i] = fmt.Sprintf("(%.0fC %s%%)", p.Temp, pwmStr)
	}
	return "{" + joinStrings(parts, " ") + "}"
}

func joinStrings(a []string, sep string) string {
	out := ""
	for i, s := range a {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}
