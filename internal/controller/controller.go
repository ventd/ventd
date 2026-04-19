// Package controller implements the per-fan control loop:
// read all sensors → evaluate curve → clamp to fan limits → write PWM.
package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/curve"
	"github.com/ventd/ventd/internal/hal"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	halnvml "github.com/ventd/ventd/internal/hal/nvml"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/watchdog"
)

// sentinelInvalidDuration is how long a sensor must return sentinel/implausible
// values consecutively before the controller calls watchdog.RestoreOne to hand
// the fan back to firmware auto. 30s = 15 ticks at the default 2s poll interval.
const sentinelInvalidDuration = 30 * time.Second

// CalibrationChecker can report whether a given pwmPath is currently being calibrated.
type CalibrationChecker interface {
	IsCalibrating(pwmPath string) bool
}

// PanicChecker reports whether the daemon is currently in the Session
// C 2e panic state. The controller tick yields when true so the
// MaxPWM values the panic handler wrote at panic start stay put until
// restore clears the flag. pwmPath is passed for symmetry with
// CalibrationChecker (and to keep the door open for per-fan panic
// scoping later), but today every checker ignores it.
type PanicChecker interface {
	IsPanicked(pwmPath string) bool
}

// curveSig is a comparable fingerprint of the non-slice fields in a
// CurveConfig. Used by the compiled-curve cache (Opt-2) to detect
// in-place config mutations (test-only pattern) that share the same
// pointer as the previously-built config.
type curveSig struct {
	typ, sensor, function        string
	minTemp, maxTemp, hysteresis float64
	smoothing                    time.Duration
	minPWM, maxPWM, value        uint8
	setpoint, kp, ki             float64
	integralClamp                float64
	feedForward                  uint8
}

func curveSigOf(c config.CurveConfig) curveSig {
	sig := curveSig{
		typ: c.Type, sensor: c.Sensor, function: c.Function,
		minTemp: c.MinTemp, maxTemp: c.MaxTemp, hysteresis: c.Hysteresis,
		smoothing: c.Smoothing.Duration,
		minPWM:    c.MinPWM, maxPWM: c.MaxPWM, value: c.Value,
	}
	if c.Setpoint != nil {
		sig.setpoint = *c.Setpoint
	}
	if c.Kp != nil {
		sig.kp = *c.Kp
	}
	if c.Ki != nil {
		sig.ki = *c.Ki
	}
	if c.IntegralClamp != nil {
		sig.integralClamp = *c.IntegralClamp
	}
	if c.FeedForward != nil {
		sig.feedForward = *c.FeedForward
	}
	return sig
}

