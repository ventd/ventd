package controller

import (
	"context"
	"math"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hal"
)

// Default GPU operating span used when the bound curve carries no usable
// temperature range. RDNA3/4 fans idle (often zero-RPM) below ~40°C and ramp
// toward the junction limit; 40→90°C is a safe, conventional default.
const (
	defaultGPUMinTempC = 40
	defaultGPUMaxTempC = 90
)

// curveSinkBackend reports the backend as a hal.CurveSink when THIS channel is
// curve-controlled — the backend satisfies CurveSink AND the channel advertises
// CapWriteCurve. Returns nil otherwise, including the common case of a backend
// that implements CurveSink for some cards but exposes this particular channel
// as per-tick PWM (AMD amdgpu: RDNA3/4 is a curve sink, RDNA1/2 is not), so the
// caller falls through to the per-tick tick() path.
func (c *Controller) curveSinkBackend() hal.CurveSink {
	cs, ok := c.backend.(hal.CurveSink)
	if !ok {
		return nil
	}
	live := c.cfg.Load()
	fan, ok := findFanByPath(live, c.pwmPath, c.fanType)
	if !ok {
		return nil
	}
	ch, err := c.channelFor(fan)
	if err != nil {
		c.logger.Warn("controller: curve-sink-capable backend but channel build failed; falling back to per-tick path",
			"pwm_path", c.pwmPath, "fan_type", c.fanType, "err", err)
		return nil
	}
	if ch.Caps&hal.CapWriteCurve == 0 {
		return nil
	}
	return cs
}

// runCurveSink drives a hal.CurveSink channel: program the hardware fan curve
// once at startup, then re-program only when the bound curve changes. The
// hardware firmware runs the per-tick control loop itself against its own
// temperature sensor, so this loop performs NO per-tick PWM write. It re-uses
// the controller's existing lifecycle — Run's deferred watchdog Restore hands
// the hardware back to its firmware-default curve on every exit path, and the
// per-tick liveCfg reload means a SIGHUP that rewrites the curve is picked up
// on the next ticker firing exactly like the per-tick path. spec-17 PR-1b.
func (c *Controller) runCurveSink(ctx context.Context, interval time.Duration, cs hal.CurveSink) error {
	c.logger.Info("controller: fan-curve (CurveSink) control acquired; programming hardware curve, firmware runs the loop (no per-tick PWM)",
		"pwm_path", c.pwmPath, "fan_type", c.fanType)
	c.programCurveSink(cs)

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
			c.programCurveSink(cs)
			// Signal control-loop liveness to /readyz exactly as the per-tick
			// path does — the curve-sink tick proves the loop is iterating.
			if c.onSensorRead != nil {
				c.onSensorRead()
			}
		}
	}
}

