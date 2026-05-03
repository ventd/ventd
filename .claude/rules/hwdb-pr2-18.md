# RULE-HWDB-PR2-18: Board profile `chip_probe.hwmon_name` is the hwmon-name-based fallback fingerprint when DMI is absent / "Default string".

Schema v1.3 introduces the optional `chip_probe: {hwmon_name: <string>}` field on
board profiles. R36 §B's mini-PC EC firmware survey identified a class of
hosts — Beelink / Minisforum / GMKtec / AceMagic mini-PCs running IT5570 or
IT8613 EC firmware — whose BIOS authors never populated DMI fields, leaving
`/sys/class/dmi/id/sys_vendor` reading literally `"Default string"`. Without a
hwmon-name-based fallback the matcher cannot bind these hosts to their board
profile and the daemon falls through to the tier-3 chip-family generic, losing
all of the board-specific overrides (PWM groups, conflict-with-userspace lists,
required modprobe args).

`chip_probe.hwmon_name` is the catalog anchor for these boards. The matcher
walks `/sys/class/hwmon/hwmonN/name` (already passed as `chipName` through
`MatchV1`) and binds a chip-probe board profile when the live hwmon name
matches the catalog string (case-insensitive). The match runs as a tier-1.5
pass — after DMI/DT board matches but before the tier-3 chip-family fallback
— so a board with a populated DMI fingerprint always wins over a chip-probe
board that happens to share the same chip family. Confidence is 0.85 (vs.
DMI-tier-1's 0.9) because hwmon-name is a less specific signal: many boards
share the same EC chip.

`validateBoardCatalogEntry` rejects:
- A profile that sets `chip_probe` together with `dmi_fingerprint` and/or
  `dt_fingerprint`. Exactly one fingerprint type is required (extends the
  existing RULE-FINGERPRINT-08 / RULE-SCHEMA-08 exclusivity to three options).
- A profile that sets `chip_probe` with an empty / whitespace-only
  `hwmon_name`. The field is the load-bearing match key; an empty value would
  match nothing.

A board profile without `chip_probe` is unchanged in behaviour — the field is
opt-in and defaults to "no chip-probe fallback", so all 16 existing v1.3
catalog entries pass through without modification.

The test fixture exercises the happy path (chip-probe match returns
Tier=Board, Confidence=0.85), the case-insensitive match path, the no-match
fall-through (tier-3 chip-family path is reached), plus the two validator
rejection cases.

Bound: internal/hwdb/profile_v1_1_test.go:TestRuleHwdbPR2_18
