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

Release assets are versioned (`ventd_<version>_linux_<arch>.deb`), so
substitute the current tag — there is no version-less `latest/download`
alias for the package files:

```
VER=1.2.0   # current release tag, without the leading v
wget https://github.com/ventd/ventd/releases/download/v${VER}/ventd_${VER}_linux_amd64.deb
sudo dpkg -i ventd_${VER}_linux_amd64.deb
```

## Fedora / RHEL / openSUSE (.rpm)

```
VER=1.2.0   # current release tag, without the leading v
sudo rpm -i https://github.com/ventd/ventd/releases/download/v${VER}/ventd_${VER}_linux_amd64.rpm
```

## Arch Linux (AUR)

AUR packages are **not published yet** — distro-native packaging
(Copr / PPA / AUR / OBS) is tracked as [issue #1307](https://github.com/ventd/ventd/issues/1307).
Two PKGBUILDs are staged in the repo under
[`packaging/aur/`](https://github.com/ventd/ventd/tree/main/packaging/aur)
and will be pushed to the AUR once that lands:

- `ventd-bin` — installs the official pre-built release binary for amd64 / arm64. Intended as the package most Arch users should install.
- `ventd` — builds from the release source tarball with the Go toolchain, for users who prefer an audited source build.

Until they are published, build from the staged PKGBUILD directly:

```
git clone https://github.com/ventd/ventd
cd ventd/packaging/aur/ventd-bin
makepkg -si
```

The PKGBUILD ships `sha256sums=('SKIP')` in-repo on purpose; if you want
the source verified, edit the checksum in with `updpkgsums` before
building. The package does not enable or start the service automatically
(Arch convention). Enable it yourself once install finishes:

```
sudo systemctl enable --now ventd.service
```

## Alpine

Alpine users need the `gcompat` package to provide glibc loader shims for runtime NVML loading:

```
apk add gcompat libc6-compat   # only needed if you use NVIDIA NVML
VER=1.2.0   # current release tag, without the leading v
wget https://github.com/ventd/ventd/releases/download/v${VER}/ventd_${VER}_linux_amd64_musl.tar.gz
tar -xzf ventd_${VER}_linux_amd64_musl.tar.gz
doas install -m 0755 ventd /usr/local/bin/ventd
```

Use the `_musl` tarball on Alpine; `gcompat` is only required for
runtime NVML loading (NVIDIA GPU fan control), not for the daemon itself.

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

The install script installs a companion uninstaller at
`/usr/local/sbin/ventd-uninstall`. Use it rather than removing files by
hand — a manual `rm` of the binary and unit leaves `ventd-recover.service`
enabled, and it then fires its `OnFailure` recovery logic at every boot
against a binary that no longer exists.

```
sudo /usr/local/sbin/ventd-uninstall
```

Every step is idempotent. In order, the uninstaller:

1. Disables and stops `ventd.service`, `ventd-recover.service`, and
   `ventd-postreboot-verify.service`.
2. `rmmod`s any out-of-tree driver ventd installed under
   `/lib/modules/<release>/extra/`, removes its DKMS registration, and
   deletes the `/etc/modules-load.d/ventd.conf` entry.
3. Removes the systemd unit files + drop-ins from both
   `/etc/systemd/system/` and `/usr/lib/systemd/system/`, then
   `daemon-reload`s.
4. Removes the binary and every helper (`ventd`, `ventd-nvml-helper`,
   `ventd-recover`, `ventd-wait-hwmon`, `ventd-postreboot-verify.sh`)
   from `/usr/local/bin` and `/usr/bin`.
5. Removes the udev rule (`90-ventd-hwmon.rules`) and reloads, plus the
   polkit rule (`50-ventd-update.rules`).
6. Unloads (`apparmor_parser -R`) and removes the AppArmor profiles.
7. Removes `/etc/ventd/` (config + auth).
8. Removes `/var/lib/ventd/` (calibration + smart-mode state) — **unless
   you pass `--keep-data`**.
9. Removes `/var/log/ventd/`.

To keep your learned smart-mode state and calibration for a future
reinstall:

```
sudo /usr/local/sbin/ventd-uninstall --keep-data
```

If you installed from a `.deb` or `.rpm`, uninstall through your package
manager instead. `sudo apt remove ventd` / `sudo dnf remove ventd`
preserves `/var/lib/ventd/` so a reinstall resumes your state;
`sudo apt purge ventd` (or a full `rpm -e`) runs the package
`postremove` hook that wipes all ventd-managed state for a clean slate.

## First boot

On first start with no admin password configured, `ventd` runs the
setup wizard on `https://<your-ip>:9999` (self-signed TLS, so your
browser will warn — accept it). There is no setup token to copy from a
file: issue #765 removed that gate. Instead, the first LAN client to
reach the wizard sees a "Create your password" page, and the account it
creates becomes the local admin. Once that password is set, normal
session-cookie auth applies to every subsequent request.

The journal logs that the daemon reached first-boot state without ever
writing a credential to disk in cleartext — the password hash goes
exclusively to `/etc/ventd/auth.json` (mode 0600). After you set the
password, the wizard walks you through hardware detection, calibration,
and initial config.

## Verification

After install, confirm the daemon is running:

```
systemctl status ventd
ss -ltnp | grep 9999
```

If the port is listening, open `https://<ip>:9999` and log in.
