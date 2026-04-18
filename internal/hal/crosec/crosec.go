// Package crosec is the Chrome OS Embedded Controller fan backend.
//
// Detection is gated on /dev/cros_ec being present AND a successful
// EC_CMD_HELLO response.  On any system without a cros_ec char device
// Enumerate returns an empty slice with no error.
//
// Fan control: EC_CMD_PWM_GET_FAN_TARGET_RPM (0x0020) for RPM reads,
// EC_CMD_PWM_SET_FAN_DUTY (0x0024, v0) for duty-cycle writes (0-100 %),
// and EC_CMD_THERMAL_AUTO_FAN_CTRL (0x0052) to hand control back to EC
// firmware on Restore.
//
// Command numbers from include/linux/platform_data/cros_ec_commands.h
// (kernel v6.17; stable since v4.4).
package crosec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ventd/ventd/internal/hal"
)

// BackendName is the registry tag applied to channels produced by this backend.
const BackendName = "crosec"

// EC command numbers (cros_ec_commands.h, kernel v6.17).
const (
	ecCmdHello              uint32 = 0x0001 // EC_CMD_HELLO
	ecCmdPWMGetFanTargetRPM uint32 = 0x0020 // EC_CMD_PWM_GET_FAN_TARGET_RPM
	ecCmdPWMSetFanDuty      uint32 = 0x0024 // EC_CMD_PWM_SET_FAN_DUTY (v0: percent uint32)
	ecCmdThermalAutoFanCtrl uint32 = 0x0052 // EC_CMD_THERMAL_AUTO_FAN_CTRL
)

// helloMagic: response.out_data == request.in_data + 0x01020304.
const helloMagic uint32 = 0x01020304

// helloCookie is the arbitrary in_data sent in EC_CMD_HELLO.
const helloCookie uint32 = 0xEC000001

// maxConsecutiveFailures is the lockout threshold: after this many successive
// Write failures the backend calls Restore and emits a structured log event.
const maxConsecutiveFailures = 5

// CROS_EC_DEV_IOCXCMD = _IOWR(0xEC, 0, struct cros_ec_command)
// struct cros_ec_command fixed part = 5 × uint32 = 20 bytes.
// _IOWR(type, nr, size) = (3 << 30) | (size << 16) | (type << 8) | nr
const ioctlCmd uintptr = (3 << 30) | (20 << 16) | (0xEC << 8)

// maxPayload caps the fixed-size data region in ecBuf. Raise if any EC
// command added here needs larger payloads; current ceiling is 4 bytes.
const maxPayload = 64

// ecBuf matches the memory layout of struct cros_ec_command plus a fixed
// payload region, passed by address to SYS_IOCTL.
type ecBuf struct {
	version uint32
	command uint32
	outsize uint32
	insize  uint32
	result  uint32
	data    [maxPayload]byte
}

// SendFunc is the low-level EC command dispatch type.
// out is the host→EC payload (may be nil); in is pre-allocated and must
// be filled with the EC→host response by the function.
// Used in tests via the injected sender; production uses the ioctl path.
type SendFunc func(cmd, ver uint32, out, in []byte) error

// State is the per-channel payload carried in hal.Channel.Opaque.
type State struct {
	FanIdx uint8 // zero-based fan index (v0 commands always use index 0)
}

// Backend is the CrOS EC implementation of hal.FanBackend.
type Backend struct {
	logger  *slog.Logger
	devPath string
	send    SendFunc // nil → real ioctl via devPath; non-nil in tests

	mu       sync.Mutex
	failures int // consecutive Write failures; reset on success
}

// NewBackend constructs a Backend that targets /dev/cros_ec.
func NewBackend(logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{logger: logger, devPath: "/dev/cros_ec"}
}

// Name returns the registry tag.
func (b *Backend) Name() string { return BackendName }

// Close is a no-op — the backend opens and closes /dev/cros_ec per call.
func (b *Backend) Close() error { return nil }

// Enumerate verifies EC_CMD_HELLO and returns a single fan channel (fan 0).
// Returns an empty slice — not an error — when the device is absent or HELLO fails.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	resp := make([]byte, 4)
	if err := b.ecCall(ecCmdHello, 0, uint32LE(helloCookie), resp); err != nil {
		b.logger.Info("crosec: backend disabled", "reason", err)
		return nil, nil
	}
	got := binary.LittleEndian.Uint32(resp)
	if want := helloCookie + helloMagic; got != want {
		b.logger.Warn("crosec: HELLO mismatch, backend disabled",
			"got", fmt.Sprintf("0x%08x", got),
			"want", fmt.Sprintf("0x%08x", want))
		return nil, nil
	}

	return []hal.Channel{{
		ID:     "0",
		Role:   hal.RoleUnknown,
		Caps:   hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		Opaque: State{FanIdx: 0},
	}}, nil
}

