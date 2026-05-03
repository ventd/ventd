package controller

import (
	"math"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/coupling"
	"github.com/ventd/ventd/internal/marginal"
)

// healthyCoupling builds a Snapshot that passes every instability
// guard (a∈(0,1), b≠0, !WarmingUp, Kappa≤1e4). Used by tests that
// want to exercise the predictive path without artificial blockers.
func healthyCoupling(channelID string, a, b float64) *coupling.Snapshot {
	return &coupling.Snapshot{
		ChannelID: channelID,
		Kind:      coupling.KindHealthy,
		Theta:     []float64{a, b},
		NSamples:  500,
		TrP:       0.1,
		Kappa:     10,
		WarmingUp: false,
	}
}

func defaultInputs(coupSnap *coupling.Snapshot) BlendedInputs {
	return BlendedInputs{
		ChannelID:    "/sys/class/hwmon/hwmon3/pwm1",
		SensorTemp:   70.0,
		Setpoint:     60.0,
		ReactivePWM:  150,
		WPred:        1.0,
		Coupling:     coupSnap,
		Marginal:     nil, // no Layer-C ⇒ Path-A and cost gate skipped
		LayerA:       nil,
		LoadFraction: 0.5,
		DT:           2 * time.Second,
		Now:          time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		MinPWM:       0,
		MaxPWM:       255,
	}
}

// RULE-CTRL-PI-01: Verify gain derivation produces K_p = τ/(K(λ+θ))
// with K = |b/(1−a)| (the corrected magnitude form). At a=0.98,
// b=−0.5, dt=2s, Balanced preset, the analytical answer is:
//
//	K   = |−0.5 / 0.02| = 25
//	τ   = -2/ln(0.98) ≈ 99.0s (clamped if needed)
//	λ   = τ (Balanced)
//	θ   = 2
//	K_p = τ / (K · (λ + θ))
func TestPI_GainDerivation(t *testing.T) {
	t.Parallel()
	a := 0.98
	b := -0.5
	dt := 2.0
	Kp, Ki, tau, ok := deriveIMCPIGains(a, b, dt, PresetBalanced)
	if !ok {
		t.Fatalf("deriveIMCPIGains returned ok=false")
	}
	expectedTau := -dt / math.Log(a)
	if math.Abs(tau-expectedTau) > 1e-6 {
		t.Fatalf("tau = %v, want %v", tau, expectedTau)
	}
	K := math.Abs(b / (1 - a))
	// Float arithmetic on 0.98 isn't exact (binary representation
	// adds ~1e-16 noise), so allow a small tolerance on the K=25
	// sanity check.
	if math.Abs(K-25.0) > 1e-9 {
		t.Fatalf("|K| = %v, want ≈ 25.0", K)
	}
	expectedKp := tau / (K * (tau + dt)) // λ = τ for Balanced
	if math.Abs(Kp-expectedKp) > 1e-9 {
		t.Fatalf("K_p = %v, want %v (corrected |K| form)", Kp, expectedKp)
	}
	if math.Abs(Ki-Kp/tau) > 1e-9 {
		t.Fatalf("K_i = %v, want K_p/τ = %v", Ki, Kp/tau)
	}
	// Critically: K_p must be POSITIVE (the spec correction).
	if Kp <= 0 {
		t.Fatalf("K_p = %v, want > 0 (the |K| sign correction)", Kp)
	}
}

