// Smart-mode builders — split from main.go in v0.5.34.
//
// Hosts the v0.5.5–v0.5.9 layer constructors that wire the live
// runtime state through the smart-mode subsystems: opportunistic
// scheduler (Layer A gap-fill), coupling RLS (Layer B), marginal-
// benefit RLS (Layer C), Layer A confidence estimator, confidence
// aggregator, and the blended IMC-PI controller. Plus the small
// captureLoadFraction helper that bridges /proc/loadavg into the
// blend's load input.
//
// All builders return nil when there are no controllable channels
// (monitor-only mode); the daemon never starts the corresponding
// goroutine in that case. RULE-CPL-WIRING-01, RULE-CMB-WIRING-01,
// and the equivalent invariants for the other layers.
//
// Split is mechanical — zero behaviour change. The call sites in
// main.go's run() / runDaemonInternal() still invoke these
// functions directly (same package).
package main

import (
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/confidence/aggregator"
	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/coupling"
	"github.com/ventd/ventd/internal/coupling/signguard"
	"github.com/ventd/ventd/internal/fallback"
	"github.com/ventd/ventd/internal/idle"
	"github.com/ventd/ventd/internal/marginal"
	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/probe/opportunistic"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/sysclass"
)

// daemon's runtime state. Returns nil when there are no controllable
// channels (monitor-only mode); the daemon then never starts the
// scheduler goroutine.
func buildOpportunisticScheduler(
	channels []*probe.ControllableChannel,
	sysDet *sysclass.Detection,
	st *state.State,
	obsWriter *observation.Writer,
	liveCfg *atomic.Pointer[config.Config],
	sguDet *signguard.Detector,
	logger *slog.Logger,
) *opportunistic.Scheduler {
	if len(channels) == 0 {
		logger.Info("opportunistic: no controllable channels; scheduler not started")
		return nil
	}
	if obsWriter == nil {
		logger.Warn("opportunistic: observation writer unavailable; scheduler not started")
		return nil
	}
	tjmax := 100.0
	if sysDet != nil && sysDet.Tjmax > 0 {
		tjmax = sysDet.Tjmax
	}
	cls := sysclass.ClassMidDesktop
	if sysDet != nil {
		cls = sysDet.Class
	}
	rd := observation.NewReader(st.Log)
	det := opportunistic.NewDetector(rd, channels, nil)
	var sguFn opportunistic.SignguardSampleFn
	if sguDet != nil {
		sguFn = func(channelID string, deltaPWMSigned int, deltaT float64) {
			sguDet.Add(signguard.Sample{
				ChannelID: channelID,
				DeltaPWM:  int8(deltaPWMSigned),
				DeltaT:    deltaT,
			})
		}
	}
	depsFor := func(ch *probe.ControllableChannel) opportunistic.ProbeDeps {
		return opportunistic.ProbeDeps{
			Class:             cls,
			Tjmax:             tjmax,
			SensorFn:          opportunistic.SysfsSensorFn(),
			RPMFn:             opportunistic.SysfsRPMFn(ch),
			WriteFn:           opportunistic.SysfsWriteFn(ch),
			ObsAppend:         obsWriter.Append,
			Now:               time.Now,
			Logger:            logger,
			SignguardSampleFn: sguFn,
		}
	}
	disabledFn := func() bool {
		if liveCfg == nil {
			return false
		}
		c := liveCfg.Load()
		if c == nil {
			return false
		}
		return c.NeverActivelyProbeAfterInstall
	}
	manualFn := func(ch *probe.ControllableChannel) bool {
		// In ventd, "manual mode" is signalled per-Control by
		// ManualPWM being non-nil (a fixed duty override). The
		// scheduler refuses any channel where any matching Control
		// pins ManualPWM. Curve-mode channels (ManualPWM == nil)
		// are eligible for opportunistic probing.
		if liveCfg == nil {
			return false
		}
		c := liveCfg.Load()
		if c == nil {
			return false
		}
		for _, ctrl := range c.Controls {
			if ctrl.ManualPWM != nil {
				return true
			}
		}
		return false
	}
	idleCfg := idle.OpportunisticGateConfig{
		GateConfig: idle.GateConfig{
			ProcRoot:      "/proc",
			SysRoot:       "/sys",
			AllowOverride: false,
		},
	}
	cfg := opportunistic.SchedulerConfig{
		Channels:               channels,
		Detector:               det,
		ProbeDeps:              depsFor(channels[0]),
		DepsForChannel:         depsFor,
		IdleCfg:                idleCfg,
		FirstInstallMarkerPath: opportunistic.DefaultFirstInstallMarkerPath,
		Disabled:               disabledFn,
		IsManualMode:           manualFn,
		LastProbeAt:            opportunistic.NewKVLastProbeStore(st.KV),
		Logger:                 logger,
	}
	sched, err := opportunistic.NewScheduler(cfg)
	if err != nil {
		logger.Warn("opportunistic scheduler init failed", "err", err)
		return nil
	}
	logger.Info("opportunistic scheduler initialised", "channels", len(channels), "class", cls)
	return sched
}

