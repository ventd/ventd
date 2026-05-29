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

// ErrSensorSentinel is returned when a backend Read detects a raw value that
// matches a known driver sentinel (e.g. 0xFFFF RPM from nct6687) or exceeds
// the plausibility cap for the reading kind. Callers must treat this as
// "skip this tick", not as a fatal error. The OK field of Reading is also
// set to false so callers that only check OK still handle the case correctly.
var ErrSensorSentinel = errors.New("hal: sensor sentinel or implausible value")

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

	// CapWritePowerProfile — the channel exposes a secondary, enum-
	// valued write surface that selects a board-wide power profile
	// (eco / comfort / turbo on msi-ec's shift_mode; balanced /
	// performance on platform-profile-class drivers). Distinct from
	// CapWritePWM because the value space is a closed set of names,
	// not a 0..255 duty cycle, and the surface shapes BIOS-managed
	// behaviour (CPU PL1/PL2, BIOS fan curve, thermal headroom) on
	// top of whatever PWM the daemon writes.
	//
	// Channels that advertise this bit MUST also satisfy
	// hal.PowerProfileBackend on their backend. The capability is
	// orthogonal to CapWritePWM — a channel may have one, both, or
	// neither (#1166).
	CapWritePowerProfile

	// CapWriteCurve — the channel is driven by a whole fan curve
	// programmed into the hardware once, after which the firmware
	// follows it on its own (the GPU / EC reads its own temperature
	// and interpolates between anchor points) rather than by a
	// per-tick duty Write. Distinct from CapWritePWM because the
	// daemon hands the hardware a (temperature → percent) curve and
	// then stops driving the channel per tick — the firmware owns the
	// control loop.
	//
	// Channels that advertise this bit MUST also satisfy hal.CurveSink
	// on their backend. The controller programs such a channel once at
	// apply time and re-programs only when the bound curve changes; it
	// does NOT spawn a per-tick PWM loop for it. The capability is
	// orthogonal to CapWritePWM — the same backend may expose per-tick
	// PWM on one card (AMD RDNA1/2) and curve-upload on another
	// (RDNA3/4) (spec-17 PR-1b).
	CapWriteCurve
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
//
// Invariant: when OK=false, every other field MUST be zero. Backends
// are responsible for enforcing this empty-by-construction shape so
// that consumers ignoring OK cannot accidentally use partial state
// (a "valid RPM" on a reading whose PWM read failed is misleading —
// the channel's overall state is unknown). Backends that branch
// internally on per-field failure paths MUST zero the rest of the
// struct before returning when any branch sets OK=false. See #1049.
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

// PowerProfileBackend is an optional capability bolted onto a
// FanBackend for laptops / platforms that expose a board-wide
// power-profile surface in parallel with the per-fan PWM surface —
// msi-ec's shift_mode (eco / comfort / turbo), thinkpad_acpi's
// thermal_mode, asus-wmi's throttle_thermal_policy, etc.
//
// Distinct from FanBackend.Write because the value space is a closed
// set of named modes rather than a 0..255 duty cycle, and writes here
// shape BIOS-managed behaviour (CPU PL1/PL2 limits, the underlying
// fan curve, platform thermal headroom) on top of whatever PWM the
// fan controller is writing.
//
// Consumers SHOULD discover this surface via channel.Caps &
// CapWritePowerProfile and a runtime type-assertion on the backend
// (`if pp, ok := backend.(hal.PowerProfileBackend); ok { ... }`).
//
// Implementations MUST be safe to call from multiple goroutines.
// Writes are unconditional from the kernel's perspective — the
// underlying drivers do not have an acquire/release contract on this
// surface. (#1166).
type PowerProfileBackend interface {
	// AvailablePowerProfiles returns the closed set of profile names
	// the channel accepts for WritePowerProfile, in implementation-
	// defined order (typically low-power → high-power; e.g.
	// {eco, comfort, turbo} on msi-ec).
	AvailablePowerProfiles(ch Channel) ([]string, error)

	// ReadPowerProfile returns the channel's current power profile
	// value verbatim from the kernel surface. Backends that surface
	// unknown raw values (msi-ec emits "unknown (192)" on boards with
	// incomplete CONF_G2_6 mappings) return the raw string so the
	// operator sees what the firmware reports.
	ReadPowerProfile(ch Channel) (string, error)

	// WritePowerProfile commands the channel to a profile value. The
	// value MUST be one of AvailablePowerProfiles; backends return an
	// error otherwise so callers can't accidentally substitute.
	WritePowerProfile(ch Channel, profile string) error
}
