// Package controller implements the per-fan control loop:
// read all sensors → evaluate curve → clamp to fan limits → write PWM.
package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/curve"
	"github.com/ventd/ventd/internal/hal"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	halmsiec "github.com/ventd/ventd/internal/hal/msiec"
	halnvml "github.com/ventd/ventd/internal/hal/nvml"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
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
	fanName    string
	curveName  string
	pwmPath    string // sysfs path for hwmon; GPU index for nvidia
	fanType    string // "hwmon" or "nvidia"
	cfg        *atomic.Pointer[config.Config]
	wd         *watchdog.Watchdog
	cal        CalibrationChecker
	panic      PanicChecker             // nil-safe; nil → panic check skipped
	calCh      *hwdb.ChannelCalibration // nil if no calibration data for this channel
	pwmUnitMax int                      // 255 for duty_0_255, N for step_0_N; 0 when calCh==nil
	logger     *slog.Logger
	backend    hal.FanBackend // resolved from fanType at construction
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

	// lastTickAt is the wall-clock time at the end of the most recent
	// legitimate control-loop tick (a tick that actually evaluated the
	// curve and committed a PWM write — or a manual-mode write). It is
	// used to compute the elapsed dt passed to EvaluateStateful each tick.
	//
	// RULE-CTRL-DT-YIELD-01 (#1046): yields — calibration in progress,
	// panic engaged, fan-not-found, refused PWM=0, sentinel carry-forward
	// — must NOT advance lastTickAt. Doing so caused the first post-yield
	// control tick to compute dt against the LAST yield rather than the
	// last actual control tick, which violated the documented "dt tracks
	// actual elapsed time" invariant in a way that could over- or
	// under-correct on the post-yield first tick.
	//
	// hasLastTickAt is false until the first legitimate tick lands and on
	// every yield path; the first post-yield control tick observes
	// hasLastTickAt==false and uses the clampDT minimum (0.1s) as dt,
	// matching first-tick behaviour after restart.
	lastTickAt    time.Time
	hasLastTickAt bool

	// wasInPanic tracks whether the previous tick was yielded to panic mode.
	// Used to detect the engagement transition and reset piState exactly once.
	wasInPanic bool

	// stuckFanWarnFired gates the once-per-daemon-lifetime
	// slog.LevelWarn for the #757 "fan not spinning" surface. The
	// controller emits a single Warn line the first time it sees a
	// fresh tick land on this channel with the stuck pattern (PWM
	// above the stiction floor, RPM=0). One emission per channel
	// per daemon lifetime — the journal trail stays short while the
	// dashboard banner + doctor surfaces own the recurring view.
	stuckFanWarnFired bool

	// stallReport + stallChannelID feed the R11 system-wide mass-stall
	// tracker (spec-v0_5_9 §2.5 w_pred_system gate). On every committed
	// tick the controller reports (channelID, committed PWM, observed
	// RPM) so the gate can drop predictive control to safe when several
	// fans are concurrently stalled. nil-safe: unset ⇒ no reporting.
	stallReport    StallReporter
	stallChannelID string

	// fatalErr carries a fatal error from tick() to Run(). Size-1 buffer
	// so tick can signal without blocking; Run reads it in the next select
	// iteration and returns the error so systemd's Restart=on-failure fires.
	fatalErr chan error

	// obsAppend and obsLabel wire the v0.5.4 observation log into the
	// hot path. Both are nil-safe: when either is nil the tick does
	// not emit observation records (pre-v0.5.6 behaviour).
	obsAppend func(rec *ObsRecord)
	obsLabel  func() string

	// blendFn is the v0.5.9 confidence-gated blend hook. Called
	// after curve eval + clamp, before hysteresis. nil-safe.
	blendFn BlendFn

	// polarityCh is the live ControllableChannel used by every PWM
	// write to route through polarity.WritePWM (RULE-POLARITY-05 /
	// RULE-POLARITY-11). When nil (test scaffolding without polarity
	// wiring), writes fall through to the unchanged byte semantics —
	// preserves the pre-#1037 behaviour for tests that don't supply
	// a channel. Issue #1037.
	polarityCh *probe.ControllableChannel

	// polarityHandedBack records that the channel has already been
	// handed back to BIOS auto via watchdog.RestoreOne after a polarity
	// refusal (RULE-POLARITY-12). The first refusal triggers the
	// handback + a single operator-visible WARN; subsequent refusals
	// in the same controller lifetime are silent skips so journald
	// isn't spammed at controller poll-rate while the operator
	// investigates the wizard's classification.
	polarityHandedBack bool
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

