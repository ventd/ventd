package sysclass

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ventd/ventd/internal/probe"
)

// SystemClass identifies the seven hardware profiles defined in R4 §10.
type SystemClass int

const (
	ClassUnknown    SystemClass = iota
	ClassHEDTAir                // Class 1: HEDT air-cooled
	ClassHEDTAIO                // Class 2: HEDT with AIO liquid cooler
	ClassMidDesktop             // Class 3: mid-range desktop
	ClassServer                 // Class 4: server / workstation with BMC
	ClassLaptop                 // Class 5: laptop with embedded controller
	ClassMiniPC                 // Class 6: fanless / low-power mini-PC
	ClassNASHDD                 // Class 7: NAS with spinning hard drives
)

func (c SystemClass) String() string {
	switch c {
	case ClassHEDTAir:
		return "hedt_air"
	case ClassHEDTAIO:
		return "hedt_aio"
	case ClassMidDesktop:
		return "mid_desktop"
	case ClassServer:
		return "server"
	case ClassLaptop:
		return "laptop"
	case ClassMiniPC:
		return "mini_pc"
	case ClassNASHDD:
		return "nas_hdd"
	default:
		return "unknown"
	}
}

// Detection is the result of system-class detection.
type Detection struct {
	Class         SystemClass
	Evidence      []string      // ordered facts that drove classification
	Tjmax         float64       // °C; 0 when unknown
	AmbientSensor AmbientSensor // see ambient.go
	BMCPresent    bool
	ECHandshakeOK *bool // nil for non-laptop classes
}

// deps bundles injectable dependencies for testability.
type deps struct {
	sysRoot       string // root for /sys reads; "" = "/"
	procRoot      string // root for /proc reads; "" = "/"
	devRoot       string // root for /dev reads; "" = "/"
	execDmidecode func(args ...string) (string, error)
}

func defaultDeps() deps {
	return deps{
		execDmidecode: runDmidecode,
	}
}

func sysPath(d deps, rel string) string {
	if d.sysRoot == "" {
		return "/sys/" + rel
	}
	return filepath.Join(d.sysRoot, rel)
}

func procPath(d deps, rel string) string {
	if d.procRoot == "" {
		return "/proc/" + rel
	}
	return filepath.Join(d.procRoot, rel)
}

func devGlob(d deps, pattern string) ([]string, error) {
	root := d.devRoot
	if root == "" {
		root = "/"
	}
	return filepath.Glob(filepath.Join(root, pattern))
}

// Detect classifies the system using §3.2 precedence rules and returns a
// Detection. The probe.Result provides the enumerated controllable channels
// and thermal sources.
func Detect(ctx context.Context, r *probe.ProbeResult) (*Detection, error) {
	return detectWithDeps(ctx, r, defaultDeps())
}

