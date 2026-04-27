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
)

const (
	// DefaultDir is the production state directory.
	DefaultDir = "/var/lib/ventd"
	dirMode    = 0o755
	fileMode   = 0o640
)

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
	// fsync the directory so the rename is durable on power loss.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
