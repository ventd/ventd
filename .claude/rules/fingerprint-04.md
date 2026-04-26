# RULE-FINGERPRINT-04: Matcher matches DMI `bios_version` glob when field is present on a board profile.

When a `dmi_fingerprint` entry has a non-empty `bios_version` field (e.g. `"GKCN*"`),
the tier-1 board matcher MUST evaluate a glob match between that pattern and the live
`DMIFingerprint.BiosVersion` value. A board entry with `bios_version: "GKCN*"` must
match a live system with `BiosVersion: "GKCN58WW"` and must NOT match a system with
`BiosVersion: "EUCN32WW"`. This field enables Lenovo Legion family dispatch: multiple
Legion generations share the same `product_name` (machine-type code) but differ in their
4-character BIOS family prefix (GKCN, EUCN, H1CN, LPCN, etc.). Without `bios_version`
matching, all generations would collapse to the same board profile regardless of
generation-specific fan quirks.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_BiosVersionGlob_Matches
