# NBFC HAL backend rules — spec-09 PR B2

These invariants govern `internal/hal/nbfc/`, the HAL backend that
sits on `internal/ec` (PR B1) + the matched `internal/hwdb/nbfc`
Config (PR A) + the optional `internal/acpi` bridge (PR B3). The
backend satisfies the `hal.FanBackend` contract; everything
downstream (watchdog Restore-on-exit, calibration, smart-mode,
doctor) Just Works because the contract is the contract.

v0.6.0 ships the backend *code* universally, but EC writes are
gated behind `--enable-nbfc-write` (mirrors `--enable-gpu-write`
per `RULE-GPU-PR2D-01`). The catalogue match + Read paths are
always on; operator opt-in is required to actually drive the EC.

Each rule binds 1:1 to a subtest.

## RULE-NBFC-HAL-01: `Enumerate` returns one `hal.Channel` per upstream `FanConfiguration`; capability set is `CapRead | CapWritePWM | CapRestore`; `Role` is inferred from `FanDisplayName`.

`Backend.buildChannels` walks `Config.FanConfigurations` exactly
once at New time and stores the result; `Enumerate` returns a
copy. The deterministic shape across calls is the hal contract
(`RULE-HAL-001` idempotence + `RULE-HAL-005` caps-stable +
`RULE-HAL-006` role-deterministic).

Role inference: substring match on the FanDisplayName picks
`RoleCPU` / `RoleGPU` / `RolePump` / `RoleCase`; unknown
substrings fall to `RoleUnknown`. The upstream naming convention
(`"CPU Fan"`, `"GPU Fan"`, `"Pump"`, `"Case Fan 1"`) is consistent
enough that this works without a per-model override table.

Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_HAL_01_EnumerateOneChannelPerFan

## RULE-NBFC-HAL-02: `Write` clamps + scales 0-255 PWM into `[MinSpeedValue, MaxSpeedValue]` register space; `Read` reverses the scaling using read-side bounds when `IndependentReadMinMaxValues=true`.

`pwmToRegister(pwm, fan)` linearly interpolates the input byte
between `MinSpeedValue` and `MaxSpeedValue`, with `FanSpeedPercentageOverrides`
taking precedence for sparse mappings (e.g. percentage 0 → a
specific "fan off" byte that isn't zero on some HP Omens).
`registerToPWM` is the inverse; when `IndependentReadMinMaxValues`
is true it uses `MinSpeedValueRead` / `MaxSpeedValueRead` instead.

The scaling guarantees a Read that follows a Write returns the
same PWM byte (or close — uint8 rounding) so the controller's
sense-and-write loop produces stable behaviour.

Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_HAL_02_WriteScalesAndWrites
Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_HAL_02_ReadReversesScaling

## RULE-NBFC-HAL-03: `Restore` writes `FanSpeedResetValue` for every fan with `ResetRequired=true`, and applies every `RegisterWriteConfiguration` with `ResetRequired=true` using its `ResetValue` / `ResetWriteMode`.

The Restore path is the watchdog's safety net (`RULE-WD-RESTORE-EXIT`):
every channel returns to firmware-managed state on daemon exit.
The nbfc schema declares two restore surfaces — per-fan
(`FanSpeedResetValue`) and per-register-write (`ResetValue`); both
are applied in order. `WriteMode` semantics (`Set` / `And` / `Or`)
are honoured on the reset path via `applyRegisterReset`.

When `--enable-nbfc-write` is off, `Restore` is a clean no-op —
there's nothing to restore because we never wrote anything.

Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_HAL_03_RestoreWritesResetValue

## RULE-NBFC-HAL-04: Lua-using configs are refused at construction (`ErrNBFCConfigNeedsLuaRuntime`); ACPI-using configs are refused only when the ACPI bridge isn't wired (`ErrNBFCConfigNeedsAcpiBridge`).

`New(ProbeOpts)` checks the matched config's `UsesLua()` and
`UsesACPI()` flags before constructing. Lua is structurally
refused in v0.8.0 (no runtime) — even a wired bridge can't admit
a Lua-using config. ACPI configs are admitted iff `opts.ACPI` is
non-nil; the bridge's allowlist (from `Config.AcpiMethodsUsed()`)
gates every method invocation per `RULE-NBFC-ACPI-01`.

A "Mixed" config that uses both ACPI methods AND EC registers
(some HP Pavilion 17 variants do) requires both surfaces:
`Transport` for the register operations, `ACPI` for the methods.
Each fan operation routes to the appropriate dispatch based on
which field is set in its FanConfiguration.

Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_HAL_04_LuaConfigRefusedAtNew
Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_HAL_04_ACPIConfigRefusedWhenBridgeNil
Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_HAL_04_ACPIConfigAdmitsWhenBridgeWired

## RULE-NBFC-HAL-05: `ReadWriteWords=true` triggers 16-bit register access (`Read16` / `Write16`); 8-bit access otherwise. Little-endian byte ordering matches upstream `nbfc-linux`'s `ec_read_word` / `ec_write_word`.

The schema's `ReadWriteWords` boolean selects between two-byte and
one-byte register access for every fan operation. The 26 upstream
configs that opt into 16-bit access (Acer Predator, some MSI
gaming laptops) use this for higher-resolution fan-speed RPM
encoding. Endianness must match upstream (`RULE-NBFC-EC-05`); a
byte-order disagreement silently produces wrong RPM readings and
wrong fan speeds.

Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_HAL_05_Words16Routes

## RULE-NBFC-HAL-WRITE-GATE: `Write` returns `ErrNBFCWriteGated` when `--enable-nbfc-write` is off; `Read` continues to work; `Restore` is a clean no-op.

v0.6.0 ships the backend default-off, matching the existing
`--enable-gpu-write` / `--unsafe-corsair-writes` pattern. The
operator-visible surface (the doctor card naming the matched
upstream config) appears universally; actual EC writes require
the explicit opt-in flag. This is the HIL-coverage gate while
laptop hardware enters the fleet — once a specific model has
field-validation evidence, that model can be unlocked per-config
in a future release.

The Read path stays open so smart-mode telemetry (catalogue
recognition, future RPM-via-tach when nbfc adds tach support)
continues to work for monitor-only mode. Restore is a no-op
because there's nothing to undo — the daemon never wrote.

Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_HAL_WriteGated
