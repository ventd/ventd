# Spec 02 — Corsair USB AIO (HID) backend

**Masterplan IDs this covers:** P2-USB-BASE (new), P2-LIQUID-01 (Corsair subset only), T-LIQUID-01 (Corsair subset).
**Target release:** v0.4.0 (first Phase 2 backend post-IPMI).
**Estimated session cost:** Sonnet, ~8–12 focused sessions, $10–20 each. One Opus consult (~$3) for protocol-design review before implementation begins.
**Dependencies already green:** P1-HAL-01.

---

## Why Corsair first, not all three vendors

Masterplan P2-LIQUID-01 groups Corsair + NZXT + Lian Li. That's a three-week protocol-reverse-engineering sprint. Split it: **ship Corsair alone in v0.4.0, NZXT in v0.4.1, Lian Li in v0.4.2.** Rationale:

- Corsair Commander Core / Core XT has the best reverse-engineered protocol documentation. `liquidctl` source is readable, `OpenCorsairLink` is archived-but-complete, and CCXT is the most common AIO in homelab builds (H100i/H150i Elite XT + iCUE LINK).
- Each vendor PR is independently shippable. You get three release posts instead of one. Marketing wedge #5 ("CoolerControl runs three processes to do AIO; ventd does it in one static binary") lands three times.
- Failure in vendor N doesn't block vendor N+1. If Lian Li's protocol turns out gnarlier than expected, you already have Corsair + NZXT shipped.

This spec covers Corsair only. NZXT and Lian Li specs will be near-identical and can be generated from this one as a template.

## Scope — what this session produces

### PR 1 — USB HID base (P2-USB-BASE)

Shared primitives so Corsair/NZXT/Lian Li don't duplicate USB plumbing.

**Files:**
- `internal/hal/usbbase/usbbase.go` (new)
- `internal/hal/usbbase/usbbase_test.go` (new)
- `internal/hal/usbbase/discover.go` (new)
- `go.mod` — add `github.com/sstallion/go-hid v0.15.0` (already present per `go.mod` inspection).

**Interface:**
```go
package usbbase

type Device interface {
    VendorID() uint16
    ProductID() uint16
    SerialNumber() string
    Read(buf []byte, timeout time.Duration) (int, error)
    Write(buf []byte) (int, error)
    Close() error
}

type Matcher struct {
    VendorID  uint16
    ProductIDs []uint16   // Multiple PIDs per family (e.g. H100i variants)
    Interface  int         // -1 means any
}

// Enumerate returns all devices matching any Matcher.
func Enumerate(matchers []Matcher) ([]Device, error)

// Watch re-enumerates on USB hotplug events.
// On Linux uses netlink udev; returns a channel of add/remove events.
func Watch(ctx context.Context, matchers []Matcher) (<-chan Event, error)
```

**Tests:** fixture-based, no real USB. Build a `fakehid` mock (extending `internal/testfixture/` pattern) that implements the interface in-memory with scriptable responses.

### PR 2 — Corsair backend (P2-LIQUID-01, Corsair subset)

**Files:**
- `internal/hal/liquid/liquid.go` (new — registry + dispatch)
- `internal/hal/liquid/corsair/corsair.go` (new)
- `internal/hal/liquid/corsair/protocol.go` (new — opcodes + framing)
- `internal/hal/liquid/corsair/devices.go` (new — VID/PID table)
- `internal/hal/liquid/corsair/safety.go` (new — pump_minimum sentinel)
- `internal/hal/liquid/corsair/corsair_test.go` (new)
- `internal/hal/liquid/corsair/safety_test.go` (new — bound to `.claude/rules/liquid-safety.md`)
- `internal/testfixture/fakeliquid/corsair.go` (new)
- `.claude/rules/liquid-safety.md` (new)
- `deploy/90-ventd-liquid.rules` (new — udev rule for VID 0x1b1c)

**Devices in scope for v0.4.0:**
| Family | VID | PIDs | Channels |
|---|---|---|---|
| Commander Core | 0x1b1c | 0x0c1c, 0x0c1e, 0x0c2a | pump (1) + fan (6) |
| Commander Core XT | 0x1b1c | 0x0c20 | fan (6) |
| Commander Pro | 0x1b1c | 0x0c10 | fan (6) |

Exclude iCUE LINK System Hub for v0.4.0 — its protocol is substantially different (daisy-chain with per-device addressing) and deserves its own v0.4.1 PR. Note the exclusion in `docs/hardware.md`.

**Protocol sources:** `liquidctl/driver/commander_core.py` is the canonical reference. Read it, port opcodes and framing to Go, do NOT shell out to it. The market-strategy wedge is specifically "no Python dependency."

**Protocol essentials to implement:**
- `cmd_wake` / `cmd_sleep` — device wake/sleep.
- `cmd_get_firmware` — enumerate on startup, log firmware version.
- `cmd_read_sensors` — temperature + RPM readings. Frame is 16-byte report, little-endian.
- `cmd_set_fan_mode` — per-channel mode (firmware curve / software PWM / fixed RPM). ventd uses software PWM mode.
- `cmd_set_fan_pwm` — per-channel PWM 0–255.
- `cmd_read_fan_rpm` — per-channel RPM.

