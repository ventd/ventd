// SPDX-License-Identifier: GPL-3.0-or-later
package thinkpad

import "errors"

// ErrFanControlDisabled is returned when the kernel refuses a level
// write because the thinkpad_acpi module was loaded without
// fan_control=1. The kernel surfaces this as EPERM silently per
// drivers/platform/x86/thinkpad_acpi.c — there is no dmesg breadcrumb
// on stock distro kernels. RULE-WIZARD-RECOVERY-10 + the
// modprobe-options-write endpoint (RULE-MODPROBE-OPTIONS-01) are the
// recovery surfaces that flip the modparam on; this sentinel is the
// signal the wizard / doctor consumes to dispatch them.
var ErrFanControlDisabled = errors.New("thinkpad: fan_control=1 modparam not set; /proc/acpi/ibm/fan refuses level writes")

// ErrProcFanAbsent is returned when /proc/acpi/ibm/fan does not exist
// at backend construction. This happens when the thinkpad_acpi module
// isn't loaded — either it isn't present (non-ThinkPad host) or the
// build excluded CONFIG_THINKPAD_ACPI. The probe layer should treat
// this as "not a ThinkPad" and skip the backend; it is not a daemon-
// fatal condition.
var ErrProcFanAbsent = errors.New("thinkpad: /proc/acpi/ibm/fan not found; thinkpad_acpi module not loaded")

// ErrInvalidProcFanResponse is returned when /proc/acpi/ibm/fan
// produces content the parser cannot recognise. Some kernel builds
// emit minimal output during early boot before the EC has reported
// its first speed sample; the controller should treat this as "skip
// this tick" (OK=false on the Reading) rather than a daemon failure.
var ErrInvalidProcFanResponse = errors.New("thinkpad: /proc/acpi/ibm/fan response missing required fields")