// Controller manages one fan channel.
type Controller struct {
	fanName   string
	curveName string
	pwmPath   string // sysfs path for hwmon; GPU index for nvidia
	fanType   string // "hwmon" or "nvidia"
	cfg       *atomic.Pointer[config.Config]
	wd        *watchdog.Watchdog
	cal       CalibrationChecker
	panic     PanicChecker // nil-safe; nil → panic check skipped
	logger    *slog.Logger
	backend   hal.FanBackend // resolved from fanType at construction
	// onSensorRead is called after each completed tick that attempted a
	// sensor read. Nil in tests; main.go wires it to web.ReadyState so the
	// /readyz probe reflects whether the control loop is still ticking.
	onSensorRead func()

	// Per-curve hysteresis + smoothing state. Reset in initCurveStateIfNeeded
	// when the bound curve name changes on hot-reload so switching between
	// curves doesn't leak stale EMA values or ramp-down thresholds.
	//
	// smoothed holds the exponentially-weighted sensor values; a missing
	// entry means "no prior reading", and the first observed value is used
	// verbatim so EMA can't be biased by a zero-initialised accumulator.
	smoothed    map[string]float64
	lastPWM     uint8   // PWM value of the most recent successful write
	lastTemp    float64 // curve's sensor reading at the moment of lastPWM
	hasLastPWM  bool    // false until the first successful write
	activeCurve string  // tracks curveCfg.Name to detect hot-reload swaps

	// Opt-1: pre-allocated maps cleared and reused every tick to eliminate
	// per-tick heap allocations from readAllSensors and applySmoothing.
	rawSensorsBuf map[string]float64
	smoothedBuf   map[string]float64
	// sentinelBuf receives the names of sensors that returned a sentinel or
	// implausible value during the most recent readAllSensors call. It is
	// separate from the value map so that ENOENT/EIO failures (which still
	// use the "loud-on-data-loss" MaxPWM path) are not conflated with
	// sentinel rejections (which carry forward the last good PWM).
	sentinelBuf map[string]bool

	// sensorInvalidSince tracks when each sensor name first returned a
	// sentinel in the current consecutive run. Cleared per-sensor when a
	// valid reading arrives. Used to trigger watchdog.RestoreOne after
	// sentinelInvalidDuration of consecutive sentinel readings.
	sensorInvalidSince map[string]time.Time

	// Opt-2: compiled curve cached across ticks; rebuilt when the config
	// pointer changes (SIGHUP) or any comparable CurveConfig field changes.
	compiledCurve    curve.Curve
	curveBuiltForCfg *config.Config
	compiledCurveSig curveSig

	// Opt-4: fan*_max read once on first RPM-target write and cached here;
	// 0 = not yet cached (non-RPM-target fans stay 0 and are never used).
	maxRPM       int
	maxRPMCached bool

	// piState holds the per-channel PI integral state, keyed by channel ID
	// (pwmPath). Each Controller manages one channel, so the map has at most
	// one entry. A map allows clean purge on panic or curve-kind change.
	//
	// Lifecycle rules:
	//   • Initialised to empty map in New().
	//   • Cleared (delete) in initCurveStateIfNeeded on curve-name change.
	//   • Cleared for all channels on panic-mode engagement.
	//   • Deleted for a channel when its curve is non-PI (handles SIGHUP
	//     reload that changes type from pi to something else).
	piState map[string]curve.PIState

	// lastTickAt is the wall-clock time at the end of the most recent tick.
	// Used to compute the elapsed dt passed to EvaluateStateful each tick.
	lastTickAt time.Time

	// wasInPanic tracks whether the previous tick was yielded to panic mode.
	// Used to detect the engagement transition and reset piState exactly once.
	wasInPanic bool

	// fatalErr carries a fatal error from tick() to Run(). Size-1 buffer
	// so tick can signal without blocking; Run reads it in the next select
	// iteration and returns the error so systemd's Restart=on-failure fires.
	fatalErr chan error
}

// Option configures a Controller. Functional options keep New's positional
// signature stable across tests while letting main.go plumb optional hooks.
type Option func(*Controller)

// WithSensorReadHook installs a callback fired at the end of each control
// tick. Used by /readyz to observe "sensor read in the last N seconds"
// without coupling the controller package to internal/web.
func WithSensorReadHook(hook func()) Option {
	return func(c *Controller) { c.onSensorRead = hook }
}

// WithPanicChecker installs the panic-mode gate. When the checker
// returns true the tick yields — no sensor reads, no curve eval, no
// PWM writes. The MaxPWM values the panic handler wrote at panic
// start are left in place until the flag is cleared. Nil-safe: when
// no checker is installed the tick behaves exactly as before.
func WithPanicChecker(p PanicChecker) Option {
	return func(c *Controller) { c.panic = p }
}

