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

Bound: internal/hal/liquid/corsair/safety_test.go:PumpMinimumFloor

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

Bound: internal/hal/liquid/corsair/safety_test.go:ReconnectPumpFloor

---

## RULE-LIQUID-03: ErrReadOnlyUnvalidatedFirmware remains as a defence-in-depth refusal at the Write boundary; production no longer constructs synthetic-read-only Corsair backends post-v0.6.1.

v0.4 wrapped any device with an unrecognised firmware as
`unknownFirmwareDevice`; writes returned `ErrReadOnlyUnvalidatedFirmware`.
v0.6.1 removed the wrapper type and the firmware allowlist entirely per
`feedback-dont-default-writes-off` — Corsair writes now proceed
unconditionally, with safety enforced by the closed-set primitives
(`RULE-LIQUID-01` pump floor, `RULE-LIQUID-02` USB-reconnect floor,
`RULE-LIQUID-04` restore-on-panic, `RULE-LIQUID-05` serialised writes,
`RULE-LIQUID-07` kernel-driver yield).

The `if !b.writable { return ErrReadOnlyUnvalidatedFirmware }` branch
remains in `corsairBackend.Write` as defence-in-depth: a future
re-introduction of a genuine refusal cause (kernel-driver-owns-device
mid-run, known-bad firmware revision surfacing a wedge bug) can set
`writable = false` on a constructed backend without further code
changes. The bound subtest constructs a synthetic-read-only backend
directly (production no longer does) and asserts the refusal contract:
`Write` returns `ErrReadOnlyUnvalidatedFirmware` and issues zero HID
commands.

Bound: internal/hal/liquid/corsair/safety_test.go:UnknownFirmwareReadOnly

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

Bound: internal/hal/liquid/corsair/safety_test.go:RestoreCompletesOnPanic

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

Bound: internal/hal/liquid/corsair/safety_test.go:SerialisedWrites

---

## RULE-LIQUID-06: Corsair Probe returns a writable backend unconditionally post-v0.6.1; the firmware allowlist + --unsafe-corsair-writes gate were removed.

v0.4 ProbeOptions required both the `--unsafe-corsair-writes` operator
flag AND a firmware version on the empty-by-default `firmwareAllowList`
for a device to enter writable mode. v0.6.1 removed both gates per
`feedback-dont-default-writes-off` — the empty-allowlist pattern was
exactly the "ship code, wait for HIL evidence" anti-pattern the rule
forbids. The CLI flag is gone; the allowlist map is gone; the type
split (`liveDevice` / `unknownFirmwareDevice` / `probeClass`) is gone.

Probe now returns a writable `corsairBackend` for any successfully-
handshaken Commander Core / ST device, regardless of firmware version.
Safety is enforced by the closed-set primitives that always were the
load-bearing protection: pump-minimum floor (`RULE-LIQUID-01`), USB-
reconnect pump floor (`RULE-LIQUID-02`), restore-on-panic
(`RULE-LIQUID-04`), per-device serialised writes (`RULE-LIQUID-05`),
and conflicting-kernel-driver yield (`RULE-LIQUID-07`).

The bound subtest exercises three different firmware-version tuples
through `probeWith` and asserts each produces a writable backend.
The earlier "flag=false / fw not listed / both true" three-case
matrix is dead — there is no flag, no allowlist.

Bound: internal/hal/liquid/corsair/safety_test.go:WriteRequiresFlagAndAllowlist

---

## RULE-LIQUID-07: Yield to conflicting kernel drivers

Before opening a hidraw device for a Corsair VID 0x1b1c PID in the ventd
PID table, ventd checks whether a kernel driver currently owns that
device. Detection is broad: any driver bound to /sys/class/hidraw/hidrawN/
device/driver is treated as a conflict, not just a named list like
corsair-cpro. If a conflict is detected, ventd attempts to unbind the
driver from that specific device via
/sys/bus/hid/drivers/<driver>/unbind (one write, no reboot required).
If unbind succeeds or no driver was bound, HID access proceeds normally.
If unbind fails, ventd marks the device unavailable for this run and
logs an actionable error identifying the conflicting driver, the hidraw
path, and the exact blacklist file to create for permanent
remediation (/etc/modprobe.d/ventd.conf with blacklist <driver>).

Rationale: corsair-cpro (mainline, targets Commander Pro) may misidentify
Commander Core devices, and out-of-tree Corsair forks from AUR can claim
Commander Core directly. Simultaneous userspace hidraw access and
kernel-driver access corrupt device state. Auto-unbind is the zero-user-
input remediation path; the error-with-guidance fallback is the escape
hatch when unbind itself is blocked.

Bound: internal/hal/liquid/corsair/safety_test.go:UnbindConflictingDriver
