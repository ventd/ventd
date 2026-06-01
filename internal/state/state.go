// Package state implements the three persistent-storage primitives for ventd:
// a YAML key-value store (KV), a binary blob store (Blob), and an append-only
// log store (Log). All three are backed by plain files under /var/lib/ventd/.
// See docs/architecture/persistent-state.md and spec-16-persistent-state.md.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultDir is the production state directory.
	DefaultDir = "/var/lib/ventd"
	dirMode    = 0o755
	fileMode   = 0o640
)

// DirOverrideEnv redirects the state directory away from the production
// DefaultDir to an alternate on-disk path. Empty/unset means production. This
// is a dev/test seam — primarily so a second daemon can run against a synthetic
// hwmon tree (VENTD_HWMON_ROOT, tools/hwmonsim) without colliding on the
// production pidfile, KV, blob, and log stores — NOT a production knob. In a
// real deployment the systemd unit's ReadWritePaths / AppArmor profile confine
// writes to DefaultDir, so an override there is blocked by the sandbox; setting
// it requires the same privilege as editing the unit. Mirrors
// hwmon.RootOverrideEnv (VENTD_HWMON_ROOT).
const DirOverrideEnv = "VENTD_STATE_DIR"

// EffectiveDir returns the state directory to use: the trimmed value of the
// VENTD_STATE_DIR override when set, else DefaultDir. All daemon entry points
// resolve the state dir through this so a single override redirects the pidfile
// and every store consistently.
func EffectiveDir() string {
	if d := strings.TrimSpace(os.Getenv(DirOverrideEnv)); d != "" {
		return d
	}
	return DefaultDir
}

// DirIsOverridden reports whether VENTD_STATE_DIR is redirecting state away from
// the production DefaultDir. Callers log a loud warning so a stray override in
// production can't silently fragment state.
func DirIsOverridden() bool {
	return EffectiveDir() != DefaultDir
}

// State bundles the three storage primitives.
type State struct {
	Dir  string
	KV   *KVDB
	Blob *BlobDB
	Log  *LogDB
}

// Open initialises the state directory and opens all three stores.
// The directory hierarchy is created if absent (RULE-STATE-10).
// CheckVersion is called to reject downgrade and apply registered migrations.
func Open(dir string, logger *slog.Logger) (*State, error) {
	if dir == "" {
		dir = DefaultDir
	}
	if err := initDirs(dir); err != nil {
		return nil, fmt.Errorf("state: init dirs: %w", err)
	}
	if err := CheckVersion(dir); err != nil {
		return nil, fmt.Errorf("state: version check: %w", err)
	}
	kv, err := openKV(filepath.Join(dir, "state.yaml"), logger)
	if err != nil {
		return nil, fmt.Errorf("state: open kv: %w", err)
	}
	blob := newBlobDB(filepath.Join(dir, "models"), logger)
	log := newLogDB(filepath.Join(dir, "logs"), logger)
	return &State{Dir: dir, KV: kv, Blob: blob, Log: log}, nil
}

// Close releases resources held by the log store.
func (s *State) Close() error {
	return s.Log.closeAll()
}

// SchemaVersionLoaded reports whether the KV store opened cleanly with an
// acceptable schema version (spec-v0_5_9 §2.5 w_pred_system gate term).
// nil-safe at both the State and KVDB level.
func (s *State) SchemaVersionLoaded() bool {
	if s == nil {
		return false
	}
	return s.KV.SchemaVersionLoaded()
}

func initDirs(base string) error {
	for _, sub := range []string{
		base,
		filepath.Join(base, "models"),
		filepath.Join(base, "logs"),
	} {
		if err := os.MkdirAll(sub, dirMode); err != nil {
			return fmt.Errorf("mkdir %s: %w", sub, err)
		}
	}
	return nil
}

// atomicWrite writes data to path using tmpfile+rename+fsync (RULE-STATE-01).
// The rename is atomic on POSIX filesystems; either the old file is visible
// or the new one is — never a partial write.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	var suf [8]byte
	if _, err := rand.Read(suf[:]); err != nil {
		return fmt.Errorf("random suffix: %w", err)
	}
	tmp := path + ".tmp." + hex.EncodeToString(suf[:])
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmp, err)
	}
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s→%s: %w", tmp, path, err)
	}
	// Apply mode explicitly after the rename. OpenFile honours the
	// process umask, and the shipped systemd unit sets UMask=0077,
	// which would turn the requested 0640 into 0600 — tripping
	// RULE-STATE-09's "0640 ventd ventd" requirement and producing
	// a permanent warning on /api/v1/doctor every install. Chmod
	// makes the mode parameter authoritative regardless of umask.
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	// fsync the directory so the rename is durable on power loss.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