func New(
	fanName, curveName string,
	pwmPath, fanType string,
	cfg *atomic.Pointer[config.Config],
	wd *watchdog.Watchdog,
	cal CalibrationChecker,
	logger *slog.Logger,
	opts ...Option,
) *Controller {
	c := &Controller{
		fanName:            fanName,
		curveName:          curveName,
		pwmPath:            pwmPath,
		fanType:            fanType,
		cfg:                cfg,
		wd:                 wd,
		cal:                cal,
		logger:             logger.With("fan", fanName, "curve", curveName),
		rawSensorsBuf:      make(map[string]float64), // Opt-1
		smoothedBuf:        make(map[string]float64), // Opt-1
		sentinelBuf:        make(map[string]bool),    // Opt-1 (sentinel tracking)
		sensorInvalidSince: make(map[string]time.Time),
		piState:            make(map[string]curve.PIState),
		lastTickAt:         time.Now(),
		fatalErr:           make(chan error, 1),
	}
	if fanType == "nvidia" {
		c.backend = halnvml.NewBackend(c.logger)
	} else {
		c.backend = halhwmon.NewBackend(c.logger)
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// signalFatal sends err to fatalErr so Run returns on the next select
// iteration. Non-blocking: if a fatal is already queued, the first one wins.
func (c *Controller) signalFatal(err error) {
	select {
	case c.fatalErr <- err:
	default:
	}
}

// Run starts the control loop. It takes manual control of the PWM channel
// via the backend, ticks at interval until ctx is cancelled, then returns.
//
// The watchdog is restored on every exit path — normal ctx.Done(), early
// error return, and panic — per hwmon-safety rule 4. The daemon-level
// defer in cmd/ventd/main.go is defence-in-depth; the controller owns
// the invariant on its own.
//
// If a panic occurs it is recovered and wrapped so main can perform an
// orderly shutdown.
//
// Individual tick errors (sensor read failure, PWM write failure) are logged
// and skipped — the loop continues so a transient sysfs hiccup does not kill
// the daemon.
func (c *Controller) Run(ctx context.Context, interval time.Duration) (err error) {
	defer func() {
		c.wd.Restore()
		if r := recover(); r != nil {
			c.logger.Error("controller: panic recovered, watchdog restored", "panic", r)
			err = fmt.Errorf("controller %s: panic: %v", c.fanName, r)
		}
	}()

	if c.fanType == "nvidia" {
		c.logger.Info("controller: nvidia fan control acquired", "gpu_index", c.pwmPath)
	}
	// For hwmon fans, manual-mode acquisition happens lazily inside
	// hal/hwmon.Backend.Write on the first tick — the controller no
	// longer touches sysfs mode files directly. Acquire-level errors
	// surface as the first tick's Write error, logged via the same
	// path as any transient write failure.

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("controller: context cancelled, stopping")
			return nil
		case fatalErr := <-c.fatalErr:
			return fatalErr
		case <-ticker.C:
			c.tick()
			// Signal liveness to anything observing the control loop
			// (currently /readyz via web.ReadyState). Fires every tick
			// interval regardless of whether the tick was a full sensor
			// read, a manual-mode PWM write, or a calibration yield —
			// all three prove the loop is still iterating.
			if c.onSensorRead != nil {
				c.onSensorRead()
			}
		}
	}
}

