// Package acpi implements the userspace ACPI method invocation
// bridge ventd's NBFC backend uses to drive fans on the 7 upstream
// nbfc-linux configs that declare their fan control via
// `ReadAcpiMethod` / `WriteAcpiMethod` / `AcpiMethod` rather than
// direct EC registers.
//
// Mechanism: writes the method path + arguments to `/proc/acpi/call`
// (provided by the out-of-tree `acpi_call` GPL-2.0+ DKMS module),
// then reads the result back. Same protocol nbfc-linux's
// `src/ec_acpi.c` uses, ported to pure Go.
//
// Safety: a closed-set discipline mirrors `internal/ec`'s register
// allowlist. The caller passes an `AllowedMethods` set derived from
// the matched nbfc config's `AcpiMethodsUsed()`. A method path not
// in the set is refused with `ErrACPIMethodNotInConfig` without
// touching `/proc/acpi/call`.
package acpi

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Errors.
var (
	// ErrACPICallNotLoaded is returned when `/proc/acpi/call` is
	// absent — the `acpi_call` DKMS module isn't loaded. The
	// preflight check + doctor surface dispatch the install path.
	ErrACPICallNotLoaded = errors.New("acpi: /proc/acpi/call missing; install acpi_call DKMS module")

	// ErrACPIMethodNotInConfig is returned when a Call(path, ...)
	// targets a method path not present in the active config's
	// allowlist. Mirrors `ec.ErrECRegisterNotInConfig`.
	ErrACPIMethodNotInConfig = errors.New("acpi: method path not in active config allowlist")

	// ErrACPIResponseUnparseable is returned when /proc/acpi/call
	// returns a response that doesn't match either of the formats
	// `acpi_call` emits (legacy decimal or 0x-prefixed hex).
	ErrACPIResponseUnparseable = errors.New("acpi: response format not recognised")
)

// procACPICallPath is the canonical kernel-userspace bridge path.
// Indirected for test injection.
const procACPICallPath = "/proc/acpi/call"

// procACPICallOpener is the test seam. Production: openACPICallReal.
var procACPICallOpener = openACPICallReal

// openACPICallReal opens /proc/acpi/call O_RDWR. The kernel module
// processes the entire write in one syscall (the method path string)
// and the read returns the result. Each Call invocation must be a
// fresh open + write + read cycle — sharing a handle across calls
// is not supported by acpi_call's protocol.
type acpiCallFile interface {
	Write([]byte) (int, error)
	Read([]byte) (int, error)
	Close() error
}

func openACPICallReal() (acpiCallFile, error) {
	f, err := os.OpenFile(procACPICallPath, os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrACPICallNotLoaded
		}
		return nil, fmt.Errorf("acpi: open %s: %w", procACPICallPath, err)
	}
	return f, nil
}

// Bridge is the per-host ACPI call dispatcher. Allowed holds the
// method path set the matched config has vetted; any Call with a
// path outside the set is refused.
type Bridge struct {
	Allowed map[string]bool
}

// New constructs a Bridge with the given allowlist.
func New(allowed map[string]bool) *Bridge {
	return &Bridge{Allowed: allowed}
}

// Available probes whether the acpi_call module is loaded by
// attempting to open /proc/acpi/call. The probe doesn't issue any
// method invocation — module-load presence is signal enough.
// Returns nil on success; ErrACPICallNotLoaded when the module is
// missing.
func Available() error {
	f, err := procACPICallOpener()
	if err != nil {
		return err
	}
	return f.Close()
}

// Call invokes the named ACPI method with optional uint64 arguments.
// Args are formatted as the acpi_call request format expects (the
// method path optionally followed by space-separated decimal or
// 0x-hex literals). Returns the method's response as a uint64.
//
// Refuses the call without touching /proc/acpi/call when:
//   - the method path is empty
//   - b.Allowed is non-nil and the path isn't in it
func (b *Bridge) Call(method string, args ...uint64) (uint64, error) {
	method = strings.TrimSpace(method)
	if method == "" {
		return 0, fmt.Errorf("acpi: Call: empty method path")
	}
	if b.Allowed != nil && !b.Allowed[method] {
		return 0, fmt.Errorf("%w: %s", ErrACPIMethodNotInConfig, method)
	}
	f, err := procACPICallOpener()
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	// Format: "<method> [arg1] [arg2] ...". acpi_call accepts both
	// decimal and 0x-prefixed hex.
	var sb strings.Builder
	sb.WriteString(method)
	for _, a := range args {
		sb.WriteByte(' ')
		sb.WriteString(strconv.FormatUint(a, 10))
	}
	if _, err := f.Write([]byte(sb.String())); err != nil {
		return 0, fmt.Errorf("acpi: write %s: %w", method, err)
	}

	buf := make([]byte, 1024)
	n, err := f.Read(buf)
	if err != nil {
		return 0, fmt.Errorf("acpi: read response for %s: %w", method, err)
	}
	return parseACPIResponse(buf[:n])
}

// parseACPIResponse handles both acpi_call response shapes:
//   - "0x12345678\0..." — modern, null-terminated hex literal
//   - "12345\0..." — legacy decimal
//   - "Error: not found\0" / "Error: AE_NOT_FOUND" — method missing
//
// Returns ErrACPIResponseUnparseable when the byte buffer doesn't
// match either expected shape, including the error-prefix cases
// (the caller branches on errors.Is to know it's the bridge talking
// rather than a transport failure).
func parseACPIResponse(buf []byte) (uint64, error) {
	// Trim everything from the first null byte (acpi_call returns
	// a null-terminated string).
	for i, b := range buf {
		if b == 0 {
			buf = buf[:i]
			break
		}
	}
	s := strings.TrimSpace(string(buf))
	if s == "" {
		return 0, ErrACPIResponseUnparseable
	}
	if strings.HasPrefix(s, "Error") {
		return 0, fmt.Errorf("%w: %s", ErrACPIResponseUnparseable, s)
	}
	// Hex form: 0x... or 0X...
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, err := strconv.ParseUint(s[2:], 16, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: hex parse %q: %v", ErrACPIResponseUnparseable, s, err)
		}
		return v, nil
	}
	// Decimal form.
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: decimal parse %q: %v", ErrACPIResponseUnparseable, s, err)
	}
	return v, nil
}
