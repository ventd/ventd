# fix-305-usbbase-hardening

You are Claude Code. Harden the USB HID primitive layer per issue #305 concerns 1 and 2. Concern 3 (go mod tidy CI lint) is DEFERRED to a separate follow-up — out of scope here.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/fix-305-usbbase-hardening origin/main
test ! -f .cowork/prompts/fix-305-usbbase-hardening.md && echo "OK: working tree is main" || {
    echo "ERROR: cowork/state files present. Abort."
    exit 1
}
```

If the sanity check fails, stop and report.

## Context (#305 concerns 1 + 2)

**Concern 1:** `fakehid.DeviceHandle` operations (`Write`, `ReadWithTimeout`, `GetFeatureReport`, `SendFeatureReport`) don't check the `closed` flag that `Close()` sets. Real go-hid returns an error on post-close operations; the fixture does not, so tests that exercise disappearance-mid-operation pass on fakehid and fail in production.

**Concern 2:** `usbbase.Handle.mu` is acquired only by `Close()`. Non-Close operations (`Write`, `Read`, `SendFeature`, `GetFeature`) delegate to `h.raw` without the lock. The package doc claims "safe for concurrent use" — only technically true because the underlying `*hid.Device` has its own synchronisation. A future `RawDevice` implementation without internal sync gets silent races. Also: concurrent Close+Write races past the closed check.

Cassidy recommends option (b) for concern 2 — serialise all per-handle I/O under `h.mu`. Cost is ~microseconds per transaction; benefit is a clean contract for backend authors.

Concern 3 (go mod tidy drift CI lint) is separate scope — new workflow file, different reviewer attention. Filing a follow-up after this PR.

## Required changes

### 1. `internal/testfixture/fakehid/fakehid.go` — close checks

In each of `Write`, `ReadWithTimeout`, `GetFeatureReport`, `SendFeatureReport`:

```go
func (d *DeviceHandle) Write(p []byte) (int, error) {
    d.mu.Lock()
    defer d.mu.Unlock()
    if d.closed {
        return 0, fmt.Errorf("fakehid: write on closed device")
    }
    // ... existing capture logic ...
}
```

Apply the same `closed` check to the other three operations. If any operation currently does NOT acquire `d.mu`, add the acquire/release around the closed check; preserve the existing body.

Error messages should be specific: `"fakehid: read on closed device"`, `"fakehid: get_feature on closed device"`, `"fakehid: send_feature on closed device"`. Real go-hid's closed-device error message shape doesn't matter here — the fixture just needs to return a non-nil error so callers can detect the condition.

### 2. `internal/hal/usbbase/usbbase.go` — serialise handle operations

Current `Handle`:

```go
type Handle struct {
    mu     sync.Mutex
    raw    RawDevice
    closed bool
}
```

Extend each I/O method to acquire `h.mu` and check `h.closed`. Example for `Write`:

```go
func (h *Handle) Write(buf []byte) error {
    h.mu.Lock()
    defer h.mu.Unlock()
    if h.closed {
        return fmt.Errorf("usbbase: write on closed handle")
    }
    _, err := h.raw.Write(buf)
    return err
}
```

Apply the SAME pattern to `Read`, `SendFeature`, `GetFeature`. Read the current implementations first — preserve any existing error-wrapping, return-value shape, timeout parameter handling. Only the lock+closed-check wrapper is prescriptive.

Update the package doc comment to reflect the new guarantee:

```go
// Handle is an open USB HID device. All methods are safe for concurrent
// use: per-handle I/O is serialised by an internal mutex. Callers should
// still avoid holding their own lock across Handle method calls to prevent
// lock ordering issues.
```

### 3. Regression tests

`internal/testfixture/fakehid/fakehid_test.go` — if the file doesn't exist, create it:

```go
// regresses #305
func TestFakehid_OpsAfterCloseReturnError(t *testing.T) {
    // Open handle, Close, then each of Write/Read/GetFeature/SendFeature
    // must return non-nil error. Assert the error message contains "closed".
}
```

`internal/hal/usbbase/usbbase_test.go`:

```go
// regresses #305
func TestHandle_ConcurrentWriteAndClose(t *testing.T) {
    // 10 goroutines racing Write and Close on the same Handle should either
    // all write successfully (if Close lost the race) OR all return a closed
    // error AFTER Close. Neither: torn writes or post-close writes landing
    // at the raw device.
    // Run with go test -race.
}
```

The race test may need a fake `RawDevice` whose `Write` records call count; assert `rawWrites + closedErrors == totalGoroutines`.

## Allowlist

- `internal/testfixture/fakehid/fakehid.go`
- `internal/testfixture/fakehid/fakehid_test.go` (may be new)
- `internal/hal/usbbase/usbbase.go`
- `internal/hal/usbbase/usbbase_test.go`
- `CHANGELOG.md`

NO other files. Do NOT add new GitHub workflows or touch `.github/` — concern 3 is deferred.

## Verification

```bash
CGO_ENABLED=0 go build -tags hidraw ./...
GOFLAGS="-tags=hidraw" go test -race -count=1 ./internal/hal/usbbase/... ./internal/testfixture/fakehid/...
gofmt -l internal/hal/usbbase/ internal/testfixture/fakehid/
go vet -tags hidraw ./internal/hal/usbbase/... ./internal/testfixture/fakehid/...
```

The `-tags hidraw` flag is because usbbase depends on go-hid under a build tag. Verify by reading the file first — if the build tag name differs, use the actual one.

## PR

Open READY (not draft). Title: `fix(hal/usbbase,fakehid): close-checks + mu-serialise per-handle I/O (closes #305 concerns 1-2)`

PR body: Closes #305 (concerns 1+2 only; concern 3 deferred to follow-up), BRANCH_CLEANLINESS, CHANGELOG entry under `### Fixed`:

> `hal/usbbase: per-handle I/O now serialised and honours closed state; fakehid matches real go-hid closed-device semantics (closes #305 concerns 1-2; concern 3 tracked separately)`

## Constraints

- Atlas merges. Do NOT merge.
- Do NOT address concern 3 (go mod tidy drift CI lint). That is a separate follow-up.
- Do NOT change the `RawDevice` interface or `Handle` struct shape beyond field-level changes if strictly needed for close-check semantics.
- Preserve ALL existing I/O error-wrapping shapes — the fix is additive lock acquisition, not error-handling refactor.
- Single commit.
- If tests use a build tag gate (`//go:build hidraw`), apply it to the new test files too.

## Reporting

- STATUS: done | blocked
- PR URL
- `go test -race -count=1 ./internal/hal/usbbase/... ./internal/testfixture/fakehid/...` tail
- Lines changed per file
- CONCERNS:
  - Any lock-ordering hazards detected during implementation (e.g., Handle method invoked from within RawDevice callback — would deadlock)
  - If concern 3 (CI lint) should be filed as role:atlas or role:sage follow-up
