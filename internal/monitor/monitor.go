// Package monitor provides live hardware discovery by scanning /sys/class/hwmon
// and NVML for all available sensor readings — not just configured ones.
package monitor

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Device struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Readings []Reading `json:"readings"`
}

type Reading struct {
	Label      string  `json:"label"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	SensorType string  `json:"sensor_type"`           // "hwmon" or "nvidia"
	SensorPath string  `json:"sensor_path"`           // full sysfs path or GPU index
	Metric     string  `json:"metric,omitempty"`      // nvidia metric name (empty = default "temp")
}

func Scan() []Device {
	var devices []Device
	devices = append(devices, scanHwmon()...)
	devices = append(devices, scanNVML()...)
	return devices
}

func scanHwmon() []Device {
	entries, err := os.ReadDir("/sys/class/hwmon")
	if err != nil {
		return nil
	}
	var devices []Device
	for _, e := range entries {
		dir := filepath.Join("/sys/class/hwmon", e.Name())
		chip := readStr(filepath.Join(dir, "name"))
		if chip == "" {
			chip = e.Name()
		}
		dev := Device{Name: friendlyDeviceName(chip), Path: e.Name()}
		dev.Readings = append(dev.Readings, scanInputs(dir, "temp", "°C", 1000)...)
		dev.Readings = append(dev.Readings, scanInputs(dir, "fan", "RPM", 1)...)
		dev.Readings = append(dev.Readings, scanInputs(dir, "in", "V", 1000)...)
		dev.Readings = append(dev.Readings, scanInputs(dir, "power", "W", 1000000)...)
		if len(dev.Readings) > 0 {
			devices = append(devices, dev)
		}
	}
	return devices
}

func scanInputs(dir, prefix, unit string, divisor float64) []Reading {
	pattern := filepath.Join(dir, prefix+"*_input")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		// filepath.Glob only fails on a malformed pattern, which would indicate
		// a bug here (the pattern is constructed from a fixed prefix list).
		// Log and continue with whatever partial result we have.
		slog.Default().Warn("monitor: glob failed", "pattern", pattern, "err", err)
	}
	naturalSortPaths(matches)
	var readings []Reading
	for _, path := range matches {
		base := strings.TrimSuffix(filepath.Base(path), "_input")
		label := readStr(filepath.Join(dir, base+"_label"))
		if label == "" {
			label = base
		}
		raw, err := readInt(path)
		if err != nil {
			continue
		}
		val := float64(raw) / divisor
		if prefix == "fan" && val == 0 {
			continue
		}
		readings = append(readings, Reading{
			Label:      label,
			Value:      val,
			Unit:       unit,
			SensorType: "hwmon",
			SensorPath: path,
		})
	}
	return readings
}

// friendlyDeviceName maps hwmon driver chip names to human-readable display names.
func friendlyDeviceName(chip string) string {
	lower := strings.ToLower(chip)
	switch lower {
	case "coretemp":
		return "Intel CPU"
	case "k10temp":
		return "AMD CPU"
	case "zenpower":
		return "AMD CPU (ZenPower)"
	case "amdgpu":
		return "AMD GPU"
	case "nouveau":
		return "NVIDIA GPU (nouveau)"
	case "nvme":
		return "NVMe Drive"
	case "drivetemp":
		return "Drive Temp"
	case "acpitz":
		return "ACPI Thermal"
	case "cpu_thermal", "cpu-thermal":
		return "CPU Thermal"
	case "iwlwifi_1":
		return "Wi-Fi"
	}
	// Nuvoton Super I/O chips: nct6687, nct6683, nct6779, nct6798, etc.
	if strings.HasPrefix(lower, "nct") {
		return "Nuvoton " + strings.ToUpper(chip)
	}
	// ITE Super I/O chips: it8688, it8792, etc.
	if strings.HasPrefix(lower, "it") && len(chip) > 2 && chip[2] >= '0' && chip[2] <= '9' {
		return "ITE " + strings.ToUpper(chip)
	}
	// Winbond / Fintek Super I/O chips
	if strings.HasPrefix(lower, "w83") {
		return "Winbond " + strings.ToUpper(chip)
	}
	if strings.HasPrefix(lower, "f71") || strings.HasPrefix(lower, "f81") {
		return "Fintek " + strings.ToUpper(chip)
	}
	return chip
}

// naturalSortPaths sorts sysfs file paths numerically by the index embedded in
// the basename (e.g. temp2_input < temp10_input). This prevents lexicographic
// ordering from placing temp10 before temp2.
func naturalSortPaths(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		return extractBaseNum(paths[i]) < extractBaseNum(paths[j])
	})
}

// extractBaseNum returns the first run of digits found in the basename of path.
func extractBaseNum(path string) int {
	base := filepath.Base(path)
	for i, ch := range base {
		if ch >= '0' && ch <= '9' {
			end := i
			for end < len(base) && base[end] >= '0' && base[end] <= '9' {
				end++
			}
			n, _ := strconv.Atoi(base[i:end])
			return n
		}
	}
	return 0
}

func readStr(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readInt(path string) (int64, error) {
	s := readStr(path)
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	return strconv.ParseInt(s, 10, 64)
}
