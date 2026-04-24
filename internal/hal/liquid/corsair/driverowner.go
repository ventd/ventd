package corsair

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ventd/ventd/internal/hal/liquid"
)

// sysfsRoot is the base for hidraw sysfs entries. Overridden in tests via
// checkAndUnbindFrom so tests never touch real /sys.
var sysfsRoot = "/sys/class/hidraw"

// CheckAndUnbind inspects /sys/class/hidraw/<node>/device/driver.
// If a driver is bound (any driver — broad detection per RULE-LIQUID-07),
// it attempts to unbind the driver from that specific device via
// /sys/bus/hid/drivers/<driver-name>/unbind.
//
// Returns:
//   - nil if no driver was bound, OR unbind succeeded
//   - ErrKernelDriverOwnsDevice (wrapping a descriptive error) if unbind failed
//
// The error message includes: driver name, hidraw path, and a remediation hint
// pointing to /etc/modprobe.d/ventd.conf.
//
// Idempotent: safe to call even when no driver is bound.
// Called once per device at Probe time (RULE-LIQUID-07).
func CheckAndUnbind(hidrawPath string) error {
	return checkAndUnbindFrom(sysfsRoot, "/sys/bus/hid/drivers", hidrawPath)
}

// checkAndUnbindFrom is the testable inner implementation.
// sysClassHidraw and sysDrivers are injected so tests can use t.TempDir().
func checkAndUnbindFrom(sysClassHidraw, sysDrivers, hidrawPath string) error {
	// hidrawPath is /dev/hidrawN; derive the sysfs node name (hidrawN).
	node := filepath.Base(hidrawPath)
	driverLink := filepath.Join(sysClassHidraw, node, "device", "driver")

	// Read the driver symlink. If it doesn't exist, no driver is bound.
	target, err := os.Readlink(driverLink)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no driver bound
		}
		// Malformed or unresolvable symlink — treat as "cannot determine",
		// return a descriptive error so the caller can skip this device.
		return fmt.Errorf("corsair: read driver symlink %s: %w", driverLink, err)
	}

	// The driver name is the last path component of the symlink target.
	driverName := filepath.Base(target)
	if driverName == "" || driverName == "." {
		return fmt.Errorf("corsair: empty driver name from symlink %s → %s", driverLink, target)
	}

	// Determine the HID device identifier by reading the uevent's HID_ID.
	// The unbind file expects the HID bus address (e.g. "0003:1B1C:0C32.0001").
	devID, err := readHIDDevID(sysClassHidraw, node)
	if err != nil {
		return fmt.Errorf("corsair: read device ID for unbind: %w", err)
	}

	// Attempt to unbind by writing the device ID to the driver's unbind file.
	// /sys/bus/hid/drivers/<driver>/unbind
	unbindPath := filepath.Join(sysDrivers, driverName, "unbind")
	if writeErr := os.WriteFile(unbindPath, []byte(devID), 0o200); writeErr != nil {
		msg := fmt.Sprintf(
			"corsair: kernel driver %q owns %s and unbind failed: %v — "+
				"to permanently block this driver from claiming Corsair devices, "+
				"create /etc/modprobe.d/ventd.conf with: blacklist %s",
			driverName, hidrawPath, writeErr, driverName,
		)
		slog.Warn("corsair: driver unbind failed; device unavailable for this run",
			"driver", driverName,
			"hidraw", hidrawPath,
			"unbind_path", unbindPath,
			"remediation", "blacklist "+driverName+" in /etc/modprobe.d/ventd.conf",
		)
		return fmt.Errorf("%w: %s", liquid.ErrKernelDriverOwnsDevice, msg)
	}

	slog.Info("corsair: unbound kernel driver from hidraw device",
		"driver", driverName, "hidraw", hidrawPath)
	return nil
}

// readHIDDevID extracts the HID device identifier string from the sysfs uevent
// for node (e.g. "hidraw0"). Returns the value of the HID_NAME or the
// directory name of the device symlink, which is the kernel's HID bus address
// (format: TTTT:VVVV:PPPP.NNNN).
func readHIDDevID(sysClassHidraw, node string) (string, error) {
	// The device symlink target's base name is the HID bus address used by unbind.
	// e.g. /sys/class/hidraw/hidraw0/device -> ../../devices/.../hid/0003:1B1C:0C32.0001
	deviceLink := filepath.Join(sysClassHidraw, node, "device")
	target, err := os.Readlink(deviceLink)
	if err != nil {
		return "", fmt.Errorf("read device symlink %s: %w", deviceLink, err)
	}
	// The last path component is the HID address.
	id := filepath.Base(target)
	if id == "" || id == "." {
		return "", fmt.Errorf("empty device ID from symlink %s → %s", deviceLink, target)
	}
	// Normalise to uppercase (kernel uses uppercase for HID addresses).
	return strings.ToUpper(id), nil
}
