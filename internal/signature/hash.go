// Package signature implements v0.5.6's workload signature learning
// per R7 (docs/research/r-bundle/R7-workload-signature-hash.md).
//
// The library turns a per-tick set of running processes into a
// stable, opaque, privacy-safe label. Layer B (v0.5.7), Layer C
// (v0.5.8), and the confidence-gated controller (v0.5.9) all key
// per-(channel, signature) state on these labels.
//
// Privacy contract: only /proc/PID/comm is hashed. The hash is
// SipHash-2-4 keyed by a per-install 32-byte salt at
// /var/lib/ventd/.signature_salt (mode 0600). The salt never leaves
// the host; a leaked diag bundle exposes only opaque hex tokens.
package signature

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dchest/siphash"
)

// DefaultSaltPath is the production location of the per-install
// SipHash key. The file holds 32 bytes — only the first 16 are
// used as the SipHash key; the remaining 16 are reserved for
// forward compatibility (HKDF derivation of per-channel sub-keys
// in a future spec).
const DefaultSaltPath = "/var/lib/ventd/.signature_salt"

// SaltLen is the number of random bytes stored in the salt file.
const SaltLen = 32

// SaltFileMode is the on-disk mode the salt file MUST hold to
// preserve RULE-SIG-SALT-01.
const SaltFileMode os.FileMode = 0o600

// ErrSaltFilePermissionsTooLoose is returned when the salt file
// exists with mode > 0600. Operators must chmod 600 before the
// daemon starts.
var ErrSaltFilePermissionsTooLoose = errors.New("signature: salt file permissions exceed 0600")

// Hasher computes SipHash-2-4 of a comm string under the per-
// install salt. The salt's first 16 bytes are split into two
// uint64 keys per the SipHash interface.
type Hasher struct {
	k0, k1 uint64
}

// NewHasher returns a Hasher keyed by the supplied salt. salt MUST
// be at least 16 bytes; longer slices are accepted (only the first
// 16 are used).
func NewHasher(salt []byte) (*Hasher, error) {
	if len(salt) < 16 {
		return nil, fmt.Errorf("signature: salt too short (%d bytes, need >= 16)", len(salt))
	}
	return &Hasher{
		k0: binary.LittleEndian.Uint64(salt[0:8]),
		k1: binary.LittleEndian.Uint64(salt[8:16]),
	}, nil
}

// HashComm returns the canonical 64-bit SipHash-2-4 of a comm
// string under the install salt. RULE-SIG-HASH-01.
func (h *Hasher) HashComm(comm string) uint64 {
	return siphash.Hash(h.k0, h.k1, []byte(comm))
}

// HashCommHex returns the same hash rendered as 16 lowercase hex
// characters — the canonical signature library label format.
func (h *Hasher) HashCommHex(comm string) string {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], h.HashComm(comm))
	return hex.EncodeToString(buf[:])
}

// LoadOrCreateSalt reads the salt file at path, regenerating with
// 32 fresh random bytes if absent. Returns an error if the file
// exists with mode > 0600 (RULE-SIG-SALT-01).
//
// Atomic create-tmp-rename prevents a partially-written salt file
// from being read by a racing process.
func LoadOrCreateSalt(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("signature: salt path empty")
	}

	info, err := os.Stat(path)
	switch {
	case err == nil:
		// File exists: enforce mode and return contents.
		if info.Mode().Perm()&^SaltFileMode != 0 {
			return nil, fmt.Errorf("%w: %s mode is %o, must be %o",
				ErrSaltFilePermissionsTooLoose, path, info.Mode().Perm(), SaltFileMode)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		if len(data) < SaltLen {
			return nil, fmt.Errorf("signature: salt file %s too short (%d bytes, need %d)",
				path, len(data), SaltLen)
		}
		return data, nil
	case errors.Is(err, os.ErrNotExist):
		// Generate fresh and write atomically.
		return generateAndWriteSalt(path)
	default:
		return nil, err
	}
}

// generateAndWriteSalt creates a fresh 32-byte salt, writes it
// atomically (tmpfile + rename) with mode 0600, and returns the
// bytes.
func generateAndWriteSalt(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	salt := make([]byte, SaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("signature: rand.Read: %w", err)
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, SaltFileMode)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(salt); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return salt, nil
}
