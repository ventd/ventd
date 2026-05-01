package opportunistic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ventd/ventd/internal/probe"
)

// SysfsWriteFn returns a WriteFn that writes the supplied byte to the
// channel's PWM sysfs file as a decimal string. Production wiring;
// tests should use a captured-write function.
func SysfsWriteFn(ch *probe.ControllableChannel) WriteFn {
	return func(v uint8) error {
		return os.WriteFile(ch.PWMPath, []byte(strconv.Itoa(int(v))), 0o644)
	}
}

// SysfsRPMFn returns an RPMFn that reads the channel's tach sysfs file.
// Returns -1 (the schema's "tach-less" marker) if TachPath is empty or
// the file is unreadable. Tach reads are O(µs) and safe in the probe
// hot loop.
func SysfsRPMFn(ch *probe.ControllableChannel) RPMFn {
	if ch.TachPath == "" {
		return func(ctx context.Context) (int32, error) { return -1, nil }
	}
	return func(ctx context.Context) (int32, error) {
		data, err := os.ReadFile(ch.TachPath)
		if err != nil {
			return -1, nil
		}
		s := strings.TrimSpace(string(data))
		v, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return -1, nil
		}
		return int32(v), nil
	}
}

// SysfsSensorFn returns a SensorFn that walks /sys/class/hwmon and
// returns every readable temp*_input as a sensor_id keyed map. The
// thermal-abort guard treats any sensor crossing the threshold as a
// trip, so over-reading is conservative; missing sensors are ignored
// silently. The returned function is safe to call repeatedly.
func SysfsSensorFn() SensorFn {
	return func(ctx context.Context) (map[string]float64, error) {
		out := make(map[string]float64)
		entries, err := os.ReadDir("/sys/class/hwmon")
		if err != nil {
			return out, nil
		}
		for _, hwmon := range entries {
			hwmonDir := filepath.Join("/sys/class/hwmon", hwmon.Name())
			tempEntries, err := os.ReadDir(hwmonDir)
			if err != nil {
				continue
			}
			for _, te := range tempEntries {
				name := te.Name()
				if !strings.HasPrefix(name, "temp") || !strings.HasSuffix(name, "_input") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(hwmonDir, name))
				if err != nil {
					continue
				}
				milliC, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
				if err != nil {
					continue
				}
				key := fmt.Sprintf("%s/%s", hwmon.Name(), name)
				out[key] = float64(milliC) / 1000.0
			}
		}
		return out, nil
	}
}
