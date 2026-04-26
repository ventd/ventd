# RULE-HWDB-CAPTURE-03: A captured profile YAML never contains a field outside the schema v1.0 allowlist.

`Anonymise()` enforces this via a strict YAML round-trip: after clearing user-set text
fields and applying text-level redaction, the profile is marshalled to YAML and decoded back
using `yaml.NewDecoder` with `KnownFields(true)`. Any field not present in the `Profile`
struct causes the decode to fail, which surfaces as a non-nil error (fail-closed per
RULE-HWDB-CAPTURE-02). The test verifies that a profile written by `Capture()` is accepted
without error by `Load()` — which also uses `KnownFields(true)` — and that the resulting
profile has exactly one entry with a valid schema_version, contributed_by="anonymous", and
verified=false. This invariant prevents schema drift: a future refactor that adds an
untagged struct field cannot silently produce files that fail on load.

Bound: internal/hwdb/capture_test.go:TestRuleHwdbCapture_03_AllowlistedFieldsOnly
