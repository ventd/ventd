# RULE-DIAG-PR2C-07: REDACTION_REPORT.json is generated for every bundle including --redact=off.

Every bundle MUST contain `REDACTION_REPORT.json` at the tarball root. For
`default-conservative` and `trusted-recipient` profiles, the report contains per-class
redaction counts and `"redaction_consistent": true/false` from the self-check result.
For `--redact=off` bundles, the report MUST still be generated and MUST contain
`"redactor_profile": "off"` and all class counts set to 0. The report is generated after
the self-check pass so it reflects the final consistent state. A bundle without
`REDACTION_REPORT.json` cannot be audited by the user before sharing and is rejected by
the manifest validator. The test fixture verifies that both a default-conservative bundle
and an off-profile bundle each contain a well-formed `REDACTION_REPORT.json`.

Bound: internal/diag/bundle_test.go:TestRuleDiagPR2C_07/redaction_report_always_present
<!-- rulelint:allow-orphan -->
