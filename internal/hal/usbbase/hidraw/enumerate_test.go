//go:build linux

package hidraw

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type fakeHidrawDev struct {
	name    string
	busType uint32
	vid     uint16
	pid     uint16
	iface   int // -1 = absent
}

// buildFakeSysfs creates a synthetic /sys/class/hidraw/ tree under a temp dir.
// uevent is written at hidrawN/device/uevent; bInterfaceNumber at hidrawN/
// (matches the OS path resolution of hidrawN/device/../bInterfaceNumber).
func buildFakeSysfs(t *testing.T, devs []fakeHidrawDev) string {
	t.Helper()
	root := t.TempDir()
	classDir := filepath.Join(root, "sys", "class", "hidraw")
	if err := os.MkdirAll(classDir, 0o755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	for _, d := range devs {
		devDir := filepath.Join(classDir, d.name)
		deviceDir := filepath.Join(devDir, "device")
		if err := os.MkdirAll(deviceDir, 0o755); err != nil {
			t.Fatalf("mkdirall device: %v", err)
		}
		uevent := fmt.Sprintf("HID_ID=%04X:%08X:%08X\n", d.busType, d.vid, d.pid)
		if err := os.WriteFile(filepath.Join(deviceDir, "uevent"), []byte(uevent), 0o644); err != nil {
			t.Fatalf("write uevent: %v", err)
		}
		// bInterfaceNumber lives at devDir/ because the OS resolves
		// devPath+"/device/../bInterfaceNumber" → devPath+"/bInterfaceNumber"
		// (device/ is a real dir in the fake tree, not a symlink).
		if d.iface >= 0 {
			if err := os.WriteFile(filepath.Join(devDir, "bInterfaceNumber"),
				fmt.Appendf(nil, "%02x\n", d.iface), 0o644); err != nil {
				t.Fatalf("write bInterfaceNumber: %v", err)
			}
		}
	}
	return classDir
}

// TestEnumerate_FiltersNonUSBBuses verifies RULE-HIDRAW-01:
// only BUS_USB (0x0003) devices are returned.
func TestEnumerate_FiltersNonUSBBuses(t *testing.T) {
	root := buildFakeSysfs(t, []fakeHidrawDev{
		{name: "hidraw0", busType: 0x0003, vid: 0x1b1c, pid: 0x0c32}, // USB   → included
		{name: "hidraw1", busType: 0x0005, vid: 0x1b1c, pid: 0x0c32}, // BT    → excluded
		{name: "hidraw2", busType: 0x0006, vid: 0x1b1c, pid: 0x0c32}, // virt  → excluded
	})

	got, err := enumerateFrom(root, nil)
	if err != nil {
		t.Fatalf("enumerateFrom: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1 (BUS_USB only)", len(got))
	}
	if got[0].BusType != 0x0003 {
		t.Errorf("BusType = %#x, want 0x0003", got[0].BusType)
	}
	if got[0].Path != "/dev/hidraw0" {
		t.Errorf("Path = %q, want /dev/hidraw0", got[0].Path)
	}
}

// TestEnumerate_MatcherVIDPID verifies VID+PID matcher filtering.
func TestEnumerate_MatcherVIDPID(t *testing.T) {
	root := buildFakeSysfs(t, []fakeHidrawDev{
		{name: "hidraw0", busType: 0x0003, vid: 0x1b1c, pid: 0x0c32},
		{name: "hidraw1", busType: 0x0003, vid: 0x1234, pid: 0xABCD},
	})

	matchers := []Matcher{{VendorID: 0x1b1c, ProductIDs: []uint16{0x0c32}}}
	got, err := enumerateFrom(root, matchers)
	if err != nil {
		t.Fatalf("enumerateFrom: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1", len(got))
	}
	if got[0].VendorID != 0x1b1c || got[0].ProductID != 0x0c32 {
		t.Errorf("VID=%#x PID=%#x, want 0x1b1c/0x0c32", got[0].VendorID, got[0].ProductID)
	}
}

// TestEnumerate_MatcherInterface verifies interface-number matcher filtering.
func TestEnumerate_MatcherInterface(t *testing.T) {
	root := buildFakeSysfs(t, []fakeHidrawDev{
		{name: "hidraw0", busType: 0x0003, vid: 0x1b1c, pid: 0x0c32, iface: 0},
		{name: "hidraw1", busType: 0x0003, vid: 0x1b1c, pid: 0x0c32, iface: 1},
	})

	matchers := []Matcher{{VendorID: 0x1b1c, Interface: 1}}
	got, err := enumerateFrom(root, matchers)
	if err != nil {
		t.Fatalf("enumerateFrom: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (interface 1 only)", len(got))
	}
	if got[0].InterfaceNumber != 1 {
		t.Errorf("InterfaceNumber = %d, want 1", got[0].InterfaceNumber)
	}
}

// TestEnumerate_MissingOptionalFields verifies absent serial / bInterfaceNumber
// do not cause an error and produce the correct zero values.
func TestEnumerate_MissingOptionalFields(t *testing.T) {
	root := buildFakeSysfs(t, []fakeHidrawDev{
		{name: "hidraw0", busType: 0x0003, vid: 0x1b1c, pid: 0x0c32, iface: -1},
	})

	got, err := enumerateFrom(root, nil)
	if err != nil {
		t.Fatalf("enumerateFrom: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].InterfaceNumber != -1 {
		t.Errorf("InterfaceNumber = %d, want -1", got[0].InterfaceNumber)
	}
	if got[0].SerialNumber != "" {
		t.Errorf("SerialNumber = %q, want empty", got[0].SerialNumber)
	}
}

// TestEnumerate_MissingSysfs verifies that a missing /sys/class/hidraw/
// returns nil slice and nil error (graceful degradation).
func TestEnumerate_MissingSysfs(t *testing.T) {
	got, err := enumerateFrom("/does/not/exist/hidraw", nil)
	if err != nil {
		t.Fatalf("enumerateFrom on missing path: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d devices, want 0", len(got))
	}
}
