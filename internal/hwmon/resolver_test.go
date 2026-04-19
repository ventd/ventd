package hwmon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRegression_Issue86_HwmonRenumberColdBoot reproduces the cold-boot udev
// race from #86: on a dual-Super-I/O board, only one chip may have finished
// enumerating when the daemon starts. FindHwmonDir must refuse to return a
// directory for a device whose hwmon subdirectory has not yet appeared —
// it must not silently succeed for the wrong chip's stable-device path.
//
// regresses #86
func TestRegression_Issue86_HwmonRenumberColdBoot(t *testing.T) {
	root := t.TempDir()

	// Cold-boot sysfs state: only nct6683.2592 (hwmon5) has finished its
	// driver probe. nct6687.2592 — the chip the operator configured against
	// — exists as a platform device but its hwmon subdirectory has not
	// appeared yet (driver probe not complete).
	nct6683Dev := filepath.Join(root, "devices/platform/nct6683.2592")
	nct6683Hwmon := filepath.Join(nct6683Dev, "hwmon/hwmon5")
	if err := os.MkdirAll(nct6683Hwmon, 0o755); err != nil {
		t.Fatal(err)
	}

	// nct6687.2592 device directory exists (ACPI-enumerated) but its
	// hwmon/ subtree has not been created yet by the kernel driver.
	nct6687Dev := filepath.Join(root, "devices/platform/nct6687.2592")
	if err := os.MkdirAll(nct6687Dev, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-fix behaviour: the single-candidate resolver returned the sole
	// present chip regardless of which stable-device path was configured,
	// so all four fans silently bound to hwmon5 (nct6683) instead of
	// hwmon6 (nct6687) for the entire 3.7-hour window until the sibling
	// enumerated. Post-fix assertion: querying the not-yet-enumerated
	// device must return an error rather than a false success.
	t.Run("not_yet_enumerated_chip_returns_error", func(t *testing.T) {
		_, err := FindHwmonDir(nct6687Dev)
		if err == nil {
			t.Fatal("FindHwmonDir: want error when configured chip has not enumerated; " +
				"got nil — cold-boot udev race would bind fans to wrong chip (#86)")
		}
	})

	// Sanity: the enumerated sibling resolves correctly by its own path.
	t.Run("enumerated_chip_resolves_by_stable_device", func(t *testing.T) {
		got, err := FindHwmonDir(nct6683Dev)
		if err != nil {
			t.Fatalf("FindHwmonDir for enumerated chip: %v", err)
		}
		if got != nct6683Hwmon {
			t.Errorf("got %q, want %q", got, nct6683Hwmon)
		}
	})

	// Simulate udev settle completing: nct6687 driver finishes its probe
	// and hwmon6 appears under the configured device.
	nct6687Hwmon := filepath.Join(nct6687Dev, "hwmon/hwmon6")
	if err := os.MkdirAll(nct6687Hwmon, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("after_udev_settle_configured_chip_resolves", func(t *testing.T) {
		got, err := FindHwmonDir(nct6687Dev)
		if err != nil {
			t.Fatalf("FindHwmonDir after udev settle: %v", err)
		}
		if got != nct6687Hwmon {
			t.Errorf("got %q, want %q", got, nct6687Hwmon)
		}
	})
}