// programCurveSink translates the bound curve into hardware anchor points and
// programs it via the CurveSink backend, skipping the write when the hardware
// already follows the current curve (fingerprint unchanged). Errors are logged
// and retried on the next tick; they never crash the daemon.
func (c *Controller) programCurveSink(cs hal.CurveSink) {
	live := c.cfg.Load()
	fan, ok := findFanByPath(live, c.pwmPath, c.fanType)
	if !ok {
		c.logger.Warn("controller: fan not found in live config, skipping curve program", "pwm_path", c.pwmPath)
		return
	}

	// Resolve the bound curve + any manual override (mirrors tick()).
	curveName := c.curveName
	var manualPWM *uint8
	for _, ctrl := range live.Controls {
		if ctrl.Fan == fan.Name {
			curveName = ctrl.Curve
			manualPWM = ctrl.ManualPWM
			break
		}
	}

	var curveCfg config.CurveConfig
	if manualPWM == nil {
		cc, found := findCurve(live, curveName)
		if !found {
			c.logger.Warn("controller: curve not found in live config, skipping curve program", "curve", curveName)
			return
		}
		curveCfg = cc
	}

	points := c.curveSinkPoints(fan, curveCfg, manualPWM, live)
	if len(points) == 0 {
		c.logger.Warn("controller: curve produced no anchor points; skipping curve program", "pwm_path", c.pwmPath)
		return
	}

	// Change detection on the OUTPUT curve: re-program only when the computed
	// anchors differ from what was last programmed. Comparing the output (not
	// a config-field fingerprint) catches every shape change — the bound
	// curve's points, the fan's PWM bounds, a manual override — without
	// enumerating which field moved.
	if c.hasProgrammedCurve && curvePointsEqual(c.lastCurvePoints, points) {
		return
	}

	// Shadow mode (#1346): never touch hardware — the operator's existing
	// controller stays in charge. Record the points so we announce once and
	// stay quiet until the output curve actually changes.
	if live.ShadowMode() {
		if !c.shadowAnnounced {
			c.logger.Info("shadow: suppressing fan-curve program (no hardware change); recent-decisions feed shows what ventd would do",
				"event", "shadow_write_suppressed", "pwm_path", c.pwmPath)
			c.shadowAnnounced = true
		}
		c.lastCurvePoints = points
		c.hasProgrammedCurve = true
		return
	}

	ch, err := c.channelFor(fan)
	if err != nil {
		c.logger.Error("controller: cannot build backend channel for curve program", "err", err, "pwm_path", c.pwmPath)
		return
	}

	if err := cs.WriteCurve(ch, points); err != nil {
		// Leave hasProgrammedCurve false so the next tick retries. A persistent
		// failure (e.g. amd_overdrive disabled, kernel < 6.15) logs every tick —
		// that is the operator's signal the gate is blocking the write.
		c.logger.Error("controller: fan-curve program failed; will retry next tick",
			"event", "curve_program_failed", "pwm_path", c.pwmPath, "err", err)
		c.hasProgrammedCurve = false
		return
	}
	c.lastCurvePoints = points
	c.hasProgrammedCurve = true
	c.logger.Info("controller: hardware fan curve programmed",
		"event", "curve_programmed", "pwm_path", c.pwmPath, "anchor_points", len(points))
}

// curvePointsEqual reports whether two anchor sets are identical.
func curvePointsEqual(a, b []hal.CurvePoint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// curveSinkPoints translates the bound curve (or manual override) into an
// ascending-by-temperature slice of hal.CurvePoint sampled at 1°C resolution
// across the curve's temperature span. The CurveSink backend resamples this to
// its hardware anchor count. Each sample is clamped to the fan's [MinPWM,
// MaxPWM] bounds before conversion to percent so the operator's limits are
// honoured (the firmware can't be told to exceed them).
func (c *Controller) curveSinkPoints(fan config.Fan, curveCfg config.CurveConfig, manualPWM *uint8, live *config.Config) []hal.CurvePoint {
	lo, hi := curveSinkTempRange(curveCfg)

	if manualPWM != nil {
		pct := pwmToPct(clamp(*manualPWM, fan.MinPWM, fan.MaxPWM))
		return []hal.CurvePoint{{TempC: lo, Pct: pct}, {TempC: hi, Pct: pct}}
	}

	built, err := buildCurve(curveCfg, live.Curves)
	if err != nil {
		c.logger.Error("controller: cannot build curve for curve program", "err", err, "curve", curveCfg.Name)
		return nil
	}
	sensors := make(map[string]float64, 1)
	pts := make([]hal.CurvePoint, 0, hi-lo+1)
	for t := lo; t <= hi; t++ {
		sensors[curveCfg.Sensor] = float64(t)
		pwm := clamp(built.Evaluate(sensors), fan.MinPWM, fan.MaxPWM)
		pts = append(pts, hal.CurvePoint{TempC: t, Pct: pwmToPct(pwm)})
	}
	return pts
}

// curveSinkTempRange picks the temperature span (°C, ascending) over which to
// sample a curve for hardware programming: the curve's explicit MinTemp/MaxTemp
// when set, else its first/last anchor temperatures, else the default GPU span.
func curveSinkTempRange(cfg config.CurveConfig) (int, int) {
	if cfg.MaxTemp > cfg.MinTemp {
		return int(math.Round(cfg.MinTemp)), int(math.Round(cfg.MaxTemp))
	}
	if len(cfg.Points) >= 2 {
		lo, hi := cfg.Points[0].Temp, cfg.Points[len(cfg.Points)-1].Temp
		if hi > lo {
			return int(math.Round(lo)), int(math.Round(hi))
		}
	}
	return defaultGPUMinTempC, defaultGPUMaxTempC
}

// pwmToPct converts a 0-255 duty byte to a 0-100 percentage.
func pwmToPct(pwm uint8) int {
	return int(math.Round(float64(pwm) / 255 * 100))
}
