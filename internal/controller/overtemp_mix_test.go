package controller

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
)

// TestFailsafeSensorNames pins the leaf-sensor resolution the mix-curve
// failsafe depends on: a single-sensor curve yields its one sensor; a mix curve
// yields the union of its sources' leaf sensors (recursively, deduped); and a
// cyclic/dangling config is handled without panic.
func TestFailsafeSensorNames(t *testing.T) {
	cfg := &config.Config{Curves: []config.CurveConfig{
		{Name: "cpu_lin", Type: "linear", Sensor: "cpu"},
		{Name: "gpu_lin", Type: "linear", Sensor: "gpu"},
		{Name: "case_mix", Type: "mix", Function: "max", Sources: []string{"cpu_lin", "gpu_lin"}},
		{Name: "nested", Type: "mix", Function: "max", Sources: []string{"case_mix", "cpu_lin"}}, // cpu twice → dedup
		{Name: "cyc", Type: "mix", Function: "max", Sources: []string{"cyc"}},                    // self-cycle
		{Name: "dangling", Type: "mix", Function: "max", Sources: []string{"nope"}},              // missing source
	}}
	c := &Controller{}
	curve := func(name string) config.CurveConfig {
		cv, ok := findCurve(cfg, name)
		if !ok {
			t.Fatalf("curve %q not found", name)
		}
		return cv
	}
	eq := func(name string, got, want []string) {
		t.Helper()
		set := map[string]bool{}
		for _, g := range got {
			set[g] = true
		}
		if len(got) != len(want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
			return
		}
		for _, w := range want {
			if !set[w] {
				t.Errorf("%s: got %v, missing %q", name, got, w)
			}
		}
	}
	eq("single", c.failsafeSensorNames(cfg, curve("cpu_lin")), []string{"cpu"})
	eq("mix", c.failsafeSensorNames(cfg, curve("case_mix")), []string{"cpu", "gpu"})
	eq("nested-dedup", c.failsafeSensorNames(cfg, curve("nested")), []string{"cpu", "gpu"})
	eq("cycle", c.failsafeSensorNames(cfg, curve("cyc")), nil)
	eq("dangling", c.failsafeSensorNames(cfg, curve("dangling")), nil)
}

// TestTick_OvertempFailsafeFiresOnMixCurveLeafSensor closes the #1442 gap: a fan
// bound to a MIX curve (case fan on max(cpu,gpu)) must be forced to full speed
// when ANY leaf sensor crits — even though the mix curve has no scalar Sensor of
// its own. Before this, mix-curve fans got no over-temperature failsafe at all.
// Here the GPU crits while the CPU is cool; the fan must be forced to 255,
// bypassing the operator max_pwm cap (200).
func TestTick_OvertempFailsafeFiresOnMixCurveLeafSensor(t *testing.T) {
	ff := newFakeFan(t)
	chipDir := filepath.Dir(ff.tempPath)
	// Two sensors in the same chip: temp1=cpu, temp2=gpu. crit 90 → engage 94.
	cpuTemp := filepath.Join(chipDir, "temp1_input")
	gpuTemp := filepath.Join(chipDir, "temp2_input")
	writeTempAttr(t, chipDir, "temp1_crit", "90000")
	writeTempAttr(t, chipDir, "temp2_crit", "90000")
	const cap = 200

	cfg := &config.Config{
		Sensors: []config.Sensor{
			{Name: "cpu", Type: "hwmon", Path: cpuTemp},
			{Name: "gpu", Type: "hwmon", Path: gpuTemp},
		},
		Fans: []config.Fan{{Name: "case", Type: "hwmon", PWMPath: ff.pwmPath, MinPWM: 40, MaxPWM: cap}},
		Curves: []config.CurveConfig{
			{Name: "cpu_lin", Type: "linear", Sensor: "cpu", MinTemp: 40, MaxTemp: 80, MinPWM: 0, MaxPWM: 255},
			{Name: "gpu_lin", Type: "linear", Sensor: "gpu", MinTemp: 40, MaxTemp: 80, MinPWM: 0, MaxPWM: 255},
			{Name: "case_mix", Type: "mix", Function: "max", Sources: []string{"cpu_lin", "gpu_lin"}},
		},
		Controls: []config.Control{{Fan: "case", Curve: "case_mix"}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "case", "case_mix")
	c.tjmaxFn = func() float64 { return 0 }

	// CPU cool, GPU hot (above engage). First tick: debounce holds, so the write
	// is the capped mix-curve value (max → 255 clamped to cap=200), not 255.
	writeTempAttr(t, chipDir, "temp1_input", "55000")
	writeTempAttr(t, chipDir, "temp2_input", "95000")
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got == 255 {
		t.Fatalf("failsafe engaged on first tick (got 255); debounce must hold")
	}

	// Backdate the GPU sensor's over-temp dwell past the debounce → engage on
	// the GPU leaf sensor, forcing full speed past the cap.
	c.emergency["gpu"].overSince = time.Now().Add(-emergencyDebounce - time.Second)
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != 255 {
		t.Errorf("mix-curve failsafe: PWM=%d, want 255 (GPU crit must force full speed past max_pwm=%d)", got, cap)
	}
	if !c.emergency["gpu"].engaged {
		t.Error("GPU leaf-sensor failsafe not engaged")
	}
	// The CPU leaf sensor stayed cool — it must NOT have engaged.
	if cpuSt := c.emergency["cpu"]; cpuSt != nil && cpuSt.engaged {
		t.Error("CPU leaf-sensor failsafe engaged while CPU was cool (55 °C)")
	}
}
