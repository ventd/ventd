// SPDX-License-Identifier: GPL-3.0-or-later
package msiec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFirmwareString(t *testing.T) {
	cases := []struct {
		name string
		fw   string
		ok   bool
	}{
		{"upstream rev", "16R8IMS1.117", true},
		{"all letters acceptable", "ABCDEFG1.100", true},
		{"too short", "1234", false},
		{"too long", strings.Repeat("A", 33), false},
		{"shell metachar", "16R8IMS1.117;rm -rf /", false},
		{"newline injection", "16R8IMS1.117\noptions evil", false},
		{"path separator", "../../etc/passwd", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFirmwareString(tc.fw)
			gotOK := err == nil
			if gotOK != tc.ok {
				t.Fatalf("ValidateFirmwareString(%q) ok=%v err=%v; want ok=%v", tc.fw, gotOK, err, tc.ok)
			}
			if !tc.ok && err != nil && !errors.Is(err, ErrUnsafeFirmwareString) {
				t.Fatalf("err=%v not errors.Is(ErrUnsafeFirmwareString)", err)
			}
		})
	}
}

func TestWriteFirmwarePin_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ventd-msiec-firmware-pin.conf")
	if err := WriteFirmwarePin(path, "16R8IMS1.117"); err != nil {
		t.Fatalf("WriteFirmwarePin: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "options msi-ec firmware=16R8IMS1.117") {
		t.Fatalf("file content missing options line; got:\n%s", got)
	}
	if !strings.Contains(got, "Managed by ventd") {
		t.Fatalf("file content missing ventd-managed marker; got:\n%s", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("perm = %o; want 0o644 (world-readable for diag-collector)", st.Mode().Perm())
	}
}

func TestWriteFirmwarePin_RefusesUnsafeStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ventd-msiec-firmware-pin.conf")
	err := WriteFirmwarePin(path, "16R8IMS1.117\noptions evil module=1")
	if !errors.Is(err, ErrUnsafeFirmwareString) {
		t.Fatalf("WriteFirmwarePin newline-injection err=%v; want ErrUnsafeFirmwareString", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatalf("WriteFirmwarePin wrote the file despite refusing the string; should be no-op on error")
	}
}

func TestRemoveFirmwarePin_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ventd-msiec-firmware-pin.conf")
	// Remove on a non-existent path must succeed.
	if err := RemoveFirmwarePin(path); err != nil {
		t.Fatalf("RemoveFirmwarePin(missing) = %v; want nil", err)
	}
	// Create + remove.
	if err := WriteFirmwarePin(path, "16R8IMS1.117"); err != nil {
		t.Fatalf("WriteFirmwarePin: %v", err)
	}
	if err := RemoveFirmwarePin(path); err != nil {
		t.Fatalf("RemoveFirmwarePin(existing) = %v; want nil", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file still exists after RemoveFirmwarePin")
	}
}
