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
// v0.6.0 ships the code default-off behind `--enable-nbfc-write`
// (mirrors `--enable-gpu-write` / `--unsafe-corsair-writes`). The
// matcher + catalogue surface are always on (read-only); operator
// opt-in is required to actually write to the EC.
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

// ErrNBFCWriteGated is returned by Write / Restore when the operator
// has not passed `--enable-nbfc-write`. The HAL Read path remains
// open in this state so smart-mode telemetry still flows (RPM
// readings, sensor maps). Mirrors `RULE-GPU-PR2D-01`.
var ErrNBFCWriteGated = errors.New("nbfc: write gated; enable with --enable-nbfc-write")

// ErrNBFCNoTransport is returned when Probe could match a config and
// classify it as supported (register-only or ACPI-with-bridge) but
// no EC transport opened cleanly (`internal/ec.ErrECNotAvailable`
// or equivalent). The doctor surface dispatches the modprobe-options
// remediation for ec_sys.
var ErrNBFCNoTransport = errors.New("nbfc: no EC transport available")
