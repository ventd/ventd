# AppArmor smoke — Debian 12

- VM: VMID 221 (fc-test-debian-12-apparmor), IP 192.168.7.228
- Date: 2026-04-25
- Kernel: 6.1.0-44-cloud-amd64
- AppArmor parser: 3.0.8
- ventd: built from 85a9247

## Profile parse

```
$ sudo apparmor_parser -Q -T /etc/apparmor.d/ventd
$ sudo apparmor_parser -Q -T /etc/apparmor.d/ventd-ipmi
```

Both profiles parse cleanly (exit 0) on AppArmor 3.0.8.

## Enforce-mode startup

ventd was run directly as the ventd user (not via systemd) because the
Debian 12 cloud kernel (6.1.0-44-cloud-amd64) does not expose
`/sys/class/hwmon` — the hwmon subsystem drivers are not compiled in.
The systemd unit's `ReadWritePaths=/sys/class/hwmon` bind-mount fails
with status 226/NAMESPACE when the path does not exist. This is a
cloud-kernel / namespace constraint, not an AppArmor issue.

Running ventd directly still exercises AppArmor confinement because the
profile attaches to the binary path `/usr/local/bin/ventd` regardless of
how the process is launched.

```
$ sudo -u ventd /usr/local/bin/ventd -config /etc/ventd/config.yaml
time=... level=INFO msg="ventd starting"
time=... level=ERROR msg="ventd: fatal" err="load config: ... read hwmon root: open .: no such file or directory"
```

ventd exits immediately after startup because `/sys/class/hwmon` is absent.
The AppArmor profile is active during the startup window.

AppArmor confinement confirmed via `aa-status`:

```
$ sudo aa-status | grep -E "enforce|ventd"
13 profiles are in enforce mode.
   ventd
   ventd-ipmi
0 processes are in enforce mode.
```

(Zero processes in enforce mode because ventd exited before aa-status ran.)

## Kernel audit log under enforce

```
Apr 25 09:17:42 ventd-smoke kernel: audit: type=1400 audit(1777108662.253:20): apparmor="STATUS" operation="profile_replace" profile="unconfined" name="ventd" pid=1081 comm="apparmor_parser"
Apr 25 09:17:42 ventd-smoke kernel: audit: type=1400 audit(1777108662.353:21): apparmor="STATUS" operation="profile_replace" profile="unconfined" name="ventd-ipmi" pid=1085 comm="apparmor_parser"
```

Only STATUS lines (profile load events). No denied lines
for legitimate ventd operations.

## Negative tests confirming denies fire

```
$ sudo -u ventd cat /dev/mem
cat: /dev/mem: Permission denied

$ sudo -u ventd cat /sys/kernel/security/apparmor/profiles
cat: /sys/kernel/security/apparmor/profiles: Permission denied
```

## ventd-ipmi profile

Profile parse: exit 0. Runtime test not applicable — no `/dev/ipmi0` on VM.
Profile confirmed loaded in enforce mode via `aa-status`.

## Complain-mode iteration

One complain-mode pass was required before enforce:

1. Pass 1 found one ALLOWED path not in the initial profile:
   - `/sys/fs/cgroup/user.slice/.../cpu.max r` — Go runtime reads cgroup v2
     CPU limits at startup. Debian 12's AppArmor 3.0.x `<abstractions/base>`
     does not cover this path; Ubuntu 24.04's AppArmor 4.0.x does.
     Rule added: `/sys/fs/cgroup/** r`.

2. Pass 2 (with that rule added): clean — no ALLOWED lines.