// RULE-CTRL-PI-02: bumpless transfer — first w_pred>0 tick produces
// predictive_output == reactive_output (no PWM step on handoff).
func TestPI_BumplessTransfer_FirstWPredTick(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced, PWMUnitMax: 255})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.WPred = 0
	// Tick 1: w_pred=0 ⇒ pure reactive, bumpless stays armed.
	r1 := bc.Compute(in)
	if r1.OutputPWM != in.ReactivePWM {
		t.Fatalf("tick 1 (w_pred=0): output %d, want reactive %d", r1.OutputPWM, in.ReactivePWM)
	}
	// Tick 2: w_pred=1 ⇒ first predictive tick ⇒ bumpless init must
	// produce predictive == reactive. With u[0]=0, predictive PWM
	// rounds to reactive PWM.
	in.WPred = 1.0
	in.Now = in.Now.Add(2 * time.Second)
	r2 := bc.Compute(in)
	if r2.OutputPWM != in.ReactivePWM {
		t.Fatalf("tick 2 (first w_pred>0): output %d, want bumpless %d (no step)",
			r2.OutputPWM, in.ReactivePWM)
	}
	if r2.UIState != "blended" {
		t.Fatalf("tick 2 UIState = %q, want blended", r2.UIState)
	}
}

// RULE-CTRL-PI-03: τ clamped to [TauMinSeconds, TauMaxSeconds].
// a → 1 normally produces unbounded τ; the cap prevents it.
func TestPI_TauClampedToMinMax(t *testing.T) {
	t.Parallel()
	dt := 2.0
	// a very close to 1 ⇒ τ would explode without clamp.
	_, _, tauHi, _ := deriveIMCPIGains(0.9999, -0.5, dt, PresetBalanced)
	if tauHi > TauMaxSeconds+1e-6 {
		t.Fatalf("τ at a=0.9999: got %v, want ≤ %v", tauHi, TauMaxSeconds)
	}
	// a very small ⇒ τ would be tiny without clamp.
	_, _, tauLo, _ := deriveIMCPIGains(0.001, -0.5, dt, PresetBalanced)
	if tauLo < TauMinSeconds-1e-6 {
		t.Fatalf("τ at a=0.001: got %v, want ≥ %v", tauLo, TauMinSeconds)
	}
}

// RULE-CTRL-PI-05: instability guards refuse predictive output when
// any of the six conditions trip. Each subtest exercises one path.
func TestPI_InstabilityGuards_AllSixCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		snap *coupling.Snapshot
	}{
		{"nil snapshot", nil},
		{"WarmingUp", &coupling.Snapshot{Theta: []float64{0.98, -0.5}, WarmingUp: true}},
		{"a<=0", &coupling.Snapshot{Theta: []float64{-0.1, -0.5}}},
		{"a>=1", &coupling.Snapshot{Theta: []float64{1.0, -0.5}}},
		{"b=0", &coupling.Snapshot{Theta: []float64{0.98, 0.0}}},
		{"Kappa>1e4", &coupling.Snapshot{Theta: []float64{0.98, -0.5}, Kappa: 1e5}},
		{"NaN theta", &coupling.Snapshot{Theta: []float64{math.NaN(), -0.5}}},
		{"short Theta", &coupling.Snapshot{Theta: []float64{0.98}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bc := NewBlended(BlendedConfig{Preset: PresetBalanced})
			in := defaultInputs(tc.snap)
			r := bc.Compute(in)
			if !r.PIRefused {
				t.Fatalf("%s: PIRefused=false, want true (output=%d, state=%s)",
					tc.name, r.OutputPWM, r.UIState)
			}
			if r.OutputPWM != in.ReactivePWM {
				t.Fatalf("%s: output=%d, want reactive=%d", tc.name, r.OutputPWM, in.ReactivePWM)
			}
			if r.WPred != 0 {
				t.Fatalf("%s: WPred=%v, want 0 after refusal", tc.name, r.WPred)
			}
		})
	}
}

