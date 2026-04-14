# Void install-smoke

## Target

Void Linux live ISO (glibc variant), x86_64, runit.

## Method

QEMU/KVM with direct kernel boot (`-kernel` + `-initrd` + explicit
cmdline) against an extracted vmlinuz/initrd from the live ISO. Unix
socket console driven by an `expect` script over `socat`.

## Result — 2026-04-14: BLOCKED (non-ventd)

Two attempts:

1. First boot reached the `root@void-live` shell. `xbps` bootstrap
   required self-update (`xbps-install -u -y xbps`) before `curl` would
   install; the expect script did not run that step, so `curl` was
   absent when the installer pipe ran. No ventd install occurred.
   install.sh post-start verification would have been the next gate but
   was not reached.

2. After updating the expect script, the live ISO landed in dracut's
   emergency shell (`dracut:/#`) — `root=live:CDLABEL=VOID_LIVE` with
   direct kernel boot did not resolve the squashfs rootfs.

No failure mode attributable to ventd observed. The runit code path in
`scripts/install.sh` remains untested end-to-end in VM.

## Indirect evidence

- runit unit installation logic in install.sh is symmetric with OpenRC
  (install unit, create log dir, symlink into runsvdir).
- The `nonvidia` musl variant is identical on Void-musl to Alpine
  (same libc, same build). Alpine smoke passed against v0.1.1.
- Void-glibc uses the same binary Ubuntu/Multipass validates.

## Next step (deferred)

Either: (a) use a full ISOLINUX boot loop via `-bios` + IDE/SATA CD-ROM
attach so the live ISO's own bootloader resolves the rootfs label, or
(b) skip live ISO and install Void to a persistent qcow2 via
`void-installer` in a seeded environment.
