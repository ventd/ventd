package hwmon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// writeFileMode is a small helper that writes a sysfs-style file with an
// explicit permission so ModeAttrWritable's owner-write check can be
// exercised against both writable (0644) and read-only (0444) attrs.
func writeFileMode(t *testing.T, path, data string, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), perm); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
	// os.WriteFile honours umask; force the exact mode for the perm check.
	if err := os.Chmod(path, perm); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func TestReadWritePWMMode_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	pwm := filepath.Join(dir, "pwm1")
	mode := filepath.Join(dir, "pwm1_mode")
	writeFileMode(t, pwm, "128\n", 0o644)
	writeFileMode(t, mode, "1\n", 0o644) // starts in PWM mode

	got, err := ReadPWMMode(pwm)
	if err != nil {
		t.Fatalf("ReadPWMMode: %v", err)
	}
	if got != PWMModePWM {
		t.Fatalf("ReadPWMMode = %d, want %d (PWM)", got, PWMModePWM)
	}

	if err := WritePWMMode(pwm, PWMModeDC); err != nil {
		t.Fatalf("WritePWMMode: %v", err)
	}
	got, err = ReadPWMMode(pwm)
	if err != nil {
		t.Fatalf("ReadPWMMode after write: %v", err)
	}
	if got != PWMModeDC {
		t.Fatalf("after WritePWMMode(DC), ReadPWMMode = %d, want %d (DC)", got, PWMModeDC)
	}
}

func TestReadPWMMode_NotExistIsWrapped(t *testing.T) {
	dir := t.TempDir()
	pwm := filepath.Join(dir, "pwm1") // no pwm1_mode sibling (it87-style)
	writeFileMode(t, pwm, "128\n", 0o644)

	_, err := ReadPWMMode(pwm)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadPWMMode on missing mode file: err = %v, want fs.ErrNotExist", err)
	}
}

func TestWritePWMMode_NotExistIsWrapped(t *testing.T) {
	dir := t.TempDir()
	pwm := filepath.Join(dir, "pwm1")
	writeFileMode(t, pwm, "128\n", 0o644)

	err := WritePWMMode(pwm, PWMModeDC)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("WritePWMMode on missing mode file: err = %v, want fs.ErrNotExist", err)
	}
}

func TestModeAttrWritable(t *testing.T) {
	dir := t.TempDir()
	pwm := filepath.Join(dir, "pwm1")
	writeFileMode(t, pwm, "128\n", 0o644)

	// No mode file → not writable.
	if ModeAttrWritable(pwm) {
		t.Fatal("ModeAttrWritable should be false when pwm1_mode is absent")
	}

	// Create the mode file once, then transition its permission with
	// chmod (which only needs ownership, never write access) — rewriting
	// the file after chmod'ing it read-only would EACCES for a non-root
	// test user (CI lanes run unprivileged; only root's CAP_DAC_OVERRIDE
	// could rewrite a 0444 file).
	mode := filepath.Join(dir, "pwm1_mode")
	writeFileMode(t, mode, "1\n", 0o644)

	// Writable mode file (0644) → writable (nct6775 family).
	if !ModeAttrWritable(pwm) {
		t.Fatal("ModeAttrWritable should be true for a 0644 mode attr")
	}

	// Read-only mode file (0444) → not writable (it87 exposes none, but
	// some drivers expose a read-only mode attr; both must be excluded).
	if err := os.Chmod(mode, 0o444); err != nil {
		t.Fatalf("chmod 0444: %v", err)
	}
	if ModeAttrWritable(pwm) {
		t.Fatal("ModeAttrWritable should be false for a 0444 (read-only) mode attr")
	}

	// Back to writable via chmod.
	if err := os.Chmod(mode, 0o644); err != nil {
		t.Fatalf("chmod 0644: %v", err)
	}
	if !ModeAttrWritable(pwm) {
		t.Fatal("ModeAttrWritable should be true after chmod back to 0644")
	}
}
