# NBFC EC transport rules — spec-09 PR B1

These invariants govern `internal/ec/`, the pure-Go EC register
transport that PR B2 sits on. No CGO. Two transports: `ec_sys`
debugfs (preferred) and `/dev/port` with the ACPI 4.0 §12.3
OBF/IBF handshake (fallback). Both register-allowlist gated.

Each rule binds 1:1 to a subtest. `tools/rulelint` blocks the merge
if a rule lacks its bound test.

## RULE-NBFC-EC-01: `Available()` prefers `ec_sys` over `/dev/port`; both-failed surfaces both causes via the wrapped `*SetupFailures`.

`internal/ec.Available()` tries `openECSys()` first; on clean open
it returns the ec_sys transport. On ec_sys failure (kernel module
unloaded, `write_support=0`, permission denied), `openDevPort()`
runs as the fallback. On both-failed, the function returns an
error chain whose `errors.Is(_, ErrECNotAvailable)` is true and
whose error string names BOTH transport-specific failure causes
so the operator-facing doctor card / journal trail can show which
fix path applies (modprobe-options-write for ec_sys; check capabilities
+ rawio for /dev/port).

ec_sys is preferred because the kernel handles the OBF/IBF
handshake internally — one syscall per byte. `/dev/port` requires
four syscalls per byte (status poll, command, data-addr, data-byte)
plus busy-wait loops; equivalent functionally but ~4x heavier on
the read-side.

Bound: internal/ec/preflight_test.go:TestAvailable_PrefersECSys
Bound: internal/ec/preflight_test.go:TestAvailable_FallsBackToDevPort
Bound: internal/ec/preflight_test.go:TestAvailable_BothFailReturnsCombinedError

## RULE-NBFC-EC-02: Every `Transport.Read` / `Write` / `Read16` / `Write16` validates the register address against the active config's allowlist; rejected registers return `ErrECRegisterNotInConfig` without touching the EC.

`WithAllowlist(t, allowed)` wraps a raw transport in the closed-set
gate. Every byte-level call consults `allowed[reg]` BEFORE issuing
any I/O; an unrecognised address returns `ErrECRegisterNotInConfig`
wrapping the rejected register number for diagnostics, with NO
side effect on the underlying device. 16-bit operations check both
`reg` and `reg+1` since they touch two bytes.

The closed-set discipline is load-bearing: it prevents a corrupted
nbfc config or a regression in the matcher from doing a wild EC
poke. The catalogue is the source of truth; ventd never probes
registers the catalogue hasn't named as safe to touch on this
hardware.

Bound: internal/ec/preflight_test.go:TestWithAllowlist_RejectsUnknown
Bound: internal/ec/preflight_test.go:TestWithAllowlist_Read16RequiresBothBytes

## RULE-NBFC-EC-03: `/dev/port` transport honours the ACPI 4.0 §12.3 OBF/IBF handshake with a 1 ms per-step deadline; deadline elapsed surfaces `ErrECBusy` without spinning forever.

The `devPortTransport.waitStatus` helper polls the EC status byte
(read from port 0x66) at `pollInterval` (10 µs) until the named
bit (OBF for read, IBF for write) matches the expected state.
A wedged EC that never advances must NOT busy-spin the daemon
indefinitely; the deadline at `pollTimeout` (1 ms) bounds each
step and returns `ErrECBusy` to the caller.

Both `pollInterval` and `pollTimeout` mirror the values upstream
`nbfc-linux/src/ec_linux.c` uses; matching upstream's behaviour
means the same operational characteristics across both daemons
(useful for operators who compare ventd + nbfc-linux behaviour
on the same laptop during transition).

The READ_EC (0x80) and WRITE_EC (0x81) command bytes also match
the ACPI 4.0 specification; the bound subtest pins the command
sequence so a regression in either constant or in the protocol
order (cmd → addr → [data]) fails CI rather than silently
mis-handshaking with the EC.

Bound: internal/ec/dev_port_test.go:TestDevPort_ReadWriteRoundTrip
Bound: internal/ec/dev_port_test.go:TestDevPort_CommandSequenceIsCorrect
Bound: internal/ec/dev_port_test.go:TestDevPort_HandshakeTimeoutSurfacesErrECBusy

## RULE-NBFC-EC-04: `ec_sys` transport refuses when `/sys/module/ec_sys/parameters/write_support` is "N"; the open-time check returns `ErrECSysWriteSupportDisabled` so the preflight surface can dispatch the modprobe-options-write remediation.

`openECSysReal()` reads the kernel's exposed `write_support`
parameter file before attempting to open `/sys/kernel/debug/ec/ec0/io`
RDWR. The kernel's `ec_sys` module loads with `write_support=0`
by default; without the parameter set to 1 every WriteAt would
silently no-op (the kernel returns success but does nothing). We
catch that case up-front and surface a typed error so the
preflight check + the operator-facing doctor card can dispatch
the existing modprobe-options-write endpoint (`RULE-MODPROBE-OPTIONS-01`)
with the `ec_sys → write_support=1` allowlist row (added in this PR).

The allowlist row's binding is exercised by the existing
`internal/hwmon/modprobe_options_test.go::TestIsAllowedModprobeOption`
table; three new rows pin the positive admit, the disabled-value
reject, and the extra-option reject.

Bound: internal/ec/ec_sys_test.go:TestECSys_WriteSupportDisabledPropagates

The `internal/hwmon/modprobe_options_test.go::TestIsAllowedModprobeOption`
table is the cross-cutting binding home for `RULE-MODPROBE-OPTIONS-01`;
the three new ec_sys-related rows extend that test rather than rebinding it.

## RULE-NBFC-EC-05: 16-bit register access uses little-endian byte ordering across two consecutive registers, matching `nbfc-linux/src/ec_linux.c::ec_read_word` / `ec_write_word`.

Both transports MUST encode `Write16(reg, val)` as a low-byte
write to `reg` followed by a high-byte write to `reg+1`, and
`Read16(reg)` as `Read(reg)` low followed by `Read(reg+1)` high.
Endianness matters for upstream-catalogue parity: a config
authored against nbfc-linux declares 16-bit values in upstream's
convention, and a byte-order disagreement would silently produce
wrong RPM readings + wrong fan speeds.

`ec_sys` implements this directly via ReadAt/WriteAt at offset
`reg` with a 2-byte buffer. `/dev/port` implements it via two
sequential 8-bit ops.

Bound: internal/ec/ec_sys_test.go:TestECSys_LittleEndianWords
Bound: internal/ec/dev_port_test.go:TestDevPort_LittleEndianWord16

## RULE-NBFC-EC-06: `Transport.Close` is idempotent; double-close returns nil without panicking.

Both `ecSysTransport.Close` and `devPortTransport.Close` set the
inner file handle to nil after closing so a second invocation
short-circuits to a clean return. The daemon's HAL backend layer
ships deferred Close calls along multiple paths (graceful
shutdown, panic recovery, ctx-cancel); double-close must not
panic or surface a misleading "use of closed file" error to the
operator.

Bound: internal/ec/ec_sys_test.go:TestECSys_CloseIdempotent
