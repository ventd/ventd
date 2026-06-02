package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/ebusy"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/sensorfreeze"
	"github.com/ventd/ventd/internal/signature"
	"github.com/ventd/ventd/internal/smartblend"
	"github.com/ventd/ventd/internal/sysclass"
	"github.com/ventd/ventd/internal/watchdog"
	"github.com/ventd/ventd/internal/web"
)

// controllerSpawner captures the daemon-lifetime dependencies shared by every
// controller so the per-control option wiring lives in exactly one place.
// Both the initial-startup loop and the SIGHUP/restart reload loop in
// runDaemonInternal call options()+spawn(), so the two paths assemble
// identical controller.Options by construction. That shared path structurally
// prevents the "reload forgot a wire" class of bug that previously shipped:
// #1240 (reload path missing the confidence-gated blend hook → smart-mode
// telemetry silently empty) and #1037 (reload path missing the polarity
// channel → inverted-polarity fans driven the wrong way after SIGHUP).
type controllerSpawner struct {
	// ctx/cancel/wg are the CONTROLLER COHORT handles: a context derived from
	// the daemon ctx but cancellable on its own, plus a cohort-scoped
	// waitgroup. newCohort (re)creates them; drainCohort tears the cohort down.
	// Keeping the cancel func in a field (rather than a local var) is also what
	// keeps `go vet`'s lostcancel quiet across the renumber respawn, which
	// re-derives the cohort. RULE-CTRL-REBIND-FOLLOW.
	ctx        context.Context
	cancel     context.CancelFunc
	wg         *sync.WaitGroup
	errCh      chan<- error
	liveCfg    *atomic.Pointer[config.Config]
	wd         *watchdog.Watchdog
	cal        *calibrate.Manager
	readyState *web.ReadyState
	panicCheck controller.PanicChecker
	smartMode  *SmartModeBundle
	sigLib     *signature.Library
	logger     *slog.Logger
	// ebusy is the shared collector each controller's hwmon backend reports
	// EBUSY storms into, for the doctor's ebusy_storm detector. nil-safe.
	ebusy *ebusy.Collector
	// stuckSensors is the shared freeze tracker each controller feeds its
	// per-tick hwmon temp readings, for the doctor's stuck_sensor detector.
	// nil-safe.
	stuckSensors *sensorfreeze.Tracker
}

// labelFn returns the signature-label reader threaded into the observation and
// blend hooks, defaulting to the disabled sentinel when signature learning is
// off (monitor-only, containers/VMs, hardware-refused, or disabled by config).
// Single source for what runDaemonInternal previously inlined four times.
func (s *controllerSpawner) labelFn() func() string {
	if s.sigLib != nil {
		return s.sigLib.Label
	}
	return func() string { return signature.FallbackLabelDisabled }
}

