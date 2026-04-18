package curve

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// defaultPICurve returns a PICurve with the documented example parameters.
func defaultPICurve() *PICurve {
	return &PICurve{
		SensorName:    "cpu",
		Setpoint:      65.0,
		Kp:            2.5,
		Ki:            0.1,
		FeedForward:   80,
		IntegralClamp: 100.0,
	}
}

// TestPI_StepResponse_Convergence drives the curve from Setpoint+20 to
// Setpoint-20. After the step, PWM must monotonically decrease and must
// pass through FeedForward ± 2 within 100 ticks.
func TestPI_StepResponse_Convergence(t *testing.T) {
	c := defaultPICurve()
	dt := 0.5

	// Phase 1: build up integral at hot temperature.
	hotSensors := map[string]float64{"cpu": c.Setpoint + 20}
	var st any = PIState{}
	for i := 0; i < 120; i++ {
		_, st = c.EvaluateStateful(hotSensors, st, dt)
	}
	ps := st.(PIState)
	if ps.Integral < 50 {
		t.Errorf("expected integral to build up before step, got %.2f", ps.Integral)
	}

	// Phase 2: step to cold and assert convergence.
	coldSensors := map[string]float64{"cpu": c.Setpoint - 20}
	prevPWM := uint8(255)
	foundFF := false

	for i := 0; i < 100; i++ {
		var pwm uint8
		pwm, st = c.EvaluateStateful(coldSensors, st, dt)
		if pwm > prevPWM {
			t.Errorf("tick %d: PWM increased from %d to %d — not monotonically non-increasing", i, prevPWM, pwm)
		}
		prevPWM = pwm
		ff := int(c.FeedForward)
		if int(pwm) >= ff-2 && int(pwm) <= ff+2 {
			foundFF = true
		}
	}

	if !foundFF {
		t.Errorf("PWM never converged to FeedForward±2 (FF=%d) within 100 ticks after step", c.FeedForward)
	}
}

// TestPI_AntiWindup_SaturationDoesNotAccumulate holds sensor at Setpoint+30
// for 1000 ticks (P alone saturates output). Integral must never exceed
// IntegralClamp, and after returning to setpoint PWM must not overshoot
// downward past FeedForward-5.
func TestPI_AntiWindup_SaturationDoesNotAccumulate(t *testing.T) {
	// Kp=10: P = 10 * 30 = 300 > 255. Back-calc anti-windup fires every tick.
	c := &PICurve{
		SensorName:    "cpu",
		Setpoint:      65.0,
		Kp:            10.0,
		Ki:            0.1,
		FeedForward:   80,
		IntegralClamp: 100.0,
	}
	dt := 0.5

	hot := map[string]float64{"cpu": c.Setpoint + 30}
	var st any = PIState{}
	for i := 0; i < 1000; i++ {
		_, newSt := c.EvaluateStateful(hot, st, dt)
		st = newSt
		ps := newSt.(PIState)
		if math.Abs(ps.Integral) > c.IntegralClamp {
			t.Errorf("tick %d: integral %.2f exceeded IntegralClamp %.2f", i, ps.Integral, c.IntegralClamp)
		}
	}

	// Drop to setpoint and measure minimum PWM over 100 ticks.
	setpointSensors := map[string]float64{"cpu": c.Setpoint}
	var minPWM uint8 = 255
	for i := 0; i < 100; i++ {
		var pwm uint8
		pwm, st = c.EvaluateStateful(setpointSensors, st, dt)
		if pwm < minPWM {
			minPWM = pwm
		}
	}

	threshold := uint8(0)
	if c.FeedForward > 5 {
		threshold = c.FeedForward - 5
	}
	if minPWM < threshold {
		t.Errorf("PWM after return to setpoint: min=%d, want ≥ FeedForward-5=%d (windup would cause deep undershoot)", minPWM, threshold)
	}
}

// TestPI_NaNSensor_FailsHigh verifies fail mode 2: NaN sensor → PWM=255, I=0.
func TestPI_NaNSensor_FailsHigh(t *testing.T) {
	c := defaultPICurve()
	sensors := map[string]float64{"cpu": math.NaN()}
	prevState := any(PIState{Integral: 50})

	pwm, newState := c.EvaluateStateful(sensors, prevState, 1.0)
	ps := newState.(PIState)

	if pwm != 255 {
		t.Errorf("NaN sensor: PWM = %d, want 255", pwm)
	}
	if ps.Integral != 0 {
		t.Errorf("NaN sensor: Integral = %.2f, want 0", ps.Integral)
	}
}

// TestPI_MissingSensor_FailsHigh verifies fail mode 1: missing sensor → PWM=255, I=0.
func TestPI_MissingSensor_FailsHigh(t *testing.T) {
	c := defaultPICurve()
	sensors := map[string]float64{"other_sensor": 70.0}
	prevState := any(PIState{Integral: 42})

	pwm, newState := c.EvaluateStateful(sensors, prevState, 1.0)
	ps := newState.(PIState)

	if pwm != 255 {
		t.Errorf("missing sensor: PWM = %d, want 255", pwm)
	}
	if ps.Integral != 0 {
		t.Errorf("missing sensor: Integral = %.2f, want 0", ps.Integral)
	}
}

