// Package liquid provides shared types for USB liquid-cooling backends
// (Corsair, NZXT, Lian Li, etc.).
//
// Errors defined here are sentinel values returned by any liquid backend;
// callers use errors.Is to distinguish them.
package liquid

import "errors"

// ErrReadOnlyUnvalidatedFirmware is the historical sentinel for the
// v0.4 firmware-allowlist gate on Corsair writes. The allowlist + the
// `--unsafe-corsair-writes` flag were removed in v0.6.1 per the
// `feedback-dont-default-writes-off` directive — Corsair writes now
// proceed unconditionally, gated only by the genuine safety primitives
// (pump-minimum floor RULE-LIQUID-01, USB-reconnect floor RULE-LIQUID-02,
// restore-on-panic RULE-LIQUID-04, serialised writes RULE-LIQUID-05).
// The sentinel is retained as defence-in-depth for any future
// firmware-specific refusal cause (e.g. a known-bad firmware revision
// surfacing a stuck-state bug); v0.6.1 callers don't return it on the
// happy path. Wrapped consumers branch on errors.Is.
var ErrReadOnlyUnvalidatedFirmware = errors.New("liquid: device is read-only (defence-in-depth; v0.4 firmware-allowlist gate removed in v0.6.1)")

// ErrKernelDriverOwnsDevice is returned by Probe when a kernel driver is
// bound to the hidraw device and the unbind attempt failed (permission denied,
// driver busy, sysfs refused the write).
//
// RULE-LIQUID-07: yield to conflicting kernel drivers; log an actionable error
// when unbind fails.
var ErrKernelDriverOwnsDevice = errors.New("liquid: kernel driver owns device; unbind failed")
