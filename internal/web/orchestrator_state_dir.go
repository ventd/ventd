package web

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ventd/ventd/internal/calibrate"
)

// orchestratorStateDir is the v0.8.x canonical directory for wizard +
// calibration state. Derived from calibrate.DefaultCalibrationPath so the
// two stay in sync without a cross-package constant duplication.
var orchestratorStateDir = filepath.Dir(calibrate.DefaultCalibrationPath)

// wipeOrchestratorStateDir removes the entire /var/lib/ventd/setup/ tree.
// Called by handleSetupReset and handleFactoryReset so a fresh wizard
// always starts from an empty canonical state dir — Goal 3 of the v0.8.x
// rework.
//
// Behaviour:
//   - Dir absent: no-op, returns nil
//   - Dir present: rm -rf, returns nil on success
//   - Permission/IO error: returns the error so the caller can log it
//
// The caller is responsible for whether to fail the reset on error. Both
// reset handlers treat wipe failure as a warning rather than HTTP 500 —
// the rest of the reset (config file, KV namespaces, applied marker) has
// already succeeded by the time we get here, and the next daemon start
// recreates whatever state the orchestrator needs.
//
// logger may be nil (silent).
func wipeOrchestratorStateDir(logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.RemoveAll(orchestratorStateDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("wipeOrchestratorStateDir: %w", err)
	}
	logger.Info("setup: orchestrator state dir wiped", "path", orchestratorStateDir)
	return nil
}
