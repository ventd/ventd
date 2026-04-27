package amdgpu

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestAMDGPU_WriteRefusesWhenOverdriveFlagFalse verifies
// RULE-EXPERIMENTAL-AMD-OVERDRIVE-01: WritePWM and WriteFanCurveGated both
// return ErrAMDOverdriveDisabled when AMDOverdrive is false, regardless of
// other card state.
func TestAMDGPU_WriteRefusesWhenOverdriveFlagFalse(t *testing.T) {
	tmp := t.TempDir()

	// Build a minimal fake RDNA1/2 card (no fan_curve).
	hwmonDir := filepath.Join(tmp, "card0", "device", "hwmon", "hwmonX")
	if err := os.MkdirAll(hwmonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hwmonDir, "pwm1"), []byte("128"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hwmonDir, "pwm1_enable"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build a fake fan_curve for the RDNA3 path.
	fanCurveDir := filepath.Join(tmp, "card0", "device", "gpu_od", "fan_ctrl")
	if err := os.MkdirAll(fanCurveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fanCurveDir, "fan_curve"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cardPath := filepath.Join(tmp, "card0")

	t.Run("rdna12_pwm_refused_when_flag_false", func(t *testing.T) {
		card := &CardInfo{
			CardPath:     cardPath,
			HwmonPath:    hwmonDir,
			HasFanCurve:  false,
			AMDOverdrive: false,
		}
		err := card.WritePWM(128)
		if !errors.Is(err, ErrAMDOverdriveDisabled) {
			t.Errorf("WritePWM with AMDOverdrive=false: want ErrAMDOverdriveDisabled, got %v", err)
		}
		// Verify pwm1 was not modified.
		raw, _ := os.ReadFile(filepath.Join(hwmonDir, "pwm1"))
		if string(raw) != "128" {
			t.Errorf("pwm1 was modified despite gate; got %q", string(raw))
		}
	})

	t.Run("rdna3_fan_curve_refused_when_flag_false", func(t *testing.T) {
		card := &CardInfo{
			CardPath:     cardPath,
			HwmonPath:    hwmonDir,
			HasFanCurve:  true,
			AMDOverdrive: false,
		}
		points := []FanCurvePoint{{0, 50, 30}, {1, 70, 50}}
		err := card.WriteFanCurveGated(points)
		if !errors.Is(err, ErrAMDOverdriveDisabled) {
			t.Errorf("WriteFanCurveGated with AMDOverdrive=false: want ErrAMDOverdriveDisabled, got %v", err)
		}
	})

	t.Run("rdna12_pwm_permitted_when_flag_true", func(t *testing.T) {
		card := &CardInfo{
			CardPath:     cardPath,
			HwmonPath:    hwmonDir,
			HasFanCurve:  false,
			AMDOverdrive: true,
		}
		err := card.WritePWM(200)
		if err != nil {
			t.Errorf("WritePWM with AMDOverdrive=true: unexpected error: %v", err)
		}
	})
}
