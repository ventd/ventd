package envelope

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/idle"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/sysclass"
)

// ErrEnvelopeDInsufficient is returned by probeD when every step is ≤ baseline.
var ErrEnvelopeDInsufficient = errors.New("envelope D: no steps above baseline; monitor-only fallback required")

// SensorFn reads the current temperature map for a channel (sensor_id → °C).
type SensorFn func(ctx context.Context) (map[string]float64, error)

// RPMFn reads the current RPM for a channel.
type RPMFn func(ctx context.Context) (uint32, error)

// IdleGateFn is a StartupGate-compatible function (injectable for tests).
type IdleGateFn func(ctx context.Context, cfg idle.GateConfig) (bool, idle.Reason, *idle.Snapshot)

// channelWriter routes PWM writes through polarity.WritePWM (RULE-ENVELOPE-01).
type channelWriter struct {
	ch        *probe.ControllableChannel
	writeFunc func(uint8) error
}

func (cw *channelWriter) Write(value uint8) error {
	return polarity.WritePWM(cw.ch, value, cw.writeFunc)
}

// readPWM reads the current PWM value directly from the sysfs file.
// No polarity inversion is applied — this is a raw readback for verification.
func readPWM(path string) (uint8, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read pwm %s: %w", path, err)
	}
	s := strings.TrimSpace(string(data))
	v, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		return 0, fmt.Errorf("parse pwm %s value %q: %w", path, s, err)
	}
	return uint8(v), nil
}

// Prober orchestrates Envelope C/D probing for a set of channels.
type Prober struct {
	db         *state.State
	cls        sysclass.SystemClass
	tjmax      float64
	ambient    float64
	sensorFn   SensorFn
	rpmFn      RPMFn
	idleGate   IdleGateFn
	idleCfg    idle.GateConfig
	log        *slog.Logger
	thresholds *Thresholds
}

// ProberConfig collects constructor arguments for Prober.
type ProberConfig struct {
	State      *state.State
	Class      sysclass.SystemClass
	Tjmax      float64
	Ambient    float64
	SensorFn   SensorFn
	RPMFn      RPMFn
	IdleGate   IdleGateFn
	IdleCfg    idle.GateConfig
	Logger     *slog.Logger
	Thresholds *Thresholds // optional override; nil uses LookupThresholds(Class)
}

// NewProber creates a Prober with the supplied configuration.
func NewProber(cfg ProberConfig) *Prober {
	return &Prober{
		db:         cfg.State,
		cls:        cfg.Class,
		tjmax:      cfg.Tjmax,
		ambient:    cfg.Ambient,
		sensorFn:   cfg.SensorFn,
		rpmFn:      cfg.RPMFn,
		idleGate:   cfg.IdleGate,
		idleCfg:    cfg.IdleCfg,
		log:        cfg.Logger,
		thresholds: cfg.Thresholds,
	}
}

// Probe runs the Envelope C/D probe sequentially across all channels (RULE-ENVELOPE-11).
// It resumes from persisted state when available (RULE-ENVELOPE-09).
func (p *Prober) Probe(ctx context.Context, channels []*probe.ControllableChannel, writeFns []func(uint8) error) error {
	var thr Thresholds
	if p.thresholds != nil {
		thr = *p.thresholds
	} else {
		thr = LookupThresholds(p.cls)
	}

	if !ambientHeadroomOK(p.ambient, p.tjmax, thr) {
		return fmt.Errorf("envelope: ambient %.1f°C leaves insufficient headroom below Tjmax %.1f°C (need %.1f°C margin)",
			p.ambient, p.tjmax, thr.AmbientHeadroomMin)
	}

	for i, ch := range channels {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		cw := &channelWriter{ch: ch, writeFunc: writeFns[i]}
		if err := p.probeOne(ctx, ch, cw, thr); err != nil && !errors.Is(err, ErrEnvelopeDInsufficient) {
			return err
		}
	}
	return nil
}

// probeOne handles a single channel: resume logic → Envelope C → Envelope D fallback.
func (p *Prober) probeOne(ctx context.Context, ch *probe.ControllableChannel, cw *channelWriter, thr Thresholds) error {
	channelID := ch.PWMPath

	kv, hasKV := LoadChannelKV(p.db.KV, channelID)

	if hasKV {
		switch kv.State {
		case StateCompleteC, StateCompleteD:
			p.log.Info("envelope: channel already complete, skipping", "channel", channelID, "state", kv.State)
			return nil
		case StatePausedUserIdle, StatePausedThermal, StatePausedLoad:
			ok, _, _ := p.idleGate(ctx, p.idleCfg)
			if !ok {
				p.log.Info("envelope: channel still paused (idle gate not satisfied)", "channel", channelID)
				return nil
			}
		case StateAbortedC:
			return p.runProbeD(ctx, ch, cw, thr, kv.BaselinePWM, channelID)
		}
	}

	return p.runProbeC(ctx, ch, cw, thr, channelID, kv, hasKV)
}