// RULE-CTRL-BLEND-01: Linear blend at intermediate w_pred produces
// output = w_pred·predictive + (1-w_pred)·reactive (with rounding +
// clamp). The integrator at b=−0.5 and error=10 moves at ~0.008
// PWM/tick, so the test runs ~150 ticks before measuring the blend
// to ensure predictive has visibly diverged from reactive.
func TestBlend_LinearMix(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced})

	// Tick 1: bumpless ⇒ predictive=reactive=150.
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	r1 := bc.Compute(in)
	if r1.OutputPWM != in.ReactivePWM {
		t.Fatalf("tick 1 bumpless: %d, want %d", r1.OutputPWM, in.ReactivePWM)
	}

	// Run 150 ticks at w_pred=1 to let the integrator accumulate
	// enough correction to be visible in uint8 PWM.
	for i := 0; i < 150; i++ {
		in.Now = in.Now.Add(2 * time.Second)
		_ = bc.Compute(in)
	}

	// At this point predictive should be visibly above reactive.
	in.Now = in.Now.Add(2 * time.Second)
	r2 := bc.Compute(in)
	if r2.PredictivePWM <= in.ReactivePWM {
		t.Fatalf("after warmup: predictive=%d, want > reactive=%d (more cooling when too hot)",
			r2.PredictivePWM, in.ReactivePWM)
	}

	// At w_pred=0.5, output should be midway (rounded).
	in.Now = in.Now.Add(2 * time.Second)
	in.WPred = 0.5
	r3 := bc.Compute(in)
	expected := uint8(math.Round(0.5*float64(r3.PredictivePWM) + 0.5*float64(in.ReactivePWM)))
	if r3.OutputPWM != expected {
		t.Fatalf("blend at w_pred=0.5: got %d, want %d (pred=%d, reactive=%d)",
			r3.OutputPWM, expected, r3.PredictivePWM, in.ReactivePWM)
	}
}

