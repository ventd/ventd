You are Claude Code, working on the ventd repository.

## Task
ID: P2-USB-BASE
Track: USBBASE
Goal: Shared USB HID primitives for Phase 2 liquid-cooler backends. Introduces `github.com/sstallion/go-hid` as a dependency and wraps it in a minimal ventd-owned API so LIQUID-01 and LIQUID-02 can share device enumeration, read-report, write-report, and disconnect-handling code instead of reimplementing it per backend.

## Care level
Medium. Pure-Go USB HID, no privileged operations. Dependency addition is the main risk — must verify `go-hid` is CGO-free (it uses cgo by default; need `hidraw` pure-Go build tag or equivalent, or document CGO dependency).

## Context you should read first

- `internal/hal/backend.go` — FanBackend interface for downstream consumer awareness.
- `go.mod` — current dependency tree. This task adds exactly one new dependency.
- `internal/hal/hwmon/backend.go` — reference implementation style (constructor, error handling, logger wiring).
- `deploy/90-ventd-hwmon.rules` — to mirror the udev-rule style for the `90-ventd-liquid.rules` file this task adds.

## What to do

1. Add `github.com/sstallion/go-hid` to `go.mod`. If it requires CGO, document that in a `// NOTE(cgo):` comment at the top of `internal/hal/usbbase/hid.go`. Prefer the `hidraw` pure-Go build tag if the library supports it.

2. Create `internal/hal/usbbase/hid.go`:
   - `type Device struct { ... }` wrapping the underlying hid.Device handle.
   - `func Enumerate(vendorID, productID uint16) ([]DeviceInfo, error)` — returns a slice of `DeviceInfo` with VID/PID/SerialNumber/Path.
   - `func Open(info DeviceInfo) (*Device, error)`.
   - `func (d *Device) Read(buf []byte) (int, error)` with a timeout via `ReadWithTimeout`.
   - `func (d *Device) Write(buf []byte) (int, error)`.
   - `func (d *Device) Close() error`.
   - `func (d *Device) IsAlive() bool` — cheap check that returns false if the device disappeared (for reconnect handling in downstream backends).

3. Create `deploy/90-ventd-liquid.rules` with udev rules that grant the `ventd` group read/write on the VID/PID combinations ventd will support (empty initially — LIQUID-01 and LIQUID-02 add entries). Keep the rule structure consistent with `90-ventd-hwmon.rules`.

4. Add `internal/hal/usbbase/hid_test.go`:
   - `TestEnumerate_NoDevices` — passes 0x0000 VID/PID, asserts empty slice + no error.
   - `TestDeviceInfo_StructuralShape` — confirms the struct has the five required fields (VID, PID, SerialNumber, Path, Manufacturer). No HID device needed.
   - `TestIsAlive_NilDevice` — calling IsAlive on a zero-value Device returns false without panic.
   - No tests that require a real USB device; those land in T-LIQUID-01 against `fakeliquid`.

5. Verify: `CGO_ENABLED=0 go build ./cmd/ventd/` — must still succeed if the library supports CGO-off. If it does not, file a concern in the PR body and verify `CGO_ENABLED=1 go build ./cmd/ventd/` works instead.

6. `go test -race -count=1 ./internal/hal/usbbase/...` — all tests pass.

7. `go vet ./...` and `golangci-lint run ./internal/hal/usbbase/...` — clean.

## Definition of done

- `internal/hal/usbbase/` package exists with `hid.go` + `hid_test.go`.
- `deploy/90-ventd-liquid.rules` exists (empty entries ok; file placeholder for LIQUID-01).
- One new dependency in go.mod; go.sum updated.
- Tests pass under `-race`.
- CGO status documented in PR body (off if possible; on with justification if not).
- CHANGELOG.md `## Unreleased` / `### Added` entry: one line.

## Out of scope for this task

- Tests outside the scope this task targets per the testplan catalogue. Only the usbbase-internal tests above.
- Any specific device protocol (Corsair Commander, NZXT Kraken, etc.) — those are LIQUID-01 / LIQUID-02.
- Implementing a full liquid FanBackend.
- Modifying any existing backend.

## Branch and PR

- Work on branch: `claude/P2-USB-BASE-hid-primitives`
- Title: `feat(hal): USB HID primitives for liquid backends (P2-USB-BASE)`
- PR description: goal verbatim, files-touched, "How I verified", dependency-addition justification, CGO status.

## Constraints

- Files touched: `internal/hal/usbbase/**`, `deploy/90-ventd-liquid.rules` (new), `go.mod`, `go.sum`, `CHANGELOG.md`.
- One new dependency only (`github.com/sstallion/go-hid`). Nothing transitive beyond what it pulls in itself.
- Prefer CGO-off; if CGO-on is required, justify.
- `CGO_ENABLED=0` main binary compatibility is a hard requirement for the main ventd binary. This is acceptable because the liquid backend will be gated behind a build tag if CGO is needed.

## Reporting

- STATUS
- PR: <url>
- SUMMARY (<= 200 words)
- CGO_STATUS: off / on-with-justification
- CONCERNS
- FOLLOWUPS
