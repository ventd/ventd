package nvml

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// laptopChassisTypes is the set of DMI chassis_type integer values that
// indicate a portable/laptop form factor (RULE-GPU-PR2D-06).
var laptopChassisTypes = map[int]struct{}{
	8:  {}, // Portable
	9:  {}, // Laptop
	10: {}, // Notebook
	11: {}, // Hand Held
	14: {}, // Sub Notebook
	31: {}, // Convertible
}

// LaptopDGPU reports whether the system is a laptop chassis with a discrete
// NVIDIA GPU. dmiRoot is the path prefix for DMI sysfs files (normally
// "/sys" in production, a temp dir in tests).
//
// Returns (true, nil) when chassis_type is in the laptop set AND at least
// one NVML GPU is detected. Returns (false, nil) for desktop or when NVML
// is unavailable.
func LaptopDGPU(dmiRoot string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(dmiRoot, "class", "dmi", "id", "chassis_type"))
	if err != nil {
		return false, fmt.Errorf("nvml: read chassis_type: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return false, fmt.Errorf("nvml: parse chassis_type %q: %w", strings.TrimSpace(string(raw)), err)
	}
	if _, ok := laptopChassisTypes[n]; !ok {
		return false, nil
	}
	// Confirm at least one NVML GPU is visible.
	if !Available() {
		return false, nil
	}
	return true, nil
}
