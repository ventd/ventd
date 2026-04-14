# Tier 0.3 Bare-Metal Smoke — phoenix@192.168.7.209

Host: Alex PC (MSI PRO Z690-A DDR4, NCT6687D, kernel 6.17.0-20-generic)
Date: 2026-04-14 (smoke script captured; repeat after next deploy)

## Commands

```bash
# Baseline snapshot, then hot-plug the fan chip driver.
ssh phoenix@192.168.7.209 sudo modprobe -r nct6687d
sleep 10
ssh phoenix@192.168.7.209 sudo modprobe nct6687d

# Diagnostic capture (needs an authenticated cookie in prod; substitute your own):
curl -s -b /tmp/ventd.cookie http://192.168.7.209:9999/api/hwdiag \
  | jq '.entries[] | select(.id=="hardware.topology_changed")'
```

## Expected diagnostic

```json
{
  "id": "hardware.topology_changed",
  "component": "hardware",
  "severity": "info",
  "summary": "Hardware removed: nct6687d (nct6687.2608)",
  "detail": "Ventd detected a change in the hardware it manages...",
  "remediation": {
    "auto_fix_id": "RERUN_SETUP",
    "label": "Re-run setup",
    "endpoint": "/api/setup/start"
  },
  "affected": ["/sys/devices/platform/nct6687.2608"],
  "context": {
    "action": "removed",
    "stable_device": "/sys/devices/platform/nct6687.2608",
    "previous": { "chip_name": "nct6687d", "class": "primary", "bases": [...] }
  }
}
```

Followed ~2s after `modprobe nct6687d` by an `"action": "added"` entry
(the same ID is idempotent — the second emit replaces the first).

## Debounce proof

```bash
# Flap faster than the 2s window — must produce zero new diagnostics.
REV_BEFORE=$(curl -s -b /tmp/ventd.cookie http://192.168.7.209:9999/api/hwdiag | jq .revision)
ssh phoenix@192.168.7.209 'sudo modprobe -r nct6687d && sudo modprobe nct6687d'
sleep 5
REV_AFTER=$(curl -s -b /tmp/ventd.cookie http://192.168.7.209:9999/api/hwdiag | jq .revision)
[ "$REV_BEFORE" = "$REV_AFTER" ] && echo DEBOUNCE_OK || echo DEBOUNCE_FAIL
```

## Periodic-only mode

```bash
sudo systemctl edit ventd --full   # add: Environment=VENTD_DISABLE_UEVENT=1
sudo systemctl restart ventd
# Induce change, wait <=5min, confirm diagnostic shows up.
```
