package smartblend

import (
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/acoustic/budget"
	"github.com/ventd/ventd/internal/confidence/aggregator"
	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/coupling"
	"github.com/ventd/ventd/internal/idle"
	"github.com/ventd/ventd/internal/marginal"
)

// Deps are the smart-mode runtimes the blend closure reads each tick.
// The daemon composition root (cmd/ventd) builds these as part of its
// SmartModeBundle and hands the blend-relevant subset here — the bundle
// itself also carries composition concerns (signature library, obs
// append, polarity channels) that the blend hook doesn't touch.
type Deps struct {
	Coupling   *coupling.Runtime
	Marginal   *marginal.Runtime
	LayerA     *layer_a.Estimator
	Aggregator *aggregator.Aggregator
	Blended    *controller.BlendedController
	Decisions  *controller.DecisionCache
}

// BuildFn returns a controller.BlendFn closure that bridges the smart-
// mode runtimes into the controller hot path: per tick it pulls the
// per-channel Layer-A/B/C snapshots, collapses them to conf_A/B/C, runs
// the aggregator for w_pred, builds the dBA-budget input, and calls
// BlendedController.Compute — caching the result for the web surface.
//
// Returns nil (reactive-only) when a required runtime is absent; the
// controller then runs without a blend hook. The channel ID and fan
// config are captured once; PWM bounds and the setpoint are re-read
// from the live Config every call so SIGHUP changes propagate.
func BuildFn(
	channelID string,
	fanCfg config.Fan,
	liveCfg *atomic.Pointer[config.Config],
	d Deps,
	labelFn func() string,
) controller.BlendFn {
	if d.LayerA == nil || d.Aggregator == nil || d.Blended == nil {
		return nil
	}
	return func(chID string, sensorTemp float64, reactivePWM uint8, dt time.Duration, now time.Time) uint8 {
		live := liveCfg.Load()
		if live == nil {
			return reactivePWM
		}
		setpoint, ok := live.Smart.Setpoints[chID]
		if !ok {
			// Fallback: setpoint matches the bound sensor's normal
			// operating range. Per spec §3.7's silence on absence,
			// we use a class-default of 70°C — a conservative
			// midpoint for desktop CPU/GPU temperatures. v0.6.x
			// can refine this from the system class.
			setpoint = 70.0
		}

		var couplingSnap *coupling.Snapshot
		if d.Coupling != nil {
			if shard := d.Coupling.Shard(chID); shard != nil {
				couplingSnap = shard.Read()
			}
		}
		var marginalSnap *marginal.Snapshot
		var sigLabel string
		if d.Marginal != nil && labelFn != nil {
			sigLabel = labelFn()
			if shard := d.Marginal.Shard(chID, sigLabel); shard != nil {
				marginalSnap = shard.Read()
			}
		}
		layerASnap := d.LayerA.Read(chID)

		// conf_A from Layer-A's atomic snapshot.
		var confA float64
		if layerASnap != nil {
			confA = layerASnap.ConfA
		}
		// conf_B from Layer-B's R12 §Q1 four-term product.
		confB := couplingSnap.Confidence()
		// conf_C from Layer-C's per-(channel, signature) product.
		// Returns 0 when the active signature has no warmed shard
		// (R12 §Q6 active-signature collapse — accept the drop, the
		// LPF rides w_pred down at L_max). RULE-AGG-SIG-COLLAPSE-01.
		confC := aggregator.ConfCFromMarginal(marginalSnap)

		// v0.5.9 ships drift detection as RFCV (R16) future work;
		// stub at false. Global gate is also a stub — true when the
		// daemon is up + has a config + has the wizard outcome
		// "control_mode". This is a coarse approximation of the
		// spec §2.5 4-term AND-gate; doctor-surface refinements
		// land in v0.5.10.
		driftFlags := [3]bool{}
		wPredSystem := true

		aggSnap := d.Aggregator.Tick(chID, confA, confB, confC,
			driftFlags, wPredSystem, now)
		if aggSnap == nil {
			return reactivePWM
		}

		// Resolve the system load fraction once — same proxy that
		// drives Path-A re-derive at the controller call site.
		// Cheap: idle.CaptureLoadAvg reads /proc/loadavg.
		loadFrac := captureLoadFraction()

		// Build the dBA-budget input bundle. When the operator has
		// AcousticOptimisation enabled (default), this reads each
		// fan's current RPM via sysfs, composes them energetically via
		// the R33 acoustic proxy, and computes the candidate channel's
		// marginal dBA-per-PWM. The BlendedController gates on this so
		// preset=Silent caps host loudness at 25 dBA, Balanced at 32
		// (#1273). A zero Acoustic value disables the gate; the gate
		// is also disabled when AcousticOptimisation=false in config.
		var acoustic controller.AcousticBudget
		if live.AcousticOptimisationEnabled() {
			acoustic = budget.Build(live, chID, d.Blended.Preset())
		}

		res := d.Blended.Compute(controller.BlendedInputs{
			ChannelID:    chID,
			SensorTemp:   sensorTemp,
			Setpoint:     setpoint,
			ReactivePWM:  reactivePWM,
			WPred:        aggSnap.Wpred,
			Coupling:     couplingSnap,
			Marginal:     marginalSnap,
			LayerA:       layerASnap,
			LoadFraction: loadFrac,
			Acoustic:     acoustic,
			DT:           dt,
			Now:          now,
			MinPWM:       fanCfg.MinPWM,
			MaxPWM:       fanCfg.MaxPWM,
		})

		// First-contact mark: persisted only after the clamped tick
		// succeeds — same lifecycle as RULE-CTRL-BLEND-02.
		if res.FirstContactClamp || (res.WPred > 0 && layerASnap != nil && !layerASnap.SeenFirstContact) {
			d.LayerA.MarkFirstContact(chID, now)
		}
		// Cache for the web /smart/channels handler so the dashboard
		// can show the controller's actual next-tick PWM target +
		// refusal flags alongside Layer-C's MarginalSlope. Atomic
		// pointer-swap; nil-safe when Decisions is unwired.
		d.Decisions.Store(chID, controller.Decision{
			Result:      res,
			ReactivePWM: reactivePWM,
		})
		return res.OutputPWM
	}
}

// captureLoadFraction returns the system's normalised load over the
// last minute, in [0, 1]. Reuses idle.CaptureLoadAvg + LoadAvgPerCPU so
// the controller and the R5 idle gate share a single /proc/loadavg
// parser. Stub for v0.5.9 — v0.6.x can substitute PSI-based fractions
// for laptop / NAS classes where loadavg is noisy.
func captureLoadFraction() float64 {
	la := idle.CaptureLoadAvg("/proc")
	frac := idle.LoadAvgPerCPU(la)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return frac
}
