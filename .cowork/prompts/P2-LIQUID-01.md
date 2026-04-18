You are Claude Code, working on the ventd repository.

## Task
ID: P2-LIQUID-01
Track: LIQUID
Goal: Liquid-cooler (AIO) backend using USB HID. First-wave vendor: Corsair Commander Core / Commander Core XT / H-series iCUE AIOs. Ships the core AIO fan/pump control path and a device protocol module for Corsair. Depends on `P2-USB-BASE` (the USB HID primitives from Wave 1).

## Care level
HIGH. AIO pumps have a minimum safe RPM below which the CPU can overheat within seconds. Pump control writes MUST clamp to vendor-specified minimums. Every liquid backend write path MUST preserve the pump-floor invariant inherited from `RULE-HWMON-PUMP-FLOOR`.

## Context you should read first

- `internal/hal/backend.go` and `internal/hal/hwmon/backend.go` — interface and reference implementation style.
- `internal/hal/usbbase/` (from P2-USB-BASE) — USB HID primitives. Must be merged to main first.
- `.claude/rules/hwmon-safety.md` — safety invariants that this backend must extend.
- OpenRGB / liquidctl Corsair protocol references (both open source and publicly documented):
  - Corsair Commander Core: VID=0x1B1C, PID=0x0C32/0x0C34
  - Commander Core XT: VID=0x1B1C, PID=0x0C2A
  - H100i/H150i Elite Capellix: VID=0x1B1C, PIDs in the 0x0C20-0x0C40 range
  - Frame format: 65-byte reports, first byte is report ID (0x00), command bytes follow.

## What to do

1. Create `internal/hal/liquid/backend.go`:
   - `type Backend struct { devices []*corsair.Device; logger *slog.Logger }` — opens all Corsair AIO devices found by USB enumeration.
   - `NewBackend(logger)` returns a Backend with zero or more open devices. Silent on no-device (desktop without an iCUE AIO).
   - Each `hal.Channel` corresponds to one fan or pump on one device. Use `hal.RoleAIOFan` for fans and `hal.RoleAIOPump` for the pump.

2. Create `internal/hal/liquid/corsair/protocol.go`:
   - `type Device struct { dev *usbbase.Device; model string; fanCount int; pumpPresent bool }`.
   - `Open(info usbbase.DeviceInfo) (*Device, error)` — performs the initial handshake with the device, reads firmware version, enumerates fan count.
   - `ReadFan(index int) (rpm int, err error)`.
   - `WriteFan(index int, pwm uint8) error`.
   - `ReadPump() (rpm int, err error)`.
   - `WritePump(pwm uint8) error` — MUST reject pwm < minPumpPWM (per device, default 60).
   - `SetFirmwareAuto() error` — hands control back to device firmware. Called from Restore.

3. `Backend.Enumerate(ctx)` returns one `hal.Channel` per fan + one per pump across all open devices. Channel ID format: `liquid:<device-serial>:fan<N>` or `liquid:<device-serial>:pump`.

4. `Backend.Read(ch)`: dispatches to the device's ReadFan or ReadPump based on channel metadata. Returns `hal.Reading{RPM: rpm, PWM: 0}` (AIO protocols don't expose duty-cycle readback reliably).

5. `Backend.Write(ch, pwm)`:
   - For fans: WriteFan(index, pwm).
   - For pumps: WritePump(pwm) — inherits the minPumpPWM clamp from the protocol module.
   - All writes go through `usbbase.Device.Write` with the canonical 65-byte HID report format.

6. `Backend.Restore(ch)`: calls SetFirmwareAuto on the device. If the same device has multiple channels, Restore is idempotent — first call hands control back, subsequent calls are no-ops.

7. `Backend.Close()`: closes all open USB devices.

8. `Backend.Name()`: `"liquid"`.

9. Add Corsair VID/PID entries to `deploy/90-ventd-liquid.rules` for the supported devices.

10. Unit tests `internal/hal/liquid/backend_test.go` + `corsair/protocol_test.go`:
    - `TestEnumerate_NoDevices` — no Corsair AIO present → empty channels, no error.
    - `TestWritePump_BelowMinimum_Rejected` — WritePump(30) when minPumpPWM=60 returns error and does not issue USB write.
    - `TestWritePump_AtMinimum_Accepted`.
    - `TestRestore_Idempotent` — two Restore calls on the same device work cleanly.
    - `TestChannelID_Format` — channels for a two-fan + pump device produce the expected ID strings.
    - Real-hardware tests deferred to `T-LIQUID-01` with a fakeliquid fixture.

11. Register the backend in `cmd/ventd/main.go` alongside hwmon and nvml.

12. Build/vet/lint/test clean under `CGO_ENABLED=0` (if `go-hid` is built with the hidraw pure-Go tag from P2-USB-BASE) or `CGO_ENABLED=1` with a build-tag gate otherwise.

## Definition of done

- `internal/hal/liquid/backend.go` + `internal/hal/liquid/corsair/protocol.go` exist.
- At least one Corsair AIO family enumerates correctly through fakeliquid-style tests.
- Pump floor is enforced at the protocol layer (not just the controller).
- Restore paths hand control back to firmware cleanly.
- Udev rules extended with the target VID/PID combinations.
- CHANGELOG entry.
- CGO status matches whatever P2-USB-BASE shipped.

## Out of scope

- NZXT Kraken (that is P2-LIQUID-02).
- Lian Li UNI HUB (later).
- Advanced Corsair features (LED control, lighting profiles) — fans/pumps only.
- HIL tests against real devices (deferred).
- Tests beyond the unit tests above.

## Branch and PR

- Branch: `claude/P2-LIQUID-01-corsair`
- Title: `feat(hal/liquid): Corsair AIO backend via USB HID (P2-LIQUID-01)`

## Constraints

- Files: `internal/hal/liquid/**`, `deploy/90-ventd-liquid.rules`, `cmd/ventd/main.go` (registration), `CHANGELOG.md`.
- No new dependencies beyond what P2-USB-BASE introduced.
- Preserve pump-floor invariant.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.
- Additional field: SUPPORTED_DEVICES — VID/PID list with device model names.
