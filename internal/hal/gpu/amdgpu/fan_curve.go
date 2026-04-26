package amdgpu

import (
	"fmt"
	"os"
	"path/filepath"
)

// FanCurvePoint is one anchor point for the RDNA3+ 5-point fan curve.
// Temp is in Celsius, Pct is percentage 0–100.
type FanCurvePoint struct {
	Index int
	Temp  int
	Pct   int
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
	defer f.Close()

	for _, p := range points {
		line := fmt.Sprintf("%d %d %d\n", p.Index, p.Temp, p.Pct)
		if _, err := f.WriteString(line); err != nil {
			return fmt.Errorf("amdgpu: write fan_curve point %d: %w", p.Index, err)
		}
	}
	// Commit the curve.
	if _, err := f.WriteString("c\n"); err != nil {
		return fmt.Errorf("amdgpu: commit fan_curve: %w", err)
	}
	return nil
}

// resetFanCurve resets the RDNA3+ fan curve to firmware default via "r".
func resetFanCurve(cardPath string) error {
	curvePath := filepath.Join(cardPath, "device", "gpu_od", "fan_ctrl", "fan_curve")
	if err := os.WriteFile(curvePath, []byte("r\n"), 0o644); err != nil {
		return fmt.Errorf("amdgpu: reset fan_curve: %w", err)
	}
	return nil
}
