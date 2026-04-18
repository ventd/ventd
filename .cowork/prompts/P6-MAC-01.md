# P6-MAC-01 — macOS fan backend via IOKit SMC

**Care level: MEDIUM.** macOS SMC commands control real fan hardware on
both Intel and Apple Silicon Macs. Sending an out-of-range fan target
can spin a MacBook fan to 100% indefinitely (annoying) or to 0%
(hardware shutoff on Intel; M-series clamps safely). Treat SMC writes
with the same care as ACPI — validated, clamped, restore-on-exit.

## Task

- **ID:** P6-MAC-01
- **Track:** MAC (Phase 6)
- **Goal:** FanBackend for macOS using IOKit SMC (System Management
  Controller). Works on Intel Macs (SMC is Intel-era hardware but
  accessible) and Apple Silicon (under Rosetta or native; SMC shim
  exists on M-series). Implemented in pure-Go via purego dlopen of
  `/System/Library/Frameworks/IOKit.framework/IOKit`.

## Context you should read first

- `internal/hal/backend.go` — FanBackend interface.
- `internal/hal/asahi/` — Apple Silicon precedent. Note: Asahi targets
  Linux-on-M1, not macOS. P6-MAC-01 is macOS userspace. Different
  entirely, but same architectural pattern: purego dlopen + struct
  marshaling.
- `cmd/ventd/main.go` — backend registration.
- `.goreleaser.yml` — darwin/amd64 and darwin/arm64 targets.

### Key external references (do NOT fetch; these are citations for your
understanding)

- SMC keys documented in: `smcFanControl` open-source project (GitHub,
  MIT license). Key catalog: `F0Ac` (current fan 0 RPM actual),
  `F0Mn`/`F0Mx` (min/max fan 0 RPM), `F0Tg` (target fan 0 RPM), `F0Sf`
  (fan 0 safe RPM), `F0ID` (fan 0 ID string), `FNum` (number of fans
  as byte count).
- Temperature keys: `TC0P` (CPU proximity), `TG0P` (GPU proximity),
  `TCXC` (CPU PECI), `TN0P` (northbridge), many more — enumerate via
  `#KEY` which returns the count and then iterate `#KEYxxxx`.
- IOKit service name: `"AppleSMC"`. Open via `IOServiceMatching`, then
  `IOServiceOpen`, then `IOConnectCallStructMethod` with selector 2
  (read) or 1 (write), passing a 256-byte command struct.

## Design — read carefully, do not deviate

### Build tag

All files in `internal/hal/macos/` carry `//go:build darwin` at the top.
Registration in `cmd/ventd/main_darwin.go` (new, build-tagged).

### SMC call layer

Load IOKit dynamically:

```go
iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_NOW)
if err != nil {
    return nil, fmt.Errorf("iokit dlopen: %w", err)
}

var (
    IOServiceMatching      func(name *byte) uintptr
    IOServiceGetMatchingService func(mainPort uint32, matching uintptr) uint32
    IOServiceOpen          func(service uint32, owning uint32, typ uint32, conn *uint32) int32
    IOConnectCallStructMethod func(
        conn uint32, selector uint32,
        inputStruct uintptr, inputSize uintptr,
        outputStruct uintptr, outputSize *uintptr,
    ) int32
    IOServiceClose         func(conn uint32) int32
)
purego.RegisterLibFunc(&IOServiceMatching, iokit, "IOServiceMatching")
// ... etc for each
```

The SMC command struct (verbatim from smcFanControl source, under MIT):

```go
// 80 bytes. Layout matches the kernel interface exactly.
type smcKeyData struct {
    Key          uint32  // 4-char ASCII packed big-endian
    Vers         smcVers // 8 bytes
    PLimitData   smcPLimit // 16 bytes
    KeyInfo      smcKeyInfo // 12 bytes
    Result       uint8
    Status       uint8
    Data8        uint8
    Data32       uint32
    Bytes        [32]byte
}
// Total: 80 bytes. Verify with a compile-time assertion:
var _ [80]byte = [unsafe.Sizeof(smcKeyData{})]byte{}
```

Compile-time size assertions are non-negotiable. If the struct is the
wrong size, IOConnectCallStructMethod silently produces garbage.

### Key read / write helpers

```go
func (b *Backend) smcRead(key string) (smcKeyData, error) {
    inp := smcKeyData{Key: packKey(key)}
    inp.Data8 = 9  // SMC_CMD_READ_KEYINFO
    var outSize uintptr = 80
    var out smcKeyData
    rc := IOConnectCallStructMethod(b.conn, 2,
        uintptr(unsafe.Pointer(&inp)), 80,
        uintptr(unsafe.Pointer(&out)), &outSize)
    if rc != 0 {
        return smcKeyData{}, fmt.Errorf("smc read %q: rc=%d", key, rc)
    }
    // Second call to actually fetch the data using the returned KeyInfo
    inp.KeyInfo = out.KeyInfo
    inp.Data8 = 5  // SMC_CMD_READ_BYTES
    rc = IOConnectCallStructMethod(b.conn, 2,
        uintptr(unsafe.Pointer(&inp)), 80,
        uintptr(unsafe.Pointer(&out)), &outSize)
    if rc != 0 {
        return smcKeyData{}, fmt.Errorf("smc read bytes %q: rc=%d", key, rc)
    }
    return out, nil
}
```

