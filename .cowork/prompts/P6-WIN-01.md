# P6-WIN-01 — Windows fan backend via WMI + ACPI

**Care level: MEDIUM.** This is a new cross-platform code path that
compiles on Windows only (build tag `windows`). It does not affect any
Linux runtime path. Mistakes here produce a Windows binary that fails
to enumerate fans — not a kernel panic. But: this is Windows, and Win32
API calls that pass garbage structs can BSOD a VM. Treat COM calls with
the same care as ioctls.

## Task

- **ID:** P6-WIN-01
- **Track:** WIN (Phase 6)
- **Goal:** Implement a FanBackend for Windows using WMI for temperature
  readings and vendor SDK / documented ACPI methods for fan control. No
  WinRing0 dependency (unsigned kernel driver, security risk).
  `CGO_ENABLED=0` preserved via `github.com/ebitengine/purego` to call
  into Win32 dynamically.

## Context you should read first

- `internal/hal/backend.go` — the FanBackend interface you're implementing.
- `internal/hal/hwmon/backend.go` — reference Linux implementation. Note
  the overall shape: NewBackend, Enumerate, Read, Write, Restore, Close,
  Name.
- `internal/hal/asahi/` — already-merged cross-platform backend (Phase 2).
  Shows the build-tag + purego pattern.
- `cmd/ventd/main.go` — how backends are registered. You'll add a
  Windows-build-tagged registration.
- `.goreleaser.yml` — you'll add `windows` to the targets matrix.

## Design — read carefully, do not deviate

### Build tag

All files in `internal/hal/windows/` carry `//go:build windows` at the
top. The registration in `cmd/ventd/main.go` goes in a
`main_windows.go` file (also tagged) so `go build` on Linux doesn't
pull Windows-only code.

### Temperature readings — WMI `MSAcpi_ThermalZoneTemperature`

WMI is accessed via COM. Claude Code should NOT write hand-rolled COM
from scratch — that path leads to days of debugging. Use the
`github.com/go-ole/go-ole` library IF it's pure-Go; verify in go.mod
that no CGO is required. If go-ole requires CGO, fall back to purego
dlopen of `oleaut32.dll` + `ole32.dll` + a hand-rolled IWbemServices
vtable. Document whichever path you choose with a CONCERN if it's the
fallback.

WMI query:

```
SELECT * FROM MSAcpi_ThermalZoneTemperature
```

Namespace: `root\WMI`. Each row has `InstanceName` and
`CurrentTemperature` (tenths of Kelvin). Convert to °C:
`(CurrentTemperature / 10.0) - 273.15`.

Return as `hal.Reading{Temperature: ..., TemperatureValid: true}`. RPM
is 0 (WMI exposes temps, not fan RPM in any standard way).

### Fan control — two-tier

1. **Vendor SDK tier (best):** if the machine is MSI/Gigabyte/ASUS and
   we have a pure-Go wrapper for their SDK (MSI.CentralServer COM,
   Gigabyte.AORUS, ASUS.AuraSync), use it.
2. **ACPI method tier (fallback):** invoke documented ACPI methods via
   `\Windows\System32\drivers\ACPI.sys` through `NtDeviceIoControlFile`
   on `\\.\ACPI`. Specifically: `_FST` (get fan status), `_FSL` (set
   fan level). This is documented by Microsoft in the ACPI spec and
   needs no driver install.

For v1, ship only the ACPI tier; vendor SDKs are FOLLOWUPS. Document
this as a CONCERN: "Vendor-specific fan APIs not wired in this PR;
ACPI fallback works on most systems but gives coarse (0-100% integer)
control."

### Fan enumeration

