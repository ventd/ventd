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
// Example:
//
//	/sys/class/hwmon/hwmon2/pwm1
//	  → resolves symlink → /sys/devices/platform/nct6687.2608/hwmon/hwmon2
//	  → go up two levels → /sys/devices/platform/nct6687.2608
func StableDevice(hwmonPath string) string {
	// Work from the hwmon directory, not a file inside it.
	dir := hwmonPath
	if !isHwmonDir(dir) {
		dir = filepath.Dir(hwmonPath)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ""
	}
	// real = /sys/devices/.../hwmon/hwmonX — device is two levels up.
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
