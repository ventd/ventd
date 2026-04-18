// Package hal defines the hardware-abstraction layer that all ventd fan
// backends (hwmon, NVML, and — in Phase 2 — IPMI, liquidctl, cros_ec,
// pwmsys, asahi) sit behind.
//
// The FanBackend interface is intentionally narrow: Enumerate / Read /
// Write / Restore / Close / Name. Backend-private state (the hwmon
// sysfs path, the NVML device index, the pre-ventd pwm_enable value,
// etc.) travels through Channel.Opaque so the controller and watchdog
// never reach into a specific backend's implementation.
//
// The interface is pure data + operations; no logging or lifecycle
// shape is imposed. Each backend implementation owns its own logger
// (set at construction) and its own mode-acquisition bookkeeping.
package hal

import (
	"context"
	"errors"
)

// ErrNotPermitted signals a permission failure during manual-mode
// acquisition (EACCES/EPERM on pwm_enable write). Callers should treat
// this as fatal — retries will not cure a misconfiguration.
var ErrNotPermitted = errors.New("hal: manual-mode acquisition not permitted")

// ChannelRole is a coarse, human-readable classification of what a
// channel is probably cooling. It's advisory — the control config is
// still the source of truth for how a fan is driven.
type ChannelRole string

// Canonical role values. Backends should pick the closest match; when
// nothing fits, use RoleUnknown rather than inventing a new string.
const (
	RoleCPU     ChannelRole = "cpu"
	RoleGPU     ChannelRole = "gpu"
	RoleCase    ChannelRole = "case"
	RolePump    ChannelRole = "pump"
	RoleAIOFan  ChannelRole = "aio_fan"
	RoleChassis ChannelRole = "chassis"
	RoleUnknown ChannelRole = "unknown"
)

// Caps is a bitset of capabilities a channel supports. Backends set
// the bits that apply; consumers check before calling operations that
// depend on them (e.g. don't call Write on a channel without a write
// cap).
type Caps uint32

// Capability bits.
const (
	// CapRead — the channel supports Reading current PWM / RPM / temp
	// via FanBackend.Read.
	CapRead Caps = 1 << iota

	// CapWritePWM — the channel accepts a 0-255 duty-cycle Write.
	CapWritePWM

	// CapWriteRPMTarget — the channel accepts an RPM setpoint Write.
	// The interface still takes a 0-255 value; the backend is
	// responsible for scaling it to a valid RPM setpoint.
	CapWriteRPMTarget

	// CapRestore — the channel supports FanBackend.Restore back to
	// its pre-ventd state (e.g. pwm_enable = 2 on hwmon, auto-curve
	// on NVML).
	CapRestore
)

// Channel is a single fan endpoint exposed by a backend. ID is
// unique within a backend; Opaque carries whatever state the backend
// needs to execute subsequent Read / Write / Restore calls.
//
// Callers MUST NOT inspect or mutate Opaque — it is the backend's
// private payload and its shape is free to change between builds.
type Channel struct {
	ID     string
	Role   ChannelRole
	Caps   Caps
	Opaque any
}

// Reading is the result of FanBackend.Read. OK=false indicates the
// backend could not populate this reading (e.g. transient sysfs
// error, NVML query failure); callers should treat non-OK readings
// as "skip this tick", not "write 0".
type Reading struct {
	PWM  uint8
	RPM  uint16
	Temp float64
	OK   bool
}

// FanBackend is the contract every fan backend implements. Implementations
// are expected to be safe to call from multiple goroutines; the controller
// tick and the watchdog restore run in separate goroutines and both hit
// the backend.
type FanBackend interface {
	// Enumerate returns every channel currently exposed by this
	// backend. It is safe to call multiple times; each call observes
	// the current hardware state (NVML GPUs hot-plugged, hwmon
	// devices bound late by udev, etc.).
	Enumerate(ctx context.Context) ([]Channel, error)

	// Read samples the channel's current state. Failures populate
	// Reading.OK = false; the error return is reserved for
	// non-recoverable backend problems.
	Read(ch Channel) (Reading, error)

	// Write commands the channel to a PWM duty cycle (0-255). For
	// channels that internally use an RPM setpoint the backend scales
	// internally. Write is responsible for any mode transitions
	// required (e.g. pwm_enable = 1 on hwmon); the controller never
	// touches mode files directly.
	Write(ch Channel, pwm uint8) error

	// Restore returns the channel to the state it was in before
	// ventd took control. Idempotent: safe to call repeatedly and
	// safe to call on a channel that was never Written to.
	Restore(ch Channel) error

	// Close releases backend-level resources (library handles,
	// netlink sockets, etc.). Individual channel state is NOT tied
	// to Close; callers should Restore channels they care about
	// before calling Close.
	Close() error

	// Name is the short, stable identifier used when channels are
	// tagged by the registry (e.g. "hwmon", "nvml").
	Name() string
}
