# spec-02-corsair-aio — amendment 2026-04-24

**Applies to:** `specs/spec-02-corsair-aio.md`
**Trigger:** PR 1.5 insertion following pure-Go hidraw decision
**References:** `specs/spec-02-hidraw.md`, `specs/spec-02-framing-review.md`

## Summary of changes

PR 1 shipped `internal/hal/usbbase/` backed by `github.com/sstallion/go-hid`
(requires `CGO_ENABLED=1`). This conflicts with ventd's static-binary
release posture. PR 1.5 is inserted to swap the substrate to a pure-Go
hidraw implementation before PR 2 builds backends on top.

The Corsair target family is narrowed to what the liquidctl
`commander_core.py` driver covers: Commander Core (`0x0c1c`), Commander
Core XT (`0x0c2a`), Commander ST (`0x0c32`). **Commander Pro is a
different protocol and is not in spec-02 scope.** It will land as
spec-02a after hardware is sourced.

## Revised PR sequence

| PR     | Title                                     | Status      |
|--------|-------------------------------------------|-------------|
| PR 1   | usbbase + fakehid fixture                 | merged, main|
| PR 1.5 | Replace go-hid with pure-Go hidraw        | new, next up|
| PR 2   | Corsair Commander Core backend            | blocked on 1.5|
| PR 3   | systemd deployment + udev rules           | unchanged   |
| PR 4   | CHANGELOG + docs + tag v0.4.0             | unchanged   |

## PR 1.5 details

See `specs/spec-02-hidraw.md` for full scope. Summary:

- New package `internal/hal/usbbase/hidraw/` — pure-Go Linux hidraw
  implementation (sysfs enumeration, ioctl feature reports, netlink
  hotplug).
- `internal/hal/usbbase/discover_linux.go` rewritten to call the new
  package instead of go-hid.
- `github.com/sstallion/go-hid` removed from `go.mod` / `go.sum`.
- `CGO_ENABLED=0 go build` succeeds; `ldd ventd` reports "not a dynamic
  executable".
- Six new invariant bindings (RULE-HIDRAW-01..06) land in
  `.claude/rules/hidraw-safety.md`.
- Test coverage uses synthetic sysfs fixture under `t.TempDir()` and
  socketpair-backed fake netlink — no real hidraw access in CI.

## PR 2 changes arising from spec-02-framing-review

Pre-amendment PR 2 scope assumed free writes to Corsair hardware.
Framing review raised hardware-safety concerns (unknown firmware,
no hardware to validate against). Post-amendment:

- **Firmware allow-list for v0.4.0 is empty.** Every probed device is
  wrapped as `unknownFirmwareDevice` and reports as read-only via the
  HAL adapter. Read paths (speeds, temps, connected-state) work
  normally.
- **Writes are gated behind an experimental flag** (working name
  `--enable-corsair-write`) documented as "may leave device in a state
  requiring iCUE to recover; not recommended without validated firmware".
- Compile-time type split (`liveDevice` vs `unknownFirmwareDevice`)
  lives inside the corsair package; the HAL-facing adapter does the
  runtime read-only check at `FanBackend.Write`.
- `FanBackend` interface unchanged. Corsair backend returns
  `ErrReadOnlyUnvalidatedFirmware` from `Write` when firmware is not
  allow-listed.

Invariant bindings for PR 2 adjust accordingly:

- **RULE-LIQUID-03** — unknown firmware returns `ErrReadOnly...` from
  `Write`. Enforced at the adapter boundary, not just inside the
  corsair package.
- New **RULE-LIQUID-06** — writable mode requires the experimental
  flag AND a firmware version on the allow-list. Two conditions, both
  required. Binds to `TestCorsair_WriteRequiresFlagAndAllowlist`.

## Deferred to spec-02a (future)

- Commander Pro (`0x1b1c:0x0c10`) — different protocol, different driver
  file in liquidctl. Waits on hardware sourcing.

## Deferred to spec-02b+ (future)

- NZXT Kraken USB HID backend
- Lian Li USB HID backend
- Aquacomputer USB HID backend

All three will reuse `internal/hal/usbbase/hidraw/` directly. This is
the payoff of PR 1.5 — no per-backend cgo dependency, no per-backend
release-matrix split.

## Budget impact

- PR 1.5 Sonnet: $15-25 target, $30 hard cap
- PR 2 Sonnet: unchanged from original estimate
- Schedule: +1 PR, ~2 days elapsed

## Action items before PR 1.5 CC session

- [ ] Commit `spec-02-hidraw.md` + `spec-02-framing-review.md` +
      this amendment to main via one docs PR
- [ ] Create `.claude/rules/hidraw-safety.md` with six RULE-HIDRAW-*
      entries (same PR as above)
- [ ] Update `specs/spec-02-corsair-aio.md` header to note this
      amendment applies (or inline the content)
- [ ] Start fresh claude.ai chat for PR 1.5 CC prompt generation
