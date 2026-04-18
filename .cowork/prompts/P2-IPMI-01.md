You are Claude Code, working on the ventd repository.

## Task
ID: P2-IPMI-01
Track: IPMI
Goal: Native IPMI backend. Talk to `/dev/ipmi0` directly via ioctl — no shell-out to `ipmitool`, no external dependency. Implements `Get Sensor Reading` (0x04/0x2D) for temperature and fan reads, and vendor-specific fan-control commands for Supermicro (0x30/0x70/0x66) and Dell (0x30/0x30). HPE stub only — their raw command set requires paid firmware; leave a clear error path.

## Care level
HIGH. IPMI sends raw bytes to a BMC that controls server-grade fan hardware. A malformed command can spin fans to 100% continuously or — worse — shut them off. Every write path MUST have a fallback to firmware-auto on daemon exit. Every vendor command must be DMI-gated (chassis_type=23 or vendor string match) so a desktop without IPMI can't accidentally send BMC commands to random `/dev/ipmi*` devices.

## Context you should read first

- `internal/hal/backend.go` — FanBackend interface.
- `internal/hal/hwmon/backend.go` — reference implementation.
- `internal/hwdb/` — DMI fingerprint database; use `hwdb.Match` for vendor gating.
- `/dev/ipmi0` ioctl interface docs: `ipmi-devintf(7)`, `include/uapi/linux/ipmi.h` (specifically `IPMICTL_SEND_COMMAND` and `IPMICTL_RECEIVE_MSG_TRUNC`).
- IPMI 2.0 spec sections on Get Sensor Reading (§35.14) and Set Fan Control (§28.3 + vendor extensions).

## What to do

1. Create `internal/hal/ipmi/backend.go`:
   - IPMI request envelope (NetFn, Cmd, Data) pure Go.
   - ioctl wrappers for IPMICTL_SEND_COMMAND and IPMICTL_RECEIVE_MSG_TRUNC via `golang.org/x/sys/unix`.
   - `type Backend struct { device string; logger *slog.Logger; vendor string }`.
   - `func NewBackend(logger *slog.Logger) *Backend` — default `/dev/ipmi0`, detect vendor at construction.

2. DMI gating: in `NewBackend`, consult `hwdb.Match` on current DMI. If chassis_type is not 23 (rack-mount) and vendor is not in a known-server list (Supermicro, Dell, HPE, Lenovo), `Enumerate` returns empty and logs `ipmi: not a server chassis, skipping`. Don't even open /dev/ipmi0.

3. Vendor detection: Supermicro if DMI vendor contains "Supermicro"; Dell if contains "Dell"; HPE if contains "HP" or "HPE"; else `unknown`. `vendor=unknown` → read-only enumeration (sensor reads work, fan writes return `errors.New("ipmi: unsupported vendor for fan control")`).

4. `Enumerate(ctx)`:
   - For each fan SDR entry (use Reserve SDR + Get SDR 0x20/0x20/0x21/0x23 sequence) with sensor type = 0x04 (fan), emit a `hal.Channel` with role `hal.RoleAIOFan` (servers don't distinguish case/CPU fans at the BMC level typically).

5. `Read(ch)`: Get Sensor Reading (NetFn 0x04, Cmd 0x2D) → reading byte + availability. Returns `hal.Reading{RPM: <converted via SDR m/b/k1/k2>}`.

6. `Write(ch, pwm)`: vendor-specific:
   - Supermicro: NetFn 0x30, Cmd 0x70, Data `[0x66, 0x01, <zone>, <pwm_percent>]`.
   - Dell: NetFn 0x30, Cmd 0x30, Data `[0x02, 0xff, <pwm_percent>]`.
   - HPE: return `errors.New("ipmi: HPE fan control requires iLO Advanced; not supported")`.
   - Convert 0-255 PWM to 0-100% per vendor convention.

7. `Restore(ch)`: vendor-specific return-to-firmware-auto:
   - Supermicro: NetFn 0x30, Cmd 0x45, Data `[0x00]` (SET_FAN_MODE = Standard).
   - Dell: NetFn 0x30, Cmd 0x30, Data `[0x01, 0x01]` (enable automatic fan control).
   - HPE: no-op (we never took control).

8. `Close`: close the fd.

9. `Name()`: return `"ipmi"`.

10. Unit tests in `internal/hal/ipmi/backend_test.go`:
    - `TestEnumerate_NonServerDMI_Empty` — seed hwdb with a desktop fingerprint; Enumerate returns 0 channels.
    - `TestVendorDetection` — table-driven, Supermicro/Dell/HPE/unknown vendor string classification.
    - `TestWritePWM_UnknownVendor_Error` — vendor=unknown path returns the expected error.
    - `TestPWMConversion` — 0-255 → 0-100% per-vendor rounding.
    - Real ioctl tests are deferred to T-IPMI-01 with fakeipmi fixture.

11. Register backend in `cmd/ventd/main.go` alongside hwmon and nvml.

12. Build, vet, lint, test all clean.

## Definition of done

- `internal/hal/ipmi/` package with backend + unit tests.
- DMI gating verified (non-server → empty enumeration, no /dev/ipmi0 access).
- Three vendors handled: Supermicro + Dell write-capable, HPE clear error.
- Restore path per-vendor.
- Backend registered in main.
- No new dependencies beyond `golang.org/x/sys`.
- CHANGELOG entry.

## Out of scope

- Socket-activated sidecar (P2-IPMI-02 handles the capability-scoped unit).
- Tests requiring real BMC (HIL tier, not CC).
- LAN IPMI (lanplus) — device-only for now.
- SEL / FRU / chassis commands.
- P-task tests beyond the unit tests in DoD; full IPMI test suite is T-IPMI-01.

## Branch and PR

- Branch: `claude/P2-IPMI-01-native-backend`
- Title: `feat(hal/ipmi): native IPMI backend via /dev/ipmi0 ioctl (P2-IPMI-01)`
- PR body includes the vendor matrix table.

## Constraints

- Files: `internal/hal/ipmi/**`, `cmd/ventd/main.go` (registration only), `CHANGELOG.md`.
- No new dependencies outside `golang.org/x/sys`.
- `CGO_ENABLED=0` compatible.
- Preserve all safety guarantees.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional field: VENDOR_MATRIX with table of implemented write paths.
