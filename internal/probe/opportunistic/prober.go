package opportunistic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/envelope"
	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/sysclass"
)

// ProbeDuration is the locked hold time for a single opportunistic probe.
// 30 ± 5 s per RULE-OPP-PROBE-02. The scheduler picks one PWM bin per
// fire, holds it, observes, restores.
const ProbeDuration = 30 * time.Second

// ProbeJitterTolerance is the ± window the prober's reported elapsed
// time may sit inside without violating RULE-OPP-PROBE-02.
const ProbeJitterTolerance = 5 * time.Second

// ErrProbeAborted indicates the probe ended early via thermal slope or
// absolute-temperature trip. The observation record is still emitted
// with the abort flag; the bin remains "visited" for the cool-down.
var ErrProbeAborted = errors.New("opportunistic: probe aborted by thermal guard")

// ErrChannelGone indicates the PWM sysfs path disappeared mid-probe
// (hot-unplug). Restore is a best-effort no-op; the scheduler removes
// the channel from its working set on the next detector pass.
var ErrChannelGone = errors.New("opportunistic: channel sysfs path no longer exists")

// SensorFn reads the current temperature map (sensor_id -> °C).
type SensorFn func(ctx context.Context) (map[string]float64, error)

// RPMFn reads the current RPM for a channel. Returns -1 for tach-less.
type RPMFn func(ctx context.Context) (int32, error)

// WriteFn dispatches a raw PWM byte to the backend. The prober wraps
// every write in polarity.WritePWM, never calls fn directly.
type WriteFn func(uint8) error

// ProbeDeps is the injectable bundle for FireOne. Everything except
// Logger is required.
type ProbeDeps struct {
	Class      sysclass.SystemClass
	Tjmax      float64
	SensorFn   SensorFn
	RPMFn      RPMFn
	WriteFn    WriteFn
	Now        func() time.Time
	ObsAppend  func(rec *observation.Record) error
	Logger     *slog.Logger
	Thresholds *envelope.Thresholds // optional override; nil uses LookupThresholds(Class)
}

// FireOne runs a single opportunistic probe on ch at gapPWM:
//  1. Read controller-managed baseline from sysfs.
//  2. Write gapPWM via polarity.WritePWM (RULE-OPP-PROBE-04).
//  3. Hold for ProbeDuration, sampling at 10 Hz, aborting on thermal
//     thresholds from envelope.LookupThresholds(class) (RULE-OPP-PROBE-05).
//  4. Restore baseline via polarity.WritePWM on every exit path
//     (RULE-OPP-PROBE-10).
//  5. Emit one observation record with EventFlag_OPPORTUNISTIC_PROBE
//     (RULE-OPP-PROBE-11).
func FireOne(ctx context.Context, ch *probe.ControllableChannel, gapPWM uint8, deps ProbeDeps) (err error) {
	if !polarity.IsControllable(ch) {
		return polarity.ErrChannelNotControllable
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	baseline, baseErr := readPWM(ch.PWMPath)
	if baseErr != nil {
		return fmt.Errorf("opportunistic: read baseline: %w", baseErr)
	}

	startTs := now()
	flags := observation.EventFlag_OPPORTUNISTIC_PROBE
	var lastTemps map[string]float64
	var lastRPM int32 = -1

	defer func() {
		// Restore baseline on every exit path.
		restoreErr := polarity.WritePWM(ch, baseline, deps.WriteFn)
		if restoreErr != nil && !errors.Is(restoreErr, polarity.ErrChannelNotControllable) {
			logger.Warn("opportunistic restore failed",
				"channel", ch.PWMPath,
				"baseline", baseline,
				"err", restoreErr)
			if err == nil {
				err = fmt.Errorf("opportunistic: restore baseline: %w", restoreErr)
			}
		}

		// Emit observation record on every exit path so the bin
		// counts as visited for the cool-down (RULE-OPP-PROBE-06).
		// Aborts AND successes both visit the bin, but aborts get
		// the additional EventFlag_ENVELOPE_C_ABORT bit.
		rec := &observation.Record{
			Ts:              startTs.UnixMicro(),
			ChannelID:       observation.ChannelID(ch.PWMPath),
			PWMWritten:      gapPWM,
			ControllerState: observation.ControllerState_WARMING,
			RPM:             lastRPM,
			Polarity:        polarityByte(ch.Polarity),
			EventFlags:      flags,
		}
		if lastTemps != nil {
			rec.SensorReadings = make(map[uint16]int16, len(lastTemps))
			i := uint16(0)
			for _, t := range lastTemps {
				rec.SensorReadings[i] = int16(t * 1000)
				i++
			}
		}
		if appendErr := deps.ObsAppend(rec); appendErr != nil {
			logger.Warn("opportunistic record append failed",
				"channel", ch.PWMPath,
				"err", appendErr)
		}
	}()

	// Step 1: write the gap PWM (polarity-aware).
	if writeErr := polarity.WritePWM(ch, gapPWM, deps.WriteFn); writeErr != nil {
		return fmt.Errorf("opportunistic: write gap pwm: %w", writeErr)
	}

	// Step 2: hold and observe.
	thr := envelope.Thresholds{}
	if deps.Thresholds != nil {
		thr = *deps.Thresholds
	} else {
		thr = envelope.LookupThresholds(deps.Class)
	}

	holdEnd := startTs.Add(ProbeDuration)
	tickEvery := time.Second / 10
	if thr.SampleHz > 0 {
		tickEvery = time.Second / time.Duration(thr.SampleHz)
	}
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	var prevTemps map[string]float64
	prevTs := startTs

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sample := <-ticker.C:
			curTemps, _ := deps.SensorFn(ctx)
			curRPM, _ := deps.RPMFn(ctx)
			lastTemps = curTemps
			lastRPM = curRPM

			if prevTemps != nil {
				dt := sample.Sub(prevTs)
				if abortOnSlope(curTemps, prevTemps, dt, thr) {
					flags |= observation.EventFlag_ENVELOPE_C_ABORT
					return ErrProbeAborted
				}
			}
			if abortOnAbsolute(curTemps, deps.Tjmax, thr) {
				flags |= observation.EventFlag_ENVELOPE_C_ABORT
				return ErrProbeAborted
			}
			prevTemps = copyTemps(curTemps)
			prevTs = sample

			if !sample.Before(holdEnd) {
				return nil
			}
		}
	}
}

