package curve

import (
	"log/slog"
	"math"
)

// PICurve is a proportional-integral controller that drives a fan PWM
// toward a temperature setpoint based on a single sensor input.
//
// Control law per tick:
//
//	err      = sensor - Setpoint           (positive when too hot)
//	P        = Kp * err
//	I_new    = clamp(I_prev + Ki * err * dt, -IntegralClamp, +IntegralClamp)
//	output   = FeedForward + P + I_new    // all in 0..255 PWM units
//	pwm      = clampU8(output)             // final 0..255 uint8
//
// Anti-windup: the integral clamp is applied BEFORE the output clamp,
// and the integral is NOT updated on ticks where the output clamps
// (back-calculation: if the output would saturate, roll back I_new to its
// pre-update value to prevent wind-up against a saturated actuator).
//
// Sign convention: higher temperature → higher PWM (more cooling). Kp > 0.
// A config with Kp <= 0 or Ki < 0 is rejected at validate() time.
//
// Note: PI curves are NOT composable inside a Mix curve in this release.
// Integral state does not survive the Mix combine step meaningfully.
// P4-MPC-01 will revisit composability.
type PICurve struct {
	SensorName    string
	Setpoint      float64 // target temperature, °C
	Kp            float64 // proportional gain, PWM per °C above setpoint
	Ki            float64 // integral gain, PWM per (°C·s)
	FeedForward   uint8   // baseline PWM added to P+I output
	IntegralClamp float64 // max |I| in PWM units; hard anti-windup bound
}

// PIState is the per-channel integral carried across ticks.
type PIState struct {
	Integral float64 // accumulated integral term in PWM units
}

// Evaluate implements Curve. Calling Evaluate on a PICurve discards integral
// action and returns only the proportional + feed-forward response for one
// tick (dt=0, I=0). Use EvaluateStateful for full PI control with integral.
//
// This makes PICurve safe for stateless callers (UI preview, diagnostics)
// and preserves the fail-high contract: a missing or invalid sensor returns 255.
func (c *PICurve) Evaluate(sensors map[string]float64) uint8 {
	pwm, _ := c.EvaluateStateful(sensors, PIState{}, 0)
	return pwm
}

// EvaluateStateful implements StatefulCurve. It computes the PI output for
// this tick and returns the updated PIState. The controller must persist the
// returned state and pass it back on the next tick.
//
// Fail modes and their safe responses:
//  1. Sensor missing from map            → PWM=255, I=0
//  2. Sensor value is NaN or ±Inf       → PWM=255, I=0
//  3. dtSeconds is NaN, ±Inf, or <0     → PWM=FeedForward+P, I unchanged (controller bug; logged at ERROR)
//  4. Arithmetic produces NaN/Inf output → PWM=255, I=0
func (c *PICurve) EvaluateStateful(sensors map[string]float64, state any, dtSeconds float64) (uint8, any) {
	s := PIState{}
	if state != nil {
		if ps, ok := state.(PIState); ok {
			s = ps
		}
	}

	// Fail mode 3: invalid dt is a controller bug — log at ERROR and return
	// P+FF only, keeping the integral unchanged so the caller's stored state
	// is not corrupted.
	if math.IsNaN(dtSeconds) || math.IsInf(dtSeconds, 0) || dtSeconds < 0 {
		slog.Default().Error("picurve: invalid dtSeconds; returning P+FF only, integral unchanged",
			"sensor", c.SensorName, "dt", dtSeconds)
		tempC, ok := sensors[c.SensorName]
		if !ok || math.IsNaN(tempC) || math.IsInf(tempC, 0) {
			return 255, PIState{}
		}
		err := tempC - c.Setpoint
		output := float64(c.FeedForward) + c.Kp*err
		if math.IsNaN(output) || math.IsInf(output, 0) {
			return 255, PIState{}
		}
		return clampU8(output), s
	}

	// Fail mode 1: sensor missing from the readings map.
	tempC, ok := sensors[c.SensorName]
	if !ok {
		slog.Default().Warn("picurve: sensor missing from readings; failing high",
			"sensor", c.SensorName)
		return 255, PIState{}
	}

	// Fail mode 2: sensor value is not a usable number.
	if math.IsNaN(tempC) || math.IsInf(tempC, 0) {
		slog.Default().Warn("picurve: sensor value is NaN/Inf; failing high",
			"sensor", c.SensorName, "value", tempC)
		return 255, PIState{}
	}

	// err > 0 when too hot → higher P → more cooling.
	err := tempC - c.Setpoint

	// Integral update with hard clamp and back-calculation anti-windup.
	prev := s.Integral
	var candidate float64
	if dtSeconds == 0 {
		// Zero dt: no integral contribution. Guards against 0*Inf edge cases.
		candidate = prev
	} else {
		errContribution := c.Ki * err * dtSeconds
		candidate = prev + errContribution
		if candidate > c.IntegralClamp {
			candidate = c.IntegralClamp
		} else if candidate < -c.IntegralClamp {
			candidate = -c.IntegralClamp
		}
		// Back-calculation anti-windup: if the tentative output saturates the
		// 0..255 range, roll the integral back to its pre-update value. This
		// prevents wind-up when the actuator is clamped at a rail.
		output := float64(c.FeedForward) + c.Kp*err + candidate
		if output > 255 || output < 0 {
			candidate = prev
		}
	}
	s.Integral = candidate
	output := float64(c.FeedForward) + c.Kp*err + s.Integral

	// Fail mode 4: guard against extreme gain values producing NaN/Inf.
	if math.IsNaN(output) || math.IsInf(output, 0) {
		slog.Default().Warn("picurve: arithmetic produced NaN/Inf output; failing high",
			"sensor", c.SensorName, "err", err, "integral", s.Integral)
		return 255, PIState{}
	}

	return clampU8(output), s
}

// clampU8 rounds a float64 PWM value to the nearest integer and clamps to [0, 255].
func clampU8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(v))
}