// RULE-CTRL-BLEND-02: First-contact clamp prevents predictive from
// reducing cooling on the first w_pred>0 tick of a channel's lifetime.
// Construct a scenario where predictive would naturally fall below
// reactive (sensor is COLDER than setpoint ⇒ controller wants less
// cooling), then verify first-contact pins to reactive.
func TestBlend_FirstContactClamp_NeverReducesCooling(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.SensorTemp = 50.0 // colder than setpoint (60) ⇒ error=-10
	in.LayerA = &layer_a.Snapshot{
		ChannelID:        in.ChannelID,
		Tier:             layer_a.TierRPMTach,
		R8Ceiling:        1.0,
		ConfA:            0.8,
		SeenFirstContact: false,
	}

	// Tick 1: bumpless ⇒ u=0, predictive=reactive. First-contact
	// clamp shouldn't fire (predictive is not below reactive).
	r1 := bc.Compute(in)
	if r1.FirstContactClamp {
		t.Fatalf("tick 1 bumpless: FirstContactClamp fired but predictive should equal reactive")
	}
	// Run ticks until the integrator drives predictive BELOW reactive
	// (sensor cold ⇒ negative error ⇒ negative u ⇒ less PWM). The
	// per-tick PWM change is ~0.008 units, so ~150 ticks gets us a
	// visible drop in uint8.
	var found bool
	for i := 0; i < 200; i++ {
		in.Now = in.Now.Add(2 * time.Second)
		r := bc.Compute(in)
		if r.FirstContactClamp {
			// Clamp fired ⇒ predictive would have been below reactive
			// without the clamp, output must equal reactive.
			if r.OutputPWM < in.ReactivePWM {
				t.Fatalf("clamp fired but output=%d still below reactive=%d",
					r.OutputPWM, in.ReactivePWM)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("first-contact clamp never fired after 200 ticks")
	}
}

// RULE-CTRL-BLEND-03: w_pred=0 returns reactive bytes-exact and never
// touches the integrator state.
func TestBlend_ZeroWPred_ReturnsReactiveExact(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.WPred = 0
	// Run several ticks at w_pred=0, vary sensor temp.
	for i, temp := range []float64{50, 60, 70, 80, 90} {
		in.SensorTemp = temp
		in.Now = in.Now.Add(2 * time.Second)
		r := bc.Compute(in)
		if r.OutputPWM != in.ReactivePWM {
			t.Fatalf("tick %d (temp=%v, w_pred=0): output=%d, want reactive=%d",
				i, temp, r.OutputPWM, in.ReactivePWM)
		}
		if r.UIState != "reactive" {
			t.Fatalf("tick %d UIState=%q, want reactive", i, r.UIState)
		}
	}
}

// RULE-CTRL-PATH-A-01: Path-A refuses the candidate ramp when
// predicted ΔT < 2°C. Produces a warmed Marginal Snapshot with very
// small slope (margin) so any candidate ΔPWM yields predicted ΔT
// below the threshold.
func TestPathA_RefusalBelow2C_FallsThroughReactive(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	// Marginal: very small margin ⇒ |ΔT| ≈ 0 ⇒ refused.
	in.Marginal = &marginal.Snapshot{
		ChannelID: in.ChannelID,
		Theta:     []float64{0.001, 0.0}, // margin ≈ 0.001 °C/PWM-unit
		WarmingUp: false,
		NSamples:  500,
	}
	// Tick 1: bumpless ⇒ ΔPWM = 0 ⇒ predicted ΔT = 0. Path-A
	// refuses (|0| < 2°C). Verify refusal flag + reactive output.
	in.Now = in.Now.Add(2 * time.Second)
	r := bc.Compute(in)
	if !r.PathARefused {
		t.Fatalf("Path-A: PathARefused=false; predicted_ΔT should be < 2°C")
	}
	if r.OutputPWM != in.ReactivePWM {
		t.Fatalf("Path-A: output=%d, want reactive=%d", r.OutputPWM, in.ReactivePWM)
	}
	if r.UIState != "refused-pathA" {
		t.Fatalf("Path-A: UIState=%q, want refused-pathA", r.UIState)
	}
	// Integrator must be frozen.
	if !r.IntegratorFrozen {
		t.Fatalf("Path-A: IntegratorFrozen=false (anti-windup hook)")
	}
}

// RULE-CTRL-PATH-A-02: Nil Marginal Snapshot ⇒ Path-A is a no-op
// (the layer doesn't exist for this channel; no refusal). The
// controller still produces a normal blended output.
func TestPathA_NilMarginalSnapshot_PathANoOp(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.Marginal = nil
	r := bc.Compute(in)
	if r.PathARefused {
		t.Fatalf("nil Marginal: PathARefused=true, want false")
	}
}

// RULE-CTRL-COST-01: cost factor table 3× / 1× / 0.2× × CostFactorBalanced.
func TestCost_KFactorTable_3x_1x_0p2x(t *testing.T) {
	t.Parallel()
	cases := []struct {
		preset Preset
		want   float64
	}{
		{PresetSilent, 3.0 * CostFactorBalanced},
		{PresetBalanced, CostFactorBalanced},
		{PresetPerformance, 0.2 * CostFactorBalanced},
	}
	for _, tc := range cases {
		got := costFactorForPreset(tc.preset)
		if math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("preset %d: got %v, want %v", tc.preset, got, tc.want)
		}
	}
}

// RULE-CTRL-COST-02: Cost gate refuses ramps where cost > benefit
// and admits ramps where benefit > cost. Tests the gate function
// directly with engineered numbers so the assertion is immediate
// and doesn't depend on the integrator's transient behaviour.
func TestCost_BenefitVsCost_RefusesWhenCostExceedsBenefit(t *testing.T) {
	t.Parallel()
	mSnap := &marginal.Snapshot{
		Theta:     []float64{0.5, 0},
		WarmingUp: false,
		NSamples:  500,
	}
	// Silent preset: k_factor = 3.0 · 0.01 = 0.03 °C/PWM-unit.
	//
	// Case A — predicted_ΔT = -3 °C (substantial cooling), ΔPWM = +6:
	//   cost    = 0.03 · 6 = 0.18
	//   benefit = -(-3)    = 3.0
	//   3.0 > 0.18 ⇒ admit (NOT refused).
	if got := evalCostGate(mSnap, -3.0, 6, PresetSilent); got {
		t.Fatalf("benefit 3 vs cost 0.18: got refused=true, want false")
	}
	// Case B — predicted_ΔT = -0.05 °C (tiny cooling), ΔPWM = +6:
	//   cost    = 0.18
	//   benefit = 0.05
	//   0.05 < 0.18 ⇒ refuse.
	if got := evalCostGate(mSnap, -0.05, 6, PresetSilent); !got {
		t.Fatalf("benefit 0.05 vs cost 0.18: got refused=false, want true")
	}
	// Case C — same tiny benefit but Performance preset:
	//   k_factor = 0.2 · 0.01 = 0.002, cost = 0.002 · 6 = 0.012.
	//   0.05 > 0.012 ⇒ admit.
	if got := evalCostGate(mSnap, -0.05, 6, PresetPerformance); got {
		t.Fatalf("Performance benefit 0.05 vs cost 0.012: got refused=true, want false")
	}
	// Case D — nil Marginal ⇒ no refusal (Layer-C absent).
	if got := evalCostGate(nil, -0.05, 6, PresetSilent); got {
		t.Fatalf("nil Marginal: got refused=true, want false (gate is no-op)")
	}
}

// RULE-CTRL-PRESET-03: PresetDBATargets locks the canonical R32 mapping
// — Silent: 25 dBA, Balanced: 32 dBA, Performance: 45 dBA — and
// DBATargetFor honours operator overrides over preset defaults.
func TestPresetDBATargets_LockedAndOverrideHonoured(t *testing.T) {
	t.Parallel()
	cases := []struct {
		preset   Preset
		expected float64
	}{
		{PresetSilent, 25.0},
		{PresetBalanced, 32.0},
		{PresetPerformance, 45.0},
	}
	for _, tc := range cases {
		got, ok := PresetDBATargets[tc.preset]
		if !ok || got != tc.expected {
			t.Errorf("PresetDBATargets[%v] = (%v, %v), want (%v, true)",
				tc.preset, got, ok, tc.expected)
		}
		// DBATargetFor with nil override returns the preset default.
		if got := DBATargetFor(tc.preset, nil); got != tc.expected {
			t.Errorf("DBATargetFor(%v, nil) = %v, want %v", tc.preset, got, tc.expected)
		}
	}
	// Operator override beats preset default.
	override := 28.0
	if got := DBATargetFor(PresetBalanced, &override); got != 28.0 {
		t.Errorf("DBATargetFor(Balanced, &28) = %v, want 28.0", got)
	}
	// Unknown preset enum falls back to Balanced default.
	if got := DBATargetFor(Preset(99), nil); got != 32.0 {
		t.Errorf("DBATargetFor(unknown, nil) = %v, want 32.0 (Balanced fallback)", got)
	}
}

// RULE-CTRL-PRESET-04 (Compute integration): when the dBA-budget gate
// refuses, Compute returns OutputPWM = ReactivePWM, sets
// DBABudgetRefused=true, populates PredictedDBA, and surfaces the
// refusal as UIState="refused-dba". Matches cost-gate semantics — no
// integrator freeze on dBA refusal (the integrator continues to
// accumulate and recovers naturally when conditions change).
func TestBlend_DBABudget_RefusesPredictiveAboveTarget(t *testing.T) {
	t.Parallel()

	bc := NewBlended(BlendedConfig{Preset: PresetBalanced, PWMUnitMax: 255})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.SensorTemp = 80.0 // high error so predictive ramps up over time
	in.Setpoint = 60.0
	// CurrentDBA pinned just under Target so any positive predictive
	// ramp (any non-zero ΔPWM with positive DBAPerPWM) trips the gate.
	in.Acoustic = AcousticBudget{
		Target:     25.0,
		CurrentDBA: 24.99,
		DBAPerPWM:  1.0,
	}

	// Warm up enough ticks for the integrator to accumulate above
	// reactive. Mirrors TestBlend_LinearMix's pattern (150 ticks at
	// a=0.98, b=-0.5 produces a measurable predictive>reactive delta).
	for i := 0; i < 150; i++ {
		_ = bc.Compute(in)
		in.Now = in.Now.Add(2 * time.Second)
	}
	r := bc.Compute(in)

	if !r.DBABudgetRefused {
		t.Fatalf("expected DBABudgetRefused=true; got %+v", r)
	}
	if r.OutputPWM != in.ReactivePWM {
		t.Errorf("OutputPWM = %d, want reactive %d on dBA refusal",
			r.OutputPWM, in.ReactivePWM)
	}
	if r.UIState != "refused-dba" {
		t.Errorf("UIState = %q, want \"refused-dba\"", r.UIState)
	}
	if r.PredictedDBA <= in.Acoustic.Target {
		t.Errorf("PredictedDBA = %v, expected > Target=%v",
			r.PredictedDBA, in.Acoustic.Target)
	}
	if r.WPred != 0 {
		t.Errorf("WPred = %v, want 0 on refusal", r.WPred)
	}
}

// RULE-CTRL-PRESET-04 (Compute integration, gate disabled): zero
// Target disables the gate; Compute behaves identically to the
// v0.5.11 controller (no DBABudgetRefused, output reflects the blend).
func TestBlend_DBABudget_NoOpWhenZeroTarget(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced, PWMUnitMax: 255})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.Acoustic = AcousticBudget{Target: 0, CurrentDBA: 999, DBAPerPWM: 999}

	r := bc.Compute(in)
	if r.DBABudgetRefused {
		t.Errorf("zero Target should disable the gate; got DBABudgetRefused=true")
	}
	if r.UIState == "refused-dba" {
		t.Errorf("UIState = %q, want non-refused-dba (gate disabled)", r.UIState)
	}
	if r.OutputPWM != in.ReactivePWM {
		// Tick 1: bumpless ⇒ predictive == reactive ⇒ output == reactive.
		t.Errorf("tick 1 OutputPWM = %d, want %d (bumpless)", r.OutputPWM, in.ReactivePWM)
	}
}

