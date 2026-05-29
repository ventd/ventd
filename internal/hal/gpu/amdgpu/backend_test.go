package amdgpu

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hal"
)

// fakeAMDCard builds a minimal amdgpu sysfs tree under sysRoot and returns the
// card path. hasFanCurve adds the RDNA3+ gpu_od/fan_ctrl/fan_curve node.
func fakeAMDCard(t *testing.T, sysRoot string, hasFanCurve bool) string {
	t.Helper()
	cardPath := filepath.Join(sysRoot, "class", "drm", "card0")
	hwmon := filepath.Join(cardPath, "device", "hwmon", "hwmon0")
	if err := os.MkdirAll(hwmon, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, v string) {
		if err := os.WriteFile(p, []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(hwmon, "name"), "amdgpu")
	write(filepath.Join(hwmon, "fan1_input"), "1500")
	write(filepath.Join(hwmon, "pwm1"), "0")
	write(filepath.Join(hwmon, "pwm1_enable"), "2")
	if hasFanCurve {
		fc := filepath.Join(cardPath, "device", "gpu_od", "fan_ctrl")
		if err := os.MkdirAll(fc, 0o755); err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(fc, "fan_curve"), "OD_FAN_CURVE:")
	}
	return cardPath
}

func readTrim(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// TestBackend_RDNA12WithOverdrive_FullControl is the end-to-end happy path: a
// RDNA1/2 card with amd_overdrive enabled is enumerated with CapWritePWM and
// Read/Write/Restore drive the real (fake) sysfs files.
func TestBackend_RDNA12WithOverdrive_FullControl(t *testing.T) {
	sysRoot := t.TempDir()
	cardPath := fakeAMDCard(t, sysRoot, false)
	hwmon := filepath.Join(cardPath, "device", "hwmon", "hwmon0")

	b := NewBackend(slog.Default(), sysRoot, true)
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if len(chs) != 1 {
		t.Fatalf("enumerate: got %d channels, want 1", len(chs))
	}
	ch := chs[0]
	if ch.ID != cardPath {
		t.Errorf("channel ID = %q, want %q", ch.ID, cardPath)
	}
	if ch.Caps&hal.CapWritePWM == 0 {
		t.Error("RDNA1/2 + amd_overdrive must advertise CapWritePWM")
	}
	if ch.Caps&(hal.CapRead|hal.CapRestore) != (hal.CapRead | hal.CapRestore) {
		t.Error("channel must advertise CapRead|CapRestore")
	}

	rd, err := b.Read(ch)
	if err != nil || !rd.OK || rd.RPM != 1500 {
		t.Errorf("read = %+v err=%v, want RPM=1500 OK=true", rd, err)
	}

	if err := b.Write(ch, 128); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readTrim(t, filepath.Join(hwmon, "pwm1")); got != "128" {
		t.Errorf("pwm1 after write = %q, want 128", got)
	}
	if got := readTrim(t, filepath.Join(hwmon, "pwm1_enable")); got != "1" {
		t.Errorf("pwm1_enable after write = %q, want 1 (manual)", got)
	}

	if err := b.Restore(ch); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := readTrim(t, filepath.Join(hwmon, "pwm1_enable")); got != "2" {
		t.Errorf("pwm1_enable after restore = %q, want 2 (firmware auto)", got)
	}
}

// TestBackend_MonitorOnlyWithoutOverdrive: without amd_overdrive the card is
// monitor-only (no CapWritePWM) and a Write is refused by the overdrive gate.
func TestBackend_MonitorOnlyWithoutOverdrive(t *testing.T) {
	sysRoot := t.TempDir()
	fakeAMDCard(t, sysRoot, false)

	b := NewBackend(slog.Default(), sysRoot, false)
	chs, _ := b.Enumerate(context.Background())
	if len(chs) != 1 {
		t.Fatalf("enumerate: got %d, want 1", len(chs))
	}
	if chs[0].Caps&hal.CapWritePWM != 0 {
		t.Error("without amd_overdrive the channel must NOT advertise CapWritePWM (monitor-only)")
	}
	if err := b.Write(chs[0], 128); err == nil {
		t.Error("write without amd_overdrive must be refused")
	}
}

// TestBackend_MonitorOnlyRDNA3: an RDNA3+ card (fan_curve interface) is
// monitor-only via the per-tick PWM path even with amd_overdrive, because
// per-tick duty control needs the fan_curve model (a follow-up).
func TestBackend_MonitorOnlyRDNA3(t *testing.T) {
	sysRoot := t.TempDir()
	fakeAMDCard(t, sysRoot, true)

	b := NewBackend(slog.Default(), sysRoot, true)
	chs, _ := b.Enumerate(context.Background())
	if len(chs) != 1 {
		t.Fatalf("enumerate: got %d, want 1", len(chs))
	}
	if chs[0].Caps&hal.CapWritePWM != 0 {
		t.Error("RDNA3+ (fan_curve) must NOT advertise CapWritePWM on the per-tick path")
	}
	if chs[0].Caps&hal.CapRestore == 0 {
		t.Error("RDNA3+ must still advertise CapRestore (fan_curve reset)")
	}
}

// TestBackend_NoCardsIsEmptyNotError: a host with no AMD GPU enumerates empty
// without erroring — the daemon must keep running.
func TestBackend_NoCardsIsEmptyNotError(t *testing.T) {
	b := NewBackend(slog.Default(), t.TempDir(), true)
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Errorf("enumerate on AMD-less host: err=%v, want nil", err)
	}
	if len(chs) != 0 {
		t.Errorf("enumerate on AMD-less host: got %d channels, want 0", len(chs))
	}
}

func TestBackend_Name(t *testing.T) {
	if got := NewBackend(slog.Default(), "/sys", false).Name(); got != BackendName {
		t.Errorf("Name() = %q, want %q", got, BackendName)
	}
}
