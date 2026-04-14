# Troubleshooting

Common issues and how to diagnose them. Most problems surface in the web UI with a specific diagnostic and a one-click fix button. If something is wrong and the UI is not showing it, start here.

## Web UI will not load

**Check the daemon is running:**

```
systemctl status ventd
```

Should show `active (running)`. If not:

```
sudo journalctl -u ventd -n 50
```

The last error is usually the cause.

**Check the port is listening:**

```
ss -ltnp | grep 9999
```

If nothing is listening, the daemon failed to bind. Common cause: another service already on port 9999. Change `web.listen` in `/etc/ventd/config.yaml`.

**Check the firewall:**

```
sudo ufw status         # Debian/Ubuntu
sudo firewall-cmd --list-all   # Fedora/RHEL
```

Allow port 9999 from the local network if you want remote access.

## "No controllable fans detected"

`ventd` walked the hwmon tree and found no writable PWM controls. Causes, in order of likelihood:

1. **BIOS is managing fans directly.** Enter BIOS and set fan control to "Software" or "PWM manual" instead of "Auto" or "Smart".
2. **Out-of-tree driver missing.** If you have a Nuvoton NCT6687D (common on MSI MEG / MPG boards) or certain newer ITE chips, `ventd` will show a diagnostic offering the DKMS install. See [hardware.md](hardware.md).
3. **Secure Boot blocking out-of-tree module.** Disable Secure Boot, or enroll a MOK for the installed module. `ventd` detects this and walks you through MOK enrollment.
4. **Kernel version mismatch.** Freshly installed kernel with no matching headers. Install headers: `sudo apt install linux-headers-$(uname -r)` (Debian/Ubuntu equivalents elsewhere).

## Fan spins at full speed and will not slow down

This almost always means `pwm_enable` is not in manual mode. `ventd` sets `pwm_enable=1` before writing, but some chips reject the write, or a concurrent program (fancontrol, TLP, vendor software) is resetting it.

**Check for conflicts:**

```
systemctl list-units | grep -iE 'fan|thermal|cool'
```

Common offenders: `fancontrol.service` (lm-sensors), `thermald.service` (Intel), `tlp.service` (laptop), `nbfc_service` (NoteBook FanControl). Disable the conflicting service:

```
sudo systemctl stop fancontrol
sudo systemctl disable fancontrol
```

**Check `pwm_enable` current value:**

```
cat /sys/class/hwmon/hwmon*/pwm*_enable
```

`1` = manual, `2`–`5` = firmware/automatic. `ventd` wants `1`.

## Fan stops and will not restart

You hit a case where the PWM `ventd` wrote was too low for the fan to keep spinning (the fan's `start_pwm` from calibration is higher than its `stop_pwm`, and the curve happened to request a value between the two). If calibration learned the wrong thresholds, the fix is to recalibrate.

**Quick recovery:**

```
sudo systemctl restart ventd
```

Restart re-runs the watchdog's registration pass, which either restores `pwm_enable` to its pre-daemon value (typically firmware auto-control) or writes `PWM=255` as a fallback — both kick the fan back into motion.

**Permanent fix** — recalibrate that fan from the web UI so `ventd` learns realistic start/stop thresholds:

Setup → Fans → select the fan → Recalibrate.

## Calibration hangs or times out

**Calibration is designed to survive browser disconnect.** Closing the tab does not stop it. Reopen the web UI at `http://<your-ip>:9999` and the Setup or Fans page shows the current progress — the `/api/calibrate/status` and `/api/setup/status` endpoints drive that view (both require an authenticated session, so `curl` without a session cookie returns 401).

If the fan RPM stays at 0 at every PWM step, calibration marks it as "no fan connected" and moves on. If the daemon is truly stuck (no progress for more than 60 s on a single step), hit the abort button in the web UI, then:

```
sudo systemctl restart ventd
```

`ventd` writes a crash-safe checkpoint after every sweep step, so after a restart it resumes the interrupted fan from the last completed PWM rather than starting over.

## NVIDIA GPU fan not controllable

NVML fan write requires root and may silently fail on some driver/card combinations.

**Enable persistence mode:**

```
sudo nvidia-smi -pm 1
```

Then restart `ventd`. If still failing, it is a driver limitation — monitoring still works, control does not. Fall back to the vendor fan curve via `nvidia-settings` or set a fixed curve in BIOS.

## Web UI asks for a setup token and I cannot find it

The token is deliberately **not** in the journal. `ventd` writes it to `/run/ventd/setup-token` (0600, root-only) and, when a controlling TTY is attached, prints it there too.

```
sudo cat /run/ventd/setup-token
```

If that file does not exist, check whether the daemon ever entered first-boot mode:

```
sudo journalctl -u ventd | grep -F 'first-boot'
```

You want to see `first-boot: setup pending` followed by `first-boot: setup token written`. If neither line appears, the daemon did not enter first-boot mode. Likely causes:

- A config already exists at `/etc/ventd/config.yaml`. Move it aside and restart:
  ```
  sudo mv /etc/ventd/config.yaml /etc/ventd/config.yaml.old
  sudo systemctl restart ventd
  ```
- The daemon crashed before reaching first-boot setup. Check `journalctl -u ventd` for the panic trace.

## Forgot the web UI password

Wipe the password hash from the config; `ventd` will mint a new setup token on restart:

```
sudo sed -i '/^  password_hash:/d' /etc/ventd/config.yaml
sudo systemctl restart ventd
sudo cat /run/ventd/setup-token
```

## Alpine: "Error loading shared library"

Alpine's musl loader does not provide the glibc-compatible shims that `ventd`'s runtime NVML dlopen needs. Install `gcompat`:

```
sudo apk add gcompat libc6-compat
```

If you have no NVIDIA GPU, `ventd` silently skips NVML — install of `gcompat` is still required if your build includes the loader path.

## Secure Boot: module refuses to load

After `ventd` builds an out-of-tree module (NCT6687D, IT87 fork), Secure Boot requires it be signed with a key the system trusts. `ventd` detects this and shows a MOK enrollment diagnostic:

1. Generate a MOK key (one-time): `ventd` writes it to `/var/lib/shim-signed/mok/`
2. Enroll it on next reboot: enter the BIOS shim prompt and confirm the new key
3. Reboot. The module now loads.

If you skipped the enrollment prompt, re-trigger it:

```
sudo mokutil --import /var/lib/shim-signed/mok/MOK.der
```

Reboot and accept.

## Daemon crashed; fans left at max

On any graceful exit (`SIGTERM`, `SIGINT`, or a recovered panic) the watchdog first tries to restore each fan's `pwm_enable` to the value it had before the daemon started — usually `2` (firmware/automatic control). Only if that restore fails, or the original mode could not be read at startup, does it fall back to `PWM=255` (full speed). A loud fan is always preferable to a stopped fan on a hot chip. Restart the daemon to return to normal curves:

```
sudo systemctl restart ventd
```

If this happens repeatedly, the daemon is crashing — check `journalctl -u ventd` for the panic trace and open an issue.

## Getting more diagnostic info

For any issue report, attach:

```
ventd --version
uname -a
cat /etc/os-release
sensors
ls -la /sys/class/hwmon/*
sudo journalctl -u ventd -n 500 --no-pager
```

Redact any personal hostnames or IPs before posting publicly.
