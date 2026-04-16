package curve

import "math"

// Points interpolates PWM between a list of (temperature, pwm) anchors.
// Valid configs carry at least two points sorted by ascending Temp
// (validate() reshapes the slice at load time so Evaluate does not need
// to re-sort on every tick).
//
//	tempC <= Anchors[0].Temp              → Anchors[0].PWM
//	tempC >= Anchors[last].Temp           → Anchors[last].PWM
//	Anchors[i].Temp < tempC < Anchors[i+1].Temp → linear interpolation
//
// Missing sensor readings fall back to the last point's PWM — same
// fail-high shape as Linear's "return MaxPWM" contract. Leaving the fan
// at the highest curve value is safer than stalling when a sensor is
// offline.
type Points struct {
	SensorName string
	Anchors    []PointAnchor
}

// PointAnchor is one control point in a Points curve.
type PointAnchor struct {
	Temp float64
	PWM  uint8
}

func (c *Points) Evaluate(sensors map[string]float64) uint8 {
	if len(c.Anchors) == 0 {
		return 0
	}
	if len(c.Anchors) == 1 {
		return c.Anchors[0].PWM
	}
	tempC, ok := sensors[c.SensorName]
	if !ok {
		return c.Anchors[len(c.Anchors)-1].PWM
	}
	first := c.Anchors[0]
	last := c.Anchors[len(c.Anchors)-1]
	if tempC <= first.Temp {
		return first.PWM
	}
	if tempC >= last.Temp {
		return last.PWM
	}
	// Linear scan — Anchors is never long enough to justify a binary
	// search (operators define a handful of points, not thousands).
	for i := 0; i < len(c.Anchors)-1; i++ {
		lo := c.Anchors[i]
		hi := c.Anchors[i+1]
		if tempC < lo.Temp || tempC > hi.Temp {
			continue
		}
		ratio := (tempC - lo.Temp) / (hi.Temp - lo.Temp)
		pwm := float64(lo.PWM) + ratio*(float64(hi.PWM)-float64(lo.PWM))
		return uint8(math.Round(pwm))
	}
	// Unreachable given the guards above, but return last anchor to
	// match the fail-high contract if the invariants ever slip.
	return last.PWM
}
