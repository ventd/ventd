package controller

import (
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/curve"
)

// stubStatelessCurve returns a fixed pre-clamp PWM and implements only
// curve.Curve, so computePWM takes the stateless branch.
type stubStatelessCurve struct{ raw uint8 }

func (s stubStatelessCurve) Evaluate(map[string]float64) uint8 { return s.raw }

// stubStatefulCurve additionally carries a PI state value back out so a
// test can assert computePWM threads it through unchanged.
type stubStatefulCurve struct {
	raw    uint8
	nextPI curve.PIState
}

func (s stubStatefulCurve) Evaluate(map[string]float64) uint8 { return s.raw }
func (s stubStatefulCurve) EvaluateStateful(_ map[string]float64, _ any, _ float64) (uint8, any) {
	return s.raw, s.nextPI
}

// TestComputePWM_ClampBeforeBlendContract pins the safety ordering R5b
// single-sources: the hard [MinPWM, MaxPWM] clamp runs BEFORE the
// predictive blend (so the reactive PWM handed to blendFn is in-bounds)
// and the blend result is re-clamped AFTER (so the predictive arm can't
// push the fan outside operator bounds either).
func TestComputePWM_ClampBeforeBlendContract(t *testing.T) {
	t.Parallel()
	fan := config.Fan{MinPWM: 50, MaxPWM: 200}
	now := time.Now()

	// 1. Clamp authority, no blend: a curve over MaxPWM clamps down, and
	//    raw reports the pre-clamp value for the tick's debug log.
	pwm, _, stateful, raw := computePWM(stubStatelessCurve{raw: 250}, nil,
		curve.PIState{}, 2.0, fan, nil, "cpu", "/p", now)
	if raw != 250 {
		t.Errorf("raw = %d, want 250 (pre-clamp curve output)", raw)
	}
	if pwm != 200 {
		t.Errorf("pwm = %d, want 200 (clamped to MaxPWM)", pwm)
	}
	if stateful {
		t.Errorf("stateful = true, want false for a stateless curve")
	}

	// 2. Clamp BEFORE blend: blendFn must receive the already-clamped
	//    reactive PWM (200), never the raw 250.
	sensors := map[string]float64{"cpu": 70}
	var gotReactive uint8 = 255
	passthrough := func(_ string, _ float64, reactive uint8, _ time.Duration, _ time.Time) uint8 {
		gotReactive = reactive
		return reactive
	}
	pwm, _, _, _ = computePWM(stubStatelessCurve{raw: 250}, sensors,
		curve.PIState{}, 2.0, fan, passthrough, "cpu", "/p", now)
	if gotReactive != 200 {
		t.Errorf("blendFn received reactivePWM = %d, want 200 (clamp must precede blend)", gotReactive)
	}
	if pwm != 200 {
		t.Errorf("pwm = %d, want 200 (passthrough of clamped reactive)", pwm)
	}

	// 3. Re-clamp AFTER blend: a blend result outside bounds is clamped
	//    in both directions.
	over := func(_ string, _ float64, _ uint8, _ time.Duration, _ time.Time) uint8 { return 255 }
	pwm, _, _, _ = computePWM(stubStatelessCurve{raw: 100}, sensors,
		curve.PIState{}, 2.0, fan, over, "cpu", "/p", now)
	if pwm != 200 {
		t.Errorf("over-MaxPWM blend: pwm = %d, want 200 (re-clamp to MaxPWM)", pwm)
	}
	under := func(_ string, _ float64, _ uint8, _ time.Duration, _ time.Time) uint8 { return 0 }
	pwm, _, _, _ = computePWM(stubStatelessCurve{raw: 100}, sensors,
		curve.PIState{}, 2.0, fan, under, "cpu", "/p", now)
	if pwm != 50 {
		t.Errorf("under-MinPWM blend: pwm = %d, want 50 (re-clamp to MinPWM)", pwm)
	}

	// 4. Blend skipped when the bound sensor has no reading this tick —
	//    the clamped curve output stands.
	blendCalled := false
	guard := func(_ string, _ float64, _ uint8, _ time.Duration, _ time.Time) uint8 {
		blendCalled = true
		return 255
	}
	pwm, _, _, _ = computePWM(stubStatelessCurve{raw: 100}, map[string]float64{},
		curve.PIState{}, 2.0, fan, guard, "cpu", "/p", now)
	if blendCalled {
		t.Errorf("blendFn called despite missing sensor reading")
	}
	if pwm != 100 {
		t.Errorf("no-blend pwm = %d, want 100 (clamped curve output)", pwm)
	}
}

// TestComputePWM_StatefulCarriesState verifies the stateful branch
// reports stateful=true and threads the curve's new PI state back to the
// caller (which owns persisting it).
func TestComputePWM_StatefulCarriesState(t *testing.T) {
	t.Parallel()
	fan := config.Fan{MinPWM: 0, MaxPWM: 255}
	want := curve.PIState{Integral: 42}

	pwm, newPI, stateful, raw := computePWM(
		stubStatefulCurve{raw: 150, nextPI: want},
		map[string]float64{"cpu": 70}, curve.PIState{}, 2.0, fan, nil, "cpu", "/p", time.Now())

	if !stateful {
		t.Errorf("stateful = false, want true for a StatefulCurve")
	}
	if newPI != want {
		t.Errorf("newPI = %+v, want %+v (carried from EvaluateStateful)", newPI, want)
	}
	if raw != 150 || pwm != 150 {
		t.Errorf("raw=%d pwm=%d, want 150/150 (in-bounds, no blend)", raw, pwm)
	}
}
