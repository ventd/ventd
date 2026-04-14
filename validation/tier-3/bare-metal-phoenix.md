# Bare-Metal Gate — phoenix@192.168.7.209 (MSI PRO Z690-A)

Host: `phoenix@192.168.7.209`
Kernel: `Linux 6.17.0-20-generic`
Board: MSI PRO Z690-A DDR4 (NCT6687D via OOT `nct6687d`)
Run: 2026-04-14

## DMI snapshot

```
board_vendor="micro-star international co., ltd."
board_name="pro z690-a ddr4(ms-7d25)"
product_name="ms-7d25"
sys_vendor="micro-star international co., ltd."
```

## Probe observations

- **Capability pass result:** hwmon6 = ClassPrimary with 8 PWM channels.
- **Tier 3 proposal (hypothetical):** `no DMI match in seed table`.
- **Tier 3 fires in live setup?** No — capability pass is non-empty so
  `emitDMICandidates` is never called.

## Result: ✅ PASS (gate precondition holds)

Two things proven simultaneously:

1. Production path: Tier 3 is correctly inert on this host because Tier 2
   already delivers 8 controllable fans. No `ComponentDMI` entries land
   in the hwdiag store.
2. Seed narrowness: even if capability pass were hypothetically empty,
   the MSI PRO board would not false-trigger `nct6687d` — the DMI seed
   intentionally scopes to MAG/MPG series because NCT6687D only ships on
   those. Other MSI boards (PRO, Tomahawk non-MAG, etc.) use classic
   Super I/O chips the in-kernel driver handles.

## Note

Phoenix actually does run NCT6687D (the chip matches the MSI-branded part
for this board), but the DMI string doesn't carry the "MAG"/"MPG" marker
that the seed uses. This is fine in the full system: the in-kernel
`nct6683` driver loads, the OOT `nct6687d` driver gets chained in by the
Tier 0.5 autoload path, and capability pass finds `hwmon6` controllable.
Tier 3 is a fallback for when *none* of that works, not a replacement
for it.
