package curve

// Fixed always returns a constant PWM value regardless of sensor readings.
// Useful for manual fan control or preset speed levels.
type Fixed struct {
	Value uint8
}

func (c *Fixed) Evaluate(sensors map[string]float64) uint8 {
	return c.Value
}
