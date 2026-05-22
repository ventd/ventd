// SPDX-License-Identifier: GPL-3.0-or-later
package lenovoideapad

import "errors"

// ErrPlatformProfileAbsent is returned when /sys/firmware/acpi/platform_profile
// does not exist at backend construction. This happens when the kernel doesn't
// expose the platform_profile ACPI surface (very old kernels) or when the
// ideapad_laptop module isn't loaded. The probe layer treats this as "not an
// IdeaPad host that ventd can drive yet" and skips the backend.
var ErrPlatformProfileAbsent = errors.New("lenovoideapad: /sys/firmware/acpi/platform_profile not found; ideapad_laptop module not loaded or kernel too old")

// ErrPlatformProfileRefused is returned when a write to platform_profile is
// rejected by the kernel (EINVAL on a state not in platform_profile_choices,
// or EPERM when ACPI policy blocks the write). Recovery is operator
// intervention — there's no equivalent of the thinkpad_acpi fan_control=1
// modparam fix for this surface.
var ErrPlatformProfileRefused = errors.New("lenovoideapad: kernel refused platform_profile write; check platform_profile_choices and ACPI policy")

// ErrInvalidPlatformProfileResponse is returned when
// /sys/firmware/acpi/platform_profile returns content the parser cannot
// recognise. Treated as Reading.OK=false (skip this tick) rather than a
// daemon failure.
var ErrInvalidPlatformProfileResponse = errors.New("lenovoideapad: platform_profile response not in known choice set")
