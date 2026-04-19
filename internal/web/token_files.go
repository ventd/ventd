package web

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// SetupTokenRuntimePath is the tmpfs path for the active setup token.
	// Cleared on reboot; always holds the token from the most recent daemon start.
	SetupTokenRuntimePath = "/run/ventd/setup-token"

	// SetupTokenPersistPath is the persistent state-dir copy of the setup token.
	// Survives daemon restarts and is the fallback when /run has been cleared
	// (e.g. after a reboot with first-boot still pending).
	SetupTokenPersistPath = "/var/lib/ventd/setup-token"
)

// WriteSetupTokenFiles writes tok atomically to both the runtime tmpfs path and
// the persistent state path. Each file is created with mode 0640. Both writes
// are always attempted; errors from each are joined and returned together.
func WriteSetupTokenFiles(tok, runtimePath, persistPath string) error {
	return errors.Join(
		writeTokenFile(tok, runtimePath),
		writeTokenFile(tok, persistPath),
	)
}

// writeTokenFile writes tok to path atomically using a temp-file rename.
// The destination file is created with mode 0640.
func writeTokenFile(tok, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".setup-token-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o640); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if _, err := fmt.Fprintln(tmp, tok); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	cleanup = false
	return nil
}
