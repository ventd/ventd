# Spec 02 — Corsair USB AIO (HID) backend

**Masterplan IDs this covers:** P2-USB-BASE (new), P2-LIQUID-01 (Corsair subset only), T-LIQUID-01 (Corsair subset).
**Target release:** v0.4.0 (first Phase 2 backend post-IPMI) — **alpha quality for Corsair, community validation requested.**
**Estimated session cost:** Sonnet, 4 PRs, $20–32 total. One Opus consult (~$2–3) in claude.ai between PR 1 merge and PR 2 start.
**Dependencies already green:** P1-HAL-01, spec-01 (tagged v0.3.1).

---

## Why Corsair first, not all three vendors

Masterplan P2-LIQUID-01 groups Corsair + NZXT + Lian Li. That's a three-week protocol-reverse-engineering sprint. Split it: **ship Corsair alone in v0.4.0, NZXT in v0.4.1, Lian Li in v0.4.2.** Rationale:

- Corsair Commander Core / Core XT has the best reverse-engineered protocol documentation. `liquidctl` ships a hand-written protocol spec (`docs/developer/protocol/commander_core.md`) alongside the Python driver — byte indices, opcode semantics, field layouts all documented. CCXT is also the most common AIO in homelab builds (H100i/H150i Elite XT + iCUE LINK).
- Each vendor PR is independently shippable. Three release posts instead of one. Marketing wedge #5 ("CoolerControl runs three processes to do AIO; ventd does it in one static binary") lands three times.
- Failure in vendor N doesn't block vendor N+1. If Lian Li's protocol turns out gnarlier than expected, you already have Corsair + NZXT shipped.

This spec covers Corsair only. NZXT and Lian Li specs will be near-identical and can be generated from this one as a template.

## Hardware reality — no Corsair Commander Core on hand

The developer does not currently own a Corsair Commander Core or any Corsair USB HID device. Acquiring one is tracked but not release-gating. v0.4.0 ships with Corsair Commander Core / Core XT / Commander Pro as **alpha quality, pending community validation**. This is honest, matches the "README never promises what isn't shipped" invariant, and does not block release.

Three-layer testing strategy makes this defensible:

1. **`fakehid` behavioural mock (Tier 2/3 — CI gating).** Port the semantics of `MockCommanderCoreDevice` from `liquidctl/tests/test_commander_core.py` to Go. In-memory USB HID device that responds to opcodes as the real device would. Covers 95% of PR 1 + PR 2.
2. **Protocol-doc cross-check (Tier 2).** Every opcode constant in `internal/hal/liquid/corsair/protocol.go` carries a `// ref: commander_core.md §N.N` comment tying it to the authoritative doc. Opus consult before PR 2 validates this mapping. No raw byte transcripts needed — the protocol doc is the source of truth and it's better than transcripts because it explains what every byte *means*.
3. **Community HIL (Tier 5 — post-ship).** `HARDWARE-REQUIRED` on `ventd --list-fans` DoD is deferred to a GitHub issue template soliciting validators with real Commander Core / Core XT / Commander Pro. v0.4.0 release notes explicitly ask for validation reports. v0.4.1 (NZXT) is the earliest we'd gate on actual Corsair hardware, and only if the community hasn't validated by then.

## Scope — what this session produces

**4 PRs, matching spec-01's known-good decomposition.** Each independently mergeable. PR order is mandatory.

### PR 1 — USB HID base + `fakehid` fixture + HAL contract row

**Target cost:** Sonnet, $5–8.

**Files:**
- `internal/hal/usbbase/usbbase.go` (new)
- `internal/hal/usbbase/usbbase_test.go` (new)
- `internal/hal/usbbase/discover.go` (new)
- `internal/testfixture/fakehid/fakehid.go` (new)
- `internal/testfixture/fakehid/fakehid_test.go` (new)
- `internal/hal/contract_test.go` (extend — add usbbase row if `TestHAL_Contract` exists and takes backend rows)
- `go.mod` — confirm `github.com/sstallion/go-hid v0.15.0` already present; add if missing

**Interface (USB HID primitives shared across Corsair/NZXT/Lian Li):**

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
    VendorID   uint16
    ProductIDs []uint16   // Multiple PIDs per family (e.g. H100i variants)
    Interface  int         // -1 means any
}

