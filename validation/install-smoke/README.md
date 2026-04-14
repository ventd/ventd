# Install Smoke Validation — v0.1.0

Date: 2026-04-14
Release: https://github.com/ventd/ventd/releases/tag/v0.1.0

## What this covers

End-to-end artifact plumbing for the README one-liner:

```bash
curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh | sudo bash
```

Specifically the non-privileged slice — tag-redirect resolution,
asset download, checksum verification, tarball layout — which is
what previously broke under `draft: true`. The privileged slice
(`install -m 755`, `systemctl enable`, `systemctl start`) must be
re-validated on a clean VM by a human; see "Open items" below.

## Gate checks performed on this host

| Check | Result |
|---|---|
| `curl -sSLI /releases/latest` resolves to `/releases/tag/v0.1.0` | ✓ |
| `ventd_0.1.0_linux_amd64.tar.gz` downloads (200) | ✓ |
| `checksums.txt` downloads (200) | ✓ |
| `sha256sum --ignore-missing -c checksums.txt` | ✓ OK |
| Tarball extracts cleanly | ✓ |
| `scripts/` + `deploy/` layout matches `find_unit` candidates | ✓ (scripts/install.sh, scripts/ventd.openrc, scripts/ventd.runit, deploy/ventd.service) |
| `bash -n scripts/install.sh` | ✓ |
| `./ventd --help` exits cleanly | ✓ |

Binary is dynamically linked (expected — purego `dlopen` forces
`DT_NEEDED` entries for `libdl`/`libpthread`/`libc` per
HARDWARE-TODO.md Alpine note).

## Release assets

```
checksums.txt
ventd_0.1.0_linux_amd64.deb
ventd_0.1.0_linux_amd64.rpm
ventd_0.1.0_linux_amd64.tar.gz
ventd_0.1.0_linux_arm64.deb
ventd_0.1.0_linux_arm64.rpm
ventd_0.1.0_linux_arm64.tar.gz
```

## Open items (clean VM required)

Needs to be run on a fresh VM by a human before the README one-liner
can be claimed as proven end-to-end:

1. `curl -sSL .../install.sh | sudo bash` on a fresh Ubuntu/Debian
   with no prior ventd state — confirm:
   - `/usr/local/bin/ventd` installed.
   - `/etc/systemd/system/ventd.service` installed + enabled + active
     (`systemctl is-active ventd` → `active`).
   - Daemon listening on `:9999`.
   - Setup token visible in `journalctl -u ventd -n 50`.
   - Post-install output is the single `ventd installed. Open ...` line.
2. Same on Alpine (musl + OpenRC) — verify `rc-service ventd status`.
3. Same on a runit host (Void) — verify symlink auto-start.

Record each run's output into `install-smoke-<distro>.md` when the
clean-VM environment is available.

## Why the install script isn't exercised fully here

Running `./install.sh` on the host that builds and tests the project
would pollute `/usr/local/bin/ventd`, `/etc/ventd/`, and
`/etc/systemd/system/ventd.service` with a tagged release binary and
disrupt development. The non-privileged half (download + verify +
extract + find_unit path resolution) is what the `draft: true` bug
broke and what the new script specifically fixes; that half is
validated above.