// runProbeC executes Envelope C (descending PWM sweep with thermal guard).
func (p *Prober) runProbeC(ctx context.Context, ch *probe.ControllableChannel, cw *channelWriter, thr Thresholds, channelID string, resumeKV ChannelKV, hasResume bool) error {
	baseline, err := readPWM(ch.PWMPath)
	if err != nil {
		return fmt.Errorf("envelope C: read baseline %s: %w", ch.PWMPath, err)
	}

	defer func() {
		// RULE-ENVELOPE-02: restore baseline on all exit paths.
		_ = cw.writeFunc(baseline)
	}()

	startStep := 0
	if hasResume && resumeKV.State == StateProbing {
		startStep = resumeKV.CompletedStepCount
	}

	kv := ChannelKV{
		State:       StateProbing,
		Envelope:    EnvelopeC,
		StartedAt:   time.Now(),
		BaselinePWM: baseline,
	}
	if hasResume {
		kv.StartedAt = resumeKV.StartedAt
		kv.CompletedStepCount = resumeKV.CompletedStepCount
	}
	_ = PersistChannelKV(p.db.KV, channelID, kv)

	_ = appendStepEvent(p.db.Log, StepEvent{
		SchemaVersion: kvSchemaVersion,
		ChannelID:     channelID,
		Envelope:      EnvelopeC,
		EventType:     EventProbeStart,
		TimestampNs:   time.Now().UnixNano(),
		PWMTarget:     uint16(baseline),
	})

	prevTemps, _ := p.sensorFn(ctx)
	stepStart := time.Now()

	for i := startStep; i < len(thr.PWMSteps); i++ {
		step := thr.PWMSteps[i]

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_ = appendStepEvent(p.db.Log, StepEvent{
			SchemaVersion:   kvSchemaVersion,
			ChannelID:       channelID,
			Envelope:        EnvelopeC,
			EventType:       EventStepBegin,
			TimestampNs:     time.Now().UnixNano(),
			PWMTarget:       uint16(step),
			ControllerState: i + 1,
		})

		if err := cw.Write(step); err != nil {
			return fmt.Errorf("envelope C: write step %d: %w", step, err)
		}

		// RULE-ENVELOPE-14: PWM readback verification.
		actual, readErr := readPWM(ch.PWMPath)
		var flags uint32
		if readErr == nil {
			diff := int(step) - int(actual)
			if diff < 0 {
				diff = -diff
			}
			if diff > 2 {
				flags |= FlagBIOSOverride
				abortReason := fmt.Sprintf("pwm readback mismatch: wrote %d got %d", step, actual)
				kv.State = StateAbortedC
				kv.AbortReason = abortReason
				_ = PersistChannelKV(p.db.KV, channelID, kv)
				_ = appendStepEvent(p.db.Log, StepEvent{
					SchemaVersion: kvSchemaVersion,
					ChannelID:     channelID,
					Envelope:      EnvelopeC,
					EventType:     EventProbeAbort,
					TimestampNs:   time.Now().UnixNano(),
					PWMTarget:     uint16(step),
					PWMActual:     uint16(actual),
					EventFlags:    flags | FlagEnvelopeCAbort,
					AbortReason:   abortReason,
				})
				return p.runProbeD(ctx, ch, cw, thr, baseline, channelID)
			}
		}

		// Hold and sample.
		holdEnd := time.Now().Add(thr.Hold)
		ticker := time.NewTicker(time.Second / time.Duration(thr.SampleHz))
		defer ticker.Stop()
		var aborted bool
		var abortReason string
		var lastTemps map[string]float64
		var lastRPM uint32

	holdLoop:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case now := <-ticker.C:
				curTemps, _ := p.sensorFn(ctx)
				curRPM, _ := p.rpmFn(ctx)
				dt := now.Sub(stepStart)
				if thermalAbort(curTemps, prevTemps, dt, thr) {
					aborted = true
					abortReason = "dTdt_exceeded"
					break holdLoop
				}
				if absoluteTempAbort(curTemps, p.tjmax, thr) {
					aborted = true
					abortReason = "T_abs_exceeded"
					break holdLoop
				}
				prevTemps = copyTemps(curTemps)
				stepStart = now
				lastTemps = curTemps
				lastRPM = curRPM
				if now.After(holdEnd) {
					break holdLoop
				}
			}
		}

		_ = appendStepEvent(p.db.Log, StepEvent{
			SchemaVersion:   kvSchemaVersion,
			ChannelID:       channelID,
			Envelope:        EnvelopeC,
			EventType:       EventStepEnd,
			TimestampNs:     time.Now().UnixNano(),
			PWMTarget:       uint16(step),
			PWMActual:       uint16(actual),
			Temps:           lastTemps,
			RPM:             lastRPM,
			ControllerState: i + 1,
			EventFlags:      flags,
			AbortReason:     abortReason,
		})

		kv.CompletedStepCount = i + 1
		kv.LastStepPWM = step
		_ = PersistChannelKV(p.db.KV, channelID, kv)

		if aborted {
			kv.State = StateAbortedC
			kv.AbortReason = abortReason
			_ = PersistChannelKV(p.db.KV, channelID, kv)
			_ = appendStepEvent(p.db.Log, StepEvent{
				SchemaVersion: kvSchemaVersion,
				ChannelID:     channelID,
				Envelope:      EnvelopeC,
				EventType:     EventProbeAbort,
				TimestampNs:   time.Now().UnixNano(),
				PWMTarget:     uint16(step),
				EventFlags:    FlagEnvelopeCAbort,
				AbortReason:   abortReason,
			})
			return p.runProbeD(ctx, ch, cw, thr, baseline, channelID)
		}
	}

	kv.State = StateCompleteC
	kv.Envelope = EnvelopeC
	kv.LastCalibrationEnvelope = EnvelopeC
	_ = PersistChannelKV(p.db.KV, channelID, kv)
	_ = appendStepEvent(p.db.Log, StepEvent{
		SchemaVersion: kvSchemaVersion,
		ChannelID:     channelID,
		Envelope:      EnvelopeC,
		EventType:     EventProbeComplete,
		TimestampNs:   time.Now().UnixNano(),
	})
	return nil
}

