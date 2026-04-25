# AppArmor smoke — Ubuntu 24.04

- VM: VMID 220 (fc-test-ubuntu-2404-apparmor), IP 192.168.7.229
- Date: 2026-04-25
- Kernel: 6.8.0-106-generic
- AppArmor parser: 4.0.1 (apparmor 4.0.1really4.0.1-0ubuntu0.24.04.6)
- ventd: built from 85a9247

## Profile parse

```
$ apparmor_parser -Q -T /etc/apparmor.d/ventd
$ apparmor_parser -Q -T /etc/apparmor.d/ventd-ipmi
```

Both profiles parse cleanly (exit 0).

## Enforce-mode startup

```
$ systemctl status ventd
● ventd.service - Fan Control Daemon
     Loaded: loaded (/etc/systemd/system/ventd.service; disabled; preset: enabled)
     Active: active (running) since Sat 2026-04-25 09:18:11 UTC; 10s ago
       Docs: https://github.com/ventd/ventd
    Process: 4243 ExecStartPre=/usr/local/sbin/ventd-wait-hwmon (code=exited, status=0/SUCCESS)
   Main PID: 4244 (ventd)
      Tasks: 8 (limit: 2315)
     Memory: 2.3M (peak: 2.8M)
        CPU: 25ms
     CGroup: /system.slice/ventd.service
             └─4244 /usr/local/bin/ventd -config /etc/ventd/config.yaml
```

AppArmor confinement:

```
$ cat /proc/4244/attr/current
ventd (enforce)
```

## Kernel audit log under enforce

```
(no output)
```

No denied lines for legitimate ventd operations.

## Negative tests confirming denies fire

```
$ sudo -u ventd cat /dev/mem
cat: /dev/mem: Permission denied

$ sudo -u ventd cat /sys/kernel/security/apparmor/profiles
cat: /sys/kernel/security/apparmor/profiles: Permission denied
```

## ventd-ipmi profile

Profile parse: exit 0. Runtime test not applicable — no `/dev/ipmi0` on VM.
Both profiles confirmed in enforce mode via `aa-status`:

```
13 profiles are in enforce mode.
   ventd
   ventd-ipmi
```

## Complain-mode iteration

Two complain-mode passes were required before enforce:

1. Pass 1 found three ALLOWED paths not in the initial profile:
   - `/dev/tty w` — Go runtime opens controlling tty at startup
   - `/sys/devices/virtual/dmi/id/chassis_type r` — AppArmor resolves symlinks;
     `/sys/class/dmi/id/**` does not cover the real inode at `/sys/devices/virtual/dmi/id/`
   - `/sys/devices/virtual/dmi/id/sys_vendor r` — same as above

2. Pass 2 (with those rules added): clean — no ALLOWED lines.
