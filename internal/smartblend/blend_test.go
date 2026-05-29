package smartblend

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/confidence/aggregator"
	"github.com/ventd/ventd/internal/confidence/drift"
	"github.com/ventd/ventd/internal/confidence/gate"
	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
)

// TestBuildFn_NilDepsReturnsNil: a missing required runtime yields a nil
// BlendFn so the controller runs reactive-only (matches the inline guard
// the wiring had in package main).
func TestBuildFn_NilDepsReturnsNil(t *testing.T) {
	t.Parallel()
	cfgPtr := &atomic.Pointer[config.Config]{}
	if BuildFn("ch", config.Fan{}, cfgPtr, Deps{}, nil, 0, false) != nil {
		t.Fatal("expected nil BlendFn with empty Deps")
	}
	la, _ := layer_a.New(layer_a.Config{})
	if BuildFn("ch", config.Fan{}, cfgPtr, Deps{LayerA: la}, nil, 0, false) != nil {
		t.Fatal("expected nil BlendFn without Aggregator/Blended")
	}
}

// TestBuildFn_ColdStartReturnsReactive wires all required runtimes but
// leaves the coupling/marginal shards cold: w_pred is hard-pinned to 0,
// so the blend passes the reactive PWM through unchanged and caches the
// decision for the web surface. Exercises the full closure end to end
// (aggregator Tick, dBA-budget build, BlendedController Compute,
// decision store) — the path the R6c move had to preserve.
func TestBuildFn_ColdStartReturnsReactive(t *testing.T) {
	t.Parallel()
	la, err := layer_a.New(layer_a.Config{})
	if err != nil {
		t.Fatal(err)
	}
	dec := controller.NewDecisionCache()
	d := Deps{
		LayerA:     la,
		Aggregator: aggregator.New(aggregator.Config{}),
		Blended:    controller.NewBlended(controller.BlendedConfig{}),
		Decisions:  dec,
	}
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(&config.Config{
		Smart: config.SmartConfig{Setpoints: map[string]float64{"ch": 60}},
	})

	fn := BuildFn("ch", config.Fan{MinPWM: 0, MaxPWM: 255}, cfgPtr, d, func() string { return "" }, 0, false)
	if fn == nil {
		t.Fatal("expected non-nil BlendFn when LayerA/Aggregator/Blended are wired")
	}

	const reactive uint8 = 150
	got := fn("ch", 70, reactive, 2*time.Second, time.Now())
	if got != reactive {
		t.Errorf("cold-start blend output = %d, want reactive %d (w_pred pinned to 0 with no warmed shards)", got, reactive)
	}
	if _, ok := dec.Load("ch"); !ok {
		t.Error("expected a decision cached for ch after a blend tick")
	}
}

// TestBlend_GateClosedForcesReactive binds RULE-GATE-WIRING-01: the blend
// hook reads d.Gate.Open() as the aggregator's wPredSystem argument. A
// closed gate drives UIState "refused" and returns the reactive PWM; a
// nil gate is treated as open (not refused), preserving pre-gate
// behaviour.
func TestBlend_GateClosedForcesReactive(t *testing.T) {
	t.Parallel()
	newDeps := func(g *gate.Evaluator) (Deps, *aggregator.Aggregator) {
		agg := aggregator.New(aggregator.Config{})
		la, err := layer_a.New(layer_a.Config{})
		if err != nil {
			t.Fatal(err)
		}
		return Deps{
			LayerA:     la,
			Aggregator: agg,
			Blended:    controller.NewBlended(controller.BlendedConfig{}),
			Decisions:  controller.NewDecisionCache(),
			Gate:       g,
		}, agg
	}
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(&config.Config{
		Smart: config.SmartConfig{Setpoints: map[string]float64{"ch": 60}},
	})
	const reactive uint8 = 150

	// Closed gate → wPredSystem=false → aggregator forces w_pred=0,
	// UIState "refused", and the blend returns the reactive PWM.
	closed := gate.New(gate.Deps{SmartDisabled: func() bool { return true }})
	closed.Evaluate() // ReasonSmartDisabled ⇒ Open()==false
	if closed.Open() {
		t.Fatal("precondition: gate must be closed")
	}
	d, agg := newDeps(closed)
	fn := BuildFn("ch", config.Fan{MinPWM: 0, MaxPWM: 255}, cfgPtr, d, func() string { return "" }, 0, false)
	if got := fn("ch", 70, reactive, 2*time.Second, time.Now()); got != reactive {
		t.Errorf("closed-gate blend = %d, want reactive %d", got, reactive)
	}
	if snap := agg.Read("ch"); snap == nil || snap.UIState != aggregator.UIStateRefused {
		t.Fatalf("closed gate must drive aggregator UIState=refused; got %+v", snap)
	}

	// nil gate → treated as open → wPredSystem=true → not refused.
	dNil, aggNil := newDeps(nil)
	fnNil := BuildFn("ch", config.Fan{MinPWM: 0, MaxPWM: 255}, cfgPtr, dNil, func() string { return "" }, 0, false)
	fnNil("ch", 70, reactive, 2*time.Second, time.Now())
	if snap := aggNil.Read("ch"); snap == nil || snap.UIState == aggregator.UIStateRefused {
		t.Fatalf("nil gate must be open (not refused); got %+v", snap)
	}
}

