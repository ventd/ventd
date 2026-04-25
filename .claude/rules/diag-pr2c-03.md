# RULE-DIAG-PR2C-03: Self-check failure is fatal unless --allow-redaction-failures is passed.

When `SelfCheck` returns a non-nil error, `bundle.Generate` MUST return that error to
the caller and the bundle file MUST be deleted (or never flushed to disk). The CLI
command exits non-zero with a message that names the leaking file(s). The only override
is `--allow-redaction-failures`, which causes the error to be downgraded to a warning
logged to stderr and printed in `REDACTION_REPORT.json` under `"warnings"`. An
undetected or silently-swallowed self-check failure means the user may share a bundle
they believe is redacted when it is not — the exact failure mode the self-check was
designed to prevent.

Bound: internal/diag/redactor/redactor_test.go:TestRuleDiagPR2C_03/self_check_failure_is_fatal
<!-- rulelint:allow-orphan -->
