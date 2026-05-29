package controller

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/confidence/layer_a"
)

// RULE-CTRL-SMART-RELAX-FLOOR: a converged smart-mode channel may relax the fan
// at most RelaxMargin PWM units below the reactive curve's current value — the
// floor is max(MinPWM, ReactivePWM − RelaxMargin), and RelaxMargin==0 forbids
// any below-curve relaxation (boost-only). This file also covers the helper
// relaxFloorPWM and the setpoint-fallback derivation DeriveSmartSetpointC.

func TestRelaxFloorPWM(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                     string
		reactive, minPWM, margin uint8
		want                     uint8
	}{
		{"margin below reactive", 150, 0, 25, 125},
		{"margin zero floors at reactive", 150, 0, 0, 150},
		{"margin exceeds reactive clamps to minPWM", 30, 20, 100, 20},
		{"floor raised to minPWM", 100, 90, 25, 90},
		{"reactive already at min", 80, 80, 25, 80},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relaxFloorPWM(tc.reactive, tc.minPWM, tc.margin); got != tc.want {
				t.Fatalf("relaxFloorPWM(%d,%d,%d) = %d, want %d",
					tc.reactive, tc.minPWM, tc.margin, got, tc.want)
			}
		})
	}
}

// TestRelaxFloor_BoundsConvergedBelowCurveRelax drives a converged channel
// (SeenFirstContact=true, so the one-shot first-contact clamp is out of the
// way) with a cold sensor so the IMC-PI wants to relax the fan well below the
// reactive curve. The output must never drop below the relax floor, and the
// clamp must engage. Binds RULE-CTRL-SMART-RELAX-FLOOR.
func TestRelaxFloor_BoundsConvergedBelowCurveRelax(t *testing.T) {
	t.Parallel()
	const margin uint8 = 10
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced, PWMUnitMax: 255})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.SensorTemp = 20.0 // well below setpoint (60) ⇒ integrator relaxes the fan
	in.RelaxMargin = margin
	in.LayerA = &layer_a.Snapshot{
		ChannelID:        in.ChannelID,
		Tier:             layer_a.TierRPMTach,
		R8Ceiling:        1.0,
		ConfA:            0.8,
		SeenFirstContact: true, // converged: first-contact clamp will not fire
	}
	floor := in.ReactivePWM - margin
	var sawClamp bool
	for i := 0; i < 6000; i++ {
		in.Now = in.Now.Add(2 * time.Second)
		r := bc.Compute(in)
		if r.OutputPWM < floor {
			t.Fatalf("tick %d: output=%d dropped below relax floor=%d (reactive=%d margin=%d)",
				i, r.OutputPWM, floor, in.ReactivePWM, margin)
		}
		if r.RelaxFloorClamped {
			sawClamp = true
			if r.OutputPWM != floor {
				t.Fatalf("tick %d: clamp set but output=%d != floor=%d", i, r.OutputPWM, floor)
			}
		}
	}
	if !sawClamp {
		t.Fatal("relax floor never engaged after 6000 ticks: predictive never tried to relax past the margin")
	}
}

// TestRelaxFloor_ZeroMargin_NeverBelowReactive: RelaxMargin==0 is the boost-only
// floor — a converged predictive estimate may never settle below the reactive
// curve, only boost above it.
func TestRelaxFloor_ZeroMargin_NeverBelowReactive(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced, PWMUnitMax: 255})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.SensorTemp = 20.0 // would relax hard, but margin 0 forbids below-curve
	in.RelaxMargin = 0
	in.LayerA = &layer_a.Snapshot{
		ChannelID:        in.ChannelID,
		Tier:             layer_a.TierRPMTach,
		R8Ceiling:        1.0,
		ConfA:            0.8,
		SeenFirstContact: true,
	}
	for i := 0; i < 1000; i++ {
		in.Now = in.Now.Add(2 * time.Second)
		r := bc.Compute(in)
		if r.OutputPWM < in.ReactivePWM {
			t.Fatalf("tick %d: boost-only (margin 0) but output=%d below reactive=%d",
				i, r.OutputPWM, in.ReactivePWM)
		}
	}
}

func writeSysfs(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDeriveSmartSetpointC covers the fallback-setpoint derivation: crit when
// present, CPU-model Tjmax for a CPU-labelled sensor with no crit, the
// [smartSetpointMinC, smartSetpointMaxC] clamp, and unresolved → (0, false).
func TestDeriveSmartSetpointC(t *testing.T) {
	t.Parallel()
	tjmax := func() float64 { return 100.0 }

	t.Run("crit present yields crit minus margin", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		in := filepath.Join(dir, "temp1_input")
		writeSysfs(t, in, "45000")
		writeSysfs(t, filepath.Join(dir, "temp1_crit"), "95000") // 95°C
		sp, ok := DeriveSmartSetpointC(in, tjmax)
		if !ok || sp != 95.0-smartSetpointMarginBelowLimitC {
			t.Fatalf("got (%v, %v), want (65, true)", sp, ok)
		}
	})

	t.Run("no crit CPU label uses Tjmax minus margin", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		in := filepath.Join(dir, "temp1_input")
		writeSysfs(t, in, "45000")
		writeSysfs(t, filepath.Join(dir, "temp1_label"), "Tctl")
		sp, ok := DeriveSmartSetpointC(in, tjmax)
		if !ok || sp != 100.0-smartSetpointMarginBelowLimitC {
			t.Fatalf("got (%v, %v), want (70, true)", sp, ok)
		}
	})

	t.Run("no crit non-CPU label is unresolved", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		in := filepath.Join(dir, "temp1_input")
		writeSysfs(t, in, "45000")
		writeSysfs(t, filepath.Join(dir, "temp1_label"), "VRM MOS")
		if _, ok := DeriveSmartSetpointC(in, tjmax); ok {
			t.Fatal("a VRM sensor with no crit must be unresolved (boost-only)")
		}
	})

	t.Run("low crit clamps up to min", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		in := filepath.Join(dir, "temp1_input")
		writeSysfs(t, in, "40000")
		writeSysfs(t, filepath.Join(dir, "temp1_crit"), "60000") // 60-30=30 → clamp 45
		sp, ok := DeriveSmartSetpointC(in, tjmax)
		if !ok || sp != smartSetpointMinC {
			t.Fatalf("got (%v, %v), want (%v, true)", sp, ok, smartSetpointMinC)
		}
	})

	t.Run("high crit clamps down to max", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		in := filepath.Join(dir, "temp1_input")
		writeSysfs(t, in, "40000")
		writeSysfs(t, filepath.Join(dir, "temp1_crit"), "140000") // 140-30=110 → clamp 90
		sp, ok := DeriveSmartSetpointC(in, tjmax)
		if !ok || sp != smartSetpointMaxC {
			t.Fatalf("got (%v, %v), want (%v, true)", sp, ok, smartSetpointMaxC)
		}
	})

	t.Run("no files is unresolved", func(t *testing.T) {
		t.Parallel()
		if _, ok := DeriveSmartSetpointC(filepath.Join(t.TempDir(), "temp1_input"), tjmax); ok {
			t.Fatal("missing sysfs files must be unresolved")
		}
	})

	t.Run("nil tjmaxFn with CPU label and no crit is unresolved", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		in := filepath.Join(dir, "temp1_input")
		writeSysfs(t, in, "45000")
		writeSysfs(t, filepath.Join(dir, "temp1_label"), "CPU")
		if _, ok := DeriveSmartSetpointC(in, nil); ok {
			t.Fatal("nil tjmaxFn with no crit must be unresolved")
		}
	})
}
