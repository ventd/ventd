package nbfc

import (
	"strings"

	nbfcdb "github.com/ventd/ventd/internal/hwdb/nbfc"
)

// pwmToRegister scales a 0-255 PWM byte from the controller's
// hal.Channel domain into the upstream `[MinSpeedValue, MaxSpeedValue]`
// register range. The mapping is linear except where a sparse
// `FanSpeedPercentageOverrides` entry takes precedence (e.g. "0%
// maps to register byte 0x42" on some HP Omens).
//
// Inversion: in nbfc-linux's convention, MinSpeedValue corresponds
// to the SLOWEST fan and MaxSpeedValue corresponds to FASTEST. On
// some configs MinSpeedValue > MaxSpeedValue (inverted polarity);
// the linear interpolation still works (sign just flips through the
// formula).
func pwmToRegister(pwm uint8, fan nbfcdb.FanConfiguration) uint16 {
	// Check sparse percentage overrides first (Write or ReadWrite scope).
	pct := float64(pwm) * 100.0 / 255.0
	for _, ov := range fan.FanSpeedPercentageOverrides {
		op := strings.ToLower(strings.TrimSpace(ov.TargetOperation))
		if op != "" && op != "write" && op != "readwrite" {
			continue
		}
		if approxEq(ov.FanSpeedPercentage, pct) {
			return ov.FanSpeedValue
		}
	}
	// Linear scale.
	lo, hi := float64(fan.MinSpeedValue), float64(fan.MaxSpeedValue)
	v := lo + (hi-lo)*pct/100.0
	return clampUint16(v, lo, hi)
}

// registerToPWM is the inverse: convert a register value back to a
// 0-255 PWM byte. Uses the read range when independent.
func registerToPWM(reg uint16, fan nbfcdb.FanConfiguration) uint8 {
	lo, hi := float64(fan.MinSpeedValue), float64(fan.MaxSpeedValue)
	if fan.IndependentReadMinMaxValues {
		lo = float64(fan.MinSpeedValueRead)
		hi = float64(fan.MaxSpeedValueRead)
	}
	// Sparse read-side overrides.
	for _, ov := range fan.FanSpeedPercentageOverrides {
		op := strings.ToLower(strings.TrimSpace(ov.TargetOperation))
		if op != "read" && op != "readwrite" {
			continue
		}
		if ov.FanSpeedValue == reg {
			return percentageToPWM(ov.FanSpeedPercentage)
		}
	}
	if hi == lo {
		return 0
	}
	pct := (float64(reg) - lo) * 100.0 / (hi - lo)
	return percentageToPWM(pct)
}

func percentageToPWM(pct float64) uint8 {
	v := pct * 255.0 / 100.0
	switch {
	case v < 0:
		return 0
	case v > 255:
		return 255
	default:
		return uint8(v + 0.5) // round
	}
}

func clampUint16(v, lo, hi float64) uint16 {
	if lo > hi {
		lo, hi = hi, lo
	}
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	if v < 0 {
		return 0
	}
	if v > 65535 {
		return 65535
	}
	return uint16(v + 0.5)
}

func approxEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.5
}
