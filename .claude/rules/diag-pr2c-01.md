# RULE-DIAG-PR2C-01: Default redaction profile is default-conservative.

When `ventd diag bundle` is invoked without a `--redact` flag, the redaction profile
MUST be `default-conservative`. The CLI's zero-value profile selector must resolve to
`default-conservative` without any additional configuration. A bundle produced with the
default profile must have `"redactor_profile": "default-conservative"` in its
`REDACTION_REPORT.json`. This prevents accidental production of un-redacted bundles when
users run the command without reading the help text — the sosreport failure mode of
opt-in redaction causing public disclosure.

Bound: internal/diag/redactor/redactor_test.go:TestRuleDiagPR2C_01/default_profile_is_conservative
<!-- rulelint:allow-orphan -->
