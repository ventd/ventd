package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ventd/ventd/internal/acoustic/runner"
	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/setup"
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
	cal := calibrate.New("/etc/ventd/calibration.json", logger, wd)
	mgr := setup.New(cal, logger)
	mgr.SetAppliedMarkerPath(setup.DefaultAppliedMarkerPath)

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
