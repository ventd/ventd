package setup

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// HwmonChip describes a single hwmon chip and its temperature sensors.
type HwmonChip struct {
	Name    string
	Sensors []HwmonSensor
}

// HwmonSensor describes one temperature sensor on an hwmon chip.
type HwmonSensor struct {
	Label           string
	Path            string
	CurrentMillideg int64
}

// heuristicSensorBinding returns the best available temperature sensor for a
// fan that could not be auto-correlated via RPM detection. Priority order:
//  1. coretemp "Package id 0" (Intel package temp — most representative)
//  2. k10temp "Tctl" or "Tdie" (AMD)
//  3. Any sensor label containing "package" or "cpu temp" (case-insensitive)
//  4. First sensor reading in the plausible range [20°C, 100°C]
//
// Returns nil if no plausible sensor is found (e.g. all readings are 0°C or
// sentinel values above 100°C).
func heuristicSensorBinding(chips []HwmonChip) *HwmonSensor {
	// Priority 1: coretemp Package id 0.
	for i := range chips {
		if chips[i].Name == "coretemp" {
			for j := range chips[i].Sensors {
				if strings.Contains(chips[i].Sensors[j].Label, "Package id 0") {
					return &chips[i].Sensors[j]
				}
			}
		}
	}
	// Priority 2: k10temp Tctl / Tdie.
	for i := range chips {
		if chips[i].Name == "k10temp" {
			for j := range chips[i].Sensors {
				l := chips[i].Sensors[j].Label
				if l == "Tctl" || l == "Tdie" {
					return &chips[i].Sensors[j]
				}
			}
		}
	}
	// Priority 3: label heuristic across all chips.
	for i := range chips {
		for j := range chips[i].Sensors {
			l := strings.ToLower(chips[i].Sensors[j].Label)
			if strings.Contains(l, "package") || strings.Contains(l, "cpu temp") {
				return &chips[i].Sensors[j]
			}
		}
	}
	// Priority 4: any sensor in plausible range [20°C, 100°C] = [20000, 100000] millideg.
	for i := range chips {
		for j := range chips[i].Sensors {
			m := chips[i].Sensors[j].CurrentMillideg
			if m >= 20_000 && m <= 100_000 {
				return &chips[i].Sensors[j]
			}
		}
	}
	return nil
}

// loadHwmonChips enumerates hwmon devices under hwmonRoot and collects their
// temperature sensors. Used as input to heuristicSensorBinding.
func loadHwmonChips(hwmonRoot string) []HwmonChip {
	entries, _ := os.ReadDir(hwmonRoot)
	var chips []HwmonChip
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(hwmonRoot, e.Name())
		name := readTrimmed(filepath.Join(dir, "name"))
		if name == "" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		sort.Strings(matches)
		var sensors []HwmonSensor
		for _, p := range matches {
			base := strings.TrimSuffix(filepath.Base(p), "_input")
			label := readTrimmed(filepath.Join(dir, base+"_label"))
			var millideg int64
			if raw := readTrimmed(p); raw != "" {
				millideg, _ = strconv.ParseInt(raw, 10, 64)
			}
			sensors = append(sensors, HwmonSensor{
				Label:           label,
				Path:            p,
				CurrentMillideg: millideg,
			})
		}
		chips = append(chips, HwmonChip{
			Name:    name,
			Sensors: sensors,
		})
	}
	return chips
}
