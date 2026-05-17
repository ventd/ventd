// SPDX-License-Identifier: GPL-3.0-or-later

package msiec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// AllowedSensorPaths is the closed set of relative paths under
// DefaultSysfsRoot that the `msiec` sensor type accepts. Restricting
// the surface here keeps operator-authored configs from escaping into
// arbitrary sysfs reads (e.g. /sys/devices/platform/msi-ec/leds/.../uevent)
// and gives the config validator a single point of truth.
//
// Discovered on Hudson's MS-16R8 (issue #1154 follow-up via #1167) by
// stringing MControlCenter's privileged helper. Units:
//
//   - *_temperature paths return integer °C (the msi-ec driver reads
//     the EC register, which already exposes degrees rather than the
//     hwmon millidegree convention).
//   - *_fan_speed paths return a 0..100 percent value for cpu and a
//     0..150 percent value for gpu (the gpu fan can over-spin past
//     nominal under cooler-boost). They are *not* tach RPM; the
//     canonical RPM source is the in-tree msi_wmi_platform hwmon
//     device.
//
// The five paths below are the entire stable surface MControlCenter
// reads. Adding a path here is a deliberate config-surface widening,
// not a casual change.
var AllowedSensorPaths = map[string]struct{}{
	"cpu/realtime_temperature": {},
	"gpu/realtime_temperature": {},
	"cpu/realtime_fan_speed":   {},
	"gpu/realtime_fan_speed":   {},
	"cpu/basic_fan_speed":      {},
}

// ErrSensorPathNotAllowed is returned when a relative path is not in
// AllowedSensorPaths. Surfacing this from the config validator gives
// operators a clear "this path is not on the allowlist" error rather
// than the EACCES / ENOENT they would get from blindly reading.
var ErrSensorPathNotAllowed = errors.New("msiec: sensor path not in allowlist")

// ValidateSensorPath checks rel against AllowedSensorPaths and returns
// nil iff it is a member. Used by both the config validator (at load
// time, before any read fires) and ReadSensor (defence-in-depth at
// read time).
func ValidateSensorPath(rel string) error {
	if _, ok := AllowedSensorPaths[rel]; !ok {
		return fmt.Errorf("%w: %q (allowed: %s)", ErrSensorPathNotAllowed, rel, allowedSensorPathsList())
	}
	return nil
}

// ReadSensor reads a single value from one of the msi-ec sensor paths
// listed in AllowedSensorPaths and returns it as a float64. The
// returned value is in the path's native unit (°C for *_temperature,
// percent for *_fan_speed) — same convention as readNvidiaMetric, and
// distinct from hwmon.ReadValue's millidegree-aware scaling.
//
// sysfsRoot defaults to DefaultSysfsRoot in production; tests inject a
// t.TempDir() to drive the parser hermetically. relPath is the
// relative path under sysfsRoot (e.g. "cpu/realtime_temperature").
// The path is validated against AllowedSensorPaths before any I/O
// fires, so a malformed config can never make ReadSensor escape the
// msi-ec directory.
func ReadSensor(sysfsRoot, relPath string) (float64, error) {
	if err := ValidateSensorPath(relPath); err != nil {
		return 0, err
	}
	full := filepath.Join(sysfsRoot, relPath)
	data, err := os.ReadFile(full)
	if err != nil {
		return 0, fmt.Errorf("msiec: read %s: %w", full, err)
	}
	raw, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, fmt.Errorf("msiec: parse %s: %w", full, err)
	}
	return raw, nil
}

// allowedSensorPathsList renders AllowedSensorPaths as a sorted,
// comma-joined list for inclusion in error messages.
func allowedSensorPathsList() string {
	keys := make([]string, 0, len(AllowedSensorPaths))
	for k := range AllowedSensorPaths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
