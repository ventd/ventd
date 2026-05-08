package iox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RULE-IOX-02 binds these subtests. EnsureFreeSpace is the helper
// every state-class write path consults before mutating in-memory
// state. The subtests cover: happy path, threshold disabled, missing
// path, and the canonical refusal path with an absurdly large
// minimum that the test environment cannot satisfy.

func TestEnsureFreeSpace_Rules(t *testing.T) {
	t.Run("RULE-IOX-02_happy_path_returns_nil", func(t *testing.T) {
		// On any filesystem with at least 1 MiB free (every CI
		// runner / dev box / HIL host in the fleet), the default
		// MinFreeBytesForState gate must admit. A subtle regression
		// where Bavail × Bsize math overflows or underflows would
		// fail this immediately because the underlying tmpdir lives
		// on a host filesystem with gigabytes free.
		dir := t.TempDir()
		if err := EnsureFreeSpace(dir, MinFreeBytesForState); err != nil {
			t.Errorf("expected nil on healthy filesystem, got %v", err)
		}
	})

	t.Run("RULE-IOX-02_zero_minimum_short_circuits", func(t *testing.T) {
		// Callers who want to disable the gate (tests, future
		// operator-tunable override that sets the threshold to 0)
		// must short-circuit BEFORE the statfs syscall fires. We
		// verify this by passing a path that doesn't exist — the
		// statfs would otherwise return ENOENT, but with min=0 the
		// gate returns nil without touching the kernel.
		err := EnsureFreeSpace("/this/path/does/not/exist/anywhere/at/all", 0)
		if err != nil {
			t.Errorf("expected nil with min=0 even on missing path, got %v", err)
		}
	})

	t.Run("RULE-IOX-02_missing_path_surfaces_underlying_error", func(t *testing.T) {
		// A path that doesn't exist surfaces the underlying statfs
		// error WITHOUT wrapping ErrInsufficientFreeSpace so callers
		// can distinguish "we couldn't measure" from "we measured
		// and it's too low" via errors.Is. This matters for the
		// doctor card: only the second case warrants a "free up
		// disk space" remediation; the first means "the path you
		// configured doesn't exist on this host".
		err := EnsureFreeSpace("/var/lib/ventd-this-does-not-exist", MinFreeBytesForState)
		if err == nil {
			t.Fatal("expected error on missing path, got nil")
		}
		if errors.Is(err, ErrInsufficientFreeSpace) {
			t.Errorf("missing path must NOT wrap ErrInsufficientFreeSpace; got %v", err)
		}
		if !strings.Contains(err.Error(), "statfs") {
			t.Errorf("expected 'statfs' in error message, got %q", err.Error())
		}
	})

	t.Run("RULE-IOX-02_huge_minimum_refuses_with_actionable_error", func(t *testing.T) {
		// An absurdly large minimum (1 EiB — larger than any
		// filesystem the test environment could possibly have)
		// must refuse with a wrapped ErrInsufficientFreeSpace
		// whose message names the path, the actual bytes free,
		// and the required minimum. The message shape is the
		// load-bearing piece for operator-facing journal entries:
		// without those three values, an operator can't tell
		// which filesystem to clear.
		dir := t.TempDir()
		const oneExbibyte uint64 = 1 << 60
		err := EnsureFreeSpace(dir, oneExbibyte)
		if err == nil {
			t.Fatal("expected refusal with absurd minimum, got nil")
		}
		if !errors.Is(err, ErrInsufficientFreeSpace) {
			t.Errorf("expected errors.Is(err, ErrInsufficientFreeSpace), got %v", err)
		}
		msg := err.Error()
		if !strings.Contains(msg, dir) {
			t.Errorf("expected path %q in error message, got %q", dir, msg)
		}
		if !strings.Contains(msg, "bytes free") {
			t.Errorf("expected 'bytes free' in error message, got %q", msg)
		}
		if !strings.Contains(msg, "need") {
			t.Errorf("expected 'need' in error message, got %q", msg)
		}
	})

	t.Run("RULE-IOX-02_works_on_file_path_not_just_dir", func(t *testing.T) {
		// statfs walks to the containing filesystem so a regular
		// file path resolves the same as its parent directory.
		// Callers that pass a state.yaml path directly must get
		// the same answer as if they'd passed filepath.Dir(path).
		dir := t.TempDir()
		filePath := filepath.Join(dir, "state.yaml")
		if err := os.WriteFile(filePath, []byte("schema_version: 1\n"), 0o600); err != nil {
			t.Fatalf("seed file: %v", err)
		}
		if err := EnsureFreeSpace(filePath, MinFreeBytesForState); err != nil {
			t.Errorf("expected nil for existing file on healthy fs, got %v", err)
		}
	})
}
