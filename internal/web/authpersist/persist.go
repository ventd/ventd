// Package authpersist handles durable storage of admin credentials in
// /etc/ventd/auth.json, separate from the fan-control config.yaml so that
// no config-write path can accidentally overwrite or discard the admin hash.
package authpersist

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const fileVersion = 1

// Auth is the on-disk representation of /etc/ventd/auth.json.
type Auth struct {
	Version int        `json:"version"`
	Admin   AdminCreds `json:"admin"`
}

// AdminCreds holds the admin user's persisted authentication data.
type AdminCreds struct {
	Username   string    `json:"username"`
	BcryptHash string    `json:"bcrypt_hash"`
	CreatedAt  time.Time `json:"created_at"`
}

// DefaultPath returns the canonical auth.json path for a given config directory.
func DefaultPath(configDir string) string {
	return filepath.Join(configDir, "auth.json")
}

// Load reads auth.json from path.
// Returns (nil, nil) when the file does not exist.
// Returns a non-nil error when the file exists but is unreadable or malformed.
func Load(path string) (*Auth, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read auth: %w", err)
	}
	var a Auth
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("parse auth: %w", err)
	}
	if a.Admin.BcryptHash == "" {
		return nil, fmt.Errorf("auth.json: admin bcrypt_hash is empty")
	}
	return &a, nil
}

// Save writes a to path atomically (tmp write → fsync → rename) and backs up
// the existing file to path+".bak" before overwriting. The post-write
// integrity check re-reads the file; if it fails the backup is restored and
// an error is returned so callers can surface the failure without silently
// losing credentials.
//
// File permissions are 0640. When running as root the file's owner/group is
// matched to the parent directory so the daemon user can read it after a
// root-invoked save.
func Save(path string, a *Auth) error {
	a.Version = fileVersion
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create auth dir: %w", err)
	}

	// Backup the existing file before we touch it. Best-effort: proceed
	// even if the copy fails (the tmp+rename below is atomic, so the
	// original is not lost unless the rename succeeds).
	bak := path + ".bak"
	if _, statErr := os.Stat(path); statErr == nil {
		if src, readErr := os.ReadFile(path); readErr == nil {
			_ = os.WriteFile(bak, src, 0o640)
		}
	}

	// Atomic write: open tmp → write → fsync → [chown] → rename.
	// Unique suffix prevents concurrent callers from truncating each other's
	// in-flight tmp files; O_EXCL makes collisions fail loudly (1/2^64 chance).
	var suf [8]byte
	if _, err := rand.Read(suf[:]); err != nil {
		return fmt.Errorf("random suffix: %w", err)
	}
	tmp := path + ".tmp." + hex.EncodeToString(suf[:])

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return fmt.Errorf("write auth %s: %w", tmp, err)
	}
	// Always remove tmp on exit; no-op after successful rename.
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write auth %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync auth %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close auth %s: %w", tmp, err)
	}

	// When invoked as root, match owner/group to the parent directory so
	// the daemon's non-root user can read its own credentials.
	if os.Geteuid() == 0 {
		if info, statErr := os.Stat(filepath.Dir(path)); statErr == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				_ = os.Chown(tmp, int(st.Uid), int(st.Gid))
			}
		}
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename auth: %w", err)
	}

	// Post-write integrity check: re-read and parse to confirm the file
	// is readable. If it fails, restore the backup and return an error —
	// a corrupt auth.json locks everyone out on next restart.
	if _, checkErr := Load(path); checkErr != nil {
		if _, bakExists := os.Stat(bak); bakExists == nil {
			_ = os.Rename(bak, path)
		}
		return fmt.Errorf("auth write integrity check failed: %w", checkErr)
	}

	// Best-effort directory fsync for durability on power loss.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
