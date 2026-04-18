# P2-IPMI-01 — native IPMI backend

**Model:** Opus 4.7. IPMI is safety-critical (writes to server fan controllers). Protocol correctness matters; a wrong byte in a vendor set-fan-mode command can brick manual control until a power cycle.
**Care level:** HIGH. No real IPMI hardware in test loop — all coverage is via `fakeipmi` fixture. Get the protocol bits right.

## Task

- **ID:** P2-IPMI-01
- **Track:** IPMI (Phase 2)
- **Goal:** Native IPMI over `/dev/ipmi0` (no `ipmitool` dependency), as a `hal.FanBackend` implementation. Read sensor readings; write vendor-specific fan-mode commands; gracefully fall back to firmware auto on daemon exit. Supermicro + Dell verified; HPE stubbed with clear error.

## Context — read before editing

1. `ventdmasterplan.mkd` §8 P2-IPMI-01 entry and P2-IPMI-02 (socket-activated sidecar — this PR does NOT implement the sidecar; just reads /dev/ipmi0 directly with assumed access).
2. `internal/hal/backend.go` — FanBackend interface contract.
3. `internal/hal/hwmon/backend.go` — reference impl; IPMI backend follows the same Enumerate/Read/Write/Restore shape.
4. `internal/hal/nvml/nvml.go` — reference for a backend using purego + ioctl pattern.
5. `internal/watchdog/watchdog.go` — understand how Restore is called; IPMI Restore must put channel back into firmware auto.
6. IPMI spec references: IPMI 2.0 spec §35 (sensor commands), Supermicro X11 IPMI manual (OEM commands 0x30/0x70/0x66), Dell IPMI guide (OEM commands 0x30/0x30).

## What to do

1. Create `internal/hal/ipmi/ipmi.go` with:
   - `Backend` struct implementing `hal.FanBackend`.
   - Raw ioctl wrappers: `IPMICTL_SEND_COMMAND (0x8004690D)`, `IPMICTL_RECEIVE_MSG_TRUNC (0x8010690B)`. Use `golang.org/x/sys/unix.Syscall` (already in deps).
   - IPMI request envelope: NetFn, Cmd, LUN, data bytes. Response envelope: completion code + data.
   - DMI-gate: only enumerate when `/sys/class/dmi/id/chassis_type` is 17 or 23, OR vendor is Supermicro / Dell / HPE. Skip silently otherwise.
2. Create `internal/hal/ipmi/supermicro.go`:
   - Set Fan Mode: NetFn 0x30, Cmd 0x45, data `[mode]` (0=Standard, 1=Full, 2=Optimal, 4=Heavy IO).
   - Set Fan Speed: NetFn 0x30, Cmd 0x70, data `[0x66, 0x01, zone, pwm_0_to_100]`.
3. Create `internal/hal/ipmi/dell.go`:
   - Set Manual Fan Control: NetFn 0x30, Cmd 0x30, data `[0x01, 0x00]` (enter manual), `[0x01, 0x01]` (exit manual).
   - Set Fan Speed: NetFn 0x30, Cmd 0x30, data `[0x02, 0xFF, pwm_0_to_100]`.
4. Create `internal/hal/ipmi/hpe.go`:
   - Stub with clear error on Write: `return fmt.Errorf("ipmi/hpe: fan control via iLO REST API not implemented in this release")`.
   - Read sensor path is fine via generic 0x04/0x2D — iLO exposes IPMI sensors normally.
5. Create `internal/hal/ipmi/sensor.go`:
   - Get Sensor Reading: NetFn 0x04, Cmd 0x2D. Generic across all vendors.
   - SDR walk (optional; may stub for now): NetFn 0x0A (storage).
6. Create `internal/testfixture/fakeipmi/fakeipmi.go`:
   - Backed by a named pipe; script-driven expected-ioctl-sequence.
   - Canned responses for Supermicro, Dell SDR walks.
   - Used by `internal/hal/ipmi/ipmi_test.go`.
7. Create `internal/hal/ipmi/ipmi_test.go`:
   - Happy-path sensor read (Supermicro canned response).
   - Happy-path fan write on Supermicro.
   - Happy-path fan write on Dell.
   - HPE Write returns the clear-error message.
   - DMI gate: no chassis_type 17/23 → Enumerate returns empty.
   - Restore: after manual-mode entry, Restore puts channel back to firmware auto.
8. Wire into registry: `hal.Register("ipmi", ipmi.NewBackend(...))` in `cmd/ventd/main.go`.
9. Add CHANGELOG entry under Unreleased/Added.

## Definition of done

- `internal/hal/ipmi/` compiles CGO_ENABLED=0.
- `fakeipmi` fixture lets tests run without `/dev/ipmi0` access (CI has no IPMI).
- All tests pass `-race -count=1`.
- DMI gate verified: on non-server systems, `hal.Resolve("ipmi:...")` returns "no channels enumerated" cleanly.
- Vendor set-fan commands match documented byte sequences (Supermicro manual §5, Dell IPMI guide).
- Restore path hands control back to firmware auto on daemon exit — verified via fakeipmi ioctl sequence.
- CHANGELOG one-line entry.
- `go vet ./internal/hal/ipmi/...` clean, `gofmt -l` produces no output.
- Binary size delta noted in PR body; flag SIZE-JUSTIFIED if > +100 KB.

## Out of scope for this task

- Socket-activated sidecar for `/dev/ipmi0` capability (that's P2-IPMI-02; this PR assumes daemon has direct device access, which is temporarily true on systems where the installer granted it).
- IPMI LAN / RMCP+ remote access (future work; this is local `/dev/ipmi0` only).
- iLO REST API for HPE (stubbed).
- IPMI SDR full walk (stubbed or minimal).
- Tests outside the scope this task targets per the testplan catalogue.
- Changing `deploy/ventd.service` capability grants (P2-IPMI-02 territory).

## Branch and PR

- Branch: `claude/P2-IPMI-01-native-backend`.
- Title: `feat(hal/ipmi): native IPMI backend for Supermicro + Dell (P2-IPMI-01)`.

## Allowlist

- `internal/hal/ipmi/**` (new)
- `internal/testfixture/fakeipmi/**` (new)
- `cmd/ventd/main.go` (registry wire-up line only)
- `CHANGELOG.md`

## Reporting

Standard STATUS/PR/SHA/BUILD/TEST/CONCERNS/FOLLOWUPS block.
