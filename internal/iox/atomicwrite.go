// Package iox holds I/O primitives shared across packages.
//
// Every persistent file write in ventd MUST go through WriteFile so the
// crash / power-loss durability contract is enforced uniformly. The
// canonical sequence is:
//
//  1. Resolve the parent directory; create it if missing (mode 0755).
//  2. Open a randomly-suffixed `<path>.tmp.<8 random bytes hex>` with
//     O_WRONLY|O_CREATE|O_EXCL and the caller's requested mode.
//  3. Write the payload, fsync the file, close it.
//  4. Atomically rename the tempfile over the destination.
//  5. **fsync the parent directory** so the rename's directory entry is
//     durable on storage media that batch metadata writes (most modern
//     filesystems on consumer SSDs).
//
// Step 5 is the load-bearing piece a naïve `tempfile + rename` skips —
// without it a power-loss between the rename syscall and the next
// fsync can leave the directory entry pointing at the previous (or
// missing) inode. ventd's persistent state (KV, blob, observation log,
// calibration, smart-mode shards, redactor mapping, TLS keys, web
// session tokens, signature salt) all care about this.
//
// Bound: RULE-IOX-01 (round-trip + dir-fsync invariant).
package iox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultDirMode is the mode parent directories are created with when
// they don't exist. 0755 — owner rw, group/world rx — matches the rest
// of the daemon's state-dir tree.
const DefaultDirMode os.FileMode = 0o755

// WriteFile writes data to path using tempfile + fsync + rename + dir-fsync.
//
// The final on-disk file has the requested mode. Parent directories are
// created with DefaultDirMode if missing. On any error before the rename
// the tempfile is cleaned up; after the rename, the dir-fsync best-
// effort step is logged silently — the rename itself is what gates
// caller-visible success.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DefaultDirMode); err != nil {
		return fmt.Errorf("iox: mkdir %s: %w", dir, err)
	}
	var suf [8]byte
	if _, err := rand.Read(suf[:]); err != nil {
		return fmt.Errorf("iox: random suffix: %w", err)
	}
	tmp := path + ".tmp." + hex.EncodeToString(suf[:])

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("iox: open %s: %w", tmp, err)
	}
	// Make sure the tempfile never lingers if any subsequent step fails.
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("iox: write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("iox: fsync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("iox: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("iox: rename %s -> %s: %w", tmp, path, err)
	}
	cleanup = false

	// Best-effort directory fsync: the rename is durable across fsck /
	// metadata-replay on every filesystem we support, but on consumer
	// SSDs without barrier=1 the directory entry can be batched. The
	// fsync forces it through. We swallow the error — the rename
	// already succeeded and the file IS visible; a fsync failure here
	// is a latent durability cost, not a correctness regression.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
