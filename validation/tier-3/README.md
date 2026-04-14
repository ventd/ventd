# Tier 3 — DMI-triggered Candidate Modules Validation

Date: 2026-04-14
Binary: `CGO_ENABLED=0 go build -o /tmp/list-fans-probe ./cmd/list-fans-probe/`

## Scope

Validates that when the Tier 2 capability-first pass finds no controllable
hwmon device, setup consults `/sys/class/dmi/id/*` and emits per-candidate
diagnostics (AutoFixID `TRY_MODULE_LOAD`) against a small seed table. When
capability pass succeeds, Tier 3 is skipped entirely.

Two layers:

1. **Unit coverage** (`internal/hwmon/dmi_test.go`,
   `internal/setup/dmi_diag_test.go`). The load-bearing layer: both canaries
   have populated hwmon, so the DMI pathway never fires in production. Tests
   cover the seed matching, stable ordering, case-insensitivity,
   idempotency, and the no-match warn entry.
2. **Bare-metal gate check** (`bare-metal-*.md`). Assert the precondition
   that Tier 3 does NOT execute on canary hosts, since capability pass finds
   Primary devices. The probe now prints DMI + what Tier 3 *would* propose
   if it were invoked, so the gate is visible without the code path firing.

## Seed triggers

| DriverNeed key | Chip | DMI trigger(s) |
|---|---|---|
| `it8688e` | IT8688E | `board_vendor` contains "gigabyte" |
| `nct6687d` | NCT6687D | `board_vendor` contains "micro-star"/"msi" AND `board_name` contains "mag"/"mpg" |

Deliberately narrow. MSI non-MAG boards (e.g. PRO Z690-A) correctly fall
through with no proposal — IT87 / NCT6775 chips there are handled by
capability pass once the in-kernel driver loads. Extending the seed is a
telemetry-driven follow-up (Tier 0.6), not a Tier 3 goal.

## Bare-metal results

| Host | DMI board | Capability pass | Tier 3 would propose | Tier 3 fires? |
|---|---|---|---|---|
| `phoenix@192.168.7.209` | MSI PRO Z690-A DDR4 | 8 PWM on hwmon6 (NCT6687D) | — (no match) | No ✓ |
| `root@192.168.7.10` | Gigabyte B550M AORUS PRO | 5 PWM on hwmon8 (IT8688) | it8688e | No ✓ |

See `bare-metal-phoenix.md` / `bare-metal-homeserver.md` for the full
probe output.

Phoenix is the better of the two proofs: its DMI string (`pro z690-a ddr4(ms-7d25)`)
deliberately doesn't match the MSI MAG/MPG triggers, so Tier 3 would find
nothing even if capability pass were empty. That's the intended behaviour
and confirms the seed narrowness.

## Gate checks

- `go vet ./...` — clean
- `go test -race ./...` — clean
- `go test -race ./internal/hwmon/... ./internal/setup/...` — clean

## What's NOT in Tier 3

- **No auto-modprobe.** The `TRY_MODULE_LOAD` diagnostic is a UI hint; the
  user clicks to load. `Remediation.Endpoint` is intentionally left empty
  (pattern documented in `hwdiag.Remediation`) until the install-driver
  HTTP endpoint lands with the UI pass.
- **No ARM device-tree equivalent.** That's Tier 5 wiring.
- **No encyclopedic DMI table.** Seed is 2 entries covering 6 triggers;
  telemetry (Tier 0.6) will expand coverage.
