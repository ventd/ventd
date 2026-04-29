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

// admissibilityBlocklist are label substrings that disqualify a sensor from the
// lowest-at-idle heuristic (§3.3 admissibility filter).
var admissibilityBlocklist = []string{
	"package", "junction", "vrm", "drivetemp",
	"cpu", "gpu", "core", "tdie", "tctl", "tccd",
	"coolant", "pump", "liquid",
	"nvme", "ssd", "hdd",
}

// identifyAmbient resolves an ambient temperature sensor from the probe result.
// It follows the §3.3 three-step fallback chain.
func identifyAmbient(r *probe.ProbeResult, _ deps) AmbientSensor {
	// Step 1: label-matched sensor.
	for _, ts := range r.ThermalSources {
		for _, sc := range ts.Sensors {
			if !sc.ReadOK {
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

	// Step 2: lowest-at-idle heuristic after admissibility filter.
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