func detectWithDeps(ctx context.Context, r *probe.ProbeResult, d deps) (*Detection, error) {
	det := &Detection{}

	// §3.2 Rule 1: NAS first
	if isNAS, ev := detectNAS(d); isNAS {
		det.Class = ClassNASHDD
		det.Evidence = ev
		fillAmbient(det, r, d)
		slog.InfoContext(ctx, "sysclass.detected", "class", det.Class)
		return det, nil
	}

	// §3.2 Rule 2: Mini-PC (no controllable PWM + N-series CPU)
	if len(r.ControllableChannels) == 0 {
		cls, tjmax, ev := classifyCPU(d)
		if cls == ClassMiniPC {
			det.Class = ClassMiniPC
			det.Tjmax = tjmax
			det.Evidence = ev
			fillAmbient(det, r, d)
			slog.InfoContext(ctx, "sysclass.detected", "class", det.Class)
			return det, nil
		}
	}

	// §3.2 Rule 3: Laptop
	if isLaptop, ev := detectLaptop(d); isLaptop {
		det.Class = ClassLaptop
		det.Evidence = ev
		cls, tjmax, cpuEv := classifyCPU(d)
		_ = cls
		det.Tjmax = tjmax
		det.Evidence = append(det.Evidence, cpuEv...)
		fillAmbient(det, r, d)
		slog.InfoContext(ctx, "sysclass.detected", "class", det.Class)
		return det, nil
	}

	// §3.2 Rule 4: Server (BMC or server CPU)
	bmc := detectBMC(d)
	cls, tjmax, cpuEv := classifyCPU(d)
	if bmc || cls == ClassServer {
		det.Class = ClassServer
		det.BMCPresent = bmc
		det.Tjmax = tjmax
		det.Evidence = cpuEv
		if bmc {
			det.Evidence = append([]string{"bmc_present"}, det.Evidence...)
		}
		fillAmbient(det, r, d)
		slog.InfoContext(ctx, "sysclass.detected", "class", det.Class)
		return det, nil
	}

	// §3.2 Rule 5: HEDT-AIO
	if cls == ClassHEDTAir || cls == ClassHEDTAIO {
		det.Tjmax = tjmax
		if hasAIOHint(r, d) {
			det.Class = ClassHEDTAIO
			det.Evidence = append(cpuEv, "aio_hint")
		} else {
			// §3.2 Rule 6: HEDT-Air
			det.Class = ClassHEDTAir
			det.Evidence = cpuEv
		}
		fillAmbient(det, r, d)
		slog.InfoContext(ctx, "sysclass.detected", "class", det.Class)
		return det, nil
	}

	// §3.2 Rule 7: Mid-desktop fallback
	if len(r.ControllableChannels) > 0 {
		det.Class = ClassMidDesktop
		det.Tjmax = tjmax
		det.Evidence = append([]string{"pwm_channels_present"}, cpuEv...)
		fillAmbient(det, r, d)
		slog.InfoContext(ctx, "sysclass.detected", "class", det.Class)
		return det, nil
	}

	// §3.2 Rule 8: Unknown — use most-conservative thresholds (mini-PC)
	det.Class = ClassUnknown
	det.Evidence = []string{"no_class_match"}
	fillAmbient(det, r, d)
	slog.WarnContext(ctx, "sysclass.unknown", "evidence", det.Evidence)
	return det, nil
}

// hasAIOHint checks for AIO cooler signals: sensor labels or USB-HID devices.
func hasAIOHint(r *probe.ProbeResult, d deps) bool {
	for _, ts := range r.ThermalSources {
		for _, sc := range ts.Sensors {
			label := strings.ToLower(sc.Label)
			if strings.Contains(label, "coolant") ||
				strings.Contains(label, "pump") ||
				strings.Contains(label, "liquid") {
				return true
			}
		}
	}
	// Check for Corsair/NZXT/EK HID devices
	patterns := []string{"dev/hidraw*"}
	for _, pat := range patterns {
		matches, _ := devGlob(d, pat)
		_ = matches
		// Presence of hidraw devices alone is insufficient; leave detailed
		// USB-HID vendor check to PR-B Corsair/NZXT integration.
	}
	return false
}

// detectLaptop checks DMI chassis type and battery presence.
func detectLaptop(d deps) (bool, []string) {
	// Battery presence
	batGlob := sysPath(d, "class/power_supply/BAT*")
	if matches, err := filepath.Glob(batGlob); err == nil && len(matches) > 0 {
		return true, []string{"battery_present"}
	}

	// DMI chassis type
	chassisPath := sysPath(d, "class/dmi/id/chassis_type")
	data, err := os.ReadFile(chassisPath)
	if err != nil {
		return false, nil
	}
	chassis := strings.TrimSpace(string(data))
	// Laptop chassis types: Portable=8, Laptop=9, Notebook=10, Hand Held=11, Sub Notebook=14, Convertible=31
	switch chassis {
	case "8", "9", "10", "11", "14", "31":
		return true, []string{fmt.Sprintf("dmi_chassis_type:%s", chassis)}
	}
	return false, nil
}

func fillAmbient(det *Detection, r *probe.ProbeResult, d deps) {
	det.AmbientSensor = identifyAmbient(r, d)
}