Enumerate returns one `hal.Channel` per ACPI fan discovered via
`_FIF` (Fan Information) method walks. If the system has no ACPI fans
(common on desktop with third-party controllers), return `[]` cleanly,
no error. Role classification: `hal.RoleCaseFan` for all enumerated
Windows fans in v1 (ACPI doesn't distinguish).

### Restore

On Close / Restore, invoke `_FSL` with the fan's original level
(captured at NewBackend). If capture failed, fall back to 100% (safe
default for Windows — it hands back to firmware auto since most
firmwares take over above 95%).

### Gating / non-Windows behaviour

On a Windows host without ACPI fans: Enumerate returns 0 channels. OK.
On a Windows host where WMI is not accessible (rare — requires admin):
NewBackend returns an error; ventd falls back to no fan control (same
as Linux with no hwmon).

### Tests

Cross-compile-only DoD (per masterplan §8 P6-WIN-01 row: "GOOS=windows
go build succeeds; Win11 VM enumerates temps"). No unit tests that run
on Windows CI yet (that's Phase 6b infrastructure work). But:

**DO add** a compile-smoke test:
- `internal/hal/windows/smoke_build_test.go` with the build tag
  `//go:build windows` — a trivial `TestCompiles(t *testing.T) { _ = NewBackend() }`
  so at minimum the package doesn't rot.

**DO add** an arch-independent config-side test:
- A single test that validates the backend-registration string `"windows"`
  doesn't collide with any existing backend name.

No other tests in this PR. Integration test will come in P6-WIN-02
(packaging + Windows Service).

## Out of scope for this PR

- Windows Service wrapper (P6-WIN-02).
- MSI installer (P6-WIN-02).
- Vendor SDK integrations (MSI/Gigabyte/ASUS).
- WinRing0 or any unsigned kernel driver — explicitly prohibited.
- libresensors or third-party temperature libraries.
- GPU-specific paths (NVML on Windows is a separate backend, not this task).
- Any Linux-side behaviour changes.

## Definition of done

- `internal/hal/windows/` package with FanBackend implementation.
- Build tag `windows` throughout. Linux build unaffected.
- `GOOS=windows GOARCH=amd64 go build ./...` succeeds cleanly on the
  CI runner (add a cross-compile job in `.github/workflows/build.yml`
  if one doesn't already cover Windows).
- `CGO_ENABLED=0` preserved on all platforms.
- No new dependencies beyond `github.com/go-ole/go-ole` (if pure-Go) or
  `github.com/ebitengine/purego` (already in tree? verify). If neither,
  document the path chosen.
- `cmd/ventd/main_windows.go` registers the backend.
- `.goreleaser.yml` includes `windows/amd64` target.
- `CHANGELOG.md`: entry under `## Unreleased / ### Added` noting
  "Windows backend (no fan control UI yet — enumeration + temperature only)."
- Smoke-compile test exists.
- go vet / golangci-lint / gofmt clean (on Linux — Windows-tagged files
  still get vet'd via `GOOS=windows go vet`).

## Branch and PR

- Branch: `claude/P6-WIN-01-windows-backend`
- PR title: `feat(hal/windows): WMI + ACPI fan backend (P6-WIN-01)`
- Open as ready-for-review (NOT draft).

## Constraints

- Files touched (allowlist):
  - `internal/hal/windows/**` (all new)
  - `cmd/ventd/main_windows.go` (new, for build-tagged registration)
  - `.goreleaser.yml`
  - `.github/workflows/build.yml` (IF needed to add Windows
    cross-compile; if Windows target already exists, skip)
  - `CHANGELOG.md`
  - `go.mod` / `go.sum` (if adding go-ole or purego, whichever works)
- No new direct dependency outside go-ole or purego.
- `CGO_ENABLED=0` compatible on ALL platforms.
- Preserve every existing safety guarantee on Linux.
- Do NOT ship a vendor SDK integration; ACPI-only for v1.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: CROSS_COMPILE_VERIFICATION — paste the output of
  `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...` showing
  clean exit.
- Additional section: WMI_QUERY_SAMPLE — the exact WMI query string
  used, plus a hypothetical 2-row result (fake data) demonstrating the
  parsing logic.
- Additional section: COM_PATH_CHOSEN — `go-ole` or `purego`; say which
  and why (one paragraph).

## Final note

This task is parallelizable with P6-MAC-01, P6-BSD-01, P6-OBSD-01.
They touch entirely disjoint directories (`internal/hal/macos/`,
`internal/hal/freebsd/`, `internal/hal/openbsd/`). No rebase conflicts
expected.
