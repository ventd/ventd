# RULE-HWDB-PR2-17: Driver profile `kernel_version: {min, max}` requires dotted-numeric strings and `Min <= Max` when both set.

Schema v1.3 introduces the optional `kernel_version: {min: "X.Y", max: "X.Y"}`
field on driver profiles. R36's per-row analysis identified eight catalog rows
that gate on a specific kernel-version window — e.g. `it87` quirks landed in
6.2 (`ignore_resource_conflict=1` + `mmio=off`), MS-01 mainline support
landed in 5.14 (NCT6798D), Strix Halo support landed in 6.13. Without this
gate, ventd would attempt drivers on kernels that can't bind them, wasting
cycles + producing misleading recovery cards.

`validateDriverProfile` rejects:
- A non-empty `min` or `max` that is not a dotted-numeric string. Valid:
  `"6.2"`, `"6.13.4"`, `"1"`. Invalid: `"6.2.x"`, `"v6.2"`, `"latest"`.
- A range where `min` > `max` (numeric comparison, not lexicographic — so
  `"6.10"` is treated as 6.10, not 6.1.0; `"6.10"` > `"6.9"` correctly).

Both `min` and `max` are optional; either or both may be empty. An absent
field block leaves the driver kernel-version-agnostic (pre-v1.3 behaviour).

The validator uses a custom dotted-numeric comparator (not strings.Compare)
so `"6.10"` correctly orders after `"6.9"` — lexicographic comparison would
otherwise put `"6.10"` before `"6.9"` and a `min: "6.9"` / `max: "6.10"`
range would falsely fail the `Min <= Max` check.

The test fixture exercises the happy path (valid range + min-only) plus
three rejection cases (non-numeric min, inverted range, lex-vs-numeric
ordering check at `6.9` / `6.10`).

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_17
