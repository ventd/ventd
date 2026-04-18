package asahi_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/asahi"
	"github.com/ventd/ventd/internal/testfixture/fakedt"
	"github.com/ventd/ventd/internal/testfixture/fakehwmon"
)

// newBackend wires a testable backend to the given DT compatible path and
// hwmon sysfs root via the test-only constructor.
func newBackend(t *testing.T, dtPath, sysRoot string) *asahi.Backend {
	t.Helper()
	return asahi.NewBackendForTest(nil, dtPath, sysRoot)
}

// --- Detection tests -------------------------------------------------------

func TestEnumerate_NonAppleDT_EmptyResult(t *testing.T) {
	// Raspberry Pi DT — no apple,tX entries.
	dt := fakedt.New(t, []string{"arm,cortex-a53", "brcm,bcm2837"})
	hw := fakehwmon.New(t, &fakehwmon.Options{
		Chips: []fakehwmon.ChipOptions{
			{Name: "macsmc_hwmon", PWMs: []fakehwmon.PWMOptions{{Index: 1, PWM: 128, Enable: 2}}},
		},
	})

	chs, err := newBackend(t, dt.CompatPath(), hw.Root).Enumerate(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 0 {
		t.Fatalf("expected 0 channels for non-Apple DT, got %d", len(chs))
	}
}

func TestEnumerate_AbsentDT_EmptyResult(t *testing.T) {
	// nil entries → compatible file not created (x86 machine, no device-tree).
	dt := fakedt.New(t, nil)
	hw := fakehwmon.New(t, &fakehwmon.Options{
		Chips: []fakehwmon.ChipOptions{
			{Name: "macsmc_hwmon", PWMs: []fakehwmon.PWMOptions{{Index: 1, PWM: 128, Enable: 2}}},
		},
	})

	chs, err := newBackend(t, dt.CompatPath(), hw.Root).Enumerate(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 0 {
		t.Fatalf("expected 0 channels for absent DT, got %d", len(chs))
	}
}

func TestEnumerate_AppleDT_NoMacsmc_EmptyResult(t *testing.T) {
	// Apple Silicon DT present, but driver is not loaded.
	dt := fakedt.New(t, []string{"apple,j274", "apple,t8103"}) // MacBook Air M1
	hw := fakehwmon.New(t, &fakehwmon.Options{
		Chips: []fakehwmon.ChipOptions{
			// Different driver, not macsmc_hwmon.
			{Name: "it8686", PWMs: []fakehwmon.PWMOptions{{Index: 1, PWM: 200, Enable: 2}}},
		},
	})

	chs, err := newBackend(t, dt.CompatPath(), hw.Root).Enumerate(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 0 {
		t.Fatalf("expected 0 channels when macsmc_hwmon not loaded, got %d", len(chs))
	}
}

// --- Role classification ---------------------------------------------------

func TestEnumerate_RoleClassification(t *testing.T) {
	// MacBook Pro 14 M1 Pro — two generic fans, one GPU fan, one pump.
	dt := fakedt.New(t, []string{"apple,j314s", "apple,t6001"})

	hw := fakehwmon.New(t, &fakehwmon.Options{
		Chips: []fakehwmon.ChipOptions{
			{
				Name: "macsmc_hwmon",
				PWMs: []fakehwmon.PWMOptions{
					{Index: 1, PWM: 100, Enable: 2},
					{Index: 2, PWM: 100, Enable: 2},
					{Index: 3, PWM: 100, Enable: 2},
					{Index: 4, PWM: 100, Enable: 2},
					{Index: 5, PWM: 100, Enable: 2},
				},
				Extra: map[string]string{
					"fan1_label": "Left Fan",   // generic → RoleCase
					"fan2_label": "Right Fan",  // generic → RoleCase
					"fan3_label": "GPU Fan",    // contains "gpu" → RoleGPU
					"fan4_label": "Pump",       // contains "pump" → RolePump
					"fan5_label": "CPU Blower", // contains "cpu" → RoleCPU
				},
			},
		},
	})

	chs, err := newBackend(t, dt.CompatPath(), hw.Root).Enumerate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 5 {
		t.Fatalf("expected 5 channels, got %d", len(chs))
	}

	byName := map[string]hal.Channel{}
	for _, ch := range chs {
		byName[filepath.Base(ch.ID)] = ch
	}

	tests := []struct {
		pwm      string
		wantRole hal.ChannelRole
	}{
		{"pwm1", hal.RoleCase}, // "Left Fan"
		{"pwm2", hal.RoleCase}, // "Right Fan"
		{"pwm3", hal.RoleGPU},  // "GPU Fan"
		{"pwm4", hal.RolePump}, // "Pump"
		{"pwm5", hal.RoleCPU},  // "CPU Blower"
	}
	for _, tt := range tests {
		ch, ok := byName[tt.pwm]
		if !ok {
			t.Errorf("channel %s not found in result", tt.pwm)
			continue
		}
		if ch.Role != tt.wantRole {
			t.Errorf("%s: role = %q, want %q", tt.pwm, ch.Role, tt.wantRole)
		}
		// All channels have pwm_enable → full write caps.
		wantCaps := hal.CapRead | hal.CapWritePWM | hal.CapRestore
		if ch.Caps != wantCaps {
			t.Errorf("%s: caps = %b, want %b", tt.pwm, ch.Caps, wantCaps)
		}
	}
}

func TestEnumerate_UnclassifiableLabel_RoleUnknown(t *testing.T) {
	dt := fakedt.New(t, []string{"apple,j274", "apple,t8103"})

	hw := fakehwmon.New(t, &fakehwmon.Options{
		Chips: []fakehwmon.ChipOptions{
			{
				Name: "macsmc_hwmon",
				PWMs: []fakehwmon.PWMOptions{{Index: 1, PWM: 128, Enable: 2}},
				// No fan1_label → absent file → empty label → RoleUnknown.
			},
		},
	})

	chs, err := newBackend(t, dt.CompatPath(), hw.Root).Enumerate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(chs))
	}
	if chs[0].Role != hal.RoleUnknown {
		t.Errorf("absent label should yield RoleUnknown, got %q", chs[0].Role)
	}
}

// --- Write-unsupported (CapRead-only) --------------------------------------

func TestEnumerate_WriteUnsupported_CapsReadOnly(t *testing.T) {
	// Simulate a machine where macsmc_hwmon exposes a pwm file but no
	// pwm_enable (early driver / read-only hardware variant).
	dt := fakedt.New(t, []string{"apple,j293", "apple,t8103"}) // MacBook Pro 13 M1

	// Build the hwmon directory manually — fakehwmon always creates
	// pwmN_enable when the Enable field is set.  We need to deliberately
	// omit it.
	sysRoot := t.TempDir()
	chipDir := filepath.Join(sysRoot, "hwmon0")
	if err := os.MkdirAll(chipDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", chipDir, err)
	}
	writeFile(t, filepath.Join(chipDir, "name"), "macsmc_hwmon")
	writeFile(t, filepath.Join(chipDir, "pwm1"), "128")
	writeFile(t, filepath.Join(chipDir, "fan1_input"), "1200")
	// Deliberately omit pwm1_enable → write unsupported.

	chs, err := newBackend(t, dt.CompatPath(), sysRoot).Enumerate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(chs))
	}
	if chs[0].Caps != hal.CapRead {
		t.Errorf("expected CapRead only when pwm_enable absent, got caps=%b", chs[0].Caps)
	}
	if chs[0].Caps&hal.CapWritePWM != 0 {
		t.Error("expected no CapWritePWM when pwm_enable absent")
	}
}

// writeFile writes content+newline to path, fatally failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content+"\n"), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}
