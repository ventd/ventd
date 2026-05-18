package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ventd/ventd/internal/acoustic/runner"
	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/setup"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/watchdog"
)

// acousticOptions captures the --mic / --mic-ref-spl / --mic-seconds /
// --mic-out CLI flags. Wired into runSetup so the wizard's
// calibrate_acoustic gate fires when the operator opts in.
//
// MicDevice="" → the gate is a no-op; the wizard skips the
// calibrate_acoustic phase entirely. Other fields are ignored in that
// case.
type acousticOptions struct {
	MicDevice string
	RefSPL    float64
	Seconds   int
	OutPath   string
}

// acousticOptionsFromFlags packages the flag values as an
// acousticOptions struct. Pure data — no validation; runner.Run
// re-validates RefSPL / Seconds against the canonical bounds.
func acousticOptionsFromFlags(device string, refSPL float64, seconds int, outPath string) acousticOptions {
	return acousticOptions{
		MicDevice: device,
		RefSPL:    refSPL,
		Seconds:   seconds,
		OutPath:   outPath,
	}
}

// makeAcousticRunner adapts internal/acoustic/runner.Run to the
// setup.AcousticRunner signature expected by CalibrateAcousticGate.
// The wizard passes its per-tick AcousticGateOptions (carrying
// MicDevice/RefSPL/Seconds/OutPath/Logger from runSetup verbatim);
// this adapter translates those into a runner.Options and dispatches.
func makeAcousticRunner() setup.AcousticRunner {
	return func(ctx context.Context, opts setup.AcousticGateOptions) error {
		_, err := runner.Run(ctx, runner.Options{
			MicDevice: opts.MicDevice,
			RefSPL:    opts.RefSPL,
			Seconds:   opts.Seconds,
			OutPath:   opts.OutPath,
			Logger:    opts.Logger,
		})
		return err
	}
}

// runSetup runs the interactive CLI setup wizard and writes an initial
// config. acousticOpts.MicDevice="" disables the optional R30
// mic-calibration step.
func runSetup(configPath string, logger *slog.Logger, acousticOpts acousticOptions) error {
	fmt.Println("=== ventd setup wizard ===")
	fmt.Println()

	// Check for existing config.
	if _, err := config.Load(configPath); err == nil {
		fmt.Printf("A config already exists at %s. Use the web UI to reconfigure.\n", configPath)
		return nil
	}

	// Watchdog scoped to the CLI wizard so per-sweep registrations have a
	// landing spot. The defer restores any fan still registered on exit
	// (matches the daemon's main-loop semantics).
	wd := watchdog.New(logger)
	defer wd.Restore()

	// Register HAL backends + wire the channel resolver — without this
	// the wizard's RPM-detection step fails with "no channel resolver
	// set for <pwm_path>" on every channel, "no fans detected" aborts
	// the run, and a CLI-driven first-boot is impossible. The daemon
	// path does this in runDaemon at the controller setup site; the
	// standalone setup wizard previously skipped it. Issue #1025.
	//
	// enableGPUWrite=false: the setup wizard discovers and calibrates
	// fans by reading hwmon/NVML; it never writes GPU fan curves. The
	// production --enable-gpu-write flag only gates daemon-time GPU
	// writes (RULE-GPU-PR2D-01).
	registerHALBackends(logger, false)
	// v0.8.x: calibration.json moved to /var/lib/ventd/setup/. Migrate any
	// legacy file before constructing the manager so the wizard reads from
	// the new canonical location. See calibrate.MigrateLegacyPath.
	if err := calibrate.MigrateLegacyPath(calibrate.DefaultCalibrationPath, calibrate.LegacyCalibrationPath, logger); err != nil {
		logger.Warn("calibrate: legacy path migration failed; setup continues with new path",
			"err", err)
	}
	cal := calibrate.New(calibrate.DefaultCalibrationPath, logger, wd)
	cal.SetChannelResolver(newChannelResolver())
	mgr := setup.New(cal, logger)
	mgr.SetAppliedMarkerPath(setup.DefaultAppliedMarkerPath)
	// Wire the polarity prober so the wizard's Phase 5b polarity probe
	// actually runs (RULE-POLARITY-03 |delta| < 150 RPM phantom cap).
	// Without it, phantom channels slip through to `controls:` —
	// issue #1026.
	mgr.SetPolarityProber(&polarity.HwmonProber{})
	// Wire the calibration-namespace KV so Phase 5b persists polarity
	// results to /var/lib/ventd/state.yaml (RULE-POLARITY-08). Without
	// this the daemon-path's #1037 wiring (main.go:990) covers the
	// web-UI wizard but not the CLI `-setup` flow — operators who run
	// the wizard from CLI would write a fresh config.yaml but the
	// daemon's next start would see no persisted polarity, mark every
	// channel "unknown", and refuse writes (polarity_refused log spam
	// + audibly stuck fans). PID-locked because state.yaml's KV is
	// already daemon-serialised; the lock surfaces "daemon is running"
	// cleanly rather than corrupting telemetry.
	if releasePID, pidErr := state.AcquirePID(state.DefaultDir); pidErr != nil {
		logger.Warn("setup: cannot acquire state lock (is ventd.service running?); polarity will not persist — re-run wizard from web UI after start", "err", pidErr)
	} else {
		defer releasePID()
		if st, stErr := state.Open(state.DefaultDir, logger); stErr != nil {
			logger.Warn("setup: state.Open failed; polarity will not persist", "err", stErr)
		} else {
			defer func() {
				if err := st.Close(); err != nil {
					logger.Error("state close", "err", err)
				}
			}()
			mgr.SetStateKV(st.KV)
		}
	}

	// v0.5.12: opt-in R30 mic calibration. When MicDevice is empty
	// the gate is a clean no-op (RULE-WIZARD-GATE-CALIBRATE-ACOUSTIC-01);
	// the wizard skips the calibrate_acoustic phase entirely.
	if acousticOpts.MicDevice != "" {
		mgr.SetAcousticGateOptions(setup.AcousticGateOptions{
			MicDevice: acousticOpts.MicDevice,
			RefSPL:    acousticOpts.RefSPL,
			Seconds:   acousticOpts.Seconds,
			OutPath:   acousticOpts.OutPath,
			Runner:    makeAcousticRunner(),
			Logger:    logger,
		})
	}

	fmt.Println("Discovering and calibrating fans (this may take several minutes)...")
	fmt.Println()

	if err := mgr.RunBlocking(); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	cfg := mgr.GeneratedConfig()
	if cfg == nil {
		return fmt.Errorf("setup: no config generated")
	}

	validated, err := config.Save(cfg, configPath)
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	_ = validated
	mgr.MarkApplied()

	fmt.Printf("\nConfig written to %s\n", configPath)
	fmt.Printf("  %d sensor(s), %d fan(s), %d curve(s), %d control(s)\n",
		len(cfg.Sensors), len(cfg.Fans), len(cfg.Curves), len(cfg.Controls))
	fmt.Println()
	fmt.Println("Start the daemon:  systemctl start ventd")
	fmt.Println("Enable on boot:    systemctl enable ventd")
	fmt.Println("Edit curves:       http://127.0.0.1:9999/")
	return nil
}
