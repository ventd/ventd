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

	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
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
	SensorType string  `json:"sensor_type"`      // "hwmon" or "nvidia"
	SensorPath string  `json:"sensor_path"`      // full sysfs path or GPU index
	Metric     string  `json:"metric,omitempty"` // nvidia metric name (empty = default "temp")
}

// scanRoot is the hwmon class directory scanHwmon reads from. Overridable
// from tests via a fake sysfs tree in t.TempDir(); the production default
// is the real kernel sysfs mount.
var scanRoot = "/sys/class/hwmon"

func Scan() []Device {
	var devices []Device
	devices = append(devices, scanHwmon()...)
	devices = append(devices, scanNVML()...)
	return devices
}

func scanHwmon() []Device {
	entries, err := os.ReadDir(scanRoot)
	if err != nil {
		return nil
	}
	var devices []Device
	for _, e := range entries {
		dir := filepath.Join(scanRoot, e.Name())
		chip := readStr(filepath.Join(dir, "name"))
		if chip == "" {
			chip = e.Name()
		}
		dev := Device{Name: friendlyDeviceName(chip), Path: e.Name()}
		dev.Readings = append(dev.Readings, scanInputs(dir, "temp", "°C", 1000)...)
		// Fan readings get an extra dedup pass on chips known to mirror
		// the same physical tach across multiple `fan*_input` zones —
		// embedded EC firmwares (thinkpad_acpi, dell-smm-hwmon,
		// asus-ec-sensors, hp-wmi-sensors) expose CPU / system /
		// chassis virtual zones that all read the same value because
		// there's only one physical tach behind them. Desktop super-I/O
		// chips (nct6687, it8688, etc.) expose N distinct PWM channels
		// with N distinct tachs — applying dedup there is the bug from
		// #40 where Phoenix's MSI Z690-A only showed 1 fan_input
		// despite the board having 7 PWM headers. Trust the chip's
		// declared layout rather than coincidental idle-RPM matches.
		fanReadings := scanInputs(dir, "fan", "RPM", 1)
		if chipMirrorsTachs(chip) {
			fanReadings = dedupMirrorFans(fanReadings)
		}
		dev.Readings = append(dev.Readings, fanReadings...)
		dev.Readings = append(dev.Readings, scanInputs(dir, "in", "V", 1000)...)
		dev.Readings = append(dev.Readings, scanInputs(dir, "power", "W", 1000000)...)
		if len(dev.Readings) > 0 {
			devices = append(devices, dev)
		}
	}
	return devices
}

// mirrorRPMTolerance is the RPM-equality threshold for grouping
// fan*_input readings that look like mirrors of the same physical fan.
// At idle, real fans drift ±20-50 RPM; ±10 is conservative enough that
// two genuinely separate fans aren't accidentally merged. At load
// (>1000 RPM) the absolute tolerance is dwarfed by per-fan variance,
// so distinct fans don't merge.
const mirrorRPMTolerance = 10

// dedupMirrorFans collapses fan readings whose RPM is within
// `mirrorRPMTolerance` of another reading already in the result. The
// surviving entry's label is preferred when it's CPU/chassis/system
// (more informative than "fan2"). A single-pass O(n²) compare is fine —
// most hwmon devices report ≤ 8 fan tach inputs.
func dedupMirrorFans(in []Reading) []Reading {
	out := make([]Reading, 0, len(in))
nextFan:
	for _, r := range in {
		rpm := int(r.Value)
		for i, kept := range out {
			diff := int(kept.Value) - rpm
			if diff < 0 {
				diff = -diff
			}
			if diff <= mirrorRPMTolerance {
				// Prefer a more-informative label when colliding
				// (kept "fan2" being replaced by incoming "CPU Fan").
				if labelMoreInformative(r.Label, kept.Label) {
					out[i].Label = r.Label
				}
				continue nextFan
			}
		}
		out = append(out, r)
	}
	return out
}

// chipMirrorsTachs reports whether a hwmon chip name is in the known
// set of EC drivers that expose the same physical fan's RPM across
// multiple fan*_input zones. Desktop super-I/O chips (nct6687,
// it8688, etc.) report N distinct PWMs + N distinct tachs and MUST
// NOT be dedup'd — that's #40, where the dedup collapsed real
// distinct fans on Phoenix's MSI Z690-A.
//
// Add new entries here only after confirming via vendor docs OR
// hwmon source that the chip mirrors a single physical tach across
// multiple sysfs zones.
var ecMirrorChips = map[string]bool{
	"thinkpad_acpi":    true,
	"dell-smm-hwmon":   true,
	"asus-ec-sensors":  true,
	"asus-wmi-sensors": true,
	"hp-wmi-sensors":   true,
	"surface_fan":      true,
	"applesmc":         true,
	"macsmc-hwmon":     true,
}

func chipMirrorsTachs(chip string) bool {
	return ecMirrorChips[strings.ToLower(chip)]
}

// labelMoreInformative returns true when a is a better label than b.
// Heuristic: any label containing "fan" + a digit is generic
// ("fan1", "fan2"); labels naming a function ("CPU Fan", "Chassis Fan",
// "System Fan", "Pump") are informative.
func labelMoreInformative(a, b string) bool {
	aLow := strings.ToLower(a)
	bLow := strings.ToLower(b)
	aGeneric := strings.HasPrefix(aLow, "fan") && len(aLow) <= 5
	bGeneric := strings.HasPrefix(bLow, "fan") && len(bLow) <= 5
	if !aGeneric && bGeneric {
		return true
	}
	return false
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
		// Reject sentinel / implausible values before they appear in the API.
		// nct6687 and similar super-I/O chips return 0xFFFF from registers that
		// are in mid-latch; after scaling these map to 255.5°C (temp), 65535 RPM
		// (fan), or 65.535 V (in). A UI that renders these without filtering
		// would show 255.5°C for a sensor that reads 8.5°C a second later.
		if isSentinelMonitorVal(prefix, val) {
			slog.Default().Warn("monitor: sentinel or implausible value suppressed",
				"path", path, "prefix", prefix, "value", val)
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

// isSentinelMonitorVal reports whether a scaled sensor value is a known driver
// sentinel or exceeds the plausibility cap for the given hwmon file prefix.
// Mirrors the thresholds in internal/hal/hwmon/sentinel.go so that the monitor
// scan path and the status/SSE path enforce the same rejection criteria.
func isSentinelMonitorVal(prefix string, val float64) bool {
	switch prefix {
	case "temp":
		return val >= halhwmon.PlausibleTempMaxCelsius
	case "fan":
		return val >= halhwmon.SentinelRPMRaw || val > halhwmon.PlausibleRPMMax
	case "in":
		return val > halhwmon.PlausibleVoltageMaxVolts
	}
	return false
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
