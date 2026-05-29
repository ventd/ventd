// Package framework vendors the fw-fanctrl Framework-laptop fan-curve
// presets as a Mode-C config corpus (spec-17 PR-2). Framework laptops run
// ChromeOS-derived EC firmware; the mainline cros_ec_hwmon driver exposes the
// EC fan as a standard hwmon pwm (writable since kernel 6.18 — see the
// cros_ec_hwmon driver catalog row), which ventd's existing hwmon backend
// drives directly. fw-fanctrl (github.com/TamtamHero/fw-fanctrl) is the
// canonical userspace tool the Framework community tunes curves with; this
// package vendors its strategy presets so ventd can recognise a Framework
// host and surface proven fan curves the operator can adopt.
//
// This is NOT an EC-register corpus like internal/hwdb/nbfc/: Framework needs
// no raw register map (cros_ec_hwmon gives hwmon), and fw-fanctrl itself talks
// to the EC via ectool. ventd never shells out to ectool or fw-fanctrl — these
// are reference curves only, so there is no register/ACPI allowlist to gate.
package framework

import "strings"

// SpeedPoint is one anchor of a fw-fanctrl speedCurve: a temperature in whole
// degrees Celsius mapped to a fan duty percentage (0-100). fw-fanctrl
// interpolates linearly between adjacent points.
type SpeedPoint struct {
	TempC    int `json:"temp"`
	SpeedPct int `json:"speed"`
}

// Strategy is one named fw-fanctrl fan-control preset. fanSpeedUpdateFrequency
// (seconds between EC writes) and movingAverageInterval (seconds of temperature
// smoothing) are fw-fanctrl runtime tunables retained verbatim for fidelity;
// ventd's own poll interval + smoothing supersede them when a curve is adopted.
type Strategy struct {
	FanSpeedUpdateFrequency int          `json:"fanSpeedUpdateFrequency"`
	MovingAverageInterval   int          `json:"movingAverageInterval"`
	SpeedCurve              []SpeedPoint `json:"speedCurve"`
}

// Config is one vendored fw-fanctrl config file. DefaultStrategy is the curve
// used on AC; StrategyOnDischarging, when non-empty, is the (quieter) curve
// fw-fanctrl switches to on battery — the AC-vs-battery dual-strategy
// mechanism. BatteryChargingStatusPath is the fw-fanctrl-AMD fork's sysfs
// charging-state path; ventd does not use it (it has its own battery gate) but
// it is preserved so the vendored config round-trips.
type Config struct {
	DefaultStrategy           string              `json:"defaultStrategy"`
	StrategyOnDischarging     string              `json:"strategyOnDischarging"`
	BatteryChargingStatusPath string              `json:"batteryChargingStatusPath,omitempty"`
	Strategies                map[string]Strategy `json:"strategies"`
}

// StrategyNames returns the strategy names in deterministic (sorted) order so
// callers (doctor surface, tests) render a stable list. The underlying map is
// unordered.
func (c *Config) StrategyNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.Strategies))
	for name := range c.Strategies {
		names = append(names, name)
	}
	// Simple insertion sort keeps the dependency surface nil — the lists are
	// tiny (≤8 entries) so allocation/scan cost is irrelevant.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// dischargeStrategy reports the on-battery strategy name, or "" when the config
// does not switch curves on battery.
func (c *Config) dischargeStrategy() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.StrategyOnDischarging)
}
