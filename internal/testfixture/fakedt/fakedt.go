// Package fakedt stubs /proc/device-tree/compatible for unit tests that need
// to exercise Apple Silicon detection without real hardware.
package fakedt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fake provides a temporary directory tree that mimics the /proc/device-tree
// layout used by the Asahi backend for Apple Silicon detection.
type Fake struct {
	// Root is the tempdir that stands in for /proc. Pass Root+"/device-tree/compatible"
	// (i.e. CompatPath()) to asahi.Backend as the DT override.
	Root string
}

// New creates a fake device-tree tree under a t.TempDir().
//
// entries is the list of DT compatible strings that will be written to the
// compatible file as a NUL-separated, NUL-terminated byte sequence — the
// exact encoding the kernel uses.  Pass nil to simulate an absent
// device-tree (non-ARM / non-DT machine).
func New(t *testing.T, entries []string) *Fake {
	t.Helper()
	root := t.TempDir()
	if entries == nil {
		return &Fake{Root: root}
	}
	dtDir := filepath.Join(root, "device-tree")
	if err := os.MkdirAll(dtDir, 0755); err != nil {
		t.Fatalf("fakedt: mkdir %s: %v", dtDir, err)
	}
	content := strings.Join(entries, "\x00") + "\x00"
	if err := os.WriteFile(filepath.Join(dtDir, "compatible"), []byte(content), 0644); err != nil {
		t.Fatalf("fakedt: write compatible: %v", err)
	}
	return &Fake{Root: root}
}

// CompatPath returns the path to the compatible file that the asahi backend
// should read.  Wire it as the dtPath override on the backend under test.
func (f *Fake) CompatPath() string {
	return filepath.Join(f.Root, "device-tree", "compatible")
}
