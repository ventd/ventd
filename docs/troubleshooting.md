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

You hit a case where `ventd` wrote a PWM below the fan's `start_pwm`. If `allow_stop: false` (the default), this is a bug — report it. If `allow_stop: true`, you opted in to the stop behaviour and need to wait until the temperature rises.

**Quick recovery:**

```
sudo systemctl restart ventd
```

Restart sets `pwm_enable=1` and writes `max_pwm` briefly during re-registration, which kicks the fan back into motion.

**Permanent fix** — recalibrate that fan from the web UI so `ventd` learns the real `start_pwm`:

Setup → Fans → select the fan → Recalibrate.

## Calibration hangs or times out

**Calibration is designed to survive browser disconnect.** Closing the tab does not stop it. Progress is at:

```
curl http://localhost:9999/api/setup/calibrate/status
```

If the fan RPM stays at 0 at every PWM step, calibration marks it as "no fan connected" and moves on. If the daemon is truly stuck (no progress for more than 60 s on a single step), abort and restart:

```
curl -X POST http://localhost:9999/api/setup/calibrate/abort
sudo systemctl restart ventd
```

`ventd` resumes from the last completed fan on restart.

## NVIDIA GPU fan not controllable

NVML fan write requires root and may silently fail on some driver/card combinations.

**Enable persistence mode:**

```
sudo nvidia-smi -pm 1
```

Then restart `ventd`. If still failing, it is a driver limitation — monitoring still works, control does not. Fall back to the vendor fan curve via `nvidia-settings` or set a fixed curve in BIOS.

## Web UI asks for a setup token and I cannot find it

The token is in the journal:

```
sudo journalctl -u ventd | grep -i 'setup token'
```

If nothing matches, the daemon never entered first-boot mode. Likely causes:

- A config already exists at `/etc/ventd/config.yaml`. Move it aside and restart:
  ```
  sudo mv /etc/ventd/config.yaml /etc/ventd/config.yaml.old
  sudo systemctl restart ventd
  ```
- The daemon crashed before printing the token. Check `journalctl -u ventd` for errors.

## Forgot the web UI password

Wipe the password hash from the config; `ventd` will print a new setup token on restart:

```
sudo sed -i '/^  password_hash:/d' /etc/ventd/config.yaml
sudo systemctl restart ventd
sudo journalctl -u ventd | grep 'Setup token'
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

`ventd`'s watchdog writes `PWM=255` (full speed) on any unclean exit as a failsafe — a loud fan is preferable to a stopped fan on a hot chip. This is intentional. Restart the daemon to return to normal curves:

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
