# Scenario 1 — Missing kernel headers

Host: `phoenix@192.168.7.208` (VM 207, Ubuntu 24.04 LTS, apt/systemd).
Running kernel: `6.8.0-110-generic`.

## Procedure

1. `qm rollback 207 clean && qm start 207` on Proxmox host.
2. `scp /tmp/ventd-test /tmp/preflight-check phoenix@192.168.7.208:/tmp/`.
3. Baseline preflight: `DKMS_MISSING` (build dir present, dkms absent).
4. `sudo apt-get purge -y linux-headers-$(uname -r) linux-headers-generic linux-headers-6.8.0-106`.
5. Post-purge preflight: `KERNEL_HEADERS_MISSING`.
6. Start daemon in first-boot mode, capture setup token from stdout.
7. `POST /login` with `setup_token=…&new_password=testtesttest` → session cookie.
8. `POST /api/hwdiag/install-kernel-headers`.
9. Verify response, verify dpkg/build-dir restored, verify preflight cleared.

## Evidence

### Baseline `preflight-check`

```json
{
  "detail": "DKMS is not installed. Without it the synthetic module will need to be rebuilt manually after every kernel update.",
  "reason": 2,
  "reason_string": "DKMS_MISSING"
}
```

### After `apt-get purge linux-headers-*`

```json
{
  "detail": "Kernel headers for 6.8.0-110-generic are not installed. They are required to build the synthetic module.",
  "reason": 1,
  "reason_string": "KERNEL_HEADERS_MISSING"
}
```

`ls -d /lib/modules/6.8.0-110-generic/build` → `No such file or directory`.

### Endpoint response

```json
{"kind":"install_log","success":true,"log":["Installing kernel headers for 6.8.0-110-generic..."]}
```

### Post-install target state

```
$ ls -d /lib/modules/$(uname -r)/build
/lib/modules/6.8.0-110-generic/build
$ dpkg -l linux-headers-6.8.0-110-generic
ii  linux-headers-6.8.0-110-generic 6.8.0-110.110 amd64  Linux kernel headers for version 6.8.0 on 64 bit x86 SMP
$ /tmp/preflight-check
{
  "reason_string": "DKMS_MISSING"   // reverted to baseline — headers fix confirmed in isolation
}
```

### Daemon log (ventd-test stdout, first-boot)

```
ventd starting
no config found, starting in first-boot mode path=/etc/ventd/config.yaml
  Ventd — First Boot
  Setup token: ac6e0-bef5e-8dc94
no hwmon PWM channels found, probing kernel modules
installing lm-sensors package_manager=apt-get
no hwmon module produced PWM channels — setup wizard will check for required drivers
NVIDIA driver not detected; GPU features disabled
web: server listening addr=http://0.0.0.0:9999
```

## Outcome

✓ `KERNEL_HEADERS_MISSING` classified correctly. Endpoint installs the
distro headers package, returns `{"kind":"install_log","success":true}`,
and `hwdiag.Store.Remove(IDOOTKernelHeadersMissing)` fires (no-op on this
VM because no Super I/O → no emitter; the Remove is exercised by the
`internal/setup/preflight_diag_test.go::TestEmitPreflightDiag` unit test
combined with the `runInstallHandler` clear-on-success code path inspected
in `internal/web/server.go:705`).