// WithCalibration installs per-channel calibration data for this controller.
// pwmUnitMax is the upper bound of the PWM scale (255 for duty_0_255 channels).
// When installed, writeWithRetry refuses phantom/BIOS-overridden channels and
// applies polarity inversion to every outgoing PWM value.
func WithCalibration(cal *hwdb.ChannelCalibration, pwmUnitMax int) Option {
	return func(c *Controller) {
		c.calCh = cal
		c.pwmUnitMax = pwmUnitMax
	}
}

// ObsRecord is the controller's view of an observation record. It
// is a package-local shape so internal/controller does not depend
// on internal/observation directly. main.go wires a closure that
// maps these fields into the real observation.Record (computing
// ChannelID via observation.ChannelID(PWMPath)) + calls Writer.Append.
//
// SensorReadings carries the per-tick sensor map (keyed by sensor
// name from config, value in °C). main.go's adapter converts to
// observation.Record's map[uint16]int16 (key=SensorID, value=°C×100)
// at write time. The map is cloned by emitObservation so the next
// tick's mutation of rawSensorsBuf cannot race the writer.
type ObsRecord struct {
	Ts             int64  // Unix microseconds
	PWMPath        string // sysfs path of the channel that just wrote
	PWMWritten     uint8
	RPM            int32 // -1 when tach-less
	SignatureLabel string
	EventFlags     uint32
	SensorReadings map[string]float64
}

// WithObservation wires the v0.5.4 observation log into the
// controller's hot path. After every successful PWM write, the
// controller emits one ObsRecord with PWMWritten + ChannelID + the
// current signature label. labelFn is the lock-free Library.Label
// reader; it MUST NOT block the controller tick. appendFn buffers
// internally and never fails the tick on I/O error.
//
// Both arguments may be nil to disable observation; in that case
// the controller behaves exactly as it did pre-v0.5.6.
func WithObservation(appendFn func(rec *ObsRecord), labelFn func() string) Option {
	return func(c *Controller) {
		c.obsAppend = appendFn
		c.obsLabel = labelFn
	}
}

// BlendFn is the v0.5.9 confidence-gated blend hook. The controller
// hot path invokes it after curve evaluation + clamp but BEFORE
// hysteresis + write, with the reactive PWM as the input and the
// final PWM as the return. Implementations live in the wiring
// layer (cmd/ventd/main.go) where the BlendedController + upstream
// runtime Snapshots are accessible.
//
// nil means no blend — pre-v0.5.9 behaviour. Returning `reactive`
// unchanged is also a valid no-op (used by the wiring layer when
// w_pred is zero or the predictive arm is refused).
type BlendFn func(channelID string, sensorTemp float64, reactivePWM uint8, dt time.Duration, now time.Time) uint8

// WithBlend installs the v0.5.9 confidence-gated blend hook.
func WithBlend(fn BlendFn) Option {
	return func(c *Controller) { c.blendFn = fn }
}

// StallReporter is the R11 mass-stall feed. The controller calls it after
// every committed PWM write with the channel ID, the byte just written,
// and the observed tach RPM (-1 when tach-less or the read failed). The
// wiring layer points it at the system-wide massstall.Tracker so the
// w_pred_system gate can see concurrent fan stalls. nil-safe.
type StallReporter func(channelID string, commandedPWM uint8, observedRPM int32, now time.Time)

