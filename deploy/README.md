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
- Network families are limited to `AF_UNIX` (journald) and
  `AF_INET{,6}` (web UI listener).

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
