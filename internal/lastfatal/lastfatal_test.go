package lastfatal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrite_PersistsOneLineWithVersionAndError(t *testing.T) {
	dir := t.TempDir()
	Write(dir, "0.7.3", errors.New("load config /etc/ventd/config.yaml: bad field"))
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	line := string(data)
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("sentinel missing trailing newline: %q", line)
	}
	if !strings.Contains(line, "ventd-v0.7.3:") {
		t.Errorf("sentinel missing version segment: %q", line)
	}
	if !strings.Contains(line, "load config /etc/ventd/config.yaml: bad field") {
		t.Errorf("sentinel missing wrapped error: %q", line)
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("want exactly one trailing newline; got %d:\n%q", strings.Count(line, "\n"), line)
	}
}

func TestWrite_MkdirsParentWhenAbsent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "missing", "subdir")
	Write(dir, "0.0.1", errors.New("startup boom"))
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("sentinel under freshly-created dir: %v", err)
	}
}

func TestWrite_NilErrorIsNoOp(t *testing.T) {
	dir := t.TempDir()
	Write(dir, "0.0.1", nil)
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Errorf("Write(nil) wrote a sentinel: stat err=%v", err)
	}
}

func TestClear_RemovesSentinel(t *testing.T) {
	dir := t.TempDir()
	Write(dir, "0.0.1", errors.New("boom"))
	Clear(dir)
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Errorf("Clear left sentinel in place: stat err=%v", err)
	}
}

func TestClear_NoSentinelIsNoOp(t *testing.T) {
	dir := t.TempDir()
	Clear(dir) // must not panic and must not error
}

func TestRead_RoundTripsWriteOutput(t *testing.T) {
	dir := t.TempDir()
	Write(dir, "0.7.5", errors.New("config parse failed"))
	got := Read(dir)
	if got == "" {
		t.Fatal("Read returned empty after Write")
	}
	if !strings.Contains(got, "config parse failed") {
		t.Errorf("Read output missing wrapped error: %q", got)
	}
}

func TestRead_MissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if got := Read(dir); got != "" {
		t.Errorf("Read on empty dir: got %q, want \"\"", got)
	}
}

func TestRead_EmptyDirArgReturnsEmpty(t *testing.T) {
	if got := Read(""); got != "" {
		t.Errorf("Read(\"\"): got %q, want \"\"", got)
	}
}