// WithStallReporter wires the mass-stall feed for one channel (spec-v0_5_9
// §2.5). channelID is the stable key the tracker counts by (the fan's
// PWM path). nil reporter is a no-op — pre-R11 behaviour.
func WithStallReporter(channelID string, fn StallReporter) Option {
	return func(c *Controller) {
		c.stallChannelID = channelID
		c.stallReport = fn
	}
}

// WithPolarityChannel wires the live probe.ControllableChannel into
// the controller's hot PWM-write path so every backend.Write goes
// through polarity.WritePWM (RULE-POLARITY-05). On inverted-polarity
// channels the helper rewrites value → 255-value before reaching the
// backend; on phantom/unknown channels it refuses the write entirely.
//
// nil-safe by construction. Without a channel wired, the controller
// falls back to the unchanged byte semantics — preserves the pre-#1037
// behaviour for tests that don't supply a channel. Issue #1037.
func WithPolarityChannel(ch *probe.ControllableChannel) Option {
	return func(c *Controller) { c.polarityCh = ch }
}

// writePWMViaPolarity is the controller's polarity-aware PWM write
// helper. Every controller-internal PWM-byte write MUST go through
// this method so the route-via-polarity contract holds across the
// main write path, the retry sub-call, and the sentinel-carry-forward
// branch (RULE-POLARITY-11). When the controller has no
// polarityCh wired, writes pass through unchanged.
//
// Returns nil when polarity.WritePWM refuses the write (phantom /
// unknown channels) — that refusal is operator-visible via the WARN
// log line; the controller treats it as a successful skip so the tick
// loop continues. A genuine backend write error is propagated.
func (c *Controller) writePWMViaPolarity(ch hal.Channel, pwm uint8) error {
	if c.polarityCh == nil {
		return c.backend.Write(ch, pwm)
	}
	var backendErr error
	err := polarity.WritePWM(c.polarityCh, pwm, func(adjusted uint8) error {
		backendErr = c.backend.Write(ch, adjusted)
		return backendErr
	})
	if err != nil {
		// Polarity refusal (phantom/unknown). RULE-POLARITY-12: hand
		// the channel back to BIOS auto on the FIRST refusal so the
		// fan doesn't sit at whatever the last write committed (often
		// PWM=0 from a calibration sweep). Without this, a refused
		// AIO pump silently stays stopped and the CPU thermal-throttles
		// or worse. The watchdog's RestoreOne is the canonical hand-
		// back primitive — same path the daemon-exit defer uses — so
		// the chip-specific pwm_enable fallback chain (RULE-HWMON-
		// ENABLE-EINVAL-FALLBACK et al) applies uniformly.
		//
		// Subsequent refusals in the same controller lifetime are
		// silent skips so journald isn't filled at controller poll-
		// rate. The handback is one-shot per controller: a re-probe
		// + config reload spawns a fresh controller whose flag starts
		// false again.
		if errors.Is(err, polarity.ErrChannelNotControllable) ||
			errors.Is(err, polarity.ErrPolarityNotResolved) {
			if !c.polarityHandedBack {
				c.logger.Warn("controller: polarity refused write; handing back to BIOS auto",
					"event", "polarity_refused",
					"pwm_path", c.pwmPath, "polarity", c.polarityCh.Polarity, "err", err)
				if c.wd != nil {
					c.wd.RestoreOne(c.pwmPath)
				}
				c.polarityHandedBack = true
			}
			return nil
		}
		// Backend error surfaced through polarity.WritePWM's fn —
		// propagate verbatim so the existing retry path runs.
		if backendErr != nil {
			return backendErr
		}
		return err
	}
	return nil
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
		fatalErr:           make(chan error, 1),
	}
	// Backend dispatch:
	//   - "nvidia" / "hwmon" / ""  → the two legacy in-tree backends
	//     constructed with the controller's own logger so the hot path
	//     never touches the global registry.
	//   - any other type           → look up by name in the HAL registry
	//     (registered at startup by cmd/ventd/calresolver.go). Covers
	//     msiec, thinkpad, ipmi, nbfc, crosec, asahi, pwmsys, legion,
	//     corsair — every non-hwmon HAL backend ventd ships.
	// Falls back to hwmon when the type names an unregistered backend so
	// legacy configs and typos still surface a clear error at first read,
	// rather than nil-panic-ing the controller goroutine.
	switch fanType {
	case "nvidia":
		c.backend = halnvml.NewBackend(c.logger)
	case "hwmon", "":
		c.backend = halhwmon.NewBackend(c.logger)
	default:
		if be, ok := hal.Backend(fanType); ok {
			c.backend = be
		} else {
			c.logger.Warn("controller: unknown fan type, falling back to hwmon",
				"fan_type", fanType, "pwm_path", pwmPath)
			c.backend = halhwmon.NewBackend(c.logger)
		}
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

// writeWithRetry performs a backend.Write with one 50ms retry. On
// ErrNotPermitted it signals fatal immediately without retrying; on double
// I/O failure it invokes watchdog.RestoreOne to hand the fan back to firmware
// auto. Returns nil iff the write eventually succeeded. kind is "curve" or
// "manual" for structured log fields.
func (c *Controller) writeWithRetry(ch hal.Channel, pwm uint8, pwmPath, kind string) error {
	// Calibration guard: refuse phantom channels and BIOS-overridden channels.
	if _, err := hwdb.ShouldApplyCurve(c.calCh); err != nil {
		c.logger.Warn("controller: write refused by calibration guard", "err", err, "pwm_path", pwmPath)
		return err
	}
	adjusted := uint8(hwdb.InvertPWM(c.calCh, int(pwm), c.pwmUnitMax))

	writeErr := c.writePWMViaPolarity(ch, adjusted)
	if writeErr == nil {
		return nil
	}
	if errors.Is(writeErr, hal.ErrNotPermitted) {
		c.logger.Error("controller: manual-mode acquisition denied by OS; daemon exiting for systemd restart",
			"channel", ch.ID, "err", writeErr)
		c.signalFatal(writeErr)
		return writeErr
	}
	c.logger.Warn("controller: PWM write failed, retrying",
		"event", "write_retry",
		"pwm_path", pwmPath, "fan", c.fanName, "kind", kind, "err", writeErr)
	time.Sleep(50 * time.Millisecond)
	retryErr := c.writePWMViaPolarity(ch, adjusted)
	if retryErr == nil {
		c.logger.Info("controller: PWM write retry succeeded",
			"event", "write_retry_succeeded",
			"pwm_path", pwmPath, "fan", c.fanName, "kind", kind)
		return nil
	}
	c.logger.Error("controller: PWM write failed after retry, triggering restore",
		"event", "write_failed_restore_triggered",
		"pwm_path", pwmPath, "fan", c.fanName, "kind", kind,
		"err1", writeErr, "err2", retryErr)
	c.wd.RestoreOne(pwmPath)
	return retryErr
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

// chanResolver lazily builds (and memoises) this tick's hal.Channel.
// The write path, observation RPM read, and stuck-fan RPM check all
// resolve through one of these so channelFor runs at most once per
// tick (it re-Enumerates for non-hwmon backends); a tick that yields
// early never calls it, so no channel is built.
type chanResolver = func() (hal.Channel, error)

// tick is one control iteration. Errors are logged and the tick is skipped;
// the loop continues on the next interval.
func (c *Controller) tick() {
	// Capture wall-clock time for PI dt computation. RULE-CTRL-DT-YIELD-01
	// (#1046): only legitimate control-loop ticks advance lastTickAt. Yields
	// (calibration, panic, missing fan, refused PWM=0, sentinel carry-
	// forward) clear hasLastTickAt instead, so the first post-yield tick
	// behaves like first-tick-after-startup (dt == clampDT min) rather than
	// computing dt against the LAST yield.
	now := time.Now()
	var dtSeconds float64
	if c.hasLastTickAt {
		dtSeconds = clampDT(now.Sub(c.lastTickAt))
	} else {
		dtSeconds = clampDT(0)
	}

	// Yield the tick to the calibration goroutine — it owns the PWM channel.
	if c.fanType != "nvidia" && c.cal.IsCalibrating(c.pwmPath) {
		c.logger.Debug("controller: calibration in progress, skipping tick")
		c.hasLastTickAt = false
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
		c.hasLastTickAt = false
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
		c.hasLastTickAt = false
		return
	}

	// Build the backend channel at most once for this tick. channelFor
	// is a pure function of (fanType, pwmPath, fan) but re-Enumerates for
	// non-hwmon backends, so the write path plus the observation and
	// stuck-fan RPM reads previously rebuilt it 2–3× per tick. The memo
	// is lazy: only the branches that actually need a channel pay for it.
	chOnce := sync.OnceValues(func() (hal.Channel, error) { return c.channelFor(fan) })

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
			c.hasLastTickAt = false
			return
		}
		ch, err := chOnce()
		if err != nil {
			c.logger.Error("controller: cannot build backend channel", "err", err)
			c.hasLastTickAt = false
			return
		}
		if err := c.writeWithRetry(ch, pwm, c.pwmPath, "manual"); err != nil {
			c.hasLastTickAt = false
			return
		}
		c.emitObservationFor(chOnce, pwm)
		c.markTickCompleted(now, chOnce, pwm)
		c.logger.Debug("controller: manual tick", "pwm", pwm)
		return
	}

	curveCfg, ok := findCurve(live, curveName)
	if !ok {
		c.logger.Warn("controller: curve not found in live config, skipping tick", "curve", curveName)
		c.hasLastTickAt = false
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
		if !c.hasLastPWM {
			c.logger.Warn("controller: sensor sentinel on first tick, restoring fan to firmware auto",
				"sensor", curveCfg.Sensor, "fan", c.fanName)
			c.wd.RestoreOne(c.pwmPath)
			c.hasLastTickAt = false
			return
		}
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
			c.hasLastTickAt = false
			return
		}
		if c.hasLastPWM {
			ch, buildErr := chOnce()
			if buildErr == nil {
				// Route through polarity.WritePWM so the inversion
				// contract holds on this branch too (RULE-POLARITY-05 /
				// RULE-POLARITY-11). Pre-#1037 this branch wrote the
				// raw lastPWM byte direct to the backend, bypassing
				// the polarity helper — on inverted-polarity fans
				// every sentinel-glitch tick wrote the wrong-direction
				// byte. The error return is intentionally swallowed:
				// the carry-forward branch is best-effort and the
				// next valid sensor read recovers.
				if writeErr := c.writePWMViaPolarity(ch, c.lastPWM); writeErr == nil {
					// Issue #1045: emit an observation record on the
					// sentinel-carry-forward write so the smart-mode
					// Layer-B / Layer-C feed sees observation continuity
					// across sentinel glitches. Without this, every
					// sentinel-glitch tick was invisible to the
					// fallback-tier classifier even though a real PWM
					// byte was committed to the channel.
					c.emitObservationFor(chOnce, c.lastPWM)
				}
			}
		}
		c.hasLastTickAt = false
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

	// Compute the target PWM: evaluate the curve, apply the hard safety
	// clamp, then blend the predictive arm and re-clamp. computePWM owns
	// that clamp→blend→re-clamp safety ordering; tick() owns the piState
	// lifecycle around it. The blend runs BEFORE hysteresis so the
	// predictive contribution feeds back into ramp-down suppression.
	prevPI := c.piState[c.pwmPath]
	pwm, newPI, stateful, raw := computePWM(c.compiledCurve, sensors, prevPI,
		dtSeconds, fan, c.blendFn, curveCfg.Sensor, c.pwmPath, now)
	if stateful {
		// PI (stateful) path: carry integral state across ticks.
		c.piState[c.pwmPath] = newPI
	} else {
		// Stateless path: clean up any stale PI state from a prior config
		// reload that changed this channel's curve from pi to non-pi.
		delete(c.piState, c.pwmPath)
	}

	// hwmon-safety rule 1: never write PWM=0 unless MinPWM=0 AND
	// AllowStop=true. clamp already enforces the MinPWM floor, so pwm==0
	// here implies MinPWM==0; refuse when AllowStop is false.
	if pwm == 0 && !fan.AllowStop {
		c.logger.Warn("controller: refusing PWM=0 on fan without allow_stop",
			"pwm_path", c.pwmPath, "fan_type", fan.Type)
		c.hasLastTickAt = false
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
			// Hysteresis-suppressed ticks are still legitimate control
			// ticks: the curve evaluated, the gate fired, the next tick
			// should compute dt against this tick. Advance lastTickAt.
			c.markTickCompleted(now, chOnce, c.lastPWM)
			return
		}
	}

	ch, err := chOnce()
	if err != nil {
		c.logger.Error("controller: cannot build backend channel", "err", err)
		c.hasLastTickAt = false
		return
	}
	if err := c.writeWithRetry(ch, pwm, c.pwmPath, "curve"); err != nil {
		c.hasLastTickAt = false
		return
	}

	c.emitObservationFor(chOnce, pwm)

	// Update hysteresis baseline — the temp + PWM we just committed are
	// what future ramp-down comparisons land against.
	c.lastPWM = pwm
	c.hasLastPWM = true
	if curveCfg.Sensor != "" {
		if t, ok := sensors[curveCfg.Sensor]; ok {
			c.lastTemp = t
		}
	}

	// Legitimate control-loop tick committed. Advance lastTickAt so the
	// next tick computes dt against this one (RULE-CTRL-DT-YIELD-01).
	c.markTickCompleted(now, chOnce, pwm)

	c.logger.Debug("controller: tick",
		"sensors", sensors,
		"curve_pwm", raw,
		"clamped_pwm", pwm,
	)
}

