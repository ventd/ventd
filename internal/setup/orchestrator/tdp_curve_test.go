package orchestrator

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/config"
)

// TestTDPAggressivenessGamma verifies the TDP→gamma mapping anchored
// to the issue #1280 acceptance: high TDP gets a concave shape (gamma
// < 1, ramps fast), low TDP gets a convex shape (gamma > 1, ramps
// gently), the reference rig CPUTDPW=0 falls back to linear (gamma=1).
func TestTDPAggressivenessGamma(t *testing.T) {
	tests := []struct {
		name      string
		cpuTDPW   int
		wantGamma float64
	}{
		{"unknown_TDP_falls_back_to_linear", 0, 1.0},
		{"35W_mini_PC_floor_gives_high_gamma", 10, 2.0},
		{"35W_exact_low_band_floor", 35, 2.0},
		{"250W_HEDT_ceiling_gives_low_gamma", 250, 0.5},
		{"300W_above_ceiling_clamps", 300, 0.5},
		{"125W_midpoint_interpolates", 125, 2.0 + (125.0-35.0)/(250.0-35.0)*(0.5-2.0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tdpAggressivenessGamma(tc.cpuTDPW)
			if math.Abs(got-tc.wantGamma) > 1e-9 {
				t.Errorf("tdpAggressivenessGamma(%d) = %v, want %v", tc.cpuTDPW, got, tc.wantGamma)
			}
		})
	}
}

// TestShapePWMPct verifies the endpoint pinning and gamma-monotonicity
// — the issue #1280 spec is "keeps the bottom anchor pinned at StartPWM
// and the top at the saturation knee, but compresses the middle".
func TestShapePWMPct(t *testing.T) {
	const bottom, top uint8 = 30, 80

	t.Run("endpoints_are_pinned_regardless_of_gamma", func(t *testing.T) {
		for _, gamma := range []float64{0.5, 1.0, 2.0} {
			if got := shapePWMPct(0, gamma, bottom, top); got != bottom {
				t.Errorf("gamma=%v: shape(0) = %d, want %d", gamma, got, bottom)
			}
			if got := shapePWMPct(1, gamma, bottom, top); got != top {
				t.Errorf("gamma=%v: shape(1) = %d, want %d", gamma, got, top)
			}
		}
	})

	t.Run("low_TDP_gamma_2_gentle_middle_band", func(t *testing.T) {
		// gamma=2 (low TDP, convex shape): at midpoint fraction=0.5,
		// shape = 0.25, so PWM% is closer to bottom than to top.
		mid := shapePWMPct(0.5, 2.0, bottom, top)
		// Linear midpoint would be 55%. gamma=2 should pull below.
		if mid >= 55 {
			t.Errorf("gamma=2 midpoint should be below linear (<55), got %d", mid)
		}
		if mid <= bottom {
			t.Errorf("gamma=2 midpoint should still be above bottom (%d), got %d", bottom, mid)
		}
	})

	t.Run("high_TDP_gamma_0_5_aggressive_middle_band", func(t *testing.T) {
		// gamma=0.5 (high TDP, concave shape): at midpoint, shape ≈
		// 0.707, so PWM% is closer to top than to bottom.
		mid := shapePWMPct(0.5, 0.5, bottom, top)
		// Linear midpoint = 55%. gamma=0.5 should pull above.
		if mid <= 55 {
			t.Errorf("gamma=0.5 midpoint should be above linear (>55), got %d", mid)
		}
		if mid >= top {
			t.Errorf("gamma=0.5 midpoint should still be below top (%d), got %d", top, mid)
		}
	})

	t.Run("linear_gamma_1_matches_old_behaviour", func(t *testing.T) {
		// gamma=1 must reproduce the pre-#1280 linear shape exactly —
		// the cpuTDPW=0 fallback is a strict no-regression contract.
		for _, frac := range []float64{0.25, 0.5, 0.75} {
			want := uint8(math.Round(float64(bottom) + frac*float64(top-bottom)))
			got := shapePWMPct(frac, 1.0, bottom, top)
			if got != want {
				t.Errorf("gamma=1 frac=%v: got %d, want %d (linear)", frac, got, want)
			}
		}
	})
}