// options assembles the controller options for one control. It is identical
// for the startup and reload paths by construction — that identity is the
// whole point of this helper.
func (s *controllerSpawner) options(
	ctrl config.Control,
	fanCfg config.Fan,
	calMap map[hwdb.ChannelKey]*hwdb.ChannelCalibration,
	resolvePWMUnitMax func(string) int,
) []controller.Option {
	opts := []controller.Option{
		controller.WithSensorReadHook(func() {
			s.readyState.MarkSensorRead(time.Now())
		}),
		controller.WithPanicChecker(s.panicCheck),
	}
	// Wire the polarity channel reference so the controller's hot PWM-write
	// path routes through polarity.WritePWM (RULE-POLARITY-05 /
	// RULE-POLARITY-11). Issue #1037.
	if s.smartMode != nil {
		if pch := findPolarityChannel(s.smartMode.Channels, fanCfg.PWMPath); pch != nil {
			opts = append(opts, controller.WithPolarityChannel(pch))
		}
	}
	if fanCfg.Type == "hwmon" {
		if hwmonName, idx, ok := parseHwmonChannel(fanCfg.PWMPath); ok {
			if calCh, found := calMap[hwdb.ChannelKey{Hwmon: hwmonName, Index: idx}]; found {
				// Issue #1044: pwmUnitMax comes from the catalog match's
				// EffectiveControllerProfile.PWMUnitMax. Hard-coding 255
				// produced garbage on step_0_N / cooling_level inverted
				// channels (e.g. thinkpad_acpi 0..7) via hwdb.InvertPWM.
				opts = append(opts, controller.WithCalibration(calCh, resolvePWMUnitMax(hwmonName)))
			}
		}
	}
	// v0.5.6: stamp every successful PWM write into the observation log with
	// the current signature label. Closes the v0.5.4 controller→obsWriter gap.
	if s.smartMode != nil && s.smartMode.ObsAppend != nil {
		opts = append(opts, controller.WithObservation(s.smartMode.ObsAppend, s.labelFn()))
	}
	// R11: feed the system-wide mass-stall tracker that backs the
	// w_pred_system gate. Reports (channel, committed PWM, observed RPM)
	// every committed tick, reusing the tach read the stuck-fan check
	// already performs. Wired here so the startup and reload paths report
	// identically.
	if s.smartMode != nil && s.smartMode.MassStall != nil {
		tracker := s.smartMode.MassStall
		opts = append(opts, controller.WithStallReporter(fanCfg.PWMPath,
			func(chID string, pwm uint8, rpm int32, now time.Time) {
				tracker.Report(chID, pwm, rpm, now)
			}))
	}
	// Surface a BIOS contesting manual mode to the doctor: the controller's
	// hwmon backend pushes per-channel EBUSY-storm telemetry into the shared
	// collector the ebusy_storm detector reads. No-op for non-hwmon backends;
	// s.ebusy.Observe is nil-safe. RULE-HWMON-EBUSY-RATE-OBSERVABILITY.
	opts = append(opts, controller.WithEBUSYObserver(s.ebusy.Observe))
	// Feed every plausible hwmon temp reading to the shared freeze tracker so
	// the doctor's stuck_sensor detector can flag a sensor frozen at a
	// plausible value while the box is thermally active. s.stuckSensors.Observe
	// is nil-safe. RULE-DOCTOR-DETECTOR-STUCK-SENSOR.
	opts = append(opts, controller.WithSensorObserver(s.stuckSensors.Observe))
	// v0.5.9: install the confidence-gated blend hook when the smart-mode
	// bundle has a BlendedController. The closure pulls the per-channel
	// Snapshots from the upstream runtimes, computes w_pred via the
	// aggregator, and routes through BlendedController.Compute.
	if s.smartMode != nil && s.smartMode.Blended != nil {
		deps := smartblend.Deps{
			Coupling:   s.smartMode.Coupling,
			Marginal:   s.smartMode.Marginal,
			LayerA:     s.smartMode.LayerA,
			Aggregator: s.smartMode.Aggregator,
			Blended:    s.smartMode.Blended,
			Decisions:  s.smartMode.Decisions,
			Gate:       s.smartMode.Gate,
			Drift:      s.smartMode.Drift,
		}
		derivedSetpoint, derivedOK := s.deriveSmartSetpoint(ctrl)
		if blendFn := smartblend.BuildFn(fanCfg.PWMPath, fanCfg, s.liveCfg, deps, s.labelFn(), derivedSetpoint, derivedOK); blendFn != nil {
			opts = append(opts, controller.WithBlend(blendFn))
		}
	}
	return opts
}

// deriveSmartSetpoint resolves the smart-mode fallback setpoint for a control's
// channel when the operator configured none: the bound sensor's thermal limit
// (tempN_crit / CPU Tjmax) minus a margin. ok is false when the sensor exposes
// no plausible limit — BuildFn then runs the channel reactive-only instead of
// guessing a target. Resolved once at controller construction; crit/Tjmax are
// static for the life of the daemon. RULE-CTRL-SMART-RELAX-FLOOR.
func (s *controllerSpawner) deriveSmartSetpoint(ctrl config.Control) (float64, bool) {
	cfg := s.liveCfg.Load()
	if cfg == nil {
		return 0, false
	}
	path := hwmonSensorPathForCurve(cfg, ctrl.Curve)
	if path == "" {
		return 0, false
	}
	return controller.DeriveSmartSetpointC(path, sysclass.TjmaxFromCPUInfo)
}

