package hwmon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StableDevice returns the stable (boot-persistent) device directory for an
// hwmon path. The hwmonX number is volatile and changes between reboots; the
// parent device directory (e.g. /sys/devices/platform/nct6687.2608) is stable.
//
// Resolution strategy:
//
//  1. Prefer the hwmon dir's `device` symlink. This is what
//     `internal/config/resolve_hwmon.go::hwmonDevicePathOf` reads, so
//     producing the same path here keeps StableDevice round-trippable
//     through the resolver.
//
//  2. Fall back to "go up two levels from the resolved hwmon dir" for
//     hwmon entries that lack a `device` symlink (rare). Matches the
//     pre-v0.5.8.1 behaviour for chips like nct6687d / it87 / coretemp.
//
// Example (platform device):
//
//	/sys/class/hwmon/hwmon2/pwm1
//	  → /sys/class/hwmon/hwmon2/device → /sys/devices/platform/nct6687.2608
//
// Example (thermal-class virtual device):
//
//	/sys/class/hwmon/hwmon0/temp1_input  (acpitz)
//	  → /sys/class/hwmon/hwmon0/device → /sys/devices/virtual/thermal/thermal_zone0
//
// The previous "up two levels" formula returned /sys/devices/virtual/thermal
// for the acpitz case — a path the resolver doesn't recognise as the
// chip's stable device. Using the `device` symlink fixes the round-trip.
func StableDevice(hwmonPath string) string {
	// Work from the hwmon directory, not a file inside it.
	dir := hwmonPath
	if !isHwmonDir(dir) {
		dir = filepath.Dir(hwmonPath)
	}
	// Strategy 1: read the hwmon dir's `device` symlink directly.
	if dev, err := filepath.EvalSymlinks(filepath.Join(dir, "device")); err == nil {
		return dev
	}
	// Strategy 2: walk up two levels from the resolved hwmon dir.
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ""
	}
	return filepath.Dir(filepath.Dir(real))
}

// FindHwmonDir returns the current hwmonX directory under a stable device path.
// e.g. /sys/devices/platform/nct6687.2608 → /sys/devices/platform/nct6687.2608/hwmon/hwmon3
func FindHwmonDir(stableDevice string) (string, error) {
	hwmonParent := filepath.Join(stableDevice, "hwmon")
	entries, err := os.ReadDir(hwmonParent)
	if err != nil {
		return "", fmt.Errorf("hwmon: device %s has no hwmon dir: %w", stableDevice, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "hwmon") {
			return filepath.Join(hwmonParent, e.Name()), nil
		}
	}
	return "", fmt.Errorf("hwmon: no hwmonX entry under %s", stableDevice)
}

// ResolvePath resolves a stored sysfs path that may have moved due to hwmonX
// renumbering. If the path still exists it is returned unchanged (changed=false).
// If stableDevice is set and the hwmon dir has moved, the file name is rebased
// onto the new hwmon directory.
func ResolvePath(storedPath, stableDevice string) (resolved string, changed bool) {
	if _, err := os.Stat(storedPath); err == nil {
		return storedPath, false // still valid
	}
	if stableDevice == "" {
		return storedPath, false
	}
	hwmonDir, err := FindHwmonDir(stableDevice)
	if err != nil {
		return storedPath, false
	}
	candidate := filepath.Join(hwmonDir, filepath.Base(storedPath))
	if _, err := os.Stat(candidate); err == nil {
		return candidate, true
	}
	return storedPath, false
}

// isHwmonDir returns true if path looks like a /sys/class/hwmon/hwmonX directory
// rather than a file inside one.
func isHwmonDir(path string) bool {
	base := filepath.Base(path)
	return strings.HasPrefix(base, "hwmon")
}
