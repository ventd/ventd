package curve

import (
	"math"
	"sort"
)

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
	// Opt-5: binary search replaces the O(n) linear scan. Anchors are sorted
	// ascending by Temp at config load (validate()), so sort.Search is safe.
	// After the guards above, tempC is strictly between first and last, so i
	// is in [1, len-1] and Anchors[i-1]/Anchors[i] bracket the temperature.
	i := sort.Search(len(c.Anchors), func(k int) bool { return c.Anchors[k].Temp >= tempC })
	lo := c.Anchors[i-1]
	hi := c.Anchors[i]
	ratio := (tempC - lo.Temp) / (hi.Temp - lo.Temp)
	pwm := float64(lo.PWM) + ratio*(float64(hi.PWM)-float64(lo.PWM))
	return uint8(math.Round(pwm))
}
