# Tier 0.3 — Hardware Change Detection Validation

Date: 2026-04-14

## Scope

Proves that `internal/hwmon.Watcher` emits a `ComponentHardware` diagnostic
when `/sys/class/hwmon` topology changes post-install, and stays silent
through transient flaps. Two mechanisms, both live concurrently:

1. **Periodic rescan** — every 5 minutes, `EnumerateDevices()` is diffed
   against the last stable snapshot (safety net for containerised or
   netlink-restricted environments).
2. **Kernel uevents** — `NETLINK_KOBJECT_UEVENT` subscription filtered to
   `SUBSYSTEM=hwmon` + `ACTION=add|remove`. Event-driven, zero polling cost.

Both produce the same diagnostic: `IDHardwareChanged`, severity `info`,
with a `Remediation{ AutoFixReRunSetup → /api/setup/start }` button.

## Test layers

- **Unit (`internal/hwmon/watcher_test.go`)** — no-change/add/remove/
  changed cases, transient-flap debounce, StableDevice-keyed fingerprint
  stability across hwmonX renumbering. Enumeration is mocked so the tests
  are deterministic and hardware-independent.
- **Unit (`internal/hwmon/uevent_test.go`)** — raw netlink payload parser:
  add/remove detection, non-hwmon rejection, malformed-record tolerance,
  empty-payload rejection. Runs on any platform; no socket needed.
- **Bare-metal smoke** — `bare-metal-phoenix.md`, `bare-metal-homeserver.md`.
  Hot-plug via `modprobe -r && modprobe` of the board's fan-chip driver;
  capture the emitted diagnostic JSON.

## Debounce

- Settle window: **2 seconds** (`defaultDebounce`).
- A device that disappears and reappears (or vice versa) within the window
  produces zero diagnostics. Verified by `TestWatcher_TransientFlap_NoEmission`.
- Rationale: driver reloads (`modprobe -r X && modprobe X`) and short
  hwmon-core races on boot otherwise spam the UI.

## Periodic-only mode

Set `VENTD_DISABLE_UEVENT=1` in the environment (e.g. systemd drop-in
`Environment=VENTD_DISABLE_UEVENT=1`). The watcher logs
`hwmon watcher: uevent subscription unavailable` at INFO and the netlink
socket is not opened. Topology changes are still caught within 5 minutes.
Used for containerised deployments where netlink is blocked, and in
validation to prove the safety-net loop independently.

## Gate checks

- `go vet ./...` — clean
- `go test -race ./...` — clean
- `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` — clean
- `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...` — clean

## What's NOT in Tier 0.3

- **No auto re-run of setup.** The diagnostic surfaces the change and
  exposes a button; the user clicks. Rationale per STRATEGY.md §0.3:
  existing fans keep their calibration; adding a fan is a config decision.
- **No DMI-change detection.** DMI is read once at boot (Tier 3). A new
  motherboard without a reboot is out of scope.
- **No disk/network/USB hotplug.** `SUBSYSTEM` filter is `hwmon` only.
- **Controller safety is unchanged.** If uevent signals a remove for an
  hwmon device the controller is actively driving, the controller's
  existing EIO/ENOENT handling takes over — the watcher does not stop
  the controller.