// computePWM is the pure speed-calculation core of a tick: evaluate the
// compiled curve, apply the hard [MinPWM, MaxPWM] safety clamp, then
// blend the smart-mode predictive arm and re-clamp. It mutates no
// controller state — the caller owns the piState lifecycle via the
// returned (newPI, stateful) pair — so the clamp→blend→re-clamp safety
// ordering lives in exactly one place.
//
// The clamp BEFORE the blend and the re-clamp AFTER are the safety
// contract: the fan config is authoritative, so neither the curve nor
// the predictive arm can drive PWM outside the operator's bounds. The
// reactive PWM handed to blendFn is therefore always already in-bounds.
// blendFn is nil on the pre-v0.5.9 reactive-only path, in which case the
// clamped curve output is returned unchanged.
//
// stateful reports whether the curve carried integral state this tick;
// raw is the pre-clamp curve output (for the tick's debug log).
func computePWM(
	compiled curve.Curve,
	sensors map[string]float64,
	prevPI curve.PIState,
	dtSeconds float64,
	fan config.Fan,
	blendFn BlendFn,
	sensorName, pwmPath string,
	now time.Time,
) (pwm uint8, newPI curve.PIState, stateful bool, raw uint8) {
	if sc, ok := compiled.(curve.StatefulCurve); ok {
		newPWM, newState := sc.EvaluateStateful(sensors, prevPI, dtSeconds)
		raw = newPWM
		newPI = newState.(curve.PIState)
		stateful = true
	} else {
		raw = compiled.Evaluate(sensors)
	}

	// Hard safety clamp: the curve cannot drive PWM outside [MinPWM, MaxPWM].
	pwm = clamp(raw, fan.MinPWM, fan.MaxPWM)

	// Confidence-gated blend hook. Runs AFTER the safety clamp so the
	// reactive PWM handed to the predictive arm is already in-bounds, and
	// the result is re-clamped so the predictive arm can't push the fan
	// outside the operator's bounds either.
	if blendFn != nil && sensorName != "" {
		if t, ok := sensors[sensorName]; ok {
			blended := blendFn(pwmPath, t, pwm, time.Duration(dtSeconds*float64(time.Second)), now)
			pwm = clamp(blended, fan.MinPWM, fan.MaxPWM)
		}
	}
	return pwm, newPI, stateful, raw
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
		case "msiec":
			// invariant: path validated against msiec.AllowedSensorPaths
			// at config load. Value units are native to the sysfs leaf
			// (°C for *_temperature; 0..100 / 0..150 percent for
			// *_fan_speed) — see internal/hal/msiec/sensor.go.
			val, err = halmsiec.ReadSensor(halmsiec.DefaultSysfsRoot, s.Path)
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
		if s.Type != "nvidia" && s.Type != "msiec" && halhwmon.IsSentinelSensorVal(s.Path, val) {
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
	if c.fanType != "hwmon" && c.fanType != "" {
		// Non-hwmon HAL backends (msiec, thinkpad, ipmi, nbfc, crosec,
		// asahi, pwmsys, legion, corsair, …) carry per-channel state
		// that varies by backend — msi-ec embeds the per-board
		// WritableModes set; thinkpad embeds the procfs path; NBFC
		// embeds the EC transport + config — so we can't construct it
		// inline like hwmon/nvml. Defer to the backend's Enumerate to
		// build a properly-populated channel and look it up by ID.
		// Enumerate cost is dominated by a single stat or sysfs read
		// per backend; negligible at controller tick rate.
		chs, err := c.backend.Enumerate(context.Background())
		if err != nil {
			return hal.Channel{}, fmt.Errorf("controller: %s enumerate: %w", c.fanType, err)
		}
		for _, ch := range chs {
			if ch.ID == c.pwmPath {
				return ch, nil
			}
		}
		return hal.Channel{}, fmt.Errorf("controller: %s channel %q not found (driver loaded?)", c.fanType, c.pwmPath)
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

// emitObservationFor emits one ObsRecord to the v0.5.4 observation log
// after a successful PWM write. Closes over the controller's pwmPath
// (its ChannelID is computed by main.go's wiring closure) and the
// signature library's lock-free Label reader.
//
// RULE-CTRL-OBS-RPM-01 (#1047): for fans with a tach the controller
// reads RPM via backend.Read so smart-mode consumers (Layer-B / Layer-C
// fallback-tier classifier in R8) see real tach data rather than the
// previous -1 sentinel. Read failures (or fans without a tach) record
// RPM=-1; backend.Read errors are NOT propagated — observation is
// best-effort, the control loop must not abort because tach is flaky.
//
// SensorReadings is cloned from the per-tick rawSensorsBuf so the
// next tick's overwrite cannot race main.go's adapter or any
// downstream consumer. Cost is one short-lived map allocation per
// channel per tick (≈10 entries × 24 bytes ≈ 240 bytes); negligible
// at controller frequencies (≤1 Hz typical).
//
// Nil-safe: when neither obsAppend nor obsLabel is wired, the
// controller behaves exactly as it did pre-v0.5.6.
func (c *Controller) emitObservationFor(chFn chanResolver, pwm uint8) {
	if c.obsAppend == nil {
		return
	}
	label := ""
	if c.obsLabel != nil {
		label = c.obsLabel()
	}
	var sensors map[string]float64
	if len(c.rawSensorsBuf) > 0 {
		sensors = make(map[string]float64, len(c.rawSensorsBuf))
		for k, v := range c.rawSensorsBuf {
			sensors[k] = v
		}
	}
	c.obsAppend(&ObsRecord{
		Ts:             time.Now().UnixMicro(),
		PWMPath:        c.pwmPath,
		PWMWritten:     pwm,
		RPM:            c.readRPMForObs(chFn),
		SignatureLabel: label,
		SensorReadings: sensors,
	})
}

// markTickCompleted advances lastTickAt + hasLastTickAt and runs
// the once-per-daemon-lifetime stuck-fan warn check (#757). All
// successful tick sites in tick() (manual write, hysteresis hold,
// curve-driven write) flow through this helper so the warn gate
// is uniform.
func (c *Controller) markTickCompleted(now time.Time, chFn chanResolver, pwm uint8) {
	c.lastTickAt = now
	c.hasLastTickAt = true
	rpm := c.maybeWarnStuckFan(chFn, pwm)
	if c.stallReport != nil {
		c.stallReport(c.stallChannelID, pwm, rpm, now)
	}
}

// stuckFanWarnPWMFloor mirrors the doctor detector's
// stuckFanMinimumPWM: a fan at PWM < this byte is below the
// stiction floor for most chassis fans, so RPM=0 there is working
// as intended, not a stuck-fan signal.
const stuckFanWarnPWMFloor uint8 = 77 // 30% of 255

// maybeWarnStuckFan reads the tach for hwmon channels above the stiction
// floor and emits one slog.LevelWarn the first time the fan matches the
// "not spinning" pattern (PWM above the floor, tach reads zero). Returns
// the rpm it read, or -1 when it didn't read (non-hwmon channel, or PWM
// below the floor) — the value markTickCompleted folds into the mass-
// stall reporter, so the hot loop reads the tach at most once per tick.
//
// The fire-once gate (c.stuckFanWarnFired) suppresses only the repeat
// WARN, NOT the read: an already-warned stuck channel must keep reporting
// its current RPM so the mass-stall tracker reflects reality. The
// dashboard banner + doctor surfaces own the recurring view.
//
// Only hwmon channels are evaluated; non-hwmon HAL backends report RPM
// via their own paths and the polarity / phantom classifiers on the read
// side already cover their stuck-fan analogs.
func (c *Controller) maybeWarnStuckFan(chFn chanResolver, pwm uint8) int32 {
	if c.fanType != "hwmon" && c.fanType != "" {
		return -1
	}
	if pwm < stuckFanWarnPWMFloor {
		return -1
	}
	rpm := c.readRPMForObs(chFn)
	if rpm <= 0 && !c.stuckFanWarnFired {
		// rpm == -1 means "tach read failed / not configured"; we
		// cannot distinguish that from "fan dead", so emit anyway —
		// the warn text below names the path so the operator can
		// triage. The fire-once gate prevents log spam.
		c.logger.Warn("controller: fan not spinning",
			"pwm_path", c.pwmPath,
			"fan", c.fanName,
			"pwm", pwm,
			"rpm", rpm,
			"guidance", "run 'ventd doctor' for chip-mode and connector diagnosis")
		c.stuckFanWarnFired = true
	}
	return rpm
}

// readRPMForObs returns the current tach RPM for the channel, or -1
// when no tach is configured / the read fails. Best-effort; never
// returns an error because observation must not abort the control
// loop. RULE-CTRL-OBS-RPM-01.
func (c *Controller) readRPMForObs(chFn chanResolver) int32 {
	ch, err := chFn()
	if err != nil {
		return -1
	}
	// Only hwmon channels have a fan_input we can read via the HAL.
	// nvidia + IPMI backends expose Read but the RPM semantics differ;
	// today only hwmon is wired into smart-mode consumers, so guard
	// here rather than adding backend-dispatch noise.
	if c.fanType != "hwmon" {
		return -1
	}
	if ch.Caps&hal.CapRead == 0 {
		return -1
	}
	reading, err := c.backend.Read(ch)
	if err != nil || !reading.OK {
		return -1
	}
	// hal.Reading.RPM is uint16; the int32 contract on ObsRecord.RPM
	// gives smart-mode consumers room to encode tach-less channels as
	// -1. A uint16 RPM trivially fits in int32.
	return int32(reading.RPM)
}
