package main

import (
	"log/slog"
	"os"

	"github.com/ventd/ventd/internal/validity"
)

// defaultCalibrationDir is where calibration JSON files are stored.
const defaultCalibrationDir = "/var/lib/ventd/calibration"

// initCalibrationStore returns a Store pointed at the default calibration
// directory, pre-creating it if needed. Called by --calibrate-probe at daemon
// startup; the setup wizard (PR 2c) also calls this before invoking per-channel
// probe functions from internal/validity (renamed from internal/calibration in
// v0.5.35 — RULE-PKG-VALIDITY-PROBE-BOUNDARY in CLAUDE.md spells out the
// three-package taxonomy: calibrate/ legacy V-model sweep, validity/ PR-2b
// channel-validity probe, probe/ catalog-less primary path).
func initCalibrationStore() *validity.Store {
	if err := os.MkdirAll(defaultCalibrationDir, 0o750); err != nil {
		slog.Default().Warn("calibration dir pre-create failed", "err", err)
	}
	return validity.NewStore(defaultCalibrationDir)
}
