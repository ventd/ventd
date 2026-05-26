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
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/signature"
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
	ctx        context.Context
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
	// v0.5.9: install the confidence-gated blend hook when the smart-mode
	// bundle has a BlendedController. The closure pulls the per-channel
	// Snapshots from the upstream runtimes, computes w_pred via the
	// aggregator, and routes through BlendedController.Compute.
	if s.smartMode != nil && s.smartMode.Blended != nil {
		if blendFn := buildBlendFn(fanCfg.PWMPath, fanCfg, s.liveCfg, s.smartMode, s.labelFn(), s.logger); blendFn != nil {
			opts = append(opts, controller.WithBlend(blendFn))
		}
	}
	return opts
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