// hwmonSensorPathForCurve returns the hwmon sysfs temp path bound to a curve via
// its sensor, or "" when the curve, its sensor, or a hwmon path can't be
// resolved — e.g. a non-hwmon sensor such as an NVML GPU temp, which exposes no
// sysfs crit register and so yields no derivable setpoint.
func hwmonSensorPathForCurve(cfg *config.Config, curveName string) string {
	var sensorName string
	for _, cv := range cfg.Curves {
		if cv.Name == curveName {
			sensorName = cv.Sensor
			break
		}
	}
	if sensorName == "" {
		return ""
	}
	for _, sn := range cfg.Sensors {
		if sn.Name == sensorName && sn.Type == "hwmon" {
			return sn.Path
		}
	}
	return ""
}

// newCohort (re)creates the controller-cohort context + waitgroup as fields,
// derived from parent. Controllers spawned afterwards run under this cohort and
// can be torn down independently of the daemon's web/watcher/scheduler
// goroutines. Assigning context.WithCancel straight into the fields (no local
// cancel var) keeps go vet's lostcancel analyzer satisfied across the renumber
// respawn that re-derives the cohort. The cohort is released by drainCohort and,
// as a backstop, transitively by the daemon ctx's own cancel (parent of every
// cohort).
func (s *controllerSpawner) newCohort(parent context.Context) {
	s.ctx, s.cancel = context.WithCancel(parent)
	s.wg = &sync.WaitGroup{}
}

// drainCohort cancels the current cohort and waits for every controller
// goroutine to return — each runs c.wd.Restore() in its defer, so the fans are
// handed back to firmware before the cohort is gone. Safe to call before any
// cohort exists (no-op) and idempotent.
func (s *controllerSpawner) drainCohort() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.wg != nil {
		s.wg.Wait()
	}
}

// reconcile spawns a controller for every control in cfg, under the spawner's
// current cohort ctx/wg. It is the single shared path used by both the
// first-boot config reload and the renumber respawn (RULE-CTRL-REBIND-FOLLOW),
// so neither can drift from the other's option wiring — the same anti-drift
// guarantee spawn/options give the startup vs reload paths (#1240/#1037). A
// control that fails to resolve is logged and skipped (lenient, unlike startup
// which is fatal).
//
// Watchdog registration is deliberately NOT done here. It is a separate concern
// the callers own, because the registration delta differs by path: first-boot
// registers the newly-configured fans, while the renumber path Deregisters the
// old path and Registers the new one for moved fans only — re-Registering an
// unmoved fan would stack a duplicate entry on its startup entry (the watchdog
// uses LIFO duplicate entries for the calibration sweep lifecycle).
func (s *controllerSpawner) reconcile(cfg *config.Config, pollInterval time.Duration) {
	calMap := loadCalibrationByChannel(s.logger)
	resolvePWMUnitMax := makePWMUnitMaxResolver(s.logger)
	for _, ctrl := range cfg.Controls {
		fanCfg, err := resolveControl(cfg, ctrl)
		if err != nil {
			s.logger.Error("resolve control during reconcile", "fan", ctrl.Fan, "err", err)
			continue
		}
		s.spawn(ctrl, fanCfg, s.options(ctrl, fanCfg, calMap, resolvePWMUnitMax), pollInterval)
	}
}

// spawn constructs one controller and launches its goroutine under the shared
// WaitGroup, reporting a fatal Run error on errCh.
func (s *controllerSpawner) spawn(ctrl config.Control, fanCfg config.Fan, opts []controller.Option, pollInterval time.Duration) {
	c := controller.New(
		ctrl.Fan, ctrl.Curve,
		fanCfg.PWMPath, fanCfg.Type,
		s.liveCfg, s.wd, s.cal, s.logger,
		opts...,
	)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if runErr := c.Run(s.ctx, pollInterval); runErr != nil {
			s.errCh <- runErr
		}
	}()
}
