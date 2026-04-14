// Package curve defines the Curve interface and built-in implementations.
// Supported types — M1: Linear. M4: Mix. Future: Trigger, Sync, Offset.
package curve

// Curve maps a set of sensor readings to a PWM duty cycle (0–255).
// sensors is a map of sensor name → temperature in degrees Celsius.
// Implementations must be safe to call concurrently from multiple goroutines.
type Curve interface {
	Evaluate(sensors map[string]float64) uint8
}
