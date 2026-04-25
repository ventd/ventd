# ventd deployment notes

## `ventd-ipmi.service` — privilege-separated IPMI sidecar

`ventd-ipmi` is an always-on helper process that holds the only privilege
needed to talk to a BMC: `CAP_SYS_RAWIO` and `DeviceAllow=/dev/ipmi0`.
The main `ventd` daemon runs with an empty `CapabilityBoundingSet=` (no
capabilities at all) and connects to the sidecar over a Unix socket at
`/run/ventd/ipmi.sock`.

The sidecar is optional: if `/dev/ipmi0` does not exist on the machine,
`ConditionPathExists=/dev/ipmi0` in the unit file prevents systemd from
even attempting to start it.

### Prerequisites

1. **System account** — created by `sysusers.d-ventd-ipmi.conf`:
   ```
   sudo systemd-sysusers /usr/lib/sysusers.d/ventd.conf
   # or on first install via postinstall.sh
   ```

2. **Runtime directory** — created by `tmpfiles.d-ventd.conf`:
   ```
   sudo systemd-tmpfiles --create /usr/lib/tmpfiles.d/ventd.conf
   # /run/ventd is also created at boot automatically once the file is installed
   ```

### Install

```bash
# Binary — install alongside ventd
sudo install -D -m 0755 ventd-ipmi /usr/libexec/ventd/ventd-ipmi

# sysusers + tmpfiles
sudo install -m 0644 deploy/sysusers.d-ventd-ipmi.conf /usr/lib/sysusers.d/ventd.conf
sudo install -m 0644 deploy/tmpfiles.d-ventd.conf /usr/lib/tmpfiles.d/ventd.conf
sudo systemd-sysusers /usr/lib/sysusers.d/ventd.conf
sudo systemd-tmpfiles --create /usr/lib/tmpfiles.d/ventd.conf

# systemd unit
sudo install -m 0644 deploy/ventd-ipmi.service /lib/systemd/system/ventd-ipmi.service
sudo systemctl daemon-reload

# The sidecar is pulled in automatically by ventd.service (WantedBy=ventd.service).
# You can also start it manually:
sudo systemctl enable --now ventd-ipmi.service
```

### Uninstall

```bash
sudo systemctl stop ventd-ipmi.service
sudo systemctl disable ventd-ipmi.service
sudo rm -f /lib/systemd/system/ventd-ipmi.service
sudo rm -f /usr/libexec/ventd/ventd-ipmi
sudo systemctl daemon-reload
# Account and tmpfiles drop-ins remain; remove when ventd itself is removed.
```

### Verify

```bash
# Check the sidecar is running and has CAP_SYS_RAWIO
systemctl status ventd-ipmi
grep CapPrm /proc/$(systemctl show -p MainPID --value ventd-ipmi)/status

# Check the socket exists and is group-writable by 'ventd'
ls -la /run/ventd/ipmi.sock

# Check the main daemon has NO IPMI capabilities
grep -E 'CapBnd|CapPrm' /proc/$(systemctl show -p MainPID --value ventd)/status
# Both lines should read 0000000000000000
```

### Security model

| Process | User | Caps | Device |
|---|---|---|---|
| `ventd` (main daemon) | `ventd` | none | none |
| `ventd-ipmi` (sidecar) | `ventd-ipmi` | `CAP_SYS_RAWIO` | `/dev/ipmi0` |

Communication is a length-prefixed JSON socket at `/run/ventd/ipmi.sock`
(mode `0660`, group `ventd`). Both processes are in the `ventd` group.

### TODO — AppArmor / SELinux profiles for ventd-ipmi

AppArmor and SELinux profiles for `ventd-ipmi` are not shipped yet.
Track in a follow-up PR. Until then, the sandbox is enforced exclusively
by systemd's `CapabilityBoundingSet=`, `DeviceAllow=`, `PrivateNetwork=yes`,
and `SystemCallFilter=` hardening.

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

To create the account without running the full installer:

```bash
sudo install -m 0644 deploy/sysusers.d-ventd.conf /usr/lib/sysusers.d/ventd.conf
sudo systemd-sysusers /usr/lib/sysusers.d/ventd.conf
```

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

## Module loading and the daemon sandbox

The shipped systemd unit runs the daemon under `ProtectKernelModules=yes`
(deny `init_module` / `finit_module`) and `ProtectSystem=strict`
(read-only `/etc`). Both of those would block a runtime `modprobe`
and any write to `/etc/modules-load.d/`.

Module probing is therefore done once at install time by
`ventd --probe-modules`, invoked from `scripts/install.sh` and
`scripts/postinstall.sh` while the installer still holds root and lives
outside the unit namespace. The winning module is persisted to
`/etc/modules-load.d/ventd.conf`; `systemd-modules-load.service`
re-loads it on every subsequent boot and the kernel-side udev rule
hands `g+w` on the resulting pwm files to the `ventd` group.

The long-running daemon never attempts these operations. At startup
it runs `DiagnoseHwmon`, a strictly read-only enumeration of
`/sys/class/hwmon`, and surfaces a remediation pointer in the journal
when no PWM channels are visible.

## Install-time security-module log

Both `scripts/install.sh` (binary / tarball / curl-pipe) and
`scripts/postinstall.sh` (.deb / .rpm) write a one-line outcome record
per security-module load attempt to `/var/log/ventd/install.log`.
Directory mode `0750`, file mode `0640`, owned `root:ventd`. The line
shape is:

```
2026-04-16T10:18:30Z apparmor=loaded  profile=/etc/apparmor.d/usr.local.bin.ventd
2026-04-16T10:18:30Z apparmor=refused parser_exit=1 profile=/etc/apparmor.d/usr.local.bin.ventd
2026-04-16T10:18:30Z apparmor=skipped reason=parser-not-installed
2026-04-16T10:18:31Z selinux=loaded   module=ventd.pp
2026-04-16T10:18:31Z selinux=refused  reason=semodule-refused module=ventd.pp
```

This is the answer to "did AppArmor actually confine ventd after
install?" once the install scrollback is gone. See #202, #204, #211.

At daemon startup, ventd additionally emits a `WARN` slog line when
`/etc/apparmor.d/usr.local.bin.ventd` exists on disk but
`/proc/self/attr/current` reads `unconfined` — this catches the
silent-downgrade class directly from `journalctl -u ventd` without
requiring the operator to remember where the install log lives.