type Event struct {
    Kind   EventKind // Add | Remove
    Device Device
}

// Enumerate returns all devices matching any Matcher.
func Enumerate(matchers []Matcher) ([]Device, error)

// Watch re-enumerates on USB hotplug events.
// On Linux uses netlink udev; returns a channel of add/remove events.
func Watch(ctx context.Context, matchers []Matcher) (<-chan Event, error)
```

**`fakehid` requirements:**
- Implements `usbbase.Device` interface in-memory.
- Scriptable: test wires up a function `func(written []byte) (response []byte)` per device.
- Supports hotplug simulation: tests can inject `Add` / `Remove` events into a `Watch()` channel.
- Concurrent-safe (sync.Mutex around read/write state).
- Lives under `internal/testfixture/` — reused by Corsair, NZXT, Lian Li backends.

**PR 1 DoD:**
- `go test -race ./internal/hal/usbbase/... ./internal/testfixture/fakehid/...` green.
- `go vet ./...` clean.
- No writes to real `/dev/hidraw*` in any test — `fakehid` only.
- HAL contract row added if `TestHAL_Contract` table-driven (check first; if not yet landed, skip silently and note in PR description).

---

### PR 2 — Corsair backend core + safety invariants

**Target cost:** Sonnet, $10–15. This is the expensive PR. Opus consult in claude.ai before starting (see "Opus consult" section below).

**Files:**
- `internal/hal/liquid/liquid.go` (new — registry + dispatch)
- `internal/hal/liquid/corsair/corsair.go` (new — backend implementation)
- `internal/hal/liquid/corsair/protocol.go` (new — opcodes, framing, struct layout)
- `internal/hal/liquid/corsair/devices.go` (new — VID/PID table)
- `internal/hal/liquid/corsair/safety.go` (new — pump_minimum sentinel)
- `internal/hal/liquid/corsair/corsair_test.go` (new)
- `internal/hal/liquid/corsair/safety_test.go` (new — bound to `.claude/rules/liquid-safety.md`)
- `internal/testfixture/fakehid/corsair.go` (new — scripted Commander Core / Core XT / Commander Pro response set, ported from `MockCommanderCoreDevice`)
- `.claude/rules/liquid-safety.md` (new)

**Devices in scope for v0.4.0:**

| Family | VID | PIDs | Channels |
|---|---|---|---|
| Commander Core | 0x1b1c | 0x0c1c, 0x0c1e, 0x0c2a | pump (1) + fan (6) |
| Commander Core XT | 0x1b1c | 0x0c20 | fan (6) |
| Commander Pro | 0x1b1c | 0x0c10 | fan (6) |

Exclude iCUE LINK System Hub for v0.4.0 — its protocol is substantially different (daisy-chain with per-device addressing) and deserves its own v0.4.1 PR. Note the exclusion in `docs/hardware.md`.

**Protocol sources:** `liquidctl/docs/developer/protocol/commander_core.md` is the canonical reference. Every opcode constant in `protocol.go` carries a `// ref: commander_core.md §N.N` comment. `liquidctl/driver/commander_core.py` is the cross-check — read opcode tuples, do NOT port driver structure 1:1 (Python-idiomatic, maps poorly to Go). The market-strategy wedge is "no Python dependency"; shelling out to `liquidctl` is not an option.

**Protocol essentials to implement:**
- `cmd_wake` / `cmd_sleep` — device wake/sleep (opcode `0x01 0x03 0x00 0x02` / `0x01 0x03 0x00 0x01`).
- `cmd_get_firmware` — enumerate on startup, log firmware version (opcode `0x02 0x13`).
- `cmd_open_endpoint` — mode-switch prelude for reads/writes (opcode `0x0d`).
- `cmd_read` — temperature, RPM, LED-count reads via `_MODE_GET_TEMPS` / `_MODE_GET_SPEEDS` / `_MODE_LED_COUNT`.
- `cmd_write` — per-channel fan mode + PWM via `_MODE_HW_FIXED_PERCENT`.
- `cmd_close_endpoint` — release mode lock (opcode `0x05`).