// TestApplyPhase_CurveAggressivenessScalesWithTDP wires the whole
// thing: probe artifact carries CPUTDPW → apply emits curves whose
// middle-band anchor is concave (higher PWM%) for a high-TDP host
// and convex (lower PWM%) for a low-TDP host across the same
// temperature anchors. This is the #1280 acceptance test against the
// 13900K-vs-J4125 example.
func TestApplyPhase_CurveAggressivenessScalesWithTDP(t *testing.T) {
	tests := []struct {
		name    string
		cpuTDPW int
	}{
		{"low_TDP_10W", 10},
		{"high_TDP_125W", 125},
	}

	midPctByTDP := make(map[int]int)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			rc := &RunContext{StateDir: stateDir}
			hwmonRoot := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
			stageSensorFixture(t, filepath.Join(hwmonRoot, "hwmon0"), "coretemp",
				map[int]string{1: "Package id 0"})
			cfgPath := filepath.Join(t.TempDir(), "config.yaml")

			seedProbeCheckpoint(t, rc, ProbeArtifact{
				Fans: []ProbedFan{
					{Index: 1, PWMPath: "/sys/hwmon0/pwm1", RPMPath: "/sys/hwmon0/fan1_input", ChipName: "nct6687", LabelHint: "Cpu Fan"},
				},
				CPUTDPW: tc.cpuTDPW,
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
				t.Fatalf("apply status=%q detail=%q", out.Status, out.Detail)
			}

			body, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			var cfg config.Config
			if err := yaml.Unmarshal(body, &cfg); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(cfg.Curves) == 0 || len(cfg.Curves[0].Points) < 3 {
				t.Fatalf("expected ≥3 curve points, got %+v", cfg.Curves)
			}
			midIdx := len(cfg.Curves[0].Points) / 2
			midPt := cfg.Curves[0].Points[midIdx]
			if midPt.PWMPct == nil {
				t.Fatalf("midpoint PWMPct nil: %+v", midPt)
			}
			midPctByTDP[tc.cpuTDPW] = int(*midPt.PWMPct)
		})
	}
	if midPctByTDP[125] <= midPctByTDP[10] {
		t.Errorf("expected high-TDP midpoint PWM%% > low-TDP midpoint PWM%%; got high=%d low=%d",
			midPctByTDP[125], midPctByTDP[10])
	}
}

// TestReadRAPLTDPW verifies the parser handles the two layouts the
// kernel has used and falls through to 0 when neither is readable.
func TestReadRAPLTDPW(t *testing.T) {
	t.Run("intel-rapl_nested_layout", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "intel-rapl", "intel-rapl:0")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "constraint_0_power_limit_uw"), []byte("125000000\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readRAPLTDPW(root); got != 125 {
			t.Errorf("readRAPLTDPW = %d, want 125", got)
		}
	})

	t.Run("intel-rapl_flat_layout", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "intel-rapl:0")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "constraint_0_power_limit_uw"), []byte("35000000\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readRAPLTDPW(root); got != 35 {
			t.Errorf("readRAPLTDPW = %d, want 35", got)
		}
	})

	t.Run("no_rapl_returns_zero", func(t *testing.T) {
		root := t.TempDir()
		if got := readRAPLTDPW(root); got != 0 {
			t.Errorf("readRAPLTDPW missing = %d, want 0", got)
		}
	})

	t.Run("malformed_returns_zero", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "intel-rapl:0")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "constraint_0_power_limit_uw"), []byte("not-a-number\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readRAPLTDPW(root); got != 0 {
			t.Errorf("readRAPLTDPW malformed = %d, want 0", got)
		}
	})
}

// TestProbePhase_PopulatesCPUTDPW exercises the wiring: the probe
// phase reads the fixture powercap tree and emits the value in the
// artifact. ApplyPhase then consumes it via probeArt.CPUTDPW.
func TestProbePhase_PopulatesCPUTDPW(t *testing.T) {
	hwmonRoot := filepath.Join(t.TempDir(), "hwmon")
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	powercapRoot := t.TempDir()
	dir := filepath.Join(powercapRoot, "intel-rapl", "intel-rapl:0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "constraint_0_power_limit_uw"), []byte("65000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := &RunContext{HwmonRoot: hwmonRoot}
	out := (ProbePhase{PowercapRoot: powercapRoot}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	var art ProbeArtifact
	if err := json.Unmarshal(out.Artifact, &art); err != nil {
		t.Fatal(err)
	}
	if art.CPUTDPW != 65 {
		t.Errorf("CPUTDPW = %d, want 65", art.CPUTDPW)
	}
}
