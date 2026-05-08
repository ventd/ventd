package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	currentVersion  = 2
	versionFileName = "version"
)

// ErrDowngrade is returned when the on-disk state was written by a newer binary.
var ErrDowngrade = errors.New("state: on-disk version is newer than this binary (downgrade detected)")

// MigrateFn migrates state from one schema version to the next.
type MigrateFn func(dir string) error

// migrations maps (from, to) version pairs to their migration function.
//
// The v1→v2 entry is a registered no-op. Both versions share the same
// on-disk schema; the bump exists to reserve the v2 slot for the
// v0.6.0 broker-namespace migration (and any other v0.6 breaking
// shape change) without triggering RULE-STATE-05's "treat as missing"
// path. A registered no-op is structurally distinct from a missing
// migrator: missing causes the upgrade loop to break out and the
// caller's state is effectively wiped on next access. Registered no-op
// keeps existing calibration / polarity / smart-mode shards intact
// across the version bump while exercising the migration mechanism
// end-to-end so any future real migration drops in cleanly.
var migrations = map[[2]int]MigrateFn{
	{1, 2}: noopV1ToV2,
}

// noopV1ToV2 is the no-op v1→v2 migration. See the doc comment on
// `migrations` above for the rationale. Calling this MUST NOT touch
// any file in the state directory; the version sentinel is bumped by
// CheckVersion's writeVersion call after every migrator returns.
func noopV1ToV2(dir string) error { return nil }

// RegisterMigration registers a migration function for the (from, to) pair.
// Called at init time by future packages that introduce schema changes.
func RegisterMigration(from, to int, fn MigrateFn) {
	migrations[[2]int{from, to}] = fn
}

// CheckVersion reads the version sentinel at dir/version and validates
// compatibility (RULE-STATE-05):
//   - Missing file: write currentVersion, return nil (first run).
//   - on-disk == currentVersion: return nil.
//   - on-disk > currentVersion: return ErrDowngrade (downgrade refused).
//   - on-disk < currentVersion: apply registered migrations; if none registered,
//     the state is treated as effectively missing and callers re-initialise on
//     first access. Returns nil.
func CheckVersion(dir string) error {
	path := filepath.Join(dir, versionFileName)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return writeVersion(path, currentVersion)
	}
	if err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return fmt.Errorf("parse version %q: %w", strings.TrimSpace(string(raw)), err)
	}

	if v > currentVersion {
		return fmt.Errorf("%w: on-disk version %d > binary version %d; "+
			"reinstall newer ventd or run 'ventd state reset'",
			ErrDowngrade, v, currentVersion)
	}
	if v < currentVersion {
		// Apply sequential migrations from v to currentVersion.
		for from := v; from < currentVersion; from++ {
			to := from + 1
			fn, ok := migrations[[2]int{from, to}]
			if !ok {
				// No migration registered — consumers treat their state as missing
				// and re-initialise. This is correct for additive-only changes.
				break
			}
			if err := fn(dir); err != nil {
				return fmt.Errorf("migrate %d→%d: %w", from, to, err)
			}
		}
		return writeVersion(path, currentVersion)
	}
	return nil
}

func writeVersion(path string, v int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(v)+"\n"), fileMode)
}
