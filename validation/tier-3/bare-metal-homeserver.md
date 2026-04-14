# Bare-Metal Gate — root@192.168.7.10 (Gigabyte B550M AORUS PRO)

Host: `root@192.168.7.10`
Kernel: `Linux 6.17.13-2-pve`
Board: Gigabyte B550M AORUS PRO (IT8688 via in-kernel `it87`)
Run: 2026-04-14

## DMI snapshot

```
board_vendor="gigabyte technology co., ltd."
board_name="b550m aorus pro"
product_name="b550m aorus pro"
sys_vendor="gigabyte technology co., ltd."
```

## Probe observations

- **Capability pass result:** hwmon8 = ClassPrimary with 5 PWM channels.
- **Tier 3 proposal (hypothetical):** `[it8688e]` — Gigabyte vendor trigger matches.
- **Tier 3 fires in live setup?** No — capability pass is non-empty so
  `emitDMICandidates` is never called.

## Result: ✅ PASS (gate precondition holds)

This host demonstrates the other side of the gate: the DMI seed *would*
match (`it8688e` via Gigabyte board_vendor), but because capability pass
succeeds the DMI pathway stays dormant. If the in-kernel `it87` driver
were ever unable to bind on a future kernel, Tier 3 would correctly
surface `it8688e` as the proposed fix — one click to install and modprobe.

## Note

The `it8688` hwmon entry here is the in-kernel driver's native support
for IT8688E on Gigabyte B550 — that chip is fully supported upstream, so
the OOT driver proposal is a fallback rather than a requirement. The
Tier 3 design intentionally covers this: capability pass takes priority
and the DMI seed is consulted only when capability pass has nothing to
promote.
