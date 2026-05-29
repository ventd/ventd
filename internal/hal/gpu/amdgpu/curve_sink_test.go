package amdgpu

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hal"
)

// TestFanCurvePointsFromHAL pins the curve-translation contract: any
// caller-supplied hal.CurvePoint slice is normalised to exactly five anchors
// with strictly-increasing temperatures and non-decreasing, [0,100]-clamped
// percentages — the only shape the gpu_od/fan_ctrl/fan_curve hardware accepts.
func TestFanCurvePointsFromHAL(t *testing.T) {
	t.Run("five_points_passthrough_shape", func(t *testing.T) {
		in := []hal.CurvePoint{
			{TempC: 40, Pct: 20}, {TempC: 55, Pct: 35}, {TempC: 70, Pct: 55},
			{TempC: 85, Pct: 80}, {TempC: 100, Pct: 100},
		}
		got, err := fanCurvePointsFromHAL(in)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != fanCurveAnchorCount {
			t.Fatalf("got %d anchors, want %d", len(got), fanCurveAnchorCount)
		}
		for i, p := range got {
			if p.Index != i {
				t.Errorf("anchor %d Index = %d, want %d", i, p.Index, i)
			}
		}
		if got[0].Temp != 40 || got[len(got)-1].Temp != 100 {
			t.Errorf("temp span = [%d,%d], want [40,100]", got[0].Temp, got[len(got)-1].Temp)
		}
	})

	t.Run("resamples_dense_input_to_five", func(t *testing.T) {
		// A 1°C-resolution ramp 40→90, 0→100%: the controller's natural output.
		in := make([]hal.CurvePoint, 0, 51)
		for tc := 40; tc <= 90; tc++ {
			in = append(in, hal.CurvePoint{TempC: tc, Pct: (tc - 40) * 2})
		}
		got, err := fanCurvePointsFromHAL(in)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != fanCurveAnchorCount {
			t.Fatalf("got %d anchors, want %d", len(got), fanCurveAnchorCount)
		}
		if got[0].Temp != 40 || got[4].Temp != 90 {
			t.Errorf("temp endpoints = [%d,%d], want [40,90]", got[0].Temp, got[4].Temp)
		}
	})

	t.Run("strictly_increasing_temps_and_monotonic_pct", func(t *testing.T) {
		// Decreasing percentage in the source is non-physical; it must be
		// clamped to non-decreasing. Out-of-range percentages clamp to [0,100].
		in := []hal.CurvePoint{
			{TempC: 50, Pct: 120}, {TempC: 60, Pct: 40}, {TempC: 60, Pct: 30},
			{TempC: 70, Pct: -5}, {TempC: 80, Pct: 90},
		}
		got, err := fanCurvePointsFromHAL(in)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < len(got); i++ {
			if got[i].Temp <= got[i-1].Temp {
				t.Errorf("temps not strictly increasing at %d: %d <= %d", i, got[i].Temp, got[i-1].Temp)
			}
			if got[i].Pct < got[i-1].Pct {
				t.Errorf("pct decreased at %d: %d < %d", i, got[i].Pct, got[i-1].Pct)
			}
		}
		for _, p := range got {
			if p.Pct < 0 || p.Pct > 100 {
				t.Errorf("pct %d out of [0,100]", p.Pct)
			}
		}
	})

	t.Run("degenerate_flat_input_gets_increasing_temps", func(t *testing.T) {
		in := []hal.CurvePoint{{TempC: 60, Pct: 50}}
		got, err := fanCurvePointsFromHAL(in)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != fanCurveAnchorCount {
			t.Fatalf("got %d anchors, want %d", len(got), fanCurveAnchorCount)
		}
		for i := 1; i < len(got); i++ {
			if got[i].Temp <= got[i-1].Temp {
				t.Errorf("flat input must still yield increasing temps; got %d <= %d", got[i].Temp, got[i-1].Temp)
			}
		}
	})

	t.Run("empty_input_errors", func(t *testing.T) {
		if _, err := fanCurvePointsFromHAL(nil); err == nil {
			t.Error("empty input must error")
		}
	})
}

