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
// fires for every pwm_enable file — makes zero heap allocations on the happy
// path. The package-level byte arrays and the func-value syscall seams mean no
// per-call allocation is needed.
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

// TestVentdRecover_RestoreAll verifies that restoreAll hands every
// pwm<N>_enable file under hwmon* directories back to firmware (writes the
// automatic value "2") and ignores other files. Targets start at a manual-ish
// "5" so the write is observable; bystanders start at "5" and must stay "5".
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

	// Files that should be handed back to firmware (5 → 2).
	targets := []string{
		filepath.Join(hwmon0, "pwm1_enable"),
		filepath.Join(hwmon0, "pwm2_enable"),
		filepath.Join(hwmon1, "pwm1_enable"),
	}
	// Files that must NOT be touched (stay 5).
	bystanders := []string{
		filepath.Join(hwmon0, "fan1_input"),
		filepath.Join(hwmon0, "temp1_input"),
		filepath.Join(root, "pwm1_enable"), // outside hwmon* dir
	}

	for _, p := range append(targets, bystanders...) {
		if err := os.WriteFile(p, []byte("5\n"), 0600); err != nil {
			t.Fatalf("setup %s: %v", p, err)
		}
	}

	restoreAll(root)

	for _, p := range targets {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if strings.TrimSpace(string(b)) != "2" {
			t.Errorf("%s: got %q want \"2\" (firmware automatic)", p, string(b))
		}
	}
	for _, p := range bystanders {
		b, _ := os.ReadFile(p)
		if strings.TrimSpace(string(b)) != "5" {
			t.Errorf("bystander %s was modified: got %q want \"5\"", p, string(b))
		}
	}
}

// TestRestoreAll_NeverWritesManualMode is the regression guard for #1434
// (RULE-WD-RECOVER-HANDBACK): the crash-recovery path must hand fans back to
// firmware automatic mode (2), never write pwm_enable=1 (manual), which on
// most super-I/O chips pins the fan at the dead daemon's last PWM.
func TestRestoreAll_NeverWritesManualMode(t *testing.T) {
	root := t.TempDir()
	hwmon0 := filepath.Join(root, "hwmon0")
	if err := os.MkdirAll(hwmon0, 0o755); err != nil {
		t.Fatal(err)
	}
	// "5" stands in for any manual-ish residual a crashed daemon left behind.
	targets := []string{
		filepath.Join(hwmon0, "pwm1_enable"),
		filepath.Join(hwmon0, "pwm2_enable"),
	}
	for _, p := range targets {
		if err := os.WriteFile(p, []byte("5\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	restoreAll(root)

	for _, p := range targets {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		got := strings.TrimSpace(string(b))
		if got == "1" {
			t.Errorf("%s: recovery wrote MANUAL mode (1) — pins the fan; must hand back to firmware (#1434)", p)
		}
		if got != "2" {
			t.Errorf("%s: got %q, want \"2\" (firmware automatic)", p, got)
		}
	}
}

// TestWriteToFD_EINVALWalksToNextValue verifies the fallback walk: when the
// chip rejects "2" and "99" with EINVAL, writeToFD advances to "0". The syscall
// seams are injected so the rejection is deterministic (a real tmpfile accepts
// any value).
func TestWriteToFD_EINVALWalksToNextValue(t *testing.T) {
	origWrite, origSeek := sysWrite, sysSeek
	defer func() { sysWrite, sysSeek = origWrite, origSeek }()

	var written []string
	sysSeek = func(int, int64, int) (int64, error) { return 0, nil }
	sysWrite = func(_ int, p []byte) (int, error) {
		written = append(written, strings.TrimSpace(string(p)))
		if string(p) == "0\n" { // only the last candidate is accepted
			return len(p), nil
		}
		return 0, syscall.EINVAL
	}

	writeToFD(7) // fd is irrelevant; the seams ignore it

	if got := strings.Join(written, ","); got != "2,99,0" {
		t.Fatalf("write sequence = %q, want \"2,99,0\" (must walk 2 → 99 → 0 on EINVAL)", got)
	}
}

// TestWriteToFD_HardErrorAborts verifies that a non-EINVAL error (e.g. EACCES /
// device gone) aborts the walk immediately — trying other values cannot help
// if the file itself is unwritable.
func TestWriteToFD_HardErrorAborts(t *testing.T) {
	origWrite, origSeek := sysWrite, sysSeek
	defer func() { sysWrite, sysSeek = origWrite, origSeek }()

	attempts := 0
	sysSeek = func(int, int64, int) (int64, error) { return 0, nil }
	sysWrite = func(int, []byte) (int, error) {
		attempts++
		return 0, syscall.EACCES
	}

	writeToFD(7)

	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (a non-EINVAL error must abort the walk)", attempts)
	}
}

// TestEnableHandbackSequence_NoManualMode locks the safety invariant: the
// recovery sequence is {2, 99, 0} and never the manual value 1 (#1434).
func TestEnableHandbackSequence_NoManualMode(t *testing.T) {
	want := []string{"2\n", "99\n", "0\n"}
	if len(enableHandbackSequence) != len(want) {
		t.Fatalf("sequence length = %d, want %d", len(enableHandbackSequence), len(want))
	}
	for i, b := range enableHandbackSequence {
		if string(b) != want[i] {
			t.Errorf("sequence[%d] = %q, want %q", i, string(b), want[i])
		}
		if strings.TrimSpace(string(b)) == "1" {
			t.Errorf("sequence[%d] is the MANUAL value 1 — must never be written on recovery (#1434)", i)
		}
	}
}
