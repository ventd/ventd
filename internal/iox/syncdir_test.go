package iox

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSyncDir_HappyPath verifies SyncDir returns nil on a valid directory.
// fsync semantics aren't observable from userspace; the test pins that the
// helper invokes Open + Sync + Close without error on a real tempdir.
func TestSyncDir_HappyPath(t *testing.T) {
	dir := t.TempDir()
	// Create a file in the dir so there's metadata to flush.
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := SyncDir(dir); err != nil {
		t.Fatalf("SyncDir(%s): %v", dir, err)
	}
}

// TestSyncDir_MissingDirReturnsError verifies that SyncDir surfaces the
// os.Open error on a missing path rather than silently returning nil.
func TestSyncDir_MissingDirReturnsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if err := SyncDir(dir); err == nil {
		t.Fatal("SyncDir on missing path: expected error, got nil")
	}
}
