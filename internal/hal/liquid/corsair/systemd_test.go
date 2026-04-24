package corsair

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// parseUnit parses a systemd unit file into a map of key → list of values.
// Section headers, blank lines, and # comments are skipped.
// Multi-value keys (e.g. DeviceAllow) accumulate every occurrence.
func parseUnit(data string) map[string][]string {
	out := make(map[string][]string)
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len(line) == 0 || line[0] == '#' || line[0] == '[' {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		out[k] = append(out[k], v)
	}
	return out
}

func loadUnit(t *testing.T, name string) map[string][]string {
	t.Helper()
	// Locate deploy/ relative to this test file.
	_, thisFile, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	testDir := filepath.Dir(thisFile)
	repoRoot := filepath.Join(testDir, "..", "..", "..", "..")
	unitFile := filepath.Join(repoRoot, "deploy", name)

	data, err := os.ReadFile(unitFile)
	if err != nil {
		t.Fatalf("read deploy/%s: %v", name, err)
	}
	return parseUnit(string(data))
}

// TestMainUnit_NoUsbHidDeviceGrant verifies that deploy/ventd.service grants no
// USB HID device access and retains no capabilities. This encodes the "single
// binary, no sidecar" architecture: udev uaccess tag + unprivileged daemon is
// the sole mechanism for user access to Corsair USB devices.
func TestMainUnit_NoUsbHidDeviceGrant(t *testing.T) {
	u := loadUnit(t, "ventd.service")

	t.Run("no_usb_hidraw_device_allow", func(t *testing.T) {
		for _, v := range u["DeviceAllow"] {
			if strings.Contains(v, "/dev/hidraw") || strings.Contains(v, "/dev/bus/usb") {
				t.Errorf("ventd.service DeviceAllow must not grant USB HID access: %q", v)
			}
			// Also check for vendor-specific references.
			if strings.Contains(v, "1b1c") {
				t.Errorf("ventd.service DeviceAllow must not reference Corsair vendor ID 1b1c: %q", v)
			}
		}
	})

	t.Run("capability_bounding_set_empty", func(t *testing.T) {
		vals := u["CapabilityBoundingSet"]
		if len(vals) != 1 || vals[0] != "" {
			t.Errorf("CapabilityBoundingSet: want exactly empty, got %v", vals)
		}
	})

	t.Run("no_ambient_cap_sys_rawio_or_admin", func(t *testing.T) {
		for _, v := range u["AmbientCapabilities"] {
			if strings.Contains(v, "CAP_SYS_RAWIO") || strings.Contains(v, "CAP_SYS_ADMIN") {
				t.Errorf("ventd.service AmbientCapabilities must not grant %s: %q", v, v)
			}
		}
	})
}
