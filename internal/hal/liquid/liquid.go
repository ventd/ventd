// Package liquid provides shared types for USB liquid-cooling backends
// (Corsair, NZXT, Lian Li, etc.).
//
// Errors defined here are sentinel values returned by any liquid backend;
// callers use errors.Is to distinguish them.
package liquid

import "errors"

// ErrReadOnlyUnvalidatedFirmware is returned by Write/SetDuty on a device
// whose firmware version is not on the backend's allow-list, or when the
// --unsafe-corsair-writes flag was not passed. The device remains readable.
//
// RULE-LIQUID-03: unknown firmware is read-only.
// RULE-LIQUID-06: writable mode requires both the unsafe flag and an
// allow-listed firmware version.
var ErrReadOnlyUnvalidatedFirmware = errors.New("liquid: device is read-only (firmware not on allow-list or unsafe flag not set)")

// ErrKernelDriverOwnsDevice is returned by Probe when a kernel driver is
// bound to the hidraw device and the unbind attempt failed (permission denied,
// driver busy, sysfs refused the write).
//
// RULE-LIQUID-07: yield to conflicting kernel drivers; log an actionable error
// when unbind fails.
var ErrKernelDriverOwnsDevice = errors.New("liquid: kernel driver owns device; unbind failed")
