package iox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFile_RoundTrip writes a payload through WriteFile and reads
// it back. Asserts the payload survives byte-equal and the on-disk
// mode matches the requested mode.
//
// Bound: RULE-IOX-01.
func TestWriteFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kv.yaml")
	want := []byte("schema_version: 1\nfoo: bar\n")

	if err := WriteFile(path, want, 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("payload = %q, want %q", got, want)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0o640", st.Mode().Perm())
	}
}

// TestWriteFile_NoTempLeak ensures the tempfile is removed on
// success — the parent directory contains exactly one file (the
// destination), not the tempfile alongside it. A leaked tempfile
// would accumulate on every write, eating disk over time.
//
// Bound: RULE-IOX-01.
func TestWriteFile_NoTempLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shard.cbor")

	if err := WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Errorf("tempfile leaked: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1 (just the destination)", len(entries))
	}
}

// TestWriteFile_OverwritesExisting confirms a second WriteFile to the
// same path replaces the previous content (the rename is the
// atomically-overwrite primitive on POSIX).
//
// Bound: RULE-IOX-01.
func TestWriteFile_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "redactor-mapping.json")

	if err := WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("first WriteFile: %v", err)
	}
	if err := WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("second WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("payload = %q, want \"second\"", got)
	}
}

// TestWriteFile_CreatesParentDir asserts the helper creates missing
// parent directories with DefaultDirMode rather than failing with
// ENOENT.
//
// Bound: RULE-IOX-01.
func TestWriteFile_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "config.yaml")

	if err := WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("destination missing: %v", err)
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || !parent.IsDir() {
		t.Errorf("parent dir not created: %v", err)
	}
}
