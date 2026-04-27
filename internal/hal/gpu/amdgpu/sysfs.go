// Package amdgpu implements AMD GPU fan control via amdgpu sysfs.
// RDNA1/2 uses pwm1/pwm1_enable (duty_0_255). RDNA3+ uses the
// gpu_od/fan_ctrl/fan_curve interface (percentage_0_100).
package amdgpu

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrRDNA3UseFanCurve is returned when a direct pwm1 write is attempted
// on an RDNA3/4 device that requires the fan_curve interface (RULE-GPU-PR2D-07).
var ErrRDNA3UseFanCurve = errors.New("amdgpu: RDNA3+ requires fan_curve interface, not pwm1 writes")

// ErrAMDOverdriveDisabled is returned when a write is attempted without the
// amd_overdrive experimental flag (RULE-EXPERIMENTAL-AMD-OVERDRIVE-01).
var ErrAMDOverdriveDisabled = errors.New("amdgpu: AMD GPU fan control requires --enable-amd-overdrive " +
	"(sets amdgpu.ppfeaturemask kernel taint; see docs/experimental/amd-overdrive.md)")

// CardInfo holds sysfs paths for a discovered AMD GPU card.
type CardInfo struct {
	// CardPath is the /sys/class/drm/card* directory.
	CardPath string
	// HwmonPath is the hwmon directory found by matching name=="amdgpu".
	HwmonPath string
	// HasFanCurve reports whether gpu_od/fan_ctrl/fan_curve is present (RDNA3+).
	HasFanCurve bool
	// AMDOverdrive mirrors the --enable-amd-overdrive experimental flag. All
	// write paths check this before touching hardware (RULE-EXPERIMENTAL-AMD-OVERDRIVE-01).
	AMDOverdrive bool
}

// Enumerate discovers AMD GPU cards by scanning /sys/class/drm/card*/device/uevent
// and finding those whose hwmon/*/name == "amdgpu". Never hard-codes hwmonN numbers.
func Enumerate(sysRoot string) ([]CardInfo, error) {
	drm := filepath.Join(sysRoot, "class", "drm")
	entries, err := os.ReadDir(drm)
	if err != nil {
		return nil, fmt.Errorf("amdgpu: read drm dir: %w", err)
	}

	var cards []CardInfo
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "card") || strings.Contains(name, "-") {
			continue
		}
		cardPath := filepath.Join(drm, name)
		info, err := probeCard(cardPath)
		if err != nil || info == nil {
			continue
		}
		cards = append(cards, *info)
	}
	return cards, nil
}

// probeCard inspects a single card directory for amdgpu hwmon and fan_curve.
func probeCard(cardPath string) (*CardInfo, error) {
	hwmonDir := filepath.Join(cardPath, "device", "hwmon")
	hwEntries, err := os.ReadDir(hwmonDir)
	if err != nil {
		return nil, nil //nolint:nilerr // no hwmon → not amdgpu or not relevant
	}

	var hwmonPath string
	for _, hw := range hwEntries {
		nameFile := filepath.Join(hwmonDir, hw.Name(), "name")
		raw, err := os.ReadFile(nameFile)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(raw)) == "amdgpu" {
			hwmonPath = filepath.Join(hwmonDir, hw.Name())
			break
		}
	}
	if hwmonPath == "" {
		return nil, nil
	}

	fanCurvePath := filepath.Join(cardPath, "device", "gpu_od", "fan_ctrl", "fan_curve")
	_, err = os.Stat(fanCurvePath)
	hasFanCurve := err == nil

	return &CardInfo{
		CardPath:    cardPath,
		HwmonPath:   hwmonPath,
		HasFanCurve: hasFanCurve,
	}, nil
}

// ReadFanRPM reads the current fan RPM from hwmon fan1_input.
func (c *CardInfo) ReadFanRPM() (int, error) {
	raw, err := os.ReadFile(filepath.Join(c.HwmonPath, "fan1_input"))
	if err != nil {
		return 0, fmt.Errorf("amdgpu: read fan1_input: %w", err)
	}
	var rpm int
	if _, err := fmt.Sscan(strings.TrimSpace(string(raw)), &rpm); err != nil {
		return 0, fmt.Errorf("amdgpu: parse fan1_input: %w", err)
	}
	return rpm, nil
}

// WritePWM writes a duty-cycle value (0–255) to pwm1 for RDNA1/2 cards.
// Returns ErrAMDOverdriveDisabled when --enable-amd-overdrive is not set.
// Returns ErrRDNA3UseFanCurve when called on an RDNA3+ card.
func (c *CardInfo) WritePWM(value uint8) error {
	if !c.AMDOverdrive {
		return ErrAMDOverdriveDisabled
	}
	if c.HasFanCurve {
		return ErrRDNA3UseFanCurve
	}
	enablePath := filepath.Join(c.HwmonPath, "pwm1_enable")
	pwmPath := filepath.Join(c.HwmonPath, "pwm1")

	if err := os.WriteFile(enablePath, []byte("1"), 0o644); err != nil {
		return fmt.Errorf("amdgpu: set pwm1_enable=1: %w", err)
	}
	if err := os.WriteFile(pwmPath, []byte(fmt.Sprintf("%d", value)), 0o644); err != nil {
		return fmt.Errorf("amdgpu: write pwm1: %w", err)
	}
	return nil
}

// RestoreAuto returns the card to firmware auto mode (pwm1_enable=2).
func (c *CardInfo) RestoreAuto() error {
	if c.HasFanCurve {
		return resetFanCurve(c.CardPath)
	}
	enablePath := filepath.Join(c.HwmonPath, "pwm1_enable")
	if err := os.WriteFile(enablePath, []byte("2"), 0o644); err != nil {
		return fmt.Errorf("amdgpu: restore pwm1_enable=2: %w", err)
	}
	return nil
}