// runProbeD executes Envelope D (ascending ramp-up from baseline, RULE-ENVELOPE-08).
func (p *Prober) runProbeD(ctx context.Context, ch *probe.ControllableChannel, cw *channelWriter, thr Thresholds, baseline uint8, channelID string) error {
	// Count steps above baseline first to detect ErrEnvelopeDInsufficient (RULE-ENVELOPE-13).
	stepsAbove := 0
	for _, s := range thr.PWMSteps {
		if s > baseline {
			stepsAbove++
		}
	}
	if stepsAbove == 0 {
		p.log.Warn("envelope D: no steps above baseline; falling back to monitor-only", "channel", channelID, "baseline", baseline)
		return ErrEnvelopeDInsufficient
	}

	defer func() {
		_ = cw.writeFunc(baseline)
	}()

	kv := ChannelKV{
		State:       StateProbingD,
		Envelope:    EnvelopeD,
		BaselinePWM: baseline,
	}
	_ = PersistChannelKV(p.db.KV, channelID, kv)

	_ = appendStepEvent(p.db.Log, StepEvent{
		SchemaVersion: kvSchemaVersion,
		ChannelID:     channelID,
		Envelope:      EnvelopeD,
		EventType:     EventProbeStart,
		TimestampNs:   time.Now().UnixNano(),
		PWMTarget:     uint16(baseline),
		EventFlags:    FlagEnvelopeDFallback,
	})

	stepNum := 0
	for _, step := range thr.PWMSteps {
		if step <= baseline {
			continue // RULE-ENVELOPE-08
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := cw.Write(step); err != nil {
			return fmt.Errorf("envelope D: write step %d: %w", step, err)
		}

		// Hold.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(thr.Hold):
		}

		curTemps, _ := p.sensorFn(ctx)
		curRPM, _ := p.rpmFn(ctx)
		stepNum++
		kv.CompletedStepCount = stepNum
		kv.LastStepPWM = step
		_ = PersistChannelKV(p.db.KV, channelID, kv)

		_ = appendStepEvent(p.db.Log, StepEvent{
			SchemaVersion:   kvSchemaVersion,
			ChannelID:       channelID,
			Envelope:        EnvelopeD,
			EventType:       EventStepEnd,
			TimestampNs:     time.Now().UnixNano(),
			PWMTarget:       uint16(step),
			Temps:           curTemps,
			RPM:             curRPM,
			ControllerState: stepNum,
			EventFlags:      FlagEnvelopeDFallback,
		})
	}

	kv.State = StateCompleteD
	kv.LastCalibrationEnvelope = EnvelopeD
	_ = PersistChannelKV(p.db.KV, channelID, kv)
	_ = appendStepEvent(p.db.Log, StepEvent{
		SchemaVersion: kvSchemaVersion,
		ChannelID:     channelID,
		Envelope:      EnvelopeD,
		EventType:     EventProbeComplete,
		TimestampNs:   time.Now().UnixNano(),
		EventFlags:    FlagEnvelopeDFallback,
	})
	return nil
}