// Read returns the current fan RPM via EC_CMD_PWM_GET_FAN_TARGET_RPM.
// Reading.PWM is left zero — the EC protocol has no fan-specific duty-cycle
// read command in v0.
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	if _, err := stateFrom(ch); err != nil {
		return hal.Reading{}, err
	}
	resp := make([]byte, 4)
	if err := b.ecCall(ecCmdPWMGetFanTargetRPM, 0, nil, resp); err != nil {
		return hal.Reading{OK: false}, nil
	}
	rpm := binary.LittleEndian.Uint32(resp)
	if rpm > 0xFFFF {
		rpm = 0xFFFF
	}
	return hal.Reading{RPM: uint16(rpm), OK: true}, nil
}

// Write sets the fan duty cycle via EC_CMD_PWM_SET_FAN_DUTY (v0).
// The HAL interface uses 0-255; EC expects 0-100 %, so we scale.
// After maxConsecutiveFailures errors, Restore is called and a structured
// event is logged.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	if _, err := stateFrom(ch); err != nil {
		return err
	}
	percent := uint32(pwm) * 100 / 255
	err := b.ecCall(ecCmdPWMSetFanDuty, 0, uint32LE(percent), nil)

	b.mu.Lock()
	if err != nil {
		b.failures++
		if b.failures >= maxConsecutiveFailures {
			fails := b.failures
			b.failures = 0 // reset so next burst gets a fresh 5-count window
			b.mu.Unlock()
			b.logger.Error("crosec: consecutive write failures, restoring EC auto mode",
				"failures", fails, "channel", ch.ID)
			_ = b.Restore(ch)
			return err
		}
		b.mu.Unlock()
		return err
	}
	b.failures = 0
	b.mu.Unlock()
	return nil
}

// Restore sends EC_CMD_THERMAL_AUTO_FAN_CTRL to hand fan control back to
// the EC firmware.  Safe to call on a channel that was never written to.
func (b *Backend) Restore(ch hal.Channel) error {
	if _, err := stateFrom(ch); err != nil {
		return err
	}
	return b.ecCall(ecCmdThermalAutoFanCtrl, 0, nil, nil)
}

// ecCall dispatches an EC command via the injected SendFunc (tests) or the
// real ioctl path (production).  out is the host→EC payload; in is
// pre-allocated and filled with the EC→host response.
func (b *Backend) ecCall(cmd, ver uint32, out, in []byte) error {
	if b.send != nil {
		return b.send(cmd, ver, out, in)
	}
	return b.ioctlCall(cmd, ver, out, in)
}

// ioctlCall opens /dev/cros_ec, issues CROS_EC_DEV_IOCXCMD, and closes it.
// ENOENT/ENODEV are normalised to a descriptive error so Enumerate can
// treat device-absent as a silent no-op.
func (b *Backend) ioctlCall(cmd, ver uint32, out, in []byte) error {
	fd, err := syscall.Open(b.devPath, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("crosec: open %s: %w", b.devPath, err)
	}
	defer func() { _ = syscall.Close(fd) }()

	var buf ecBuf
	buf.version = ver
	buf.command = cmd
	buf.outsize = uint32(len(out))
	buf.insize = uint32(len(in))
	if len(out) > 0 {
		copy(buf.data[:], out)
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(fd), ioctlCmd, uintptr(unsafe.Pointer(&buf)))
	if errno != 0 {
		return fmt.Errorf("crosec: ioctl 0x%04x: %w", cmd, errno)
	}
	if buf.result != 0 {
		return fmt.Errorf("crosec: EC result %d for cmd 0x%04x", buf.result, cmd)
	}
	if len(in) > 0 {
		n := len(in)
		if n > maxPayload {
			n = maxPayload
		}
		copy(in, buf.data[:n])
	}
	return nil
}

// stateFrom coerces a Channel's Opaque into State.
func stateFrom(ch hal.Channel) (State, error) {
	switch v := ch.Opaque.(type) {
	case State:
		return v, nil
	case *State:
		if v == nil {
			return State{}, errors.New("hal/crosec: nil opaque state")
		}
		return *v, nil
	default:
		return State{}, fmt.Errorf("hal/crosec: channel %q has wrong opaque type %T", ch.ID, ch.Opaque)
	}
}

// uint32LE encodes v as a 4-byte little-endian slice.
func uint32LE(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}
