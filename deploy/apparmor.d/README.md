# AppArmor profile — ventd

## Current status: COMPLAIN mode (v0.3.x)

The profile ships in `flags=(complain)` mode. Denials are **logged** to the
kernel audit log (`dmesg` / `journalctl -k`) but the daemon is **not blocked**.
This matches Ubuntu's convention for new profiles that have not yet been
validated across a full hardware matrix.

### Why complain mode?

The v0.3.0 enforce-mode profile was written against a narrow test-rig path
list. On fresh installs it caused two regressions (#459):

1. **TLS cert generation failed** — `tls.crt.tmp` was denied (profile only
   listed `cert.pem.tmp`), so HTTPS fell back to loopback-only HTTP. The URL
   printed by the installer (`https://192.168.x.x:9999`) was unreachable.

2. **hwmon enumeration degraded** — The profile matched a path component named
   literally `hwmon`, but the kernel names directories `hwmon0`, `hwmon1`, etc.
   Reads on `/sys/devices/virtual/thermal/…/hwmon0/name` and NVMe thermal
   paths were denied, causing incomplete fan detection.

### What was fixed (Approach B paths)

The profile now grants the correct paths:

| Gap | Fix |
|---|---|
| `tls.crt.tmp` denied | `/etc/ventd/*.tmp rw` wildcard covers all atomic-write temps |
| `hwmon0` denied (literal `hwmon` pattern) | `hwmon*` pattern matches `hwmon0`…`hwmonN` |
| `/sys/devices/virtual/thermal/**` denied | Explicit read grant added |
| NVML device nodes missing | `/dev/nvidia*` rw added |
| `/proc/cpuinfo`, `/proc/meminfo` missing | Added for runtime diagnostics |

### Switching to enforce mode

Once you have verified there are no unexpected denials in `dmesg` for your
hardware, switch to enforce:

```bash
# Check for audit lines from ventd (should be empty after a clean run):
sudo journalctl -k | grep -i 'ventd.*audit'

# Edit the profile and remove the 'complain' flag:
sudo sed -i 's/flags=(attach_disconnected,complain)/flags=(attach_disconnected)/' \
    /etc/apparmor.d/usr.local.bin.ventd

# Reload in enforce mode:
sudo apparmor_parser -r /etc/apparmor.d/usr.local.bin.ventd

# Confirm:
sudo aa-status | grep ventd
```

If you see new denials after switching to enforce, run `sudo aa-logprof` to
generate suggested rule additions, then open a PR with the extra rules.

### Validation script

`scripts/validate-apparmor-profile.sh` checks syntax and required permissions:

```bash
bash scripts/validate-apparmor-profile.sh
```

It requires `apparmor_parser` for the syntax check; if not installed it skips
that step and only validates the permission grep checks.

### Roadmap

Enforce mode will be re-enabled by default once regression test coverage across
the supported hardware matrix is added (tracked in the test masterplan).
