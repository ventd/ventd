// Package nbfc implements the HAL backend that drives laptop fans
// via the embedded controller, using the upstream nbfc-linux
// catalogue (`internal/hwdb/nbfc`) for per-model register / method
// metadata and `internal/ec` for the EC transport.
//
// The package satisfies the `hal.FanBackend` contract; everything
// downstream (watchdog Restore-on-exit, calibration, smart-mode
// confidence aggregator, doctor card) Just Works because the
// contract is the contract.
//
// Writes are unconditional once the matcher resolves a catalogue
// entry and the EC transport opens. The closed-set register
// allowlist (RULE-NBFC-EC-02), the upstream-vetted catalogue's
// per-model register map, and the existing RULE-IDLE-02 (battery)
// + RULE-IDLE-03 (container) hard refuses are the safety
// mechanism — there is no extra --enable-nbfc-write gate. See
// `feedback-dont-default-writes-off` in auto-memory for rationale.
package nbfc

import "errors"

// ErrNBFCNoMatch is returned by Probe when the live DMI doesn't
// resolve to any upstream catalogue entry. Not a fatal error — the
// daemon proceeds without the NBFC backend and the doctor card
// surfaces the contribution invite (`RULE-NBFC-DOCTOR-01`).
var ErrNBFCNoMatch = errors.New("nbfc: no upstream catalogue match for this DMI")

// ErrNBFCConfigNeedsLuaRuntime is returned by Probe when the matched
// config uses Lua. v0.8.0 doesn't ship a Lua runtime; the operator
// sees a refusal here + a Warning doctor card explaining the
// limitation.
var ErrNBFCConfigNeedsLuaRuntime = errors.New("nbfc: matched config requires Lua runtime (refused in v0.8.0)")

// ErrNBFCConfigNeedsAcpiBridge is returned by Probe when the matched
// config uses ACPI methods and the `acpi_call` DKMS module isn't
// available. PR B3 ships the ACPI bridge; the preflight surfaces the
// install path. Surfaced as a Warning doctor card on hosts where the
// bridge is missing.
var ErrNBFCConfigNeedsAcpiBridge = errors.New("nbfc: matched config requires acpi_call DKMS module (install via wizard)")

// ErrNBFCNoTransport is returned when Probe could match a config and
// classify it as supported (register-only or ACPI-with-bridge) but
// no EC transport opened cleanly (`internal/ec.ErrECNotAvailable`
// or equivalent). The doctor surface dispatches the modprobe-options
// remediation for ec_sys.
var ErrNBFCNoTransport = errors.New("nbfc: no EC transport available")