// readPWM reads the current PWM byte from the sysfs file. No polarity
// inversion — this is the controller's writeback we want to preserve.
func readPWM(path string) (uint8, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	v, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	return uint8(v), nil
}

// abortOnSlope mirrors envelope's thermalAbort but with the local
// signature. dT/dt above thr.DTDtAbortCPerSec (or per-min for NAS)
// triggers abort.
func abortOnSlope(cur, prev map[string]float64, dt time.Duration, thr envelope.Thresholds) bool {
	if len(prev) == 0 || dt <= 0 {
		return false
	}
	if thr.DTDtAbortCPerSec == 0 && thr.DTDtAbortCPerMin == 0 {
		return false
	}
	for id, c := range cur {
		p, ok := prev[id]
		if !ok {
			continue
		}
		delta := c - p
		if thr.DTDtAbortCPerSec > 0 {
			rate := delta / dt.Seconds()
			if rate > thr.DTDtAbortCPerSec {
				return true
			}
		} else {
			rate := delta / dt.Minutes()
			if rate > thr.DTDtAbortCPerMin {
				return true
			}
		}
	}
	return false
}

// abortOnAbsolute mirrors envelope's absoluteTempAbort.
func abortOnAbsolute(cur map[string]float64, tjmax float64, thr envelope.Thresholds) bool {
	var ceiling float64
	if thr.TAbsOffsetBelowTjmax > 0 {
		ceiling = tjmax - thr.TAbsOffsetBelowTjmax
	} else {
		ceiling = thr.TAbsAbsolute
	}
	if ceiling <= 0 {
		return false
	}
	for _, t := range cur {
		if t > ceiling {
			return true
		}
	}
	return false
}

func copyTemps(src map[string]float64) map[string]float64 {
	if src == nil {
		return nil
	}
	out := make(map[string]float64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// polarityByte encodes the polarity string as the byte the observation
// schema expects (0=normal, 1=inverted, 2=indeterminate / phantom).
func polarityByte(p string) uint8 {
	switch p {
	case "normal":
		return 0
	case "inverted":
		return 1
	default:
		return 2
	}
}
