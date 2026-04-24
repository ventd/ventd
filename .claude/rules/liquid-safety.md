# liquid-safety — invariant bindings for the Corsair AIO backend

**Package:** `internal/hal/liquid/corsair/`
**Spec:** `specs/spec-02-corsair-aio.md`
**Enforced by:** `tools/rulelint`

Every rule below is 1:1 with a subtest in the Corsair safety test. rulelint
fails the build if a rule lacks a corresponding subtest or if a subtest
drifts from its bound rule.

---

## RULE-LIQUID-01: Pump-channel PWM never falls below pump_minimum

Channel 0 on Commander Core and Commander ST is the pump channel. Its duty
cycle must never be written below `pump_minimum` (default 50). The floor is
config-overridable per device but cannot be set to zero. Enforcement lives
in the HAL write path, not in the controller — a misbehaving controller
that emits a zero duty cycle must not be able to stall the pump via the
backend. A stalled pump stops coolant circulation; sustained heat load
without active liquid cooling can destroy CPU and VRM components within
seconds.

Bound: internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/PumpMinimumFloor

---

## RULE-LIQUID-02: USB disconnect mid-write never leaves the pump below pump_minimum

If the USB link drops while a write command is in flight, the pump channel
may be left at an indeterminate duty cycle. On reconnect, the first action
the backend takes is a write-pump-to-safe-floor command before any other
command sequence resumes. This guarantee holds regardless of what duty
cycle was requested before the disconnect and regardless of whether the
in-flight write completed on the device side. Skipping the floor write on
reconnect would allow the pump to remain at whatever speed the firmware
defaulted to on reset, which may be below pump_minimum on some Commander
Core firmware versions.

Bound: internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/ReconnectPumpFloor

---

## RULE-LIQUID-03: Unknown firmware is read-only

A firmware version not present on the allow-list (which is empty for
v0.4.0) causes the adapter to wrap the probed device as an
`unknownFirmwareDevice`. Any call to `SetDuty` or `SetCurve` on that
wrapper returns `ErrReadOnlyUnvalidatedFirmware`. Read operations — fan
speeds, coolant temperature, connected-state — proceed normally. The
enforcement point is the HAL adapter boundary, not a runtime flag inside
the corsair package; the type split between `liveDevice` and
`unknownFirmwareDevice` makes the read-only constraint compile-time for
code inside the package. Sending unvalidated write commands to an unknown
firmware risks leaving the device in a state that requires iCUE to recover.

Bound: internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/UnknownFirmwareReadOnly

---

## RULE-LIQUID-04: Restore completes even on panic

`Restore()` must return every channel to firmware curve mode before the HID
handle closes. Each channel's restore call is wrapped in a deferred
function; a panic partway through the restore sequence does not skip
un-restored channels because deferred calls run in reverse registration
order and the HID handle close is deferred last. A partial restore — where
a panic after channel N aborts channels N+1..end — leaves those fans at the
daemon's last written duty cycle, potentially at low speed, after daemon
exit. The deferred structure is the same pattern as the hwmon watchdog's
per-entry recover loop.

Bound: internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/RestoreCompletesOnPanic

---

## RULE-LIQUID-05: Concurrent writes to a single device are forbidden

Only one HID command transfer may be in flight per device at a time. A
per-device mutex is held for the full command-plus-response round-trip and
released only after the response has been read and validated. Commander
Core firmware does not queue concurrent commands; a second write that
arrives while the device is processing a first command corrupts the
sequence counter and can produce a wedged device that stops responding
until USB reset. The mutex is per-device instance, not per-channel, because
all channels share one HID endpoint.

Bound: internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/SerialisedWrites

---

## RULE-LIQUID-06: Writable mode requires both the unsafe flag and an allow-listed firmware

A Corsair device enters writable mode only when both conditions are true:
(a) the operator passed `--unsafe-corsair-writes` on the ventd command
line, and (b) the device's firmware version is present on the allow-list,
which is empty for v0.4.0. Either condition false causes the device to be
wrapped as `unknownFirmwareDevice`; writes return
`ErrReadOnlyUnvalidatedFirmware`. Both conditions are checked at `Probe`
time and the result is a compile-time type, not a runtime flag inside the
backend. Because the v0.4.0 allow-list is empty, every real device shipped
in v0.4.0 is read-only regardless of the flag; the flag exists so that the
gate mechanism is exercised in tests before any firmware version is added to
the list.

Bound: internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/WriteRequiresFlagAndAllowlist
