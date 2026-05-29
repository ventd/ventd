package amdgpu

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ventd/ventd/internal/hal"
)

// fanCurveAnchorCount is the fixed number of anchor points the RDNA3/4
// gpu_od/fan_ctrl/fan_curve interface requires. The firmware interpolates
// between these five points.
const fanCurveAnchorCount = 5

// FanCurvePoint is one anchor point for the RDNA3+ 5-point fan curve.
// Temp is in Celsius, Pct is percentage 0–100.
type FanCurvePoint struct {
	Index int
	Temp  int
	Pct   int
}

// fanCurvePointsFromHAL normalises a caller-supplied hal.CurvePoint slice
// (ascending by TempC) into exactly fanCurveAnchorCount FanCurvePoints with
// strictly-increasing temperatures and non-decreasing percentages — the shape
// the gpu_od/fan_ctrl/fan_curve hardware accepts. The input is resampled to
// five anchors evenly spaced across its temperature span, interpolating the
// percentage at each anchor, so a CurveSink consumer need not know the
// hardware's anchor count (spec-17 PR-1b).
func fanCurvePointsFromHAL(points []hal.CurvePoint) ([]FanCurvePoint, error) {
	if len(points) == 0 {
		return nil, fmt.Errorf("amdgpu: fan_curve: at least one source point required")
	}
	lo := points[0].TempC
	hi := points[len(points)-1].TempC
	if hi <= lo {
		// Degenerate / flat input: spread the anchors 1°C apart from lo so the
		// hardware still receives five strictly-increasing temperatures.
		hi = lo + (fanCurveAnchorCount - 1)
	}
	out := make([]FanCurvePoint, fanCurveAnchorCount)
	prevPct := 0
	for i := 0; i < fanCurveAnchorCount; i++ {
		temp := lo + (hi-lo)*i/(fanCurveAnchorCount-1)
		pct := clampInt(interpPct(points, temp), 0, 100)
		if pct < prevPct {
			// Fan curves are monotonic non-decreasing; the hardware rejects a
			// curve whose percentage falls as temperature rises.
			pct = prevPct
		}
		prevPct = pct
		out[i] = FanCurvePoint{Index: i, Temp: clampInt(temp, 0, 255), Pct: pct}
	}
	// Integer temperature rounding can collide adjacent anchors; the hardware
	// requires strictly-increasing temperatures, so nudge any duplicate up.
	for i := 1; i < len(out); i++ {
		if out[i].Temp <= out[i-1].Temp {
			out[i].Temp = out[i-1].Temp + 1
		}
	}
	return out, nil
}

// interpPct returns the percentage at temperature t from the piecewise-linear
// curve defined by points (ascending by TempC). Below the first anchor it
// holds the first percentage; above the last anchor it holds the last.
func interpPct(points []hal.CurvePoint, t int) int {
	if t <= points[0].TempC {
		return points[0].Pct
	}
	last := points[len(points)-1]
	if t >= last.TempC {
		return last.Pct
	}
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		if t <= b.TempC {
			span := b.TempC - a.TempC
			if span <= 0 {
				return b.Pct
			}
			return a.Pct + (b.Pct-a.Pct)*(t-a.TempC)/span
		}
	}
	return last.Pct
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// WriteFanCurve writes a 5-anchor-point curve to gpu_od/fan_ctrl/fan_curve
// and commits it. The RDNA3+ firmware interpolates between anchor points.
func WriteFanCurve(cardPath string, points []FanCurvePoint) error {
	if len(points) == 0 {
		return fmt.Errorf("amdgpu: fan_curve: at least one anchor point required")
	}
	curvePath := filepath.Join(cardPath, "device", "gpu_od", "fan_ctrl", "fan_curve")

	f, err := os.OpenFile(curvePath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("amdgpu: open fan_curve: %w", err)
	}

	for _, p := range points {
		line := fmt.Sprintf("%d %d %d\n", p.Index, p.Temp, p.Pct)
		if _, err := f.WriteString(line); err != nil {
			_ = f.Close()
			return fmt.Errorf("amdgpu: write fan_curve point %d: %w", p.Index, err)
		}
	}
	// Commit the curve.
	if _, err := f.WriteString("c\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("amdgpu: commit fan_curve: %w", err)
	}
	return f.Close()
}

// WriteFanCurveGated is a CardInfo method that applies the amd_overdrive gate
// (RULE-EXPERIMENTAL-AMD-OVERDRIVE-01) and the RDNA4 kernel-version gate
// (RULE-EXPERIMENTAL-AMD-OVERDRIVE-04) before delegating to WriteFanCurve.
func (c *CardInfo) WriteFanCurveGated(points []FanCurvePoint) error {
	if !c.AMDOverdrive {
		return ErrAMDOverdriveDisabled
	}
	if err := checkRDNA4KernelGate(c.CardPath, osReleasePath); err != nil {
		return err
	}
	return WriteFanCurve(c.CardPath, points)
}

// resetFanCurve resets the RDNA3+ fan curve to firmware default via "r".
func resetFanCurve(cardPath string) error {
	curvePath := filepath.Join(cardPath, "device", "gpu_od", "fan_ctrl", "fan_curve")
	if err := os.WriteFile(curvePath, []byte("r\n"), 0o644); err != nil {
		return fmt.Errorf("amdgpu: reset fan_curve: %w", err)
	}
	return nil
}
