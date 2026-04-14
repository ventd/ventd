# ventd deployment notes

## `ventd.service`

The shipped unit is sandboxed by default. Key points:

- `CapabilityBoundingSet=` and `AmbientCapabilities=` are **empty**. ventd
  does not need any capability at runtime. Sysfs pwm writes are
  DAC-checked by the kernel at `open()`; the common failure mode is
  "running as non-root with no write bit on the pwm file", and the
  correct fix is a udev rule, not `CAP_DAC_OVERRIDE`.
- `ReadWritePaths=` punches three write windows through
  `ProtectSystem=strict` / `ProtectKernelTunables=yes`:
  - `/etc/ventd` — `config.yaml`, `calibration.json`, auto-generated TLS
    cert + key.
  - `/run/ventd` — first-boot setup token.
  - `/sys/class/hwmon` — `pwm<N>` and `pwm<N>_enable`.
- `ConfigurationDirectory=ventd` with `ConfigurationDirectoryMode=0700` —
  systemd creates `/etc/ventd` before `ExecStart` (and before the namespace
  setup driven by `ReadWritePaths`), with the right mode, owned by the
  unit's `User=`. The installer no longer pre-creates the directory under
  systemd, so a `stop → rm -rf /etc/ventd → start` cycle no longer trips
  `status=226/NAMESPACE`. Mode stays at `0700` (root-owned) until the
  `User=ventd` migration introduces a group that needs read access.
- Under OpenRC/runit (where `ConfigurationDirectory=` has no effect) the
  installer creates `/etc/ventd` at mode `0700` itself — same end state,
  different mechanism.
- `RuntimeDirectory=ventd` with `RuntimeDirectoryMode=0700` — systemd
  creates `/run/ventd` before `ExecStart` with mode 0700 and cleans it up
  on stop. The setup token must not be world-readable; the mode is
  enforced here rather than relying on the `os.MkdirAll(..., 0700)` in
  the daemon.
- Network families are limited to `AF_UNIX` (journald),
  `AF_INET{,6}` (web UI listener), and `AF_NETLINK` (hwmon uevent
  watcher — `NETLINK_KOBJECT_UEVENT` for hot-plug detection). Without
  `AF_NETLINK` the watcher silently falls back to 5-minute periodic
  rescans and fans/sensors added after boot aren't noticed.

## Running as a non-root user (optional hardening)

The unit currently sets `User=root`. To drop to a dedicated user, create
the account and a udev rule that widens DAC on the pwm sysfs nodes for
the service group.

Example udev rule — `/etc/udev/rules.d/60-ventd-hwmon.rules`:

```udev
# Grant the "ventd" group write access to hwmon pwm control files.
# Matches pwm<N> and pwm<N>_enable on any hwmon device.
SUBSYSTEM=="hwmon", KERNEL=="hwmon[0-9]*", \
  RUN+="/bin/sh -c 'chgrp ventd /sys%p/pwm* 2>/dev/null; chmod g+w /sys%p/pwm* 2>/dev/null'"
```

Reload with:

```
sudo udevadm control --reload
sudo udevadm trigger --subsystem-match=hwmon
```

Then edit the unit:

```
User=ventd
Group=ventd
```

If you take this route, also make sure `/etc/ventd` and `/run/ventd`
are owned by `ventd:ventd` (the install script handles this when it is
told to use a non-root user).

## Why not `DynamicUser=` / `StateDirectory=` / `RuntimeDirectory=`?

Those directives are a cleaner way to manage the state/runtime dirs but
they move the paths (`/var/lib/private/ventd`, etc.) and break the
existing install script. That restructuring is tracked as a follow-up
PR — the current PR only adds hardening.

## Verifying the sandbox

```
systemd-analyze security ventd
```

The unit should land in the "OK" band (≈1.x–2.x). A passing score is
**not** sufficient on its own — always verify on real hardware that:

1. `systemctl start ventd` succeeds.
2. A pwm write lands. Set a fan through the UI, then
   `cat /sys/class/hwmon/hwmonX/pwmN` and confirm the value, or watch
   the RPM change.
3. First boot creates `/run/ventd/setup-token`.
4. TLS cert auto-generation writes to `/etc/ventd/`.
5. Writing to `/etc/ventd/` while it is `chmod 0500` still fails
   cleanly (loopback fallback from the TLS PR).
6. `journalctl -u ventd` shows no `EPERM` from the seccomp filter.

## Expected-benign log noise under the hardened unit

`AutoloadModules` tries to persist the winning hwmon module into
`/etc/modules-load.d/ventd.conf` (or `/etc/modules`) and optional
modprobe args into `/etc/modprobe.d/ventd.conf`. Under
`ProtectSystem=strict` those directories are read-only and the write
fails with:

```
could not persist hwmon module module=nct6775 err="write /etc/modules-load.d/ventd.conf: read-only file system"
```

This is benign: `AutoloadModules` still `modprobe`s the module fresh on
every start, so PWM channels appear regardless. The warn is noise, not
a failure. Tracked for a follow-up — either widen `ReadWritePaths` to
cover those dirs, drop the persist step entirely (systemd loads the
module on each start anyway), or downgrade the log level.