// tick is one control iteration. Errors are logged and the tick is skipped;
// the loop continues on the next interval.
func (c *Controller) tick() {
	// Capture wall-clock time for PI dt computation. lastTickAt is updated
	// at the end of every tick (including yields) via defer so dt tracks
	// actual elapsed time rather than only work-completing ticks.
	now := time.Now()
	dtSeconds := clampDT(now.Sub(c.lastTickAt))
	defer func() { c.lastTickAt = now }()

	// Yield the tick to the calibration goroutine — it owns the PWM channel.
	if c.fanType != "nvidia" && c.cal.IsCalibrating(c.pwmPath) {
		c.logger.Debug("controller: calibration in progress, skipping tick")
		return
	}

	// Panic-mode gate. On the transition into panic (engagement), reset all
	// PI integral state so the forced 100% PWM doesn't wind up the integral
	// for the period the daemon is in emergency mode.
	isPanicked := c.panic != nil && c.panic.IsPanicked(c.pwmPath)
	if isPanicked {
		if !c.wasInPanic {
			c.wasInPanic = true
			for k := range c.piState {
				c.piState[k] = curve.PIState{}
			}
		}
		c.logger.Debug("controller: panic active, skipping tick")
		return
	}
	c.wasInPanic = false

	// Opt-3: read the config pointer exactly once per tick; all code below
	// uses this snapshot so the full tick sees a consistent config view even
	// if a SIGHUP swaps the pointer mid-tick.
	live := c.cfg.Load()

	fan, ok := findFanByPath(live, c.pwmPath, c.fanType)
	if !ok {
		c.logger.Warn("controller: fan not found in live config, skipping tick")
		return
	}

	// Find the control binding for this fan to check ManualPWM.
	var manualPWM *uint8
	curveName := c.curveName
	for _, ctrl := range live.Controls {
		if ctrl.Fan == fan.Name {
			curveName = ctrl.Curve
			manualPWM = ctrl.ManualPWM
			break
		}
	}

	// Manual mode: write the fixed PWM directly, skip sensor reads and curve.
	if manualPWM != nil {
		pwm := clamp(*manualPWM, fan.MinPWM, fan.MaxPWM)
		// hwmon-safety rule 1: never write PWM=0 unless MinPWM=0 AND
		// AllowStop=true. clamp already enforces the MinPWM floor, so
		// pwm==0 here implies MinPWM==0; refuse when AllowStop is false.
		if pwm == 0 && !fan.AllowStop {
			c.logger.Warn("controller: refusing manual PWM=0 on fan without allow_stop",
				"pwm_path", c.pwmPath, "fan_type", fan.Type)
			return
		}
		ch, err := c.channelFor(fan)
		if err != nil {
			c.logger.Error("controller: cannot build backend channel", "err", err)
			return
		}
		if writeErr := c.backend.Write(ch, pwm); writeErr != nil {
			if errors.Is(writeErr, hal.ErrNotPermitted) {
				c.logger.Error("controller: manual-mode acquisition denied by OS; daemon exiting for systemd restart",
					"channel", ch.ID, "err", writeErr)
				c.signalFatal(writeErr)
				return
			}
			c.logger.Error("controller: manual PWM write failed", "err", writeErr)
		}
		c.logger.Debug("controller: manual tick", "pwm", pwm)
		return
	}

	curveCfg, ok := findCurve(live, curveName)
	if !ok {
		c.logger.Warn("controller: curve not found in live config, skipping tick", "curve", curveName)
		return
	}

	// Reset per-curve state when the bound curve name changes (hot-reload
	// swapped the fan to a different curve, or a fresh controller hasn't
	// ticked yet). Without this reset, a switch from a hot curve to a cold
	// one would retain the old lastPWM and suppress ramp-down via
	// hysteresis against a temp the new curve has never seen.
	c.initCurveStateIfNeeded(curveCfg.Name)

	// Read all configured sensors. Individual failures are logged and the sensor
	// is omitted from the map; the curve implementations handle missing sensors
	// safely (Linear returns MaxPWM, keeping the fan at full speed on data loss).
	// This isolates a single flaky sensor from affecting fans on other sensors.
	// Opt-1: writes into the pre-allocated rawSensorsBuf; no heap allocation.
	// sentinelBuf receives names of sensors that returned sentinel/implausible
	// values — distinct from ENOENT/EIO failures so the two failure modes get
	// different treatment below.
	readAllSensors(c.logger, live.Sensors, c.rawSensorsBuf, c.sentinelBuf)

	// Apply EMA smoothing to each sensor before curve evaluation. With
	// Smoothing=0 (the default), α=1 → passthrough; a zero-smoothing
	// config produces the same sensor map it would have without this
	// stage, preserving pre-3a behaviour bit-for-bit.
	sensors := c.applySmoothing(c.rawSensorsBuf, curveCfg.Smoothing.Duration, live.PollInterval.Duration)

	// Sentinel gate: if the curve's bound sensor just returned a sentinel
	// value, carry forward the last known good PWM rather than letting the
	// curve evaluate against a missing key (which would produce MaxPWM).
	// ENOENT/EIO failures still use the MaxPWM fail-safe — this path is
	// intentionally narrow to the sentinel case.
	if curveCfg.Sensor != "" && c.sentinelBuf[curveCfg.Sensor] {
		since, tracked := c.sensorInvalidSince[curveCfg.Sensor]
		if !tracked {
			since = time.Now()
			c.sensorInvalidSince[curveCfg.Sensor] = since
			c.logger.Warn("controller: sensor sentinel, carrying forward last PWM",
				"sensor", curveCfg.Sensor, "fan", c.fanName)
		}
		if time.Since(since) >= sentinelInvalidDuration {
			c.logger.Warn("controller: sensor sentinel for >30s, restoring fan to firmware auto",
				"sensor", curveCfg.Sensor, "fan", c.fanName)
			c.wd.RestoreOne(c.pwmPath)
			return
		}
		if c.hasLastPWM {
			ch, buildErr := c.channelFor(fan)
			if buildErr == nil {
				_ = c.backend.Write(ch, c.lastPWM)
			}
		}
		return
	}
	// Sentinel cleared: reset tracking for this sensor so the 30s clock
	// restarts fresh on the next sentinel run.
	if curveCfg.Sensor != "" {
		delete(c.sensorInvalidSince, curveCfg.Sensor)
	}

	// Opt-2: build the curve graph once; only rebuild when the config pointer
	// changes (SIGHUP) or any comparable CurveConfig field changes (catches
	// in-place mutations used in tests while production always uses new pointers).
	sig := curveSigOf(curveCfg)
	if c.compiledCurve == nil || c.curveBuiltForCfg != live || c.compiledCurveSig != sig {
		built, err := buildCurve(curveCfg, live.Curves)
		if err != nil {
			c.logger.Error("controller: cannot build curve from live config", "err", err)
			return
		}
		c.compiledCurve = built
		c.curveBuiltForCfg = live
		c.compiledCurveSig = sig
	}

	var raw uint8
	if sc, ok := c.compiledCurve.(curve.StatefulCurve); ok {
		// PI (stateful) path: carry integral state across ticks.
		prev := c.piState[c.pwmPath]
		newPWM, newState := sc.EvaluateStateful(sensors, prev, dtSeconds)
		c.piState[c.pwmPath] = newState.(curve.PIState)
		raw = newPWM
	} else {
		// Stateless path: clean up any stale PI state from a prior config
		// reload that changed this channel's curve from pi to non-pi.
		delete(c.piState, c.pwmPath)
		raw = c.compiledCurve.Evaluate(sensors)
	}

	// Clamp to the fan's configured PWM range. This is the hard safety layer:
	// the fan config is authoritative — the curve cannot drive PWM outside it.
	pwm := clamp(raw, fan.MinPWM, fan.MaxPWM)

	// hwmon-safety rule 1: never write PWM=0 unless MinPWM=0 AND
	// AllowStop=true. clamp already enforces the MinPWM floor, so pwm==0
	// here implies MinPWM==0; refuse when AllowStop is false.
	if pwm == 0 && !fan.AllowStop {
		c.logger.Warn("controller: refusing PWM=0 on fan without allow_stop",
			"pwm_path", c.pwmPath, "fan_type", fan.Type)
		return
	}

	// Hysteresis gate — ramp-DOWN only. Ramp-UP is never suppressed so
	// a sudden heat spike always gets immediate cooling, per
	// hwmon-safety.md. Only single-sensor curves (linear, points) can be
	// gated: mix/fixed have no scalar temp to compare against.
	if c.hasLastPWM && pwm < c.lastPWM && curveCfg.Hysteresis > 0 && curveCfg.Sensor != "" {
		currentTemp, ok := sensors[curveCfg.Sensor]
		if ok && currentTemp > c.lastTemp-curveCfg.Hysteresis {
			c.logger.Debug("controller: hysteresis suppressing ramp-down",
				"pwm_path", c.pwmPath,
				"current_temp", currentTemp,
				"last_temp", c.lastTemp,
				"hysteresis", curveCfg.Hysteresis,
				"proposed_pwm", pwm,
				"held_pwm", c.lastPWM,
			)
			return
		}
	}

	ch, err := c.channelFor(fan)
	if err != nil {
		c.logger.Error("controller: cannot build backend channel", "err", err)
		return
	}
	if writeErr := c.backend.Write(ch, pwm); writeErr != nil {
		if errors.Is(writeErr, hal.ErrNotPermitted) {
			c.logger.Error("controller: manual-mode acquisition denied by OS; daemon exiting for systemd restart",
				"channel", ch.ID, "err", writeErr)
			c.signalFatal(writeErr)
			return
		}
		c.logger.Warn("controller: PWM write failed, retrying",
			"event", "write_retry",
			"pwm_path", c.pwmPath, "fan", c.fanName, "err", writeErr)
		time.Sleep(50 * time.Millisecond)
		if retryErr := c.backend.Write(ch, pwm); retryErr != nil {
			c.logger.Error("controller: PWM write failed after retry, triggering restore",
				"event", "write_failed_restore_triggered",
				"pwm_path", c.pwmPath, "fan", c.fanName,
				"err1", writeErr, "err2", retryErr)
			c.wd.RestoreOne(c.pwmPath)
			return
		}
		c.logger.Info("controller: PWM write retry succeeded",
			"event", "write_retry_succeeded",
			"pwm_path", c.pwmPath, "fan", c.fanName)
	}

	// Update hysteresis baseline — the temp + PWM we just committed are
	// what future ramp-down comparisons land against.
	c.lastPWM = pwm
	c.hasLastPWM = true
	if curveCfg.Sensor != "" {
		if t, ok := sensors[curveCfg.Sensor]; ok {
			c.lastTemp = t
		}
	}

	c.logger.Debug("controller: tick",
		"sensors", sensors,
		"curve_pwm", raw,
		"clamped_pwm", pwm,
	)
}