// TestBackend_RDNA3CurveSink is the RDNA3/4 curve-upload happy path: a card
// with the fan_curve interface and amd_overdrive enumerates with CapWriteCurve
// (not CapWritePWM), and WriteCurve commits a five-point curve to the sysfs
// fan_curve node. It also pins that the per-tick PWM path is refused on these
// cards and that the channel satisfies the hal.CurveSink contract.
func TestBackend_RDNA3CurveSink(t *testing.T) {
	sysRoot := t.TempDir()
	cardPath := fakeAMDCard(t, sysRoot, true)
	// Navi 31 (RDNA3): IsRDNA4 is false, so the kernel-6.15 gate is a no-op
	// regardless of the host kernel — keeps the test independent of osrelease.
	if err := os.WriteFile(filepath.Join(cardPath, "device", "device"), []byte("0x744c\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var b hal.CurveSink = NewBackend(slog.Default(), sysRoot, true)
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if len(chs) != 1 {
		t.Fatalf("enumerate: got %d channels, want 1", len(chs))
	}
	ch := chs[0]
	if ch.Caps&hal.CapWriteCurve == 0 {
		t.Error("RDNA3/4 + amd_overdrive must advertise CapWriteCurve")
	}
	if ch.Caps&hal.CapWritePWM != 0 {
		t.Error("RDNA3/4 must NOT advertise CapWritePWM (no per-tick duty)")
	}
	if ch.Caps&(hal.CapRead|hal.CapRestore) != (hal.CapRead | hal.CapRestore) {
		t.Error("channel must advertise CapRead|CapRestore")
	}

	// Per-tick PWM write is refused on the fan_curve cards.
	if err := b.Write(ch, 128); !errors.Is(err, ErrRDNA3UseFanCurve) {
		t.Errorf("per-tick Write on RDNA3 = %v, want ErrRDNA3UseFanCurve", err)
	}

	points := []hal.CurvePoint{
		{TempC: 40, Pct: 20}, {TempC: 55, Pct: 35}, {TempC: 70, Pct: 55},
		{TempC: 85, Pct: 80}, {TempC: 100, Pct: 100},
	}
	if err := b.WriteCurve(ch, points); err != nil {
		t.Fatalf("WriteCurve: %v", err)
	}
	raw := readTrim(t, filepath.Join(cardPath, "device", "gpu_od", "fan_ctrl", "fan_curve"))
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	// 5 anchor lines + the "c" commit line.
	if len(lines) != fanCurveAnchorCount+1 {
		t.Fatalf("fan_curve content = %q (%d lines), want %d", raw, len(lines), fanCurveAnchorCount+1)
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "c" {
		t.Errorf("last line = %q, want commit \"c\"", lines[len(lines)-1])
	}
	if !strings.HasPrefix(lines[0], "0 40 20") {
		t.Errorf("first anchor line = %q, want \"0 40 20\"", lines[0])
	}
}

// TestBackend_RDNA3CurveSink_RefusedWithoutOverdrive: without amd_overdrive an
// RDNA3/4 card is monitor-only — no CapWriteCurve — and WriteCurve is refused
// by the overdrive gate without touching the sysfs node.
func TestBackend_RDNA3CurveSink_RefusedWithoutOverdrive(t *testing.T) {
	sysRoot := t.TempDir()
	cardPath := fakeAMDCard(t, sysRoot, true)
	curvePath := filepath.Join(cardPath, "device", "gpu_od", "fan_ctrl", "fan_curve")

	b := NewBackend(slog.Default(), sysRoot, false)
	chs, _ := b.Enumerate(context.Background())
	if len(chs) != 1 {
		t.Fatalf("enumerate: got %d, want 1", len(chs))
	}
	if chs[0].Caps&hal.CapWriteCurve != 0 {
		t.Error("without amd_overdrive RDNA3/4 must NOT advertise CapWriteCurve")
	}
	points := []hal.CurvePoint{{TempC: 40, Pct: 20}, {TempC: 100, Pct: 100}}
	if err := b.WriteCurve(chs[0], points); !errors.Is(err, ErrAMDOverdriveDisabled) {
		t.Errorf("WriteCurve without overdrive = %v, want ErrAMDOverdriveDisabled", err)
	}
	if got := readTrim(t, curvePath); got != "OD_FAN_CURVE:" {
		t.Errorf("fan_curve node was modified despite gate: %q", got)
	}
}