// RULE-CTRL-PRESET-04 (Compute integration, ordering): when Path-A
// refuses, the dBA-budget check is short-circuited and DBABudgetRefused
// stays false. Pins the priority chain (path-A > cost > dBA in the
// refusal cascade) so a future re-order doesn't silently widen the
// dBA gate's blast radius.
func TestBlend_DBABudget_PathARefusalShortCircuitsDBA(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced, PWMUnitMax: 255})

	// Path-A: tiny Theta produces predictedDeltaT < 2°C → refuses.
	saturatedM := &marginal.Snapshot{
		Theta:     []float64{0.001, 0},
		WarmingUp: false,
		NSamples:  500,
	}
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.SensorTemp = 80.0
	in.Marginal = saturatedM
	// dBA budget would also refuse under any ΔPWM (CurrentDBA already
	// at Target). But Path-A's earlier check should short-circuit it.
	in.Acoustic = AcousticBudget{Target: 25, CurrentDBA: 25, DBAPerPWM: 1.0}

	r := bc.Compute(in)

	if !r.PathARefused {
		t.Fatalf("expected PathARefused=true (tiny Theta); got %+v", r)
	}
	if r.UIState != "refused-pathA" {
		t.Errorf("UIState = %q, want \"refused-pathA\" (path-A wins)", r.UIState)
	}
	if r.DBABudgetRefused {
		t.Errorf("DBABudgetRefused=true; want false (path-A short-circuits dBA check)")
	}
}