// initCurveStateIfNeeded resets smoothing / hysteresis / PI state when the
// bound curve name changes on hot-reload. A rename-in-place (same Name,
// new params) retains the state — EMA converges quickly enough that
// bridging the parameter change is preferable to a visible step.
func (c *Controller) initCurveStateIfNeeded(curveName string) {
	if c.activeCurve == curveName {
		return
	}
	c.activeCurve = curveName
	c.smoothed = nil
	c.hasLastPWM = false
	c.lastPWM = 0
	c.lastTemp = 0
	// Opt-2: curve name changed; force a graph rebuild on the next tick.
	c.compiledCurve = nil
	c.curveBuiltForCfg = nil
	delete(c.piState, c.pwmPath)
}

// applySmoothing applies a per-sensor EMA using α = poll / (smoothing + poll).
// Passthrough when smoothing == 0 (α collapses to 1). First observation for a
// sensor is stored verbatim so the EMA is not biased by a zero init.
func (c *Controller) applySmoothing(raw map[string]float64, smoothing, poll time.Duration) map[string]float64 {
	if smoothing <= 0 || poll <= 0 {
		return raw
	}
	if c.smoothed == nil {
		c.smoothed = make(map[string]float64, len(raw))
	}
	alpha := float64(poll) / float64(smoothing+poll)
	// Opt-1: reuse the pre-allocated smoothedBuf map instead of allocating a
	// new map each tick. clear() resets entries without releasing memory.
	clear(c.smoothedBuf)
	for name, v := range raw {
		prev, seen := c.smoothed[name]
		if !seen {
			c.smoothed[name] = v
			c.smoothedBuf[name] = v
			continue
		}
		s := alpha*v + (1-alpha)*prev
		c.smoothed[name] = s
		c.smoothedBuf[name] = s
	}
	return c.smoothedBuf
}

