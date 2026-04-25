# AppArmor profiles — ventd

Two profiles ship in this directory:

- **`ventd`** — main daemon (`/usr/local/bin/ventd`, `/usr/bin/ventd`)
- **`ventd-ipmi`** — IPMI sidecar (`/usr/libexec/ventd/ventd-ipmi`)

## Shipping mode

Both profiles ship in **enforce** mode. Denials are blocked, not merely
logged. This is the correct production setting for v0.4.1+.

The previous README claimed "complain mode (v0.3.x)". That was accurate
for v0.3.0 but stale by v0.4.0. The regression (#459) that caused the
stale claim has been fixed; the profiles now pass enforce-mode HIL on
Ubuntu 24.04 and Debian 12.

## Switching to complain mode (debugging)

If ventd fails to start after install and you suspect an AppArmor denial:

```bash
# Switch to complain mode (logs denials, does not block)
sudo aa-complain /etc/apparmor.d/ventd
sudo aa-complain /etc/apparmor.d/ventd-ipmi

# Restart and watch kernel audit log
sudo systemctl restart ventd
sudo journalctl -k | grep apparmor
```

## Restoring enforce mode

```bash
sudo aa-enforce /etc/apparmor.d/ventd
sudo aa-enforce /etc/apparmor.d/ventd-ipmi
sudo systemctl restart ventd
```

## Bug report: attach audit log

When filing a bug for an AppArmor denial, attach:

```bash
journalctl -k | grep apparmor
cat /proc/$(pidof ventd)/attr/current
```

Historical context: issues #459, #202, #204, #211.