// RULE-CTRL-PRESET-04: EvalDBABudget refuses ramps that push the
// candidate dBA above the configured target; admits ramps that fit
// inside the budget; a zero Target or zero DBAPerPWM disables the gate.
func TestEvalDBABudget_RefusesAboveTarget(t *testing.T) {
	t.Parallel()

	t.Run("admit_when_inside_budget", func(t *testing.T) {
		b := AcousticBudget{Target: 32.0, CurrentDBA: 28.0, DBAPerPWM: 0.1}
		// 28 + 0.1*30 = 31 < 32 → admit.
		refuse, predicted := EvalDBABudget(b, 30)
		if refuse {
			t.Errorf("inside budget: got refuse=true, want false")
		}
		if math.Abs(predicted-31.0) > 1e-9 {
			t.Errorf("predicted = %v, want 31.0", predicted)
		}
	})

	t.Run("refuse_when_above_target", func(t *testing.T) {
		b := AcousticBudget{Target: 32.0, CurrentDBA: 30.0, DBAPerPWM: 0.1}
		// 30 + 0.1*30 = 33 > 32 → refuse.
		refuse, predicted := EvalDBABudget(b, 30)
		if !refuse {
			t.Errorf("above budget: got refuse=false, want true")
		}
		if math.Abs(predicted-33.0) > 1e-9 {
			t.Errorf("predicted = %v, want 33.0", predicted)
		}
	})

	t.Run("at_target_admits", func(t *testing.T) {
		// candidate exactly equal to target — admits (refuse only on strict >)
		b := AcousticBudget{Target: 32.0, CurrentDBA: 30.0, DBAPerPWM: 0.1}
		refuse, _ := EvalDBABudget(b, 20) // 30 + 0.1*20 = 32 == target
		if refuse {
			t.Errorf("at target: got refuse=true, want false (strict >)")
		}
	})

	t.Run("zero_target_disables_gate", func(t *testing.T) {
		b := AcousticBudget{Target: 0, CurrentDBA: 50, DBAPerPWM: 1.0}
		refuse, _ := EvalDBABudget(b, 100)
		if refuse {
			t.Errorf("zero target: got refuse=true, want false (gate disabled)")
		}
	})

	t.Run("negative_target_disables_gate", func(t *testing.T) {
		b := AcousticBudget{Target: -5, CurrentDBA: 50, DBAPerPWM: 1.0}
		refuse, _ := EvalDBABudget(b, 100)
		if refuse {
			t.Errorf("negative target: got refuse=true, want false (gate disabled)")
		}
	})

	t.Run("zero_dba_per_pwm_disables_gate", func(t *testing.T) {
		b := AcousticBudget{Target: 32, CurrentDBA: 50, DBAPerPWM: 0}
		// CurrentDBA already over Target, but no per-PWM impact ⇒ no
		// refuse possible from a ramp.
		refuse, _ := EvalDBABudget(b, 100)
		if refuse {
			t.Errorf("zero DBAPerPWM: got refuse=true, want false (gate has no effect)")
		}
	})

	t.Run("delta_pwm_is_absolute_value", func(t *testing.T) {
		// Negative ΔPWM (cooling-down ramp) shouldn't bypass the gate.
		b := AcousticBudget{Target: 32, CurrentDBA: 30, DBAPerPWM: 0.1}
		refusePos, _ := EvalDBABudget(b, 30)
		refuseNeg, _ := EvalDBABudget(b, -30)
		if refusePos != refuseNeg {
			t.Errorf("|ΔPWM| asymmetry: +30 refused=%v, -30 refused=%v",
				refusePos, refuseNeg)
		}
	})
}

