package sysclass

import (
	"log/slog"
	"strings"

	"github.com/ventd/ventd/internal/probe"
)

// AmbientSource describes how the ambient sensor was identified.
type AmbientSource int

const (
	AmbientLabeled      AmbientSource = iota // explicit label: ambient/intake/inlet/sio/systin
	AmbientLowestAtIdle                      // heuristic: lowest admissible temp sensor
	AmbientFallback25C                       // no usable sensors; assume 25 °C
)

// AmbientSensor captures the ambient temperature reference used for headroom checks.
type AmbientSensor struct {
	Source      AmbientSource
	SensorPath  string  // empty for AmbientFallback25C
	SensorLabel string  // empty for AmbientFallback25C
	Reading     float64 // °C at probe-start
}

// ambientLabelKeywords are substrings that, when found case-insensitively in a
// sensor label, identify it as an explicit ambient sensor.
var ambientLabelKeywords = []string{"ambient", "intake", "inlet", "sio", "systin"}

// ambientPlausibleMinC / ambientPlausibleMaxC bracket the plausible range
// for an ambient sensor reading at probe time. Sensors outside this
// window are filtered before the lowest-at-idle heuristic so an
// unconnected thermistor pad on NCT6687 (which reads single-digit
// degrees because the pin is floating or shorted to ground) cannot
// silently win the ambient race and trip envelope's later "ambient
// reading outside [10,50]°C, probe deferred" refusal (#1277).
//
// The lower bound is intentionally tight: 12 °C is colder than any
// indoor server room we expect ventd to ship into without an HVAC
// failure, but warm enough that a NCT6687 floating-pin pad at 8.5 °C
// (proxmox HIL 2026-05-20) is rejected. The upper bound is 40 °C: a
// PC chassis ambient above 40 °C is hot enough that the operator has
// bigger problems than fan control.
const (
	ambientPlausibleMinC = 12.0
	ambientPlausibleMaxC = 40.0
)

// admissibilityBlocklist are label substrings that disqualify a sensor from the
// lowest-at-idle heuristic (§3.3 admissibility filter).
//
// Issue #1162: Intel's Dynamic Platform & Thermal Framework (DPTF)
// surfaces as a /sys/class/thermal/thermal_zone* with a sensor that
// reports near-zero values when the policy engine isn't actively
// running — it's a power-management policy device, not a physical
// temperature sensor. Every Intel laptop ≥10th gen with intel_dptf
// loaded otherwise wins the lowest-at-idle race with a ~0 °C reading
// and subsequent envelope probes trip "ambient reading outside
// [10,50]°C, probe deferred". Block the known DPTF ACPI HIDs and the
// proc_thermal virtual zone explicitly; acpitz is intentionally NOT
// blocked because it's firmware-derived but reports real physical
// temperatures.
var admissibilityBlocklist = []string{
	"package", "junction", "vrm", "drivetemp",
	"cpu", "gpu", "core", "tdie", "tctl", "tccd",
	"coolant", "pump", "liquid",
	"nvme", "ssd", "hdd",
	// Intel DPTF policy-engine surfaces (issue #1162).
	"int3400",                          // DPTF manager (the one Hudson's box hit)
	"int3403",                          // DPTF satellite (TPCH/CPU package proxy)
	"intc1041", "intc10a0", "intc10b0", // newer DPTF generations
	"dptf", // catch-all for vendor-labeled DPTF zones
	"tcpu", // proc_thermal_pci virtual zone
}

// identifyAmbient resolves an ambient temperature sensor from the probe result.
// It follows the §3.3 three-step fallback chain.
func identifyAmbient(r *probe.ProbeResult, _ deps) AmbientSensor {
	// Step 1: label-matched sensor (plausibility-gated).
	for _, ts := range r.ThermalSources {
		for _, sc := range ts.Sensors {
			if !sc.ReadOK {
				continue
			}
			if !plausibleAmbientReading(sc.InitialRead) {
				continue
			}
			label := strings.ToLower(sc.Label)
			for _, kw := range ambientLabelKeywords {
				if strings.Contains(label, kw) {
					return AmbientSensor{
						Source:      AmbientLabeled,
						SensorPath:  sc.Path,
						SensorLabel: sc.Label,
						Reading:     sc.InitialRead,
					}
				}
			}
		}
	}

	// Step 2: lowest-at-idle heuristic after admissibility +
	// plausibility filters. The plausibility gate is the new addition:
	// an unconnected NCT thermistor pad reading ~8 °C will not win
	// the race even though its label is empty (so it passes the
	// admissibility filter).
	best := AmbientSensor{Reading: 1e9}
	found := false
	for _, ts := range r.ThermalSources {
		for _, sc := range ts.Sensors {
			if !sc.ReadOK {
				continue
			}
			if !isAdmissible(sc.Label) {
				continue
			}
			if !plausibleAmbientReading(sc.InitialRead) {
				continue
			}
			if sc.InitialRead < best.Reading {
				best = AmbientSensor{
					Source:      AmbientLowestAtIdle,
					SensorPath:  sc.Path,
					SensorLabel: sc.Label,
					Reading:     sc.InitialRead,
				}
				found = true
			}
		}
	}
	if found {
		return best
	}

	// Step 3: 25 °C fallback.
	slog.Warn("sysclass: no admissible ambient sensor found; assuming 25 °C",
		"diag", "AMBIENT-FALLBACK-25C-NO-SENSORS")
	return AmbientSensor{
		Source:  AmbientFallback25C,
		Reading: 25.0,
	}
}

// plausibleAmbientReading returns true when reading falls within the
// expected indoor-ambient range. Used as a second filter alongside the
// label-keyword admissibility check so an unconnected thermistor pad
// (NCT6687, NCT6797) can't win the lowest-at-idle race with a
// physically-impossible reading.
func plausibleAmbientReading(c float64) bool {
	return c >= ambientPlausibleMinC && c <= ambientPlausibleMaxC
}

// isAdmissible returns false when a sensor label contains any admissibility
// blocklist substring. Sensors without a label are admissible.
func isAdmissible(label string) bool {
	if label == "" {
		return true
	}
	lower := strings.ToLower(label)
	for _, bad := range admissibilityBlocklist {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	return true
}
