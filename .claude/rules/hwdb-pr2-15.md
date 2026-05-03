# RULE-HWDB-PR2-15: Board profile `pwm_groups` validates that each entry's channel is non-empty, fans is non-empty, and fan ids are unique.

Schema v1.3 introduces the optional `pwm_groups: [{channel: <pwm-leaf>, fans:
[<fan-id>, ...]}]` field on board profiles. The field exists because R29 §4
found that Phoenix's MSI Z690-A drives Cpu_Fan + Pump_Fan + Sys_Fan_1 +
Sys_Fan_2 with **identical PWM values across all 2479 captured status
samples** — one PWM channel, four fans. Without this grouping data the
v0.5.11+ cost gate computes per-fan loudness independently, missing the
+10·log10(N) energetic-sum penalty that real grouped fans exhibit.

`validateBoardCatalogEntry` rejects:
- An entry whose `channel` is empty / whitespace-only.
- An entry whose `fans` slice is empty (a group with zero fans is meaningless).
- An entry whose `fans` slice contains an empty fan id, OR contains a
  duplicate fan id (the same fan listed twice on the same channel).

A board profile without `pwm_groups` is unchanged in behaviour — the field
is opt-in and defaults to "no grouping known", which means the cost gate
treats each fan independently (the pre-v1.3 behaviour).

The test fixture exercises the happy path (two valid groups load cleanly),
plus three rejection cases (empty channel, empty fans, duplicate fan id).

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_15
