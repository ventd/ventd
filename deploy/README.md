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
- `ConfigurationDirectory=ventd` with `ConfigurationDirectoryMode=0750` —
  systemd creates `/etc/ventd` before `ExecStart` (and before the namespace
  setup driven by `ReadWritePaths`), with the right mode, owned by the
  unit's `User=` (`ventd:ventd`). The mode is `0750`, not `0700`, because
  the `ventd` group (whose only member is the daemon account itself) needs
  read access to `config.yaml` and the auto-generated TLS material; "other"
  stays locked out.
- Under OpenRC/runit (where `ConfigurationDirectory=` has no effect) the
  installer creates `/etc/ventd` at mode `0750` owned by `ventd:ventd` —
  same end state, different mechanism.
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

## User=ventd and the pwm udev rule

The unit runs as `User=ventd` / `Group=ventd`. The installer creates
that system account (nologin shell, no home directory) and owns
`/etc/ventd` and `/run/ventd` as `ventd:ventd`. `CapabilityBoundingSet=`
stays **empty** — no capabilities are granted to compensate for the
dropped root privilege. Pwm write access is DAC-driven instead.

The shipped udev rule at `deploy/90-ventd-hwmon.rules` is copied to
`/etc/udev/rules.d/90-ventd-hwmon.rules` by the installer. It
chgrp's `pwm<N>` and `pwm<N>_enable` to the `ventd` group with `g+w`
on every hwmon device bind — boot, hot-plug, or driver reload.

**Keying.** The rule matches on `ATTR{name}` (the chip name, e.g.
`nct6687`, `it87`, `amdgpu`), never on `KERNEL=="hwmon*"` or a
specific `hwmonN` index. Indices reshuffle across reboots whenever
driver load order changes; matching on chip name survives that.

**Find your chip name:**

```
for n in /sys/class/hwmon/hwmon*/name; do
  echo "$(dirname "$n"): $(cat "$n")"
done
```

Open `/etc/udev/rules.d/90-ventd-hwmon.rules`, uncomment the line(s)
that match, and reload:

```
sudo udevadm control --reload
sudo udevadm trigger --subsystem-match=hwmon
```

Only group write (`g+w`) is granted. The rule is never world-writable
and never setuid.

**Path precedence.** The installer writes to `/etc/udev/rules.d/`,
which takes precedence over `/lib/udev/rules.d/` (or
`/usr/lib/udev/rules.d/`) on every supported distro, so distro-shipped
rules for the same hwmon device cannot shadow it.

**Group membership.** Only the `ventd` daemon account is in the
`ventd` group. Do not add interactive users to it. For CLI debugging
of `/etc/ventd`, use `sudo -u ventd cat …` or plain `sudo`.

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