// RULE-CTRL-PI-04 (anti-windup): when the candidate predictive PWM
// would saturate at MaxPWM and the integrator wants to push further
// up (error > 0), the integrator must be frozen on the saturating
// tick. We start with reactive=150 and a huge error so the integrator
// drives predictive up over many ticks; once it hits MaxPWM, the
// freeze trigger fires.
func TestPI_AntiWindup_PWMSaturation(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.SensorTemp = 1000.0 // huge error so the integrator ramps fast
	in.Setpoint = 60.0
	in.MaxPWM = 160 // tight clamp so saturation fires within ~5 ticks
	in.ReactivePWM = 150

	// Run ticks until we observe the freeze trigger. With error=940
	// and Ki·dt≈7.9e-4, each tick adds ~0.74 PWM-units to the
	// integrator's contribution; saturation hits within 20 ticks.
	var sawFreeze bool
	for i := 0; i < 100; i++ {
		in.Now = in.Now.Add(2 * time.Second)
		r := bc.Compute(in)
		if r.IntegratorFrozen {
			if r.OutputPWM != in.MaxPWM {
				t.Fatalf("tick %d frozen: output=%d, want MaxPWM=%d", i, r.OutputPWM, in.MaxPWM)
			}
			sawFreeze = true
			break
		}
	}
	if !sawFreeze {
		t.Fatalf("anti-windup never fired after 100 ticks at extreme error (rampoutshould have saturated)")
	}
}