// TestPI_DtZero_NoIntegralUpdate verifies that dt=0 leaves integral unchanged.
func TestPI_DtZero_NoIntegralUpdate(t *testing.T) {
	c := defaultPICurve()
	sensors := map[string]float64{"cpu": 70.0}
	initial := PIState{Integral: 33.0}

	_, newState := c.EvaluateStateful(sensors, any(initial), 0)
	ps := newState.(PIState)

	if ps.Integral != initial.Integral {
		t.Errorf("dt=0: Integral changed from %.2f to %.2f, want unchanged", initial.Integral, ps.Integral)
	}
}

// TestPI_DtNegative_IntegralUnchanged_Warned verifies fail mode 3: dt<0 →
// integral unchanged, PWM computed from P+FF only.
func TestPI_DtNegative_IntegralUnchanged_Warned(t *testing.T) {
	c := defaultPICurve()
	sensors := map[string]float64{"cpu": 75.0} // err = 75-65 = 10
	initial := PIState{Integral: 55.0}

	pwm, newState := c.EvaluateStateful(sensors, any(initial), -1.0)
	ps := newState.(PIState)

	if ps.Integral != initial.Integral {
		t.Errorf("dt<0: Integral changed from %.2f to %.2f, want unchanged", initial.Integral, ps.Integral)
	}

	// PWM must be P+FF only (no integral contribution from this tick).
	errVal := 75.0 - c.Setpoint // = 10
	wantOutput := float64(c.FeedForward) + c.Kp*errVal
	wantPWM := clampU8(wantOutput)
	if pwm != wantPWM {
		t.Errorf("dt<0: PWM = %d, want %d (P+FF only, no integral)", pwm, wantPWM)
	}
}

// TestPI_ConfigValidation_RejectsBadGains exercises all rejection paths in
// config.validate() for type="pi". Each case must return an error naming the
// specific invalid field.
func TestPI_ConfigValidation_RejectsBadGains(t *testing.T) {
	// buildYAML constructs a minimal valid config with one PI curve.
	// Pass field overrides as key→value pairs; use "skip_<field>" to omit a field.
	buildYAML := func(overrides map[string]interface{}) string {
		kp := 2.5
		ki := 0.1
		sp := 65.0
		ic := 100.0
		ff := 80

		for k, v := range overrides {
			switch k {
			case "kp":
				kp = v.(float64)
			case "ki":
				ki = v.(float64)
			case "setpoint":
				sp = v.(float64)
			case "integral_clamp":
				ic = v.(float64)
			case "feed_forward":
				ff = v.(int)
			}
		}

		_, skipSP := overrides["skip_setpoint"]
		_, skipKP := overrides["skip_kp"]
		_, skipKI := overrides["skip_ki"]
		_, skipFF := overrides["skip_feed_forward"]
		_, skipIC := overrides["skip_integral_clamp"]

		y := "version: 1\npoll_interval: 2s\n"
		y += "web:\n  listen: 127.0.0.1:9999\n"
		y += "sensors:\n  - name: cpu\n    type: hwmon\n    path: /sys/class/hwmon/hwmon0/temp1_input\n"
		y += "fans:\n  - name: fan1\n    type: hwmon\n    pwm_path: /sys/class/hwmon/hwmon0/pwm1\n    min_pwm: 30\n    max_pwm: 255\n"
		y += "curves:\n  - name: pi_curve\n    type: pi\n    sensor: cpu\n"
		if !skipSP {
			y += fmt.Sprintf("    setpoint: %g\n", sp)
		}
		if !skipKP {
			y += fmt.Sprintf("    kp: %g\n", kp)
		}
		if !skipKI {
			y += fmt.Sprintf("    ki: %g\n", ki)
		}
		if !skipFF {
			y += fmt.Sprintf("    feed_forward: %d\n", ff)
		}
		if !skipIC {
			y += fmt.Sprintf("    integral_clamp: %g\n", ic)
		}
		y += "controls:\n  - fan: fan1\n    curve: pi_curve\n"
		return y
	}

	cases := []struct {
		name      string
		overrides map[string]interface{}
		wantField string
	}{
		{"kp_zero", map[string]interface{}{"kp": 0.0}, "kp"},
		{"kp_negative", map[string]interface{}{"kp": -1.0}, "kp"},
		{"kp_too_large", map[string]interface{}{"kp": 101.0}, "kp"},
		{"ki_negative", map[string]interface{}{"ki": -0.1}, "ki"},
		{"ki_too_large", map[string]interface{}{"ki": 101.0}, "ki"},
		{"setpoint_negative", map[string]interface{}{"setpoint": -1.0}, "setpoint"},
		{"setpoint_too_large", map[string]interface{}{"setpoint": 200.0}, "setpoint"},
		{"integral_clamp_zero", map[string]interface{}{"integral_clamp": 0.0}, "integral_clamp"},
		{"integral_clamp_too_large", map[string]interface{}{"integral_clamp": 256.0}, "integral_clamp"},
		{"missing_setpoint", map[string]interface{}{"skip_setpoint": true}, "setpoint"},
		{"missing_kp", map[string]interface{}{"skip_kp": true}, "kp"},
		{"missing_ki", map[string]interface{}{"skip_ki": true}, "ki"},
		{"missing_feed_forward", map[string]interface{}{"skip_feed_forward": true}, "feed_forward"},
		{"missing_integral_clamp", map[string]interface{}{"skip_integral_clamp": true}, "integral_clamp"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yml := buildYAML(tc.overrides)
			_, err := config.Parse([]byte(yml))
			if err == nil {
				t.Fatalf("Parse succeeded, want error mentioning %q\nYAML:\n%s", tc.wantField, yml)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("error %q does not mention field %q", err.Error(), tc.wantField)
			}
		})
	}
}
