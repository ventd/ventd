package ipmi_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.uber.org/goleak"

	"github.com/ventd/ventd/internal/hal/ipmi"
	"github.com/ventd/ventd/internal/testfixture/fakeipmi"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// makeSendRecvAdapter bridges a fakeipmi.Fake to the ipmi.WithSendRecv hook.
// req = [netfn, cmd, data...]; resp is a 128-byte buffer filled with [cc, payload...].
func makeSendRecvAdapter(f *fakeipmi.Fake) func(req, resp []byte) error {
	return func(req, resp []byte) error {
		if len(req) < 2 {
			return errors.New("fakeipmi: short IPMI request")
		}
		result, err := f.Respond(req[0], req[1], req[2:])
		if err != nil {
			return err
		}
		copy(resp, result)
		return nil
	}
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

// TestEnumerate_DmiGate_DesktopChassisRejects verifies that Enumerate returns 0
// channels when chassis_type=3 (Desktop) and vendor is ASUS (not in the server
// allowlist).
func TestEnumerate_DmiGate_DesktopChassisRejects(t *testing.T) {
	b := ipmi.NewBackendWithDMI(slog.Default(), 3, "ASUS")

	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: unexpected error: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("expected 0 channels for desktop chassis with unknown vendor, got %d", len(channels))
	}
}

// TestEnumerate_DmiGate_ServerChassisAccepts verifies that Enumerate proceeds
// when chassis_type=23 (Rack Mount Chassis), regardless of vendor string.
func TestEnumerate_DmiGate_ServerChassisAccepts(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{
		Vendor:  "supermicro",
		SDRFans: []fakeipmi.SDRFanRecord{{SensorNumber: 0x01, Name: "FAN1"}},
	})
	// NewBackendForTest sets chassisType=23 internally.
	b := ipmi.NewBackendForTest(slog.Default(), "supermicro", ipmi.WithSendRecv(makeSendRecvAdapter(f)))

	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: unexpected error: %v", err)
	}
	if len(channels) == 0 {
		t.Error("expected >= 1 channel for rack-mount server (chassis_type=23), got 0")
	}
}

// TestEnumerate_DmiGate_VendorAllowlistAccepts verifies that Enumerate proceeds
// when chassis_type=3 (Desktop) but the sys_vendor matches the server allowlist
// (Supermicro workstation boards can have non-rack chassis type in DMI).
func TestEnumerate_DmiGate_VendorAllowlistAccepts(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{
		Vendor:  "supermicro",
		SDRFans: []fakeipmi.SDRFanRecord{{SensorNumber: 0x01, Name: "FAN1"}},
	})
	// chassis_type=3 (Desktop) but vendor=Supermicro → allowlist match proceeds.
	b := ipmi.NewBackendWithDMI(slog.Default(), 3, "Supermicro",
		ipmi.WithSendRecv(makeSendRecvAdapter(f)))

	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: unexpected error: %v", err)
	}
	if len(channels) == 0 {
		t.Error("expected >= 1 channel when vendor is Supermicro (server allowlist), got 0")
	}
}

// TestEnumerate_DmiGate_NoProbeOnReject verifies that Enumerate never calls into
// the IPMI transport layer when the DMI gate rejects the system. The sendRecv
// call count is the probe counter — it must stay 0 when the gate fires before
// any SDR enumeration attempt.
func TestEnumerate_DmiGate_NoProbeOnReject(t *testing.T) {
	var probeCount int
	countingRecv := func(req, resp []byte) error {
		probeCount++
		return nil
	}
	// chassis_type=3 (Desktop), vendor=ASUS → DMI gate must reject before any probe.
	b := ipmi.NewBackendWithDMI(slog.Default(), 3, "ASUS", ipmi.WithSendRecv(countingRecv))

	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: unexpected error: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("expected 0 channels on DMI rejection, got %d", len(channels))
	}
	if probeCount != 0 {
		t.Errorf("IPMI transport called %d time(s); must be 0 — /dev/ipmi0 must not be probed on DMI rejection", probeCount)
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
