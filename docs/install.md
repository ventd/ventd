# Installation

`ventd` is a single static binary. The install script detects your architecture and init system, drops the binary at `/usr/local/bin/ventd`, installs the service file, and starts the daemon.

## One-line install

```
curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh | sudo bash
```

The script prints one thing when it finishes: the URL to open in your browser. Open it, complete the setup wizard, and you are done.

## Supported platforms

| | amd64 | arm64 |
|---|---|---|
| Ubuntu / Debian | yes | yes |
| Fedora / RHEL / CentOS | yes | yes |
| Arch / Manjaro | yes | yes |
| openSUSE | yes | yes |
| Alpine (musl) | yes | yes |
| Void | yes | yes |
| NixOS | via overlay | via overlay |

Init systems supported: systemd, OpenRC, runit.

## Manual install

If you prefer not to pipe curl to bash:

1. Download the release tarball for your architecture from [GitHub Releases](https://github.com/ventd/ventd/releases).
2. Verify the checksum against `checksums.txt`:
   ```
   sha256sum -c checksums.txt --ignore-missing
   ```
3. Extract and install:
   ```
   tar -xzf ventd_*_linux_amd64.tar.gz
   sudo install -m 0755 ventd /usr/local/bin/ventd
   sudo install -m 0644 deploy/ventd.service /etc/systemd/system/ventd.service
   sudo install -d /etc/ventd
   sudo install -m 0644 config.example.yaml /etc/ventd/config.example.yaml
   ```
4. Enable and start:
   ```
   sudo systemctl daemon-reload
   sudo systemctl enable --now ventd
   ```

For OpenRC or runit, use the equivalent init file under `scripts/`.

## Debian / Ubuntu (.deb)

```
wget https://github.com/ventd/ventd/releases/latest/download/ventd_amd64.deb
sudo dpkg -i ventd_amd64.deb
```

## Fedora / RHEL / openSUSE (.rpm)

```
sudo rpm -i https://github.com/ventd/ventd/releases/latest/download/ventd_amd64.rpm
```

## Arch (AUR)

An AUR package is planned. Until then, use the tarball install.

## Alpine

Alpine users need the `gcompat` package to provide glibc loader shims for runtime NVML loading:

```
apk add gcompat libc6-compat
wget https://github.com/ventd/ventd/releases/latest/download/ventd_linux_amd64.tar.gz
tar -xzf ventd_linux_amd64.tar.gz
doas install -m 0755 ventd /usr/local/bin/ventd
```

Then write an OpenRC init script from `scripts/ventd.openrc` into `/etc/init.d/`.

## NixOS

Add the `ventd` derivation from [the overlay](https://github.com/ventd/nixpkgs-overlay) (coming soon) or build from source:

```
git clone https://github.com/ventd/ventd
cd ventd
nix-shell -p go
go build ./cmd/ventd/
```

## What the installer does, in order

1. Detects architecture (`amd64`/`arm64`) and libc (`glibc`/`musl`).
2. Downloads the matching release tarball, verifies SHA-256 against
   `checksums.txt`.
3. Runs the install-environment preflight: `udevadm` reachable,
   `/etc/udev/rules.d/` writable, SELinux/AppArmor enforcement
   state, `/sys/class/hwmon` present.
4. Creates the `ventd` system user/group (nologin shell, no home).
5. Installs the binary to `/usr/local/bin/ventd` and the unit files
   to `/etc/systemd/system/` (or `/etc/init.d/`, `/etc/sv/`).
6. Installs the chip-agnostic udev rule to
   `/etc/udev/rules.d/90-ventd-hwmon.rules` and triggers it so pwm
   files become `g+w` to the `ventd` group immediately. **No chip
   identification or rule editing is required** — the rule fires on
   every hwmon device and chgrp's only the `pwm[0-9]*` files
   present.
7. Runs `ventd --probe-modules` once at root, OUTSIDE the daemon's
   sandbox, to load the right Super-I/O driver via `sensors-detect`
   and persist it to `/etc/modules-load.d/ventd.conf` so subsequent
   boots reload it automatically. The long-running daemon never
   modprobes — `ProtectKernelModules=yes` denies it.
8. (Optional) installs the AppArmor profile if `apparmor_parser` is
   present; builds and loads the SELinux module if `semodule` and
   `selinux-policy-devel` are present. Both are best-effort —
   missing tools leave the rest of the install untouched.
9. Enables and starts the unit. The daemon's first journald INFO
   line is the `DiagnoseHwmon` summary: `hwmon: PWM channels visible
   writable=N total=N chips=…`.

## Adding hardware after install

Plug in a new fan, AIO controller, or GPU. The hwmon watcher picks
it up within ten seconds and offers it via the web UI. If the new
device needs a kernel module that wasn't loaded at install time
(unusual: most boards ship a single Super-I/O), re-run the module
probe:

```
sudo ventd --rescan-hwmon
```

This is idempotent — re-running over an already-loaded module is a
fast no-op.

## Uninstall

```
sudo systemctl stop ventd
sudo systemctl disable ventd
sudo rm /usr/local/bin/ventd
sudo rm /etc/systemd/system/ventd.service
sudo systemctl daemon-reload
```

Config files under `/etc/ventd/` are preserved. Delete the directory if you want a clean removal:

```
sudo rm -rf /etc/ventd
```

## First boot

On first start with no config, `ventd` runs the setup wizard on `http://<your-ip>:9999`. It generates a one-time setup token and publishes it in two places, both kept out of journald so the token is not retained in persistent logs:

- `/run/ventd/setup-token` (0600, root-only) — the reliable path under systemd
- The controlling TTY, if one is attached (e.g. when you start the daemon by hand)

Read the token with:

```
sudo cat /run/ventd/setup-token
```

The journal does log `first-boot: setup token written` with the file path — you can use that to confirm the daemon reached first-boot state. Enter the token in the browser and the wizard walks you through hardware detection, calibration, and initial config.

## Verification

After install, confirm the daemon is running:

```
systemctl status ventd
ss -ltnp | grep 9999
```

If the port is listening, open `http://<ip>:9999` and log in.
