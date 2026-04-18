You are Claude Code, working on the ventd repository.

## Task
ID: P2-LIQUID-02
Track: LIQUID
Goal: Extend the liquid backend with NZXT Kraken-family AIOs (NZXT's Z-series, X-series G2). Second vendor in the LIQUID track. Protocol is very different from Corsair's (NZXT uses 64-byte feature reports and a state-machine for fan/pump coupling), but the `internal/hal/liquid` scaffolding from P2-LIQUID-01 should accommodate it via a new `nzxt` sub-package next to `corsair`.

## Care level
HIGH. Same pump-floor invariant as P2-LIQUID-01. Kraken pump commands and fan commands share a byte-level frame; a malformed frame has been reported in community tooling (liquidctl issue history) to lock the device until re-plugged.

## Context you should read first

- `internal/hal/liquid/backend.go` — the dispatch layer from P2-LIQUID-01 that this task extends.
- `internal/hal/liquid/corsair/` — reference implementation for a vendor sub-package.
- `internal/hal/usbbase/` — USB HID primitives.
- NZXT Kraken documentation (liquidctl source): 
  - Kraken X (X42/X52/X62/X72): VID=0x1E71, PID=0x170E
  - Kraken Z (Z53/Z63/Z73): VID=0x1E71, PID=0x3008
  - Kraken 2023/Elite: VID=0x1E71, PID=0x300E/0x300F
  - Frame format: 64 bytes, report ID implicit, command byte followed by channel index.

## What to do

1. Create `internal/hal/liquid/nzxt/protocol.go` mirroring the Corsair protocol module's shape:
   - `type Device struct { dev *usbbase.Device; model string; fanCount int; pumpPresent bool }`.
   - `Open`, `ReadFan`, `WriteFan`, `ReadPump`, `WritePump`, `SetFirmwareAuto`.
   - NZXT minPumpPWM default: 50 (slightly lower than Corsair's 60 — verify in community protocol docs).

2. Update `internal/hal/liquid/backend.go` to enumerate both Corsair AND NZXT devices at construction, producing a unified channel list with vendor-tagged channel IDs: `liquid:corsair:<serial>:fan1`, `liquid:nzxt:<serial>:pump`, etc.

3. Dispatch logic in Backend.Read/Write/Restore: look at the vendor prefix in the channel ID and route to the right protocol module.

4. Extend `deploy/90-ventd-liquid.rules` with NZXT VID/PIDs.

5. Unit tests `internal/hal/liquid/nzxt/protocol_test.go`:
   - `TestWritePump_BelowNZXTMinimum_Rejected`.
   - `TestFrameFormat_64Bytes` — confirm NZXT frames are 64 bytes, not 65 (a common bug source).
   - `TestMultiDevice_CorsairAndNZXT` — backend with one of each, Enumerate returns channels from both correctly tagged.

6. Build/vet/lint/test clean.

## Definition of done

- `internal/hal/liquid/nzxt/` package exists with protocol.go + protocol_test.go.
- Backend dispatches by vendor prefix in channel ID.
- Udev rules extended.
- Multi-vendor enumeration tested.
- CHANGELOG entry.

## Out of scope

- Lian Li UNI HUB (Phase 2+ beyond this wave).
- Kraken AIO pump display (the Z-series LCD) — that's a later feature.
- Real hardware tests.

## Branch and PR

- Branch: `claude/P2-LIQUID-02-nzxt-kraken`
- Title: `feat(hal/liquid): NZXT Kraken AIO support (P2-LIQUID-02)`

## Constraints

- Depends on P2-LIQUID-01 being merged. If it isn't yet, this task halts with a clear message.
- Files: `internal/hal/liquid/**` (new `nzxt/` sub-package + backend.go edits), `deploy/90-ventd-liquid.rules`, `CHANGELOG.md`.
- No new dependencies.
- Pump-floor invariant inherited.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.
- Additional field: SUPPORTED_DEVICES — NZXT VID/PID list + Kraken model names.
