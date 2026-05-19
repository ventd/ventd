package platformprofile

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// DetectHardware does a best-effort one-shot survey of CPU + thermal +
// cooling capabilities. Missing values are zero-default; the selector
// tolerates partial input.
func DetectHardware(logger *slog.Logger) HardwareSummary {
	hw := HardwareSummary{}

	hw.CPUModel = readCPUModel()
	hw.TJmaxC = readTJmaxC()
	hw.TDPWatts = readTDPWatts()
	hw.FanMaxRPM = readFanMaxRPM()
	hw.FanCount = readFanCount()
	hw.ChassisClass = readChassisClass()

	if logger != nil {
		logger.Info("platform_profile hardware survey",
			"cpu_model", hw.CPUModel,
			"tjmax_c", hw.TJmaxC,
			"tdp_w", hw.TDPWatts,
			"fan_max_rpm", hw.FanMaxRPM,
			"fan_count", hw.FanCount,
			"chassis_class", hw.ChassisClass)
	}
	return hw
}

func readCPUModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

// readTJmaxC scans hwmon for a coretemp/k10temp chip and reads temp1_crit
// (millideg C) → returns whole C. Returns 0 if no candidate is readable.
func readTJmaxC() int {
	root := "/sys/class/hwmon"
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		nameBytes, err := os.ReadFile(filepath.Join(root, e.Name(), "name"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(nameBytes))
		if name != "coretemp" && name != "k10temp" {
			continue
		}
		for _, candidate := range []string{"temp1_crit", "temp1_max"} {
			path := filepath.Join(root, e.Name(), candidate)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			milliC, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				continue
			}
			return milliC / 1000
		}
	}
	return 0
}

// readTDPWatts reads the RAPL package power-limit (PL1) in whole watts.
func readTDPWatts() int {
	data, err := os.ReadFile("/sys/class/powercap/intel-rapl/intel-rapl:0/constraint_0_power_limit_uw")
	if err != nil {
		return 0
	}
	uw, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return int(uw / 1_000_000)
}

// readFanMaxRPM returns the largest fan1_max value across all hwmon chips
// that expose a fan1_max attribute. Best-effort; 0 if none found.
func readFanMaxRPM() int {
	matches, err := filepath.Glob("/sys/class/hwmon/hwmon*/fan*_max")
	if err != nil {
		return 0
	}
	best := 0
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}
		if v > best {
			best = v
		}
	}
	return best
}

// readFanCount returns the number of fanN_input attributes found.
func readFanCount() int {
	matches, err := filepath.Glob("/sys/class/hwmon/hwmon*/fan*_input")
	if err != nil {
		return 0
	}
	return len(matches)
}

// readChassisClass classifies the host as "laptop", "desktop", "server", or
// "unknown" based on DMI chassis_type. See
// https://www.dmtf.org/sites/default/files/standards/documents/DSP0134_3.6.0.pdf
// §7.4.1 for the enumeration.
func readChassisClass() string {
	data, err := os.ReadFile("/sys/class/dmi/id/chassis_type")
	if err != nil {
		return "unknown"
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return "unknown"
	}
	switch n {
	case 8, 9, 10, 14, 30, 31, 32: // portable / laptop / notebook / sub-notebook / tablet / convertible / detachable
		return "laptop"
	case 17, 23, 28: // server-class chassis types
		return "server"
	case 3, 4, 5, 6, 7, 15, 16: // desktop / lo-pro / pizza-box / mini-tower / tower / space-saving / lunch-box
		return "desktop"
	}
	return "unknown"
}

// osNumCPU is a tiny indirection so tests can inject a fake.
var osNumCPU = func() int { return runtime.NumCPU() }

// FanMaxRPMReader returns a closure that re-reads fan1_max each call. Used
// by tests that want to drive the controller without touching globals.
func FanMaxRPMReader() func() (int, error) {
	return func() (int, error) {
		v := readFanMaxRPM()
		if v == 0 {
			return 0, errors.New("no fan_max found")
		}
		return v, nil
	}
}
