package smartblend

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/confidence/aggregator"
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
	if BuildFn("ch", config.Fan{}, cfgPtr, Deps{}, nil) != nil {
		t.Fatal("expected nil BlendFn with empty Deps")
	}
	la, _ := layer_a.New(layer_a.Config{})
	if BuildFn("ch", config.Fan{}, cfgPtr, Deps{LayerA: la}, nil) != nil {
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

	fn := BuildFn("ch", config.Fan{MinPWM: 0, MaxPWM: 255}, cfgPtr, d, func() string { return "" })
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