// readNvidiaMetric is overridable so tests can substitute a fake without
// requiring a real NVML-backed GPU. Production code always resolves to
// nvidia.ReadMetric.
var readNvidiaMetric = nvidia.ReadMetric

// readAllSensors reads every sensor in the config into dst, clearing it first.
// Opt-1: dst is the Controller's pre-allocated rawSensorsBuf, reused every tick.
// Individual sensor failures are logged and the sensor is omitted from dst;
// the caller never receives an error for a partial read.
//
// sentinelDst, if non-nil, receives the names of sensors that returned a
// sentinel or implausible value (distinct from I/O errors). Callers that need
// to carry forward the last good PWM — rather than fall back to the loud
// MaxPWM path used for ENOENT/EIO — consult this set after the call.
func readAllSensors(logger *slog.Logger, sensors []config.Sensor, dst map[string]float64, sentinelDst map[string]bool) {
	clear(dst)
	if sentinelDst != nil {
		clear(sentinelDst)
	}
	for _, s := range sensors {
		var (
			val float64
			err error
		)
		switch s.Type {
		case "nvidia":
			// invariant: validated at config load — see config.validate
			idx, parseErr := strconv.ParseUint(s.Path, 10, 32)
			if parseErr != nil {
				logger.Error(
					"controller: invariant violated — nvidia sensor path not numeric at runtime; config load should have rejected this",
					"sensor", s.Name,
					"path", s.Path,
					"err", parseErr,
				)
				continue
			}
			val, err = readNvidiaMetric(uint(idx), s.Metric)
		default: // "hwmon" and any future sysfs-backed types
			val, err = hwmon.ReadValue(s.Path)
		}
		if err != nil {
			logger.Warn("controller: sensor read failed, skipping sensor this tick",
				"sensor", s.Name, "err", err)
			continue
		}
		// Sentinel / plausibility filter: reject values matching known driver
		// sentinels or exceeding the plausibility cap for the sensor kind.
		// Only applied to hwmon paths because nvidia metrics have no 0xFFFF
		// sentinel convention and the NVML driver self-validates.
		if s.Type != "nvidia" && halhwmon.IsSentinelSensorVal(s.Path, val) {
			logger.Warn("controller: sensor returned sentinel or implausible value, skipping",
				"sensor", s.Name, "path", s.Path, "value", val)
			if sentinelDst != nil {
				sentinelDst[s.Name] = true
			}
			continue
		}
		dst[s.Name] = val
	}
}