`packKey` converts `"F0Ac"` to the big-endian uint32 representation.

### FanBackend methods

- `NewBackend(logger)`: IOServiceGetMatchingService("AppleSMC") →
  IOServiceOpen → captures `conn`. Fails cleanly if AppleSMC absent
  (e.g. sandboxed environment).
- `Enumerate`: reads `FNum` for fan count; for each index `i` in
  `0..FNum-1`, constructs a channel with ID `"smc:fan" + i`,
  role=`hal.RoleCaseFan`, opaque-field carrying the fan index.
- `Read(ch)`: reads `F{i}Ac` (RPM actual). Temperature readings are
  separate (we'd read e.g. `TC0P`); FanBackend.Read is fan-centric so
  just populate RPM and omit Temperature.
- `Write(ch, pwm)`: converts 0-255 PWM to an RPM target by linearly
  interpolating between `F{i}Mn` and `F{i}Mx`. Writes via SMC write
  (selector 1 equivalent). Original Min/Max captured at NewBackend for
  Restore.
- `Restore(ch)`: writes the captured `F{i}Sf` (safe RPM) back, which
  in SMC semantics hands control to firmware.
- `Close`: IOServiceClose, release IOKit ref.
- `Name()`: `"macos"`.

### Apple Silicon note

On M-series: SMC is emulated via a kernel shim (`AppleSMCRosetta` or
similar). Reads work but may return different keys than Intel —
particularly `TC0P` may be absent; use `Tp0X`, `Tp0Y` where available.
Document this as a CONCERN. Fallback behaviour on missing key: skip
the reading (omit from output), don't error.

### Tests (cross-compile-only DoD per masterplan)

Same as P6-WIN-01:
- `internal/hal/macos/smoke_build_test.go` —
  `//go:build darwin`, `TestCompiles(t *testing.T) { _ = NewBackend() }`.
- Cross-compile verification:
  `GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build ./...`
  `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...`

## Out of scope for this PR

- pkg installer + LaunchDaemon + notarisation (P6-MAC-02).
- GPU fan control (no public SMC keys; out of reach for v1).
- External thermal sensors (TG0P read works; tying to specific GPU fan
  is a future task).
- Running unit tests on macOS CI (infrastructure Phase 6b work).

## Definition of done

- `internal/hal/macos/` package with FanBackend.
- Build tag `darwin` throughout.
- `GOOS=darwin GOARCH=amd64 go build ./...` clean.
- `GOOS=darwin GOARCH=arm64 go build ./...` clean.
- `CGO_ENABLED=0` preserved.
- No new dependencies beyond purego (likely already in tree; verify).
- `cmd/ventd/main_darwin.go` registers the backend.
- `.goreleaser.yml` includes `darwin/amd64` and `darwin/arm64`.
- `CHANGELOG.md`: entry under `## Unreleased / ### Added`.
- Smoke-compile test exists.
- Struct-size compile-time assertion for `smcKeyData` present and
  verified.
- go vet / golangci-lint / gofmt clean (including via `GOOS=darwin go vet`).

## Branch and PR

- Branch: `claude/P6-MAC-01-macos-smc-backend`
- PR title: `feat(hal/macos): IOKit SMC fan backend via purego (P6-MAC-01)`
- Open as ready-for-review (NOT draft).

## Constraints

- Files touched (allowlist):
  - `internal/hal/macos/**` (all new)
  - `cmd/ventd/main_darwin.go` (new)
  - `.goreleaser.yml`
  - `.github/workflows/build.yml` (IF needed for cross-compile matrix)
  - `CHANGELOG.md`
  - `go.mod` / `go.sum` (if purego not already present)
- `CGO_ENABLED=0` compatible on ALL platforms.
- Preserve Linux safety guarantees.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: CROSS_COMPILE_VERIFICATION — outputs of both
  `GOOS=darwin GOARCH=amd64` and `GOARCH=arm64` compile runs.
- Additional section: SMC_KEY_CATALOG — list which SMC keys you
  implemented (F0Ac/F0Mn/F0Mx/F0Tg/F0Sf plus any temp keys).
- Additional section: STRUCT_SIZE_VERIFICATION — copy the compile-time
  size assertion code verbatim from your source.

## Final note

Parallelizable with P6-WIN-01, P6-BSD-01, P6-OBSD-01. Disjoint
directories, no conflicts expected.
