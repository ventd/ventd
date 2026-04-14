# Scenario 3 — Secure Boot / MOK enrollment

Host: `phoenix@192.168.7.224` (VM 201, Debian 12 + UEFI OVMF + Secure Boot,
kernel `6.1.0-44-amd64`).

Design invariant: `/api/hwdiag/mok-enroll` must return **instructions only**
— MOK enrollment requires a reboot and an interactive firmware prompt that
cannot be automated. The endpoint must not `exec` anything.

## Procedure

1. `mokutil --sb-state` → `SecureBoot enabled`.
2. Baseline preflight: `SECURE_BOOT_BLOCKS`.
3. Start daemon in first-boot mode, login, capture session cookie.
4. `POST /api/hwdiag/mok-enroll`.
5. Verify response shape (`kind=instructions`, distro-specific commands)
   and verify no server-side side effects (no file created under
   `/var/lib/shim-signed/mok/`, no mokutil invocation in daemon log).

## Evidence

### Baseline preflight

```json
{
  "detail": "Secure Boot is enabled; the unsigned synthetic module will be refused by the kernel. Enroll a Machine Owner Key (MOK) to sign the module, or disable Secure Boot in firmware.",
  "reason": 3,
  "reason_string": "SECURE_BOOT_BLOCKS"
}
```

### Endpoint response (`POST /api/hwdiag/mok-enroll`)

```json
{
  "kind": "instructions",
  "commands": [
    "sudo apt-get install -y mokutil",
    "sudo mkdir -p /var/lib/shim-signed/mok",
    "sudo openssl req -new -x509 -newkey rsa:2048 -keyout /var/lib/shim-signed/mok/MOK.priv -outform DER -out /var/lib/shim-signed/mok/MOK.der -days 36500 -nodes -subj \"/CN=ventd out-of-tree module signing/\"",
    "sudo mokutil --import /var/lib/shim-signed/mok/MOK.der",
    "# Set a one-time password when prompted.",
    "# Reboot, and at the blue MOK Manager screen choose \"Enroll MOK\"",
    "# → Continue → Yes → enter the password → Reboot.",
    "# After reboot, ventd will sign its module automatically."
  ],
  "detail": "Secure Boot requires every kernel module to be signed by a key the firmware trusts. MOK (Machine Owner Key) enrollment lets you register your own signing key. This must be done interactively at boot time — we cannot automate it. After your MOK is enrolled, re-run the driver install from the setup wizard."
}
```

Debian-specific install command (`sudo apt-get install -y mokutil`) is
correctly selected by `DistroInfo.MOKInstallCommand()` off the `ID=debian`
line of `/etc/os-release`.

### Server-side side-effects check

```
$ which mokutil                # (was already present in base image)
/usr/bin/mokutil
$ ls /var/lib/shim-signed/mok/
ls: cannot access '/var/lib/shim-signed/mok/': No such file or directory
$ grep -i mokutil /tmp/ventd.log
(no output)
```

No file created, no command executed — endpoint returned a payload and
nothing else.

## Outcome

✓ `SECURE_BOOT_BLOCKS` classified correctly. Endpoint behaves per design:
returns `kind=instructions` with distro-appropriate commands and a plain
English `detail`. Does not exec anything server-side. `IDOOTSecureBoot`
remains in the store — correct, because the user has not yet performed
the manual enrollment.