// buildCouplingRuntime constructs the v0.5.7 Layer-B thermal coupling
// estimator runtime with one shard per controllable channel. Returns
// nil when there are no controllable channels (monitor-only mode);
// the daemon then never starts the coupling goroutine.
//
// N_coupled is fixed at 0 for v0.5.7 — the well-posed reduced-model
// case (R9 §U4) where each shard learns its own thermal time constant
// `a` and load coefficient `c`. v0.5.8 raises N_coupled to enable
// cross-fan b_ij learning once Layer C is in.
//
// hwmonFingerprint invalidates persisted shards on hardware change
// per RULE-CPL-PERSIST-01. dmiFingerprint is host-stable and
// changes when the motherboard changes — sufficient for v0.5.7;
// v0.5.10 doctor refines.
//
// RULE-CPL-WIRING-01: Runtime is constructed only when len(channels) > 0.
// RULE-CPL-WIRING-02: Exactly one shard per controllable channel is registered.
func buildCouplingRuntime(
	channels []*probe.ControllableChannel,
	st *state.State,
	hwmonFingerprint string,
	logger *slog.Logger,
) *coupling.Runtime {
	if len(channels) == 0 {
		logger.Info("coupling: no controllable channels; runtime not started")
		return nil
	}
	rt := coupling.NewRuntime(st.Dir, hwmonFingerprint, logger)
	for _, ch := range channels {
		shard, err := coupling.New(coupling.DefaultConfig(ch.PWMPath, 0))
		if err != nil {
			logger.Warn("coupling: shard init failed",
				"channel", ch.PWMPath, "err", err)
			continue
		}
		if addErr := rt.AddShard(shard); addErr != nil {
			logger.Warn("coupling: AddShard failed",
				"channel", ch.PWMPath, "err", addErr)
			continue
		}
	}
	logger.Info("coupling: runtime initialised",
		"channels", len(channels),
		"hwmon_fp", hwmonFingerprint)
	return rt
}

// runEnvelopeBackground waits for the idle gate then runs the Envelope C/D
// probe sequentially across all controllable channels (RULE-ENVELOPE-11).
// Called from run() as a goroutine; ctx is cancelled when run() returns.
// buildMarginalRuntime constructs the v0.5.8 Layer-C
// marginal-benefit estimator runtime. Returns nil when there are
// no controllable channels (monitor-only mode); the daemon then
// never starts the goroutine.
//
// Per spec-v0_5_8 §6.2: wiring-only. Shards are admitted lazily
// on the first OnObservation call carrying a non-fallback
// signature; v0.5.9 wires the actual feed (sensor readings live
// in the controller, not in the controller→obsWriter Record).
//
// hwmonFingerprint matches v0.5.7's choice — dmiFingerprint —
// for v0.5.8; v0.5.10 doctor refines.
//
// RULE-CMB-WIRING-01: Returns nil when len(channels) == 0.
// RULE-CMB-WIRING-03: Caller starts Run exactly once.
func buildMarginalRuntime(
	channels []*probe.ControllableChannel,
	st *state.State,
	hwmonFingerprint string,
	sguDet *signguard.Detector,
	logger *slog.Logger,
) *marginal.Runtime {
	if len(channels) == 0 {
		logger.Info("marginal: no controllable channels; runtime not started")
		return nil
	}
	// sguDet is passed as the SignguardLookup; nil falls back to
	// noSignguard{} (always-false). Layer-B parents stay nil for now
	// — that wiring gap is tracked separately.
	var sgu marginal.SignguardLookup
	if sguDet != nil {
		sgu = sguDet
	}
	rt := marginal.NewRuntime(st.Dir, hwmonFingerprint, nil, sgu, logger)
	logger.Info("marginal: runtime initialised",
		"channels", len(channels),
		"hwmon_fp", hwmonFingerprint,
		"signguard_wired", sgu != nil)
	return rt
}

