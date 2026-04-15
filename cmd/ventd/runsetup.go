package main

import (
	"fmt"
	"log/slog"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/setup"
	"github.com/ventd/ventd/internal/watchdog"
)

// runSetup runs the interactive CLI setup wizard and writes an initial config.
func runSetup(configPath string, logger *slog.Logger) error {
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
