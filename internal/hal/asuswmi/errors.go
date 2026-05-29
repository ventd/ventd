// SPDX-License-Identifier: GPL-3.0-or-later
package asuswmi

import "errors"

// ErrFanCurveRefused is returned when the kernel refuses a custom fan-curve
// write. The asus-wmi driver round-trips the curve through an ACPI method that
// the platform firmware can reject (the well-documented "ASUS BIOS rejected
// fan curve" failure on some Strix/TUF models — the firmware validates the
// curve shape and returns an error for points it dislikes). The kernel
// surfaces this as EIO/ENODEV on the sysfs write; the typed wrap lets the
// doctor / wizard branch on errors.Is without string-matching the syscall.
var ErrFanCurveRefused = errors.New("asuswmi: asus_custom_fan_curve write refused by firmware (BIOS rejected the fan curve)")

// ErrNoFanCurveHwmon is returned by stateFrom when a channel's state does not
// point at an asus_custom_fan_curve hwmon directory. It indicates a
// programming error (a channel minted outside Enumerate with an empty
// HwmonDir), not a runtime hardware condition.
var ErrNoFanCurveHwmon = errors.New("asuswmi: channel state has empty HwmonDir")
