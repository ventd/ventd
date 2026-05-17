package orchestrator

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DiscoveredSensor is one CPU/GPU temperature sensor the wizard found
// suitable for binding into a default curve. The Path is the absolute
// sysfs tempN_input file the daemon will poll. Label is the
// user-facing name; the orchestrator picks a stable one from the
// driver-supplied label or the chip+temp index.
//
// CritC and MaxC are the chip-reported thermal limits in °C (read
// from tempN_crit and tempN_max — coretemp / k10temp / k8temp
// populate these per-chip). CritC is TjMax (shutdown threshold);
// MaxC is the spec-rated warn level. ApplyPhase derives the default
// fan curve's MaxTemp from CritC - safety margin so the fans hit
// 100% well before thermal throttle. Zero when the chip doesn't
// expose the file (rare for CPU chips, common for ACPI thermal
// zones).
type DiscoveredSensor struct {
	Label    string  `json:"label"`
	Path     string  `json:"path"`
	ChipName string  `json:"chip_name"`
	Kind     string  `json:"kind"` // "cpu" | "gpu" | "drive" | "ambient" | "other"
	CritC    float64 `json:"crit_c,omitempty"`
	MaxC     float64 `json:"max_c,omitempty"`
}

// DiscoverCPUSensor returns the best-effort CPU temperature sensor
// under hwmonRoot. Three passes, in order:
//
//  1. Well-known CPU chip names (coretemp, k10temp, zenpower, …) with
//     a labelled package/die/tctl temp input — most accurate.
//  2. Any non-GPU hwmon device whose temp label mentions
//     CPU/package/die.
//  3. Last-resort: ACPI thermal zone (acpitz) which is present on
//     virtually all x86 systems but less accurate.
//
// Returns the zero-value DiscoveredSensor (empty Path) when nothing
// matches. The caller's policy decides whether that's a wizard
// failure or a "fall back to monitor-only" trigger.
//
// Ported from internal/setup.discoverCPUTempSensor so the orchestrator
// path uses the same proven multi-pass heuristic as the legacy wizard.
func DiscoverCPUSensor(hwmonRoot string) DiscoveredSensor {
	entries, _ := os.ReadDir(hwmonRoot)

	cpuChips := map[string]bool{
		"coretemp":    true,
		"k10temp":     true,
		"k8temp":      true,
		"zenpower":    true,
		"zenpower2":   true,
		"cpu_thermal": true,
		"aml_thermal": true,
		"mtk_thermal": true,
	}

	// Pass 1: known CPU chip names, prefer labelled package/die/tctl.
	for _, e := range entries {
		dir := filepath.Join(hwmonRoot, e.Name())
		chip := readTrimmedFile(filepath.Join(dir, "name"))
		if !cpuChips[chip] {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		sort.Strings(matches)
		for _, p := range matches {
			base := strings.TrimSuffix(filepath.Base(p), "_input")
			label := strings.ToLower(readTrimmedFile(filepath.Join(dir, base+"_label")))
			if strings.Contains(label, "package") ||
				strings.Contains(label, "tdie") ||
				strings.Contains(label, "tctl") ||
				strings.Contains(label, "die") {
				return DiscoveredSensor{
					Label:    titleCase(label) + " (" + chip + ")",
					Path:     p,
					ChipName: chip,
					Kind:     "cpu",
					CritC:    readMillidegC(filepath.Join(dir, base+"_crit")),
					MaxC:     readMillidegC(filepath.Join(dir, base+"_max")),
				}
			}
		}
		// No labelled package/die — take the first temp input from this CPU chip.
		if len(matches) > 0 {
			base := strings.TrimSuffix(filepath.Base(matches[0]), "_input")
			return DiscoveredSensor{
				Label:    titleCase(chip) + " temp",
				Path:     matches[0],
				ChipName: chip,
				Kind:     "cpu",
				CritC:    readMillidegC(filepath.Join(dir, base+"_crit")),
				MaxC:     readMillidegC(filepath.Join(dir, base+"_max")),
			}
		}
	}

	// Pass 2: any non-GPU hwmon device with a CPU/package label.
	gpuChips := map[string]bool{
		"amdgpu":   true,
		"nouveau":  true,
		"i915":     true,
		"radeon":   true,
		"asus_wmi": true,
	}
	for _, e := range entries {
		dir := filepath.Join(hwmonRoot, e.Name())
		chip := readTrimmedFile(filepath.Join(dir, "name"))
		if gpuChips[chip] {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		sort.Strings(matches)
		for _, p := range matches {
			base := strings.TrimSuffix(filepath.Base(p), "_input")
			label := strings.ToLower(readTrimmedFile(filepath.Join(dir, base+"_label")))
			if strings.Contains(label, "cpu") ||
				strings.Contains(label, "package") ||
				strings.Contains(label, "die") {
				return DiscoveredSensor{
					Label:    titleCase(label) + " (" + chip + ")",
					Path:     p,
					ChipName: chip,
					Kind:     "cpu",
					CritC:    readMillidegC(filepath.Join(dir, base+"_crit")),
					MaxC:     readMillidegC(filepath.Join(dir, base+"_max")),
				}
			}
		}
	}

	// Pass 3: acpitz fallback.
	for _, e := range entries {
		dir := filepath.Join(hwmonRoot, e.Name())
		chip := readTrimmedFile(filepath.Join(dir, "name"))
		if chip != "acpitz" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		sort.Strings(matches)
		if len(matches) > 0 {
			base := strings.TrimSuffix(filepath.Base(matches[0]), "_input")
			return DiscoveredSensor{
				Label:    "ACPI thermal zone",
				Path:     matches[0],
				ChipName: chip,
				Kind:     "cpu",
				CritC:    readMillidegC(filepath.Join(dir, base+"_crit")),
				MaxC:     readMillidegC(filepath.Join(dir, base+"_max")),
			}
		}
	}

	return DiscoveredSensor{}
}

func readTrimmedFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readMillidegC reads a hwmon-style millidegree-C file (e.g.
// temp1_crit = "100000" → 100.0 °C). Returns 0 when the file is
// absent or malformed — the caller treats 0 as "value not reported"
// and falls back to a safe default.
func readMillidegC(path string) float64 {
	s := readTrimmedFile(path)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return float64(n) / 1000.0
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