// TestBlend_DriftFlagsFromDetectorIntoTick binds RULE-DRIFT-AGG-WIRING-01:
// the blend closure passes the drift detector's per-layer flags into
// aggregator.Tick (never SetDrift), so a flagged layer surfaces as
// DriftFlags + UIState "drifting".
func TestBlend_DriftFlagsFromDetectorIntoTick(t *testing.T) {
	det := drift.New(drift.DefaultConfig())
	c := drift.DefaultConfig()

	// Pre-flag Layer B for "ch": warm a steady baseline, then a step-up.
	preObs := func(residual float64) [3]bool {
		var in [3]drift.Inputs
		in[drift.LayerB] = drift.Inputs{Residual: residual, Converged: true, Valid: true}
		return det.Observe("ch", in)
	}
	for i := 0; i < c.WarmupTicks+10; i++ {
		preObs(1.0)
	}
	var flags [3]bool
	for i := 0; i < 60; i++ {
		if flags = preObs(100.0); flags[drift.LayerB] {
			break
		}
	}
	if !flags[drift.LayerB] {
		t.Fatal("precondition: detector should flag Layer B drift")
	}

	// Build the closure with NO coupling/marginal runtimes, so the
	// closure's Layer-B/C inputs are Invalid and the detector HOLDS the
	// pre-set Layer-B flag rather than resetting it. The held flag must
	// flow through Observe into aggregator.Tick.
	la, err := layer_a.New(layer_a.Config{})
	if err != nil {
		t.Fatal(err)
	}
	agg := aggregator.New(aggregator.Config{})
	d := Deps{
		LayerA:     la,
		Aggregator: agg,
		Blended:    controller.NewBlended(controller.BlendedConfig{}),
		Decisions:  controller.NewDecisionCache(),
		Drift:      det,
	}
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(&config.Config{Smart: config.SmartConfig{Setpoints: map[string]float64{"ch": 60}}})
	fn := BuildFn("ch", config.Fan{MinPWM: 0, MaxPWM: 255}, cfgPtr, d, func() string { return "" }, 0, false)
	fn("ch", 70, 150, 2*time.Second, time.Now())

	snap := agg.Read("ch")
	if snap == nil || !snap.DriftFlags[drift.LayerB] {
		t.Fatalf("detector's Layer-B drift flag must reach aggregator.Tick; got %+v", snap)
	}
	if snap.UIState != aggregator.UIStateDrifting {
		t.Fatalf("a set drift flag must collapse to UIState drifting; got %q", snap.UIState)
	}
}

// TestBuildFn_NoSetpointNoDerived_ReactiveOnly binds RULE-CTRL-SMART-RELAX-FLOOR:
// when neither a configured nor a derivable setpoint exists, the channel has no
// reference signal for the IMC-PI, so the closure runs reactive-only — it
// returns the reactive PWM and caches no predictive decision, instead of the
// old silent 70°C predictive path.
func TestBuildFn_NoSetpointNoDerived_ReactiveOnly(t *testing.T) {
	t.Parallel()
	la, err := layer_a.New(layer_a.Config{})
	if err != nil {
		t.Fatal(err)
	}
	dec := controller.NewDecisionCache()
	d := Deps{
		LayerA:     la,
		Aggregator: aggregator.New(aggregator.Config{}),
		Blended:    controller.NewBlended(controller.BlendedConfig{}),
		Decisions:  dec,
	}
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(&config.Config{Smart: config.SmartConfig{}}) // no setpoints

	// derivedOK == false ⇒ no thermal limit resolved for the bound sensor.
	fn := BuildFn("ch", config.Fan{MinPWM: 0, MaxPWM: 255}, cfgPtr, d, func() string { return "" }, 0, false)
	const reactive uint8 = 150
	if got := fn("ch", 70, reactive, 2*time.Second, time.Now()); got != reactive {
		t.Errorf("unresolved setpoint: output = %d, want reactive %d", got, reactive)
	}
	if _, ok := dec.Load("ch"); ok {
		t.Error("reactive-only path must not cache a predictive decision")
	}
}

// TestBuildFn_DerivedSetpointUsed: with no configured setpoint but a derived
// one available (derivedOK), the closure proceeds through the predictive path
// (a decision is cached) rather than running reactive-only.
func TestBuildFn_DerivedSetpointUsed(t *testing.T) {
	t.Parallel()
	la, err := layer_a.New(layer_a.Config{})
	if err != nil {
		t.Fatal(err)
	}
	dec := controller.NewDecisionCache()
	d := Deps{
		LayerA:     la,
		Aggregator: aggregator.New(aggregator.Config{}),
		Blended:    controller.NewBlended(controller.BlendedConfig{}),
		Decisions:  dec,
	}
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(&config.Config{Smart: config.SmartConfig{}}) // no setpoints

	fn := BuildFn("ch", config.Fan{MinPWM: 0, MaxPWM: 255}, cfgPtr, d, func() string { return "" }, 65.0, true)
	fn("ch", 70, 150, 2*time.Second, time.Now())
	if _, ok := dec.Load("ch"); !ok {
		t.Error("a derived setpoint should let the closure proceed and cache a decision")
	}
}
