// Package drift implements R16 per-(channel, layer) drift detection for
// the v0.5.9 confidence controller. It feeds the `driftFlags [3]bool` the
// aggregator already consumes (`aggregator.Tick`): a flagged layer's
// confidence decays by 0.5^(t/60s) before the min-collapse, so a drifting
// layer rides w_pred down and reactive control takes over while the RLS
// re-learns.
//
// The method is the residual EWMA control chart (Ross, Adams, Tasoulis &
// Hand, "Exponentially weighted moving average charts for detecting
// concept drift", Pattern Recognition Letters 2012 — arXiv:1212.6018):
// each layer's prediction residual is monitored by an EWMA and a drift is
// declared when it crosses an adaptive control limit μ + L·σ built from
// the layer's own healthy baseline. See chart.go for the math.
//
// EWMA control charts lag on ABRUPT change — which is by design here: the
// PR-1 w_pred_system gate (internal/confidence/gate) handles abrupt system
// failures (mass-stall, battery, scrub) instantly, so this detector's job
// is GRADUAL per-layer model drift: a re-cabled fan, dust load, an ambient
// regime shift, an operator curve edit. The two mechanisms divide the work
// cleanly.
//
// The detector is snapshot-type-agnostic: it takes scalar Inputs (residual
// magnitude + convergence) per layer, exactly as the aggregator takes
// scalar conf_A/B/C, so it imports none of the layer packages and the math
// stays pure. The caller (the blend hook) maps each layer's Snapshot into
// Inputs. State is per-channel behind a mutex (mirroring
// aggregator.Aggregator); the web surface reads it lock-free via an
// atomic.Pointer. There is no Reset/Wipe — like the coupling / marginal /
// layer_a runtimes, in-memory drift state lives for the daemon-process
// lifetime and resets only on a true process restart (RULE-DRIFT-RESTART-01).
package drift

import (
	"math"
	"sync"
	"sync/atomic"
)

// Layer indices, matching aggregator.LayerA/LayerB/LayerC.
const (
	LayerA = 0
	LayerB = 1
	LayerC = 2
)

// Config holds the EWMA-control-chart thresholds. All fields are
// HIL-tunable; use DefaultConfig for the conservative shipped values.
type Config struct {
	// EWMAAlpha smooths the monitored sqrt-residual (the charted z).
	EWMAAlpha float64
	// BaselineAlpha is the slow EWMA rate for the healthy baseline μ.
	BaselineAlpha float64
	// DispersionAlpha is the EWMA rate for the dispersion (σ proxy).
	DispersionAlpha float64
	// L is the trip control-limit multiplier (μ + L·σ).
	L float64
	// LClear is the clear control-limit multiplier (< L → hysteresis).
	LClear float64
	// TripDwell is the consecutive over-limit observations before flagging.
	TripDwell int
	// ClearDwell is the consecutive under-clear-limit observations before clearing.
	ClearDwell int
	// MinSigma floors the dispersion so a near-constant residual cannot
	// make any micro-wobble trip — the key conservatism knob.
	MinSigma float64
	// WarmupTicks is the post-convergence observation count before the
	// baseline is trusted enough to permit a trip.
	WarmupTicks int
}

// DefaultConfig returns the shipped, deliberately conservative thresholds.
// A false trip only loses predictive benefit (reactive is the safe
// fallback) and a missed drift is bounded by the aggregator's clamp +
// Lipschitz cap, so the defaults favour few false positives.
func DefaultConfig() Config {
	return Config{
		EWMAAlpha:       0.2,
		BaselineAlpha:   0.02,
		DispersionAlpha: 0.02,
		L:               3.0,
		LClear:          1.5,
		TripDwell:       3,
		ClearDwell:      5,
		MinSigma:        0.05,
		WarmupTicks:     30,
	}
}

// Inputs is one layer's per-tick signal. Residual is the layer's residual
// MAGNITUDE (e.g. EWMAResidual = EWMA of e², or RMSResidual²); the chart
// monitors its square root. Converged gates whether the layer may be
// judged for drift. Valid is false when the layer's Snapshot was absent
// this tick (the detector then holds prior state and never flags).
type Inputs struct {
	Residual  float64
	Converged bool
	Valid     bool
}

// Evidence is the per-layer read view for the API / doctor surface. The
// numeric fields are meaningful only when Converged is true; the surface
// renders "—" otherwise (no theatre).
type Evidence struct {
	Drifting     bool    `json:"drifting"`
	Converged    bool    `json:"converged"`
	Residual     float64 `json:"residual"`      // current sqrt-residual monitor (z)
	Baseline     float64 `json:"baseline"`      // μ
	ControlLimit float64 `json:"control_limit"` // μ + L·σ (the trip threshold)
	Sigma        float64 `json:"sigma"`         // dispersion after the MinSigma floor
}

// Detector owns per-channel drift state. Observe is the hot path (called
// inline from the blend closure, once per channel per tick); Snapshot is
// the lock-free read for the web surface.
type Detector struct {
	cfg    Config
	mu     sync.Mutex
	states map[string]*channelState
}

type channelState struct {
	layers [3]chartState
	snap   atomic.Pointer[[3]Evidence]
}

// New constructs a Detector with the given config.
func New(cfg Config) *Detector {
	return &Detector{cfg: cfg, states: make(map[string]*channelState)}
}

// Observe folds this tick's three layer inputs into the channel's state
// and returns the resulting driftFlags, which the caller passes straight
// into aggregator.Tick. Thread-safe: distinct fan goroutines touch
// distinct channelID keys, but the state map is mutex-guarded (mirrors
// aggregator.Aggregator). The per-layer Evidence is published via an
// atomic.Pointer for the lock-free Snapshot read.
func (d *Detector) Observe(channelID string, in [3]Inputs) [3]bool {
	d.mu.Lock()
	cs, ok := d.states[channelID]
	if !ok {
		cs = &channelState{}
		d.states[channelID] = cs
	}

	var flags [3]bool
	var ev [3]Evidence
	for i := 0; i < 3; i++ {
		if in[i].Valid {
			r := 0.0
			if in[i].Residual > 0 {
				r = math.Sqrt(in[i].Residual)
			}
			cs.layers[i] = step(cs.layers[i], r, in[i].Converged, d.cfg)
		}
		st := cs.layers[i]
		flags[i] = st.flagged
		ev[i] = Evidence{
			Drifting:     st.flagged,
			Converged:    in[i].Valid && in[i].Converged && st.inited,
			Residual:     st.z,
			Baseline:     st.baseline,
			ControlLimit: st.controlLimit(d.cfg),
			Sigma:        st.sigmaFloored(d.cfg),
		}
	}
	evCopy := ev
	cs.snap.Store(&evCopy)
	d.mu.Unlock()
	return flags
}

// Snapshot returns the per-layer Evidence for channelID, lock-free.
// Returns a zero array when the channel has never been observed.
func (d *Detector) Snapshot(channelID string) [3]Evidence {
	d.mu.Lock()
	cs, ok := d.states[channelID]
	d.mu.Unlock()
	if !ok {
		return [3]Evidence{}
	}
	if p := cs.snap.Load(); p != nil {
		return *p
	}
	return [3]Evidence{}
}
