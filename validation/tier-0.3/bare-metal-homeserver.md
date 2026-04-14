# Tier 0.3 Bare-Metal Smoke — root@192.168.7.10

Host: Home server (Gigabyte B550M AORUS PRO, IT8688, kernel 6.17.13-2-pve)
Date: 2026-04-14 (smoke script captured; repeat after next deploy)

## Commands

```bash
ssh root@192.168.7.10 modprobe -r it87
sleep 10
ssh root@192.168.7.10 'modprobe it87 force_id=0x8688'

curl -s -b /tmp/ventd.cookie http://192.168.7.10:9999/api/hwdiag \
  | jq '.entries[] | select(.component=="hardware")'
```

## Expected sequence

1. `modprobe -r it87` → within 2s, uevent fires; after debounce elapses:
   one diagnostic with `context.action = "removed"` for the
   `/sys/devices/platform/it87.XYZ` stable device, `summary` naming the
   IT8688 chip.
2. `modprobe it87 ...` → same ID replaced with `action = "added"`.

The IT87 fork emits remove+add on reload; the 2s debounce collapses a
fast reload into a single `changed` diagnostic when it completes inside
the window — acceptable behaviour; the UI message stays "Re-run setup".

## Debounce proof

```bash
REV_BEFORE=$(curl -s -b /tmp/ventd.cookie http://192.168.7.10:9999/api/hwdiag | jq .revision)
ssh root@192.168.7.10 'modprobe -r it87 && modprobe it87 force_id=0x8688'
sleep 5
REV_AFTER=$(curl -s -b /tmp/ventd.cookie http://192.168.7.10:9999/api/hwdiag | jq .revision)
[ "$REV_BEFORE" = "$REV_AFTER" ] && echo DEBOUNCE_OK || echo DEBOUNCE_FAIL
```

## Periodic-only mode

Same as phoenix: `Environment=VENTD_DISABLE_UEVENT=1`, restart, induce
change, confirm diagnostic inside 5 minutes.
