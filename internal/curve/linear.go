package curve

import "math"

// Linear maps a named sensor's temperature to PWM via linear interpolation.
//
//	tempC <= MinTemp  →  MinPWM
//	tempC >= MaxTemp  →  MaxPWM
//	between           →  linearly interpolated, rounded to nearest integer
//
// If SensorName is absent from the sensors map, MaxPWM is returned so the fan
// runs at its highest configured speed rather than stalling silently.
type Linear struct {
	SensorName string
	MinTemp    float64
	MaxTemp    float64
	MinPWM     uint8
	MaxPWM     uint8
}

func (c *Linear) Evaluate(sensors map[string]float64) uint8 {
	tempC, ok := sensors[c.SensorName]
	if !ok {
		return c.MaxPWM
	}
	if tempC <= c.MinTemp {
		return c.MinPWM
	}
	if tempC >= c.MaxTemp {
		return c.MaxPWM
	}
	ratio := (tempC - c.MinTemp) / (c.MaxTemp - c.MinTemp)
	pwm := float64(c.MinPWM) + ratio*float64(c.MaxPWM-c.MinPWM)
	return uint8(math.Round(pwm))
}
