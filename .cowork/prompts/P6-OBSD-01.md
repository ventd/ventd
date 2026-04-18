# P6-OBSD-01 — OpenBSD fan backend via hw.sensors sysctl

**Care level: LOW.** OpenBSD's hw.sensors sysctl namespace is read-only
from userspace; there's no writable fan-control path for third-party
daemons (OpenBSD's philosophy: fan control belongs in the kernel or
firmware, not userspace). This task delivers a read-only backend for
temperature and fan-RPM visibility. Writes always return an error.

## Task

- **ID:** P6-OBSD-01
- **Track:** OBSD (Phase 6)
- **Goal:** Read-only FanBackend for OpenBSD via hw.sensors sysctl.
  Enumerates fan sensors and temperature sensors; write operations are
  unsupported and return a clear error.

## Context you should read first

- `internal/hal/backend.go` — FanBackend interface. Note: `Write` is
  mandatory on the interface; this backend's Write returns an error
  rather than being absent.
- `internal/hal/hwmon/backend.go` — Linux reference.
- `internal/hal/freebsd/` (from parallel P6-BSD-01) — closest sibling,
  but OpenBSD hw.sensors differs from FreeBSD's in detail.
- `cmd/ventd/main.go` — registration pattern.

### Key external references (citations — do NOT fetch)

- OpenBSD hw.sensors uses `sysctl` MIB: `hw.sensors`. Each entry is a
  `struct sensor` (128 bytes) or `struct sensordev` (wrapper).
- Access via `sysctl(3)`: `unix.Sysctl` with the `[CTL_HW, HW_SENSORS,
  devIdx, typeIdx, itemIdx]` oid form. Each level is an integer.
- Sensor types: `SENSOR_TEMP=0`, `SENSOR_FANRPM=1`, `SENSOR_VOLTS_DC=2`,
  many others. Only TEMP and FANRPM interest us here.
- Value format: `int64` raw, in μdegC (TEMP) or RPM (FANRPM).

## Design — read carefully, do not deviate

### Build tag

All files in `internal/hal/openbsd/` carry `//go:build openbsd`.
Registration in `cmd/ventd/main_openbsd.go`.

### Sensor struct

Needs exact 128-byte layout matching OpenBSD's `sys/sensors.h`:

```go
type sensor struct {
    Desc    [32]byte  // null-terminated description
    Tv      [16]byte  // struct timeval
    Value   int64
    Type    uint32    // SENSOR_* enum
    Status  uint32    // SENSOR_S_* enum
    Numt    uint32    // sensor sub-number
    Flags   uint32
    _pad    [40]byte
}
// Compile-time size assertion — NON-NEGOTIABLE
var _ [128]byte = [unsafe.Sizeof(sensor{})]byte{}
```

If the assertion fails at compile time, something is wrong. Do NOT
ship without this check.

### Enumeration

Walk the sysctl tree by iterating devIdx `0..N` until `ENOENT`. For each
device, iterate typeIdx `0..SENSOR_MAX_TYPES-1`, and for each type iterate
itemIdx `0..N` until ENOENT. That's three nested loops, but OpenBSD
convention — other OSes present flat trees, OpenBSD is hierarchical.

At each `itemIdx`, read the sensor struct; if `Type == SENSOR_FANRPM`,
emit a `hal.Channel` with role `hal.RoleCaseFan`, ID
`"obsd:dev{devIdx}:fan{itemIdx}"`, opaque carrying the full oid for
Read.

Temperature sensors (`SENSOR_TEMP`) are NOT enumerated as channels (same
reasoning as FreeBSD: controller has its own sensor-reading path).

### FanBackend methods

- `NewBackend(logger)`: walks the tree at construction, caches oid per
  channel.
- `Read(ch)`: single sysctl call using cached oid, returns
  `hal.Reading{RPM: uint32(sensor.Value), Valid: sensor.Status == 0}`.
- `Write(ch, pwm)`: returns `errors.New("openbsd: fan control via userspace unsupported; use firmware/BIOS settings")`.
  This is not a TODO; OpenBSD philosophy intentionally excludes this path.
- `Restore(ch)`: no-op returning nil (we never took control, nothing to
  restore).
- `Close`: no-op (stateless sysctl reads).
- `Name()`: `"openbsd"`.

### Tests

Cross-compile-only DoD. Minimal test set:
- `internal/hal/openbsd/smoke_build_test.go` with `//go:build openbsd`.
- Arch-independent oid-construction test (pure integer manipulation).
- Sensor-struct size assertion (already in production file as
  compile-time check).

## Out of scope for this PR

- Any write path (explicitly excluded per OpenBSD design).
- `sensorsd(8)` integration.
- Package manifest for OpenBSD ports.

## Definition of done

- `internal/hal/openbsd/` package with read-only FanBackend.
- `//go:build openbsd` on all production files.
- `GOOS=openbsd GOARCH=amd64 CGO_ENABLED=0 go build ./...` clean.
- Struct-size compile-time assertion present.
- No new dependencies beyond `golang.org/x/sys`.
- `cmd/ventd/main_openbsd.go` registers the backend.
- `.goreleaser.yml` includes `openbsd/amd64`.
- `CHANGELOG.md`: entry noting "OpenBSD read-only backend (fan control
  intentionally unsupported; see docs/platforms.md)."
- Smoke-compile test exists.
- go vet / gofmt clean (via `GOOS=openbsd go vet`).

## Branch and PR

- Branch: `claude/P6-OBSD-01-openbsd-backend`
- PR title: `feat(hal/openbsd): hw.sensors read-only fan backend (P6-OBSD-01)`
- Open as ready-for-review (NOT draft).

## Constraints

- Files touched (allowlist):
  - `internal/hal/openbsd/**` (all new)
  - `cmd/ventd/main_openbsd.go` (new)
  - `.goreleaser.yml`
  - `.github/workflows/build.yml` (IF needed for OpenBSD cross-compile)
  - `CHANGELOG.md`
  - `docs/platforms.md` (new if absent; a short matrix of OS x feature,
    documenting that OpenBSD is read-only by design)
- No new dependencies beyond `golang.org/x/sys`.
- `CGO_ENABLED=0` compatible.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: CROSS_COMPILE_VERIFICATION.
- Additional section: STRUCT_SIZE_VERIFICATION — copy the compile-time
  assertion from source.
- Additional section: OID_WALK_EXAMPLE — paste a hypothetical sensor
  tree walk showing how you iterate devIdx / typeIdx / itemIdx.

## Final note

Parallelizable with P6-WIN-01, P6-MAC-01, P6-BSD-01. Disjoint
directories.