// Numerical sanity: closed-loop step response on a synthetic plant.
// Drives the integrator long enough to verify the controller IS
// producing positive correction in the right direction (more cooling
// when too hot). Not a RULE-binding — a regression guard against
// future sign-flip mistakes in the IMC-PI math.
func TestPI_StepResponse_DrivesPredictiveAboveReactive(t *testing.T) {
	t.Parallel()
	bc := NewBlended(BlendedConfig{Preset: PresetBalanced})
	in := defaultInputs(healthyCoupling("ch", 0.98, -0.5))
	in.SensorTemp = 70.0
	in.Setpoint = 60.0 // 10°C above ⇒ controller should ramp PWM up

	// Run 100 ticks. The integrator should accumulate positive
	// correction; predictive PWM should rise above reactive
	// monotonically (no oscillation in this open-loop test).
	prevPredictive := uint8(0)
	for i := 0; i < 100; i++ {
		in.Now = in.Now.Add(2 * time.Second)
		r := bc.Compute(in)
		if i > 0 && r.PredictivePWM < prevPredictive {
			t.Fatalf("tick %d: predictive %d dropped from %d (controller should ramp UP)",
				i, r.PredictivePWM, prevPredictive)
		}
		prevPredictive = r.PredictivePWM
	}
	if prevPredictive <= in.ReactivePWM {
		t.Fatalf("after 100 ticks: predictive=%d, want > reactive=%d (controller failed to ramp)",
			prevPredictive, in.ReactivePWM)
	}
}

// PresetFromString round-trip + unknown-fallback smoke test. Pinned
// so PR-A.4's config layer can rely on the parser's contract.
func TestPresetFromString_RoundTripAndFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		want   Preset
		wantOK bool
	}{
		{"silent", PresetSilent, true},
		{"Silent", PresetSilent, true},
		{"SILENT", PresetSilent, true},
		{"balanced", PresetBalanced, true},
		{"", PresetBalanced, true}, // empty == default, no warning
		{"performance", PresetPerformance, true},
		{"chaos", PresetBalanced, false}, // unknown ⇒ Balanced + warning
	}
	for _, tc := range cases {
		got, ok := PresetFromString(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("PresetFromString(%q) = (%d, %v), want (%d, %v)",
				tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
	// String round-trip back to canonical YAML names.
	if PresetSilent.String() != "silent" {
		t.Errorf("PresetSilent.String() = %q", PresetSilent.String())
	}
	if PresetBalanced.String() != "balanced" {
		t.Errorf("PresetBalanced.String() = %q", PresetBalanced.String())
	}
	if PresetPerformance.String() != "performance" {
		t.Errorf("PresetPerformance.String() = %q", PresetPerformance.String())
	}
}
