# Tier 2 — Capability-First hwmon Discovery Validation

Date: 2026-04-14
Binary: `go build -o /tmp/ventd-test ./cmd/ventd/` (CGO_ENABLED=0)

## Scope

Validates that `hwmon.EnumerateDevices` replaces chip-name gating with
capability-first classification, and that `setup.discoverHwmonControls`
still promotes the same set of writable channels on known-good bare-metal
hosts.

Split into two layers:

1. **Unit capability matrix** (`internal/hwmon/enumerate_test.go`). Covers
   all four capability classes plus the NVIDIA skip case using synthetic
   sysfs trees under `t.TempDir()`. This is the load-bearing coverage
   because VMs have no Super I/O and cannot exercise the real classifier
   against real hardware.
2. **Bare-metal regression smoke** (`bare-metal-*.md` in this directory).
   Confirms the primary fan set on NCT6687D and on the home server is
   identical to the pre-Tier-2 output.

## Capability matrix (unit)

| Class | Sysfs shape | Expected consumer behaviour |
|---|---|---|
| `ClassNoFans` | only `tempN_input` | skipped by `discoverHwmonControls` |
| `ClassReadOnly` | `fanN_input` present, no `pwmN_enable` | skipped for control; surfaces RPM for UI badge |
| `ClassOpenLoop` | `pwmN` + `pwmN_enable`, no `fanN_input` | promoted to candidate; calibration notes lack of RPM readback |
| `ClassPrimary` | `pwmN` + `pwmN_enable` + `fanN_input` | promoted, standard calibration path |
| `ClassSkipNVIDIA` | chip name `nvidia` | skipped; NVML drives those fans |

Tested by `TestEnumerateDevices_CapabilityMatrix`,
`TestEnumerateDevices_NumericSort`, `TestEnumerateDevices_MissingRoot`.

## Bare-metal smoke (template)

Run on each canary host, paste output into the per-host scenario file:

```
# pre-Tier-2 baseline (captured from git main@c1e925f before the change landed)
/tmp/ventd-main -print-fans > /tmp/fans-pre.txt
# post-Tier-2
/tmp/ventd-test -print-fans > /tmp/fans-post.txt
diff -u /tmp/fans-pre.txt /tmp/fans-post.txt
```

Expected: empty diff (identical control path set).

| Host | Chip | Discovered set | Outcome |
|---|---|---|---|
| `phoenix@192.168.7.209` | NCT6687D (OOT `nct6687d`) | hwmon6: 8 PWM channels | ✓ PASS — identical pre/post |
| `root@192.168.7.10` | IT8688 (in-kernel `it87`) | hwmon8: 5 PWM channels | ✓ PASS — identical pre/post |

Probe source: `cmd/list-fans-probe` (committed alongside Tier 2 as a
validation helper — not a runtime component). Raw outputs archived in
`probe-phoenix.txt` and `probe-homeserver.txt`.

## Gate checks

- `go vet ./...` — clean
- `go test -race ./...` — clean (full suite)
- `go test -race ./internal/hwmon/... ./internal/setup/...` — clean

## Non-regressions verified in unit

- Sort stability: `hwmon2` precedes `hwmon10`.
- NVIDIA entries with controllable-looking PWM are still excluded
  (NVML owns those fans).
- `fanN_target` + `pwmN_enable` + `fanN_input` (pre-RDNA AMD shape) still
  classifies as `ClassPrimary` so the RPM-target calibration path (Tier 1.5)
  continues to receive the same inputs.
