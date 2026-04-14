# Alpine install-smoke

## Target

Alpine Linux 3.20 cloud image (genericcloud qcow2), x86_64, musl libc, OpenRC.

## Method

QEMU/KVM with cloud-init NoCloud seed ISO. `runcmd` pipes
`install.sh` from GitHub `main`, then records binary metadata and
attempts exec. Serial log captured to file.

## Result — v0.1.1 (2026-04-14): PASS

- install.sh detected musl via `/lib/ld-musl-*.so.1` and selected
  `ventd_0.1.1_linux_amd64_musl.tar.gz`.
- Checksum verification succeeded.
- Binary on disk: `ELF 64-bit LSB executable, x86-64, statically linked`.
- OpenRC registration and start: `* Starting ventd ... [ ok ]`.
- `install.sh` post-start verification passed (service active after 3s).
- Binary exec on Alpine: succeeds (prints usage when given unknown flag).

## Result — v0.1.0 (pre-fix): FAIL

- install.sh fetched the default glibc-linked tarball.
- `ldd` reported glibc SONAMEs (libc.so.6, libdl.so.2, libpthread.so.0).
- Binary exec returned ENOENT on `/lib64/ld-linux-x86-64.so.2`.
- Root cause: `github.com/ebitengine/purego/internal/fakecgo` injects
  glibc NEEDED entries even with `CGO_ENABLED=0`.

## Fix

Commit `742de43` adds a `nonvidia` build tag that compiles out the
purego-backed NVML implementation and replaces it with a pure-Go stub.
`.goreleaser.yml` now produces a second archive variant suffixed
`_musl` built with `-tags nonvidia`. `scripts/install.sh` selects that
variant when musl is detected.

## Reproducer

```
cd ~/ventd-smoke
cp alpine-cloud.qcow2 alpine-disk.qcow2
qemu-img resize alpine-disk.qcow2 4G
sudo systemd-run --unit=ventd-smoke-alpine --service-type=simple \
    --working-directory=$PWD \
    qemu-system-x86_64 -enable-kvm -m 1024 -smp 2 -machine q35 \
      -drive file=$PWD/alpine-disk.qcow2,if=virtio,format=qcow2 \
      -drive file=$PWD/seed-alpine.iso,if=virtio,format=raw,readonly=on \
      -netdev user,id=n0 -device virtio-net-pci,netdev=n0 \
      -serial file:$PWD/alpine-serial.log \
      -display none -monitor none
# wait for ===SMOKE:END=== in alpine-serial.log
```
