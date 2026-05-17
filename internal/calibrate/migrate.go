package calibrate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// DefaultCalibrationPath is the v0.8.x location for calibration.json. Moved
// from /etc/ventd/ to /var/lib/ventd/setup/ so:
//
//   - /etc holds only user-curated config (config.yaml). Operators backing
//     up /etc no longer accidentally restore stale calibration when
//     redeploying onto upgraded hardware.
//   - /var/lib/ventd/setup/ is the single canonical wipe target for the
//     orchestrator's sanitize phase and the setup/reset endpoint.
const DefaultCalibrationPath = "/var/lib/ventd/setup/calibration.json"

// LegacyCalibrationPath is the v0.7.x and earlier location for
// calibration.json. MigrateLegacyPath relocates any file at this path to
// DefaultCalibrationPath on the first daemon start of an upgraded host.
const LegacyCalibrationPath = "/etc/ventd/calibration.json"

// MigrateLegacyPath ensures any pre-v0.8.x /etc/ventd/calibration.json is
// moved to the new /var/lib/ventd/setup/ location. Idempotent: safe on
// every daemon start.
//
// Behaviour matrix:
//
//	newPath exists  | legacyPath exists | action
//	yes             | (any)             | no-op (migration already happened
//	                                       OR new install never had legacy)
//	no              | no                | no-op (fresh install)
//	no              | yes               | copy legacy → new, write tombstone
//	                                       <legacyPath>.moved-to-var-lib so
//	                                       operators see what happened
//
// Returns error only on filesystem failures the caller cannot reasonably
// recover from (parent mkdir, file read, file write). Missing files are
// not errors — they're handled by the matrix above.
//
// logger may be nil (silent migration); the production caller passes the
// daemon's logger so the migration shows up in `journalctl -u ventd`.
func MigrateLegacyPath(newPath, legacyPath string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	// Already migrated, or new install — fast path.
	if _, err := os.Stat(newPath); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("calibrate: stat %s: %w", newPath, err)
	}

	// No legacy file — nothing to migrate.
	legacyInfo, err := os.Stat(legacyPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("calibrate: stat %s: %w", legacyPath, err)
	}
	if legacyInfo.IsDir() {
		// Unexpected — legacy path is a directory. Leave it alone so an
		// operator who did something custom doesn't lose their state.
		logger.Warn("calibrate: legacy path is a directory, not a file; skipping migration",
			"legacy_path", legacyPath)
		return nil
	}

	// Ensure target directory exists.
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return fmt.Errorf("calibrate: mkdir %s: %w", filepath.Dir(newPath), err)
	}

	// Atomic copy: write to <new>.tmp, fsync, rename. We do NOT
	// os.Rename across paths because /etc and /var/lib are often on
	// different filesystems; rename(2) across mounts is EXDEV.
	if err := copyFileAtomic(legacyPath, newPath); err != nil {
		return fmt.Errorf("calibrate: copy %s → %s: %w", legacyPath, newPath, err)
	}

	// Write a tombstone alongside the legacy file so an operator who
	// finds /etc/ventd/calibration.json.moved-to-var-lib understands
	// what happened. Best-effort: failure here doesn't roll back the
	// successful copy.
	tombstone := legacyPath + ".moved-to-var-lib"
	tombstoneBody := fmt.Sprintf(
		"ventd v0.8.x relocated this file to %s on %s.\n"+
			"The original is left in place for one release cycle; you may delete\n"+
			"both this tombstone and %s once the daemon has booted cleanly.\n",
		newPath, time.Now().UTC().Format(time.RFC3339), legacyPath)
	if err := os.WriteFile(tombstone, []byte(tombstoneBody), 0o644); err != nil {
		logger.Warn("calibrate: tombstone write failed (migration still succeeded)",
			"tombstone", tombstone, "err", err)
	}

	logger.Info("calibrate: migrated legacy calibration.json to v0.8.x location",
		"from", legacyPath, "to", newPath)
	return nil
}

// copyFileAtomic copies src to dst via a temp file + rename. The temp
// file is removed on any error so a partial migration never leaves
// garbage in /var/lib/ventd/setup/.
func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