**Mapping to HAL `FanBackend` interface:**
- Each physical device → one backend instance registered with `hal.Registry`.
- Channels are `ChannelRole.Pump` or `ChannelRole.CaseFan` based on index (pump is always channel 0 on Commander Core; all channels are CaseFan on Commander Pro / Core XT).
- `Caps.HasRPMTarget = false` (Corsair exposes PWM only via the ventd path, not RPM closed-loop).
- `Restore()` writes the per-channel "firmware curve" mode — this hands control back to the BIOS/device default. Never leave pumps at a low PWM on exit.

**Safety invariants (`.claude/rules/liquid-safety.md`):**
1. `RULE-LIQUID-01`: Pump channel PWM never falls below `pump_minimum` (default 50). Config-overridable per device. Enforced in the HAL write path, not in the controller.
2. `RULE-LIQUID-02`: USB disconnect mid-write does not leave the pump below `pump_minimum`. On reconnect, first action is write-pump-to-safe-floor.
3. `RULE-LIQUID-03`: Firmware version mismatch (unknown firmware) → read-only mode. Never write.
4. `RULE-LIQUID-04`: On `Restore()`, every channel returns to firmware mode before the HID handle closes. Panic in the middle does not skip un-restored channels.
5. `RULE-LIQUID-05`: Writes are serialised per device (one HID transfer at a time). No concurrent writes — Corsair Core firmware corrupts state under concurrent access.

## The Opus consult — before any code

Before starting PR 2, spend one Opus chat session (in claude.ai, not CC — free on your Max plan) on this:

**Protocol framing design review.** The Corsair Commander Core framing has three layers: (1) HID report ID wrapper, (2) command/channel/sequence envelope, (3) command-specific payload. The `liquidctl` implementation is Python-idiomatic and doesn't map cleanly to Go's type system. Have Opus review your proposed Go struct layout + framing approach before you spend a day writing it the wrong way. Show it the `liquidctl` source and your draft Go types. One hour here saves three days of refactoring.

## Definition of done

- [ ] `GOOS=linux go test -race ./internal/hal/usbbase/... ./internal/hal/liquid/...` passes.
- [ ] `go test -run TestLiquidSafety_Invariants ./internal/hal/liquid/corsair/...` passes; every subtest maps 1:1 to a `RULE-LIQUID-<N>`.
- [ ] On a system with a Corsair Commander Core attached, `ventd --list-fans` enumerates the pump + case fans with correct role classification. **This is HARDWARE-REQUIRED — do not claim DoD without real-hardware verification. Flag it in the PR description if you don't have the hardware yet.**
- [ ] `deploy/90-ventd-liquid.rules` parses under `udevadm verify`.
- [ ] `docs/hardware.md` updated with Corsair compat table (supported firmware versions, known-broken variants if any).
- [ ] `CHANGELOG.md` bullet: "Native Corsair AIO support (Commander Core, Core XT, Commander Pro). No liquidctl dependency."

## Explicit non-goals (do not scope-creep)

- No NZXT, no Lian Li, no Aqua Computer, no EK Loop. Separate specs.
- No RGB. Market-strategy §3 — OpenRGB lane, stay out of it.
- No iCUE LINK System Hub (v0.4.1).
- No Mac / Windows cross-compile paths — those are P6-WIN / P6-MAC.

## CC session prompt — copy/paste this

```
Read /home/claude/specs/spec-02-corsair-aio.md. Before any Go code, verify:
(1) go.mod already has github.com/sstallion/go-hid, (2) no existing
internal/hal/liquid/ or internal/hal/usbbase/ package conflicts with what
this spec creates, (3) .claude/rules/hwmon-safety.md exists as a template
for the new liquid-safety.md.

PR order is mandatory: PR 1 (usbbase) merges before PR 2 (corsair) starts.
Do NOT begin Corsair until usbbase tests are green.

For PR 2, I have separately consulted Opus on the protocol framing design.
The agreed Go struct layout is in /home/claude/specs/spec-02-framing.md
(I will add this after the Opus chat). Read that file before writing
internal/hal/liquid/corsair/protocol.go.

Use Sonnet throughout. Do not invoke subagents — each PR is a linear
sequence. Commit at every green-test boundary.

Hardware access: I have a Corsair Commander Core at my desk. When PR 2
reaches `ventd --list-fans` verification, pause and ask me to plug it in
and run the command — do not fabricate test output.
```

## Cost discipline notes

- The `liquidctl` source pass is the expensive part in tokens. Do it yourself in claude.ai first (Max plan, flat-rate), summarise the opcodes into a reference doc, and hand the reference doc to CC. CC doesn't need to read 3000 lines of Python.
- If CC starts "exploring" the HAL interface, stop the session. The interface is already defined in `internal/hal/backend.go` — point CC at the file explicitly.
- The udev rule (`deploy/90-ventd-liquid.rules`) is 3 lines. Do not let CC write a 40-line rule "for future-proofing." Match existing `90-ventd-hwmon.rules` style exactly.
