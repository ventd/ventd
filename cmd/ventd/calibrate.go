package main

import (
	"log/slog"
	"os"

	"github.com/ventd/ventd/internal/calibration"
)

// defaultCalibrationDir is where calibration JSON files are stored.
const defaultCalibrationDir = "/var/lib/ventd/calibration"

// initCalibrationStore returns a Store pointed at the default calibration
// directory, pre-creating it if needed. Called by --calibrate-probe at daemon
// startup; the setup wizard (PR 2c) also calls this before invoking per-channel
// probe functions from internal/calibration.
func initCalibrationStore() *calibration.Store {
	if err := os.MkdirAll(defaultCalibrationDir, 0o750); err != nil {
		slog.Default().Warn("calibration dir pre-create failed", "err", err)
	}
	return calibration.NewStore(defaultCalibrationDir)
}
