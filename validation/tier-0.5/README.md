# Tier 0.5 — OOT Module Fallback Chain Validation

Date: 2026-04-14
Binary: `go build -o /tmp/ventd-test ./cmd/ventd/` (CGO_ENABLED=0)
Helper: `cmd/preflight-check` — calls `hwmon.PreflightOOT` with a synthetic
`DriverNeed` on the live host so the classification chain can be exercised
on VMs that lack a real Super I/O chip. Accepts `-max-kernel=X.Y` to inject
a synthetic ceiling for the kernel-too-new case.

## Scope

Validates that the preflight → hwdiag → remediation chain behaves correctly
for every blocker class defined by `hwmon.Reason`. Split into two layers:

1. **Endpoint + classification on VMs** (this directory). Each scenario
   drives the live endpoint, verifies the wire shape, and confirms
   `PreflightOOT` classifies correctly on that distro.
2. **Preflight→hwdiag mapping as a unit test**
   (`internal/setup/preflight_diag_test.go`). Guards the `Reason → Entry`
   mapping including AutoFixID, endpoint, severity, and detail passthrough
   for all four blocker classes. This is the load-bearing test for the
   chain because none of the VMs carry a real Super I/O, so the live
   setup-manager preflight branch never fires on them.

## Results

| # | Scenario | Host | Expected Reason | Endpoint | Outcome |
|---|---|---|---|---|---|
| 1 | Missing kernel headers | VM 207 (Ubuntu 24.04, 192.168.7.208) | `KERNEL_HEADERS_MISSING` | `/api/hwdiag/install-kernel-headers` | ✓ headers reinstalled; preflight cleared |
| 2 | Missing DKMS | VM 207 | `DKMS_MISSING` | `/api/hwdiag/install-dkms` | ✓ dkms installed; preflight = OK |
| 3 | Secure Boot blocks | VM 201 (Debian 12 + SB, 192.168.7.224) | `SECURE_BOOT_BLOCKS` | `/api/hwdiag/mok-enroll` | ✓ `kind=instructions` returned; nothing executed server-side |
| 4 | Kernel too new | VM 203 (Arch, 192.168.7.217) | `KERNEL_TOO_NEW` (synthetic) | — (no remediation by design) | ✓ synthetic ceiling 6.6 triggers; no real driver sets a ceiling — **gap tracked in HARDWARE-TODO.md** |

Bare-metal regression smoke (`phoenix@192.168.7.209`, NCT6687D, kernel
6.17.0-20-generic, SB disabled): `preflight-check` reports `OK`. Read-only;
no PWM writes, no calibration.

## Gate checks

- `go vet ./...` — clean
- `go test -race ./...` — clean
- `internal/setup/preflight_diag_test.go` — 6 cases (4 blocker classes +
  `ReasonOK` no-op + no-store no-op) all pass
- `internal/hwmon/ootpreflight_test.go` — existing 7 cases + `TestKernelAbove`
  continue to pass (covers the chain order: SB → ceiling → headers → DKMS)

See per-scenario notes in this directory for captured JSON responses and
journalctl-equivalent daemon logs.