// buildLayerAEstimator constructs the v0.5.9 conf_A estimator with one
// channel admitted per controllable PWM. Tier is selected via
// fallback.SelectTier — Tier 0 (tach present), Tier 4 (laptop EC), or
// Tier 7 (open-loop refusal). Per spec-v0_5_9 §2.4 + RULE-CONFA-FORMULA-01.
//
// Returns nil when there are no controllable channels (monitor-only).
func buildLayerAEstimator(
	channels []*probe.ControllableChannel,
	logger *slog.Logger,
) *layer_a.Estimator {
	if len(channels) == 0 {
		logger.Info("layer_a: no controllable channels; estimator not constructed")
		return nil
	}
	est, err := layer_a.New(layer_a.Config{})
	if err != nil {
		logger.Warn("layer_a: New failed", "err", err)
		return nil
	}
	now := time.Now()
	for _, ch := range channels {
		tier := fallback.SelectTier(ch)
		if err := est.Admit(ch.PWMPath, tier, layer_a.DefaultNoiseFloor, now); err != nil {
			logger.Warn("layer_a: Admit failed",
				"channel", ch.PWMPath, "tier", tier, "err", err)
			continue
		}
	}
	logger.Info("layer_a: estimator initialised", "channels", len(channels))
	return est
}

// buildAggregator constructs the v0.5.9 R12-locked confidence
// aggregator. Per-channel state is admitted lazily on first Tick;
// no per-channel pre-warm here.
//
// Returns nil when there are no controllable channels.
func buildAggregator(
	channels []*probe.ControllableChannel,
	logger *slog.Logger,
) *aggregator.Aggregator {
	if len(channels) == 0 {
		logger.Info("aggregator: no controllable channels; not constructed")
		return nil
	}
	a := aggregator.New(aggregator.Config{})
	logger.Info("aggregator: initialised", "channels", len(channels))
	return a
}

// buildBlendedController constructs the v0.5.9 IMC-PI blended
// controller. The Preset enum is resolved from Config.Smart.Preset
// at construction; SIGHUP-driven preset changes require restart for
// the gain cache to refresh (acceptable per spec §3.7 — gains are
// cached for 60-NSamples cycles already).
//
// Returns nil when there are no controllable channels.
func buildBlendedController(
	channels []*probe.ControllableChannel,
	cfg *config.Config,
	logger *slog.Logger,
) *controller.BlendedController {
	if len(channels) == 0 {
		logger.Info("blended: no controllable channels; not constructed")
		return nil
	}
	preset, ok := controller.PresetFromString(cfg.Smart.Preset)
	if !ok {
		logger.Warn("blended: unknown smart.preset; falling back to balanced",
			"got", cfg.Smart.Preset)
	}
	bc := controller.NewBlended(controller.BlendedConfig{
		Preset:     preset,
		PWMUnitMax: 255,
	})
	logger.Info("blended: controller initialised",
		"channels", len(channels), "preset", preset.String())
	return bc
}

