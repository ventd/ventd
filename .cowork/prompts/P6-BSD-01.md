# P6-BSD-01 — FreeBSD fan backend via hw.sensors + superio

**Care level: LOW-MEDIUM.** FreeBSD hw.sensors is a read-only sysctl
namespace; reading it can't break anything. Writing to superio registers
has real hardware implications but is done through FreeBSD's superio(4)
kernel driver with kernel-enforced bounds. Core risk is shipping code
that doesn't compile on FreeBSD — the masterplan's acceptance bar.

## Task

- **ID:** P6-BSD-01
- **Track:** BSD (Phase 6)
- **Goal:** FanBackend for FreeBSD using hw.sensors sysctl tree for
  enumeration and temperature reads, plus the superio(4) driver for
  fan control on motherboards with a recognised Super I/O chip.

## Context you should read first

- `internal/hal/backend.go` — FanBackend interface.
- `internal/hal/hwmon/backend.go` — Linux reference. Many conceptual
  parallels: hw.sensors is FreeBSD's rough equivalent of /sys/class/hwmon.
- `cmd/ventd/main.go` — backend registration pattern.

### Key external references (citations — do NOT fetch)

- `sysctl hw.sensors` returns a flat name=value list. Entries look like
  `hw.sensors.lm0.fan0=1234 RPM` and `hw.sensors.cpu0.temp0=42.00 degC`.
  The prefix (`lm0`, `cpu0`) identifies the provider driver.
- Access via `sysctl(3)`: `sysctlbyname` with name, returns a raw
  buffer of type-specific data. Use `golang.org/x/sys/unix`'s
  `SysctlRaw` variant.
- superio(4): FreeBSD exposes `/dev/superio0`. Writes use `ioctl` with
  `SUPERIO_WRITE` command. Kernel mediates.

## Design — read carefully, do not deviate

### Build tag

All files in `internal/hal/freebsd/` carry `//go:build freebsd`.
Registration in `cmd/ventd/main_freebsd.go`.

### Enumeration — two-phase

1. **Sensor enumeration via hw.sensors:**
   Walk the sysctl tree using `unix.SysctlRaw` for the `hw.sensors`
   MIB. For each entry, parse name+type:
   - `fan` sensors → `hal.Channel` for reads. RPM is the value.
   - `temp` sensors → NOT emitted as channels (FanBackend.Enumerate is
     fan-centric); temperature is read per-tick via the controller's
     separate sensor-reading path, which already uses hw.sensors
     on FreeBSD. You will NOT implement that path here — assume the
     controller has an OS-aware sensor reader.

2. **Writable-fan enumeration via superio:**
   Try to open `/dev/superio0`. If absent, fan writes are not supported
   — enumerated fans are read-only (PWM writes return
   `errors.New("freebsd: superio not available; no fan control on this host")`).
   If present, for each fan sensor from step 1, associate it with the
   superio chip if the name prefix matches `it`, `nct`, `fintek`, or
   `winbond` (common Super I/O chip families exposed through the
   superio driver). Channels associated with a superio chip get a
   writable flag; others don't.

### FanBackend methods

- `NewBackend(logger)`: walks hw.sensors, opens /dev/superio0 if
  present, builds channel list.
- `Read(ch)`: re-reads the fan sysctl, returns `hal.Reading{RPM: ...}`.
- `Write(ch, pwm)`: if channel is writable (superio-backed), issue
  `SUPERIO_WRITE` ioctl against the specific chip register derived from
  the channel ID. Conversion: scale 0-255 PWM to the chip's PWM
  register range (typically 0-255 for NCT/IT chips; confirm from
  `superio(4)` man page at build time and hardcode).
  If not writable, return a clear error.
- `Restore(ch)`: ioctl with the firmware-auto mode register value
  (typically 0x00 on NCT). Captured at NewBackend.
- `Close`: close /dev/superio0 fd.
- `Name()`: `"freebsd"`.

### Sensor name parsing

hw.sensors is flat text. Parse the node-index form:
`hw.sensors.<provider><idx>.<type><idx>=<value> <unit>`.
Example: `hw.sensors.nct0.fan2=1850 RPM`.

Provider regex: `^(?P<provider>[a-z]+)(?P<p_idx>[0-9]+)$`.
Sensor regex: `^(?P<type>fan|temp|volt)(?P<s_idx>[0-9]+)$`.

Invalid entries skipped silently, logged at DEBUG. Don't fail
NewBackend on one malformed entry.

### Tests

Cross-compile-only DoD. Minimal test set:
- `internal/hal/freebsd/smoke_build_test.go` with
  `//go:build freebsd` and a trivial compile test.
- `internal/hal/freebsd/parse_test.go` (arch-independent; no build tag)
  for the sensor-name parser alone, since that logic is pure string
  manipulation and can be unit-tested on any host. Table-driven: known
  good names, malformed names, edge cases (multiple digits).

## Out of scope for this PR

- Writing sensors that aren't in the superio list (e.g. IPMI sensors on
  FreeBSD — covered by P2-IPMI-01 portability, or left for a later task).
- `sensorsd(8)` integration (FreeBSD's built-in sensor daemon).
- pkg manifest for FreeBSD ports tree (future packaging task).
- Running tests on a FreeBSD runner.

## Definition of done

- `internal/hal/freebsd/` package with FanBackend.
- `//go:build freebsd` on all production files.
- `GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build ./...` clean.
- Arch-independent parser test passes on Linux CI.
- No new dependencies beyond `golang.org/x/sys`.
- `cmd/ventd/main_freebsd.go` registers the backend.
- `.goreleaser.yml` includes `freebsd/amd64` (and arm64 if easy).
- `CHANGELOG.md`: entry under `## Unreleased / ### Added`.
- Smoke-compile test exists for FreeBSD target.
- Parser test present and passing.
- go vet / gofmt clean (including via `GOOS=freebsd go vet`).

## Branch and PR

- Branch: `claude/P6-BSD-01-freebsd-backend`
- PR title: `feat(hal/freebsd): hw.sensors + superio fan backend (P6-BSD-01)`
- Open as ready-for-review (NOT draft).

## Constraints

- Files touched (allowlist):
  - `internal/hal/freebsd/**` (all new)
  - `cmd/ventd/main_freebsd.go` (new)
  - `.goreleaser.yml`
  - `.github/workflows/build.yml` (IF needed for FreeBSD cross-compile)
  - `CHANGELOG.md`
- No new dependencies beyond `golang.org/x/sys`.
- `CGO_ENABLED=0` compatible.
- Preserve Linux/Windows/macOS behaviour unchanged.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: CROSS_COMPILE_VERIFICATION — output of the
  FreeBSD build command.
- Additional section: PARSER_TEST_COVERAGE — list the test cases in
  parse_test.go.
- Additional section: SUPERIO_CHIP_MATRIX — list the chip families
  (it/nct/fintek/winbond) and what PWM-register range each uses.

## Final note

Parallelizable with P6-WIN-01, P6-MAC-01, P6-OBSD-01. Disjoint
directories.
