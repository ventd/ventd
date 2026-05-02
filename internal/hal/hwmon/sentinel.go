package hwmon

import (
	"path/filepath"
	"strings"
)

// Sentinel and plausibility constants for hwmon sysfs reads.
//
// nct6687 (and several other super-I/O chips) return 0xFFFF from registers
// that are in mid-latch or unmapped. These appear as:
//
//	temp*_input → 255500 millidegrees (= 255.5°C after ÷1000)
//	fan*_input  → 65535 RPM (raw, no scaling)
//	in*_input   → 65535 millivolts (= 65.535 V after ÷1000)
//
// Values that escape plausibility caps are also rejected: no consumer CPU
// or chipset reaches 150°C without having already triggered a thermal
// shutdown, and no PSU rail exceeds 20 V.
const (
	// SentinelRPMRaw is the raw RPM sentinel emitted by drivers when the
	// register is unavailable (0xFFFF). Any read of this exact value is
	// rejected as invalid regardless of the plausibility cap.
	SentinelRPMRaw = 65535

	// PlausibleRPMMax is the highest RPM value that the backend accepts
	// as legitimate. High-speed server fans can reach ~10 000 RPM; consumer
	// fans are typically below 4 000. Values above this cap are rejected.
	PlausibleRPMMax = 10000

	// PlausibleTempMaxCelsius is the highest temperature (in °C, post-scale)
	// that the controller treats as a valid reading. The sentinel 255.5°C
	// and every value above 150°C are rejected. No consumer CPU/chipset is
	// operational above this threshold.
	PlausibleTempMaxCelsius = 150.0

	// PlausibleTempMinCelsius is the lowest temperature (in °C, post-scale)
	// the controller treats as a valid reading. Any reading at or below
	// −273.15°C is below absolute zero — physically impossible and a
	// sign of a sensor latch error or signed/unsigned underflow in a
	// driver. Research-validated against R28 hostile-fan agent + kernel
	// hwmon sysfs-interface docs: drivers historically have no canonical
	// "value unavailable" signal, so a sub-absolute-zero filter is the
	// defensive complement to the high-end PlausibleTempMaxCelsius cap.
	// The Framework 13 AMD 7040 EC reports −17.x°C from an I2C bus
	// underflow; that real-world degraded reading is still passed
	// through (operator UI surfaces it for triage). Only physically
	// impossible values are filtered here.
	PlausibleTempMinCelsius = -273.15

	// PlausibleVoltageMaxVolts is the highest voltage (in V, post-scale) that
	// the controller treats as valid. The 0xFFFF sentinel maps to ~65.5 V;
	// no standard PSU rail exceeds 20 V.
	PlausibleVoltageMaxVolts = 20.0
)

// IsSentinelRPM reports whether raw is a known driver sentinel or exceeds
// the plausibility cap for RPM readings. Used by the hwmon backend's Read()
// to gate the RPM field before populating Reading.
func IsSentinelRPM(raw int) bool {
	return raw == SentinelRPMRaw || raw > PlausibleRPMMax
}

// IsSentinelSensorVal reports whether val (the scaled result already returned
// by hwmon.ReadValue) is a sentinel or implausible for the sensor kind inferred
// from path. Used by the controller's readAllSensors and the web status builder
// to reject bogus values before they reach curve evaluation or the UI.
//
// Inference rules (matching hwmon.ReadValue's divisors):
//
//	temp* files → val is in °C  → reject ≥ PlausibleTempMaxCelsius OR ≤ PlausibleTempMinCelsius
//	in*   files → val is in V   → cap at PlausibleVoltageMaxVolts
//	fan*  files → val is in RPM → cap at PlausibleRPMMax; sentinel = SentinelRPMRaw
func IsSentinelSensorVal(path string, val float64) bool {
	base := filepath.Base(path)
	switch {
	case strings.HasPrefix(base, "temp"):
		return val >= PlausibleTempMaxCelsius || val <= PlausibleTempMinCelsius
	case strings.HasPrefix(base, "in"):
		return val > PlausibleVoltageMaxVolts
	case strings.HasPrefix(base, "fan"):
		return val >= SentinelRPMRaw || val > PlausibleRPMMax
	}
	return false
}
