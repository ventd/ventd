package orchestrator

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoveredSensor is one CPU/GPU temperature sensor the wizard found
// suitable for binding into a default curve. The Path is the absolute
// sysfs tempN_input file the daemon will poll. Label is the
// user-facing name; the orchestrator picks a stable one from the
// driver-supplied label or the chip+temp index.
type DiscoveredSensor struct {
	Label    string `json:"label"`
	Path     string `json:"path"`
	ChipName string `json:"chip_name"`
	Kind     string `json:"kind"` // "cpu" | "gpu" | "drive" | "ambient" | "other"
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
				}
			}
		}
		// No labelled package/die — take the first temp input from this CPU chip.
		if len(matches) > 0 {
			return DiscoveredSensor{
				Label:    titleCase(chip) + " temp",
				Path:     matches[0],
				ChipName: chip,
				Kind:     "cpu",
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
			return DiscoveredSensor{
				Label:    "ACPI thermal zone",
				Path:     matches[0],
				ChipName: chip,
				Kind:     "cpu",
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

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