**Mapping to HAL `FanBackend` interface:**
- Each physical device → one backend instance registered with `hal.Registry`.
- Channels are `ChannelRole.Pump` or `ChannelRole.CaseFan` based on index (pump is always channel 0 on Commander Core; all channels are CaseFan on Commander Pro / Core XT).
- `Caps.HasRPMTarget = false` (Corsair exposes PWM only via the ventd path, not RPM closed-loop).
- `Restore()` writes per-channel firmware-curve mode — hands control back to BIOS/device default. **Never leave pumps at low PWM on exit.**

**Safety invariants (`.claude/rules/liquid-safety.md`):**

Five rules, 1:1 with five subtests in `safety_test.go:TestLiquidSafety_Invariants`. Format must match `.claude/rules/hwmon-safety.md` exactly — CC reads that file first and mirrors format.

1. `RULE-LIQUID-01`: Pump channel PWM never falls below `pump_minimum` (default 50). Config-overridable per device. Enforced in the HAL write path, not in the controller.
   **Bound:** `internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/PumpMinimumFloor`
2. `RULE-LIQUID-02`: USB disconnect mid-write does not leave the pump below `pump_minimum`. On reconnect, first action is write-pump-to-safe-floor.
   **Bound:** `internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/ReconnectPumpFloor`
3. `RULE-LIQUID-03`: Firmware version mismatch (unknown firmware) → read-only mode. Never write.
   **Bound:** `internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/UnknownFirmwareReadOnly`
4. `RULE-LIQUID-04`: On `Restore()`, every channel returns to firmware mode before the HID handle closes. Panic in the middle does not skip un-restored channels.
   **Bound:** `internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/RestoreCompletesOnPanic`
5. `RULE-LIQUID-05`: Writes are serialised per device (one HID transfer at a time). No concurrent writes — Corsair Core firmware corrupts state under concurrent access.
   **Bound:** `internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/SerialisedWrites`

**PR 2 DoD:**
- `GOOS=linux go test -race ./internal/hal/liquid/corsair/...` green.
- `go test -run TestLiquidSafety_Invariants ./internal/hal/liquid/corsair/...` green; every subtest maps 1:1 to a `RULE-LIQUID-<N>`.
- `tools/rulelint` (meta-lint) green — no orphan rules, no orphan subtests.
- `goleak` assertion in every test using goroutines — no goroutine leak.
- `fakehid/corsair.go` responses cross-referenced against `commander_core.md` — grep for `// ref:` comments, verify each maps to a doc section.

---

### PR 3 — Udev rule + unit-file tests

**Target cost:** Sonnet, $3–5.

**Files:**
- `deploy/90-ventd-liquid.rules` (new — ~3 lines, match `90-ventd-hwmon.rules` style)
- `internal/hal/liquid/corsair/udev_test.go` (new — parse rule file, assert VID 0x1b1c matched correctly, `TAG+="uaccess"` present)
- `internal/hal/liquid/corsair/systemd_test.go` (new if needed — verify main ventd.service does NOT need new DeviceAllow for USB HID; uaccess tag on udev rule is the mechanism)

**Behaviour:**
1. Udev rule matches VID 0x1b1c Corsair devices and tags them `uaccess` — grants the logged-in seat user access without requiring ventd to run as root or use a sidecar.
2. Rule parses under `udevadm verify` (test gated with `//go:build linux && udev_integration` — run in VM, not in CI container).
3. Unit-file test: main `ventd.service` does NOT require any new capability or DeviceAllow grant for USB HID — the udev `uaccess` tag is the access mechanism. This is the "single binary, no sidecar" marketing wedge.

**If `udevadm verify` can't run in the CI container**, gate (2) behind `//go:build udev_integration` and document the missing coverage in `TESTING.md` under the hardware-gated matrix — same pattern as spec-01 PR 2.

**PR 3 DoD:**
- `deploy/90-ventd-liquid.rules` exists, 3–5 lines, matches existing rule file style.
- Unit tests green in CI.
- No new DeviceAllow or CapabilityBoundingSet entries in `deploy/ventd.service`.

---

### PR 4 — VID gate + hwdb entries + docs + CHANGELOG

**Target cost:** Sonnet, $2–4.

**Files:**
- `internal/hal/liquid/corsair/backend.go` (minor — add VID-present gate in `Enumerate()`)
- `internal/hwdb/profiles.yaml` (add 3–4 Corsair-bearing build fingerprints)
- `docs/hardware.md` (new section — Corsair compat table)
- `CHANGELOG.md` (bullet under v0.4.0)

