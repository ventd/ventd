package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestVentdRecover_NoAllocs verifies that writeToFD — the hot write path that
// fires for every pwm_enable file — makes zero heap allocations. The package-
// level [2]byte array means no per-call allocation is needed.
func TestVentdRecover_NoAllocs(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "pwm1_enable")
	if err := os.WriteFile(path, []byte("2\n"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	fd, err := syscall.Open(path, syscall.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = syscall.Close(fd) }()

	allocs := testing.AllocsPerRun(200, func() {
		syscall.Seek(fd, 0, 0) //nolint:errcheck
		writeToFD(fd)
	})
	if allocs != 0 {
		t.Errorf("writeToFD allocs=%.0f want 0", allocs)
	}
}

// TestVentdRecover_BinarySize builds the binary with -ldflags="-s -w" -trimpath
// CGO_ENABLED=0 and asserts it stays under a size budget. The goal is to
// prevent import creep (adding fmt, slog, etc.) that would grow the binary
// without providing recovery value. Note: Go's runtime floor means we cannot
// reach the 8 KB aspirational target stated in the P3-RECOVER-01 spec; 4 MB
// is a practical ceiling that still guards against bloat.
func TestVentdRecover_BinarySize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary build in short mode")
	}
	// Locate the module root so "go build" resolves the package correctly.
	tmp := t.TempDir()
	out := filepath.Join(tmp, "ventd-recover")
	cmd := exec.Command("go", "build",
		"-ldflags=-s -w",
		"-trimpath",
		"-o", out,
		".",
	)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	const maxBytes = 4 * 1024 * 1024 // 4 MB
	if info.Size() > maxBytes {
		t.Errorf("binary %d bytes > %d byte limit; imports may have grown",
			info.Size(), maxBytes)
	}
}

// TestVentdRecover_IsPWMEnable covers the name-filter that guards the write loop.
func TestVentdRecover_IsPWMEnable(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"pwm1_enable", true},
		{"pwm12_enable", true},
		{"pwm_enable", false},        // missing digit
		{"pwm1_freq", false},         // wrong suffix
		{"fan1_input", false},        // different type
		{"pwm1enablex", false},       // no underscore
		{"pwmx_enable", false},       // non-digit mid
		{"pwm1_enable_extra", false}, // extra suffix
	}
	for _, tc := range cases {
		if got := isPWMEnable(tc.name); got != tc.want {
			t.Errorf("isPWMEnable(%q)=%v want %v", tc.name, got, tc.want)
		}
	}
}

// TestVentdRecover_RestoreAll verifies that restoreAll writes "1\n" to every
// pwm<N>_enable file under hwmon* directories and ignores other files.
func TestVentdRecover_RestoreAll(t *testing.T) {
	root := t.TempDir()
	hwmon0 := filepath.Join(root, "hwmon0")
	hwmon1 := filepath.Join(root, "hwmon1")
	if err := os.MkdirAll(hwmon0, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hwmon1, 0755); err != nil {
		t.Fatal(err)
	}

	// Files that should be written.
	targets := []string{
		filepath.Join(hwmon0, "pwm1_enable"),
		filepath.Join(hwmon0, "pwm2_enable"),
		filepath.Join(hwmon1, "pwm1_enable"),
	}
	// Files that must NOT be written.
	bystanders := []string{
		filepath.Join(hwmon0, "fan1_input"),
		filepath.Join(hwmon0, "temp1_input"),
		filepath.Join(root, "pwm1_enable"), // outside hwmon* dir
	}

	for _, p := range append(targets, bystanders...) {
		if err := os.WriteFile(p, []byte("2\n"), 0600); err != nil {
			t.Fatalf("setup %s: %v", p, err)
		}
	}

	restoreAll(root)

	for _, p := range targets {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if strings.TrimSpace(string(b)) != "1" {
			t.Errorf("%s: got %q want \"1\"", p, string(b))
		}
	}
	for _, p := range bystanders {
		b, _ := os.ReadFile(p)
		if strings.TrimSpace(string(b)) != "2" {
			t.Errorf("bystander %s was modified: got %q want \"2\"", p, string(b))
		}
	}
}
