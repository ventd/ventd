# NBFC ACPI bridge rules — spec-09 PR B3

These invariants govern `internal/acpi/`, the userspace ACPI method
invocation bridge that completes catalogue coverage for the 7
upstream nbfc-linux configs that drive fans via firmware-defined
ACPI methods rather than direct EC registers (HP Pavilion 17
Notebook PC, HP 250 G8 Notebook PC, Acer TravelMate P253, ASUSTeK
X551CA, Acer Aspire E1-570G, plus two others).

Mechanism: writes the method path + arguments to `/proc/acpi/call`
(provided by the out-of-tree `acpi_call` GPL-2.0+ DKMS module),
then reads the result. Port of `nbfc-linux/src/ec_acpi.c`.

Each rule binds 1:1 to a subtest.

## RULE-NBFC-ACPI-01: `Bridge.Call` refuses method paths not in the active config's allowlist; refused calls touch nothing.

`acpi.New(allowed)` constructs a bridge with a closed-set allowlist
derived from `nbfc.Config.AcpiMethodsUsed()`. Every `Call(method, ...)`
invocation:

- Trims whitespace and rejects an empty path with a wrapped error
  (no `/proc/acpi/call` touch).
- When `Allowed != nil`, consults the set and rejects with
  `ErrACPIMethodNotInConfig` for a path not present.
- A nil `Allowed` set means "permissive" — useful for tests; the
  production wiring in `internal/hal/nbfc/probe.go` always passes a
  non-nil allowlist drawn from the matched config.

Mirrors `internal/ec`'s register allowlist (`RULE-NBFC-EC-02`):
the catalogue is the closed set; a corrupt config or a regression
in the matcher cannot cause an arbitrary ACPI method invocation.

Bound: internal/acpi/call_test.go:TestRULE_NBFC_ACPI_01_AllowlistGate
Bound: internal/acpi/call_test.go:TestRULE_NBFC_ACPI_01_AllowlistAdmits

## RULE-NBFC-ACPI-02: The response parser handles both legacy-decimal and `0x`-prefixed-hex `acpi_call` response formats; unparseable responses surface a typed error.

`acpi_call`'s response format evolved over years — the
`nix-community/acpi_call` fork emits `0x12345678\0`-shaped strings,
while older builds emit bare decimal `12345\0`. The parser handles
both, trims the trailing null, and skips leading/trailing whitespace.

`Error: AE_NOT_FOUND`-shaped strings (the module-emits-when-method-
missing case) are surfaced as wrapped `ErrACPIResponseUnparseable`
so the caller branches via `errors.Is` to distinguish "kernel
returned a value we can't parse" from a transport / open error.

Bound: internal/acpi/call_test.go:TestRULE_NBFC_ACPI_02_ResponseFormats
Bound: internal/acpi/call_test.go:TestRULE_NBFC_ACPI_02_UnparseableResponseTypedError

## RULE-NBFC-ACPI-03: `Available()` distinguishes "module not loaded" (ENOENT) from "ready to use" (open succeeds); each surfaces a distinct doctor remediation.

`acpi.Available()` attempts to open `/proc/acpi/call`. When the
module isn't loaded the open returns ENOENT, which the path layer
maps to `ErrACPICallNotLoaded`. Callers use `errors.Is` to detect
the missing-module case and dispatch the DKMS install pathway
(`acpi_call` GPL-2.0+ — same shape as `nct6687d` / `legion_laptop`
via the existing DKMS install pipeline).

Bound: internal/acpi/call_test.go:TestRULE_NBFC_ACPI_03_AvailableDistinguishesCauses
Bound: internal/acpi/call_test.go:TestRULE_NBFC_ACPI_03_AvailableSucceedsWhenLoaded

## RULE-NBFC-ACPI-04: The NBFC HAL backend wires an ACPI bridge in `ProbeOpts.ACPI` for configs that invoke methods; New refuses ACPI-using configs cleanly with `ErrNBFCConfigNeedsAcpiBridge` when the bridge is nil.

`internal/hal/nbfc.New` checks `Config.UsesACPI()` at construction:

- ACPI-using config + nil bridge → `ErrNBFCConfigNeedsAcpiBridge`.
  The doctor surface emits the install-path remediation.
- ACPI-using config + wired bridge → admits; subsequent Read /
  Write / Restore dispatch through the bridge instead of (or
  alongside) the EC transport.

The dispatch logic per fan operation:

- `fan.ReadAcpiMethod != ""` → `Read` calls the bridge.
- `fan.WriteAcpiMethod != ""` → `Write` calls the bridge.
- `fan.ResetAcpiMethod != ""` → `Restore` calls the bridge.
- `RegisterWriteConfiguration.WriteMode == "Call"` → reset path
  calls the bridge with the named method.

A config that mixes register + ACPI (some HP Pavilion 17 variants)
is admitted: register fields route to the transport, method fields
route to the bridge. Both surfaces must be wired.

Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_ACPI_04_BackendDispatchesACPI

## RULE-NBFC-ACPI-05: The `acpi_call` request format is `<method> [arg1] [arg2] ...` with decimal-formatted arguments; tests pin the exact wire format.

The bridge formats Call invocations as the upstream `acpi_call`
module's documented request shape: a method path optionally
followed by space-separated decimal integer arguments. The format
matches `nbfc-linux/src/ec_acpi.c`'s emission so operators
comparing ventd's `/proc/acpi/call` traffic against nbfc-linux's
see byte-identical writes.

The wire-format test pins this — a future refactor that re-encodes
arguments (e.g. as hex) would break compatibility with upstream-
known-working method invocations and fail this test before
shipping.

Bound: internal/acpi/call_test.go:TestACPICall_RequestFormat
