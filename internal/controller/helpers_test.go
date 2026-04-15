package controller

import (
	"testing"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/curve"
)

// TestClamp pins the safety contract used on every PWM write: the curve
// output cannot escape the per-fan [MinPWM, MaxPWM] window. This is the
// last line between a curve bug and a fan stuck at 100% (loud) or 0%
// (no airflow under load).
func TestClamp(t *testing.T) {
	cases := []struct {
		name           string
		v, lo, hi, want uint8
	}{
		{"below_lo_clipped_up", 5, 40, 200, 40},
		{"above_hi_clipped_down", 250, 40, 200, 200},
		{"at_lo_passthrough", 40, 40, 200, 40},
		{"at_hi_passthrough", 200, 40, 200, 200},
		{"inside_passthrough", 100, 40, 200, 100},
		{"zero_below_lo", 0, 40, 200, 40},
		{"max_above_hi", 255, 40, 200, 200},
		{"degenerate_lo_eq_hi", 100, 100, 100, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clamp(tc.v, tc.lo, tc.hi); got != tc.want {
				t.Errorf("clamp(%d,%d,%d) = %d, want %d",
					tc.v, tc.lo, tc.hi, got, tc.want)
			}
		})
	}
}

// TestFindFanByPath pins the (path, type) lookup used at every tick. Both
// fields must match: a hwmon entry with PWMPath="0" must NOT shadow a
// nvidia entry with PWMPath="0".
func TestFindFanByPath(t *testing.T) {
	cfg := &config.Config{
		Fans: []config.Fan{
			{Name: "cpu", Type: "hwmon", PWMPath: "/sys/class/hwmon/hwmon3/pwm1"},
			{Name: "gpu", Type: "nvidia", PWMPath: "0"},
		},
	}
	cases := []struct {
		name       string
		path, ftyp string
		wantOK     bool
		wantName   string
	}{
		{"hwmon_match", "/sys/class/hwmon/hwmon3/pwm1", "hwmon", true, "cpu"},
		{"nvidia_match", "0", "nvidia", true, "gpu"},
		{"path_match_wrong_type", "0", "hwmon", false, ""},
		{"unknown_path", "/sys/class/hwmon/hwmon99/pwm1", "hwmon", false, ""},
		{"empty", "", "hwmon", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := findFanByPath(cfg, tc.path, tc.ftyp)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.Name != tc.wantName {
				t.Errorf("name = %q, want %q", got.Name, tc.wantName)
			}
		})
	}
}

// TestFindCurve pins the by-name lookup used at every tick.
func TestFindCurve(t *testing.T) {
	cfg := &config.Config{
		Curves: []config.CurveConfig{
			{Name: "cpu_curve", Type: "linear"},
			{Name: "gpu_curve", Type: "fixed", Value: 200},
		},
	}
	if got, ok := findCurve(cfg, "cpu_curve"); !ok || got.Type != "linear" {
		t.Errorf("cpu_curve lookup failed: ok=%v got=%+v", ok, got)
	}
	if got, ok := findCurve(cfg, "gpu_curve"); !ok || got.Value != 200 {
		t.Errorf("gpu_curve lookup failed: ok=%v got=%+v", ok, got)
	}
	if _, ok := findCurve(cfg, "missing"); ok {
		t.Errorf("missing curve: ok=true, want false")
	}
}

// TestBuildCurve_Linear pins the linear-curve build path. The returned
// Curve must produce the same outputs as a directly-constructed curve.Linear
// with the same parameters.
func TestBuildCurve_Linear(t *testing.T) {
	got, err := buildCurve(config.CurveConfig{
		Name: "cpu_curve", Type: "linear", Sensor: "cpu",
		MinTemp: 40, MaxTemp: 80, MinPWM: 50, MaxPWM: 200,
	}, nil)
	if err != nil {
		t.Fatalf("buildCurve: %v", err)
	}
	lin, ok := got.(*curve.Linear)
	if !ok {
		t.Fatalf("got %T, want *curve.Linear", got)
	}
	if lin.SensorName != "cpu" || lin.MinTemp != 40 || lin.MaxTemp != 80 ||
		lin.MinPWM != 50 || lin.MaxPWM != 200 {
		t.Errorf("linear params wrong: %+v", lin)
	}
}

// TestBuildCurve_Fixed pins the fixed-curve build path.
func TestBuildCurve_Fixed(t *testing.T) {
	got, err := buildCurve(config.CurveConfig{
		Name: "pump_curve", Type: "fixed", Value: 204,
	}, nil)
	if err != nil {
		t.Fatalf("buildCurve: %v", err)
	}
	fix, ok := got.(*curve.Fixed)
	if !ok {
		t.Fatalf("got %T, want *curve.Fixed", got)
	}
	if fix.Value != 204 {
		t.Errorf("Value = %d, want 204", fix.Value)
	}
}

// TestBuildCurve_Mix pins the mix-curve build path including recursive
// resolution of source curves through the allCurves slice. Mix without
// known sources must fail loudly so a config typo doesn't silently
// degrade to a single-source curve.
func TestBuildCurve_Mix(t *testing.T) {
	all := []config.CurveConfig{
		{Name: "cpu_curve", Type: "linear", Sensor: "cpu", MinTemp: 40, MaxTemp: 80, MinPWM: 50, MaxPWM: 255},
		{Name: "gpu_curve", Type: "linear", Sensor: "gpu", MinTemp: 50, MaxTemp: 85, MinPWM: 60, MaxPWM: 255},
		{Name: "case_curve", Type: "mix", Function: "max",
			Sources: []string{"cpu_curve", "gpu_curve"}},
	}
	got, err := buildCurve(all[2], all)
	if err != nil {
		t.Fatalf("mix build: %v", err)
	}
	mix, ok := got.(*curve.Mix)
	if !ok {
		t.Fatalf("got %T, want *curve.Mix", got)
	}
	if len(mix.Sources) != 2 {
		t.Errorf("mix sources = %d, want 2", len(mix.Sources))
	}
}

// TestBuildCurve_MixUnknownSource pins the fail-loud contract for typo'd
// source names — a missing source must NOT silently drop, it must error.
func TestBuildCurve_MixUnknownSource(t *testing.T) {
	all := []config.CurveConfig{
		{Name: "cpu_curve", Type: "linear", Sensor: "cpu", MinTemp: 40, MaxTemp: 80, MinPWM: 50, MaxPWM: 255},
		{Name: "case_curve", Type: "mix", Function: "max",
			Sources: []string{"cpu_curve", "ghost_curve"}},
	}
	_, err := buildCurve(all[1], all)
	if err == nil {
		t.Fatal("want error for unknown source, got nil")
	}
}

// TestBuildCurve_MixBadFunction pins the unknown-function error path.
func TestBuildCurve_MixBadFunction(t *testing.T) {
	all := []config.CurveConfig{
		{Name: "case_curve", Type: "mix", Function: "average_with_a_twist", Sources: nil},
	}
	_, err := buildCurve(all[0], all)
	if err == nil {
		t.Fatal("want error for unknown mix function, got nil")
	}
}

// TestBuildCurve_UnknownType pins the catch-all error path. New curve
// types added in the future must update both the switch and (typically)
// add a buildCurve test alongside.
func TestBuildCurve_UnknownType(t *testing.T) {
	_, err := buildCurve(config.CurveConfig{Name: "x", Type: "logistic"}, nil)
	if err == nil {
		t.Fatal("want error for unknown type, got nil")
	}
}
