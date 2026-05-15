// Package ec implements pure-Go read/write access to the laptop
// embedded controller's register space, supporting two transports:
// the in-tree `ec_sys` debugfs interface (preferred) and direct
// `/dev/port` I/O with the ACPI 4.0 §12.3 OBF/IBF handshake
// (fallback). Both transports are CGO-free.
//
// The package is consumed by `internal/hal/nbfc` (spec-09 PR B2) to
// implement the FanBackend contract on EC-locked laptops; the
// matched `nbfc-linux` config (via `internal/hwdb/nbfc`) supplies
// the per-register protocol details.
//
// Safety: Transport refuses any register address not in the allowlist
// supplied by the active nbfc config (RULE-NBFC-EC-02). The
// allowlist is the closed set of registers the upstream catalogue
// has vetted as safe to touch on this hardware. A corrupted config
// or a regression in the matcher cannot cause an arbitrary EC poke.
package ec

import (
	"errors"
	"fmt"
)

// Errors returned across the transport surface. Errors are sentinel
// values so callers branch via `errors.Is`.
var (
	// ErrECBusy is returned when an EC handshake (OBF/IBF) timed out.
	// The caller should retry once after a short pause; persistent
	// busy state means the EC has wedged and the daemon should refuse
	// to write to it further.
	ErrECBusy = errors.New("ec: handshake timeout — controller busy")

	// ErrECNotAvailable is returned by Available() when neither
	// transport could be opened. The wrapped error chain identifies
	// each transport's specific failure cause; callers use
	// `errors.As(err, &SetupFailures{})` to surface the full picture.
	ErrECNotAvailable = errors.New("ec: no transport available")

	// ErrECRegisterNotInConfig is returned by Read / Write on a
	// register address that does not appear in the active config's
	// register allowlist. This is the closed-set discipline that
	// keeps a corrupt config from doing a wild write.
	ErrECRegisterNotInConfig = errors.New("ec: register not in active config allowlist")

	// ErrECSysWriteSupportDisabled is returned when the `ec_sys`
	// module is loaded but `write_support=1` was not passed. The
	// preflight check surfaces a modprobe-options-write remediation;
	// see RULE-NBFC-EC-04.
	ErrECSysWriteSupportDisabled = errors.New("ec: ec_sys.write_support is not set; load module with `ec_sys.write_support=1`")
)

// Transport is the narrow interface every EC backend implements.
// 8-bit and 16-bit register access are both surfaced because the
// upstream nbfc-linux schema permits either via the ReadWriteWords
// boolean. The implementations encode multi-byte values little-
// endian, matching `nbfc-linux`'s `ec_read_word` / `ec_write_word`
// helpers.
type Transport interface {
	// Read returns the 8-bit value at the named EC register. Errors:
	//   - ErrECRegisterNotInConfig: address rejected by the active
	//     allowlist; no I/O performed.
	//   - ErrECBusy: handshake timed out (transient on /dev/port;
	//     should not occur on ec_sys).
	//   - other: underlying I/O error wrapped with context.
	Read(reg uint8) (uint8, error)

	// Write writes the 8-bit value to the named EC register. Same
	// error surface as Read.
	Write(reg uint8, val uint8) error

	// Read16 returns the little-endian 16-bit value at the named EC
	// register (i.e. byte at reg is low, reg+1 is high).
	Read16(reg uint8) (uint16, error)

	// Write16 writes the little-endian 16-bit value at the named EC
	// register.
	Write16(reg uint8, val uint16) error

	// Name identifies this transport in logs ("ec_sys" or "dev_port").
	Name() string

	// Close releases backend-level resources (file handles, etc.).
	// Idempotent.
	Close() error
}

// SetupFailures captures the chain of attempts Available() made when
// neither transport opened cleanly. Each field is non-nil on a
// specific failure cause; callers render whichever applies.
type SetupFailures struct {
	ECSys   error // why ec_sys couldn't open
	DevPort error // why /dev/port couldn't open
}

// Error implements error.
func (f *SetupFailures) Error() string {
	return fmt.Sprintf("ec: no transport opened (ec_sys: %v; /dev/port: %v)", f.ECSys, f.DevPort)
}

// Is satisfies errors.Is dispatching to the wrapped sentinel.
func (f *SetupFailures) Is(target error) bool {
	return target == ErrECNotAvailable
}

// withAllowlist wraps a Transport in a register-allowlist gate. Each
// Read / Write / Read16 / Write16 call consults `allowed` (a set of
// register addresses derived from the matched nbfc config) and
// rejects unrecognised addresses with ErrECRegisterNotInConfig.
// 16-bit operations check both the requested register and its +1
// neighbour, since the underlying read/write touches two bytes.
type withAllowlist struct {
	inner   Transport
	allowed map[uint8]bool
}

// WithAllowlist wraps t in a register-allowlist gate. The allowed
// set comes from `nbfc.Config.RegistersUsed()` (added in PR B2);
// passing an empty or nil set refuses every register access.
func WithAllowlist(t Transport, allowed map[uint8]bool) Transport {
	return &withAllowlist{inner: t, allowed: allowed}
}

func (w *withAllowlist) Name() string { return w.inner.Name() }
func (w *withAllowlist) Close() error { return w.inner.Close() }

func (w *withAllowlist) Read(reg uint8) (uint8, error) {
	if !w.allowed[reg] {
		return 0, fmt.Errorf("%w: read %#x", ErrECRegisterNotInConfig, reg)
	}
	return w.inner.Read(reg)
}

func (w *withAllowlist) Write(reg uint8, val uint8) error {
	if !w.allowed[reg] {
		return fmt.Errorf("%w: write %#x", ErrECRegisterNotInConfig, reg)
	}
	return w.inner.Write(reg, val)
}

func (w *withAllowlist) Read16(reg uint8) (uint16, error) {
	if !w.allowed[reg] || !w.allowed[reg+1] {
		return 0, fmt.Errorf("%w: read16 %#x", ErrECRegisterNotInConfig, reg)
	}
	return w.inner.Read16(reg)
}

func (w *withAllowlist) Write16(reg uint8, val uint16) error {
	if !w.allowed[reg] || !w.allowed[reg+1] {
		return fmt.Errorf("%w: write16 %#x", ErrECRegisterNotInConfig, reg)
	}
	return w.inner.Write16(reg, val)
}