// buildCurve constructs a Curve from a CurveConfig.
// allCurves is required to resolve mix curve sources recursively.
// Add new curve types here as they are implemented.
func buildCurve(cfg config.CurveConfig, allCurves []config.CurveConfig) (curve.Curve, error) {
	switch cfg.Type {
	case "linear":
		return &curve.Linear{
			SensorName: cfg.Sensor,
			MinTemp:    cfg.MinTemp,
			MaxTemp:    cfg.MaxTemp,
			MinPWM:     cfg.MinPWM,
			MaxPWM:     cfg.MaxPWM,
		}, nil

	case "points":
		anchors := make([]curve.PointAnchor, len(cfg.Points))
		for i, p := range cfg.Points {
			anchors[i] = curve.PointAnchor{Temp: p.Temp, PWM: p.PWM}
		}
		return &curve.Points{SensorName: cfg.Sensor, Anchors: anchors}, nil

	case "fixed":
		return &curve.Fixed{Value: cfg.Value}, nil

	case "mix":
		fn, err := curve.ParseMixFunc(cfg.Function)
		if err != nil {
			return nil, fmt.Errorf("curve %q: %w", cfg.Name, err)
		}
		sources := make([]curve.Curve, 0, len(cfg.Sources))
		for _, srcName := range cfg.Sources {
			var srcCfg config.CurveConfig
			for _, c := range allCurves {
				if c.Name == srcName {
					srcCfg = c
					break
				}
			}
			if srcCfg.Name == "" {
				return nil, fmt.Errorf("curve %q: source %q not found", cfg.Name, srcName)
			}
			built, err := buildCurve(srcCfg, allCurves) // recursive
			if err != nil {
				return nil, err
			}
			sources = append(sources, built)
		}
		return &curve.Mix{Sources: sources, Function: fn}, nil

	case "pi":
		return &curve.PICurve{
			SensorName:    cfg.Sensor,
			Setpoint:      *cfg.Setpoint,
			Kp:            *cfg.Kp,
			Ki:            *cfg.Ki,
			FeedForward:   *cfg.FeedForward,
			IntegralClamp: *cfg.IntegralClamp,
		}, nil

	default:
		return nil, fmt.Errorf("unknown curve type %q", cfg.Type)
	}
}

