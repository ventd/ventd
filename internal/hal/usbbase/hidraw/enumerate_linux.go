//go:build linux

package hidraw

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
)

// Enumerate returns all USB hidraw devices matching any of the given matchers.
// Devices with BusType != BUS_USB (0x03) are silently excluded (RULE-HIDRAW-01).
// If /sys/class/hidraw/ does not exist, returns nil, nil.
func Enumerate(matchers []Matcher) ([]DeviceInfo, error) {
	return enumerateFrom("/sys/class/hidraw", matchers)
}

// enumerateFrom walks sysRoot (normally /sys/class/hidraw) and returns matching
// USB HID devices. Exposed for tests to inject a synthetic sysfs tree.
func enumerateFrom(sysRoot string, matchers []Matcher) ([]DeviceInfo, error) {
	entries, err := os.ReadDir(sysRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			slog.Info("hidraw: sysfs not present; no hidraw devices", "path", sysRoot)
			return nil, nil
		}
		return nil, fmt.Errorf("hidraw: read %s: %w", sysRoot, err)
	}

	var out []DeviceInfo
	for _, e := range entries {
		devPath := sysRoot + "/" + e.Name()

		busType, vid, pid, err := parseHIDID(devPath)
		if err != nil {
			slog.Warn("hidraw: skip (uevent parse failed)", "dev", e.Name(), "err", err)
			continue
		}

		// RULE-HIDRAW-01: exclude non-USB buses.
		if busType != 0x03 {
			continue
		}

		iface := readBInterfaceNumber(devPath)

		if len(matchers) > 0 && !matchesAny(matchers, vid, pid, iface) {
			continue
		}

		serial := readSerial(devPath)

		out = append(out, DeviceInfo{
			Path:            "/dev/" + e.Name(),
			VendorID:        vid,
			ProductID:       pid,
			InterfaceNumber: iface,
			SerialNumber:    serial,
			BusType:         busType,
		})
	}
	return out, nil
}

// parseHIDID reads the HID_ID field from devPath/device/uevent.
// Format: HID_ID=TTTT:VVVVVVVV:PPPPPPPP (all hex).
func parseHIDID(devPath string) (busType uint32, vid, pid uint16, err error) {
	// Use string concat rather than filepath.Join to preserve the OS's
	// symlink-aware path resolution when device/ is a symlink in real sysfs.
	data, err := os.ReadFile(devPath + "/device/uevent")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read uevent: %w", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "HID_ID=") {
			continue
		}
		parts := strings.SplitN(strings.TrimSpace(line[7:]), ":", 3)
		if len(parts) != 3 {
			return 0, 0, 0, fmt.Errorf("malformed HID_ID %q", line)
		}
		var b, v, p uint64
		if _, e := fmt.Sscanf(parts[0], "%x", &b); e != nil {
			return 0, 0, 0, fmt.Errorf("bustype: %w", e)
		}
		if _, e := fmt.Sscanf(parts[1], "%x", &v); e != nil {
			return 0, 0, 0, fmt.Errorf("vid: %w", e)
		}
		if _, e := fmt.Sscanf(parts[2], "%x", &p); e != nil {
			return 0, 0, 0, fmt.Errorf("pid: %w", e)
		}
		return uint32(b), uint16(v), uint16(p), nil
	}
	return 0, 0, 0, fmt.Errorf("HID_ID not found in uevent")
}

// readBInterfaceNumber reads the USB interface number from
// devPath/device/../bInterfaceNumber (ASCII hex). Returns -1 if absent.
// The path with ".." is passed as-is so the OS resolves any sysfs symlinks.
func readBInterfaceNumber(devPath string) int {
	data, err := os.ReadFile(devPath + "/device/../bInterfaceNumber")
	if err != nil {
		return -1
	}
	var iface int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%x", &iface); err != nil {
		return -1
	}
	return iface
}

// readSerial reads the USB device serial string from
// devPath/device/../../serial. Returns "" if absent.
func readSerial(devPath string) string {
	data, err := os.ReadFile(devPath + "/device/../../serial")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