**Behaviour:**
1. **VID gate:** `Enumerate()` first enumerates USB devices looking for VID 0x1b1c. If none found, return zero channels and log a single debug-level event. Do not open any HID handles. This avoids spurious `/dev/hidraw*` opens on systems with no Corsair hardware — every open triggers udev events and can surface in kernel logs.
2. **hwdb entries:** Add 3–4 Corsair-bearing build fingerprints to `internal/hwdb/profiles.yaml`: a generic Commander Core desktop, a Commander Core XT variant, a Commander Pro + Corsair case combo, one "Corsair hub detected but unknown firmware" gate for the read-only path.
3. **docs/hardware.md:** Corsair compat table. Columns: Family, PIDs, Channels, Known firmware versions tested, Status. Every row honest — "unverified" where appropriate.
4. **CHANGELOG.md** under `v0.4.0`:
   ```
   - Native Corsair AIO support (Commander Core, Commander Core XT, Commander Pro).
     No liquidctl dependency. Single-binary, no sidecar required — access via
     `uaccess` udev tag. ALPHA QUALITY: tested against `fakehid` fixture and the
     liquidctl protocol documentation; real-hardware validation pending community
     reports. If you own a Commander Core/Core XT/Commander Pro and can validate,
     please file an issue with `ventd --list-fans` output.
   ```

**PR 4 DoD:**
- `go test -race ./...` green across the whole repo.
- `internal/hwdb/profiles.yaml` passes schema validation.
- `docs/hardware.md` renders on GitHub without broken tables.
- `CHANGELOG.md` bullet is honest about alpha status.

---

## Opus consult — between PR 1 merge and PR 2 start

**Where:** claude.ai (this chat or a new one — flat-rate on Max plan, $0 marginal cost).
**Cost if you use Opus in CC by accident:** $2–5 per back-and-forth. Do NOT.
**Duration target:** 30–60 minutes.

**What you hand Opus:**
1. `liquidctl/docs/developer/protocol/commander_core.md` (pasted into the chat — fetch it via web_fetch at the top of the consult).
2. Your draft Go struct layout for `internal/hal/liquid/corsair/protocol.go` — just the types, not the functions.
3. This spec (for context on the HAL interface constraints).

**What you ask Opus to check:**
- Every opcode constant maps to a section in the protocol doc.
- Go struct packing matches the Commander Core wire format (HID report ID wrapper + command/channel/sequence envelope + payload). Endianness is correct (little-endian per Corsair convention).
- Zero-value behaviour of structs doesn't corrupt frames — a zero struct must be an invalid/harmless frame, not an accidental wake.
- Sequence-number handling: stateful or stateless? If stateful, where does state live — per-device or per-backend?
- Firmware-version gating lives at the type level if possible (e.g. `UnknownFirmwareDevice` type that only exposes `Read`, never `Write`). `RULE-LIQUID-03` becomes compile-time-enforced.

**Deliverable of the consult:** a `specs/spec-02-framing-review.md` file committed alongside `spec-02-corsair-aio.md`. PR 2's CC prompt references this file explicitly — CC reads it before writing `protocol.go`.

---

## Definition of done (spec-02 as a whole)

- [ ] PR 1 merged: `internal/hal/usbbase/` + `internal/testfixture/fakehid/` green.
- [ ] Opus consult complete; `specs/spec-02-framing-review.md` committed.
- [ ] PR 2 merged: `internal/hal/liquid/corsair/` + `.claude/rules/liquid-safety.md` green; 5 invariants bound.
- [ ] PR 3 merged: `deploy/90-ventd-liquid.rules` + udev tests green.
- [ ] PR 4 merged: VID gate + hwdb + `docs/hardware.md` + CHANGELOG honest-alpha bullet.
- [ ] `tools/rulelint` green across the repo — no orphan rules or subtests.
- [ ] Tag `v0.4.0` cut **only after** `gh pr merge --squash --delete-branch` succeeds on PR 4 AND `git pull` shows the squash commit on main AND `git tag --sort=-v:refname | head -5` confirms no tag collision.
- [ ] GitHub release notes explicitly request community validation; link an issue template for Corsair validation reports.
- [ ] HARDWARE-REQUIRED checkbox for `ventd --list-fans` against real Commander Core is explicitly marked "deferred to community validation, v0.4.1 acceptance" — not claimed as met.