func findFanByPath(cfg *config.Config, pwmPath, fanType string) (config.Fan, bool) {
	for _, f := range cfg.Fans {
		if f.PWMPath == pwmPath && f.Type == fanType {
			return f, true
		}
	}
	return config.Fan{}, false
}

func findCurve(cfg *config.Config, name string) (config.CurveConfig, bool) {
	for _, c := range cfg.Curves {
		if c.Name == name {
			return c, true
		}
	}
	return config.CurveConfig{}, false
}

// parseNvidiaIndex parses the GPU index encoded in pwmPath for nvidia fans.
// Returns a wrapped error whose message includes the offending input so an
// operator can find the broken config line.
func parseNvidiaIndex(s string) (uint, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("controller: parse nvidia GPU index from %q: %w", s, err)
	}
	return uint(v), nil
}

// channelFor builds the hal.Channel the configured backend expects for this fan.
// The channel is built fresh each tick because config.Fan.ControlKind can flip
// on hot-reload (a user may retype a fan from pwm to rpm_target + SIGHUP), and
// the backend dispatches Write based on the RPMTarget opaque flag.
func (c *Controller) channelFor(fan config.Fan) (hal.Channel, error) {
	if c.fanType == "nvidia" {
		if _, err := parseNvidiaIndex(c.pwmPath); err != nil {
			return hal.Channel{}, err
		}
		return hal.Channel{
			ID:     c.pwmPath,
			Role:   hal.RoleGPU,
			Caps:   hal.CapRead | hal.CapWritePWM | hal.CapRestore,
			Opaque: halnvml.State{Index: c.pwmPath},
		}, nil
	}
	caps := hal.CapRead | hal.CapRestore
	rpmTarget := fan.ControlKind == "rpm_target"
	if rpmTarget {
		caps |= hal.CapWriteRPMTarget
		// Opt-4: read fan*_max once on first RPM-target write and cache it so
		// subsequent ticks skip the sysfs round-trip. The cached value is
		// embedded in the channel state where the backend reads it.
		if !c.maxRPMCached {
			c.maxRPM = hwmon.ReadFanMaxRPM(c.pwmPath)
			c.maxRPMCached = true
		}
	} else {
		caps |= hal.CapWritePWM
	}
	return hal.Channel{
		ID:   c.pwmPath,
		Role: hal.RoleUnknown,
		Caps: caps,
		Opaque: halhwmon.State{
			PWMPath:    c.pwmPath,
			RPMTarget:  rpmTarget,
			MaxRPM:     c.maxRPM,
			OrigEnable: -1,
		},
	}, nil
}

// clampDT converts a tick-to-tick duration to a bounded float64 dt in seconds.
// Capped to [0.1s, 10.0s] so a paused/suspended daemon on resume does not
// produce a massive Ki*err*dt integral kick.
func clampDT(d time.Duration) float64 {
	s := d.Seconds()
	if s < 0.1 {
		return 0.1
	}
	if s > 10.0 {
		return 10.0
	}
	return s
}

func clamp(v, lo, hi uint8) uint8 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
