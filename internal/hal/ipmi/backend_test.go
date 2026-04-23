package ipmi_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.uber.org/goleak"

	"github.com/ventd/ventd/internal/hal/ipmi"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestEnumerate_NonServerDMI_Empty verifies that Enumerate returns 0 channels
// and does not open /dev/ipmi0 when the DMI chassis type indicates a desktop.
func TestEnumerate_NonServerDMI_Empty(t *testing.T) {
	b := ipmi.NewBackendNonServer(slog.Default())

	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: unexpected error: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("expected 0 channels for non-server desktop chassis, got %d", len(channels))
	}
}

// TestVendorDetection verifies that vendor strings are classified correctly.
func TestVendorDetection(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Super Micro Computer, Inc.", "supermicro"},
		{"Supermicro", "supermicro"},
		{"SUPERMICRO", "supermicro"},
		{"Dell Inc.", "dell"},
		{"Dell EMC", "dell"},
		{"DELL", "dell"},
		{"HP", "hpe"},
		{"HPE", "hpe"},
		{"Hewlett-Packard", "hpe"},
		{"Hewlett Packard Enterprise", "hpe"},
		{"Lenovo", "unknown"}, // DMI-gated as server but no fan-write commands
		{"ASUS", "unknown"},
		{"MSI", "unknown"},
		{"", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ipmi.DetectVendorFromString(tt.input)
			if got != tt.want {
				t.Errorf("DetectVendorFromString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestWritePWM_UnknownVendor_Error verifies that Write returns an "unsupported
// vendor" error for machines that passed DMI gating but have no fan-write path.
func TestWritePWM_UnknownVendor_Error(t *testing.T) {
	b := ipmi.NewBackendForTest(slog.Default(), "unknown")
	ch := ipmi.MakeTestChannel(0x01, "Fan 1")

	err := b.Write(ch, 128)
	if err == nil {
		t.Fatal("expected error from Write on unknown vendor, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported vendor") {
		t.Errorf("error %q does not contain 'unsupported vendor'", err.Error())
	}
}

// TestPWMConversion verifies that PWM 0-255 maps to the correct 0-100 percent
// with standard rounding.
func TestPWMConversion(t *testing.T) {
	tests := []struct {
		pwm  uint8
		want uint8
	}{
		{0, 0},
		{255, 100},
		// 128 * 100 / 255 = 50.196... → 50
		{128, 50},
		// 127 * 100 / 255 = 49.804... → 50
		{127, 50},
		// 26 * 100 / 255 = 10.196... → 10
		{26, 10},
		// 25 * 100 / 255 = 9.804... → 10
		{25, 10},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("pwm=%d", tt.pwm), func(t *testing.T) {
			got := ipmi.PWMToPercent(tt.pwm)
			if got != tt.want {
				t.Errorf("PWMToPercent(%d) = %d, want %d", tt.pwm, got, tt.want)
			}
		})
	}
}
