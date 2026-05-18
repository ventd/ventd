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

	// Expect cpu_temp + 4 GPU sensors (2 GPUs × {temp, fan_pct}).
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

// TestApplyPhase_DefaultCurveAnchorDensityScalesWithFanRange pins the
// #1231 fix: a wide-range hwmon fan (e.g. dell_smm with PWM range 179)
// must receive a default curve with more anchors than a narrow-range
// fan so each per-tick PWM step is small enough that the fan can
// physically absorb it without overshoot → opposite swing → audible
// hunting cycle.
//
// Target: ~30 PWM units per segment, so PWM range 179 → 7 anchors,
// PWM range 243 → 9 anchors. Floor is 3 anchors so the monitor-only
// path still gets a usable curve.
func TestApplyPhase_DefaultCurveAnchorDensityScalesWithFanRange(t *testing.T) {
	cases := []struct {
		name        string
		fanMinPWM   uint8
		fanMaxPWM   uint8
		wantAnchors int
	}{
		{"narrow_range_floor_3", 120, 180, 3}, // range 60 → 2 → clamped to 3
		{"dell_smm_class", 76, 255, 6},        // range 179 → 6 (rounded from 5.97)
		{"nct_full_range", 12, 255, 8},        // range 243 → 8 (rounded from 8.1)
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
					{Index: 1, PWMPath: "/sys/hwmon0/pwm1", RPMPath: "/sys/hwmon0/fan1_input", ChipName: "x", LabelHint: "F"},
				},
			})
			seedPolarityCheckpoint(t, rc, PolarityArtifact{
				Results: []PolarityFanResult{{PWMPath: "/sys/hwmon0/pwm1", Polarity: "normal"}},
			})
			seedCalibrateCheckpoint(t, rc, CalibrateArtifact{
				Results: []CalibrateFanResult{
					{PWMPath: "/sys/hwmon0/pwm1", StartPWM: tc.fanMinPWM, MaxRPM: 2000, SweepMode: "pwm"},
				},
			})

			// Patch MaxPWM via a trailing hack: ApplyPhase always sets
			// MaxPWM=255 from buildConfig. To exercise the narrow case
			// without rewiring buildConfig's fan MaxPWM source, we
			// upcast via a custom fixture below.
			out := (ApplyPhase{ConfigPath: cfgPath, HwmonRoot: hwmonRoot}).Execute(context.Background(), rc)
			if out.Status != StatusSuccess {
				t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
			}

			body, _ := os.ReadFile(cfgPath)
			var cfg config.Config
			if err := yaml.Unmarshal(body, &cfg); err != nil {
				t.Fatalf("parse config: %v", err)
			}

			// Synthesise the test's expected fan MaxPWM and re-run the
			// curve-anchor logic directly via the exported helper so the
			// regression test pins defaultCurvePoints's behaviour
			// independent of the buildConfig MaxPWM=255 default.
			fixtureFans := []config.Fan{{
				Type:   "hwmon",
				MinPWM: tc.fanMinPWM,
				MaxPWM: tc.fanMaxPWM,
			}}
			pts := defaultCurvePoints(40, 90, fixtureFans)
			if len(pts) != tc.wantAnchors {
				t.Errorf("range %d → anchors %d, want %d (anchors=%+v)",
					int(tc.fanMaxPWM)-int(tc.fanMinPWM), len(pts), tc.wantAnchors, anchorSummary(pts))
			}
			// First anchor temp = minTemp, last = maxTemp. First PWM%
			// = 20, last = 100. These bound the curve so a single bad
			// anchor doesn't break the cold or thermal-throttle case.
			if pts[0].Temp != 40 {
				t.Errorf("first anchor Temp = %v, want 40", pts[0].Temp)
			}
			if pts[len(pts)-1].Temp != 90 {
				t.Errorf("last anchor Temp = %v, want 90", pts[len(pts)-1].Temp)
			}
			if pts[0].PWMPct == nil || *pts[0].PWMPct != 20 {
				t.Errorf("first PWMPct must be 20 (idle floor)")
			}
			if pts[len(pts)-1].PWMPct == nil || *pts[len(pts)-1].PWMPct != 100 {
				t.Errorf("last PWMPct must be 100 (max ramp)")
			}

			// Strict monotonic Temp + monotonic PWMPct — otherwise the
			// runtime Points evaluator can't interpolate.
			for i := 1; i < len(pts); i++ {
				if pts[i].Temp <= pts[i-1].Temp {
					t.Errorf("anchor %d Temp %v <= prev %v (not strictly ascending)", i, pts[i].Temp, pts[i-1].Temp)
				}
				if *pts[i].PWMPct < *pts[i-1].PWMPct {
					t.Errorf("anchor %d PWMPct %d < prev %d (descending)", i, *pts[i].PWMPct, *pts[i-1].PWMPct)
				}
			}

			// Verify the emitted curve in cfg.Curves matches density
			// for the buildConfig MaxPWM=255 case (which is what
			// ApplyPhase always produces for hwmon fans). Range
			// = 255 - tc.fanMinPWM → expected anchors.
			liveRange := 255 - int(tc.fanMinPWM)
			liveAnchors := 3
			if liveRange > 0 {
				liveAnchors = (liveRange + 15) / 30
				if liveAnchors < 3 {
					liveAnchors = 3
				}
			}
			if got := len(cfg.Curves[0].Points); got != liveAnchors {
				t.Errorf("emitted curve anchors = %d, want %d for live range %d",
					got, liveAnchors, liveRange)
			}
		})
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
		parts[i] = fmt.Sprintf("(%.0f°C %s%%)", p.Temp, pwmStr)
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
