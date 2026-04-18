// Package curve defines the Curve interface and built-in implementations.
// Supported types — M1: Linear. M4: Mix. Phase 4: PI.
package curve

// Curve maps a set of sensor readings to a PWM duty cycle (0–255).
// sensors is a map of sensor name → temperature in degrees Celsius.
// Implementations must be safe to call concurrently from multiple goroutines.
type Curve interface {
	Evaluate(sensors map[string]float64) uint8
}

// StatefulCurve extends Curve for control laws that accumulate state across
// ticks (PI integral, MPC model residual, etc). The controller is responsible
// for persisting State per-channel and passing it back on each call.
//
// State is opaque to the controller; the curve returns a new state value
// (by value, not pointer) that the controller stores until the next tick.
// This keeps Evaluate callable from a single goroutine per channel without
// curve-side locking, and lets the curve impl choose its state layout.
//
// Implementations MUST be pure in (sensors, state) — same inputs produce
// the same (pwm, newState) — so a stale state value after a config reload
// simply retriggers convergence rather than diverging.
type StatefulCurve interface {
	Curve
	EvaluateStateful(sensors map[string]float64, state any, dtSeconds float64) (pwm uint8, newState any)
}
