# P2-USB-BASE — shared USB HID primitives

**Care level:** HIGH. Other Phase 2 backends (LIQUID, future CROSEC revisions) are blocked on this; if the primitive is wrong, fixes cascade into four backend PRs. Work the task however your session is configured — no model-gated abort.

## Task

- **ID:** P2-USB-BASE
- **Track:** USBBASE (Phase 2)
- **Goal:** Land the shared USB HID primitive layer (`internal/hal/usbbase/`) that later backends (LIQUID, CROSEC) use for device discovery, open/close, report read/write. No backend-specific logic in this PR — only the generic wrapper around `github.com/sstallion/go-hid`.

## Context — read before editing

1. `ventdmasterplan.mkd` §2 (repo conventions), §7 (dependency graph), §8 (P2-USB-BASE entry).
2. `internal/hal/backend.go` — the FanBackend interface shape; USB-BASE is NOT a FanBackend implementation, it's a shared primitive layer backends build on.
3. `internal/hal/nvml/nvml.go` — reference for how another CGO-off-via-dlopen primitive layer is structured.
4. `go.mod` — confirm no existing HID dependency.
5. `internal/testfixture/fakenvml/` — shape for a test fixture corresponding to a hardware primitive.

## What to do

1. Add dependency `github.com/sstallion/go-hid` (pure-Go, MIT, uses hidraw on Linux — CGO-off compatible via the `hidraw` build tag).
2. Create `internal/hal/usbbase/usbbase.go` exporting:
   - `Device` struct: `VendorID`, `ProductID`, `Path`, `Manufacturer`, `Product`, `Serial`.
   - `Enumerate() ([]Device, error)`: returns all HID devices. Safe to call frequently (hidraw enumerates from `/sys`).
   - `Open(path string) (*Handle, error)`: opens a device by path.
   - `Handle.Close() error`, `Handle.GetFeature(reportID byte, buf []byte) (int, error)`, `Handle.SendFeature(buf []byte) error`, `Handle.Read(buf []byte, timeout time.Duration) (int, error)`, `Handle.Write(buf []byte) error`.
3. Create `internal/hal/usbbase/usbbase_test.go`:
   - Table-driven tests using a `fakehid` shim that implements the same interface.
   - Test: Enumerate returns expected list on stubbed fakehid.
   - Test: Open/Close lifecycle; double-close is idempotent.
   - Test: Read/Write/Feature operations pass-through correctly.
4. Add `internal/testfixture/fakehid/fakehid.go`:
   - In-memory HID device simulator matching the `usbbase` interface.
   - Supports script-driven expected-read/expected-write sequences for tests.
5. Document that main daemon uses hidraw path (no libusb CGO). Confirm via `go build -tags hidraw ./...` and `CGO_ENABLED=0 go build ./...` both succeed.
6. Add CHANGELOG entry under Unreleased/Added: "feat(hal/usbbase): shared USB HID primitive layer (P2-USB-BASE)".

## Definition of done

- `internal/hal/usbbase/` compiles under CGO_ENABLED=0.
- `internal/hal/usbbase/usbbase_test.go` passes with `-race`.
- `internal/testfixture/fakehid/` is importable and has its own minimal test.
- `go mod tidy` shows `sstallion/go-hid` at its current stable version.
- No changes outside the files listed in the allowlist below.
- CHANGELOG has a single one-line entry.
- `go vet ./internal/hal/usbbase/...` clean.
- `gofmt -l internal/hal/usbbase/` produces no output.
- Binary size delta <= +150 KB (go-hid is lean, but it's a new dep; flag with SIZE-JUSTIFIED if larger).

## Out of scope for this task

- Any LIQUID or CROSEC-specific protocol code (those are P2-LIQUID-01 and P2-CROSEC-01).
- Writes to actual hardware in tests — all tests run against fakehid only.
- udev rules for device access permissions (those land with the first backend that needs them).
- Integration with `hal.Registry` — usbbase is a primitive, not a registered backend.
- Tests outside the scope this task targets per the testplan catalogue. P-task PRs add tests only as documented in testplan §18 row R19.

## Branch and PR

- Branch: `claude/P2-USB-BASE-primitive-layer`.
- Commit style: conventional commits.
- Open PR as ready-for-review (NOT draft) with title: `feat(hal/usbbase): shared USB HID primitive layer (P2-USB-BASE)`.
- PR description must include:
  - The goal verbatim.
  - Files-touched bulleted list.
  - "How I verified" showing `CGO_ENABLED=0 go build ./...`, `go test -race ./internal/hal/usbbase/... ./internal/testfixture/fakehid/...`, `go vet`, `gofmt -l`.
  - Binary size before/after.
  - Task ID: P2-USB-BASE.

## Allowlist

- `go.mod`, `go.sum`
- `internal/hal/usbbase/**` (new)
- `internal/testfixture/fakehid/**` (new)
- `CHANGELOG.md`

## Reporting

On completion (or if blocked):
- `STATUS`: done | partial | blocked.
- `PR`: URL.
- `BEFORE-SHA`, `AFTER-SHA`, `POST-PUSH GIT LOG`.
- `BUILD`, `TEST`, `GOFMT` output.
- `CONCERNS`: things you second-guessed.
- `FOLLOWUPS`: things you noticed that weren't in scope.
