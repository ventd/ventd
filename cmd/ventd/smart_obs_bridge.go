// Package main — Smart-mode observation bridge.
//
// This file wires the controller's per-tick observation stream to
// the smart-mode estimators (Layer-B coupling and Layer-C marginal).
// Prior to v0.5.37 the bridge was missing: coupling.Shard.Update and
// marginal.Runtime.OnObservation had no production callers, so the
// RLS estimators stayed at θ=[0,0], n_samples=0 forever regardless
// of workload (RFC #1024 root cause; issue #1033 audit finding).
//
// The bridge is layered on top of the existing buildObsAppend
// persistence closure — every controller tick still flows to the
// observation log; the additions are the Layer-B Update + Layer-C
// OnObservation feeds.

package main

import (
	"math"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/coupling"
	"github.com/ventd/ventd/internal/marginal"
	"github.com/ventd/ventd/internal/observation"
)

// channelObsState tracks the rolling per-channel temperature for
// Layer-B's RLS regressor (φ[0]=T_prev) and Layer-C's ΔT input.
// First-tick observations are skipped — no T_prev means no delta to
// compute, so the controller's tick boundary is the natural place
// to register the lifetime baseline.
type channelObsState struct {
	lastTemp float64
	seen     bool
}

// buildSmartObsBridge wraps the buildObsAppend persistence closure
// with the Layer-B + Layer-C data feed. The returned closure is what
// SmartModeBundle.ObsAppend is set to in cmd/ventd/main.go.
//
// Per RULE-CPL-WIRING-04 + RULE-CMB-WIRING-04:
//
//   - On every controller tick after the first per channel, the
//     closure calls coupling.Shard(channelID).Update(now, phi, y) with
//     φ=[T_prev, pwm_now] and y=T_now (v0.5.7 NCoupled=0 reduced-model
//     layout — d_B=2, θ=[a, b_ii]); AND
//     marginal.Runtime.OnObservation({Now, ChannelID, SignatureLabel,
//     PWMWritten, DeltaT=T_now-T_prev, Load=0.0}).
//
//   - The first tick of a channel's lifetime is skipped: there is no
//     T_prev to delta against. The shard is admitted lazily on the
//     second tick (matching RULE-CMB-WIRING-03's "shards are admitted
//     lazily on first observation" semantics — except the
//     "first observation" was previously dead code).
//
//   - When both runtimes are nil the bridge collapses to the legacy
//     buildObsAppend behaviour (persist only). When only one is nil
//     the other still gets fed.
//
// T_now is the maximum sensor reading on each tick — a "system
// temperature" proxy uniform across channels. Layer-B's θ[1] (b_ii
// self-coupling) still differentiates per channel because each
// channel's pwm is independent; θ[0] (the autoregressive coefficient)
// absorbs the shared T-prev contribution and is per-channel because
// each shard has its own θ vector. Per-channel sensor binding
// (curve.temp_sensor → channel) is a v0.6.x refinement once HIL
// evidence confirms the proxy is sufficient.
//
// Load is 0.0 in this first-cut wiring. Layer-C's d=2 form
// (φ=[1, load]) learns θ[0] as the intrinsic ΔT-per-PWM and θ[1]≈0
// when load is constant; saturation predictions stay accurate for
// the load-independent case. v0.6.x plumbs PSI cpu.some avg10 from
// idle.Capture (the same source the soft-idle gate uses).
//
// The closure is mutex-protected per call because controller ticks
// for different channels are serialised in v0.5.x (one PWM write at
// a time), but defensive locking costs nothing and survives any
// future parallelisation.
func buildSmartObsBridge(
	obsWriter *observation.Writer,
	couplingRT *coupling.Runtime,
	marginalRT *marginal.Runtime,
) func(*controller.ObsRecord) {
	persist := buildObsAppend(obsWriter)
	if couplingRT == nil && marginalRT == nil {
		return persist
	}

	state := map[string]*channelObsState{}
	var mu sync.Mutex

	return func(rec *controller.ObsRecord) {
		persist(rec)

		tNow := maxTempReading(rec.SensorReadings)
		if math.IsNaN(tNow) {
			// No usable sensor reading this tick — can't compute a
			// regressor. Skip without burning the per-channel state.
			return
		}
		now := time.UnixMicro(rec.Ts)

		mu.Lock()
		defer mu.Unlock()

		s, ok := state[rec.PWMPath]
		if !ok {
			s = &channelObsState{}
			state[rec.PWMPath] = s
		}

		if s.seen {
			feedCouplingAndMarginal(rec, now, tNow, s.lastTemp,
				couplingRT, marginalRT)
		}
		s.lastTemp = tNow
		s.seen = true
	}
}

// feedCouplingAndMarginal is the inner write step — extracted so the
// per-channel state-management above stays readable and the test
// surface for "what does the bridge emit per tick" is one function.
func feedCouplingAndMarginal(
	rec *controller.ObsRecord,
	now time.Time,
	tNow, tPrev float64,
	couplingRT *coupling.Runtime,
	marginalRT *marginal.Runtime,
) {
	// Layer-B feed (RULE-CPL-WIRING-04). Skip silently when the
	// channel has no registered shard — happens transiently during
	// daemon startup before AddShard completes for every channel.
	if couplingRT != nil {
		if shard := couplingRT.Shard(rec.PWMPath); shard != nil {
			// φ layout for NCoupled=0: [T_prev, pwm_now], d=2.
			_ = shard.Update(now,
				[]float64{tPrev, float64(rec.PWMWritten)},
				tNow)
		}
	}
	// Layer-C feed (RULE-CMB-WIRING-04). marginal.Runtime admits
	// shards lazily; we just emit the observation. Fallback labels
	// (RULE-CMB-LIB-02) are filtered inside OnObservation.
	if marginalRT != nil {
		marginalRT.OnObservation(marginal.ObservationInput{
			Now:            now,
			ChannelID:      rec.PWMPath,
			SignatureLabel: rec.SignatureLabel,
			PWMWritten:     rec.PWMWritten,
			DeltaT:         tNow - tPrev,
			Load:           0.0, // TODO v0.6.x: plumb PSI cpu.some avg10
		})
	}
}

// maxTempReading picks the largest finite plausible temperature
// reading from the controller's sensor map. Returns NaN when the map
// is empty or every value is non-finite so the caller can skip the
// tick gracefully.
//
// "Max" is the v0.6.0 first-cut proxy for per-channel T_self — the
// hottest plausible reading is the dominant thermal driver across
// all channels in practice (CPU package + GPU hotspot typically).
// A future per-channel binding (curve.temp_sensor → channel)
// supersedes this once smart-mode is convergent.
func maxTempReading(readings map[string]float64) float64 {
	if len(readings) == 0 {
		return math.NaN()
	}
	out := math.Inf(-1)
	for _, v := range readings {
		if !math.IsNaN(v) && !math.IsInf(v, 0) && v > out {
			out = v
		}
	}
	if math.IsInf(out, -1) {
		return math.NaN()
	}
	return out
}
