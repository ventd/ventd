// Package xe implements read-only fan RPM monitoring for Intel Arc GPUs.
// Intel Arc (xe driver, kernel 6.12+) and DG2 (i915 driver) expose fan*_input
// attributes via hwmon but provide no userspace write path — fan control is
// firmware-managed. This package contains NO write code paths (RULE-GPU-PR2D-08).
package xe

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CardInfo holds sysfs paths for a discovered Intel GPU card.
type CardInfo struct {
	// CardPath is the /sys/class/drm/card* directory.
	CardPath string
	// HwmonPath is the hwmon directory found by matching name=="xe" or "i915".
	HwmonPath string
	// DriverName is "xe" or "i915".
	DriverName string
}

// Enumerate discovers Intel GPU cards by scanning drm entries whose driver
// is xe or i915 and whose hwmon/*/name matches. Never hard-codes hwmonN.
func Enumerate(sysRoot string) ([]CardInfo, error) {
	drm := filepath.Join(sysRoot, "class", "drm")
	entries, err := os.ReadDir(drm)
	if err != nil {
		return nil, fmt.Errorf("xe: read drm dir: %w", err)
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

func probeCard(cardPath string) (*CardInfo, error) {
	driverLink := filepath.Join(cardPath, "device", "driver")
	target, err := os.Readlink(driverLink)
	if err != nil {
		return nil, nil //nolint:nilerr
	}
	driverName := filepath.Base(target)
	if driverName != "xe" && driverName != "i915" {
		return nil, nil
	}

	hwmonDir := filepath.Join(cardPath, "device", "hwmon")
	hwEntries, err := os.ReadDir(hwmonDir)
	if err != nil {
		return nil, nil //nolint:nilerr
	}

	for _, hw := range hwEntries {
		nameFile := filepath.Join(hwmonDir, hw.Name(), "name")
		raw, err := os.ReadFile(nameFile)
		if err != nil {
			continue
		}
		n := strings.TrimSpace(string(raw))
		if n == "xe" || n == "i915" {
			return &CardInfo{
				CardPath:   cardPath,
				HwmonPath:  filepath.Join(hwmonDir, hw.Name()),
				DriverName: driverName,
			}, nil
		}
	}
	return nil, nil
}

// ReadFanRPMs reads all fan*_input attributes and returns RPM values.
func (c *CardInfo) ReadFanRPMs() ([]int, error) {
	entries, err := os.ReadDir(c.HwmonPath)
	if err != nil {
		return nil, fmt.Errorf("xe: read hwmon dir: %w", err)
	}

	var rpms []int
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "fan") || !strings.HasSuffix(e.Name(), "_input") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(c.HwmonPath, e.Name()))
		if err != nil {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			continue
		}
		rpms = append(rpms, v)
	}
	return rpms, nil
}
