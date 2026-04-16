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

// Controller manages one fan channel.
type Controller struct {
	fanName   string
	curveName string
	pwmPath   string // sysfs path for hwmon; GPU index for nvidia
	fanType   string // "hwmon" or "nvidia"
	cfg       *atomic.Pointer[config.Config]
	wd        *watchdog.Watchdog
	cal       CalibrationChecker
	logger    *slog.Logger
}

func New(
	fanName, curveName string,
	pwmPath, fanType string,
	cfg *atomic.Pointer[config.Config],
	wd *watchdog.Watchdog,
	cal CalibrationChecker,
	logger *slog.Logger,
) *Controller {
	return &Controller{
		fanName:   fanName,
		curveName: curveName,
		pwmPath:   pwmPath,
		fanType:   fanType,
		cfg:       cfg,
		wd:        wd,
		cal:       cal,
		logger:    logger.With("fan", fanName, "curve", curveName),
	}
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

	// Read all configured sensors. Individual failures are logged and the sensor
	// is omitted from the map; the curve implementations handle missing sensors
	// safely (Linear returns MaxPWM, keeping the fan at full speed on data loss).
	// This isolates a single flaky sensor from affecting fans on other sensors.
	sensors := readAllSensors(c.logger, live.Sensors)

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

	c.logger.Debug("controller: tick",
		"sensors", sensors,
		"curve_pwm", raw,
		"clamped_pwm", pwm,
	)
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
