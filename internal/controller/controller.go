// Package controller implements the per-fan control loop:
// read all sensors → evaluate curve → clamp to fan limits → write PWM.
package controller

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/curve"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/watchdog"
)

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
		fanName:   fanName,
		curveName: curveName,
		pwmPath:   pwmPath,
		fanType:   fanType,
		cfg:       cfg,
		wd:        wd,
		cal:       cal,
		logger:    logger.With("fan", fanName, "curve", curveName),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Run starts the control loop. It takes manual control of the PWM channel
// (pwm_enable=1), ticks at interval until ctx is cancelled, then returns.
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
	} else {
		live := c.cfg.Load()
		isRPMTarget := false
		if fan, ok := findFanByPath(live, c.pwmPath, c.fanType); ok {
			isRPMTarget = fan.ControlKind == "rpm_target"
		}
		if isRPMTarget {
			// fan*_target fans use pwm*_enable for manual mode, not a
			// (non-existent) fan*_target_enable file.
			enablePath := hwmon.RPMTargetEnablePath(c.pwmPath)
			if writeErr := hwmon.WritePWMEnablePath(enablePath, 1); writeErr != nil &&
				!errors.Is(writeErr, fs.ErrNotExist) {
				return fmt.Errorf("controller %s: take manual control: %w", c.fanName, writeErr)
			}
			c.logger.Info("controller: RPM-target fan manual control acquired", "path", c.pwmPath)
		} else if writeErr := hwmon.WritePWMEnable(c.pwmPath, 1); writeErr != nil {
			if !errors.Is(writeErr, fs.ErrNotExist) {
				return fmt.Errorf("controller %s: take manual control: %w", c.fanName, writeErr)
			}
			c.logger.Info("controller: pwm_enable not supported by driver, writing PWM values directly",
				"pwm_path", c.pwmPath)
		} else {
			c.logger.Info("controller: manual PWM control acquired", "pwm_path", c.pwmPath)
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("controller: context cancelled, stopping")
			return nil
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
	// Yield the tick to the calibration goroutine — it owns the PWM channel.
	if c.fanType != "nvidia" && c.cal.IsCalibrating(c.pwmPath) {
		c.logger.Debug("controller: calibration in progress, skipping tick")
		return
	}
	// Yield to an active panic — the panic handler has already written
	// MaxPWM to this fan and we must not overwrite it with a curve-
	// derived value until the flag clears.
	if c.panic != nil && c.panic.IsPanicked(c.pwmPath) {
		c.logger.Debug("controller: panic active, skipping tick")
		return
	}

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
		var writeErr error
		if c.fanType == "nvidia" {
			idx, err := parseNvidiaIndex(c.pwmPath)
			if err != nil {
				c.logger.Error("controller: invalid nvidia GPU index", "err", err)
				return
			}
			writeErr = nvidia.WriteFanSpeed(idx, pwm)
		} else if fan.ControlKind == "rpm_target" {
			maxRPM := hwmon.ReadFanMaxRPM(c.pwmPath)
			rpm := int(math.Round(float64(pwm) / 255.0 * float64(maxRPM)))
			writeErr = hwmon.WriteFanTarget(c.pwmPath, rpm)
		} else {
			writeErr = hwmon.WritePWM(c.pwmPath, pwm)
		}
		if writeErr != nil {
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
	rawSensors := readAllSensors(c.logger, live.Sensors)

	// Apply EMA smoothing to each sensor before curve evaluation. With
	// Smoothing=0 (the default), α=1 → passthrough; a zero-smoothing
	// config produces the same sensor map it would have without this
	// stage, preserving pre-3a behaviour bit-for-bit.
	sensors := c.applySmoothing(rawSensors, curveCfg.Smoothing.Duration, live.PollInterval.Duration)

	crv, err := buildCurve(curveCfg, live.Curves)
	if err != nil {
		c.logger.Error("controller: cannot build curve from live config", "err", err)
		return
	}

	raw := crv.Evaluate(sensors)

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

	var writeErr error
	if c.fanType == "nvidia" {
		idx, err := parseNvidiaIndex(c.pwmPath)
		if err != nil {
			c.logger.Error("controller: invalid nvidia GPU index", "err", err)
			return
		}
		writeErr = nvidia.WriteFanSpeed(idx, pwm)
	} else if fan.ControlKind == "rpm_target" {
		maxRPM := hwmon.ReadFanMaxRPM(c.pwmPath)
		rpm := int(math.Round(float64(pwm) / 255.0 * float64(maxRPM)))
		writeErr = hwmon.WriteFanTarget(c.pwmPath, rpm)
	} else {
		writeErr = hwmon.WritePWM(c.pwmPath, pwm)
	}
	if writeErr != nil {
		c.logger.Error("controller: PWM write failed", "err", writeErr)
		return
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

// initCurveStateIfNeeded resets smoothing / hysteresis state when the
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
	out := make(map[string]float64, len(raw))
	for name, v := range raw {
		prev, seen := c.smoothed[name]
		if !seen {
			c.smoothed[name] = v
			out[name] = v
			continue
		}
		s := alpha*v + (1-alpha)*prev
		c.smoothed[name] = s
		out[name] = s
	}
	return out
}

// readNvidiaMetric is overridable so tests can substitute a fake without
// requiring a real NVML-backed GPU. Production code always resolves to
// nvidia.ReadMetric.
var readNvidiaMetric = nvidia.ReadMetric

// readAllSensors reads every sensor in the config and returns a name→tempC map.
// Individual sensor failures are logged and the sensor is omitted from the
// returned map; the caller never receives an error for a partial read.
func readAllSensors(logger *slog.Logger, sensors []config.Sensor) map[string]float64 {
	m := make(map[string]float64, len(sensors))
	for _, s := range sensors {
		var (
			tempC float64
			err   error
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
			tempC, err = readNvidiaMetric(uint(idx), s.Metric)
		default: // "hwmon" and any future sysfs-backed types
			tempC, err = hwmon.ReadValue(s.Path)
		}
		if err != nil {
			logger.Warn("controller: sensor read failed, skipping sensor this tick",
				"sensor", s.Name, "err", err)
			continue
		}
		m[s.Name] = tempC
	}
	return m
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

func clamp(v, lo, hi uint8) uint8 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
