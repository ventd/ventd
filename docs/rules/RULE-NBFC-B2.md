# NBFC HAL backend rules — spec-09 PR B2

These invariants govern `internal/hal/nbfc/`, the HAL backend that
sits on `internal/ec` (PR B1) + the matched `internal/hwdb/nbfc`
Config (PR A) + the optional `internal/acpi` bridge (PR B3). The
backend satisfies the `hal.FanBackend` contract; everything
downstream (watchdog Restore-on-exit, calibration, smart-mode,
doctor) Just Works because the contract is the contract.

v0.6.0 shipped the backend behind a `--enable-nbfc-write` HIL-evidence
gate; v0.6.1 removed it per `feedback-dont-default-writes-off`. The
catalogue match + Read paths AND the Write / Restore paths are now
all on universally. The closed-set register allowlist
(`RULE-NBFC-EC-02`), the upstream-vetted catalogue, and the existing
idle/battery/container refuses (`RULE-IDLE-02`, `RULE-IDLE-03`) are
the safety mechanism — no operator opt-in flag is required.

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

## RULE-NBFC-HAL-DEFAULT-WRITES-ON: `Write` and `Restore` proceed unconditionally once `Backend.New` succeeds; there is no extra `--enable-nbfc-write` operator-opt-in flag.

The closed-set register allowlist (`RULE-NBFC-EC-02`), the
upstream-vetted nbfc-linux catalogue's per-model register map,
the existing `RULE-IDLE-02` (battery refuse) and `RULE-IDLE-03`
(container refuse) hard gates, and the watchdog's `Restore`-on-
exit contract (`RULE-WD-RESTORE-EXIT`) are the safety mechanism.
An additional per-backend opt-in flag pending HIL evidence
contradicts ventd's framing ("install, open browser, click Apply
— ventd handles the rest") and produces zero operator value:
every laptop user would see "your hardware is recognised but you
can't actually use it" and either give up or hand-edit the flag.
See `feedback-dont-default-writes-off` in auto-memory for the
broader rationale.

The earlier `RULE-NBFC-HAL-WRITE-GATE` framing + `ErrNBFCWriteGated`
sentinel landed in v0.6.0 and were removed in the v0.6.1 follow-up
alongside the Corsair firmware-allowlist gate (`RULE-LIQUID-03` /
`RULE-LIQUID-06`) — both were HIL-style "ship code, wait for
evidence to flip" gates with no genuine safety justification
beyond the prior catalog/allowlist surface.

Bound: internal/hal/nbfc/backend_test.go:TestRULE_NBFC_HAL_DEFAULT_WRITES_ON