## Explicit non-goals (do not scope-creep)

- No NZXT, no Lian Li, no Aqua Computer, no EK Loop. Separate specs.
- No RGB. Market-strategy §3 — OpenRGB lane, stay out of it.
- No iCUE LINK System Hub (v0.4.1 or later).
- No Mac / Windows cross-compile paths — those are P6-WIN / P6-MAC.
- No real-hardware DoD as a ship gate for v0.4.0. Hardware validation is a post-ship community ask.
- No scope expansion "while we're here" — PR 4's hwdb entries are exactly 3–4, not "all the Corsair builds we can find."

## CC session prompts

Four prompts, one per PR. Run each independently. Before each session, the operator (Phoenix) runs:

```
git fetch origin
git checkout main
git pull origin main
git branch --list feat/spec-02-pr<N>   # MUST be empty — delete if stale
git tag --sort=-v:refname | head -5    # for PR 4 CHANGELOG only
```

Then starts a fresh CC session and pastes the prompt for the target PR.

### PR 1 prompt

```
Read /home/phoenix/ventd/specs/spec-02-corsair-aio.md, PR 1 section only.
Then read .claude/rules/hwmon-safety.md for rule-file format reference
(do not edit it).

Create branch feat/spec-02-pr1-usbbase from main. All work on this branch.
Do NOT commit on main.

Deliver: internal/hal/usbbase/ package (usbbase.go, discover.go, and tests)
plus internal/testfixture/fakehid/ (fakehid.go, fakehid_test.go).

Verify go.mod has github.com/sstallion/go-hid v0.15.0. Add if missing.

Constraints:
- go test -race must be green before any commit.
- fakehid is pure in-memory — no /dev/hidraw* access in tests, ever.
- No subagents. Sonnet only. Commit at every green-test boundary.
- Before declaring done, run and paste output of:
    git status
    git branch --show-current
    git log --oneline -5
  If git status shows anything uncommitted, commit it. If current branch
  is not feat/spec-02-pr1-usbbase, STOP and ask.

Do not scope-creep into Corsair-specific code. This PR is generic USB HID
primitives only.
```

### PR 2 prompt

```
Read /home/phoenix/ventd/specs/spec-02-corsair-aio.md, PR 2 section.
Read /home/phoenix/ventd/specs/spec-02-framing-review.md — the Opus
consult output. This is the authoritative struct layout; do not deviate
without pausing and asking.
Read .claude/rules/hwmon-safety.md for rule-file format reference.

Create branch feat/spec-02-pr2-corsair from main (main must include the
PR 1 squash commit — verify with git log --oneline -3 before starting).

Deliver: internal/hal/liquid/{liquid.go, corsair/{corsair.go, protocol.go,
devices.go, safety.go, corsair_test.go, safety_test.go}}, extension of
internal/testfixture/fakehid/corsair.go, and .claude/rules/liquid-safety.md.

Constraints:
- Every opcode constant in protocol.go carries a // ref: commander_core.md §N.N
  comment. No bare opcodes.
- safety_test.go:TestLiquidSafety_Invariants has exactly 5 subtests,
  1:1 with RULE-LIQUID-01..05 in .claude/rules/liquid-safety.md.
- Every goroutine-using test uses goleak.VerifyNone(t) in a defer.
- No concurrent writes to a single fakehid Corsair device — enforce in the
  backend, test the enforcement.
- Sonnet only. No subagents. No Opus. Commit at every green-test boundary.
- Before declaring done, run and paste output of:
    git status
    git branch --show-current
    git log --oneline -5
    go test -race ./internal/hal/liquid/... ./internal/testfixture/fakehid/...
    go run ./tools/rulelint

Do not touch deploy/ or docs/hardware.md or CHANGELOG.md — those are PR 3
and PR 4.
```

### PR 3 prompt

