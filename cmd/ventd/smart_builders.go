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
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/acoustic/proxy"
	acrunner "github.com/ventd/ventd/internal/acoustic/runner"
	"github.com/ventd/ventd/internal/confidence/aggregator"
	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/coupling"
	"github.com/ventd/ventd/internal/coupling/signguard"
	"github.com/ventd/ventd/internal/fallback"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/idle"
	"github.com/ventd/ventd/internal/marginal"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/platformprofile"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/probe/opportunistic"
	"github.com/ventd/ventd/internal/signature"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/sysclass"
)

// loadSignatureState restores the persisted signature library from
// KV at daemon start. It is the helper extracted from
// runDaemonInternal (audit pass-3 #1075) so the rule-binding test
// for RULE-SIG-WIRING-01 exercises the same code path production
// runs — not a replayed LoadManifest + LoadLabels sequence in
// isolation. A regression that drops the call site has to delete a
// named-method reference, which is much harder to do by accident
// than removing an inline block.
//
// RULE-SIG-WIRING-01 + RULE-SIG-PERSIST-02: read the persisted
// manifest, then re-hydrate every bucket's HitCount /
// LastSeenUnix / CurrentEWMA so a daemon restart preserves the
// operator-visible workload history (issue #1035 row 11).
//
// Failures degrade to cold-start with a WARN log; a corrupted
// manifest must not brick the daemon. Nil library or nil KV are
// clean no-ops so test scaffolding (and pre-smart-mode hosts)
// proceed unchanged.
//
// Before LoadLabels, the helper sweeps the persisted layer via
// EvictPersistedBefore (RULE-SIG-PERSIST-03) so workloads that
// haven't fired in PersistedEvictionAge (30 days) never restore
// into memory — caps long-running-daemon /var/lib/ventd/ growth.
func loadSignatureState(sigLib *signature.Library, kv *state.KVDB, logger *slog.Logger) {
	if sigLib == nil || kv == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	cutoff := time.Now().Add(-signature.PersistedEvictionAge)
	if evicted, evictErr := sigLib.EvictPersistedBefore(kv, cutoff); evictErr != nil {
		// Best-effort: log + continue. The eviction sweep MUST NOT
		// block warm-restart — a partial sweep that surfaced an
		// error has still freed some rows; LoadLabels below
		// continues with whatever survived.
		logger.Warn("signature: persist-eviction sweep had errors",
			"evicted", evicted, "err", evictErr)
	} else if evicted > 0 {
		logger.Info("signature: persist-eviction sweep dropped stale buckets",
			"evicted", evicted, "age_days", int(signature.PersistedEvictionAge.Hours())/24)
	}
	labels, manifestErr := signature.LoadManifest(kv)
	if manifestErr != nil {
		logger.Warn("signature: LoadManifest failed; cold start", "err", manifestErr)
		return
	}
	if len(labels) == 0 {
		logger.Info("signature: no persisted labels; cold start")
		return
	}
	if loadErr := sigLib.LoadLabels(kv, labels); loadErr != nil {
		logger.Warn("signature: LoadLabels failed; cold start",
			"labels", len(labels), "err", loadErr)
		return
	}
	logger.Info("signature: library warm-restarted from KV",
		"labels", len(labels))
}

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
	strictIdleGate bool,
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
	// IRQ baseline lives on the scheduler heap so soft-mode delta
	// detection works across the 60 s tick interval. The first soft
	// tick seeds it from the current IRQ counters and admits without
	// enforcing the IRQ check (no prior reading to delta against);
	// every subsequent tick computes the delta vs the prior tick's
	// counters per RULE-OPP-IDLE-02.
	irqBaseline := idle.IRQCounters{}
	// ProcessBaseline gives the soft gate baseline-tolerance for the
	// blocklist check: long-running media transcoders (ffmpeg,
	// plex-transcoder, etc.) on a Plex / Jellyfin homelab are
	// steady-state load and shouldn't permanently refuse the probe,
	// while a fresh rsync or dnf invocation still triggers a refusal
	// because it's new vs the previous tick's baseline. Same
	// scheduler-heap pattern as irqBaseline.
	processBaseline := map[string]int{}
	idleCfg := idle.OpportunisticGateConfig{
		GateConfig: idle.GateConfig{
			ProcRoot:      "/proc",
			SysRoot:       "/sys",
			AllowOverride: false,
		},
		// Class drives the per-class soft-mode PSI / loadavg ceilings
		// (internal/idle/thresholds.go). Without it the gate falls
		// through to MidDesktop defaults; with the detected class
		// homelab boxes (server / NAS / mini-PC) get the looser
		// ceilings their steady-state services need.
		Class:           cls,
		IRQBaseline:     &irqBaseline,
		ProcessBaseline: &processBaseline,
	}
	if strictIdleGate {
		idleCfg.Mode = idle.ModeStrictIdle
		logger.Info("opportunistic: strict idle gate active (--strict-idle-gate); 600s durability + tight PSI thresholds")
	} else {
		idleCfg.Mode = idle.ModeSoftIdle
		logger.Info("opportunistic: soft idle gate active (default v0.6.0+); single-shot eval + relaxed PSI thresholds (RULE-OPP-IDLE-SOFT-MODE)")
	}
	// Per-channel min_pwm floor for the scheduler's gap filter.
	// Bins below the configured floor are dropped from the gap set
	// before pick — they're fan-off territory by operator declaration
	// (config Fans[i].MinPWM is "below this the fan doesn't spin
	// usefully") so probing there produces no calibration data and
	// reliably trips the slope abort on thermally-loaded hosts.
	minPWMs := map[uint16]uint8{}
	if liveCfg != nil {
		if c := liveCfg.Load(); c != nil {
			for _, f := range c.Fans {
				if f.MinPWM == 0 {
					continue
				}
				minPWMs[observation.ChannelID(f.PWMPath)] = f.MinPWM
			}
		}
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
		MinPWMs:                minPWMs,
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
// stateDir + hwmonFingerprint drive RULE-CONFA-WIRING-02 +
// RULE-CONFA-PERSIST-01/02: after Admit, each channel's persisted
// Bucket is loaded from <stateDir>/smart/conf-A/<channel>.cbor and the
// bin histogram + first-contact flag + last-update wall-clock are
// restored. A fingerprint mismatch (motherboard swap, hwmon
// re-enumeration) discards cleanly per RULE-CONFA-PERSIST-02 and the
// channel re-warms from zero. A schema mismatch
// (RULE-CONFA-PERSIST-03) discards likewise.
//
// Returns nil when there are no controllable channels (monitor-only).
func buildLayerAEstimator(
	channels []*probe.ControllableChannel,
	stateDir string,
	hwmonFingerprint string,
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
	var loaded, fresh int
	for _, ch := range channels {
		tier := fallback.SelectTier(ch)
		if err := est.Admit(ch.PWMPath, tier, layer_a.DefaultNoiseFloor, now); err != nil {
			logger.Warn("layer_a: Admit failed",
				"channel", ch.PWMPath, "tier", tier, "err", err)
			continue
		}
		if stateDir == "" {
			continue
		}
		ok, loadErr := est.LoadChannel(stateDir, ch.PWMPath, hwmonFingerprint, logger)
		switch {
		case loadErr != nil:
			logger.Warn("layer_a: LoadChannel error (cold start)",
				"channel", ch.PWMPath, "err", loadErr)
			fresh++
		case ok:
			loaded++
		default:
			fresh++
		}
	}
	logger.Info("layer_a: estimator initialised",
		"channels", len(channels),
		"loaded", loaded,
		"cold_start", fresh,
		"hwmon_fp", hwmonFingerprint)
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
			acoustic = buildAcousticBudget(live, chID, smart.Blended.Preset())
		}

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
			Acoustic:     acoustic,
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

// buildAcousticBudget assembles the per-tick AcousticBudget for the
// candidate channel using R33 (no-microphone psychoacoustic proxy):
//
//   - Target:     PresetDBATargets[preset] (or operator override) — the
//     dBA cap the gate must not exceed.
//   - CurrentDBA: proxy.Compose() over every configured hwmon fan whose
//     RPM is currently readable, plus R30's per-host K_cal
//     mic-calibration offset when /var/lib/ventd/acoustic/
//     k_cal.json is present. Without K_cal the value is in
//     within-host "au" (today's behaviour); with K_cal it is
//     dBA at the mic position. (#1281)
//   - DBAPerPWM:  proxy.CostRate() for the candidate channel using the
//     fan's measured RPM and a default-classified blade
//     count. Cost-rate is multiplied by the preset weight
//     (Silent 3x cost-averse, Performance 0.2x).
//
// Per-tick cost: one open+read+close per configured hwmon fan
// (typically 1-8) + one optional read of the K_cal file. At ~50 µs
// each that's well under the controller's 2 s tick budget. Returns a
// zero AcousticBudget when no RPMs are readable (host in early boot,
// every fan tach offline) — the gate treats Target=0 as "disabled"
// and the controller behaves identically to the v0.5.11 no-budget
// path.
func buildAcousticBudget(live *config.Config, chID string, preset controller.Preset) controller.AcousticBudget {
	if live == nil {
		return controller.AcousticBudget{}
	}
	target := controller.DBATargetFor(preset, live.Smart.DBATarget)
	if target <= 0 {
		return controller.AcousticBudget{}
	}

	// Compose host loudness from every fan with a readable RPM.
	// hwmon fans read /sys; NVIDIA fans call nvmlDeviceGetFanSpeedRPM
	// (R535+); ErrFanRPMUnsupported / older driver / pre-Maxwell GPU
	// silently skips the fan rather than failing the whole budget.
	// (#1282)
	fans := make([]proxy.Fan, 0, len(live.Fans))
	var candidateRPM float64
	for _, f := range live.Fans {
		var (
			rpm        float64
			ok         bool
			class      proxy.FanClass
			diameterMM float64
			bladeCount int
		)
		switch f.Type {
		case "hwmon":
			if f.RPMPath == "" {
				continue
			}
			r, gotRPM := readRPMSafe(f.RPMPath)
			if !gotRPM || r <= 0 {
				continue
			}
			rpm, ok = float64(r), true
			// hwmon fans honour the curated hwdb fan_profiles entry
			// when present; otherwise resolveFanShape falls through
			// to the name-hint heuristic + 120mm default. (#1283)
			class, diameterMM, bladeCount = resolveFanShape(f)
		case "nvidia":
			r, err := readNvidiaFanRPM(f.PWMPath)
			if err != nil || r <= 0 {
				continue
			}
			rpm, ok = float64(r), true
			// GPU fans use ClassGPUShroud + a name-hint diameter
			// heuristic (#1282); the hwdb catalog doesn't carry
			// per-GPU FanProfiles today.
			class = proxy.ClassGPUShroud
			diameterMM = nvidiaShroudDiameterMM(f.Name)
		default:
			continue
		}
		if !ok {
			continue
		}
		fans = append(fans, proxy.Fan{
			Class:      class,
			DiameterMM: diameterMM,
			BladeCount: bladeCount,
			RPM:        rpm,
		})
		if f.Name == chID || f.PWMPath == chID {
			candidateRPM = rpm
		}
	}
	if len(fans) == 0 {
		return controller.AcousticBudget{}
	}

	current := proxy.Compose(fans) + loadKCalOffset()

	// CostRate for the candidate channel. When the candidate RPM is
	// unknown (chID didn't match any hwmon fan — typically because
	// chID is an nvidia path), DBAPerPWM stays 0 and the gate has
	// nothing per-PWM to refuse — Target alone still bounds via the
	// current-vs-target check.
	var dbaPerPWM float64
	if candidateRPM > 0 {
		dbaPerPWM = proxy.CostRate(
			proxy.ClassCase120140,
			candidateRPM,
			120,
			0, 0,
			5.0, // typical 4-pin PWM consumer fan slope
			presetMultiplierFor(preset),
		)
	}

	return controller.AcousticBudget{
		Target:     target,
		CurrentDBA: current,
		DBAPerPWM:  dbaPerPWM,
	}
}

// readRPMSafe reads a hwmon fan*_input file and returns the int RPM.
// Failure returns (0, false). Used by buildAcousticBudget to compose
// per-tick host loudness without holding the controller hot path on
// blocking IO — sysfs reads are kernel-buffered single-page transfers,
// typically <50 µs each.
func readRPMSafe(path string) (int, bool) {
	if path == "" {
		return 0, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(raw))
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	if v < 0 {
		return 0, false
	}
	return v, true
}

// readNvidiaFanRPMFn is the package-level seam tests use to inject a
// deterministic RPM source without spinning up libnvidia-ml. Default
// is nvidia.ReadFanRPM; smart_builders_nvidia_rpm_test.go points it
// at a fixture. (#1282)
var readNvidiaFanRPMFn = nvidia.ReadFanRPM

// readNvidiaFanRPM resolves a config.Fan{Type:"nvidia"} entry's
// PWMPath (encoded as the GPU index decimal string) into the live
// NVML-reported fan RPM. Failure surfaces (0, err) so the budget
// builder skips the fan rather than failing the host total. (#1282)
func readNvidiaFanRPM(pwmPath string) (uint32, error) {
	idx, err := strconv.ParseUint(strings.TrimSpace(pwmPath), 10, 32)
	if err != nil {
		return 0, err
	}
	return readNvidiaFanRPMFn(uint(idx))
}

// nvidiaShroudDiameterMM picks an axial-shroud diameter heuristic
// from the GPU fan's operator-visible name: triple-fan aftermarket
// AIBs (Aorus, Strix, TUF, Gaming X) ship 120mm shrouds; everything
// else defaults to the 80mm Founders-Edition-class shroud. The
// proxy's tip-speed math scales with diameter², so this distinction
// matters for multi-GPU workstations whose loudness is GPU-fan-
// dominated. (#1282)
func nvidiaShroudDiameterMM(name string) float64 {
	lname := strings.ToLower(name)
	for _, hint := range []string{"aorus", "strix", "tuf", "gaming x", "trinity", "amp", "triple"} {
		if strings.Contains(lname, hint) {
			return 120
		}
	}
	return 80
}

// fanProfileCatalogPtr is the catalog source of per-fan class +
// diameter metadata. The daemon's startup wires the matched
// BoardCatalogEntry here (when DMI / DT / chip-probe matched a
// tier-1/1.5 board); buildAcousticBudget reads from it to replace
// the name-hint heuristics with the curated catalog template.
// Nil = no catalog match (no regression — falls back to defaults).
// (#1283)
var fanProfileCatalogPtr atomic.Pointer[hwdb.BoardCatalogEntry]

// findMatchingBoardEntry returns the first hwdb board entry whose
// DMI fingerprint matches the live host. Mirrors hwdb.MatchV1's tier-1
// preference (skip wildcard-only "*" entries — those are tier-3
// generics with no board-specific FanProfiles to contribute).
// Returns nil when nothing matches. (#1283)
func findMatchingBoardEntry(cat *hwdb.Catalog, dmi hwdb.DMIFingerprint) *hwdb.BoardCatalogEntry {
	if cat == nil {
		return nil
	}
	for _, entry := range cat.Boards {
		if entry == nil || entry.DMIFingerprint == nil {
			continue
		}
		if isWildcardDMIFingerprint(entry.DMIFingerprint) {
			continue
		}
		if hwdb.MatchBoardEntry(entry, dmi, hwdb.LiveDTData{}, true) {
			return entry
		}
	}
	return nil
}

// isWildcardDMIFingerprint returns true when every field in the DMI
// fingerprint is empty or "*". Tier-3 generic entries use this shape
// and never carry board-specific FanProfiles. (#1283)
func isWildcardDMIFingerprint(f *hwdb.BoardDMIFingerprint) bool {
	if f == nil {
		return true
	}
	wild := func(s string) bool { return s == "" || s == "*" }
	return wild(f.SysVendor) && wild(f.ProductName) &&
		wild(f.BoardVendor) && wild(f.BoardName) &&
		wild(f.BoardVersion) && wild(f.BiosVersion)
}

// SetFanProfileCatalog publishes the matched board entry so the
// smart-mode acoustic-budget builder can look up per-fan class +
// diameter overrides. Safe to call multiple times: lock-free atomic
// swap. Passing nil disables the catalog path (heuristics-only).
// (#1283)
func SetFanProfileCatalog(entry *hwdb.BoardCatalogEntry) {
	fanProfileCatalogPtr.Store(entry)
}

// resolveFanShape returns the (class, diameter_mm, blade_count) for
// a config fan. When the matched hwdb board entry has a FanProfile
// keyed on this fan's PWM channel (the basename of f.PWMPath), the
// catalog values take precedence; otherwise the name-hint heuristic
// + 120mm default + per-class blade-count default applies. (#1283)
func resolveFanShape(f config.Fan) (proxy.FanClass, float64, int) {
	entry := fanProfileCatalogPtr.Load()
	if entry != nil && f.Type == "hwmon" && f.PWMPath != "" {
		channel := filepath.Base(f.PWMPath)
		if fp, ok := hwdb.LookupFanProfile(entry, channel); ok {
			class := proxy.FanClass(fp.Class)
			diameter := float64(fp.DiameterMM)
			if diameter <= 0 {
				diameter = 120
			}
			return class, diameter, fp.DefaultBladeCount
		}
	}
	return defaultFanClassFor(f), 120, 0
}

// defaultFanClassFor returns the acoustic-proxy FanClass for a config
// fan entry. Without per-fan blade/diameter calibration data, we
// classify by chip-name + label heuristics: AIO pumps get ClassAIOPump,
// laptop blowers ClassLaptopBlower, everything else ClassCase120140.
// v0.6.x extension: read per-fan class from the wizard's catalog
// overlay once spec-15 sub-issue lands.
func defaultFanClassFor(f config.Fan) proxy.FanClass {
	if f.IsPump {
		return proxy.ClassAIOPump
	}
	name := strings.ToLower(f.Name)
	switch {
	case strings.Contains(name, "blower"):
		return proxy.ClassLaptopBlower
	case strings.Contains(name, "pump"):
		return proxy.ClassAIOPump
	case strings.Contains(name, "gpu") || f.Type == "nvidia":
		return proxy.ClassGPUShroud
	default:
		return proxy.ClassCase120140
	}
}

// kCalPath is the persisted R30 microphone calibration JSON. Set as
// a package-level variable so tests can point at a fixture path
// without monkey-patching the global filesystem. (#1281)
var kCalPath = acrunner.DefaultKCalPath

// loadKCalOffset returns the K_cal offset (dB) when the per-host
// microphone calibration record at kCalPath is present and parseable,
// 0 otherwise. The offset is added to proxy.Compose() so the host
// loudness reported by /api/v1/smart/status.acoustic.current_dba is
// true dBA at the mic position when calibrated, and the within-host
// au scale otherwise. (#1281)
func loadKCalOffset() float64 {
	r, present, err := acrunner.LoadResult(kCalPath)
	if err != nil || !present {
		return 0
	}
	return r.KCalOffset
}

// micCalibrated reports whether a K_cal calibration record is
// present and parseable at kCalPath. Used by the smart-mode status
// handler to surface a mic_calibrated boolean so the UI can render a
// "calibrate mic" hint when the displayed current_dba is still in au.
// (#1281)
func micCalibrated() bool {
	_, present, err := acrunner.LoadResult(kCalPath)
	return err == nil && present
}

// presetMultiplierFor maps the controller's Preset enum to the
// acoustic-proxy's PresetMultiplier so CostRate scales with the
// operator's quietness preference. R33-LOCK-09.
func presetMultiplierFor(p controller.Preset) proxy.PresetMultiplier {
	switch p {
	case controller.PresetSilent:
		return proxy.PresetSilent
	case controller.PresetPerformance:
		return proxy.PresetPerformance
	default:
		return proxy.PresetBalanced
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

// startHwmonSwapMonitor wires the daemon's controllable-channel
// slice into the hwmon swap-monitor goroutine. Spawns one
// goroutine via wg that watches for hwmonN renumbering during
// daemon runtime (USB GPU hotplug, modprobe -r/-i, etc.) — the
// startup-time resolution captured stable-device anchors via
// hwmon.StableDevice, and this loop calls hwmon.MonitorSwap to
// re-resolve them periodically. RULE-HWMON-SWAP-MONITOR.
//
// Channels whose PWMPath doesn't resolve to a stable device (e.g.
// NVML, IPMI, virtual /sys/devices/virtual entries that don't
// expose a chip parent) are silently filtered out — the monitor's
// scope is the hwmon sysfs surface where renumbering can happen.
//
// Returns immediately when no eligible channels exist; the caller
// (daemon-startup) treats the absence of a goroutine as benign.
//
// v0.5.41 ships observability-only: onSwap is nil, so detections
// are surfaced via WARN log lines but no remap dispatch happens.
// The remap dispatch needs a coordinated update across the
// controller's per-channel cache, watchdog entries, and the
// calibration manager — that's a separate refactor scoped to a
// follow-up PR. The seam (onSwap callback) is in place so the
// follow-up only wires the dispatch, not the detection.
func startHwmonSwapMonitor(
	ctx context.Context,
	wg *sync.WaitGroup,
	channels []*probe.ControllableChannel,
	logger *slog.Logger,
) {
	inputs := make([]hwmon.ChannelInput, 0, len(channels))
	for _, ch := range channels {
		if ch == nil || ch.PWMPath == "" {
			continue
		}
		stable := hwmon.StableDevice(ch.PWMPath)
		if stable == "" {
			continue
		}
		inputs = append(inputs, hwmon.ChannelInput{
			StoredPath:   ch.PWMPath,
			StableDevice: stable,
		})
	}
	if len(inputs) == 0 {
		if logger != nil {
			logger.Info("hwmon: swap monitor not started (no eligible channels)")
		}
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		hwmon.MonitorSwap(ctx, inputs, hwmon.DefaultSwapMonitorInterval, logger, nil)
	}()
}

// startPlatformProfileController starts the active platform-profile auto-
// control loop. Detects hardware capabilities, builds a Selector from the
// kernel's available profile choices, and spawns the controller goroutine.
//
// Silently no-ops when:
//   - the platform_profile sysfs interface isn't present (most pre-2020
//     hardware, or vendors that never wired it up — not an error, just
//     "this feature doesn't apply to this host"),
//   - the available-profile list is empty (kernel exposed the interface
//     but no choices — should never happen but be safe), or
//   - selector construction returns an error.
//
// Per feedback-ventd-zero-config-smart this runs by default with no
// configuration knob. The kernel-side "echo $profile > .../profile"
// remains available as the escape hatch — the controller observes
// external writes and backs off for 10 minutes after one.
func startPlatformProfileController(ctx context.Context, wg *sync.WaitGroup, logger *slog.Logger) {
	snap, err := platformprofile.Read()
	if err != nil {
		logger.Warn("platform_profile: sysfs read failed; auto-control disabled", "err", err.Error())
		return
	}
	if !snap.Present {
		logger.Info("platform_profile: kernel does not expose a platform_profile interface; auto-control inactive")
		return
	}
	if len(snap.Available) == 0 {
		logger.Warn("platform_profile: interface present but choices list is empty; auto-control inactive",
			"path", snap.Path)
		return
	}

	hw := platformprofile.DetectHardware(logger)
	sel, err := platformprofile.NewSelector(hw, snap.Available)
	if err != nil {
		logger.Warn("platform_profile: selector construction failed; auto-control inactive",
			"err", err.Error(), "available", snap.Available)
		return
	}

	store := platformprofile.NewLearningStore("/var/lib/ventd/platform_profile.json")
	ctrl := platformprofile.NewController(platformprofile.ControllerOptions{
		Logger:      logger,
		Selector:    sel,
		Store:       store,
		Hardware:    hw,
		TempReader:  platformprofile.DefaultTempReader(),
		RPMReader:   platformprofile.FanMaxRPMReader(),
		LoadReader:  platformprofile.DefaultLoadReader(),
		PowerReader: platformprofile.DefaultPowerReader(),
	})

	wg.Add(1)
	go func() {
		defer wg.Done()
		ctrl.Run(ctx)
	}()
}
