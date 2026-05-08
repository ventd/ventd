package iox

import (
	"errors"
	"fmt"
	"syscall"
)

// ErrInsufficientFreeSpace signals that the filesystem holding a
// write target has less free space than the caller's required
// minimum. Wrapped errors include the path, the measured free
// bytes, and the required minimum so operators reading the
// journal can correlate the refusal to a specific filesystem
// without taking a separate measurement.
//
// Per RULE-IOX-02. Callers refuse the write rather than letting
// the in-memory state mutate ahead of a doomed disk-write — the
// load-bearing semantic the senior-review's C7 finding pinned
// for KV state.
var ErrInsufficientFreeSpace = errors.New("iox: insufficient free space")

// MinFreeBytesForState is the default minimum free space ventd
// requires before a state-class write proceeds. 1 MiB is tight
// enough that healthy systems essentially never see refusals,
// large enough to leave headroom for the tempfile + final +
// dir-fsync sequence even if the marshalled payload grows by an
// order of magnitude relative to what the daemon writes today
// (state.yaml is typically <100 KiB, blobs are CBOR-framed and
// size-bounded by their schemas). Per RULE-IOX-02.
const MinFreeBytesForState uint64 = 1 << 20 // 1 MiB

// EnsureFreeSpace returns nil when the filesystem holding `path`
// has at least `minBytes` available; otherwise returns an error
// wrapping ErrInsufficientFreeSpace whose message names the path,
// the measured free bytes, and the required minimum.
//
// `path` may be either an existing directory or a regular file —
// statfs(2) walks to the containing filesystem either way. A
// path that doesn't exist surfaces the underlying statfs error
// (typically wrapped ENOENT) WITHOUT wrapping
// ErrInsufficientFreeSpace, so callers can distinguish "we
// definitely don't have room" from "we couldn't measure" via
// `errors.Is(err, ErrInsufficientFreeSpace)`.
//
// `minBytes == 0` short-circuits to nil — callers that want
// to disable the gate (e.g. tests, or future operator-tunable
// override paths) pass 0 rather than a sentinel.
func EnsureFreeSpace(path string, minBytes uint64) error {
	if minBytes == 0 {
		return nil
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return fmt.Errorf("statfs %s: %w", path, err)
	}
	// Bavail is the number of free blocks available to non-root.
	// Bsize is the optimal transfer block size; on every Linux
	// filesystem in production use Bsize equals the fragment size
	// in bytes. Bavail × Bsize gives free bytes available to the
	// daemon's user (root or otherwise).
	avail := st.Bavail * uint64(st.Bsize)
	if avail < minBytes {
		return fmt.Errorf("%w: %s has %d bytes free, need %d",
			ErrInsufficientFreeSpace, path, avail, minBytes)
	}
	return nil
}
