# RULE-PROBE-10: internal/hwdb/bios_known_bad.go MUST NOT exist.

No per-board BIOS-version denylist is permitted in the hwdb package. A hardcoded
`bios_known_bad.go` or equivalent file would require constant maintenance, create a false
sense of security (list is always incomplete), and couple the probe's refuse decision to
knowledge that the catalog overlay and precondition checks already handle through
`overrides.unsupported` and `experimental:` schema fields. The probe uses read-only hwmon
enumeration for channel discovery and catalog overlay for capability hints; it does not
need a BIOS version allowlist or denylist. The test asserts the file does not exist in the
module tree.

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-10_no_bios_known_bad_file