// buildBlendFn returns a controller.BlendFn closure that bridges
// the per-controller hot path to the v0.5.9 BlendedController. It
// pulls Layer-B / Layer-C Snapshots, computes conf_A from the
// LayerA estimator, asks the aggregator for w_pred, and routes
// through BlendedController.Compute. PWM bounds come from the live
// fan config so SIGHUP reloads track operator changes.
//
// Returns nil when the smart bundle isn't fully populated (any of
// LayerA / Aggregator / Blended unset) — caller-side check; the
// produced closure assumes non-nil bundle pointers.
//
// The closure is cheap on the hot path: each Snapshot read is a
// single atomic.Pointer load; the aggregator + BlendedController
// each take a per-channel mutex briefly. Total per-call work fits
// in <100µs at d=2 IMC-PI math, well under the 2s controller tick.
func buildBlendFn(
	channelID string,
	fanCfg config.Fan,
	liveCfg *atomic.Pointer[config.Config],
	smart *SmartModeBundle,
	labelFn func() string,
	logger *slog.Logger,
) controller.BlendFn {
	if smart == nil || smart.LayerA == nil || smart.Aggregator == nil || smart.Blended == nil {
		return nil
	}
	// We capture the channel ID and fan config snapshot once. PWM
	// bounds and Setpoint are re-read on every call from the live
	// Config so SIGHUP-driven changes propagate immediately.
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
		if smart.Coupling != nil {
			if shard := smart.Coupling.Shard(chID); shard != nil {
				couplingSnap = shard.Read()
			}
		}
		var marginalSnap *marginal.Snapshot
		var sigLabel string
		if smart.Marginal != nil && labelFn != nil {
			sigLabel = labelFn()
			if shard := smart.Marginal.Shard(chID, sigLabel); shard != nil {
				marginalSnap = shard.Read()
			}
		}
		layerASnap := smart.LayerA.Read(chID)

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
		// LPF rides w_pred down at L_max).
		var confC float64
		if marginalSnap != nil && !marginalSnap.WarmingUp {
			cc := marginalSnap.Confidence
			if cc.SaturationAdmit {
				confC = cc.ResidualTerm * cc.CovarianceTerm * cc.SampleCountTerm
			}
		}

		// v0.5.9 ships drift detection as RFCV (R16) future work;
		// stub at false. Global gate is also a stub — true when the
		// daemon is up + has a config + has the wizard outcome
		// "control_mode". This is a coarse approximation of the
		// spec §2.5 4-term AND-gate; doctor-surface refinements
		// land in v0.5.10.
		driftFlags := [3]bool{}
		wPredSystem := true

		aggSnap := smart.Aggregator.Tick(chID, confA, confB, confC,
			driftFlags, wPredSystem, now)
		if aggSnap == nil {
			return reactivePWM
		}

		// Resolve the system load fraction once — same proxy that
		// drives Path-A re-derive at the controller call site.
		// Cheap: idle.CaptureLoadAvg reads /proc/loadavg.
		loadFrac := captureLoadFraction()

		res := smart.Blended.Compute(controller.BlendedInputs{
			ChannelID:    chID,
			SensorTemp:   sensorTemp,
			Setpoint:     setpoint,
			ReactivePWM:  reactivePWM,
			WPred:        aggSnap.Wpred,
			Coupling:     couplingSnap,
			Marginal:     marginalSnap,
			LayerA:       layerASnap,
			LoadFraction: loadFrac,
			DT:           dt,
			Now:          now,
			MinPWM:       fanCfg.MinPWM,
			MaxPWM:       fanCfg.MaxPWM,
		})

		// First-contact mark: persisted only after the clamped tick
		// succeeds — same lifecycle as RULE-CTRL-BLEND-02.
		if res.FirstContactClamp || (res.WPred > 0 && layerASnap != nil && !layerASnap.SeenFirstContact) {
			smart.LayerA.MarkFirstContact(chID, now)
		}
		// Cache for the web /smart/channels handler so the dashboard
		// can show the controller's actual next-tick PWM target +
		// refusal flags alongside Layer-C's MarginalSlope. Atomic
		// pointer-swap; nil-safe when smart.Decisions is unwired.
		smart.Decisions.Store(chID, controller.Decision{
			Result:      res,
			ReactivePWM: reactivePWM,
		})
		return res.OutputPWM
	}
}

// captureLoadFraction returns the system's normalised load over
// the last minute, in [0, 1]. Reuses idle.CaptureLoadAvg +
// LoadAvgPerCPU so the v0.5.9 controller and the existing R5 idle
// gate share a single /proc/loadavg parser. Stub for v0.5.9 —
// v0.6.x can substitute PSI-based fractions for laptop / NAS
// classes where loadavg is noisy.
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