```
Read /home/phoenix/ventd/specs/spec-02-corsair-aio.md, PR 3 section.
Read existing deploy/90-ventd-hwmon.rules — match its style exactly.
Read deploy/ventd.service — confirm no new DeviceAllow is needed.

Create branch feat/spec-02-pr3-udev from main (main must include PR 2
squash commit).

Deliver: deploy/90-ventd-liquid.rules (3–5 lines matching hwmon-rules
style), internal/hal/liquid/corsair/udev_test.go, and systemd_test.go
if unit-file assertions aren't already elsewhere.

Constraints:
- udev rule matches VID 0x1b1c and applies TAG+="uaccess". Nothing else.
- If udevadm verify can't run in the CI container, gate that test with
  //go:build udev_integration and note it in TESTING.md.
- No changes to ventd.service. If you feel a change is needed, STOP and ask.
- Sonnet only. Commit at every green-test boundary.
- Before declaring done, paste: git status, branch, last 5 commits,
  go test output.
```

### PR 4 prompt

```
Read /home/phoenix/ventd/specs/spec-02-corsair-aio.md, PR 4 section.
Read internal/hwdb/profiles.yaml for schema and style reference.
Read docs/hardware.md if it exists; otherwise match CHANGELOG.md tone.

Create branch feat/spec-02-pr4-docs-gate from main (main must include
PR 3 squash commit).

Deliver:
- VID gate in internal/hal/liquid/corsair/backend.go Enumerate()
- 3–4 Corsair-bearing fingerprints in internal/hwdb/profiles.yaml
- Corsair compat table in docs/hardware.md
- v0.4.0 bullet in CHANGELOG.md, explicit about ALPHA status and the
  community-validation ask.

Constraints:
- The CHANGELOG bullet must explicitly say "ALPHA QUALITY" and
  "real-hardware validation pending community reports."
- hwdb entries are exactly 3–4. Not 5, not "as many as we can find."
- Sonnet only. Commit at every green-test boundary.
- Before declaring done, paste: git status, branch, last 5 commits,
  full go test ./... output, tools/rulelint output, and
  git tag --sort=-v:refname | head -5 (to confirm no v0.4.0 tag yet —
  that's a post-merge manual step).
```

## Cost discipline notes

- **The protocol doc pass is the expensive part in tokens.** Do it yourself in claude.ai first (Max plan, flat-rate), summarise opcodes + framing into `spec-02-framing-review.md`, hand that to CC. CC never reads `commander_core.py` (3000 lines of Python).
- **If CC starts "exploring" the HAL interface, stop the session.** The interface is already defined in `internal/hal/backend.go` — point CC at the file explicitly.
- **The udev rule is 3 lines.** Do not let CC write a 40-line rule "for future-proofing." Match existing `90-ventd-hwmon.rules` style exactly.
- **PR 2 is the one that can blow the budget.** If you see CC heading past $12 mid-session (check ccusage), pause it. The likely cause is CC trying to write the protocol layer from scratch instead of porting — in which case the Opus consult output is missing or wrong. Fix the spec, don't push through.
- **Never run Opus in CC.** Protocol-design review happens in claude.ai only. See `ventd-daily-rules.md`.

## Tag & release discipline (spec-01 lessons carried forward)

After PR 4 merges:

1. `git fetch origin && git checkout main && git pull origin main`
2. Verify `git log --oneline -5` shows PR 4's squash commit on main — not just the local merge.
3. `git tag --sort=-v:refname | head -5` — confirm no `v0.4.0` exists yet.
4. Only then: `git tag -a v0.4.0 -m "..." && git push origin v0.4.0`.
5. If anything goes wrong with the tag push, `gh run list --limit 10` immediately — the Release and release-changelog workflows fire on tag push and cannot be un-fired. `gh run cancel <id>` any that shouldn't complete. Check `gh release list` for orphan releases.
6. If CHANGELOG header collides with an existing tag, fix CHANGELOG in a fix-up PR before tagging. Do NOT tag into a broken CHANGELOG.

## Why this is mostly cheap (despite being bigger than spec-01)

- Pure Go, fixture-based testing, no hardware on ship gate.
- Pattern proven by spec-01: 4-PR shape, squash-merge, tag after main pull.
- `fakehid` is reusable for NZXT (spec-02b) and Lian Li (spec-02c) — amortised cost.
- Protocol doc does the heavy lifting that recorded byte transcripts would do — and is easier to reason about.
- Opus consult happens on flat-rate Max plan, $0 marginal.
- Invariant binding automates correctness review via `rulelint`.

The expensive alternative — buying hardware, pausing 3 weeks for shipping, then doing everything over again — is explicitly rejected. Ship alpha, gather community validation, iterate.
